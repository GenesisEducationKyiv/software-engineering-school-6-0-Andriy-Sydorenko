package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

// mockRepo only exposes the dial-knobs the current test suite actually
// uses. Error-injection fields can be added back at the point a test
// needs them — speculative knobs were pruned to keep the mock honest.
type mockRepo struct {
	findExisting     *domain.Subscription
	findByEmail      []domain.Subscription
	findByUnsubToken *domain.Subscription
	findToken        *domain.ConfirmationToken

	findExistingErr error
	findByEmailErr  error
	findUnsubErr    error
	findTokenErr    error
	createErr       error
	confirmErr      error
	deleteSubErr    error
	deleteTokenErr  error

	createdSub     *domain.Subscription
	createdToken   *domain.ConfirmationToken
	confirmedID    uint
	deletedSubID   uint
	deletedTokenID uint
}

func (m *mockRepo) CreateSubscriptionWithToken(_ context.Context, sub *domain.Subscription, token *domain.ConfirmationToken) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.createdSub = sub
	m.createdToken = token
	sub.ID = 1
	token.SubscriptionID = sub.ID
	return nil
}

func (m *mockRepo) FindSubscriptionByEmailAndRepo(_ context.Context, _, _ string) (*domain.Subscription, error) {
	return m.findExisting, m.findExistingErr
}

func (m *mockRepo) FindSubscriptionsByEmail(_ context.Context, _ string) ([]domain.Subscription, error) {
	return m.findByEmail, m.findByEmailErr
}

func (m *mockRepo) FindSubscriptionByUnsubscribeToken(_ context.Context, _ string) (*domain.Subscription, error) {
	return m.findByUnsubToken, m.findUnsubErr
}

func (m *mockRepo) DeleteSubscription(_ context.Context, id uint) error {
	m.deletedSubID = id
	return m.deleteSubErr
}

func (m *mockRepo) ConfirmSubscription(_ context.Context, id uint) error {
	m.confirmedID = id
	return m.confirmErr
}

func (m *mockRepo) FindTokenByValue(_ context.Context, _ string) (*domain.ConfirmationToken, error) {
	return m.findToken, m.findTokenErr
}

func (m *mockRepo) DeleteToken(_ context.Context, id uint) error {
	m.deletedTokenID = id
	return m.deleteTokenErr
}

// fixedTokens returns a fresh deterministic TokenGenerator that cycles
// through "unsub-token", "confirm-token", ... so tests can assert on
// persisted values without faking crypto/rand.
func fixedTokens() TokenGenerator {
	values := []string{"unsub-token", "confirm-token"}
	i := 0
	return func() (string, error) {
		v := values[i%len(values)]
		i++
		return v, nil
	}
}

type mockGitHub struct {
	err error
}

func (m *mockGitHub) ValidateRepo(_ context.Context, _, _ string) error {
	return m.err
}

type mockNotifier struct {
	err            error
	sentEmail      string
	sentRepo       string
	sentToken      string
	sentUnsubToken string
	callCount      int
}

func (m *mockNotifier) SendConfirmation(_ context.Context, email, repo, token, unsubscribeToken string) error {
	m.callCount++
	m.sentEmail = email
	m.sentRepo = repo
	m.sentToken = token
	m.sentUnsubToken = unsubscribeToken
	return m.err
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name        string
		req         domain.SubscribeRequest
		repo        *mockRepo
		github      *mockGitHub
		notifier    *mockNotifier
		wantErr     error
		wantCreated bool
	}{
		{
			name:        "valid subscription",
			req:         domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
			repo:        &mockRepo{},
			github:      &mockGitHub{},
			notifier:    &mockNotifier{},
			wantErr:     nil,
			wantCreated: true,
		},
		{
			name:     "invalid repo format - no slash",
			req:      domain.SubscribeRequest{Email: "a@b.com", Repo: "invalid"},
			repo:     &mockRepo{},
			github:   &mockGitHub{},
			notifier: &mockNotifier{},
			wantErr:  domain.ErrInvalidRepoFormat,
		},
		{
			name:     "invalid repo format - too many slashes",
			req:      domain.SubscribeRequest{Email: "a@b.com", Repo: "a/b/c"},
			repo:     &mockRepo{},
			github:   &mockGitHub{},
			notifier: &mockNotifier{},
			wantErr:  domain.ErrInvalidRepoFormat,
		},
		{
			name:     "duplicate subscription",
			req:      domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
			repo:     &mockRepo{findExisting: &domain.Subscription{ID: 1}},
			github:   &mockGitHub{},
			notifier: &mockNotifier{},
			wantErr:  domain.ErrAlreadySubscribed,
		},
		{
			name:     "repo not on github",
			req:      domain.SubscribeRequest{Email: "a@b.com", Repo: "ghost/ghost"},
			repo:     &mockRepo{},
			github:   &mockGitHub{err: domain.ErrRepoNotFound},
			notifier: &mockNotifier{},
			wantErr:  domain.ErrRepoNotFound,
		},
		{
			name:     "github rate limited",
			req:      domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
			repo:     &mockRepo{},
			github:   &mockGitHub{err: domain.ErrRateLimited},
			notifier: &mockNotifier{},
			wantErr:  domain.ErrRateLimited,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.repo, tc.github, tc.notifier, fixedTokens())
			err := s.Subscribe(context.Background(), tc.req)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err=%v, want %v", err, tc.wantErr)
			}

			if tc.wantCreated {
				assertSubscribeSideEffects(t, tc.repo, tc.notifier, tc.req.Email)
			}
		})
	}
}

