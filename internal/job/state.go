package job

import (
	"fmt"
	"time"
)

// Transition records a single state change with a timestamp and optional
// human-readable diagnostic. Requirements.md §9: "Each state transition should
// include a timestamp and diagnostic information where appropriate."
type Transition struct {
	From   State     `json:"from"`
	To     State     `json:"to"`
	At     time.Time `json:"at"`
	Reason string    `json:"reason,omitempty"`
}

// allowed maps each state to the states it may transition to.
var allowed = map[State][]State{
	StateCreated:  {StateQueued, StateCancelled, StateFailed},
	StateQueued:   {StateSent, StateCancelled, StateFailed},
	StateSent:     {StatePrinting, StateFailed, StateCancelled},
	StatePrinting: {StateCompleted, StateFailed},
	// terminal states have no outgoing transitions
}

// CanTransition reports whether from -> to is a legal transition.
func CanTransition(from, to State) bool {
	for _, s := range allowed[from] {
		if s == to {
			return true
		}
	}
	return false
}

// Tracker holds a job's current state and its transition history. It is not safe
// for concurrent use; the queue serialises access per job.
type Tracker struct {
	current State
	history []Transition
	now     func() time.Time
}

// NewTracker returns a Tracker starting in StateCreated. If now is nil,
// time.Now is used. Injecting now keeps tests deterministic.
func NewTracker(now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{current: StateCreated, now: now}
}

// State returns the current state.
func (t *Tracker) State() State { return t.current }

// History returns a copy of the recorded transitions.
func (t *Tracker) History() []Transition {
	out := make([]Transition, len(t.history))
	copy(out, t.history)
	return out
}

// ErrIllegalTransition is returned when a transition is not permitted.
type ErrIllegalTransition struct {
	From, To State
}

func (e ErrIllegalTransition) Error() string {
	return fmt.Sprintf("illegal job transition %s -> %s", e.From, e.To)
}

// To moves the job to state to, recording a timestamped transition. A reason is
// required when moving to StateFailed so failures always carry a human-readable
// cause (Requirements.md §9a). Returns ErrIllegalTransition for disallowed moves.
func (t *Tracker) To(to State, reason string) error {
	if !CanTransition(t.current, to) {
		return ErrIllegalTransition{From: t.current, To: to}
	}
	if to == StateFailed && reason == "" {
		return fmt.Errorf("transition to %s requires a reason", StateFailed)
	}
	t.history = append(t.history, Transition{
		From:   t.current,
		To:     to,
		At:     t.now(),
		Reason: reason,
	})
	t.current = to
	return nil
}
