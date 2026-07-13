package notifier

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier/mocks"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notify"
)

func mustJSON(t *testing.T, c notify.EmailCommand) []byte {
	t.Helper()
	b, err := json.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestHandlerSendsAndAcks(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := mocks.NewMockMailer(ctrl)
	m.EXPECT().Send(gomock.Any(), "a@x.com", "Subj", "<p>hi</p>").Return(nil)

	h := NewHandler(m)
	data := mustJSON(t, notify.EmailCommand{RecipientEmail: "a@x.com", Subject: "Subj", HTMLBody: "<p>hi</p>"})
	if err := h(context.Background(), notify.SubjectConfirmation, data); err != nil {
		t.Fatalf("want nil (ack), got %v", err)
	}
}

func TestHandlerSendFailureIsTransient(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := mocks.NewMockMailer(ctrl)
	m.EXPECT().Send(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Return(errors.New("smtp down"))

	h := NewHandler(m)
	data := mustJSON(t, notify.EmailCommand{RecipientEmail: "a@x.com", Subject: "S", HTMLBody: "b"})
	err := h(context.Background(), notify.SubjectRelease, data)
	if err == nil || errors.Is(err, ErrPermanent) {
		t.Fatalf("want transient error, got %v", err)
	}
}

func TestHandlerBadPayloadIsPermanent(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := mocks.NewMockMailer(ctrl) // Send must NOT be called

	h := NewHandler(m)
	err := h(context.Background(), notify.SubjectConfirmation, []byte("{not json"))
	if !errors.Is(err, ErrPermanent) {
		t.Fatalf("want permanent, got %v", err)
	}
}

func TestHandlerEmptyRecipientIsPermanent(t *testing.T) {
	ctrl := gomock.NewController(t)
	m := mocks.NewMockMailer(ctrl) // Send must NOT be called

	h := NewHandler(m)
	data := mustJSON(t, notify.EmailCommand{RecipientEmail: "", Subject: "S", HTMLBody: "b"})
	if err := h(context.Background(), notify.SubjectConfirmation, data); !errors.Is(err, ErrPermanent) {
		t.Fatalf("want permanent, got %v", err)
	}
}
