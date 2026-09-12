//go:build !darwin

package autostart

// New returns ErrUnsupported until a backend exists for this platform. The Linux
// systemd user unit and Windows Scheduled Task backends are planned
// (docs/ui-design.md §5.3, PhaseMapping.md Phase 2/3).
func New(cfg Config) (Service, error) {
	return nil, ErrUnsupported
}
