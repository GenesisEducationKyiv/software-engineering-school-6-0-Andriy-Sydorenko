package service

import (
	"context"
	"log/slog"

	"github.com/google/uuid"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

type Publisher interface {
	Publish(ctx context.Context, subject, dedupID string, cmd notify.EmailCommand) error
}

type EmailNotifier struct {
	composer  *Composer
	publisher Publisher
}

func NewEmailNotifier(baseURL string, publisher Publisher) *EmailNotifier {
	return &EmailNotifier{composer: NewComposer(baseURL), publisher: publisher}
}

func (n *EmailNotifier) SendConfirmation(
	ctx context.Context,
	email, repo, token, unsubscribeToken string,
) error {
	msg, err := n.composer.Confirmation(email, repo, token, unsubscribeToken)
	if err != nil {
		return err
	}
	eventID := uuid.NewString()
	slog.Info("notify: published", "event_id", eventID, "subject", notify.SubjectConfirmation, "repo", repo)
	return n.publisher.Publish(
		ctx, notify.SubjectConfirmation, notify.ConfirmationDedupID(token),
		notify.EmailCommand{EventID: eventID, RecipientEmail: msg.To, Subject: msg.Subject, HTMLBody: msg.HTMLBody},
	)
}

func (n *EmailNotifier) SendReleaseNotification(
	ctx context.Context,
	email, repo, tag, unsubscribeToken string,
) error {
	msg, err := n.composer.Release(email, repo, tag, unsubscribeToken)
	if err != nil {
		return err
	}
	eventID := uuid.NewString()
	slog.Info("notify: published", "event_id", eventID, "subject", notify.SubjectRelease, "repo", repo, "tag", tag)
	return n.publisher.Publish(
		ctx, notify.SubjectRelease, notify.ReleaseDedupID(repo, tag, email),
		notify.EmailCommand{EventID: eventID, RecipientEmail: msg.To, Subject: msg.Subject, HTMLBody: msg.HTMLBody},
	)
}
