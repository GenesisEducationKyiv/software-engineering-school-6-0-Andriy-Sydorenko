package notifier

import (
	"context"
	"log/slog"
)

// Recipient is one release-notification target. Transport-agnostic — the
// grpcserver maps notifierpb.Recipient to this; the core never sees proto.
type Recipient struct {
	Email          string
	UnsubscribeURL string
}

// Core is the notifier service-core: render (composer) + send (mailer), with no
// transport types in its signatures.
type Core struct {
	composer *Composer
	mailer   Mailer
}

// NewCore builds a Core from SMTP config. Email links arrive pre-rendered from
// the core, so the notifier needs no BASE_URL of its own.
func NewCore(cfg *Config) *Core {
	return &Core{
		composer: NewComposer(),
		mailer:   NewSMTPMailer(cfg),
	}
}

// NewCoreWithMailer builds a Core with an explicit Mailer. Used where the mailer
// must be substituted (benchmarks, integration tests that assert transport
// without hitting SMTP).
func NewCoreWithMailer(mailer Mailer) *Core {
	return &Core{composer: NewComposer(), mailer: mailer}
}

// SendConfirmation renders + sends one confirmation email. A render failure is
// returned (a programming/template bug, not a transient send error); a send
// failure is counted in failed and not returned, so the caller's single
// contract is (sent, failed, err).
func (c *Core) SendConfirmation(
	ctx context.Context,
	email, repo, confirmURL, unsubscribeURL string,
) (sent, failed uint32, err error) {
	msg, err := c.composer.Confirmation(email, repo, confirmURL, unsubscribeURL)
	if err != nil {
		return 0, 0, err
	}
	if sendErr := c.mailer.Send(ctx, msg); sendErr != nil {
		slog.ErrorContext(ctx, "notifier: confirmation send failed", "repo", repo, "err", sendErr)
		return 0, 1, nil
	}
	return 1, 0, nil
}

// SendReleaseNotifications fans one detected release out to every recipient. One
// bad address never aborts the batch: it's logged, counted in failed, and
// dropped (no DLQ — no broker yet, spec §10.0). A render failure for one
// recipient is treated like a send failure (counted, not returned), so a single
// malformed token can't sink the whole batch.
func (c *Core) SendReleaseNotifications(
	ctx context.Context,
	repo, tag, notesURL string,
	recipients []Recipient,
) (sent, failed uint32, err error) {
	for _, r := range recipients {
		msg, composeErr := c.composer.Release(r.Email, repo, tag, r.UnsubscribeURL)
		if composeErr != nil {
			slog.ErrorContext(
				ctx,
				"notifier: release compose failed",
				"repo",
				repo,
				"tag",
				tag,
				"err",
				composeErr,
			)
			failed++
			continue
		}
		if sendErr := c.mailer.Send(ctx, msg); sendErr != nil {
			slog.ErrorContext(
				ctx,
				"notifier: release send failed",
				"repo",
				repo,
				"tag",
				tag,
				"err",
				sendErr,
			)
			failed++
			continue
		}
		sent++
	}
	return sent, failed, nil
}
