// Package job defines the print-job model and its state machine.
//
// A Job is the unit of work delivered from the cloud (trencitos) to the bridge.
// The bridge does not render labels: per Requirements.md §8 the print payload is
// server-generated Brother raster, so the bridge validates, queues, prints, and
// reports state. Label dimensions are carried only to validate against the
// printer's loaded media.
package job

import (
	"errors"
	"time"
)

// State is a print-job lifecycle state. See Requirements.md §9.
type State string

const (
	StateCreated   State = "created"
	StateQueued    State = "queued"
	StateSent      State = "sent"
	StatePrinting  State = "printing"
	StateCompleted State = "completed"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
)

// Terminal reports whether no further transitions are allowed from s.
func (s State) Terminal() bool {
	switch s {
	case StateCompleted, StateFailed, StateCancelled:
		return true
	default:
		return false
	}
}

// Dimensions describes the physical label size in millimetres. The bridge uses
// this to validate against the printer's loaded media, not to render.
type Dimensions struct {
	WidthMM  float64 `json:"width_mm"`
	HeightMM float64 `json:"height_mm"`
}

// Job is a single print job. IDs are idempotency keys (Requirements.md §10):
// the bridge must not reprint a job whose ID it has already terminally handled.
type Job struct {
	ID         string            `json:"id"`
	TenantID   string            `json:"tenant_id"`
	UserID     string            `json:"user_id"`
	PrinterID  string            `json:"printer_id"`
	Dimensions Dimensions        `json:"dimensions"`
	Copies     int               `json:"copies"`
	Payload    []byte            `json:"payload"` // server-generated Brother raster
	CreatedAt  time.Time         `json:"created_at"`
	Metadata   map[string]string `json:"metadata,omitempty"`
}

// ErrInvalidJob is returned when a job fails structural validation.
var ErrInvalidJob = errors.New("invalid job")

// Validate checks required fields before a job is accepted into the queue
// (Requirements.md §8: "Validate job structure"). It does not check media
// compatibility; that happens at print time against a live printer.
func (j Job) Validate() error {
	switch {
	case j.ID == "":
		return wrap("missing id")
	case j.TenantID == "":
		return wrap("missing tenant_id")
	case j.UserID == "":
		return wrap("missing user_id")
	case j.PrinterID == "":
		return wrap("missing printer_id")
	case j.Copies < 1:
		return wrap("copies must be >= 1")
	case len(j.Payload) == 0:
		return wrap("empty payload")
	case j.Dimensions.WidthMM <= 0 || j.Dimensions.HeightMM <= 0:
		return wrap("invalid dimensions")
	}
	return nil
}

func wrap(msg string) error {
	return errors.Join(ErrInvalidJob, errors.New(msg))
}
