package service

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const testBaseURL = "https://notify.example.com"

func TestComposerConfirmation(t *testing.T) {
	c := NewComposer(testBaseURL)

	msg, err := c.Confirmation("user@example.com", "golang/go", "ctoken", "utoken")
	require.NoError(t, err)

	assert.Equal(t, "user@example.com", msg.To)
	assert.Contains(t, msg.Subject, "golang/go")

	confirmURL := testBaseURL + "/api/confirm/ctoken"
	unsubURL := testBaseURL + "/api/unsubscribe/utoken"

	assert.Contains(t, msg.PlainBody, confirmURL)
	assert.Contains(t, msg.PlainBody, unsubURL)
	assert.Contains(t, msg.PlainBody, "golang/go")

	require.NotEmpty(t, msg.HTMLBody)
	assert.Contains(t, msg.HTMLBody, confirmURL)

	// ZWSP-broken URL: prevents mail clients from auto-linkifying the "copy this link" text.
	wantDisplay := strings.Replace(confirmURL, "://", zwsp+"://", 1)
	assert.Contains(t, msg.HTMLBody, wantDisplay)

	assertUnsubHeaders(t, msg.Headers, unsubURL)
}

func TestComposerRelease(t *testing.T) {
	c := NewComposer(testBaseURL)

	msg, err := c.Release("user@example.com", "golang/go", "v1.24.0", "utoken")
	require.NoError(t, err)

	assert.Contains(t, msg.Subject, "v1.24.0")
	assert.Contains(t, msg.Subject, "golang/go")

	releaseURL := "https://github.com/golang/go/releases/tag/v1.24.0"
	assert.Contains(t, msg.PlainBody, releaseURL)
	assert.Contains(t, msg.HTMLBody, releaseURL)

	assertUnsubHeaders(t, msg.Headers, testBaseURL+"/api/unsubscribe/utoken")
}

func TestBreakAutoLink(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://example.com/path", "https" + zwsp + "://example.com/path"},
		{"http://x.y", "http" + zwsp + "://x.y"},
		{"no-scheme.example.com", "no-scheme.example.com"},
	}
	for _, tc := range cases {
		assert.Equal(t, tc.want, breakAutoLink(tc.in))
	}
}

func assertUnsubHeaders(t *testing.T, h map[string]string, unsubURL string) {
	t.Helper()
	assert.Equal(t, "<"+unsubURL+">", h["List-Unsubscribe"])
	assert.Equal(t, "List-Unsubscribe=One-Click", h["List-Unsubscribe-Post"])
}
