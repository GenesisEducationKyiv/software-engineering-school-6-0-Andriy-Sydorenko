//go:build e2e

package e2e

import (
	"regexp"

	"github.com/Andriy-Sydorenko/repo-release-notifier/tests/e2e/harness"
)

// TestSubscribe_RateLimited drives the form against a repo whose owner the
// GitHub fixture serves as rate-limited. Proves the UI surfaces *some*
// error to the user; per-error status-code mapping lives in service unit tests.
func (s *SubscribeSuite) TestSubscribe_RateLimited() {
	s.H.GitHub.SetBehavior("rate", harness.GHRateLimited)
	s.submitAndExpectError("rate/limited", regexp.MustCompile(`(?i)rate.?limit|try again`))
}

// TestSubscribe_ServerError covers the GitHub 5xx branch.
func (s *SubscribeSuite) TestSubscribe_ServerError() {
	s.H.GitHub.SetBehavior("boom", harness.GHServerError)
	s.submitAndExpectError("boom/anything", regexp.MustCompile(`(?i)error|unavailable|failed`))
}

func (s *SubscribeSuite) submitAndExpectError(repo string, msg *regexp.Regexp) {
	t := s.T()
	p := s.page()
	goHome(t, p)

	s.Require().NoError(emailField(p).Fill(uniqueEmail("ghfail")))
	s.Require().NoError(repoField(p).Fill(repo))

	resp, err := p.ExpectResponse("**/api/subscribe", func() error {
		return submitBtn(p).Click()
	})
	s.Require().NoError(err)
	s.Require().GreaterOrEqual(resp.Status(), 400, "expected an error status from /api/subscribe")

	s.Require().NoError(expect.Locator(statusEl(p)).ToHaveClass(regexp.MustCompile(`\berr\b`)))
	s.Require().NoError(expect.Locator(statusEl(p)).ToContainText(msg))
}
