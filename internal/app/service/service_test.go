package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/service/mocks"
)

type fixture struct {
	repo   *mocks.MockSubscriptionRepo
	tokens *mocks.MockTokenRepo
	events *mocks.MockEventPublisher
	svc    *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		repo:   mocks.NewMockSubscriptionRepo(ctrl),
		tokens: mocks.NewMockTokenRepo(ctrl),
		events: mocks.NewMockEventPublisher(ctrl),
	}
	f.svc = New(f.repo, f.tokens, f.events)
	return f
}

func TestConfirmSubscription(t *testing.T) {
	t.Run(
		"valid token confirms and deletes token", func(t *testing.T) {
			f := newFixture(t)
			f.tokens.EXPECT().FindTokenByValue(gomock.Any(), "abc123").
				Return(&domain.ConfirmationToken{ID: 7, SubscriptionID: 42}, nil)
			f.repo.EXPECT().ConfirmSubscription(gomock.Any(), uint(42)).Return(nil)
			f.tokens.EXPECT().DeleteToken(gomock.Any(), uint(7)).Return(nil)

			require.NoError(t, f.svc.ConfirmSubscription(context.Background(), "abc123"))
		},
	)

	t.Run(
		"empty token rejected without lookup", func(t *testing.T) {
			f := newFixture(t)
			err := f.svc.ConfirmSubscription(context.Background(), "")
			require.ErrorIs(t, err, domain.ErrTokenNotFound)
		},
	)

	t.Run(
		"unknown token rejected", func(t *testing.T) {
			f := newFixture(t)
			f.tokens.EXPECT().FindTokenByValue(gomock.Any(), "missing").Return(nil, nil)

			err := f.svc.ConfirmSubscription(context.Background(), "missing")
			require.ErrorIs(t, err, domain.ErrTokenNotFound)
		},
	)

	t.Run(
		"lookup error propagates", func(t *testing.T) {
			f := newFixture(t)
			f.tokens.EXPECT().FindTokenByValue(gomock.Any(), gomock.Any()).Return(
				nil,
				errors.New("db oops"),
			)

			err := f.svc.ConfirmSubscription(context.Background(), "tok")
			require.Error(t, err)
		},
	)

	t.Run(
		"confirm failure aborts before token delete", func(t *testing.T) {
			// No DeleteToken EXPECT: must not delete the token if confirm failed.
			f := newFixture(t)
			f.tokens.EXPECT().FindTokenByValue(gomock.Any(), gomock.Any()).
				Return(&domain.ConfirmationToken{ID: 7, SubscriptionID: 42}, nil)
			f.repo.EXPECT().ConfirmSubscription(
				gomock.Any(),
				uint(42),
			).Return(errors.New("confirm failed"))

			err := f.svc.ConfirmSubscription(context.Background(), "tok")
			require.Error(t, err)
		},
	)

	t.Run(
		"delete-token failure swallowed", func(t *testing.T) {
			// Sub is confirmed; failing to delete the spent token is a log line, not a user error.
			f := newFixture(t)
			f.tokens.EXPECT().FindTokenByValue(gomock.Any(), gomock.Any()).
				Return(&domain.ConfirmationToken{ID: 7, SubscriptionID: 42}, nil)
			f.repo.EXPECT().ConfirmSubscription(gomock.Any(), uint(42)).Return(nil)
			f.tokens.EXPECT().DeleteToken(gomock.Any(), uint(7)).Return(errors.New("delete failed"))

			require.NoError(t, f.svc.ConfirmSubscription(context.Background(), "tok"))
		},
	)
}

func TestUnsubscribe(t *testing.T) {
	t.Run(
		"valid token deletes subscription and publishes cleanup event", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(gomock.Any(), "u").
				Return(&domain.Subscription{ID: 9, PublicID: "pid-9", Repo: "o/r"}, nil)
			f.repo.EXPECT().DeleteSubscription(gomock.Any(), uint(9)).Return(nil)
			f.events.EXPECT().SubscriptionRemoved(gomock.Any(), "pid-9", "o/r").Return(nil)

			require.NoError(t, f.svc.Unsubscribe(context.Background(), "u"))
		},
	)

	t.Run(
		"event publish failure is swallowed", func(t *testing.T) {
			// Delete is the user's goal; a failed cleanup event is benign (logged, not 5xx).
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(gomock.Any(), "u").
				Return(&domain.Subscription{ID: 9, PublicID: "pid-9", Repo: "o/r"}, nil)
			f.repo.EXPECT().DeleteSubscription(gomock.Any(), uint(9)).Return(nil)
			f.events.EXPECT().SubscriptionRemoved(gomock.Any(), "pid-9", "o/r").
				Return(errors.New("nats down"))

			require.NoError(t, f.svc.Unsubscribe(context.Background(), "u"))
		},
	)

	t.Run(
		"empty token rejected without lookup", func(t *testing.T) {
			f := newFixture(t)
			require.ErrorIs(t, f.svc.Unsubscribe(context.Background(), ""), domain.ErrTokenNotFound)
		},
	)

	t.Run(
		"unknown token rejected", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(
				gomock.Any(),
				gomock.Any(),
			).Return(nil, nil)
			require.ErrorIs(
				t,
				f.svc.Unsubscribe(context.Background(), "ghost"),
				domain.ErrTokenNotFound,
			)
		},
	)

	t.Run(
		"lookup error propagates", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(gomock.Any(), gomock.Any()).
				Return(nil, errors.New("db oops"))
			require.Error(t, f.svc.Unsubscribe(context.Background(), "u"))
		},
	)

	t.Run(
		"delete error propagates (no event)", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(gomock.Any(), gomock.Any()).
				Return(&domain.Subscription{ID: 9, PublicID: "pid-9", Repo: "o/r"}, nil)
			f.repo.EXPECT().DeleteSubscription(
				gomock.Any(),
				uint(9),
			).Return(errors.New("delete oops"))
			require.Error(t, f.svc.Unsubscribe(context.Background(), "u"))
		},
	)
}

func TestGetSubscriptions(t *testing.T) {
	t.Run(
		"returns list", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionsByEmail(gomock.Any(), "a@b.com").Return(
				[]domain.Subscription{
					{ID: 1, Email: "a@b.com", Repo: "golang/go"},
					{ID: 2, Email: "a@b.com", Repo: "kubernetes/kubernetes"},
				}, nil,
			)

			got, err := f.svc.GetSubscriptions(context.Background(), "a@b.com")
			require.NoError(t, err)
			assert.Len(t, got, 2)
		},
	)

	t.Run(
		"empty list returns empty slice", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionsByEmail(gomock.Any(), gomock.Any()).Return(nil, nil)

			got, err := f.svc.GetSubscriptions(context.Background(), "a@b.com")
			require.NoError(t, err)
			assert.Empty(t, got)
		},
	)

	t.Run(
		"invalid email rejected without lookup", func(t *testing.T) {
			cases := []string{"   ", "not-an-email", ""}
			for _, email := range cases {
				t.Run(
					email, func(t *testing.T) {
						f := newFixture(t)
						_, err := f.svc.GetSubscriptions(context.Background(), email)
						require.ErrorIs(t, err, domain.ErrInvalidEmail)
					},
				)
			}
		},
	)

	t.Run(
		"repo error wrapped", func(t *testing.T) {
			boom := errors.New("db oops")
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionsByEmail(gomock.Any(), gomock.Any()).Return(nil, boom)

			_, err := f.svc.GetSubscriptions(context.Background(), "a@b.com")
			require.ErrorIs(t, err, boom)
		},
	)
}
