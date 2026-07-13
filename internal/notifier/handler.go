package notifier

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

// NewHandler returns a Handler that sends each EmailCommand via the mailer.
// Malformed payloads are permanent (dead-lettered); send failures are transient (retried).
func NewHandler(m Mailer) Handler {
	return func(ctx context.Context, subject string, data []byte) error {
		var cmd notify.EmailCommand
		if err := json.Unmarshal(data, &cmd); err != nil {
			return fmt.Errorf("%w: unmarshal email command: %w", ErrPermanent, err)
		}
		if cmd.RecipientEmail == "" {
			return fmt.Errorf("%w: empty recipient", ErrPermanent)
		}
		start := time.Now()
		if err := m.Send(ctx, cmd.RecipientEmail, cmd.Subject, cmd.HTMLBody); err != nil {
			return fmt.Errorf("send email: %w", err)
		}
		sendDuration.Observe(time.Since(start).Seconds())
		slog.Info("notify: sent", "event_id", cmd.EventID, "subject", subject)
		return nil
	}
}
