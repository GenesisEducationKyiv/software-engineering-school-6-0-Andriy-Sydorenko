package logging

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var fixedTime = time.Date(2026, 5, 27, 23, 29, 42, 0, time.UTC)

// newHandler returns a handler writing to buf. buf is not an *os.File, so
// shouldColor reports false and output is plain (deterministic) text.
func newHandler(t *testing.T, buf *bytes.Buffer, level slog.Level) *TextHandler {
	t.Helper()
	h := NewTextHandler(buf, &slog.HandlerOptions{Level: level})
	require.False(t, h.color, "color must be off for a bytes.Buffer")
	return h
}

func handle(t *testing.T, h slog.Handler, rec slog.Record) string {
	t.Helper()
	require.NoError(t, h.Handle(context.Background(), rec))
	return h.(*TextHandler).w.(*bytes.Buffer).String()
}

func TestTextHandler_BasicLine(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(t, &buf, slog.LevelInfo)

	rec := slog.NewRecord(fixedTime, slog.LevelInfo, "hello", 0)
	rec.Add("count", 3)

	out := handle(t, h, rec)
	assert.Equal(t, "[INFO ] 2026/05/27 - 23:29:42 | hello | count=3\n", out)
}

func TestTextHandler_LevelLabels(t *testing.T) {
	cases := []struct {
		level slog.Level
		label string
	}{
		{slog.LevelDebug, "[DEBUG]"},
		{slog.LevelInfo, "[INFO ]"},
		{slog.LevelWarn, "[WARN ]"},
		{slog.LevelError, "[ERROR]"},
	}
	for _, tc := range cases {
		t.Run(
			tc.label, func(t *testing.T) {
				var buf bytes.Buffer
				h := newHandler(t, &buf, slog.LevelDebug)
				out := handle(t, h, slog.NewRecord(fixedTime, tc.level, "m", 0))
				assert.True(t, strings.HasPrefix(out, tc.label), "got %q", out)
			},
		)
	}
}

func TestTextHandler_ErrChainUnwinds(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(t, &buf, slog.LevelInfo)

	wrapped := fmt.Errorf("scanner: %w", fmt.Errorf("fetch: %w", errors.New("rate limited")))
	rec := slog.NewRecord(fixedTime, slog.LevelError, "repo check failed", 0)
	rec.Add("repo", "foo/bar", "err", wrapped)

	out := handle(t, h, rec)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	require.Len(t, lines, 4)

	assert.Contains(t, lines[0], "repo check failed")
	assert.Contains(t, lines[0], "repo=foo/bar")
	assert.NotContains(t, lines[0], "err=")
	assert.Equal(t, "        err: scanner: fetch: rate limited", lines[1])
	assert.Equal(t, "             fetch: rate limited", lines[2])
	assert.Equal(t, "             rate limited", lines[3])
}

type secret struct{ token string }

func (secret) LogValue() slog.Value { return slog.StringValue("REDACTED") }

// The handler must resolve LogValuer attrs so a redacting value masks its
// underlying secret instead of printing the raw struct.
func TestTextHandler_ResolvesLogValuer(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(t, &buf, slog.LevelInfo)

	rec := slog.NewRecord(fixedTime, slog.LevelInfo, "auth", 0)
	rec.Add("apikey", secret{token: "hunter2"})

	out := handle(t, h, rec)
	assert.Contains(t, out, "apikey=REDACTED")
	assert.NotContains(t, out, "hunter2")
}

func TestTextHandler_DropsEmptyKey(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(t, &buf, slog.LevelInfo)

	rec := slog.NewRecord(fixedTime, slog.LevelInfo, "m", 0)
	rec.AddAttrs(slog.String("", "ghost"), slog.String("kept", "v"))

	out := handle(t, h, rec)
	assert.NotContains(t, out, "ghost")
	assert.Contains(t, out, "kept=v")
}

func TestTextHandler_WithAttrsAccumulates(t *testing.T) {
	var buf bytes.Buffer
	h := newHandler(t, &buf, slog.LevelInfo)

	child := h.WithAttrs([]slog.Attr{slog.String("svc", "scanner")})
	rec := slog.NewRecord(fixedTime, slog.LevelInfo, "m", 0)
	rec.Add("repo", "foo/bar")

	require.NoError(t, child.Handle(context.Background(), rec))
	out := buf.String()
	assert.Contains(t, out, "svc=scanner")
	assert.Contains(t, out, "repo=foo/bar")
}

func TestTextHandler_WithAttrsEmptyReturnsReceiver(t *testing.T) {
	h := newHandler(t, &bytes.Buffer{}, slog.LevelInfo)
	assert.Same(t, h, h.WithAttrs(nil), "empty WithAttrs must not allocate a new handler")
}

func TestTextHandler_Enabled(t *testing.T) {
	h := newHandler(t, &bytes.Buffer{}, slog.LevelWarn)
	ctx := context.Background()
	assert.False(t, h.Enabled(ctx, slog.LevelInfo))
	assert.True(t, h.Enabled(ctx, slog.LevelWarn))
	assert.True(t, h.Enabled(ctx, slog.LevelError))
}

func TestShouldColor(t *testing.T) {
	t.Run(
		"NO_COLOR forces off", func(t *testing.T) {
			t.Setenv("NO_COLOR", "1")
			t.Setenv("FORCE_COLOR", "1") // NO_COLOR wins
			assert.False(t, shouldColor(&bytes.Buffer{}))
		},
	)
	t.Run(
		"FORCE_COLOR forces on", func(t *testing.T) {
			t.Setenv("FORCE_COLOR", "1")
			assert.True(t, shouldColor(&bytes.Buffer{}))
		},
	)
	t.Run(
		"non-file writer is off", func(t *testing.T) {
			assert.False(t, shouldColor(&bytes.Buffer{}))
		},
	)
}
