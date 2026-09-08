package queue

import (
	"context"
	"errors"
	"sync"

	"brotherConnect/internal/job"
	"brotherConnect/internal/printer"
)

// ErrClosed is returned when submitting to a closed queue.
var ErrClosed = errors.New("queue closed")

// worker prints jobs for a single printer sequentially, satisfying
// Requirements.md §8 "Print jobs sequentially per printer".
type worker struct {
	printerID string
	jobs      chan job.Job
	done      chan struct{}
	q         *Queue

	stopOnce sync.Once
}

func (w *worker) submit(j job.Job) {
	select {
	case w.jobs <- j:
	case <-w.done:
	}
}

func (w *worker) stop() {
	w.stopOnce.Do(func() { close(w.done) })
}

func (w *worker) run(ctx context.Context) {
	for {
		select {
		case <-w.done:
			return
		case j := <-w.jobs:
			w.process(ctx, j)
		}
	}
}

// process runs one job through queued -> sent -> printing -> completed/failed,
// reporting each transition. On terminal success or permanent failure the job
// ID is recorded for duplicate suppression.
func (w *worker) process(ctx context.Context, j job.Job) {
	tr := job.NewTracker(w.q.now)
	emit := func(to job.State, reason string) bool {
		if err := tr.To(to, reason); err != nil {
			w.q.log.Error("illegal transition", "job_id", j.ID, "err", err)
			return false
		}
		hist := tr.History()
		w.q.sink(j.ID, hist[len(hist)-1], tr.State())
		return true
	}

	// created -> queued
	if !emit(job.StateQueued, "") {
		return
	}
	// queued -> sent
	if !emit(job.StateSent, "") {
		return
	}
	// sent -> printing
	if !emit(job.StatePrinting, "") {
		return
	}

	// Apply copies by repeating the payload (Requirements.md §8).
	var printErr error
	for i := 0; i < j.Copies; i++ {
		if printErr = w.q.driver.Print(ctx, j.PrinterID, j.Payload); printErr != nil {
			break
		}
	}

	if printErr != nil {
		emit(job.StateFailed, classify(printErr))
		// Record permanent failures so redelivery is not reprinted; a
		// not-printable status is treated as terminal for this job attempt.
		w.q.dedup.record(j.PrinterID, j.ID)
		w.q.log.Error("print failed", "job_id", j.ID, "printer_id", j.PrinterID, "err", printErr)
		return
	}

	emit(job.StateCompleted, "")
	w.q.dedup.record(j.PrinterID, j.ID)
}

// classify turns a driver error into a human-readable failure reason
// (Requirements.md §9a).
func classify(err error) string {
	var notPrintable printer.ErrNotPrintable
	if errors.As(err, &notPrintable) {
		switch notPrintable.Status {
		case printer.StatusOutOfMedia:
			return "printer is out of media"
		case printer.StatusCoverOpen:
			return "printer cover is open"
		case printer.StatusWrongMedia:
			return "loaded media does not match the label size"
		case printer.StatusOffline:
			return "printer is offline"
		default:
			return "printer is not ready"
		}
	}
	var unknown printer.ErrUnknownPrinter
	if errors.As(err, &unknown) {
		return "printer is not connected to this bridge"
	}
	return "print failed: " + err.Error()
}
