// Package bench benchmarks the core↔notifier boundary over both gRPC and
// HTTP/JSON, wrapping the SAME notifier.Core so transport is the only variable.
// HTTP is benchmark-only; gRPC is the runtime transport (spec §8).
package bench

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
)

// Shared, transport-neutral constants. The token rides identically in gRPC
// metadata (Bearer) and the HTTP Authorization header, so auth is enabled and
// symmetric on both sides (spec §8 fairness + §10.1 benchmark neutrality).
const (
	baseURL = "https://notify.example.com"
	token   = "bench-internal-token"
)

// stubMailer is a no-op mailer that only counts sends. It makes the benchmark
// measure compose + transport + serialization, NOT SMTP. Send is allocation-free
// and goroutine-safe so it never distorts B/op or races under the gRPC server's
// per-call goroutines.
type stubMailer struct {
	count atomic.Uint64
}

func (s *stubMailer) Send(_ context.Context, _ notifier.Message) error {
	s.count.Add(1)
	return nil
}

// newCore builds the Core shared by both transports, over a fresh stub mailer.
func newCore() *notifier.Core {
	return notifier.NewCoreWithMailer(baseURL, &stubMailer{})
}

// --- JSON request/response shapes, mirroring the proto messages field-for-field.

type jsonRecipient struct {
	Email            string `json:"email"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

type jsonSendConfirmationRequest struct {
	Email            string `json:"email"`
	Repo             string `json:"repo"`
	ConfirmToken     string `json:"confirm_token"`
	UnsubscribeToken string `json:"unsubscribe_token"`
}

type jsonSendReleaseNotificationsRequest struct {
	Repo       string          `json:"repo"`
	Tag        string          `json:"tag"`
	NotesURL   string          `json:"notes_url"`
	Recipients []jsonRecipient `json:"recipients"`
}

type jsonSendAck struct {
	Sent   uint32 `json:"sent"`
	Failed uint32 `json:"failed"`
}

// --- Canonical workload builders. Deterministic, so every transport/run sees
//     identical bytes.

// confirmationFields returns the fixed SendConfirmation workload.
func confirmationFields() (email, repo, confirmToken, unsubscribeToken string) {
	return "subscriber@example.com", "golang/go", "confirm-tok-abcdef0123456789", "unsub-tok-9876543210fedcba"
}

// releaseFields returns the fixed (repo, tag, notesURL) for the release workload.
func releaseFields() (repo, tag, notesURL string) {
	return "golang/go", "v1.24.0", "https://github.com/golang/go/releases/tag/v1.24.0"
}

// makeRecipients builds n deterministic recipients. Emails/tokens are realistic
// lengths so payload sizes reflect real traffic, not empty strings.
func makeRecipients(n int) []notifier.Recipient {
	rs := make([]notifier.Recipient, n)
	for i := range rs {
		rs[i] = notifier.Recipient{
			Email:            fmt.Sprintf("subscriber%08d@example.com", i),
			UnsubscribeToken: fmt.Sprintf("unsub-token-%016x", i),
		}
	}
	return rs
}

// recipientCounts is the payload-scaling axis for SendReleaseNotifications.
var recipientCounts = []int{1, 100, 1000, 10000}
