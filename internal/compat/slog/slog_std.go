//go:build !legacy

// Package slog is the bridge's structured-logging import. Normally it is a
// thin alias layer over the standard library's log/slog. In the legacy macOS
// 10.13 build (Go 1.20, `-tags legacy`, see docs/macos-legacy.md) it aliases
// golang.org/x/exp/slog instead, which has the same API. All types are
// aliases, so values are interchangeable with the underlying package.
//
// The split uses a custom tag rather than a `go1.21` constraint on purpose: a
// //go:build goX.Y line also sets that file's language version, which would
// downgrade it below the module's Go version.
//
// Import this package instead of "log/slog" in any code that is part of the
// legacy build. Only the names the bridge uses are re-exported; add more here
// (in both files) when needed.
package slog

import "log/slog"

type (
	Logger         = slog.Logger
	Handler        = slog.Handler
	HandlerOptions = slog.HandlerOptions
	Level          = slog.Level
)

const (
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

var (
	New            = slog.New
	NewTextHandler = slog.NewTextHandler
	Warn           = slog.Warn

	// DiscardHandler drops every record.
	DiscardHandler = slog.DiscardHandler
)
