// Package cluster – gRPC Master Controller.
//
// MasterControllerServer is the authoritative coordinator for a 6-PC,
// 2 000-session GoSessionEngine cluster.  It runs as a single gRPC server
// process (typically on PC #1 or a dedicated orchestrator host) and exposes
// four RPCs:
//
//   - BroadcastCookie  — a worker that solved a JS/bot challenge uploads its
//     session cookies; the server stores them in the Global Cookie Jar and
//     fans them out to every active WatchCookies subscriber instantly.
//   - UpdateStatus     — workers report session lifecycle transitions
//     ("idle" → "active" → "challenge" → "closed").
//   - GetGlobalCookies — returns a point-in-time snapshot of the jar.
//   - WatchCookies     — server-streaming RPC; subscribers receive a push
//     every time BroadcastCookie adds new cookies.
//   - GetAllStatus     — returns a snapshot of every tracked session.
//
// Thread-safety:
//   - The Global Cookie Jar is guarded by a sync.RWMutex; reads never block
//     each other so 2 000 workers polling the jar concurrently is safe.
//   - Session state is stored in a sync.Map, eliminating map-lock contention
//     across thousands of goroutines.
//   - Subscriber list is guarded by a separate sync.Mutex; it is only accessed
//     on BroadcastCookie (write) and WatchCookies (connect/disconnect), both
//     of which are infrequent relative to UpdateStatus.
package cluster

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/firasghr/GoSessionEngine/cluster/pb"
)

// ─── Global Cookie Jar ───────────────────────────────────────────────────────

// cookieEntry is one cookie record in the jar.
type cookieEntry struct {
	Cookie    *pb.Cookie
	StoredAt  time.Time
}

// GlobalCookieJar is a thread-safe store for session cookies that have been
// validated by any worker in the cluster.  The jar is keyed by cookie name so
// a later BroadcastCookie with the same name always replaces the older entry.
type GlobalCookieJar struct {
	mu      sync.RWMutex
	entries map[string]cookieEntry
	version atomic.Int64
}

// NewGlobalCookieJar creates an empty jar.
func NewGlobalCookieJar() *GlobalCookieJar {
	return &GlobalCookieJar{entries: make(map[string]cookieEntry)}
}

// Store saves cookies from the broadcast, increments the jar version, and
// returns the new version number.
func (j *GlobalCookieJar) Store(cookies []*pb.Cookie) int64 {
	j.mu.Lock()
	for _, c := range cookies {
		j.entries[c.Name] = cookieEntry{Cookie: c, StoredAt: time.Now()}
	}
	j.mu.Unlock()
	return j.version.Add(1)
}

// Snapshot returns a copy of all cookies and the current version atomically.
func (j *GlobalCookieJar) Snapshot() ([]*pb.Cookie, int64) {
	j.mu.RLock()
	out := make([]*pb.Cookie, 0, len(j.entries))
	for _, e := range j.entries {
		out = append(out, e.Cookie)
	}
	ver := j.version.Load()
	j.mu.RUnlock()
	return out, ver
}

// ToHTTPCookies converts the jar contents to []*http.Cookie for use with
// net/http clients.  Expired cookies (expires_unix > 0 and in the past) are
// omitted.
func (j *GlobalCookieJar) ToHTTPCookies() []*http.Cookie {
	j.mu.RLock()
	defer j.mu.RUnlock()
	now := time.Now().Unix()
	out := make([]*http.Cookie, 0, len(j.entries))
	for _, e := range j.entries {
		c := e.Cookie
		if c.ExpiresUnix > 0 && c.ExpiresUnix < now {
			continue // skip expired
		}
		out = append(out, &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		})
	}
	return out
}

// ─── Subscriber management ───────────────────────────────────────────────────

// subscriber is an active WatchCookies stream.
type subscriber struct {
	pcID string
	ch   chan *pb.GetGlobalCookiesResponse
}

// ─── MasterControllerServer ──────────────────────────────────────────────────

// MasterControllerServer implements pb.MasterControllerServer and acts as the
// cluster-wide session coordinator.
type MasterControllerServer struct {
	pb.UnimplementedMasterControllerServer

	jar *GlobalCookieJar

	// sessions stores *pb.SessionStatus values keyed by session_id (int32).
	sessions sync.Map

	// subscribers holds active WatchCookies streams.
	subMu sync.Mutex
	subs  map[string]*subscriber // keyed by pcID
}

// NewMasterControllerServer creates a ready-to-use server.
func NewMasterControllerServer() *MasterControllerServer {
	return &MasterControllerServer{
		jar:  NewGlobalCookieJar(),
		subs: make(map[string]*subscriber),
	}
}

