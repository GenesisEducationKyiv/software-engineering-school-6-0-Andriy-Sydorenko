package notifier

import "context"

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	BaseURL  string
}

type Notifier struct {
	composer *Composer
	mailer   Mailer
}

func New(cfg *Config) *Notifier {
	return &Notifier{
		composer: NewComposer(cfg.BaseURL),
		mailer:   NewSMTPMailer(cfg),
	}
}

func (n *Notifier) SendConfirmation(ctx context.Context, email, repo, token, unsubscribeToken string) error {
	msg, err := n.composer.Confirmation(email, repo, token, unsubscribeToken)
	if err != nil {
		return err
	}
	return n.mailer.Send(ctx, msg)
}

func (n *Notifier) SendReleaseNotification(ctx context.Context, email, repo, tag, unsubscribeToken string) error {
	msg, err := n.composer.Release(email, repo, tag, unsubscribeToken)
	if err != nil {
		return err
	}
	return n.mailer.Send(ctx, msg)
}
