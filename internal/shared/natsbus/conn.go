package natsbus

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

func Connect(url string) (*nats.Conn, jetstream.JetStream, error) {
	nc, err := nats.Connect(
		url,
		nats.Name("repo-release-notifier"),
		nats.MaxReconnects(-1), // never stop retrying through outages
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(
			func(_ *nats.Conn, err error) {
				slog.Warn("nats disconnected", "err", err)
			},
		),
		nats.ReconnectHandler(
			func(c *nats.Conn) {
				slog.Info("nats reconnected", "url", c.ConnectedUrl())
			},
		),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("nats connect %q: %w", url, err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, nil, fmt.Errorf("jetstream init: %w", err)
	}
	return nc, js, nil
}

const dedupWindow = time.Hour

func EnsureStreams(ctx context.Context, js jetstream.JetStream) error {
	if _, err := js.CreateOrUpdateStream(
		ctx, jetstream.StreamConfig{
			Name:       notify.StreamName,
			Subjects:   []string{notify.StreamSubject},
			Storage:    jetstream.FileStorage,
			Duplicates: dedupWindow,
		},
	); err != nil {
		return fmt.Errorf("ensure stream %s: %w", notify.StreamName, err)
	}
	if _, err := js.CreateOrUpdateStream(
		ctx, jetstream.StreamConfig{
			Name:     notify.DLQStreamName,
			Subjects: []string{notify.DLQSubject},
			Storage:  jetstream.FileStorage,
		},
	); err != nil {
		return fmt.Errorf("ensure stream %s: %w", notify.DLQStreamName, err)
	}
	if _, err := js.CreateOrUpdateStream(
		ctx, jetstream.StreamConfig{
			Name:       saga.EventsStreamName,
			Subjects:   []string{saga.EventsStreamSubject},
			Storage:    jetstream.FileStorage,
			Duplicates: dedupWindow,
		},
	); err != nil {
		return fmt.Errorf("ensure stream %s: %w", saga.EventsStreamName, err)
	}
	return nil
}
