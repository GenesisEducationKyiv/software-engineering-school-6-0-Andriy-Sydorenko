package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

type Coordinator struct {
	parts   Participants
	confirm ConfirmationPublisher
	store   SagaStore
	ids     IDGen
}

func NewCoordinator(parts Participants, confirm ConfirmationPublisher, store SagaStore, ids IDGen) *Coordinator {
	return &Coordinator{parts: parts, confirm: confirm, store: store, ids: ids}
}

// UUIDGen is the production IDGen.
type UUIDGen struct{}

func (UUIDGen) NewID() string    { return uuid.NewString() }
func (UUIDGen) NewToken() string { return uuid.NewString() }

// Subscribe runs the subscribe saga synchronously: register (compensatable) →
// create (pivot) → confirmation (terminal). On a pre-pivot failure it compensates;
// after the pivot it only rolls forward.
func (c *Coordinator) Subscribe(ctx context.Context, email, repo string) error {
	rec := domain.SagaRecord{
		SagaID:         c.ids.NewID(),
		State:          domain.StateStarted,
		SubscriptionID: c.ids.NewID(),
		Payload: domain.SagaPayload{
			Email:        email,
			Repo:         repo,
			ConfirmToken: c.ids.NewToken(),
			UnsubToken:   c.ids.NewToken(),
		},
	}
	if err := c.store.Create(ctx, &rec); err != nil {
		return errors.Join(domain.ErrInternal, err)
	}

	// Step A — register the repo (risky GitHub validation, fail-fast, compensatable).
	reply, err := c.parts.RegisterRepo(ctx, rec.SagaID, rec.SubscriptionID, repo)
	if err != nil || !reply.OK {
		_ = c.store.SetState(ctx, rec.SagaID, domain.StateAborted, codeOf(reply, err))
		return outcomeErr(reply, err)
	}
	_ = c.store.SetState(ctx, rec.SagaID, domain.StateCatalogOK, "")

	// Step B — create the subscription (pivot).
	_ = c.store.SetState(ctx, rec.SagaID, domain.StateSubPending, "")
	cmd := c.createCmd(&rec)
	createReply, err := c.parts.CreateSubscription(ctx, &cmd)
	if err != nil || !createReply.OK {
		_ = c.store.SetState(ctx, rec.SagaID, domain.StateCompensating, codeOf(createReply, err))
		if relErr := c.parts.ReleaseRepo(ctx, rec.SubscriptionID); relErr != nil {
			slog.ErrorContext(ctx, "saga: compensation ReleaseRepo failed", "saga", rec.SagaID, "err", relErr)
		}
		_ = c.store.SetState(ctx, rec.SagaID, domain.StateCompensated, codeOf(createReply, err))
		return outcomeErr(createReply, err)
	}
	_ = c.store.SetState(ctx, rec.SagaID, domain.StateCommitted, "")

	// Step C — terminal: request the confirmation email (only after COMMITTED).
	c.requestConfirmation(ctx, &rec)
	return nil
}

func (c *Coordinator) requestConfirmation(ctx context.Context, rec *domain.SagaRecord) {
	if err := c.confirm.RequestConfirmation(
		ctx, rec.Payload.Email, rec.Payload.Repo, rec.Payload.ConfirmToken, rec.Payload.UnsubToken,
	); err != nil {
		slog.ErrorContext(ctx, "saga: confirmation request failed; recovery will retry", "saga", rec.SagaID, "err", err)
		return
	}
	_ = c.store.SetState(ctx, rec.SagaID, domain.StateDone, "")
}

func (c *Coordinator) createCmd(rec *domain.SagaRecord) saga.CreateSubscriptionCommand {
	return saga.CreateSubscriptionCommand{
		SagaID:         rec.SagaID,
		SubscriptionID: rec.SubscriptionID,
		Email:          rec.Payload.Email,
		Repo:           rec.Payload.Repo,
		ConfirmToken:   rec.Payload.ConfirmToken,
		UnsubToken:     rec.Payload.UnsubToken,
	}
}

// Recover resumes unfinished sagas after a restart: compensate before the pivot,
// roll forward after it.
func (c *Coordinator) Recover(ctx context.Context) error {
	recs, err := c.store.FindUnfinished(ctx)
	if err != nil {
		return err
	}
	for i := range recs {
		c.recoverOne(ctx, &recs[i])
	}
	return nil
}

func (c *Coordinator) recoverOne(ctx context.Context, rec *domain.SagaRecord) {
	switch rec.State {
	case domain.StateStarted, domain.StateCatalogOK, domain.StateCompensating:
		// Pivot not confirmed → roll back.
		if err := c.parts.ReleaseRepo(ctx, rec.SubscriptionID); err != nil {
			slog.ErrorContext(ctx, "recover: ReleaseRepo failed", "saga", rec.SagaID, "err", err)
			return
		}
		_ = c.store.SetState(ctx, rec.SagaID, domain.StateCompensated, "recovered")
	case domain.StateSubPending:
		// Create may or may not have landed → roll forward idempotently.
		cmd := c.createCmd(rec)
		reply, err := c.parts.CreateSubscription(ctx, &cmd)
		switch {
		case err == nil && reply.OK:
			_ = c.store.SetState(ctx, rec.SagaID, domain.StateCommitted, "")
			c.requestConfirmation(ctx, rec)
		case err == nil && reply.Code == saga.CodeAlreadySubscribed:
			// A different holder of (email,repo) → compensate.
			if relErr := c.parts.ReleaseRepo(ctx, rec.SubscriptionID); relErr != nil {
				slog.ErrorContext(ctx, "recover: compensation ReleaseRepo failed", "saga", rec.SagaID, "err", relErr)
				return
			}
			_ = c.store.SetState(ctx, rec.SagaID, domain.StateCompensated, "recovered-dup")
		default:
			// Unresolved (transport error or non-dup failure): log and leave it for the next sweep.
			slog.ErrorContext(ctx, "recover: subscription.create unresolved, will retry next sweep",
				"saga", rec.SagaID, "code", reply.Code, "err", err)
		}
	case domain.StateCommitted:
		c.requestConfirmation(ctx, rec)
	}
}

func codeOf(reply saga.Reply, err error) string {
	if err != nil {
		return err.Error()
	}
	return reply.Code
}

// outcomeErr maps a participant reply/transport error to a saga-outcome error.
func outcomeErr(reply saga.Reply, err error) error {
	if err != nil {
		return errors.Join(domain.ErrInternal, err)
	}
	switch reply.Code {
	case saga.CodeRepoNotFound:
		return domain.ErrRepoNotFound
	case saga.CodeRateLimited:
		return domain.ErrRateLimited
	case saga.CodeAlreadySubscribed:
		return domain.ErrAlreadySubscribed
	default:
		return domain.ErrInternal
	}
}
