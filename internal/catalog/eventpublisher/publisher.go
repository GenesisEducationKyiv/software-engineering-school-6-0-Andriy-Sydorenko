// Package eventpublisher publishes Catalog's domain events to JetStream.
package eventpublisher

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

type Publisher struct {
	js jetstream.JetStream
}

func New(js jetstream.JetStream) *Publisher {
	return &Publisher{js: js}
}

// ReleaseDetected publishes one event per new release. Dedup on (repo, tag) so a
// scanner re-run for the same release doesn't trigger a second fan-out.
func (p *Publisher) ReleaseDetected(ctx context.Context, repo, tag string) error {
	evt := saga.ReleaseDetectedEvent{Repo: repo, Tag: tag, EventID: uuid.NewString()}
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal release.detected: %w", err)
	}
	if _, err := p.js.Publish(ctx, saga.SubjReleaseDetected, data,
		jetstream.WithMsgID("release:"+repo+":"+tag)); err != nil {
		return fmt.Errorf("publish release.detected: %w", err)
	}
	return nil
}
