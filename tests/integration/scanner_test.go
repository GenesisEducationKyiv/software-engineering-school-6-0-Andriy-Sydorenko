//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifierclient"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/scanner"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/subscription"
)

// stubFetcher returns a fixed tag for any repo.
type stubFetcher struct{ tag string }

func (s stubFetcher) GetLatestRelease(_ context.Context, _, _ string) (string, error) {
	return s.tag, nil
}

// recordingNotifier counts release recipients across batched sends.
type recordingNotifier struct{ sends int }

func (n *recordingNotifier) SendReleaseNotifications(_ context.Context, _, _, _ string, recipients []notifierclient.Recipient) error {
	n.sends += len(recipients)
	return nil
}

func TestScanCycle_baselineThenNotify(t *testing.T) {
	db := mustSharedDB(t)
	truncateAll(t, db)
	ctx := context.Background()

	subRepo := subscription.NewRepository(db)
	// One confirmed subscriber on golang/go.
	sub := &domain.Subscription{Email: "a@x.com", Repo: "golang/go", Confirmed: true, UnsubscribeToken: "u1"}
	tok := &domain.ConfirmationToken{Token: "t1"}
	require.NoError(t, subRepo.CreateSubscriptionWithToken(ctx, sub, tok))
	require.NoError(t, subRepo.ConfirmSubscription(ctx, sub.ID))

	svc := subscription.New(subRepo, nil, nil, subscription.RandomToken) // scan path uses no github/notifier
	store := scanner.NewRepository(db)
	note := &recordingNotifier{}

	// First cycle: tag v1.0 is the baseline — record, send nothing.
	scan1 := scanner.New(svc, store, stubFetcher{tag: "v1.0"}, note, &scanner.Config{Interval: time.Minute, Concurrency: 1})
	scan1.RunOnceForTest(ctx)
	assert.Equal(t, 0, note.sends, "baseline must not notify")
	tag, found, err := store.GetLastSeenTag(ctx, "golang/go")
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "v1.0", tag)

	// Second cycle: tag advances to v1.1 — notify the confirmed subscriber.
	scan2 := scanner.New(svc, store, stubFetcher{tag: "v1.1"}, note, &scanner.Config{Interval: time.Minute, Concurrency: 1})
	scan2.RunOnceForTest(ctx)
	assert.Equal(t, 1, note.sends, "tag advance must notify once")
	tag, _, err = store.GetLastSeenTag(ctx, "golang/go")
	require.NoError(t, err)
	assert.Equal(t, "v1.1", tag)
}
