package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/scanner/mocks"
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
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return(
		[]domain.Subscription{
			{ID: 1, Email: "a@x.com", Repo: "golang/go", UnsubscribeToken: "uA"},
			{ID: 2, Email: "b@x.com", Repo: "golang/go", UnsubscribeToken: "uB"},
		}, nil,
	)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(
		gomock.Any(), "a@x.com", "golang/go", "v1.1", "uA",
	).Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(
		gomock.Any(), "b@x.com", "golang/go", "v1.1", "uB",
	).Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestSameTagNoNotification(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.0", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	// Tag unchanged: record the poll (last_polled_at), but no subs fetch / Send.
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.0").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestFirstBaselineSilent(t *testing.T) {
	// First sighting: persist the baseline, skip email — the current release
	// predates every subscription, so it's new to no one.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(nil, nil)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	// No subs fetch / Send.

	f.scanner.runOnce(context.Background())
}

func TestEmptyTagSkips(t *testing.T) {
	// Empty tag = "no releases yet" — must short-circuit before touching the cursor.
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
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return(
		[]string{"invalid", "golang/go"}, nil,
	)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return(
		[]domain.Subscription{{ID: 1, Email: "a@x.com", Repo: "golang/go"}}, nil,
	)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(
		gomock.Any(), "a@x.com", "golang/go", "v1.1", "",
	).Return(nil)

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
	require.NoError(
		t,
		err,
		"panic must be converted to nil so the worker pool can keep dispatching",
	)
}

func TestPanicInOneRepoDoesNotKillCycle(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return(
		[]string{"bad/one", "good/two"},
		nil,
	)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "bad", "one").Do(
		func(_ context.Context, _, _ string) { panic("synthetic panic") },
	)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "good", "two").Return("v2.0", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "good/two").Return(
		&domain.WatchedRepo{Repo: "good/two", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "good/two").Return(
		[]domain.Subscription{
			{ID: 1, Email: "ok@x.com", Repo: "good/two", UnsubscribeToken: "u"},
		}, nil,
	)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "good/two", "v2.0").Return(nil)
	f.notifier.EXPECT().SendReleaseNotification(
		gomock.Any(), "ok@x.com", "good/two", "v2.0", "u",
	).Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestSendFailureKeepsCursor(t *testing.T) {
	// A failed send must NOT advance the cursor: the next scan has to re-detect
	// the release and retry. No SaveWatchedRepoTag EXPECT — gomock fails if it's
	// called.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return(
		[]domain.Subscription{
			{ID: 1, Email: "a@x.com", Repo: "golang/go", UnsubscribeToken: "u"},
		}, nil,
	)
	f.notifier.EXPECT().SendReleaseNotification(
		gomock.Any(), "a@x.com", "golang/go", "v1.1", "u",
	).Return(errors.New("nats down"))

	f.scanner.runOnce(context.Background())
}

func TestSaveTagFailureAfterNotifyIsTolerated(t *testing.T) {
	// Notify precedes persist. A failed persist after a successful send is
	// harmless: the next scan re-publishes and the per-recipient dedup id
	// suppresses the copy already delivered.
	f := newFixture(t)
	f.repo.EXPECT().FindDistinctConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().FindConfirmedSubscriptionsByRepo(gomock.Any(), "golang/go").Return(
		[]domain.Subscription{
			{ID: 1, Email: "a@x.com", Repo: "golang/go", UnsubscribeToken: "u"},
		}, nil,
	)
	f.notifier.EXPECT().SendReleaseNotification(
		gomock.Any(), "a@x.com", "golang/go", "v1.1", "u",
	).Return(nil)
	f.repo.EXPECT().SaveWatchedRepoTag(
		gomock.Any(), "golang/go", "v1.1",
	).Return(errors.New("db write failed"))

	f.scanner.runOnce(context.Background())
}

func TestIsNewReleaseContract(t *testing.T) {
	w := &domain.WatchedRepo{LastSeenTag: "v1.0"}
	assert.True(t, w.IsNewRelease("v1.1"))
	assert.False(t, w.IsNewRelease("v1.0"))
	assert.False(t, w.IsNewRelease(""), "empty incoming must not be treated as a regression")
}
