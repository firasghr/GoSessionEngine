// Package cluster – gRPC Worker Client.
//
// WorkerClient wraps the generated pb.MasterControllerClient with a
// higher-level API tailored to GoSessionEngine workers:
//
//   - ReportStatus    — one-shot call to report a session lifecycle change.
//   - BroadcastCookie — one-shot call to upload freshly obtained cookies.
//   - GetCookies      — fetch the current Global Cookie Jar snapshot.
//   - WatchCookies    — start a background goroutine that streams cookie
//     updates from the master and calls a handler function on each update.
//
// Each of the 6 PCs creates exactly one WorkerClient (pointing at the master's
// gRPC address) and shares it across all of its local sessions.
package cluster

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/firasghr/GoSessionEngine/cluster/pb"
)

// WorkerClient is the client-side façade for the MasterController gRPC service.
// It is safe for concurrent use by many goroutines.
type WorkerClient struct {
	pcID   string
	conn   *grpc.ClientConn
	client pb.MasterControllerClient
}

// NewWorkerClient dials the master at addr and returns a ready WorkerClient.
// pcID identifies this PC (e.g. "pc-1", "pc-2", …).
//
// The connection uses plain-text gRPC (no TLS) which is appropriate for a
// trusted LAN.  For internet-facing deployments replace insecure.NewCredentials
// with tls.NewClientTLSFromFile or similar.
func NewWorkerClient(pcID, addr string, opts ...grpc.DialOption) (*WorkerClient, error) {
	defaults := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	opts = append(defaults, opts...)

	conn, err := grpc.NewClient(addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("worker client: dial %s: %w", addr, err)
	}
	return &WorkerClient{
		pcID:   pcID,
		conn:   conn,
		client: pb.NewMasterControllerClient(conn),
	}, nil
}

// Close tears down the underlying gRPC connection.
func (w *WorkerClient) Close() error {
	return w.conn.Close()
}

// ReportStatus tells the master about a session lifecycle transition.
// state is one of "idle", "active", "challenge", "closed".
func (w *WorkerClient) ReportStatus(ctx context.Context, sessionID int32, state string) error {
	_, err := w.client.UpdateStatus(ctx, &pb.UpdateStatusRequest{
		Status: &pb.SessionStatus{
			SessionId: sessionID,
			PcId:      w.pcID,
			State:     state,
		},
	})
	if err != nil {
		return fmt.Errorf("worker client: report status session %d: %w", sessionID, err)
	}
	return nil
}

// BroadcastCookie uploads cookies obtained after solving a JS challenge.
// The master will persist them in the Global Cookie Jar and push them to all
// subscribed workers so they can start making authenticated requests
// immediately.
//
// cookies is a slice of standard library *http.Cookie values; they are
// converted to the protobuf representation automatically.
func (w *WorkerClient) BroadcastCookie(ctx context.Context, sessionID int32, cookies []*http.Cookie) error {
	pbCookies := make([]*pb.Cookie, 0, len(cookies))
	for _, c := range cookies {
		var exp int64
		if !c.Expires.IsZero() {
			exp = c.Expires.Unix()
		}
		pbCookies = append(pbCookies, &pb.Cookie{
			Name:        c.Name,
			Value:       c.Value,
			Domain:      c.Domain,
			Path:        c.Path,
			ExpiresUnix: exp,
			Secure:      c.Secure,
			HttpOnly:    c.HttpOnly,
		})
	}

	resp, err := w.client.BroadcastCookie(ctx, &pb.BroadcastCookieRequest{
		PcId:      w.pcID,
		SessionId: sessionID,
		Cookies:   pbCookies,
	})
	if err != nil {
		return fmt.Errorf("worker client: broadcast cookie: %w", err)
	}
	if !resp.Accepted {
		return fmt.Errorf("worker client: broadcast cookie: master rejected")
	}
	return nil
}

// GetCookies fetches a snapshot of the Global Cookie Jar from the master and
// returns it as []*http.Cookie for use with net/http clients.
func (w *WorkerClient) GetCookies(ctx context.Context) ([]*http.Cookie, error) {
	resp, err := w.client.GetGlobalCookies(ctx, &pb.GetGlobalCookiesRequest{PcId: w.pcID})
	if err != nil {
		return nil, fmt.Errorf("worker client: get cookies: %w", err)
	}
	return pbCookiesToHTTP(resp.Cookies), nil
}

// WatchCookies opens a streaming subscription and calls onUpdate every time
// the master pushes a fresh Global Cookie Jar snapshot.  The goroutine exits
// when ctx is cancelled or the stream encounters a non-recoverable error.
//
// This is the primary mechanism by which worker PCs receive cookies the moment
// any PC solves a challenge: PC #1 calls BroadcastCookie → master pushes to
// all subscribers → all other PCs receive the cookies in onUpdate within one
// network round-trip.
//
// onUpdate is called from the background goroutine; if it blocks it will delay
// receipt of subsequent updates.
func (w *WorkerClient) WatchCookies(ctx context.Context, onUpdate func([]*http.Cookie)) error {
	stream, err := w.client.WatchCookies(ctx, &pb.WatchCookiesRequest{PcId: w.pcID})
	if err != nil {
		return fmt.Errorf("worker client: open watch stream: %w", err)
	}

	go func() {
		for {
			resp, err := stream.Recv()
			if err != nil {
				return // context cancelled or server closed stream
			}
			onUpdate(pbCookiesToHTTP(resp.Cookies))
		}
	}()
	return nil
}

// pbCookiesToHTTP converts a slice of protobuf Cookie messages to
// []*http.Cookie, skipping cookies that are already expired.
func pbCookiesToHTTP(pbCookies []*pb.Cookie) []*http.Cookie {
	now := time.Now().Unix()
	out := make([]*http.Cookie, 0, len(pbCookies))
	for _, c := range pbCookies {
		if c.ExpiresUnix > 0 && c.ExpiresUnix < now {
			continue
		}
		hc := &http.Cookie{
			Name:     c.Name,
			Value:    c.Value,
			Domain:   c.Domain,
			Path:     c.Path,
			Secure:   c.Secure,
			HttpOnly: c.HttpOnly,
		}
		if c.ExpiresUnix > 0 {
			hc.Expires = time.Unix(c.ExpiresUnix, 0)
		}
		out = append(out, hc)
	}
	return out
}
