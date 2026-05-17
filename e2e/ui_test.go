//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/suite"

	"github.com/Andriy-Sydorenko/repo-release-notifier/e2e/harness"
)

// pw + browser are owned by TestMain so all UI suites share one Chromium
// process. Per-test isolation comes from a fresh BrowserContext + Page.
var (
	pw      *playwright.Playwright
	browser playwright.Browser
	expect  playwright.PlaywrightAssertions
)

func TestMain(m *testing.M) {
	var err error
	pw, err = playwright.Run()
	if err != nil {
		log.Fatalf("playwright run: %v", err)
	}
	browser, err = pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(true),
	})
	if err != nil {
		log.Fatalf("chromium launch: %v", err)
	}
	expect = playwright.NewPlaywrightAssertions(5000)

	code := m.Run()

	_ = browser.Close()
	_ = pw.Stop()
	os.Exit(code)
}

type SubscribeSuite struct {
	harness.BaseSuite
}

func TestSubscribe(t *testing.T) {
	suite.Run(t, new(SubscribeSuite))
}

// page opens a fresh browser context + page bound to the harness BaseURL.
// Cleanup is wired to the test, not the suite, so per-test isolation holds.
func (s *SubscribeSuite) page() playwright.Page {
	t := s.T()
	t.Helper()
	ctx, err := browser.NewContext(playwright.BrowserNewContextOptions{
		BaseURL: playwright.String(s.H.BaseURL),
	})
	s.Require().NoError(err)
	t.Cleanup(func() { _ = ctx.Close() })

	p, err := ctx.NewPage()
	s.Require().NoError(err)
	return p
}

func uniqueEmail(prefix string) string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("%s+%d-%s@example.com", prefix, time.Now().UnixNano(), hex.EncodeToString(b))
}

// expectNoSubscribeRequest asserts that running action does NOT trigger
// a POST /api/subscribe (HTML5 form validation should block client-side).
func expectNoSubscribeRequest(t *testing.T, p playwright.Page, action func() error) {
	t.Helper()
	_, err := p.ExpectRequest("**/api/subscribe", func() error {
		return action()
	}, playwright.PageExpectRequestOptions{Timeout: playwright.Float(500)})
	if err == nil {
		t.Fatalf("expected no /api/subscribe request, but one was fired")
	}
	if !strings.Contains(err.Error(), "Timeout") && !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected timeout waiting for request, got: %v", err)
	}
}

func emailField(p playwright.Page) playwright.Locator {
	return p.GetByLabel(regexp.MustCompile(`(?i)your email`))
}

func repoField(p playwright.Page) playwright.Locator {
	return p.GetByLabel(regexp.MustCompile(`(?i)github repository`))
}

func submitBtn(p playwright.Page) playwright.Locator {
	return p.GetByRole("button", playwright.PageGetByRoleOptions{
		Name: regexp.MustCompile(`(?i)^subscribe$`),
	})
}

func statusEl(p playwright.Page) playwright.Locator {
	return p.Locator("#status")
}

func goHome(t *testing.T, p playwright.Page) {
	t.Helper()
	if _, err := p.Goto("/"); err != nil {
		t.Fatalf("goto /: %v", err)
	}
}

func (s *SubscribeSuite) TestRendersForm() {
	t := s.T()
	p := s.page()
	goHome(t, p)

	s.Require().NoError(expect.Page(p).ToHaveTitle(regexp.MustCompile(`(?i)Subscribe to GitHub release notifications`)))
	s.Require().NoError(expect.Locator(p.GetByRole("heading", playwright.PageGetByRoleOptions{
		Name: regexp.MustCompile(`(?i)Subscribe to GitHub release notifications`),
	})).ToBeVisible())
	s.Require().NoError(expect.Locator(emailField(p)).ToBeVisible())
	s.Require().NoError(expect.Locator(repoField(p)).ToBeVisible())
	s.Require().NoError(expect.Locator(p.GetByLabel(regexp.MustCompile(`(?i)api key`))).ToBeVisible())
	s.Require().NoError(expect.Locator(submitBtn(p)).ToBeEnabled())
}

