// Package notifierclient is the core's view of the notifier microservice: a
// transport-neutral interface plus a gRPC adapter. The subscription and scanner
// modules depend ONLY on the interfaces here — never on proto/gen/notifierpb.
package notifierclient

import (
	"context"
	"fmt"

	pb "github.com/Andriy-Sydorenko/repo-release-notifier/proto/gen/notifierpb"
)

// Recipient is one release-notification target (core side, no proto).
type Recipient struct {
	Email            string
	UnsubscribeToken string
}

// ConfirmationSender is the subscribe-path dependency. Matches the subscription
// module's ConfirmationSender exactly, so the adapter drops in where the
// in-process notifier used to sit.
type ConfirmationSender interface {
	SendConfirmation(ctx context.Context, email, repo, confirmToken, unsubscribeToken string) error
}

// ReleaseSender is the scan-path dependency: one batched fan-out call per
// detected (repo, tag).
type ReleaseSender interface {
	SendReleaseNotifications(ctx context.Context, repo, tag, notesURL string, recipients []Recipient) error
}

// NotifierClient is the full core→notifier surface (both call-sites).
type NotifierClient interface {
	ConfirmationSender
	ReleaseSender
}

// Adapter maps the core's plain types to notifierpb and back. It wraps a
// generated client built from platform.Dial; auth + correlation ride in the
// client interceptors configured there, so this layer stays pure mapping.
type Adapter struct {
	pb pb.NotifierServiceClient
}

func NewAdapter(client pb.NotifierServiceClient) *Adapter {
	return &Adapter{pb: client}
}

func (a *Adapter) SendConfirmation(ctx context.Context, email, repo, confirmToken, unsubscribeToken string) error {
	_, err := a.pb.SendConfirmation(ctx, &pb.SendConfirmationRequest{
		Email:            email,
		Repo:             repo,
		ConfirmToken:     confirmToken,
		UnsubscribeToken: unsubscribeToken,
	})
	if err != nil {
		return fmt.Errorf("notifier SendConfirmation: %w", err)
	}
	return nil
}

func (a *Adapter) SendReleaseNotifications(ctx context.Context, repo, tag, notesURL string, recipients []Recipient) error {
	pbRecipients := make([]*pb.Recipient, 0, len(recipients))
	for _, r := range recipients {
		pbRecipients = append(pbRecipients, &pb.Recipient{
			Email:            r.Email,
			UnsubscribeToken: r.UnsubscribeToken,
		})
	}
	_, err := a.pb.SendReleaseNotifications(ctx, &pb.SendReleaseNotificationsRequest{
		Repo:       repo,
		Tag:        tag,
		NotesUrl:   notesURL,
		Recipients: pbRecipients,
	})
	if err != nil {
		return fmt.Errorf("notifier SendReleaseNotifications: %w", err)
	}
	return nil
}

var _ NotifierClient = (*Adapter)(nil)
