package catalog_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/catalog/mocks"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

func TestRegister_ValidRepo_RegistersAndReturnsOK(t *testing.T) {
	ctrl := gomock.NewController(t)
	val := mocks.NewMockRepoValidator(ctrl)
	val.EXPECT().ValidateRepo(gomock.Any(), "owner", "name").Return(nil)
	store := mocks.NewMockStore(ctrl)
	store.EXPECT().Register(gomock.Any(), "sub-1", "owner/name").Return(nil)

	h := catalog.NewHandler(store, val)
	out, err := h.Register(context.Background(), mustJSON(t, saga.RegisterRepoCommand{Repo: "owner/name", SubscriptionID: "sub-1"}))

	require.NoError(t, err)
	require.Equal(t, saga.Reply{OK: true}, out)
}

func TestRegister_BadRepo_ReturnsRepoNotFound_NoRegister(t *testing.T) {
	ctrl := gomock.NewController(t)
	val := mocks.NewMockRepoValidator(ctrl)
	val.EXPECT().ValidateRepo(gomock.Any(), "owner", "ghost").Return(catalog.ErrRepoNotFound)
	store := mocks.NewMockStore(ctrl) // Register must NOT be called

	h := catalog.NewHandler(store, val)
	out, err := h.Register(context.Background(), mustJSON(t, saga.RegisterRepoCommand{Repo: "owner/ghost", SubscriptionID: "x"}))

	require.NoError(t, err)
	require.Equal(t, saga.Reply{OK: false, Code: saga.CodeRepoNotFound}, out)
}

func TestRegister_RateLimited_ReturnsRateLimited(t *testing.T) {
	ctrl := gomock.NewController(t)
	val := mocks.NewMockRepoValidator(ctrl)
	val.EXPECT().ValidateRepo(gomock.Any(), "o", "n").Return(catalog.ErrRateLimited)

	h := catalog.NewHandler(mocks.NewMockStore(ctrl), val)
	out, err := h.Register(context.Background(), mustJSON(t, saga.RegisterRepoCommand{Repo: "o/n", SubscriptionID: "x"}))

	require.NoError(t, err)
	require.Equal(t, saga.Reply{OK: false, Code: saga.CodeRateLimited}, out)
}

func TestRegister_MalformedRepo_ReturnsRepoNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	val := mocks.NewMockRepoValidator(ctrl) // ValidateRepo must NOT be called

	h := catalog.NewHandler(mocks.NewMockStore(ctrl), val)
	out, err := h.Register(context.Background(), mustJSON(t, saga.RegisterRepoCommand{Repo: "no-slash", SubscriptionID: "x"}))

	require.NoError(t, err)
	require.Equal(t, saga.Reply{OK: false, Code: saga.CodeRepoNotFound}, out)
}

func TestRelease_DelegatesToStore(t *testing.T) {
	ctrl := gomock.NewController(t)
	store := mocks.NewMockStore(ctrl)
	store.EXPECT().Release(gomock.Any(), "sub-1").Return(nil)

	h := catalog.NewHandler(store, mocks.NewMockRepoValidator(ctrl))
	out, err := h.Release(context.Background(), mustJSON(t, saga.ReleaseRepoCommand{SubscriptionID: "sub-1"}))

	require.NoError(t, err)
	require.Equal(t, saga.Reply{OK: true}, out)
}
