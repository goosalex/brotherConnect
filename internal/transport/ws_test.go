package transport

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"brotherConnect/internal/protocol"
)

// wsTestServer accepts one WebSocket connection, records the auth header and the
// client's Hello, replies with Welcome, delivers a job, and forwards subsequent
// client messages to a channel — enough to exercise the real WSDialer + wsConn
// + Client over loopback.
type wsTestServer struct {
	auth     chan string
	hello    chan protocol.Hello
	fromCli  chan protocol.Type
}

func newWSTestServer() *wsTestServer {
	return &wsTestServer{
		auth:    make(chan string, 1),
		hello:   make(chan protocol.Hello, 1),
		fromCli: make(chan protocol.Type, 16),
	}
}

func (s *wsTestServer) handler(w http.ResponseWriter, r *http.Request) {
	s.auth <- r.Header.Get("Authorization")
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	ctx := r.Context()

	// Read the client's Hello.
	env, err := readEnvelope(ctx, c)
	if err != nil {
		return
	}
	var hello protocol.Hello
	_ = protocol.Decode(env, &hello)
	s.hello <- hello

	// Reply with Welcome, then deliver a job.
	_ = writeEnvelope(ctx, c, protocol.TypeWelcome, protocol.Welcome{ProtocolVersion: protocol.Version, SessionID: "s1"})
	_ = writeEnvelope(ctx, c, protocol.TypeJobDeliver, protocol.JobDeliver{})

	// Forward everything the client sends afterwards (heartbeats, acks, …).
	for {
		env, err := readEnvelope(ctx, c)
		if err != nil {
			return
		}
		s.fromCli <- env.Type
	}
}

func readEnvelope(ctx context.Context, c *websocket.Conn) (protocol.Envelope, error) {
	_, data, err := c.Read(ctx)
	if err != nil {
		return protocol.Envelope{}, err
	}
	var env protocol.Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return protocol.Envelope{}, err
	}
	return env, nil
}

func writeEnvelope(ctx context.Context, c *websocket.Conn, t protocol.Type, msg any) error {
	env, err := protocol.Encode(t, msg)
	if err != nil {
		return err
	}
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	return c.Write(ctx, websocket.MessageText, data)
}

func expectType(t *testing.T, ch <-chan protocol.Type, want protocol.Type) {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case got := <-ch:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", want)
		}
	}
}

func TestWSDialerHandshakeAndRoundTrip(t *testing.T) {
	srv := newWSTestServer()
	httpSrv := httptest.NewServer(http.HandlerFunc(srv.handler))
	defer httpSrv.Close()
	wsURL := "ws" + strings.TrimPrefix(httpSrv.URL, "http")

	inbound := make(chan protocol.Type, 16)
	client := NewClient(
		NewWSDialer(wsURL, "test-token"),
		Config{
			Hello:             protocol.Hello{ProtocolVersion: protocol.Version, InstallationID: "bri_test"},
			Backoff:           fastBackoff(),
			HeartbeatInterval: 20 * time.Millisecond, // generate client->server traffic
		},
		func(e protocol.Envelope) { inbound <- e.Type },
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go client.Run(ctx)

	// The dialer must present the device token as a Bearer credential.
	select {
	case a := <-srv.auth:
		if a != "Bearer test-token" {
			t.Fatalf("Authorization = %q, want Bearer test-token", a)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server never received a connection")
	}

	// The server must receive the client's Hello with its installation ID.
	select {
	case h := <-srv.hello:
		if h.InstallationID != "bri_test" || h.ProtocolVersion != protocol.Version {
			t.Fatalf("hello = %+v", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server never received Hello")
	}

	// The client must receive the delivered job over the real transport...
	expectType(t, inbound, protocol.TypeJobDeliver)
	// ...and the server must receive heartbeats the client sends.
	expectType(t, srv.fromCli, protocol.TypeHeartbeat)
}

func TestWSDialerBadURLErrors(t *testing.T) {
	// A dial to a closed port must return an error (so the reconnect loop backs
	// off) rather than panic.
	d := NewWSDialer("ws://127.0.0.1:1/bridge", "tok")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := d.Dial(ctx); err == nil {
		t.Fatal("expected dial error to an unreachable endpoint")
	}
}
