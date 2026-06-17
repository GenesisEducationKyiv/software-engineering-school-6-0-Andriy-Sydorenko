package notifier

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

var ErrPermanent = errors.New("permanent failure")

// Handler processes one decoded message. Return nil to ack, ErrPermanent-wrapped
// to dead-letter immediately, or any other error to retry (nak).
type Handler func(ctx context.Context, subject string, data []byte) error

type action int

const (
	actionAck action = iota
	actionNak
	actionTerm
)

func classify(err error, numDelivered, maxDeliver int) action {
	switch {
	case err == nil:
		return actionAck
	case errors.Is(err, ErrPermanent):
		return actionTerm
	case numDelivered >= maxDeliver:
		return actionTerm
	default:
		return actionNak
	}
}

func Subscribe(
	ctx context.Context,
	js jetstream.JetStream,
	maxDeliver int,
	ackWait time.Duration,
	h Handler,
) (jetstream.ConsumeContext, error) {
	cons, err := js.CreateOrUpdateConsumer(
		ctx, notify.StreamName, jetstream.ConsumerConfig{
			Durable:       notify.DurableConsumer,
			AckPolicy:     jetstream.AckExplicitPolicy,
			FilterSubject: notify.StreamSubject,
			MaxDeliver:    maxDeliver,
			AckWait:       ackWait,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", notify.DurableConsumer, err)
	}

	return cons.Consume(
		func(msg jetstream.Msg) {
			herr := h(ctx, msg.Subject(), msg.Data())
			numDelivered := 1
			if md, mderr := msg.Metadata(); mderr == nil && md.NumDelivered <= math.MaxInt {
				numDelivered = int(md.NumDelivered)
			}
			switch classify(herr, numDelivered, maxDeliver) {
			case actionAck:
				_ = msg.Ack()
			case actionNak:
				slog.Warn("notify: retrying message", "subject", msg.Subject(), "err", herr)
				_ = msg.Nak()
			case actionTerm:
				slog.Error("notify: dead-lettering message", "subject", msg.Subject(), "err", herr)
				if _, derr := js.Publish(ctx, notify.DLQSubject, msg.Data()); derr != nil {
					slog.Error("notify: DLQ publish failed", "err", derr)
				}
				_ = msg.Term()
			}
		},
	)
}
