// Package printer defines printer discovery and printing abstractions.
//
// The real USB backend (Brother QL over libusb/gousb) and the mDNS/Bonjour
// backend require cgo and platform libraries, and are Phase 1/Phase 2 concrete
// implementations of these interfaces. This package ships stdlib-only
// interfaces plus a stub backend so the bridge builds and is testable offline.
// See Requirements.md §6 and PhaseMapping.md.
package printer

import (
	"context"
	"time"
)

// Connection is how a printer is attached.
type Connection string

const (
	ConnectionUSB     Connection = "usb"
	ConnectionNetwork Connection = "network"
)

// Status is a coarse printer condition surfaced to the cloud and users in plain
// language (Requirements.md §9a).
type Status string

const (
	StatusReady      Status = "ready"
	StatusBusy       Status = "busy"
	StatusOutOfMedia Status = "out_of_media"
	StatusWrongMedia Status = "wrong_media"
	StatusCoverOpen  Status = "cover_open"
	StatusError      Status = "error"
	StatusOffline    Status = "offline"
)

// Printable reports whether a printer in this status can accept a job now.
func (s Status) Printable() bool { return s == StatusReady }

// Printer describes a discovered printer. ID is stable across reconnects: the
// device serial number where available, otherwise a bridge-local identifier
// (Requirements.md §6 "Printer identity").
type Printer struct {
	ID           string
	Model        string
	SerialNumber string
	Connection   Connection
	Status       Status
	// LoadedMediaMM is the media size currently loaded, used to validate jobs
	// against requested label dimensions. Zero values mean unknown.
	LoadedWidthMM  float64
	LoadedHeightMM float64
}

// Available reports whether the printer can currently be printed to.
func (p Printer) Available() bool { return p.Status.Printable() }

// EventKind classifies a discovery event.
type EventKind int

const (
	EventConnected EventKind = iota
	EventDisconnected
	EventStatusChanged
)

// Event is a change in the discovered-printer set (Requirements.md §6:
// "Detect connection and disconnection events").
type Event struct {
	Kind    EventKind
	Printer Printer
	At      time.Time
}

// Discoverer finds printers and streams change events. Implementations must be
// safe to Start once; Events is closed when the context is cancelled.
type Discoverer interface {
	// Start begins discovery and returns a channel of events. The channel is
	// closed when ctx is done.
	Start(ctx context.Context) (<-chan Event, error)
	// List returns the currently known printers.
	List() []Printer
}

// Driver sends raster payloads to a specific printer and reports its status.
type Driver interface {
	// Print sends the raster payload to the printer identified by id. It blocks
	// until the print completes or fails.
	Print(ctx context.Context, id string, raster []byte) error
	// Status queries the live status of a printer.
	Status(ctx context.Context, id string) (Status, error)
}

// Registrar is an optional Driver capability: drivers that resolve printers by
// attributes other than ID (e.g. the CUPS driver matches by model) implement it
// so the bridge can supply full printer descriptors from discovery.
type Registrar interface {
	Register(Printer)
}

// MediaReporter is an optional Driver capability to read the media size (in mm)
// currently loaded in a printer, for validating a job's requested label size
// against the loaded media (Requirements.md §8).
type MediaReporter interface {
	LoadedMedia(ctx context.Context, id string) (width, height float64, ok bool)
}
