package correlation_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/correlation"
)

func TestFromContext_emptyWhenUnset(t *testing.T) {
	assert.Equal(t, "", correlation.FromContext(context.Background()))
}

func TestWithIDRoundTrip(t *testing.T) {
	ctx := correlation.WithID(context.Background(), "abc-123")
	assert.Equal(t, "abc-123", correlation.FromContext(ctx))
}

func TestNewID_uniqueNonEmpty(t *testing.T) {
	a, b := correlation.NewID(), correlation.NewID()
	require.NotEmpty(t, a)
	assert.NotEqual(t, a, b)
}
