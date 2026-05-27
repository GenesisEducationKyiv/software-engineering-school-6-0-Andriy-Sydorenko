package logging

import (
	"io"
	"log/slog"
)

// NewLogger builds a *slog.Logger from cfg, writing to w. The Format
// switch defaults to JSON as a safety net if an unvalidated Config slips
// through (internal/config validates during LoadConfig).
func NewLogger(cfg Config, w io.Writer) *slog.Logger {
	opts := &slog.HandlerOptions{
		Level:     cfg.slogLevel(),
		AddSource: cfg.AddSource(),
	}
	var h slog.Handler
	switch cfg.Format {
	case FormatText:
		h = NewTextHandler(w, opts)
	default:
		h = slog.NewJSONHandler(w, opts)
	}
	return slog.New(h)
}
