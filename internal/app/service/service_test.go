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

// fixedTokens cycles unsub, then confirm — order matches Service.Subscribe.
func fixedTokens() TokenGenerator {
	values := []string{"unsub-token", "confirm-token"}
	i := 0
	return func() (string, error) {
		v := values[i%len(values)]
		i++
		return v, nil
	}
}

type fixture struct {
	repo     *mocks.MockSubscriptionRepo
	tokens   *mocks.MockTokenRepo
	github   *mocks.MockRepoValidator
	notifier *mocks.MockConfirmationSender
	svc      *Service
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	f := &fixture{
		repo:     mocks.NewMockSubscriptionRepo(ctrl),
		tokens:   mocks.NewMockTokenRepo(ctrl),
		github:   mocks.NewMockRepoValidator(ctrl),
		notifier: mocks.NewMockConfirmationSender(ctrl),
	}
	f.svc = New(f.repo, f.tokens, f.github, f.notifier, fixedTokens())
	return f
}

func TestSubscribe_HappyPath(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.repo.EXPECT().
		FindSubscriptionByEmailAndRepo(ctx, "a@b.com", "golang/go").
		Return(nil, nil)
	f.github.EXPECT().ValidateRepo(ctx, "golang", "go").Return(nil)
	f.repo.EXPECT().
		CreateSubscriptionWithToken(ctx, gomock.Any(), gomock.Any()).
		DoAndReturn(
			func(_ context.Context, sub *domain.Subscription, tok *domain.ConfirmationToken) error {
				assert.Equal(t, "unsub-token", sub.UnsubscribeToken)
				assert.Equal(t, "confirm-token", tok.Token)
				return nil
			},
		)
	f.notifier.EXPECT().
		SendConfirmation(ctx, "a@b.com", "golang/go", "confirm-token", "unsub-token").
		Return(nil)

	require.NoError(
		t,
		f.svc.Subscribe(ctx, domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}),
	)
}

func TestSubscribe_RepoFormatRejected(t *testing.T) {
	// No EXPECTs set: bad format must short-circuit before any repo/github call.
	cases := []string{"invalid", "a/b/c", "", "/repo", "owner/"}
	for _, repo := range cases {
		t.Run(
			repo, func(t *testing.T) {
				f := newFixture(t)
				err := f.svc.Subscribe(
					context.Background(),
					domain.SubscribeRequest{Email: "a@b.com", Repo: repo},
				)
				require.ErrorIs(t, err, domain.ErrInvalidRepoFormat)
			},
		)
	}
}

func TestSubscribe_DuplicateRejected(t *testing.T) {
	f := newFixture(t)
	f.repo.EXPECT().
		FindSubscriptionByEmailAndRepo(gomock.Any(), "a@b.com", "golang/go").
		Return(&domain.Subscription{ID: 1}, nil)

	err := f.svc.Subscribe(
		context.Background(),
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
	)
	require.ErrorIs(t, err, domain.ErrAlreadySubscribed)
}

func TestSubscribe_GitHubErrorPropagates(t *testing.T) {
	cases := []struct {
		name string
		err  error
	}{
		{"repo not found", domain.ErrRepoNotFound},
		{"rate limited", domain.ErrRateLimited},
	}
	for _, tc := range cases {
		t.Run(
			tc.name, func(t *testing.T) {
				f := newFixture(t)
				f.repo.EXPECT().FindSubscriptionByEmailAndRepo(
					gomock.Any(),
					gomock.Any(),
					gomock.Any(),
				).Return(nil, nil)
				f.github.EXPECT().ValidateRepo(gomock.Any(), "golang", "go").Return(tc.err)

				err := f.svc.Subscribe(
					context.Background(),
					domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
				)
				require.ErrorIs(t, err, tc.err)
			},
		)
	}
}

func TestSubscribe_NotifierFailureSwallowed(t *testing.T) {
	// Persist-then-send: row is the commitment, SMTP failure must not 5xx.
	f := newFixture(t)
	f.repo.EXPECT().FindSubscriptionByEmailAndRepo(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil, nil)
	f.github.EXPECT().ValidateRepo(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	f.repo.EXPECT().CreateSubscriptionWithToken(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil)
	f.notifier.EXPECT().SendConfirmation(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).
		Return(errors.New("smtp down"))

	require.NoError(
		t,
		f.svc.Subscribe(
			context.Background(),
			domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
		),
	)
}

func TestSubscribe_RepoLookupErrorPropagates(t *testing.T) {
	boom := errors.New("db down")
	f := newFixture(t)
	f.repo.EXPECT().FindSubscriptionByEmailAndRepo(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil, boom)

	err := f.svc.Subscribe(
		context.Background(),
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
	)
	require.ErrorIs(t, err, boom)
}

func TestSubscribe_PersistErrorPropagatesAndSkipsEmail(t *testing.T) {
	boom := errors.New("insert failed")
	f := newFixture(t)
	f.repo.EXPECT().FindSubscriptionByEmailAndRepo(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil, nil)
	f.github.EXPECT().ValidateRepo(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)
	f.repo.EXPECT().CreateSubscriptionWithToken(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(boom)

	err := f.svc.Subscribe(
		context.Background(),
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
	)
	require.ErrorIs(t, err, boom)
}

func TestSubscribe_TokenGenerationErrorAborts(t *testing.T) {
	boom := errors.New("rng exhausted")
	ctrl := gomock.NewController(t)
	repo := mocks.NewMockSubscriptionRepo(ctrl)
	tokens := mocks.NewMockTokenRepo(ctrl)
	github := mocks.NewMockRepoValidator(ctrl)
	notifier := mocks.NewMockConfirmationSender(ctrl)

	repo.EXPECT().FindSubscriptionByEmailAndRepo(
		gomock.Any(),
		gomock.Any(),
		gomock.Any(),
	).Return(nil, nil)
	github.EXPECT().ValidateRepo(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil)

	failingGen := func() (string, error) { return "", boom }
	svc := New(repo, tokens, github, notifier, failingGen)

	err := svc.Subscribe(
		context.Background(),
		domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
	)
	require.ErrorIs(t, err, boom)
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
		"valid token deletes subscription", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(gomock.Any(), "u").
				Return(&domain.Subscription{ID: 9}, nil)
			f.repo.EXPECT().DeleteSubscription(gomock.Any(), uint(9)).Return(nil)

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
		"delete error propagates", func(t *testing.T) {
			f := newFixture(t)
			f.repo.EXPECT().FindSubscriptionByUnsubscribeToken(gomock.Any(), gomock.Any()).
				Return(&domain.Subscription{ID: 9}, nil)
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

func TestRepoFormatRegex(t *testing.T) {
	tests := []struct {
		input string
		valid bool
	}{
		{"golang/go", true},
		{"a/b", true},
		{"owner-name/repo.name_1", true},
		{"invalid", false},
		{"a/b/c", false},
		{"", false},
		{"/repo", false},
		{"owner/", false},
		{"own er/repo", false},
	}
	for _, tc := range tests {
		t.Run(
			tc.input, func(t *testing.T) {
				assert.Equal(t, tc.valid, repoFormatRegex.MatchString(tc.input))
			},
		)
	}
}