func assertSubscribeSideEffects(t *testing.T, repo *mockRepo, notifier *mockNotifier, wantEmail string) {
	t.Helper()
	if repo.createdSub == nil {
		t.Fatal("expected subscription created")
	}
	if repo.createdToken == nil {
		t.Fatal("expected confirmation token created")
	}
	if notifier.callCount != 1 {
		t.Fatalf("expected 1 confirmation email, got %d", notifier.callCount)
	}
	if notifier.sentEmail != wantEmail {
		t.Fatalf("notifier got email %q, want %q", notifier.sentEmail, wantEmail)
	}
}

func TestSubscribeNotifierFailureSwallowed(t *testing.T) {
	repo := &mockRepo{}
	s := New(repo, &mockGitHub{}, &mockNotifier{err: errors.New("smtp down")}, fixedTokens())

	err := s.Subscribe(context.Background(), domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"})
	if err != nil {
		t.Fatalf("expected nil error when notifier fails, got %v", err)
	}
	if repo.createdSub == nil {
		t.Fatal("subscription should still be created")
	}
}

func TestConfirmSubscription(t *testing.T) {
	tests := []struct {
		name           string
		tokenValue     string
		repo           *mockRepo
		wantErr        error
		wantConfirmed  uint
		wantDeletedTok uint
	}{
		{
			name:           "valid token",
			tokenValue:     "abc123",
			repo:           &mockRepo{findToken: &domain.ConfirmationToken{ID: 7, SubscriptionID: 42}},
			wantErr:        nil,
			wantConfirmed:  42,
			wantDeletedTok: 7,
		},
		{
			name:       "empty token",
			tokenValue: "",
			repo:       &mockRepo{},
			wantErr:    domain.ErrTokenNotFound,
		},
		{
			name:       "token not found",
			tokenValue: "missing",
			repo:       &mockRepo{findToken: nil},
			wantErr:    domain.ErrTokenNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())
			err := s.ConfirmSubscription(context.Background(), tc.tokenValue)

			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("got err=%v, want %v", err, tc.wantErr)
			}
			if tc.wantConfirmed != 0 && tc.repo.confirmedID != tc.wantConfirmed {
				t.Fatalf("confirmed id=%d, want %d", tc.repo.confirmedID, tc.wantConfirmed)
			}
			if tc.wantDeletedTok != 0 && tc.repo.deletedTokenID != tc.wantDeletedTok {
				t.Fatalf("deleted token id=%d, want %d", tc.repo.deletedTokenID, tc.wantDeletedTok)
			}
		})
	}
}

func TestSubscribeRepoLookupError(t *testing.T) {
	boom := errors.New("db down")
	repo := &mockRepo{findExistingErr: boom}
	s := New(repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())

	err := s.Subscribe(context.Background(), domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped db error, got %v", err)
	}
	if repo.createdSub != nil {
		t.Fatal("must not persist on lookup failure")
	}
}

func TestSubscribePersistError(t *testing.T) {
	boom := errors.New("insert failed")
	repo := &mockRepo{createErr: boom}
	notifier := &mockNotifier{}
	s := New(repo, &mockGitHub{}, notifier, fixedTokens())

	err := s.Subscribe(context.Background(), domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped insert error, got %v", err)
	}
	if notifier.callCount != 0 {
		t.Fatal("must not send email when persist failed")
	}
}

func TestSubscribeTokenGenerationError(t *testing.T) {
	boom := errors.New("rng exhausted")
	failingGen := func() (string, error) { return "", boom }
	repo := &mockRepo{}
	s := New(repo, &mockGitHub{}, &mockNotifier{}, failingGen)

	err := s.Subscribe(context.Background(), domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"})
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("expected wrapped token error, got %v", err)
	}
	if repo.createdSub != nil {
		t.Fatal("must not persist when token generation fails")
	}
}

