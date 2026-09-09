package transport

import (
	"context"
	"sync"
	"testing"
	"time"

	"brotherConnect/internal/protocol"
)

func welcome(t *testing.T) protocol.Envelope {
	t.Helper()
	env, err := protocol.Encode(protocol.TypeWelcome, protocol.Welcome{ProtocolVersion: protocol.Version, SessionID: "s1"})
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func fastBackoff() *Backoff {
	return &Backoff{Base: time.Millisecond, Max: 5 * time.Millisecond, Factor: 2.0}
}

func TestClientHandshakeAndReceive(t *testing.T) {
	conn := NewFakeConn()
	conn.Push(welcome(t))

	var mu sync.Mutex
	var got []protocol.Type
	handler := func(e protocol.Envelope) {
		mu.Lock()
		got = append(got, e.Type)
		mu.Unlock()
	}

	c := NewClient(NewFakeDialer(conn), Config{
		Hello:   protocol.Hello{ProtocolVersion: protocol.Version, InstallationID: "bri_test"},
		Backoff: fastBackoff(),
	}, handler)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// Deliver a job after the handshake.
	jd, _ := protocol.Encode(protocol.TypeJobDeliver, protocol.JobDeliver{})
	conn.Push(jd)

	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		for _, ty := range got {
			if ty == protocol.TypeJobDeliver {
				return true
			}
		}
		return false
	})

	// The bridge should have sent a Hello during handshake.
	assertSentType(t, conn, protocol.TypeHello)
}

func TestClientReconnectsAfterDrop(t *testing.T) {
	conn1 := NewFakeConn()
	conn1.Push(welcome(t))
	conn2 := NewFakeConn()
	conn2.Push(welcome(t))

	c := NewClient(NewFakeDialer(conn1, conn2), Config{
		Hello:   protocol.Hello{ProtocolVersion: protocol.Version, InstallationID: "bri_test"},
		Backoff: fastBackoff(),
	}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go c.Run(ctx)

	// Wait for first handshake, then drop conn1 to force a reconnect.
	waitFor(t, func() bool { return hasSentType(conn1, protocol.TypeHello) })
	conn1.Close()

	// The client should dial conn2 and handshake again.
	waitFor(t, func() bool { return hasSentType(conn2, protocol.TypeHello) })
}

func TestClientStopsOnTokenRevoked(t *testing.T) {
	conn := NewFakeConn()
	conn.Push(welcome(t))
	revoked, _ := protocol.Encode(protocol.TypeTokenRevoked, protocol.Error{Code: "revoked"})
	conn.Push(revoked)

	c := NewClient(NewFakeDialer(conn), Config{
		Hello:   protocol.Hello{ProtocolVersion: protocol.Version, InstallationID: "bri_test"},
		Backoff: fastBackoff(),
	}, nil)

	errc := make(chan error, 1)
	go func() { errc <- c.Run(context.Background()) }()

	select {
	case err := <-errc:
		if err != ErrRevoked {
			t.Fatalf("Run returned %v, want ErrRevoked", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not stop after token revocation")
	}
}

func hasSentType(c *FakeConn, ty protocol.Type) bool {
	for _, e := range c.Sent() {
		if e.Type == ty {
			return true
		}
	}
	return false
}

func assertSentType(t *testing.T, c *FakeConn, ty protocol.Type) {
	t.Helper()
	if !hasSentType(c, ty) {
		t.Fatalf("expected a %s message to have been sent", ty)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
