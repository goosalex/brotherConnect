//go:build legacy

package slog

import (
	"context"

	"golang.org/x/exp/slog"
)

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

	// DiscardHandler drops every record (log/slog.DiscardHandler is Go 1.24+
	// and has no x/exp equivalent).
	DiscardHandler Handler = discardHandler{}
)

type discardHandler struct{}

func (discardHandler) Enabled(context.Context, slog.Level) bool  { return false }
func (discardHandler) Handle(context.Context, slog.Record) error { return nil }
func (d discardHandler) WithAttrs([]slog.Attr) slog.Handler      { return d }
func (d discardHandler) WithGroup(string) slog.Handler           { return d }
