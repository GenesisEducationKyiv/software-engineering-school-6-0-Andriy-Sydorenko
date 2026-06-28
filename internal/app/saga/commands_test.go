package saga

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}

type stubActions struct {
	confirmErr error
	unsubErr   error
	gotToken   string
}

func (s *stubActions) ConfirmSubscription(_ context.Context, token string) error {
	s.gotToken = token
	return s.confirmErr
}

func (s *stubActions) Unsubscribe(_ context.Context, token string) error {
	s.gotToken = token
	return s.unsubErr
}

func TestCommandHandler_Confirm(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		st := &stubActions{}
		out, err := NewCommandHandler(st).Confirm(context.Background(), mustJSON(t, saga.ConfirmCommand{Token: "tok"}))
		require.NoError(t, err)
		require.Equal(t, saga.Reply{OK: true}, out)
		require.Equal(t, "tok", st.gotToken)
	})

	t.Run("token not found -> code, not error", func(t *testing.T) {
		out, err := NewCommandHandler(&stubActions{confirmErr: domain.ErrTokenNotFound}).
			Confirm(context.Background(), mustJSON(t, saga.ConfirmCommand{Token: "x"}))
		require.NoError(t, err)
		require.Equal(t, saga.Reply{OK: false, Code: saga.CodeTokenNotFound}, out)
	})

	t.Run("internal error propagates", func(t *testing.T) {
		_, err := NewCommandHandler(&stubActions{confirmErr: errors.New("db down")}).
			Confirm(context.Background(), mustJSON(t, saga.ConfirmCommand{Token: "x"}))
		require.Error(t, err)
	})
}

func TestCommandHandler_Unsubscribe(t *testing.T) {
	t.Run("ok", func(t *testing.T) {
		out, err := NewCommandHandler(&stubActions{}).
			Unsubscribe(context.Background(), mustJSON(t, saga.UnsubscribeCommand{Token: "tok"}))
		require.NoError(t, err)
		require.Equal(t, saga.Reply{OK: true}, out)
	})

	t.Run("token not found -> code", func(t *testing.T) {
		out, err := NewCommandHandler(&stubActions{unsubErr: domain.ErrTokenNotFound}).
			Unsubscribe(context.Background(), mustJSON(t, saga.UnsubscribeCommand{Token: "x"}))
		require.NoError(t, err)
		require.Equal(t, saga.Reply{OK: false, Code: saga.CodeTokenNotFound}, out)
	})
}
