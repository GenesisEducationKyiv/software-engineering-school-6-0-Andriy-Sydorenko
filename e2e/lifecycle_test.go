//go:build e2e

package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// apiSubscribe POSTs to /api/subscribe and returns the status code + body.
func (s *SubscribeSuite) apiSubscribe(email, repo string) (int, string) {
	t := s.T()
	t.Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "repo": repo})
	req, _ := http.NewRequest(http.MethodPost, s.H.BaseURL+"/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if s.H.APIKey != "" {
		req.Header.Set("X-API-Key", s.H.APIKey)
	}
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(rb)
}

// TestLifecycle exercises subscribe → confirm (via token from real email)
// → unsubscribe (via token from same email). Proves Mailpit round-trip,
// DB state changes, and the cross-endpoint token contract all work.
func (s *SubscribeSuite) TestLifecycle() {
	email := fmt.Sprintf("lifecycle+%d@example.com", time.Now().UnixNano())
	repo := "golang/go"

	status, body := s.apiSubscribe(email, repo)
	s.Require().Equalf(http.StatusOK, status, "subscribe body: %s", body)

	mail := s.H.WaitForMail(s.T(), email, 3*time.Second)
	s.Require().NotEmpty(mail.ConfirmToken, "confirm token not found in email body")
	s.Require().NotEmpty(mail.UnsubToken, "unsubscribe token not found in email body")

	confirmResp, err := http.Get(s.H.BaseURL + "/api/confirm/" + mail.ConfirmToken)
	s.Require().NoError(err)
	confirmResp.Body.Close()
	s.Require().Equal(http.StatusOK, confirmResp.StatusCode)

	unsubResp, err := http.Get(s.H.BaseURL + "/api/unsubscribe/" + mail.UnsubToken)
	s.Require().NoError(err)
	unsubResp.Body.Close()
	s.Require().Equal(http.StatusOK, unsubResp.StatusCode)

	// Token can no longer be used a second time — proves DB state changed.
	again, err := http.Get(s.H.BaseURL + "/api/unsubscribe/" + mail.UnsubToken)
	s.Require().NoError(err)
	again.Body.Close()
	s.Require().NotEqual(http.StatusOK, again.StatusCode, "unsub token should be one-shot")
}
