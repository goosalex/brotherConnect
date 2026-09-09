package printer

import "fmt"

// ErrUnknownPrinter indicates a job targeted a printer the bridge does not know.
type ErrUnknownPrinter struct{ ID string }

func (e ErrUnknownPrinter) Error() string {
	return fmt.Sprintf("unknown printer %q", e.ID)
}

// ErrNotPrintable indicates the printer exists but cannot print in its current
// status (e.g. out of media, cover open). This is a permanent failure for the
// current attempt but may clear on the next status change.
type ErrNotPrintable struct {
	ID     string
	Status Status
}

func (e ErrNotPrintable) Error() string {
	return fmt.Sprintf("printer %q not printable: %s", e.ID, e.Status)
}
