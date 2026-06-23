//go:build e2e

package e2e

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

// SubscribeSuite drives the full subscribe saga through the real services:
// POST orchestrator/subscribe → confirmation email in Mailpit → confirm/unsubscribe
// on the subscription service. State is verified through behavior (the email + the
// one-shot tokens), not DB reads — row-level asserts live in the integration tier.
type SubscribeSuite struct {
	harness.BaseSuite
}

func TestSubscribe(t *testing.T) {
	suite.Run(t, new(SubscribeSuite))
}

// subscribe POSTs to the orchestrator's public /subscribe endpoint.
func (s *SubscribeSuite) subscribe(email, repo string) (int, string) {
	s.T().Helper()
	body, _ := json.Marshal(map[string]string{"email": email, "repo": repo})
	resp, err := http.Post(s.H.OrchestratorURL+"/subscribe", "application/json", bytes.NewReader(body))
	s.Require().NoError(err)
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(rb)
}

func (s *SubscribeSuite) TestLifecycle() {
	email := fmt.Sprintf("lifecycle+%d@example.com", time.Now().UnixNano())

	status, body := s.subscribe(email, "golang/go")
	s.Require().Equalf(http.StatusOK, status, "subscribe body: %s", body)

	// The orchestrator committed and emitted the confirmation event; the
	// subscription service rendered it and the notifier delivered it to Mailpit.
	mail := s.H.WaitForMail(s.T(), email, 5*time.Second)
	s.Require().NotEmpty(mail.ConfirmToken, "confirm token not found in email")
	s.Require().NotEmpty(mail.UnsubToken, "unsub token not found in email")

	confirmResp, err := http.Get(s.H.AppURL + "/api/confirm/" + mail.ConfirmToken)
	s.Require().NoError(err)
	confirmResp.Body.Close()
	s.Require().Equal(http.StatusOK, confirmResp.StatusCode)

	unsubResp, err := http.Get(s.H.AppURL + "/api/unsubscribe/" + mail.UnsubToken)
	s.Require().NoError(err)
	unsubResp.Body.Close()
	s.Require().Equal(http.StatusOK, unsubResp.StatusCode)

	// Token is one-shot — the state change is verified through behavior.
	again, err := http.Get(s.H.AppURL + "/api/unsubscribe/" + mail.UnsubToken)
	s.Require().NoError(err)
	again.Body.Close()
	s.Require().NotEqual(http.StatusOK, again.StatusCode, "unsub token should be one-shot")
}

func (s *SubscribeSuite) TestBadRepo_AbortsWithNoEmail() {
	s.H.GitHub.SetBehavior("ghost", harness.GHNotFound)

	email := fmt.Sprintf("bad+%d@example.com", time.Now().UnixNano())
	status, _ := s.subscribe(email, "ghost/ghost")

	s.Require().Equal(http.StatusNotFound, status)

	var subs int64
	s.Require().NoError(s.H.SubDB.Raw(`SELECT COUNT(*) FROM subscriptions`).Scan(&subs).Error)
	s.Require().Equal(int64(0), subs, "no subscription should be created for a bad repo")
}
