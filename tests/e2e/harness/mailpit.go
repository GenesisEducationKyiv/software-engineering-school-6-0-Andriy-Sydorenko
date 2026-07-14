//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// CapturedMail is the subset of a Mailpit message tests typically need.
// Tokens are extracted from the plain-text body of the confirmation email.
type CapturedMail struct {
	ID           string
	To           string
	ConfirmToken string
	UnsubToken   string
}

var (
	confirmTokenRE = regexp.MustCompile(`/api/confirm/([A-Za-z0-9_\-]+)`)
	unsubTokenRE   = regexp.MustCompile(`/api/unsubscribe/([A-Za-z0-9_\-]+)`)
)

// WaitForMail polls Mailpit until at least one message addressed to toAddr is
// present, then returns it with confirm/unsub tokens extracted. Fails the test
// on timeout.
func (h *Harness) WaitForMail(t *testing.T, toAddr string, timeout time.Duration) CapturedMail {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		msg, ok := h.fetchLatestMessage(t, toAddr)
		if ok {
			return msg
		}
		if time.Now().After(deadline) {
			t.Fatalf("mailpit: no message addressed to %s within %s", toAddr, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (h *Harness) fetchLatestMessage(t *testing.T, toAddr string) (CapturedMail, bool) {
	t.Helper()
	searchURL := fmt.Sprintf("%s/api/v1/search?query=%s",
		h.MailpitURL, url.QueryEscape("to:"+toAddr))
	resp, err := http.Get(searchURL)
	require.NoError(t, err)
	defer resp.Body.Close()

	var search struct {
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&search))
	if len(search.Messages) == 0 {
		return CapturedMail{}, false
	}

	id := search.Messages[0].ID
	msgResp, err := http.Get(h.MailpitURL + "/api/v1/message/" + id)
	require.NoError(t, err)
	defer msgResp.Body.Close()

	var full struct {
		Text string `json:"Text"`
		HTML string `json:"HTML"`
	}
	require.NoError(t, json.NewDecoder(msgResp.Body).Decode(&full))

	body := full.Text
	if body == "" {
		body = full.HTML
	}

	out := CapturedMail{ID: id, To: toAddr}
	if m := confirmTokenRE.FindStringSubmatch(body); len(m) == 2 {
		out.ConfirmToken = m[1]
	}
	if m := unsubTokenRE.FindStringSubmatch(body); len(m) == 2 {
		out.UnsubToken = m[1]
	}
	return out, true
}
