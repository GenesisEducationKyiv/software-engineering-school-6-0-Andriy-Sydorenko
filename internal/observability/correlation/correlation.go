// Package correlation carries a request-scoped correlation ID through context.
// It is a dependency-free leaf: logging and the gRPC interceptors import it to
// stamp every log line and propagate the ID across service boundaries.
package correlation

import (
	"context"

	"github.com/google/uuid"
)

type ctxKey struct{}

func WithID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

func FromContext(ctx context.Context) string {
	if id, ok := ctx.Value(ctxKey{}).(string); ok {
		return id
	}
	return ""
}

func NewID() string {
	return uuid.NewString()
}
