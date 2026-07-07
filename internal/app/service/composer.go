package service

import (
	"fmt"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/templates"
)

// Zero-width space inserted after the URL scheme to prevent mail clients
// from auto-linkifying the "copy this link" text.
const zwsp = "\u200b"

func breakAutoLink(url string) string {
	return strings.Replace(url, "://", zwsp+"://", 1)
}

type Composer struct {
	baseURL string
}

type Message struct {
	To        string
	Subject   string
	PlainBody string
	HTMLBody  string
	Headers   map[string]string
}

func NewComposer(baseURL string) *Composer {
	return &Composer{baseURL: baseURL}
}

func (c *Composer) Confirmation(email, repo, token, unsubscribeToken string) (Message, error) {
	confirmURL := fmt.Sprintf("%s/api/confirm/%s", c.baseURL, token)
	unsubscribeURL := c.unsubscribeURL(unsubscribeToken)

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

func (c *Composer) Release(email, repo, tag, unsubscribeToken string) (Message, error) {
	releaseURL := fmt.Sprintf("https://github.com/%s/releases/tag/%s", repo, tag)
	unsubscribeURL := c.unsubscribeURL(unsubscribeToken)

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

func (c *Composer) unsubscribeURL(token string) string {
	return fmt.Sprintf("%s/api/unsubscribe/%s", c.baseURL, token)
}

func unsubscribeHeaders(url string) map[string]string {
	return map[string]string{
		"List-Unsubscribe":      fmt.Sprintf("<%s>", url),
		"List-Unsubscribe-Post": "List-Unsubscribe=One-Click",
	}
}