func (s *SubscribeSuite) TestHappyPath() {
	t := s.T()
	p := s.page()
	goHome(t, p)

	s.Require().NoError(emailField(p).Fill(uniqueEmail("happy")))
	s.Require().NoError(repoField(p).Fill("golang/go"))

	resp, err := p.ExpectResponse("**/api/subscribe", func() error {
		return submitBtn(p).Click()
	})
	s.Require().NoError(err)
	s.Require().Equal(200, resp.Status())

	s.Require().NoError(expect.Locator(statusEl(p)).ToHaveClass(regexp.MustCompile(`\bok\b`)))
	s.Require().NoError(expect.Locator(statusEl(p)).ToContainText(regexp.MustCompile(`(?i)check your inbox`)))
	s.Require().NoError(expect.Locator(emailField(p)).ToHaveValue(""))
	s.Require().NoError(expect.Locator(repoField(p)).ToHaveValue(""))
}

func (s *SubscribeSuite) TestDuplicate() {
	t := s.T()
	p := s.page()
	goHome(t, p)

	email := uniqueEmail("dup")
	for _, want := range []int{200, 409} {
		s.Require().NoError(emailField(p).Fill(email))
		s.Require().NoError(repoField(p).Fill("kubernetes/kubernetes"))
		resp, err := p.ExpectResponse("**/api/subscribe", func() error {
			return submitBtn(p).Click()
		})
		s.Require().NoError(err)
		s.Require().Equal(want, resp.Status())
	}

	s.Require().NoError(expect.Locator(statusEl(p)).ToHaveClass(regexp.MustCompile(`\berr\b`)))
	s.Require().NoError(expect.Locator(statusEl(p)).ToContainText(regexp.MustCompile(`(?i)already subscribed`)))
}

func (s *SubscribeSuite) TestRepoNotFound() {
	t := s.T()
	// Pre-stage: tell the GitHub fixture to 404 for owner "ghost".
	s.H.GitHub.SetBehavior("ghost", harness.GHNotFound)

	p := s.page()
	goHome(t, p)

	s.Require().NoError(emailField(p).Fill(uniqueEmail("notfound")))
	s.Require().NoError(repoField(p).Fill("ghost/missing"))

	resp, err := p.ExpectResponse("**/api/subscribe", func() error {
		return submitBtn(p).Click()
	})
	s.Require().NoError(err)
	s.Require().Equal(404, resp.Status())

	s.Require().NoError(expect.Locator(statusEl(p)).ToHaveClass(regexp.MustCompile(`\berr\b`)))
	s.Require().NoError(expect.Locator(statusEl(p)).ToContainText(regexp.MustCompile(`(?i)repository not found`)))
}

func (s *SubscribeSuite) TestHTML5Validation() {
	t := s.T()
	p := s.page()
	goHome(t, p)

	s.Require().NoError(emailField(p).Fill(uniqueEmail("badrepo")))
	s.Require().NoError(repoField(p).Fill("no-slash-here"))
	expectNoSubscribeRequest(t, p, func() error { return submitBtn(p).Click() })

	s.Require().NoError(emailField(p).Fill("not-an-email"))
	s.Require().NoError(repoField(p).Fill("golang/go"))
	expectNoSubscribeRequest(t, p, func() error { return submitBtn(p).Click() })
}

func (s *SubscribeSuite) TestNetworkFailure() {
	t := s.T()
	p := s.page()
	goHome(t, p)

	s.Require().NoError(p.Route("**/api/subscribe", func(route playwright.Route) {
		_ = route.Abort("failed")
	}))

	s.Require().NoError(emailField(p).Fill(uniqueEmail("net")))
	s.Require().NoError(repoField(p).Fill("golang/go"))
	s.Require().NoError(submitBtn(p).Click())

	s.Require().NoError(expect.Locator(statusEl(p)).ToHaveClass(regexp.MustCompile(`\berr\b`)))
	s.Require().NoError(expect.Locator(statusEl(p)).ToContainText(regexp.MustCompile(`(?i)network error`)))
}
