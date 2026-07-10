package notify

import (
	"time"
)

const (
	StreamName    = "NOTIFICATIONS"
	StreamSubject = "notify.>"

	SubjectConfirmation = "notify.confirmation"
	SubjectRelease      = "notify.release"

	DurableConsumer = "notifier"

	DLQStreamName = "NOTIFY_DLQ"
	DLQSubject    = "dlq.notify"
)

func DedupWindow(scanInterval time.Duration) time.Duration {
	return 3 * scanInterval
}

type EmailCommand struct {
	EventID        string `json:"event_id"`
	RecipientEmail string `json:"recipient_email"`
	Subject        string `json:"subject"`
	HTMLBody       string `json:"html_body"`
}

// ConfirmationDedupID is the JetStream Nats-Msg-Id for a confirmation email.
// The confirm token is unique per subscription, so retries dedup cleanly.
func ConfirmationDedupID(token string) string {
	return "confirmation:" + token
}

// ReleaseDedupID is the Nats-Msg-Id for a release email to one recipient.
// Unique per (repo, tag, recipient) so a scanner re-run does not double-send.
func ReleaseDedupID(repo, tag, email string) string {
	return "release:" + repo + ":" + tag + ":" + email
}
