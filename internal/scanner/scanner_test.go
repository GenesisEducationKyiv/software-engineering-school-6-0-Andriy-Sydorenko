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
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifierclient"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner/mocks"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/subscription"
)

type fixture struct {
	subs     *mocks.MockSubscriberLister
	store    *mocks.MockWatchedRepoStore
	github   *mocks.MockReleaseFetcher
	notifier *mocks.MockReleaseSender
	scanner  *Scanner
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		subs:     mocks.NewMockSubscriberLister(ctrl),
		store:    mocks.NewMockWatchedRepoStore(ctrl),
		github:   mocks.NewMockReleaseFetcher(ctrl),
		notifier: mocks.NewMockReleaseSender(ctrl),
	}
	// Concurrency=1: deterministic dispatch. Parallelism: workerpool_test.go.
	f.scanner = New(f.subs, f.store, f.github, f.notifier, &Config{Interval: time.Minute, Concurrency: 1})
	return f
}

func TestNewReleaseNotifiesAll(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.store.EXPECT().GetLastSeenTag(gomock.Any(), "golang/go").Return("v1.0", true, nil)
	f.subs.EXPECT().ListConfirmedSubscribers(gomock.Any(), "golang/go").Return([]subscription.Subscriber{
		{Email: "a@x.com", UnsubscribeToken: "uA"},
		{Email: "b@x.com", UnsubscribeToken: "uB"},
	}, nil)
	f.store.EXPECT().UpsertLastSeenTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	f.notifier.EXPECT().SendReleaseNotifications(
		gomock.Any(), "golang/go", "v1.1", "https://github.com/golang/go/releases/tag/v1.1",
		[]notifierclient.Recipient{
			{Email: "a@x.com", UnsubscribeToken: "uA"},
			{Email: "b@x.com", UnsubscribeToken: "uB"},
		},
	).Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestSameTagNoNotification(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.0", nil)
	f.store.EXPECT().GetLastSeenTag(gomock.Any(), "golang/go").Return("v1.0", true, nil)
	// Tag unchanged ⇒ no subscriber fetch, no upsert, no send.

	f.scanner.runOnce(context.Background())
}

func TestFirstBaselineSilent(t *testing.T) {
	// First sighting (no watched_repo row): record tag, skip email — otherwise
	// every repo's existing release would spam all confirmed subs on first scan.
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.store.EXPECT().GetLastSeenTag(gomock.Any(), "golang/go").Return("", false, nil)
	f.store.EXPECT().UpsertLastSeenTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	// No ListConfirmedSubscribers, no send.

	f.scanner.runOnce(context.Background())
}

func TestEmptyTagSkips(t *testing.T) {
	// Empty tag = "no releases yet" — short-circuit before reading state.
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"owner/empty"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "owner", "empty").Return("", nil)

	f.scanner.runOnce(context.Background())
}

func TestRateLimitAbortsCycle(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"a/b", "c/d"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "a", "b").Return("", domain.ErrRateLimited)

	f.scanner.runOnce(context.Background())
}

func TestInvalidRepoFormatContinues(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"invalid", "golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.store.EXPECT().GetLastSeenTag(gomock.Any(), "golang/go").Return("v1.0", true, nil)
	f.subs.EXPECT().ListConfirmedSubscribers(gomock.Any(), "golang/go").Return([]subscription.Subscriber{
		{Email: "a@x.com", UnsubscribeToken: ""},
	}, nil)
	f.store.EXPECT().UpsertLastSeenTag(gomock.Any(), "golang/go", "v1.1").Return(nil)
	f.notifier.EXPECT().SendReleaseNotifications(
		gomock.Any(), "golang/go", "v1.1", "https://github.com/golang/go/releases/tag/v1.1",
		[]notifierclient.Recipient{{Email: "a@x.com", UnsubscribeToken: ""}},
	).Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestReposListingErrorAborts(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return(nil, errors.New("db down"))

	f.scanner.runOnce(context.Background())
}

func TestContextCancelled(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"a/b", "c/d"}, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f.scanner.runOnce(ctx)
}

