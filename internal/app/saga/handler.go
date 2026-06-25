package saga

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// Store is the subscription persistence the saga handler needs.
type Store interface {
	CreateForSaga(
		ctx context.Context,
		sub *domain.Subscription,
		token *domain.ConfirmationToken,
	) (already, mine bool, err error)
}

type Handler struct {
	repo Store
}

func NewHandler(repo Store) *Handler {
	return &Handler{repo: repo}
}

// Create is the saga pivot: persist the subscription in one tx
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