// BroadcastCookie stores new cookies in the Global Cookie Jar and pushes them
// to every active WatchCookies subscriber.
func (s *MasterControllerServer) BroadcastCookie(
	_ context.Context, req *pb.BroadcastCookieRequest,
) (*pb.BroadcastCookieResponse, error) {
	if len(req.Cookies) == 0 {
		return nil, status.Error(codes.InvalidArgument, "cookies must not be empty")
	}

	ver := s.jar.Store(req.Cookies)
	cookies, _ := s.jar.Snapshot()
	resp := &pb.GetGlobalCookiesResponse{Cookies: cookies, Version: ver}

	s.subMu.Lock()
	for _, sub := range s.subs {
		select {
		case sub.ch <- resp:
		default:
			// Subscriber is slow; drop rather than block BroadcastCookie.
		}
	}
	s.subMu.Unlock()

	return &pb.BroadcastCookieResponse{Accepted: true}, nil
}

// UpdateStatus records the latest lifecycle state for a session.
func (s *MasterControllerServer) UpdateStatus(
	_ context.Context, req *pb.UpdateStatusRequest,
) (*pb.UpdateStatusResponse, error) {
	if req.Status == nil {
		return nil, status.Error(codes.InvalidArgument, "status must not be nil")
	}
	s.sessions.Store(req.Status.SessionId, req.Status)
	return &pb.UpdateStatusResponse{Ok: true}, nil
}

// GetGlobalCookies returns a snapshot of the current Global Cookie Jar.
func (s *MasterControllerServer) GetGlobalCookies(
	_ context.Context, req *pb.GetGlobalCookiesRequest,
) (*pb.GetGlobalCookiesResponse, error) {
	cookies, ver := s.jar.Snapshot()
	return &pb.GetGlobalCookiesResponse{Cookies: cookies, Version: ver}, nil
}

// WatchCookies subscribes the caller to Global Cookie Jar updates.  The stream
// remains open until the client disconnects or the context is cancelled.  A
// snapshot of the current jar is sent immediately so the subscriber is
// up-to-date before the first BroadcastCookie event arrives.
func (s *MasterControllerServer) WatchCookies(
	req *pb.WatchCookiesRequest,
	stream pb.MasterController_WatchCookiesServer,
) error {
	if req.PcId == "" {
		return status.Error(codes.InvalidArgument, "pc_id must not be empty")
	}

	ch := make(chan *pb.GetGlobalCookiesResponse, 32)
	sub := &subscriber{pcID: req.PcId, ch: ch}

	s.subMu.Lock()
	s.subs[req.PcId] = sub
	s.subMu.Unlock()

	defer func() {
		s.subMu.Lock()
		delete(s.subs, req.PcId)
		s.subMu.Unlock()
	}()

	// Send the current snapshot immediately.
	cookies, ver := s.jar.Snapshot()
	if err := stream.Send(&pb.GetGlobalCookiesResponse{Cookies: cookies, Version: ver}); err != nil {
		return fmt.Errorf("watch cookies: send initial snapshot: %w", err)
	}

	// Forward updates until the client disconnects.
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case update := <-ch:
			if err := stream.Send(update); err != nil {
				return fmt.Errorf("watch cookies: send update: %w", err)
			}
		}
	}
}

// GetAllStatus returns a point-in-time snapshot of every tracked session.
func (s *MasterControllerServer) GetAllStatus(
	_ context.Context, _ *pb.GetAllStatusRequest,
) (*pb.GetAllStatusResponse, error) {
	var sessions []*pb.SessionStatus
	s.sessions.Range(func(_, v any) bool {
		if st, ok := v.(*pb.SessionStatus); ok {
			sessions = append(sessions, st)
		}
		return true
	})
	return &pb.GetAllStatusResponse{Sessions: sessions}, nil
}

// Jar exposes the underlying GlobalCookieJar for in-process consumers (e.g.
// tests and monitoring handlers).
func (s *MasterControllerServer) Jar() *GlobalCookieJar { return s.jar }

// ─── Server lifecycle ─────────────────────────────────────────────────────────

// ListenAndServe starts the gRPC server on addr (e.g. ":50051") and blocks
// until the provided context is cancelled.  It closes the listener on return.
func ListenAndServe(ctx context.Context, addr string, opts ...grpc.ServerOption) error {
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("cluster: listen %s: %w", addr, err)
	}

	srv := grpc.NewServer(opts...)
	pb.RegisterMasterControllerServer(srv, NewMasterControllerServer())

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(lis) }()

	select {
	case <-ctx.Done():
		srv.GracefulStop()
		return nil
	case err := <-errCh:
		return fmt.Errorf("cluster: serve: %w", err)
	}
}
