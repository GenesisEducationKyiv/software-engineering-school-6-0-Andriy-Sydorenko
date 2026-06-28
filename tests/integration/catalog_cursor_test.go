//go:build integration

package integration

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCatalogWatchedRepoUpsert exercises the real GORM upsert (OnConflict on the
// natural PK) against Postgres — the path mocks can't cover. Moved here with the
// cursor when the scanner was extracted into Catalog.
func TestCatalogWatchedRepoUpsert(t *testing.T) {
	repo := newCatalogRepo(t)
	ctx := context.Background()

	// Absent repo → nil, nil (caller treats it as a first sighting).
	got, err := repo.GetWatchedRepo(ctx, "golang/go")
	require.NoError(t, err)
	assert.Nil(t, got)

	// First save inserts the row.
	require.NoError(t, repo.SaveWatchedRepoTag(ctx, "golang/go", "v1.0"))
	got, err = repo.GetWatchedRepo(ctx, "golang/go")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "v1.0", got.LastSeenTag)

	// Second save upserts in place: tag advances, no duplicate row.
	require.NoError(t, repo.SaveWatchedRepoTag(ctx, "golang/go", "v1.1"))
	got, err = repo.GetWatchedRepo(ctx, "golang/go")
	require.NoError(t, err)
	assert.Equal(t, "v1.1", got.LastSeenTag)
}
