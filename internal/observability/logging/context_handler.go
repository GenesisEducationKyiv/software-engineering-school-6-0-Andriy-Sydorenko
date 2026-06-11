package logging

import (
	"context"
	"log/slog"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/correlation"
)

type ContextHandler struct {
	inner slog.Handler
}

func (h ContextHandler) Enabled(ctx context.Context, lvl slog.Level) bool {
	return h.inner.Enabled(ctx, lvl)
}

func (h ContextHandler) Handle(ctx context.Context, r slog.Record) error {
	if id := correlation.FromContext(ctx); id != "" {
		r.AddAttrs(slog.String("correlation_id", id))
	}
	return h.inner.Handle(ctx, r)
}

func (h ContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return ContextHandler{inner: h.inner.WithAttrs(attrs)}
}

func (h ContextHandler) WithGroup(name string) slog.Handler {
	return ContextHandler{inner: h.inner.WithGroup(name)}
}
