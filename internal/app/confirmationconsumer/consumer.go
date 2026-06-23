// Package confirmationconsumer handles the orchestrator's post-commit
// confirmation.requested event by rendering and publishing the confirmation email.
package confirmationconsumer

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

type ConfirmationSender interface {
	SendConfirmation(ctx context.Context, email, repo, token, unsubscribeToken string) error
}

type Consumer struct {
	notifier ConfirmationSender
}

func New(notifier ConfirmationSender) *Consumer {
	return &Consumer{notifier: notifier}
}

// Handle implements natsbus.EventHandler for events.confirmation.requested.
func (c *Consumer) Handle(ctx context.Context, _ string, data []byte) error {
	var evt saga.ConfirmationRequestedEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return fmt.Errorf("%w: unmarshal confirmation.requested: %w", natsbus.ErrPermanent, err)
	}
	return c.notifier.SendConfirmation(ctx, evt.Email, evt.Repo, evt.ConfirmToken, evt.UnsubToken)
}
