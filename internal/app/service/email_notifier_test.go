package service

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service/mocks"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

func TestSendConfirmationPublishesRenderedCommand(t *testing.T) {
	ctrl := gomock.NewController(t)
	pub := mocks.NewMockPublisher(ctrl)
	n := NewEmailNotifier("https://x.test", pub)

	pub.EXPECT().Publish(
		gomock.Any(),
		notify.SubjectConfirmation,
		notify.ConfirmationDedupID("ctok"),
		gomock.AssignableToTypeOf(notify.EmailCommand{}),
	).DoAndReturn(func(_ context.Context, _, _ string, cmd notify.EmailCommand) error {
		if cmd.RecipientEmail != "a@x.com" {
			t.Fatalf("recipient=%q", cmd.RecipientEmail)
		}
		if !strings.Contains(cmd.Subject, "golang/go") {
			t.Fatalf("subject=%q", cmd.Subject)
		}
		if !strings.Contains(cmd.HTMLBody, "/api/confirm/ctok") {
			t.Fatalf("html missing confirm url: %q", cmd.HTMLBody)
		}
		if cmd.EventID == "" {
			t.Fatal("event_id should be set for correlation")
		}
		return nil
	})

	if err := n.SendConfirmation(context.Background(), "a@x.com", "golang/go", "ctok", "utok"); err != nil {
		t.Fatalf("err=%v", err)
	}
}

func TestSendReleasePublishesRenderedCommand(t *testing.T) {
	ctrl := gomock.NewController(t)
	pub := mocks.NewMockPublisher(ctrl)
	n := NewEmailNotifier("https://x.test", pub)

	pub.EXPECT().Publish(
		gomock.Any(),
		notify.SubjectRelease,
		notify.ReleaseDedupID("golang/go", "v1.1", "a@x.com"),
		gomock.AssignableToTypeOf(notify.EmailCommand{}),
	).Return(nil)

	if err := n.SendReleaseNotification(context.Background(), "a@x.com", "golang/go", "v1.1", "utok"); err != nil {
		t.Fatalf("err=%v", err)
	}
}
