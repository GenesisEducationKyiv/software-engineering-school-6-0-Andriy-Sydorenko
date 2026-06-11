package platform_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/platform"
)

func TestGetOrDefault(t *testing.T) {
	t.Setenv("FOO", "bar")
	assert.Equal(t, "bar", platform.GetOrDefault("FOO", "fallback"))
	assert.Equal(t, "fallback", platform.GetOrDefault("MISSING", "fallback"))
}

func TestMustGet(t *testing.T) {
	t.Setenv("TOKEN", "secret")
	got, err := platform.MustGet("TOKEN")
	require.NoError(t, err)
	assert.Equal(t, "secret", got)

	_, err = platform.MustGet("ABSENT")
	require.Error(t, err)
}

func TestGetDuration(t *testing.T) {
	t.Setenv("DUR", "250ms")
	assert.Equal(t, 250*time.Millisecond, platform.GetDuration("DUR", time.Second))
	assert.Equal(t, time.Second, platform.GetDuration("DUR_MISSING", time.Second))
}

func TestGetDurationPanicsOnInvalid(t *testing.T) {
	t.Setenv("DUR_BAD", "not-a-duration")
	// Panic must name the env key — the operator's only signal to find the typo.
	defer func() {
		r := recover()
		require.NotNil(t, r, "expected panic on malformed duration")
		assert.Contains(t, r.(string), "DUR_BAD")
	}()
	_ = platform.GetDuration("DUR_BAD", time.Second)
}

func TestGetInt(t *testing.T) {
	t.Setenv("INT", "17")
	assert.Equal(t, 17, platform.GetInt("INT", 1))
	assert.Equal(t, 42, platform.GetInt("INT_MISSING", 42))
}

func TestGetIntPanicsOnInvalid(t *testing.T) {
	t.Setenv("INT_BAD", "abc")
	assert.Panics(t, func() { _ = platform.GetInt("INT_BAD", 1) })
}
