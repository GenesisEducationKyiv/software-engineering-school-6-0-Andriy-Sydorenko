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
	EventID        string            `json:"event_id"`
	RecipientEmail string            `json:"recipient_email"`
	Subject        string            `json:"subject"`
	HTMLBody       string            `json:"html_body"`
	PlainBody      string            `json:"plain_body,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
}

func ConfirmationDedupID(token string) string {
	return "confirmation:" + token
}

func ReleaseDedupID(repo, tag, email string) string {
	return "release:" + repo + ":" + tag + ":" + email
}
