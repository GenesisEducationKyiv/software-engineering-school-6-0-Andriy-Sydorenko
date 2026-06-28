package service_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/service"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/service/mocks"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// stubIDs yields deterministic ids/tokens: saga-1, sub-1 / confirm-1, unsub-1.
type stubIDs struct {
	ids    []string
	tokens []string
	i, j   int
}

func newStubIDs() *stubIDs {
	return &stubIDs{ids: []string{"saga-1", "sub-1"}, tokens: []string{"confirm-1", "unsub-1"}}
}

func (s *stubIDs) NewID() string    { v := s.ids[s.i]; s.i++; return v }
func (s *stubIDs) NewToken() string { v := s.tokens[s.j]; s.j++; return v }

// recStore records state transitions so tests can assert the terminal state.
type recStore struct {
	states     []domain.State
	unfinished []domain.SagaRecord
}

func (s *recStore) Create(context.Context, *domain.SagaRecord) error { return nil }

func (s *recStore) SetState(_ context.Context, _ string, st domain.State, _ string) error {
	s.states = append(s.states, st)
	return nil
}

func (s *recStore) FindUnfinished(context.Context) ([]domain.SagaRecord, error) {
	return s.unfinished, nil
}

func (s *recStore) last() domain.State {
	if len(s.states) == 0 {
		return ""
	}
	return s.states[len(s.states)-1]
}

type harness struct {
	parts   *mocks.MockParticipants
	confirm *mocks.MockConfirmationPublisher
	store   *recStore
	coord   *service.Coordinator
}

func newHarness(t *testing.T) *harness {
	ctrl := gomock.NewController(t)
	h := &harness{
		parts:   mocks.NewMockParticipants(ctrl),
		confirm: mocks.NewMockConfirmationPublisher(ctrl),
		store:   &recStore{},
	}
	h.coord = service.NewCoordinator(h.parts, h.confirm, h.store, newStubIDs())
	return h
}

func TestSubscribe_HappyPath_CommitsThenRequestsConfirmation(t *testing.T) {
	h := newHarness(t)
	h.parts.EXPECT().RegisterRepo(gomock.Any(), "saga-1", "sub-1", "golang/go").
		Return(saga.Reply{OK: true}, nil)
	h.parts.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, cmd *saga.CreateSubscriptionCommand) (saga.Reply, error) {
			require.Equal(t, "sub-1", cmd.SubscriptionID)
			require.Equal(t, "confirm-1", cmd.ConfirmToken)
			require.Equal(t, "unsub-1", cmd.UnsubToken)
			return saga.Reply{OK: true}, nil
		})
	h.confirm.EXPECT().RequestConfirmation(gomock.Any(), "a@b.com", "golang/go", "confirm-1", "unsub-1").
		Return(nil)
	// ReleaseRepo must NOT be called — no EXPECT set.

	require.NoError(t, h.coord.Subscribe(context.Background(), "a@b.com", "golang/go"))
	require.Equal(t, domain.StateDone, h.store.last())
}

func TestSubscribe_BadRepo_Aborts_NoCreate_NoConfirmation(t *testing.T) {
	h := newHarness(t)
	h.parts.EXPECT().RegisterRepo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(saga.Reply{OK: false, Code: saga.CodeRepoNotFound}, nil)
	// CreateSubscription / ReleaseRepo / RequestConfirmation must NOT be called.

	err := h.coord.Subscribe(context.Background(), "a@b.com", "golang/go")
	require.ErrorIs(t, err, domain.ErrRepoNotFound)
	require.Equal(t, domain.StateAborted, h.store.last())
}

func TestSubscribe_CreateFails_CompensatesRelease_NoConfirmation(t *testing.T) {
	h := newHarness(t)
	h.parts.EXPECT().RegisterRepo(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).
		Return(saga.Reply{OK: true}, nil)
	h.parts.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).
		Return(saga.Reply{OK: false, Code: saga.CodeAlreadySubscribed}, nil)
	h.parts.EXPECT().ReleaseRepo(gomock.Any(), "sub-1").Return(nil) // compensation fires
	// RequestConfirmation must NOT be called.

	err := h.coord.Subscribe(context.Background(), "a@b.com", "golang/go")
	require.ErrorIs(t, err, domain.ErrAlreadySubscribed)
	require.Equal(t, domain.StateCompensated, h.store.last())
}

func TestRecover_Committed_RepublishesConfirmationOnly(t *testing.T) {
	h := newHarness(t)
	h.store.unfinished = []domain.SagaRecord{{
		SagaID: "saga-1", State: domain.StateCommitted, SubscriptionID: "sub-1",
		Payload: domain.SagaPayload{Email: "a@b.com", Repo: "o/r", ConfirmToken: "c", UnsubToken: "u"},
	}}
	h.confirm.EXPECT().RequestConfirmation(gomock.Any(), "a@b.com", "o/r", "c", "u").Return(nil)
	// ReleaseRepo / CreateSubscription must NOT be called.

	require.NoError(t, h.coord.Recover(context.Background()))
	require.Equal(t, domain.StateDone, h.store.last())
}

func TestRecover_CatalogOK_Compensates(t *testing.T) {
	h := newHarness(t)
	h.store.unfinished = []domain.SagaRecord{{
		SagaID: "saga-1", State: domain.StateCatalogOK, SubscriptionID: "sub-1",
		Payload: domain.SagaPayload{Email: "a@b.com", Repo: "o/r"},
	}}
	h.parts.EXPECT().ReleaseRepo(gomock.Any(), "sub-1").Return(nil)
	// RequestConfirmation must NOT be called.

	require.NoError(t, h.coord.Recover(context.Background()))
	require.Equal(t, domain.StateCompensated, h.store.last())
}

func TestRecover_SubPending_RollsForward(t *testing.T) {
	h := newHarness(t)
	h.store.unfinished = []domain.SagaRecord{{
		SagaID: "saga-1", State: domain.StateSubPending, SubscriptionID: "sub-1",
		Payload: domain.SagaPayload{Email: "a@b.com", Repo: "o/r", ConfirmToken: "c", UnsubToken: "u"},
	}}
	h.parts.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).Return(saga.Reply{OK: true}, nil)
	h.confirm.EXPECT().RequestConfirmation(gomock.Any(), "a@b.com", "o/r", "c", "u").Return(nil)

	require.NoError(t, h.coord.Recover(context.Background()))
	require.Equal(t, domain.StateDone, h.store.last())
}

func TestRecover_SubPending_UnresolvedCreate_StaysPending(t *testing.T) {
	h := newHarness(t)
	h.store.unfinished = []domain.SagaRecord{{
		SagaID: "saga-1", State: domain.StateSubPending, SubscriptionID: "sub-1",
		Payload: domain.SagaPayload{Email: "a@b.com", Repo: "o/r", ConfirmToken: "c", UnsubToken: "u"},
	}}
	// A transport error (or any non-OK, non-duplicate reply) must not silently
	// abandon the saga: no compensation, no confirmation, no terminal transition —
	// it is left for the next sweep.
	h.parts.EXPECT().CreateSubscription(gomock.Any(), gomock.Any()).
		Return(saga.Reply{}, errors.New("nats timeout"))

	require.NoError(t, h.coord.Recover(context.Background()))
	require.Empty(t, h.store.states, "unresolved create must leave the saga pending, not transitioned")
}
