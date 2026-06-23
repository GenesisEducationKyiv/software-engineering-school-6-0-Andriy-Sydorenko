package natsbus

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/nats-io/nats.go/jetstream"
)

// ErrPermanent marks a message that must not be retried (e.g. a malformed
// payload): the consumer terminates it instead of redelivering.
var ErrPermanent = errors.New("permanent failure")

// EventHandler processes one decoded message. nil -> ack, ErrPermanent-wrapped
// -> term (drop), any other error -> nak (retry).
type EventHandler func(ctx context.Context, subject string, data []byte) error

type ConsumerConfig struct {
	Stream        string
	Durable       string
	FilterSubject string
	MaxDeliver    int
	AckWait       time.Duration
}

// Consume binds a durable JetStream consumer with at-least-once ack/nak/term
// semantics. Unlike the notifier's email consumer it does not dead-letter:
// these are internal events where a dropped permanent failure is benign.
func Consume(
	ctx context.Context,
	js jetstream.JetStream,
	cfg ConsumerConfig,
	h EventHandler,
) (jetstream.ConsumeContext, error) {
	cons, err := js.CreateOrUpdateConsumer(
		ctx, cfg.Stream, jetstream.ConsumerConfig{
			Durable:       cfg.Durable,
			AckPolicy:     jetstream.AckExplicitPolicy,
			FilterSubject: cfg.FilterSubject,
			MaxDeliver:    cfg.MaxDeliver,
			AckWait:       cfg.AckWait,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", cfg.Durable, err)
	}

	return cons.Consume(
		func(msg jetstream.Msg) {
			herr := h(ctx, msg.Subject(), msg.Data())
			numDelivered := 1
			if md, mderr := msg.Metadata(); mderr == nil && md.NumDelivered <= math.MaxInt {
				numDelivered = int(md.NumDelivered)
			}
			switch {
			case herr == nil:
				_ = msg.Ack()
			case errors.Is(herr, ErrPermanent) || numDelivered >= cfg.MaxDeliver:
				slog.Error("consume: dropping message", "subject", msg.Subject(), "err", herr)
				_ = msg.Term()
			default:
				slog.Warn("consume: retrying message", "subject", msg.Subject(), "err", herr)
				_ = msg.Nak()
			}
		},
	)
}