func TestUnsubscribe(t *testing.T) {
	tests := []struct {
		name        string
		token       string
		repo        *mockRepo
		wantErr     error
		wantDeleted uint
	}{
		{
			name:        "valid token deletes subscription",
			token:       "u",
			repo:        &mockRepo{findByUnsubToken: &domain.Subscription{ID: 9}},
			wantDeleted: 9,
		},
		{
			name:    "empty token",
			token:   "",
			repo:    &mockRepo{},
			wantErr: domain.ErrTokenNotFound,
		},
		{
			name:    "unknown token",
			token:   "ghost",
			repo:    &mockRepo{findByUnsubToken: nil},
			wantErr: domain.ErrTokenNotFound,
		},
		{
			name:    "lookup error",
			token:   "u",
			repo:    &mockRepo{findUnsubErr: errors.New("db oops")},
			wantErr: errors.New("db oops"),
		},
		{
			name:    "delete error",
			token:   "u",
			repo:    &mockRepo{findByUnsubToken: &domain.Subscription{ID: 9}, deleteSubErr: errors.New("delete oops")},
			wantErr: errors.New("delete oops"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())
			err := s.Unsubscribe(context.Background(), tc.token)

			if tc.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tc.repo.deletedSubID != tc.wantDeleted {
					t.Fatalf("deleted id=%d, want %d", tc.repo.deletedSubID, tc.wantDeleted)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected error %v, got nil", tc.wantErr)
			}
			if errors.Is(tc.wantErr, domain.ErrTokenNotFound) && !errors.Is(err, domain.ErrTokenNotFound) {
				t.Fatalf("got err=%v, want ErrTokenNotFound", err)
			}
		})
	}
}

func TestConfirmSubscriptionLookupError(t *testing.T) {
	repo := &mockRepo{findTokenErr: errors.New("db oops")}
	s := New(repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())

	err := s.ConfirmSubscription(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error on lookup failure")
	}
	if repo.confirmedID != 0 {
		t.Fatal("must not confirm on lookup failure")
	}
}

func TestConfirmSubscriptionDeleteTokenFailureSwallowed(t *testing.T) {
	repo := &mockRepo{
		findToken:      &domain.ConfirmationToken{ID: 7, SubscriptionID: 42},
		deleteTokenErr: errors.New("delete failed"),
	}
	s := New(repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())

	err := s.ConfirmSubscription(context.Background(), "tok")
	if err != nil {
		t.Fatalf("delete-token failure must not surface, got %v", err)
	}
	if repo.confirmedID != 42 {
		t.Fatalf("subscription must still be confirmed, got id=%d", repo.confirmedID)
	}
}

func TestConfirmSubscriptionConfirmError(t *testing.T) {
	repo := &mockRepo{
		findToken:  &domain.ConfirmationToken{ID: 7, SubscriptionID: 42},
		confirmErr: errors.New("confirm failed"),
	}
	s := New(repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())

	err := s.ConfirmSubscription(context.Background(), "tok")
	if err == nil {
		t.Fatal("expected error when confirm fails")
	}
	if repo.deletedTokenID != 0 {
		t.Fatal("must not delete token when confirm fails")
	}
}

func TestGetSubscriptionsSuccess(t *testing.T) {
	tests := []struct {
		name    string
		repo    *mockRepo
		wantLen int
	}{
		{
			name: "returns list",
			repo: &mockRepo{findByEmail: []domain.Subscription{
				{ID: 1, Email: "a@b.com", Repo: "golang/go"},
				{ID: 2, Email: "a@b.com", Repo: "kubernetes/kubernetes"},
			}},
			wantLen: 2,
		},
		{
			name:    "empty list returns empty slice",
			repo:    &mockRepo{findByEmail: nil},
			wantLen: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := New(tc.repo, &mockGitHub{}, &mockNotifier{}, fixedTokens())
			got, err := s.GetSubscriptions(context.Background(), "a@b.com")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Fatalf("got %d subs, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestGetSubscriptionsInvalidEmail(t *testing.T) {
	cases := []string{"   ", "not-an-email", ""}
	for _, email := range cases {
		t.Run(email, func(t *testing.T) {
			s := New(&mockRepo{}, &mockGitHub{}, &mockNotifier{}, fixedTokens())
			_, err := s.GetSubscriptions(context.Background(), email)
			if !errors.Is(err, domain.ErrInvalidEmail) {
				t.Fatalf("got err=%v, want ErrInvalidEmail", err)
			}
		})
	}
}

func TestGetSubscriptionsRepoErrorWrapped(t *testing.T) {
	boom := errors.New("db oops")
	s := New(&mockRepo{findByEmailErr: boom}, &mockGitHub{}, &mockNotifier{}, fixedTokens())

	_, err := s.GetSubscriptions(context.Background(), "a@b.com")
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("got err=%v, want wrapped %v", err, boom)
	}
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
		t.Run(tc.input, func(t *testing.T) {
			if got := repoFormatRegex.MatchString(tc.input); got != tc.valid {
				t.Fatalf("repoFormatRegex(%q) = %v, want %v", tc.input, got, tc.valid)
			}
		})
	}
}
