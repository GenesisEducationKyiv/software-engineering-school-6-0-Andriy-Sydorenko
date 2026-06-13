package notifier

import (
	"fmt"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier/templates"
)

// Zero-width space inserted after the URL scheme to prevent mail clients
// from auto-linkifying the "copy this link" text. Built from a rune so the
// source carries no invisible character (staticcheck ST1018).
var zwsp = string(rune(0x200B))

func breakAutoLink(url string) string {
	return strings.Replace(url, "://", zwsp+"://", 1)
}

// Composer renders email bodies from data the core is handed. It owns no URL
// scheme: confirm/unsubscribe links arrive fully formed (built core-side from
// the core's own BASE_URL), so the notifier stays ignorant of the core's routes.
type Composer struct{}

func NewComposer() *Composer {
	return &Composer{}
}

func (c *Composer) Confirmation(email, repo, confirmURL, unsubscribeURL string) (Message, error) {
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
		return Message{}, err
	}

	return Message{
		To:        email,
		Subject:   fmt.Sprintf("Confirm your subscription to %s releases", repo),
		PlainBody: plain,
		HTMLBody:  html,
		Headers:   unsubscribeHeaders(unsubscribeURL),
	}, nil
}

func (c *Composer) Release(email, repo, tag, unsubscribeURL string) (Message, error) {
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, tag)

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
		return Message{}, err
	}

	return Message{
		To:        email,
		Subject:   fmt.Sprintf("New release %s for %s", tag, repo),
		PlainBody: plain,
		HTMLBody:  html,
		Headers:   unsubscribeHeaders(unsubscribeURL),
	}, nil
}

func unsubscribeHeaders(url string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      fmt.Sprintf("<%s>", url),
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}
