package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner/mocks"
)

type fixture struct {
	repo     *mocks.MockRepository
	github   *mocks.MockReleaseFetcher
	notifier *mocks.MockReleaseNotifier
	scanner  *Scanner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		repo:     mocks.NewMockRepository(ctrl),
		github:   mocks.NewMockReleaseFetcher(ctrl),
		notifier: mocks.NewMockReleaseNotifier(ctrl),
	}
	// Concurrency=1: deterministic dispatch. Parallelism: workerpool_test.go.
	f.scanner = New(f.repo, f.github, f.notifier, &Config{Interval: time.Minute, Concurrency: 1})
	return f
}

func TestNewReleaseNotifiesAll(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return([]domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0", UnsubscribeToken: "uA"},
		{ID: 2, Email: "b@x.com", Repo: "golang/go", LastSeenTag: "v1.0", UnsubscribeToken: "uB"},
	}, nil)
	f.repo.EXPECT().UpdateLastSeenTag(gomock.Any(), uint(1), "v1.1").Return(nil)
	f.repo.EXPECT().UpdateLastSeenTag(gomock.Any(), uint(2), "v1.1").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(gomock.Any(), "a@x.com", "golang/go", "v1.1", "uA").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(gomock.Any(), "b@x.com", "golang/go", "v1.1", "uB").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestSameTagNoNotification(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.0", nil)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return([]domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0"},
	}, nil)
	// No Update/Send EXPECT: tag unchanged ⇒ silent.

	f.scanner.runOnce(context.Background())
}

func TestFirstBaselineSilent(t *testing.T) {
	// First sighting: persist tag, skip email — otherwise every new sub gets spam for the current release.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return([]domain.Subscription{
		{ID: 1, LastSeenTag: ""},
	}, nil)
	f.repo.EXPECT().UpdateLastSeenTag(gomock.Any(), uint(1), "v1.1").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestEmptyTagSkips(t *testing.T) {
	// Empty tag = "no releases yet" — must short-circuit before fetching subs.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"owner/empty"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "owner", "empty").Return("", nil)

	f.scanner.runOnce(context.Background())
}

func TestRateLimitAbortsCycle(t *testing.T) {
	// concurrency=1 + atomic signal flag guarantees second repo is skipped.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"a/b", "c/d"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "a", "b").Return("", domain.ErrRateLimited)

	f.scanner.runOnce(context.Background())
}

func TestInvalidRepoFormatContinues(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"invalid", "golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return([]domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0"},
	}, nil)
	f.repo.EXPECT().UpdateLastSeenTag(gomock.Any(), uint(1), "v1.1").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(gomock.Any(), "a@x.com", "golang/go", "v1.1", "").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestReposListingErrorAborts(t *testing.T) {
	// No GitHub EXPECT: listing failure is terminal for the cycle.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return(nil, errors.New("db down"))

	f.scanner.runOnce(context.Background())
}

func TestContextCancelled(t *testing.T) {
	// Pool's pre-check skips dispatch on cancelled ctx — no GitHub EXPECTs.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"a/b", "c/d"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.scanner.runOnce(ctx)
}

func TestSafeCheckRepoRecoversFromPanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	github := mocks.NewMockReleaseFetcher(ctrl)
	notifier := mocks.NewMockReleaseNotifier(ctrl)

	github.EXPECT().GetLatestRelease(gomock.Any(), "owner", "repo").Do(
		func(_ context.Context, _, _ string) { panic("boom") },
	)
	s := New(repo, github, notifier, &Config{Interval: time.Minute, Concurrency: 1})

	err := s.safeCheckRepo(context.Background(), "owner/repo")
	require.NoError(t, err, "panic must be converted to nil so the worker pool can keep dispatching")
}

func TestPanicInOneRepoDoesNotKillCycle(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"bad/one", "good/two"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "bad", "one").Do(
		func(_ context.Context, _, _ string) { panic("synthetic panic") },
	)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "good", "two").Return("v2.0", nil)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "good/two").Return([]domain.Subscription{
		{ID: 1, Email: "ok@x.com", Repo: "good/two", LastSeenTag: "v1.0", UnsubscribeToken: "u"},
	}, nil)
	f.repo.EXPECT().UpdateLastSeenTag(gomock.Any(), uint(1), "v2.0").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(gomock.Any(), "ok@x.com", "good/two", "v2.0", "u").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestUpdateTagFailureSkipsNotification(t *testing.T) {
	// Persist precedes notify — otherwise a failed persist re-fires on the next scan.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return([]domain.Subscription{
		{ID: 1, Email: "a@x.com", Repo: "golang/go", LastSeenTag: "v1.0", UnsubscribeToken: "u"},
	}, nil)
	f.repo.EXPECT().UpdateLastSeenTag(gomock.Any(), uint(1), "v1.1").Return(errors.New("db write failed"))

	f.scanner.runOnce(context.Background())
}

func TestIsNewTagContract(t *testing.T) {
	s := &domain.Subscription{LastSeenTag: "v1.0"}
	assert.True(t, s.IsNewTag("v1.1"))
	assert.False(t, s.IsNewTag("v1.0"))
	assert.False(t, s.IsNewTag(""), "empty incoming must not be treated as a regression")
}
