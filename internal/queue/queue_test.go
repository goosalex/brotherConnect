package queue

import (
	"sync"
	"testing"
	"time"

	"brotherConnect/internal/job"
	"brotherConnect/internal/printer"
)

func testJob(id, printerID string) job.Job {
	return job.Job{
		ID:         id,
		TenantID:   "t1",
		UserID:     "u1",
		PrinterID:  printerID,
		Dimensions: job.Dimensions{WidthMM: 50, HeightMM: 70},
		Copies:     1,
		Payload:    []byte{0xAA},
	}
}

// collectSink records transitions for assertions.
type collectSink struct {
	mu     sync.Mutex
	states map[string][]job.State
}

func newCollectSink() *collectSink { return &collectSink{states: map[string][]job.State{}} }

func (c *collectSink) fn(jobID string, _ job.Transition, current job.State) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states[jobID] = append(c.states[jobID], current)
}

func (c *collectSink) terminal(jobID string) (job.State, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s := c.states[jobID]
	if len(s) == 0 {
		return "", false
	}
	return s[len(s)-1], true
}

// waitFor polls until cond is true or the deadline elapses.
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

func TestSubmitPrintsJobToCompletion(t *testing.T) {
	backend := printer.NewStubBackend(nil)
	sink := newCollectSink()
	q := New(backend, sink.fn, Options{})
	defer q.Close()

	res, err := q.Submit(testJob("job-1", "SN-STUB-0001"))
	if err != nil || res != Accepted {
		t.Fatalf("Submit = %v, %v; want Accepted", res, err)
	}
	waitFor(t, func() bool {
		s, ok := sink.terminal("job-1")
		return ok && s == job.StateCompleted
	})
	if backend.PrintedCount("SN-STUB-0001") != 1 {
		t.Fatalf("printed %d times, want 1", backend.PrintedCount("SN-STUB-0001"))
	}
}

// Acceptance criterion 8: a repeated job ID does not print twice.
func TestDuplicateJobIsSuppressed(t *testing.T) {
	backend := printer.NewStubBackend(nil)
	sink := newCollectSink()
	q := New(backend, sink.fn, Options{})
	defer q.Close()

	if _, err := q.Submit(testJob("dup", "SN-STUB-0001")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := sink.terminal("dup")
		return ok && s == job.StateCompleted
	})

	res, err := q.Submit(testJob("dup", "SN-STUB-0001"))
	if err != nil {
		t.Fatalf("resubmit error: %v", err)
	}
	if res != Duplicate {
		t.Fatalf("resubmit result = %v, want Duplicate", res)
	}
	if got := backend.PrintedCount("SN-STUB-0001"); got != 1 {
		t.Fatalf("printed %d times, want 1 (no duplicate print)", got)
	}
}

func TestCopiesRepeatsPrint(t *testing.T) {
	backend := printer.NewStubBackend(nil)
	sink := newCollectSink()
	q := New(backend, sink.fn, Options{})
	defer q.Close()

	j := testJob("multi", "SN-STUB-0001")
	j.Copies = 3
	if _, err := q.Submit(j); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := sink.terminal("multi")
		return ok && s == job.StateCompleted
	})
	if got := backend.PrintedCount("SN-STUB-0001"); got != 3 {
		t.Fatalf("printed %d times, want 3", got)
	}
}

func TestRejectsInvalidJob(t *testing.T) {
	backend := printer.NewStubBackend(nil)
	q := New(backend, nil, Options{})
	defer q.Close()

	bad := testJob("", "SN-STUB-0001") // missing ID
	res, err := q.Submit(bad)
	if res != Rejected || err == nil {
		t.Fatalf("Submit = %v, %v; want Rejected with error", res, err)
	}
}

func TestFailedPrintReportsReason(t *testing.T) {
	backend := printer.NewStubBackend(nil)
	backend.SetStatus("SN-STUB-0001", printer.StatusOutOfMedia)
	sink := newCollectSink()
	q := New(backend, sink.fn, Options{})
	defer q.Close()

	if _, err := q.Submit(testJob("fail", "SN-STUB-0001")); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool {
		s, ok := sink.terminal("fail")
		return ok && s == job.StateFailed
	})
	if backend.PrintedCount("SN-STUB-0001") != 0 {
		t.Fatal("should not have printed when out of media")
	}
}
