package notifier

import (
	"fmt"
	"net/smtp"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/templates"
)

// Zero-width space inserted after the URL scheme to prevent mail clients
// from auto-linkifying the "copy this link" text.
const zwsp = "\u200B"

func breakAutoLink(url string) string {
	return strings.Replace(url, "://", zwsp+"://", 1)
}

type Config struct {
	Host     string
	Port     string
	Username string
	Password string
	BaseURL  string
}

type Notifier struct {
	cfg Config
}

func New(cfg *Config) *Notifier {
	return &Notifier{cfg: *cfg}
}

func (n *Notifier) SendConfirmation(email, repo, token, unsubscribeToken string) error {
	subject := fmt.Sprintf("Confirm your subscription to %s releases", repo)
	confirmURL := fmt.Sprintf("%s/api/confirm/%s", n.cfg.BaseURL, token)
	unsubscribeURL := fmt.Sprintf("%s/api/unsubscribe/%s", n.cfg.BaseURL, unsubscribeToken)

	plain := fmt.Sprintf(
		"You have subscribed to release notifications for %s.\n\n"+
			"Please confirm your subscription by clicking the link below:\n%s\n\n"+
			"If you did not request this, you can safely ignore this email, "+
			"or unsubscribe here:\n%s",
		repo, confirmURL, unsubscribeURL,
	)
	html, err := templates.RenderEmail(
		"confirmation.html", map[string]string{
			"Repo":              repo,
			"ConfirmURL":        confirmURL,
			"ConfirmURLDisplay": breakAutoLink(confirmURL),
			"UnsubscribeURL":    unsubscribeURL,
		},
	)
	if err != nil {
		return err
	}

	return n.send(email, subject, plain, html, unsubscribeURL)
}

func (n *Notifier) SendReleaseNotification(email, repo, tag, unsubscribeToken string) error {
	subject := fmt.Sprintf("New release %s for %s", tag, repo)
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, tag)
	unsubscribeURL := fmt.Sprintf("%s/api/unsubscribe/%s", n.cfg.BaseURL, unsubscribeToken)

	plain := fmt.Sprintf(
		"A new release has been published for %s!\n\n"+
			"Tag: %s\n"+
			"View release: %s\n\n"+
			"To unsubscribe from these notifications:\n%s",
		repo, tag, releaseURL, unsubscribeURL,
	)
	html, err := templates.RenderEmail(
		"release.html", map[string]string{
			"Repo":           repo,
			"Tag":            tag,
			"ReleaseURL":     releaseURL,
			"UnsubscribeURL": unsubscribeURL,
		},
	)
	if err != nil {
		return err
	}

	return n.send(email, subject, plain, html, unsubscribeURL)
}

func (n *Notifier) send(to, subject, plain, html, unsubscribeURL string) error {
	if n.cfg.Host == "" {
		return fmt.Errorf("SMTP not configured: host is required")
	}

	boundary := "boundary-repo-release-notifier"
	msg := fmt.Sprintf(
		"From: %s\r\n"+
			"To: %s\r\n"+
			"Subject: %s\r\n"+
			"List-Unsubscribe: <%s>\r\n"+
			"List-Unsubscribe-Post: List-Unsubscribe=One-Click\r\n"+
			"MIME-Version: 1.0\r\n"+
			"Content-Type: multipart/alternative; boundary=\"%s\"\r\n"+
			"\r\n"+
			"--%s\r\n"+
			"Content-Type: text/plain; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: 8bit\r\n"+
			"\r\n%s\r\n"+
			"--%s\r\n"+
			"Content-Type: text/html; charset=UTF-8\r\n"+
			"Content-Transfer-Encoding: 8bit\r\n"+
			"\r\n%s\r\n"+
			"--%s--\r\n",
		n.cfg.Username, to, subject, unsubscribeURL, boundary,
		boundary, plain,
		boundary, html,
		boundary,
	)

	addr := fmt.Sprintf("%s:%s", n.cfg.Host, n.cfg.Port)
	auth := smtp.PlainAuth("", n.cfg.Username, n.cfg.Password, n.cfg.Host)

	if err := smtp.SendMail(addr, auth, n.cfg.Username, []string{to}, []byte(msg)); err != nil {
		return fmt.Errorf("failed to send email to %s: %w", to, err)
	}

	return nil
}
