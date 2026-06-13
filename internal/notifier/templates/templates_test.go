package templates_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier/templates"
)

func TestRenderEmail_confirmation(t *testing.T) {
	html, err := templates.RenderEmail("confirmation.html", map[string]string{
		"Repo":              "golang/go",
		"ConfirmURL":        "https://x/api/confirm/tok",
		"ConfirmURLDisplay": "https://x/api/confirm/tok",
		"UnsubscribeURL":    "https://x/api/unsubscribe/u",
	})
	require.NoError(t, err)
	assert.Contains(t, html, "golang/go")
	assert.Contains(t, html, "https://x/api/confirm/tok")
}

func TestRenderEmail_release(t *testing.T) {
	html, err := templates.RenderEmail("release.html", map[string]string{
		"Repo":           "golang/go",
		"Tag":            "v1.24.0",
		"ReleaseURL":     "https://github.com/golang/go/releases/tag/v1.24.0",
		"UnsubscribeURL": "https://x/api/unsubscribe/u",
	})
	require.NoError(t, err)
	assert.Contains(t, html, "v1.24.0")
}

func TestRenderEmail_unknownTemplate(t *testing.T) {
	_, err := templates.RenderEmail("missing.html", nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "missing.html"))
}
