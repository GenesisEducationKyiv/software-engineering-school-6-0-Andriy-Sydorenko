//go:build e2e

package harness_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/suite"

	"github.com/Andriy-Sydorenko/repo-release-notifier/tests/e2e/harness"
)

type SmokeSuite struct {
	harness.BaseSuite
}

func TestSmoke(t *testing.T) {
	suite.Run(t, new(SmokeSuite))
}

func (s *SmokeSuite) TestHealth() {
	resp, err := http.Get(s.H.BaseURL + "/health")
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusOK, resp.StatusCode)
}

func (s *SmokeSuite) TestSubscribeRoundTripsMailThroughMailpit() {
	email := fmt.Sprintf("smoke+%d@example.com", time.Now().UnixNano())

	body, _ := json.Marshal(map[string]string{
		"email": email,
		"repo":  "golang/go",
	})
	req, _ := http.NewRequest(http.MethodPost, s.H.BaseURL+"/api/subscribe", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.H.APIKey)

	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	respBody, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	s.Require().Equalf(http.StatusOK, resp.StatusCode, "body: %s", respBody)

	// Mail send is synchronous in this code path, but poll briefly to be safe.
	s.Require().Eventually(func() bool {
		return mailpitCount(s.T(), s.H.MailpitURL, email) >= 1
	}, 3*time.Second, 50*time.Millisecond, "mailpit never received message for %s", email)
}

func mailpitCount(t *testing.T, mailpitURL, toAddr string) int {
	t.Helper()
	url := mailpitURL + "/api/v1/search?query=" + "to%3A" + toAddr
	resp, err := http.Get(url)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var payload struct {
		MessagesCount int `json:"messages_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return 0
	}
	return payload.MessagesCount
}
