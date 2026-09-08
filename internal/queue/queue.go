// Package queue implements the bridge's local print queue: jobs are validated,
// deduplicated, and printed sequentially per printer, with every state change
// reported back to the caller. See Requirements.md §8, §9, §10.
//
// This is the Phase 1 in-memory implementation. A durable, power-loss-safe local
// store is Phase 2 (Requirements.md §10, PhaseMapping.md).
package queue

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"brotherConnect/internal/job"
	"brotherConnect/internal/printer"
)

// StatusSink receives every job state transition so it can be reported to the
// cloud (Requirements.md §9, §14 job_status).
type StatusSink func(jobID string, tr job.Transition, current job.State)

// Options configures a Queue.
type Options struct {
	// Retention is the duplicate-suppression window. Zero uses DefaultRetention.
	Retention time.Duration
	// Now is the clock; nil uses time.Now. Injected for deterministic tests.
	Now func() time.Time
	// Logger is optional; nil discards logs.
	Logger *slog.Logger
}

// Queue accepts jobs and prints them sequentially per printer.
type Queue struct {
	driver printer.Driver
	sink   StatusSink
	dedup  *dedupStore
	now    func() time.Time
	log    *slog.Logger

	mu      sync.Mutex
	workers map[string]*worker // printerID -> worker
	wg      sync.WaitGroup
	closed  bool
}

// New creates a Queue that prints via driver and reports transitions to sink.
func New(driver printer.Driver, sink StatusSink, opts Options) *Queue {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	if sink == nil {
		sink = func(string, job.Transition, job.State) {}
	}
	return &Queue{
		driver:  driver,
		sink:    sink,
		dedup:   newDedupStore(opts.Retention, now),
		now:     now,
		log:     log,
		workers: map[string]*worker{},
	}
}

// SubmitResult reports the outcome of an enqueue attempt.
type SubmitResult int

const (
	// Accepted means the job was validated and queued.
	Accepted SubmitResult = iota
	// Duplicate means the job ID was already terminally processed for this
	// printer and was suppressed (Requirements.md §10, acceptance 8).
	Duplicate
	// Rejected means the job failed structural validation.
	Rejected
)

// Submit validates and enqueues a job. Duplicate job IDs (per printer) are
// suppressed. The returned error is non-nil only for Rejected results.
func (q *Queue) Submit(j job.Job) (SubmitResult, error) {
	if err := j.Validate(); err != nil {
		return Rejected, err
	}
	if q.dedup.seenRecently(j.PrinterID, j.ID) {
		q.log.Info("suppressed duplicate job", "job_id", j.ID, "printer_id", j.PrinterID)
		return Duplicate, nil
	}

	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return Rejected, ErrClosed
	}
	w, ok := q.workers[j.PrinterID]
	if !ok {
		w = q.newWorker(j.PrinterID)
		q.workers[j.PrinterID] = w
	}
	q.mu.Unlock()

	w.submit(j)
	return Accepted, nil
}

// Close stops accepting jobs and waits for in-flight workers to drain.
func (q *Queue) Close() {
	q.mu.Lock()
	if q.closed {
		q.mu.Unlock()
		return
	}
	q.closed = true
	for _, w := range q.workers {
		w.stop()
	}
	q.mu.Unlock()
	q.wg.Wait()
}

// newWorker starts a per-printer worker goroutine. Caller holds q.mu.
func (q *Queue) newWorker(printerID string) *worker {
	w := &worker{
		printerID: printerID,
		jobs:      make(chan job.Job, 64),
		done:      make(chan struct{}),
		q:         q,
	}
	q.wg.Add(1)
	go func() {
		defer q.wg.Done()
		w.run(context.Background())
	}()
	return w
}
