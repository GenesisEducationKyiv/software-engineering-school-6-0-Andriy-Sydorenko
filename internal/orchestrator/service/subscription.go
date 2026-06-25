package service

import (
	"context"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// SubscriptionClient drives the subscription service's confirm/unsubscribe
// commands over Core NATS request-reply — what the orchestrator's confirm and
// unsubscribe pages call. Returns domain.ErrTokenNotFound for a bad/expired
// token so the page can render the right message.
type SubscriptionClient struct {
	nc      *nats.Conn
	timeout time.Duration
}

func NewSubscriptionClient(nc *nats.Conn, timeout time.Duration) *SubscriptionClient {
	return &SubscriptionClient{nc: nc, timeout: timeout}
}

func (c *SubscriptionClient) Confirm(ctx context.Context, token string) error {
	return c.command(ctx, saga.SubjSubscriptionConfirm, saga.ConfirmCommand{Token: token})
}

func (c *SubscriptionClient) Unsubscribe(ctx context.Context, token string) error {
	return c.command(ctx, saga.SubjSubscriptionUnsubscribe, saga.UnsubscribeCommand{Token: token})
}

func (c *SubscriptionClient) command(ctx context.Context, subject string, cmd any) error {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var reply saga.Reply
	if err := natsbus.RequestJSON(ctx, c.nc, subject, cmd, &reply); err != nil {
		return err
	}
	if !reply.OK {
		if reply.Code == saga.CodeTokenNotFound {
			return domain.ErrTokenNotFound
		}
		return domain.ErrInternal
	}
	return nil
}
