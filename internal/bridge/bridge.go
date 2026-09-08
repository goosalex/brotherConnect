// Package bridge wires the transport, printer discovery, and local queue into a
// running bridge. It is the top-level orchestrator for Phase 1 (Requirements.md
// §3, PhaseMapping.md).
package bridge

import (
	"context"
	"log/slog"

	"brotherConnect/internal/config"
	"brotherConnect/internal/job"
	"brotherConnect/internal/printer"
	"brotherConnect/internal/protocol"
	"brotherConnect/internal/queue"
	"brotherConnect/internal/transport"
)

// Bridge coordinates the connection to trencitos, printer discovery, and job
// execution.
type Bridge struct {
	cfg        config.Config
	client     *transport.Client
	discoverer printer.Discoverer
	queue      *queue.Queue
	log        *slog.Logger
}

// New constructs a Bridge from its collaborators. The client is created here so
// its inbound handler can route to this Bridge.
func New(cfg config.Config, dialer transport.Dialer, disc printer.Discoverer, driver printer.Driver, log *slog.Logger) *Bridge {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	b := &Bridge{
		cfg:        cfg,
		discoverer: disc,
		log:        log,
	}

	// Queue reports every job transition; forward them to the cloud.
	b.queue = queue.New(driver, b.onJobTransition, queue.Options{Logger: log})

	b.client = transport.NewClient(dialer, transport.Config{
		Hello: protocol.Hello{
			ProtocolVersion: protocol.Version,
			InstallationID:  cfg.InstallationID,
			BridgeVersion:   cfg.BridgeVersion,
		},
		HeartbeatInterval: cfg.HeartbeatInterval,
		Logger:            log,
	}, b.onMessage)

	return b
}

// Run starts discovery and the transport loop, blocking until ctx is cancelled
// or the token is revoked. It drains the queue on shutdown.
func (b *Bridge) Run(ctx context.Context) error {
	events, err := b.discoverer.Start(ctx)
	if err != nil {
		return err
	}
	go b.watchPrinters(ctx, events)

	defer b.queue.Close()
	return b.client.Run(ctx)
}

// watchPrinters forwards discovery events to the cloud as printer updates
// (Requirements.md §6, §9a).
func (b *Bridge) watchPrinters(ctx context.Context, events <-chan printer.Event) {
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			b.sendPrinterUpdate(ev.Printer, ev.Kind != printer.EventDisconnected)
		}
	}
}

func (b *Bridge) sendPrinterUpdate(p printer.Printer, available bool) {
	upd := protocol.PrinterUpdate{
		PrinterID:    p.ID,
		Model:        p.Model,
		SerialNumber: p.SerialNumber,
		Connection:   string(p.Connection),
		Status:       string(p.Status),
		Available:    available && p.Available(),
	}
	env, err := protocol.Encode(protocol.TypePrinterUpdate, upd)
	if err != nil {
		b.log.Error("encode printer update", "err", err)
		return
	}
	if err := b.client.Send(env); err != nil {
		b.log.Warn("send printer update", "printer_id", p.ID, "err", err)
	}
}

// onMessage handles inbound cloud messages routed from the transport read loop.
func (b *Bridge) onMessage(env protocol.Envelope) {
	switch env.Type {
	case protocol.TypeJobDeliver:
		b.handleJobDeliver(env)
	default:
		b.log.Debug("unhandled message", "type", env.Type)
	}
}

// handleJobDeliver acknowledges a delivered job and submits it to the queue.
func (b *Bridge) handleJobDeliver(env protocol.Envelope) {
	var jd protocol.JobDeliver
	if err := protocol.Decode(env, &jd); err != nil {
		b.log.Error("decode job", "err", err)
		return
	}
	j := jd.Job

	// Acknowledge receipt first so the cloud can mark the job delivered
	// (Requirements.md §10).
	if ack, err := protocol.Encode(protocol.TypeJobAck, protocol.JobAck{JobID: j.ID}); err == nil {
		_ = b.client.Send(ack)
	}

	res, err := b.queue.Submit(j)
	switch res {
	case queue.Rejected:
		b.sendError("invalid_job", err.Error(), j.ID)
	case queue.Duplicate:
		b.log.Info("duplicate job ignored", "job_id", j.ID)
	case queue.Accepted:
		b.log.Info("job accepted", "job_id", j.ID, "printer_id", j.PrinterID)
	}
}

// onJobTransition reports a job state change to the cloud (Requirements.md §9,
// §14 job_status). Passed as the queue's StatusSink.
func (b *Bridge) onJobTransition(jobID string, tr job.Transition, current job.State) {
	msg := protocol.JobStatus{JobID: jobID, Transition: tr, CurrentState: current}
	env, err := protocol.Encode(protocol.TypeJobStatus, msg)
	if err != nil {
		b.log.Error("encode job status", "err", err)
		return
	}
	if err := b.client.Send(env); err != nil {
		b.log.Warn("send job status", "job_id", jobID, "err", err)
	}
}

func (b *Bridge) sendError(code, message, jobID string) {
	env, err := protocol.Encode(protocol.TypeError, protocol.Error{Code: code, Message: message, JobID: jobID})
	if err != nil {
		return
	}
	_ = b.client.Send(env)
}
