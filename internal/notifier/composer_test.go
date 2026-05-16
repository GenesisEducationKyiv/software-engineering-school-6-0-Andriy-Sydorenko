package notifier

import (
	"strings"
	"testing"
)

const testBaseURL = "https://notify.example.com"

func TestComposerConfirmation(t *testing.T) {
	c := NewComposer(testBaseURL)

	msg, err := c.Confirmation("user@example.com", "golang/go", "ctoken", "utoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if msg.To != "user@example.com" {
		t.Errorf("To = %q, want user@example.com", msg.To)
	}
	if !strings.Contains(msg.Subject, "golang/go") {
		t.Errorf("Subject should mention repo, got %q", msg.Subject)
	}

	confirmURL := testBaseURL + "/api/confirm/ctoken"
	unsubURL := testBaseURL + "/api/unsubscribe/utoken"

	if !strings.Contains(msg.PlainBody, confirmURL) {
		t.Errorf("PlainBody missing confirm URL: %q", msg.PlainBody)
	}
	if !strings.Contains(msg.PlainBody, unsubURL) {
		t.Errorf("PlainBody missing unsubscribe URL: %q", msg.PlainBody)
	}
	if !strings.Contains(msg.PlainBody, "golang/go") {
		t.Errorf("PlainBody missing repo")
	}

	if msg.HTMLBody == "" {
		t.Fatal("HTMLBody should be rendered")
	}
	if !strings.Contains(msg.HTMLBody, confirmURL) {
		t.Errorf("HTMLBody missing confirm URL (href)")
	}

	// The display variant must include the ZWSP-broken URL so mail
	// clients don't auto-linkify the "copy this link" text.
	wantDisplay := strings.Replace(confirmURL, "://", zwsp+"://", 1)
	if !strings.Contains(msg.HTMLBody, wantDisplay) {
		t.Errorf("HTMLBody should contain ZWSP-broken display URL")
	}

	assertUnsubHeaders(t, msg.Headers, unsubURL)
}

func TestComposerRelease(t *testing.T) {
	c := NewComposer(testBaseURL)

	msg, err := c.Release("user@example.com", "golang/go", "v1.24.0", "utoken")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(msg.Subject, "v1.24.0") || !strings.Contains(msg.Subject, "golang/go") {
		t.Errorf("Subject = %q, missing tag or repo", msg.Subject)
	}

	releaseURL := "https://github.com/golang/go/releases/tag/v1.24.0"
	if !strings.Contains(msg.PlainBody, releaseURL) {
		t.Errorf("PlainBody missing release URL: %q", msg.PlainBody)
	}
	if !strings.Contains(msg.HTMLBody, releaseURL) {
		t.Errorf("HTMLBody missing release URL")
	}

	assertUnsubHeaders(t, msg.Headers, testBaseURL+"/api/unsubscribe/utoken")
}

func TestBreakAutoLink(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://example.com/path", "https" + zwsp + "://example.com/path"},
		{"http://x.y", "http" + zwsp + "://x.y"},
		// Only the first occurrence is broken — there is exactly one
		// scheme separator per URL we generate.
		{"no-scheme.example.com", "no-scheme.example.com"},
	}
	for _, tc := range cases {
		got := breakAutoLink(tc.in)
		if got != tc.want {
			t.Errorf("breakAutoLink(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func assertUnsubHeaders(t *testing.T, h map[string]string, unsubURL string) {
	t.Helper()
	if got := h["List-Unsubscribe"]; got != "<"+unsubURL+">" {
		t.Errorf("List-Unsubscribe = %q, want <%s>", got, unsubURL)
	}
	if got := h["List-Unsubscribe-Post"]; got != "List-Unsubscribe=One-Click" {
		t.Errorf("List-Unsubscribe-Post = %q", got)
	}
}
