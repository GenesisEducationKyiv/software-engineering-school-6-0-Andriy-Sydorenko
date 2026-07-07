package service

import "context"

type EmailSender interface {
	SendEmail(ctx context.Context, recipientEmail, subject, htmlBody string) error
}

type EmailNotifier struct {
	composer *Composer
	sender   EmailSender
}

func NewEmailNotifier(baseURL string, sender EmailSender) *EmailNotifier {
	return &EmailNotifier{composer: NewComposer(baseURL), sender: sender}
}

func (n *EmailNotifier) SendConfirmation(
	ctx context.Context,
	email, repo, token, unsubscribeToken string,
) error {
	msg, err := n.composer.Confirmation(email, repo, token, unsubscribeToken)
	if err != nil {
		return err
	}
	return n.sender.SendEmail(ctx, msg.To, msg.Subject, msg.HTMLBody)
}

func (n *EmailNotifier) SendReleaseNotification(
	ctx context.Context,
	email, repo, tag, unsubscribeToken string,
) error {
	msg, err := n.composer.Release(email, repo, tag, unsubscribeToken)
	if err != nil {
		return err
	}
	return n.sender.SendEmail(ctx, msg.To, msg.Subject, msg.HTMLBody)
}
