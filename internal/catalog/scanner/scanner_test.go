package scanner

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/scanner/mocks"
)

type fixture struct {
	repo    *mocks.MockRepository
	github  *mocks.MockReleaseFetcher
	events  *mocks.MockEventPublisher
	scanner *Scanner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		repo:   mocks.NewMockRepository(ctrl),
		github: mocks.NewMockReleaseFetcher(ctrl),
		events: mocks.NewMockEventPublisher(ctrl),
	}
	// Concurrency=1: deterministic dispatch. Parallelism: workerpool_test.go.
	f.scanner = New(f.repo, f.github, f.events, &Config{Interval: time.Minute, Concurrency: 1})
	return f
}

func TestNewReleasePublishesEvent(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	f.events.EXPECT().ReleaseDetected(gomock.Any(), "golang/go", "v1.1").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestSameTagNoEvent(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.0", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	// Tag unchanged: re-save the tag, but no event.
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.0").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestFirstBaselineSilent(t *testing.T) {
	// First sighting: persist the baseline, skip the event — the current release
	// predates every subscription, so it's new to no one.
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(nil, nil)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.1").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestEmptyTagSkips(t *testing.T) {
	// Empty tag = "no releases yet" — must short-circuit before touching the cursor.
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"owner/empty"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "owner", "empty").Return("", nil)

	f.scanner.runOnce(context.Background())
}

func TestRateLimitAbortsCycle(t *testing.T) {
	// concurrency=1 + atomic signal flag guarantees second repo is skipped.
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"a/b", "c/d"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "a", "b").Return("", domain.ErrRateLimited)

	f.scanner.runOnce(context.Background())
}

func TestInvalidRepoFormatContinues(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return(
		[]string{"invalid", "golang/go"}, nil,
	)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	f.events.EXPECT().ReleaseDetected(gomock.Any(), "golang/go", "v1.1").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestReposListingErrorAborts(t *testing.T) {
	// No GitHub EXPECT: listing failure is terminal for the cycle.
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return(nil, errors.New("db down"))

	f.scanner.runOnce(context.Background())
}

func TestContextCancelled(t *testing.T) {
	// Pool's pre-check skips dispatch on cancelled ctx — no GitHub EXPECTs.
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"a/b", "c/d"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.scanner.runOnce(ctx)
}

func TestSafeCheckRepoRecoversFromPanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockRepository(ctrl)
	github := mocks.NewMockReleaseFetcher(ctrl)
	events := mocks.NewMockEventPublisher(ctrl)

	github.EXPECT().GetLatestRelease(gomock.Any(), "owner", "repo").Do(
		func(_ context.Context, _, _ string) { panic("boom") },
	)
	s := New(repo, github, events, &Config{Interval: time.Minute, Concurrency: 1})

	err := s.safeCheckRepo(context.Background(), "owner/repo")
	require.NoError(
		t,
		err,
		"panic must be converted to nil so the worker pool can keep dispatching",
	)
}

func TestPanicInOneRepoDoesNotKillCycle(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return(
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
	f.repo.EXPECT().SaveWatchedRepoTag(gomock.Any(), "good/two", "v2.0").Return(nil)
	f.events.EXPECT().ReleaseDetected(gomock.Any(), "good/two", "v2.0").Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestSaveTagFailureSkipsEvent(t *testing.T) {
	// Persist precedes publish — a failed persist re-fires next scan instead of
	// publishing on a cursor that didn't move.
	f := newFixture(t)
	f.repo.EXPECT().ActiveRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.repo.EXPECT().GetWatchedRepo(gomock.Any(), "golang/go").Return(
		&domain.WatchedRepo{Repo: "golang/go", LastSeenTag: "v1.0"}, nil,
	)
	f.repo.EXPECT().SaveWatchedRepoTag(
		gomock.Any(), "golang/go", "v1.1",
	).Return(errors.New("db write failed"))
	// No ReleaseDetected EXPECT.

	f.scanner.runOnce(context.Background())
}

func TestIsNewReleaseContract(t *testing.T) {
	w := &domain.WatchedRepo{LastSeenTag: "v1.0"}
	assert.True(t, w.IsNewRelease("v1.1"))
	assert.False(t, w.IsNewRelease("v1.0"))
	assert.False(t, w.IsNewRelease(""), "empty incoming must not be treated as a regression")
}
