package natspublisher

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

type Publisher struct {
	js jetstream.JetStream
}

func New(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

func (p *Publisher) Publish(
	ctx context.Context,
	subject, dedupID string,
	cmd notify.EmailCommand,
) error {
	data, err := json.Marshal(cmd)
	if err != nil {
		return fmt.Errorf("marshal email command: %w", err)
	}
	if _, err := p.js.Publish(ctx, subject, data, jetstream.WithMsgID(dedupID)); err != nil {
		return fmt.Errorf("publish %s: %w", subject, err)
	}
	return nil
}
