//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/repository"
)

// TestWatchedRepoUpsert exercises the real GORM upsert (OnConflict on the
// natural PK + now() stamp) against Postgres — the path mocks can't cover.
func TestWatchedRepoUpsert(t *testing.T) {
	db := mustSharedDB(t)
	truncateAll(t, db)
	repo := repository.New(db)
	ctx := context.Background()

	// Absent repo → nil, nil (caller treats it as a first sighting).
	got, err := repo.GetWatchedRepo(ctx, "golang/go")
	require.NoError(t, err)
	assert.Nil(t, got)

	// First save inserts the row and stamps last_polled_at.
	require.NoError(t, repo.SaveWatchedRepoTag(ctx, "golang/go", "v1.0"))
	got, err = repo.GetWatchedRepo(ctx, "golang/go")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "v1.0", got.LastSeenTag)
	require.False(t, got.LastPolledAt.IsZero(), "last_polled_at stamped on insert")
	firstPoll := got.LastPolledAt

	// Second save upserts in place: tag advances, poll bumped, no duplicate row.
	require.NoError(t, repo.SaveWatchedRepoTag(ctx, "golang/go", "v1.1"))
	got, err = repo.GetWatchedRepo(ctx, "golang/go")
	require.NoError(t, err)
	assert.Equal(t, "v1.1", got.LastSeenTag)
	assert.False(t, got.LastPolledAt.Before(firstPoll), "last_polled_at advances on update")

	var count int64
	require.NoError(t, db.Model(&domain.WatchedRepo{}).
		Where("repo = ?", "golang/go").Count(&count).Error)
	assert.Equal(t, int64(1), count, "upsert must not create a duplicate row")
}
