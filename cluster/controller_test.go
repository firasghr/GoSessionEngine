package cluster_test

import (
	"context"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/firasghr/GoSessionEngine/cluster/pb"
	"github.com/firasghr/GoSessionEngine/cluster"
)

// startTestServer spins up a MasterControllerServer on a random localhost port
// and returns the address, the server instance, and a stop function.
func startTestServer(t *testing.T) (addr string, srv *cluster.MasterControllerServer, stop func()) {
	t.Helper()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	grpcSrv := grpc.NewServer()
	srv = cluster.NewMasterControllerServer()
	pb.RegisterMasterControllerServer(grpcSrv, srv)

	go func() { _ = grpcSrv.Serve(lis) }()

	return lis.Addr().String(), srv, func() { grpcSrv.GracefulStop() }
}

// dialTestClient dials addr and returns a pb.MasterControllerClient.
func dialTestClient(t *testing.T, addr string) pb.MasterControllerClient {
	t.Helper()
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewMasterControllerClient(conn)
}

// ─── GlobalCookieJar unit tests ───────────────────────────────────────────────

func TestGlobalCookieJar_StoreAndSnapshot(t *testing.T) {
	jar := cluster.NewGlobalCookieJar()
	cookies, ver := jar.Snapshot()
	if len(cookies) != 0 {
		t.Errorf("fresh jar: expected 0 cookies, got %d", len(cookies))
	}
	if ver != 0 {
		t.Errorf("fresh jar: expected version 0, got %d", ver)
	}

	jar.Store([]*pb.Cookie{
		{Name: "_abck", Value: "abc123", Domain: "example.com", Path: "/"},
	})

	cookies, ver = jar.Snapshot()
	if len(cookies) != 1 {
		t.Errorf("after Store: expected 1 cookie, got %d", len(cookies))
	}
	if ver != 1 {
		t.Errorf("after Store: expected version 1, got %d", ver)
	}
	if cookies[0].Name != "_abck" {
		t.Errorf("cookie name: got %q, want _abck", cookies[0].Name)
	}
}

func TestGlobalCookieJar_StoreUpdatesExisting(t *testing.T) {
	jar := cluster.NewGlobalCookieJar()
	jar.Store([]*pb.Cookie{{Name: "sess", Value: "old"}})
	jar.Store([]*pb.Cookie{{Name: "sess", Value: "new"}})

	cookies, _ := jar.Snapshot()
	if len(cookies) != 1 {
		t.Errorf("expected 1 cookie after update, got %d", len(cookies))
	}
	if cookies[0].Value != "new" {
		t.Errorf("cookie value: got %q, want new", cookies[0].Value)
	}
}

func TestGlobalCookieJar_ToHTTPCookies_SkipsExpired(t *testing.T) {
	jar := cluster.NewGlobalCookieJar()
	jar.Store([]*pb.Cookie{
		{Name: "fresh", Value: "v1", ExpiresUnix: time.Now().Add(time.Hour).Unix()},
		{Name: "expired", Value: "v2", ExpiresUnix: 1}, // epoch = long expired
	})

	hc := jar.ToHTTPCookies()
	if len(hc) != 1 {
		t.Errorf("expected 1 non-expired cookie, got %d", len(hc))
	}
	if hc[0].Name != "fresh" {
		t.Errorf("expected cookie 'fresh', got %q", hc[0].Name)
	}
}

// ─── gRPC BroadcastCookie ─────────────────────────────────────────────────────

func TestBroadcastCookie_Accepted(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()
	c := dialTestClient(t, addr)

	resp, err := c.BroadcastCookie(context.Background(), &pb.BroadcastCookieRequest{
		PcId:      "pc-1",
		SessionId: 0,
		Cookies:   []*pb.Cookie{{Name: "_abck", Value: "test", Domain: "example.com", Path: "/"}},
	})
	if err != nil {
		t.Fatalf("BroadcastCookie: %v", err)
	}
	if !resp.Accepted {
		t.Error("expected Accepted=true")
	}
}

func TestBroadcastCookie_EmptyCookiesRejected(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()
	c := dialTestClient(t, addr)

	_, err := c.BroadcastCookie(context.Background(), &pb.BroadcastCookieRequest{
		PcId:    "pc-1",
		Cookies: nil,
	})
	if err == nil {
		t.Error("expected error for empty cookies")
	}
}

// ─── gRPC UpdateStatus / GetAllStatus ─────────────────────────────────────────

func TestUpdateStatus_and_GetAllStatus(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()
	c := dialTestClient(t, addr)

	_, err := c.UpdateStatus(context.Background(), &pb.UpdateStatusRequest{
		Status: &pb.SessionStatus{
			SessionId: 42,
			PcId:      "pc-3",
			State:     "active",
		},
	})
	if err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	all, err := c.GetAllStatus(context.Background(), &pb.GetAllStatusRequest{})
	if err != nil {
		t.Fatalf("GetAllStatus: %v", err)
	}
	if len(all.Sessions) != 1 {
		t.Errorf("expected 1 session, got %d", len(all.Sessions))
	}
	s := all.Sessions[0]
	if s.SessionId != 42 || s.State != "active" || s.PcId != "pc-3" {
		t.Errorf("unexpected session: %+v", s)
	}
}

