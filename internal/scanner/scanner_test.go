package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

type mockScannerRepo struct {
	repos          []string
	reposErr       error
	subsByRepo     map[string][]domain.Subscription
	subsErr        error
	updatedTags    map[uint]string
	updateTagErr   error
	reposCalls     int
	subsCalls      int
	updateTagCalls int
}

func newMockScannerRepo() *mockScannerRepo {
	return &mockScannerRepo{
		subsByRepo:  map[string][]domain.Subscription{},
		updatedTags: map[uint]string{},
	}
}

func (m *mockScannerRepo) FindDistinctConfirmedRepos(_ context.Context) ([]string, error) {
	m.reposCalls++
	return m.repos, m.reposErr
}

func (m *mockScannerRepo) FindConfirmedSubscriptionsByRepo(_ context.Context, repo string) ([]domain.Subscription, error) {
	m.subsCalls++
	if m.subsErr != nil {
		return nil, m.subsErr
	}
	return m.subsByRepo[repo], nil
}

func (m *mockScannerRepo) UpdateLastSeenTag(_ context.Context, id uint, tag string) error {
	m.updateTagCalls++
	if m.updateTagErr != nil {
		return m.updateTagErr
	}
	m.updatedTags[id] = tag
	return nil
}

type mockFetcher struct {
	tags  map[string]string
	errs  map[string]error
	calls int
}

func (m *mockFetcher) GetLatestRelease(_ context.Context, owner, repo string) (string, error) {
	m.calls++
	key := owner + "/" + repo
	if err := m.errs[key]; err != nil {
		return "", err
	}
	return m.tags[key], nil
}

type mockReleaseNotifier struct {
	err  error
	sent []sentRelease
}

type sentRelease struct {
	email, repo, tag, unsubToken string
}

func (m *mockReleaseNotifier) SendReleaseNotification(email, repo, tag, unsubscribeToken string) error {
	m.sent = append(m.sent, sentRelease{email, repo, tag, unsubscribeToken})
	return m.err
}

func newScanner(repo *mockScannerRepo, fetcher *mockFetcher, notifier *mockReleaseNotifier) *Scanner {
	return New(repo, fetcher, notifier, time.Minute)
}

func TestNewReleaseNotifiesAll(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"golang/go"}
	repo.subsByRepo["golang/go"] = []domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0", UnsubscribeToken: "uA"},
		{ID: 2, Email: "b@x.com", Repo: "golang/go", LastSeenTag: "v1.0", UnsubscribeToken: "uB"},
	}
	fetcher := &mockFetcher{tags: map[string]string{"golang/go": "v1.1"}}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if len(notifier.sent) != 2 {
		t.Fatalf("want 2 notifications, got %d", len(notifier.sent))
	}
	if repo.updatedTags[1] != "v1.1" || repo.updatedTags[2] != "v1.1" {
		t.Fatalf("tags not updated: %v", repo.updatedTags)
	}
	if notifier.sent[0].unsubToken != "uA" {
		t.Fatalf("unsub token missing: %+v", notifier.sent[0])
	}
}

func TestSameTagNoNotification(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"golang/go"}
	repo.subsByRepo["golang/go"] = []domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0"},
	}
	fetcher := &mockFetcher{tags: map[string]string{"golang/go": "v1.0"}}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if len(notifier.sent) != 0 {
		t.Fatalf("want 0 notifications, got %d", len(notifier.sent))
	}
	if repo.updateTagCalls != 0 {
		t.Fatalf("want no tag updates, got %d", repo.updateTagCalls)
	}
}

func TestFirstBaselineSilent(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"golang/go"}
	repo.subsByRepo["golang/go"] = []domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: ""},
	}
	fetcher := &mockFetcher{tags: map[string]string{"golang/go": "v1.1"}}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if len(notifier.sent) != 0 {
		t.Fatalf("want silent baseline, got %d notifications", len(notifier.sent))
	}
	if repo.updatedTags[1] != "v1.1" {
		t.Fatalf("baseline tag not recorded: %v", repo.updatedTags)
	}
}

func TestEmptyTagSkips(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"owner/empty"}
	repo.subsByRepo["owner/empty"] = []domain.Subscription{{ID: 1, LastSeenTag: ""}}
	fetcher := &mockFetcher{tags: map[string]string{"owner/empty": ""}}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if repo.subsCalls != 0 {
		t.Fatal("should not fetch subs when repo has no releases")
	}
	if len(notifier.sent) != 0 {
		t.Fatal("should not notify when there's no tag")
	}
}

func TestRateLimitAbortsCycle(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"a/b", "c/d"}
	repo.subsByRepo["c/d"] = []domain.Subscription{{ID: 1}}
	fetcher := &mockFetcher{
		tags: map[string]string{"a/b": "", "c/d": "v1"},
		errs: map[string]error{"a/b": domain.ErrRateLimited},
	}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if fetcher.calls != 1 {
		t.Fatalf("want 1 GitHub call before abort, got %d", fetcher.calls)
	}
	if len(notifier.sent) != 0 {
		t.Fatal("should not notify when rate-limited")
	}
}

func TestInvalidRepoFormatContinues(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"invalid", "golang/go"}
	repo.subsByRepo["golang/go"] = []domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0"},
	}
	fetcher := &mockFetcher{tags: map[string]string{"golang/go": "v1.1"}}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if len(notifier.sent) != 1 {
		t.Fatalf("want 1 notification for valid repo, got %d", len(notifier.sent))
	}
}

func TestReposListingErrorAborts(t *testing.T) {
	repo := newMockScannerRepo()
	repo.reposErr = errors.New("db down")
	fetcher := &mockFetcher{}
	notifier := &mockReleaseNotifier{}

	newScanner(repo, fetcher, notifier).runOnce(context.Background())

	if fetcher.calls != 0 {
		t.Fatal("should not call GitHub when repo listing fails")
	}
}

func TestContextCancelled(t *testing.T) {
	repo := newMockScannerRepo()
	repo.repos = []string{"a/b", "c/d"}
	fetcher := &mockFetcher{tags: map[string]string{"a/b": "", "c/d": ""}}
	notifier := &mockReleaseNotifier{}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	newScanner(repo, fetcher, notifier).runOnce(ctx)

	if fetcher.calls != 0 {
		t.Fatalf("want no GitHub calls when ctx cancelled, got %d", fetcher.calls)
	}
}
