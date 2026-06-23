// Package eventpublisher publishes the subscription service's domain events.
package eventpublisher

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

type Publisher struct {
	js jetstream.JetStream
}

func New(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

// SubscriptionRemoved tells Catalog to release the repo registration after an
// unsubscribe. Dedup on subscription_id so a redelivered unsubscribe is a no-op.
func (p *Publisher) SubscriptionRemoved(ctx context.Context, subscriptionID, repo string) error {
	evt := saga.SubscriptionRemovedEvent{SubscriptionID: subscriptionID, Repo: repo}
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal subscription.removed: %w", err)
	}
	if _, err := p.js.Publish(ctx, saga.SubjSubscriptionRemoved, data,
		jetstream.WithMsgID("removed:"+subscriptionID)); err != nil {
		return fmt.Errorf("publish subscription.removed: %w", err)
	}
	return nil
}
