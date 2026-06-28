package saga

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// SubscriptionActions is the service capability the confirm/unsubscribe command
// handlers drive (the existing service logic, reached over NATS instead of HTTP).
type SubscriptionActions interface {
	ConfirmSubscription(ctx context.Context, token string) error
	Unsubscribe(ctx context.Context, token string) error
}

// CommandHandler serves the subscription.confirm / subscription.unsubscribe
// request-reply commands the orchestrator's confirm/unsubscribe pages issue.
type CommandHandler struct {
	actions SubscriptionActions
}

func NewCommandHandler(actions SubscriptionActions) *CommandHandler {
	return &CommandHandler{actions: actions}
}

func (h *CommandHandler) Confirm(ctx context.Context, data []byte) (any, error) {
	var cmd saga.ConfirmCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, fmt.Errorf("unmarshal confirm command: %w", err)
	}
	if err := h.actions.ConfirmSubscription(ctx, cmd.Token); err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) {
			return saga.Reply{OK: false, Code: saga.CodeTokenNotFound}, nil
		}
		return nil, err
	}
	return saga.Reply{OK: true}, nil
}

func (h *CommandHandler) Unsubscribe(ctx context.Context, data []byte) (any, error) {
	var cmd saga.UnsubscribeCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, fmt.Errorf("unmarshal unsubscribe command: %w", err)
	}
	if err := h.actions.Unsubscribe(ctx, cmd.Token); err != nil {
		if errors.Is(err, domain.ErrTokenNotFound) {
			return saga.Reply{OK: false, Code: saga.CodeTokenNotFound}, nil
		}
		return nil, err
	}
	return saga.Reply{OK: true}, nil
}
