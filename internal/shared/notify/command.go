// Package notify holds the wire contract shared between the core publisher and
// the notifier consumer: the email command payload, NATS subjects, and dedup ids.
package notify

const (
	StreamName    = "NOTIFICATIONS"
	StreamSubject = "notify.>"

	SubjectConfirmation = "notify.confirmation"
	SubjectRelease      = "notify.release"

	DurableConsumer = "notifier"

	DLQStreamName = "NOTIFY_DLQ"
	DLQSubject    = "dlq.notify"
)

type EmailCommand struct {
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
