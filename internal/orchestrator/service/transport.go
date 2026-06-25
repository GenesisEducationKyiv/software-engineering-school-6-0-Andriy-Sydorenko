package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/orchestrator/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// Participants is the set of saga commands the coordinator issues. Each is a
// synchronous Core NATS request-reply call.
type Participants interface {
	RegisterRepo(ctx context.Context, sagaID, subscriptionID, repo string) (saga.Reply, error)
	ReleaseRepo(ctx context.Context, subscriptionID string) error
	CreateSubscription(ctx context.Context, cmd *saga.CreateSubscriptionCommand) (saga.Reply, error)
}

// ConfirmationPublisher emits the post-commit terminal step.
type ConfirmationPublisher interface {
	RequestConfirmation(ctx context.Context, email, repo, confirmToken, unsubToken string) error
}

// SagaStore persists the saga log for forward/backward recovery.
type SagaStore interface {
	Create(ctx context.Context, rec *domain.SagaRecord) error
	SetState(ctx context.Context, sagaID string, state domain.State, lastErr string) error
	FindUnfinished(ctx context.Context) ([]domain.SagaRecord, error)
}

// IDGen mints saga ids, the cross-service subscription id, and email tokens.
type IDGen interface {
	NewID() string
	NewToken() string
}

// natsParticipants speaks request-reply to the participant services.
type natsParticipants struct {
	nc      *nats.Conn
	timeout time.Duration
}

func NewNATSParticipants(nc *nats.Conn, timeout time.Duration) Participants {
	return &natsParticipants{nc: nc, timeout: timeout}
}

func (p *natsParticipants) RegisterRepo(ctx context.Context, sagaID, subscriptionID, repo string) (saga.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var reply saga.Reply
	err := natsbus.RequestJSON(ctx, p.nc, saga.SubjCatalogRegister,
		saga.RegisterRepoCommand{SagaID: sagaID, SubscriptionID: subscriptionID, Repo: repo}, &reply)
	return reply, err
}

func (p *natsParticipants) ReleaseRepo(ctx context.Context, subscriptionID string) error {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var reply saga.Reply
	return natsbus.RequestJSON(ctx, p.nc, saga.SubjCatalogRelease,
		saga.ReleaseRepoCommand{SubscriptionID: subscriptionID}, &reply)
}

func (p *natsParticipants) CreateSubscription(ctx context.Context, cmd *saga.CreateSubscriptionCommand) (saga.Reply, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	var reply saga.Reply
	err := natsbus.RequestJSON(ctx, p.nc, saga.SubjSubscriptionCreate, cmd, &reply)
	return reply, err
}

// natsConfirmationPublisher emits the confirmation-requested event on JetStream.
type natsConfirmationPublisher struct {
	js jetstream.JetStream
}

func NewNATSConfirmationPublisher(js jetstream.JetStream) ConfirmationPublisher {
	return &natsConfirmationPublisher{js: js}
}

func (p *natsConfirmationPublisher) RequestConfirmation(ctx context.Context, email, repo, confirmToken, unsubToken string) error {
	evt := saga.ConfirmationRequestedEvent{Email: email, Repo: repo, ConfirmToken: confirmToken, UnsubToken: unsubToken}
	data, err := json.Marshal(evt)
	if err != nil {
		return fmt.Errorf("marshal confirmation.requested: %w", err)
	}
	if _, err := p.js.Publish(ctx, saga.SubjConfirmationRequested, data,
		jetstream.WithMsgID("confirm-req:"+confirmToken)); err != nil {
		return fmt.Errorf("publish confirmation.requested: %w", err)
	}
	return nil
}
