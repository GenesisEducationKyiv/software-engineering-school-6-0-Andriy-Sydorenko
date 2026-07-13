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

const nakBackoff = 5 * time.Second

// Handler processes one decoded message. Return nil to ack, ErrPermanent-wrapped
// to dead-letter immediately, or any other error to retry (nak).
type Handler func(ctx context.Context, subject string, data []byte) error

type action int

const (
	actionAck action = iota
	actionNak
	actionTerm
)

func (a action) label() string {
	switch a {
	case actionNak:
		return "nak"
	case actionTerm:
		return "term"
	default:
		return "ack"
	}
}

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
			hctx, cancel := context.WithTimeout(context.Background(), ackWait)
			defer cancel()
			herr := h(hctx, msg.Subject(), msg.Data())
			numDelivered := 1
			if md, mderr := msg.Metadata(); mderr == nil && md.NumDelivered <= math.MaxInt {
				numDelivered = int(md.NumDelivered)
			}
			act := classify(herr, numDelivered, maxDeliver)
			messagesTotal.WithLabelValues(msg.Subject(), act.label()).Inc()
			switch act {
			case actionAck:
				_ = msg.Ack()
			case actionNak:
				slog.Warn(
					"notify: retrying message",
					"subject",
					msg.Subject(),
					"err",
					herr,
					"delay",
					nakBackoff,
				)
				_ = msg.NakWithDelay(nakBackoff)
			case actionTerm:
				slog.Error("notify: dead-lettering message", "subject", msg.Subject(), "err", herr)
				// Detached context: the DLQ park must survive shutdown cancellation.
				dlqCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_, derr := js.Publish(dlqCtx, notify.DLQSubject, msg.Data())
				cancel()
				if derr != nil {
					// Nak so a failed park stays redeliverable instead of being lost.
					slog.Error(
						"notify: DLQ publish failed, naking for retry",
						"subject",
						msg.Subject(),
						"err",
						derr,
					)
					_ = msg.NakWithDelay(nakBackoff)
					return
				}
				_ = msg.Term()
			}
		},
	)
}
