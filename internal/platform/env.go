package platform

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

// GetOrDefault returns the env var value, or fallback if unset/empty.
func GetOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// MustGet returns the env var value, or an error if unset/empty. Callers
// decide whether a missing value is fatal (unlike config's panic helpers).
func MustGet(key string) (string, error) {
	if v := os.Getenv(key); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("required env var %s is not set", key)
}

// GetDuration parses a duration env var, falling back when unset. A malformed
// value panics: the operator set it on purpose and got the format wrong;
// silently using the default would hide the bug.
func GetDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		panic(fmt.Sprintf("env: invalid %s %q: %v", key, v, err))
	}
	return d
}

// GetInt parses an int env var, falling back when unset. A malformed value
// panics, mirroring GetDuration: an operator typo must surface loudly rather
// than silently fall back to the default.
func GetInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		panic(fmt.Sprintf("env: invalid %s %q: %v", key, v, err))
	}
	return n
}
