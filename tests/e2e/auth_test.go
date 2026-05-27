//go:build e2e

package e2e

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/suite"

	"github.com/Andriy-Sydorenko/repo-release-notifier/tests/e2e/harness"
)

// AuthSuite has its own harness configured with a non-empty API key so the
// APIKeyAuth middleware actually enforces. The default suite leaves the key
// empty so browser-driven form tests work without a header.
type AuthSuite struct {
	harness.BaseSuite
}

func (s *AuthSuite) SetupSuite() {
	s.SetupSuiteWith(harness.Options{APIKey: harness.DefaultAPIKey})
}

func TestAuth(t *testing.T) {
	suite.Run(t, new(AuthSuite))
}

func (s *AuthSuite) TestSubscriptions_NoKey_401() {
	resp, err := http.Get(s.H.BaseURL + "/api/subscriptions")
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusUnauthorized, resp.StatusCode)
}

func (s *AuthSuite) TestSubscriptions_WrongKey_403() {
	req, err := http.NewRequest(http.MethodGet, s.H.BaseURL+"/api/subscriptions", nil)
	s.Require().NoError(err)
	req.Header.Set("X-API-Key", "not-the-key")
	resp, err := http.DefaultClient.Do(req)
	s.Require().NoError(err)
	defer resp.Body.Close()
	s.Equal(http.StatusForbidden, resp.StatusCode)
}
