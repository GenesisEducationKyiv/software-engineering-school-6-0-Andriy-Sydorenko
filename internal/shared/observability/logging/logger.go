package logging

import (
	"io"
	"log/slog"
)

func NewLogger(cfg *Config, w io.Writer) *slog.Logger {
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
	return slog.New(contextHandler{h})
}
