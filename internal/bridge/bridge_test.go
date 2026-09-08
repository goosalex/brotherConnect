package bridge

import (
	"context"
	"testing"
	"time"

	"brotherConnect/internal/config"
	"brotherConnect/internal/job"
	"brotherConnect/internal/printer"
	"brotherConnect/internal/protocol"
	"brotherConnect/internal/transport"
)

// TestEndToEndJobPath exercises the Phase 1 acceptance path: a job delivered
// over the (fake) WSS connection is acknowledged, printed via the stub backend,
// and its terminal status is reported back to the cloud.
func TestEndToEndJobPath(t *testing.T) {
	conn := transport.NewFakeConn()
	welcome, _ := protocol.Encode(protocol.TypeWelcome, protocol.Welcome{ProtocolVersion: protocol.Version})
	conn.Push(welcome)

	backend := printer.NewStubBackend(nil)
	cfg := config.Config{InstallationID: "bri_test", BridgeVersion: "test"}
	b := New(cfg, transport.NewFakeDialer(conn), backend, backend, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go b.Run(ctx)

	// Deliver a valid job once the handshake is underway.
	j := job.Job{
		ID: "job-e2e", TenantID: "t1", UserID: "u1", PrinterID: "SN-STUB-0001",
		Dimensions: job.Dimensions{WidthMM: 50, HeightMM: 70}, Copies: 1, Payload: []byte{0x01},
	}
	deliver, _ := protocol.Encode(protocol.TypeJobDeliver, protocol.JobDeliver{Job: j})
	conn.Push(deliver)

	// The printer should receive exactly one payload.
	waitFor(t, func() bool { return backend.PrintedCount("SN-STUB-0001") == 1 })

	// The bridge should have acked the job and reported a completed status.
	waitFor(t, func() bool {
		var acked, completed bool
		for _, e := range conn.Sent() {
			switch e.Type {
			case protocol.TypeJobAck:
				var a protocol.JobAck
				_ = protocol.Decode(e, &a)
				acked = acked || a.JobID == "job-e2e"
			case protocol.TypeJobStatus:
				var s protocol.JobStatus
				_ = protocol.Decode(e, &s)
				completed = completed || (s.JobID == "job-e2e" && s.CurrentState == job.StateCompleted)
			}
		}
		return acked && completed
	})
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