func TestSafeCheckRepoRecoversFromPanic(t *testing.T) {
	ctrl := gomock.NewController(t)
	subs := mocks.NewMockSubscriberLister(ctrl)
	store := mocks.NewMockWatchedRepoStore(ctrl)
	github := mocks.NewMockReleaseFetcher(ctrl)
	notifier := mocks.NewMockReleaseSender(ctrl)

	github.EXPECT().GetLatestRelease(gomock.Any(), "owner", "repo").Do(
		func(_ context.Context, _, _ string) { panic("boom") },
	)
	s := New(subs, store, github, notifier, &Config{Interval: time.Minute, Concurrency: 1})

	err := s.safeCheckRepo(context.Background(), "owner/repo")
	require.NoError(t, err, "panic must be converted to nil so the worker pool can keep dispatching")
}

func TestPanicInOneRepoDoesNotKillCycle(t *testing.T) {
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"bad/one", "good/two"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "bad", "one").Do(
		func(_ context.Context, _, _ string) { panic("synthetic panic") },
	)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "good", "two").Return("v2.0", nil)
	f.store.EXPECT().GetLastSeenTag(gomock.Any(), "good/two").Return("v1.0", true, nil)
	f.subs.EXPECT().ListConfirmedSubscribers(gomock.Any(), "good/two").Return([]subscription.Subscriber{
		{Email: "ok@x.com", UnsubscribeToken: "u"},
	}, nil)
	f.store.EXPECT().UpsertLastSeenTag(gomock.Any(), "good/two", "v2.0").Return(nil)
	f.notifier.EXPECT().SendReleaseNotifications(
		gomock.Any(), "good/two", "v2.0", "https://github.com/good/two/releases/tag/v2.0",
		[]notifierclient.Recipient{{Email: "ok@x.com", UnsubscribeToken: "u"}},
	).Return(nil)

	f.scanner.runOnce(context.Background())
}

func TestUpdateTagFailureSkipsNotification(t *testing.T) {
	// Persist precedes notify — a failed upsert must not send (else it re-fires
	// next scan). Per-repo: one upsert gates the whole recipient fan-out.
	f := newFixture(t)
	f.subs.EXPECT().ListConfirmedRepos(gomock.Any()).Return([]string{"golang/go"}, nil)
	f.github.EXPECT().GetLatestRelease(gomock.Any(), "golang", "go").Return("v1.1", nil)
	f.store.EXPECT().GetLastSeenTag(gomock.Any(), "golang/go").Return("v1.0", true, nil)
	f.subs.EXPECT().ListConfirmedSubscribers(gomock.Any(), "golang/go").Return([]subscription.Subscriber{
		{Email: "a@x.com", UnsubscribeToken: "u"},
	}, nil)
	f.store.EXPECT().UpsertLastSeenTag(gomock.Any(), "golang/go", "v1.1").Return(errors.New("db write failed"))
	// No send: upsert failed.

	f.scanner.runOnce(context.Background())
}

func TestValidateRepo(t *testing.T) {
	t.Run("delegates to validator, exists", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		v := mocks.NewMockRepoValidator(ctrl)
		v.EXPECT().ValidateRepo(gomock.Any(), "golang", "go").Return(nil)
		s := &Scanner{validator: v}

		ok, err := s.ValidateRepo(context.Background(), "golang", "go")
		require.NoError(t, err)
		assert.True(t, ok)
	})

	t.Run("not found maps to (false, nil)", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		v := mocks.NewMockRepoValidator(ctrl)
		v.EXPECT().ValidateRepo(gomock.Any(), "no", "repo").Return(domain.ErrRepoNotFound)
		s := &Scanner{validator: v}

		ok, err := s.ValidateRepo(context.Background(), "no", "repo")
		require.NoError(t, err)
		assert.False(t, ok)
	})

	t.Run("transport error propagates", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		v := mocks.NewMockRepoValidator(ctrl)
		v.EXPECT().ValidateRepo(gomock.Any(), "rl", "repo").Return(domain.ErrRateLimited)
		s := &Scanner{validator: v}

		ok, err := s.ValidateRepo(context.Background(), "rl", "repo")
		require.ErrorIs(t, err, domain.ErrRateLimited)
		assert.False(t, ok)
	})
}

func TestIsNewTagContract(t *testing.T) {
	s := &domain.Subscription{LastSeenTag: "v1.0"}
	assert.True(t, s.IsNewTag("v1.1"))
	assert.False(t, s.IsNewTag("v1.0"))
	assert.False(t, s.IsNewTag(""), "empty incoming must not be treated as a regression")
}

var _ API = (*Scanner)(nil)
