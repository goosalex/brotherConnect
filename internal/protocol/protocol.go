// Package protocol defines the WSS message envelope and message types exchanged
// between the bridge and trencitos. See Requirements.md §14.
//
// The concrete wire contract is finalised with trencitos in Phase 0; these types
// are the bridge-side working definition and are versioned so the server and
// bridge can negotiate compatibility on connect (Requirements.md §5).
package protocol

import (
	"encoding/json"
	"fmt"

	"brotherConnect/internal/job"
)

// Version is the protocol version this bridge speaks. Negotiated on connect via
// the Hello/Welcome handshake.
const Version = 1

// Type identifies the kind of message carried in an Envelope.
type Type string

const (
	// Handshake (Requirements.md §5: version negotiation on connect).
	TypeHello   Type = "hello"   // bridge -> cloud: offer version + identity
	TypeWelcome Type = "welcome" // cloud -> bridge: accept, confirm version

	// Liveness.
	TypeHeartbeat Type = "heartbeat" // bridge -> cloud, periodic

	// Printer inventory and status (Requirements.md §6, §9a).
	TypePrinterUpdate Type = "printer_update" // bridge -> cloud

	// Job flow (Requirements.md §8, §9).
	TypeJobDeliver Type = "job_deliver" // cloud -> bridge
	TypeJobAck     Type = "job_ack"     // bridge -> cloud: received
	TypeJobStatus  Type = "job_status"  // bridge -> cloud: state change

	// Control.
	TypeError          Type = "error"           // either direction
	TypeTokenRevoked   Type = "token_revoked"   // cloud -> bridge
)

// Envelope wraps every message. Payload is decoded based on Type.
type Envelope struct {
	Type    Type            `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// Hello is the bridge's opening message.
type Hello struct {
	ProtocolVersion int    `json:"protocol_version"`
	InstallationID  string `json:"installation_id"`
	BridgeVersion   string `json:"bridge_version"`
}

// Welcome is the cloud's acceptance. If ProtocolVersion differs from the
// bridge's, the bridge must decide whether it can proceed.
type Welcome struct {
	ProtocolVersion int    `json:"protocol_version"`
	SessionID       string `json:"session_id"`
}

// Heartbeat is a periodic liveness ping.
type Heartbeat struct {
	InstallationID string `json:"installation_id"`
}

// PrinterUpdate reports the bridge's current view of a printer, including its
// availability/status (Requirements.md §9a) and the media the printer has
// sensed as loaded (Requirements.md §8), so the server can pre-select the label
// size and warn on a mismatch.
type PrinterUpdate struct {
	PrinterID    string `json:"printer_id"`
	Model        string `json:"model"`
	SerialNumber string `json:"serial_number,omitempty"`
	Connection   string `json:"connection"` // "usb" | "network"
	Status       string `json:"status"`     // e.g. "ready", "out_of_media", "cover_open", "offline"
	Available    bool   `json:"available"`
	// DPI is the print-head resolution in dots per inch (Brother QL 300,
	// NIIMBOT B1/B21 203), so the server can size barcodes and QR codes for
	// the device (see docs/niimbot.md §6.1 for measured minimums). Omitted
	// when unknown.
	DPI int `json:"dpi,omitempty"`
	// LoadedWidthMM / LoadedHeightMM are the sensed loaded media in millimetres,
	// where the device reports it. Omitted (0) when unknown; a 0 height means
	// continuous tape (width only).
	LoadedWidthMM  float64 `json:"loaded_width_mm,omitempty"`
	LoadedHeightMM float64 `json:"loaded_height_mm,omitempty"`
}

// JobDeliver carries a job from the cloud to the bridge.
type JobDeliver struct {
	Job job.Job `json:"job"`
}

// JobAck acknowledges receipt of a delivered job (Requirements.md §10:
// delivery acknowledgements).
type JobAck struct {
	JobID string `json:"job_id"`
}

// JobStatus reports a job state change with timestamp and optional reason.
type JobStatus struct {
	JobID       string         `json:"job_id"`
	Transition  job.Transition `json:"transition"`
	CurrentState job.State     `json:"current_state"`
}

// Error is a structured error report.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	JobID   string `json:"job_id,omitempty"`
}

// Encode marshals a typed message into an Envelope.
func Encode(t Type, msg any) (Envelope, error) {
	raw, err := json.Marshal(msg)
	if err != nil {
		return Envelope{}, fmt.Errorf("encode %s: %w", t, err)
	}
	return Envelope{Type: t, Payload: raw}, nil
}

// Decode unmarshals an envelope's payload into dst.
func Decode(e Envelope, dst any) error {
	if err := json.Unmarshal(e.Payload, dst); err != nil {
		return fmt.Errorf("decode %s: %w", e.Type, err)
	}
	return nil
}
