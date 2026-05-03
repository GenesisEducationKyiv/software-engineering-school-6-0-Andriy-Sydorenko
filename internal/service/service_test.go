package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

type mockRepo struct {
	findExisting        *domain.Subscription
	findExistingErr     error
	findByEmail         []domain.Subscription
	findByEmailErr      error
	findByUnsubToken    *domain.Subscription
	findByUnsubTokenErr error
	createErr           error
	createTokenErr      error
	findToken           *domain.ConfirmationToken
	findTokenErr        error
	confirmErr          error
	deleteSubErr        error
	deleteTokenErr      error

	createdSub     *domain.Subscription
	createdToken   *domain.ConfirmationToken
	confirmedID    uint
	deletedSubID   uint
	deletedTokenID uint
}

func (m *mockRepo) CreateSubscription(_ context.Context, sub *domain.Subscription) error {
	m.createdSub = sub
	if m.createErr == nil {
		sub.ID = 1
	}
	return m.createErr
}

func (m *mockRepo) FindSubscriptionByEmailAndRepo(_ context.Context, _, _ string) (*domain.Subscription, error) {
	return m.findExisting, m.findExistingErr
}

func (m *mockRepo) FindSubscriptionsByEmail(_ context.Context, _ string) ([]domain.Subscription, error) {
	return m.findByEmail, m.findByEmailErr
}

func (m *mockRepo) FindSubscriptionByUnsubscribeToken(_ context.Context, _ string) (*domain.Subscription, error) {
	return m.findByUnsubToken, m.findByUnsubTokenErr
}

func (m *mockRepo) DeleteSubscription(_ context.Context, id uint) error {
	m.deletedSubID = id
	return m.deleteSubErr
}

func (m *mockRepo) ConfirmSubscription(_ context.Context, id uint) error {
	m.confirmedID = id
	return m.confirmErr
}

func (m *mockRepo) CreateToken(_ context.Context, token *domain.ConfirmationToken) error {
	m.createdToken = token
	return m.createTokenErr
}

func (m *mockRepo) FindTokenByValue(_ context.Context, _ string) (*domain.ConfirmationToken, error) {
	return m.findToken, m.findTokenErr
}

func (m *mockRepo) DeleteToken(_ context.Context, id uint) error {
	m.deletedTokenID = id
	return m.deleteTokenErr
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

func (m *mockNotifier) SendConfirmation(email, repo, token, unsubscribeToken string) error {
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
			s := New(tc.repo, tc.github, tc.notifier)
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
	s := New(repo, &mockGitHub{}, &mockNotifier{err: errors.New("smtp down")})

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
			s := New(tc.repo, &mockGitHub{}, &mockNotifier{})
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
