// Package saga implements the subscription service's saga participant handlers
// (subscription.create / subscription.cancel) driven by the orchestrator.
package saga

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// Store is the subscription persistence the saga handlers need.
type Store interface {
	// CreateForSaga inserts the subscription + token. On an (email,repo) conflict
	// it reports already=true, and mine=true when the existing row is this saga's
	// own retry (same public_id) versus a genuine duplicate.
	CreateForSaga(ctx context.Context, sub *domain.Subscription, token *domain.ConfirmationToken) (already, mine bool, err error)
	DeleteByPublicID(ctx context.Context, publicID string) error
}

type Handler struct {
	repo Store
}

func NewHandler(repo Store) *Handler {
	return &Handler{repo: repo}
}

// Create is the saga pivot: persist the subscription. Idempotent under recovery
// retries (same public_id -> OK); a different holder of (email,repo) is a duplicate.
func (h *Handler) Create(ctx context.Context, data []byte) (any, error) {
	var cmd saga.CreateSubscriptionCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, fmt.Errorf("unmarshal create command: %w", err)
	}
	sub := &domain.Subscription{
		PublicID:         cmd.SubscriptionID,
		Email:            cmd.Email,
		Repo:             cmd.Repo,
		UnsubscribeToken: cmd.UnsubToken,
	}
	token := &domain.ConfirmationToken{Token: cmd.ConfirmToken}

	already, mine, err := h.repo.CreateForSaga(ctx, sub, token)
	if err != nil {
		return nil, fmt.Errorf("create subscription: %w", err)
	}
	if already && !mine {
		return saga.Reply{OK: false, Code: saga.CodeAlreadySubscribed}, nil
	}
	return saga.Reply{OK: true}, nil
}

// Cancel is the compensation for Create: remove the subscription by public_id.
// Idempotent — a missing row is a no-op.
func (h *Handler) Cancel(ctx context.Context, data []byte) (any, error) {
	var cmd saga.CancelSubscriptionCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, fmt.Errorf("unmarshal cancel command: %w", err)
	}
	if err := h.repo.DeleteByPublicID(ctx, cmd.SubscriptionID); err != nil {
		return nil, fmt.Errorf("cancel subscription: %w", err)
	}
	return saga.Reply{OK: true}, nil
}