// ─── gRPC GetGlobalCookies ─────────────────────────────────────────────────────

func TestGetGlobalCookies_ReturnsJarSnapshot(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()
	c := dialTestClient(t, addr)

	// Broadcast first.
	_, err := c.BroadcastCookie(context.Background(), &pb.BroadcastCookieRequest{
		PcId:    "pc-1",
		Cookies: []*pb.Cookie{{Name: "tok", Value: "xyz"}},
	})
	if err != nil {
		t.Fatalf("BroadcastCookie: %v", err)
	}

	resp, err := c.GetGlobalCookies(context.Background(), &pb.GetGlobalCookiesRequest{PcId: "pc-2"})
	if err != nil {
		t.Fatalf("GetGlobalCookies: %v", err)
	}
	if len(resp.Cookies) != 1 || resp.Cookies[0].Name != "tok" {
		t.Errorf("unexpected cookies: %v", resp.Cookies)
	}
	if resp.Version < 1 {
		t.Errorf("expected version >= 1, got %d", resp.Version)
	}
}

// ─── gRPC WatchCookies streaming ──────────────────────────────────────────────

func TestWatchCookies_ReceivesInitialSnapshot(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()
	c := dialTestClient(t, addr)

	// Pre-populate the jar.
	_, _ = c.BroadcastCookie(context.Background(), &pb.BroadcastCookieRequest{
		PcId:    "pc-1",
		Cookies: []*pb.Cookie{{Name: "init", Value: "v0"}},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	stream, err := c.WatchCookies(ctx, &pb.WatchCookiesRequest{PcId: "pc-2"})
	if err != nil {
		t.Fatalf("WatchCookies: %v", err)
	}

	// The first message should be the current snapshot.
	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv initial snapshot: %v", err)
	}
	if len(msg.Cookies) == 0 {
		t.Error("expected at least one cookie in initial snapshot")
	}
}

func TestWatchCookies_ReceivesBroadcastPush(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()
	c := dialTestClient(t, addr)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	stream, err := c.WatchCookies(ctx, &pb.WatchCookiesRequest{PcId: "pc-5"})
	if err != nil {
		t.Fatalf("WatchCookies: %v", err)
	}

	// Consume initial (empty) snapshot.
	if _, err := stream.Recv(); err != nil {
		t.Fatalf("Recv initial: %v", err)
	}

	// Broadcast from another goroutine.
	go func() {
		time.Sleep(100 * time.Millisecond)
		_, _ = c.BroadcastCookie(context.Background(), &pb.BroadcastCookieRequest{
			PcId:    "pc-1",
			Cookies: []*pb.Cookie{{Name: "pushed", Value: "abc"}},
		})
	}()

	// Second Recv should carry the broadcast.
	msg, err := stream.Recv()
	if err != nil {
		t.Fatalf("Recv push: %v", err)
	}
	found := false
	for _, ck := range msg.Cookies {
		if ck.Name == "pushed" {
			found = true
		}
	}
	if !found {
		t.Errorf("pushed cookie not found in stream message: %v", msg.Cookies)
	}
}

// ─── WorkerClient high-level API ──────────────────────────────────────────────

func TestWorkerClient_BroadcastAndGet(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()

	w, err := cluster.NewWorkerClient("pc-1", addr)
	if err != nil {
		t.Fatalf("NewWorkerClient: %v", err)
	}
	defer w.Close()

	cookies := []*http.Cookie{
		{Name: "_abck", Value: "sentinel", Domain: "example.com", Path: "/",
			Expires: time.Now().Add(time.Hour)},
	}
	if err := w.BroadcastCookie(context.Background(), 0, cookies); err != nil {
		t.Fatalf("BroadcastCookie: %v", err)
	}

	got, err := w.GetCookies(context.Background())
	if err != nil {
		t.Fatalf("GetCookies: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected at least one cookie from GetCookies")
	}
	if got[0].Name != "_abck" || got[0].Value != "sentinel" {
		t.Errorf("unexpected cookie: %+v", got[0])
	}
}

func TestWorkerClient_ReportStatus(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()

	w, err := cluster.NewWorkerClient("pc-2", addr)
	if err != nil {
		t.Fatalf("NewWorkerClient: %v", err)
	}
	defer w.Close()

	if err := w.ReportStatus(context.Background(), 100, "active"); err != nil {
		t.Fatalf("ReportStatus: %v", err)
	}
}

func TestWorkerClient_WatchCookies(t *testing.T) {
	addr, _, stop := startTestServer(t)
	defer stop()

	w, err := cluster.NewWorkerClient("pc-6", addr)
	if err != nil {
		t.Fatalf("NewWorkerClient: %v", err)
	}
	defer w.Close()

	received := make(chan []*http.Cookie, 5)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	if err := w.WatchCookies(ctx, func(c []*http.Cookie) {
		received <- c
	}); err != nil {
		t.Fatalf("WatchCookies: %v", err)
	}

	// Drain the initial snapshot.
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("did not receive initial snapshot within 1s")
	}

	// Trigger a broadcast and wait for the push.
	go func() {
		time.Sleep(50 * time.Millisecond)
		_ = w.BroadcastCookie(context.Background(), 0,
			[]*http.Cookie{{Name: "watch_test", Value: "ok"}})
	}()

	select {
	case cookies := <-received:
		found := false
		for _, c := range cookies {
			if c.Name == "watch_test" {
				found = true
			}
		}
		if !found {
			t.Error("watch_test cookie not found in pushed update")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("did not receive broadcast push within 2s")
	}
}

// ─── bufconn in-memory integration test ──────────────────────────────────────

// startBufconnServer starts a MasterControllerServer on an in-memory bufconn
// listener (no OS port allocation) and returns a dial function for connecting
// clients and a cleanup function.
func startBufconnServer(t *testing.T) (dialFunc func(context.Context, string) (net.Conn, error), stop func()) {
	t.Helper()
	const bufSize = 1 << 20 // 1 MiB
	lis := bufconn.Listen(bufSize)

	grpcSrv := grpc.NewServer()
	pb.RegisterMasterControllerServer(grpcSrv, cluster.NewMasterControllerServer())
	go func() { _ = grpcSrv.Serve(lis) }()

	dialFn := func(ctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(ctx)
	}
	stopFn := func() {
		grpcSrv.GracefulStop()
		_ = lis.Close()
	}
	return dialFn, stopFn
}

// dialBufconn creates a gRPC client connection through the in-memory bufconn.
func dialBufconn(t *testing.T, dialFn func(context.Context, string) (net.Conn, error)) pb.MasterControllerClient {
	t.Helper()
	conn, err := grpc.NewClient(
		"passthrough:///bufconn",
		grpc.WithContextDialer(dialFn),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("dialBufconn: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return pb.NewMasterControllerClient(conn)
}

// TestWatchCookies_BufconnBroadcast is an in-memory integration test for the
// Master-Worker gRPC setup.  It uses bufconn to avoid real network port
// collisions.  The test:
//
//  1. Starts the MasterControllerServer on an in-memory bufconn listener.
//  2. Connects two mock WorkerClient instances (pc-bw1, pc-bw2).
//  3. Worker 2 opens a WatchCookies stream and consumes its initial snapshot.
//  4. Worker 1 broadcasts a mock _abck cookie.
//  5. Asserts Worker 2 receives the exact cookie payload within 50 milliseconds.
//
// Synchronisation is achieved with channels and a sync.WaitGroup; no
// time.Sleep is used.
func TestWatchCookies_BufconnBroadcast(t *testing.T) {
	dialFn, stop := startBufconnServer(t)
	t.Cleanup(stop)

	worker1 := dialBufconn(t, dialFn)
	worker2 := dialBufconn(t, dialFn)

	// Worker 2 opens a WatchCookies stream with a generous parent deadline so
	// the test is not flaky on a loaded CI machine.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)

	stream, err := worker2.WatchCookies(ctx, &pb.WatchCookiesRequest{PcId: "pc-bw2"})
	if err != nil {
		t.Fatalf("WatchCookies: %v", err)
	}

	// Buffered channel drains the stream in a background goroutine.
	// Size 8 is large enough that the goroutine never blocks in this test.
	received := make(chan *pb.GetGlobalCookiesResponse, 8)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msg, err := stream.Recv()
			if err != nil {
				return // context cancelled or stream closed
			}
			received <- msg
		}
	}()

	// Wait for the initial snapshot (may be empty – just proves the stream is live).
	// bufconn is in-memory so 200ms is ample even on a loaded CI machine.
	select {
	case <-received:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("timeout waiting for initial WatchCookies snapshot")
	}

	// Worker 1 broadcasts the _abck cookie.  The subscription is guaranteed to
	// be active because we already received the initial snapshot, which is sent
	// only after the subscriber is registered.
	_, err = worker1.BroadcastCookie(ctx, &pb.BroadcastCookieRequest{
		PcId:    "pc-bw1",
		Cookies: []*pb.Cookie{{Name: "_abck", Value: "bufconn-sentinel", Domain: "example.com", Path: "/"}},
	})
	if err != nil {
		t.Fatalf("BroadcastCookie: %v", err)
	}

	// Worker 2 must receive the pushed cookie within 50 ms.
	// bufconn has zero network latency so this deadline is generous.
	select {
	case msg := <-received:
		found := false
		for _, ck := range msg.Cookies {
			if ck.Name == "_abck" && ck.Value == "bufconn-sentinel" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("_abck=bufconn-sentinel not found in Worker 2's stream message: %v", msg.Cookies)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("Worker 2 did not receive _abck cookie within 50ms")
	}

	cancel()  // terminate the stream
	wg.Wait() // wait for the drainer goroutine to exit
}
