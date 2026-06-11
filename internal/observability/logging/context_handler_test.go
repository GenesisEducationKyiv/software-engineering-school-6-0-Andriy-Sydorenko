package logging_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/correlation"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/observability/logging"
)

func TestNewLogger_injectsCorrelationID(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{Format: logging.FormatJSON, Level: logging.LevelInfo}, &buf)

	ctx := correlation.WithID(context.Background(), "corr-xyz")
	logger.InfoContext(ctx, "hello")

	assert.Contains(t, buf.String(), "corr-xyz")
	assert.Contains(t, buf.String(), "correlation_id")
}

func TestNewLogger_noIDWhenAbsent(t *testing.T) {
	var buf bytes.Buffer
	logger := logging.NewLogger(logging.Config{Format: logging.FormatJSON, Level: logging.LevelInfo}, &buf)

	logger.InfoContext(context.Background(), "hello")

	assert.False(t, strings.Contains(buf.String(), "correlation_id"))
}
