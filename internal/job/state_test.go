package job

import (
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time {
	return func() time.Time { return t }
}

func TestTrackerHappyPath(t *testing.T) {
	now := time.Date(2026, 9, 8, 12, 0, 0, 0, time.UTC)
	tr := NewTracker(fixedClock(now))

	for _, s := range []State{StateQueued, StateSent, StatePrinting, StateCompleted} {
		if err := tr.To(s, ""); err != nil {
			t.Fatalf("To(%s): %v", s, err)
		}
	}
	if tr.State() != StateCompleted {
		t.Fatalf("state = %s, want completed", tr.State())
	}
	if got := len(tr.History()); got != 4 {
		t.Fatalf("history len = %d, want 4", got)
	}
	for _, h := range tr.History() {
		if !h.At.Equal(now) {
			t.Errorf("transition %s->%s missing timestamp", h.From, h.To)
		}
	}
}

func TestTrackerIllegalTransition(t *testing.T) {
	tr := NewTracker(fixedClock(time.Now()))
	// created -> printing is not allowed
	err := tr.To(StatePrinting, "")
	if _, ok := err.(ErrIllegalTransition); !ok {
		t.Fatalf("got %v, want ErrIllegalTransition", err)
	}
}

func TestTrackerFailRequiresReason(t *testing.T) {
	tr := NewTracker(fixedClock(time.Now()))
	_ = tr.To(StateQueued, "")
	_ = tr.To(StateSent, "")
	if err := tr.To(StateFailed, ""); err == nil {
		t.Fatal("expected error when failing without a reason")
	}
	if err := tr.To(StateFailed, "printer offline"); err != nil {
		t.Fatalf("failing with reason: %v", err)
	}
}

func TestTerminalStates(t *testing.T) {
	for _, s := range []State{StateCompleted, StateFailed, StateCancelled} {
		if !s.Terminal() {
			t.Errorf("%s should be terminal", s)
		}
		if CanTransition(s, StateQueued) {
			t.Errorf("%s should have no outgoing transitions", s)
		}
	}
	if StatePrinting.Terminal() {
		t.Error("printing should not be terminal")
	}
}
