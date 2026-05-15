package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/domain"
)

func init() {
	gin.SetMode(gin.TestMode)
}

type fakeService struct {
	subscribeErr    error
	confirmErr      error
	unsubscribeErr  error
	getSubs         []domain.SubscriptionResponse
	getSubsErr      error
	subscribeCalled bool
	unsubCalledTok  string
	confirmCalledTk string
}

func (f *fakeService) Subscribe(_ context.Context, _ domain.SubscribeRequest) error {
	f.subscribeCalled = true
	return f.subscribeErr
}

func (f *fakeService) ConfirmSubscription(_ context.Context, token string) error {
	f.confirmCalledTk = token
	return f.confirmErr
}

func (f *fakeService) Unsubscribe(_ context.Context, token string) error {
	f.unsubCalledTok = token
	return f.unsubscribeErr
}

func (f *fakeService) GetSubscriptions(_ context.Context, _ string) ([]domain.SubscriptionResponse, error) {
	return f.getSubs, f.getSubsErr
}

func newTestRouter(h *Handler) *gin.Engine {
	r := gin.New()
	r.POST("/api/subscribe", h.Subscribe)
	r.GET("/api/confirm/:token", h.ConfirmSubscription)
	r.GET("/api/unsubscribe/:token", h.Unsubscribe)
	r.GET("/api/subscriptions", h.GetSubscriptions)
	return r
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSubscribe(t *testing.T) {
	tests := []struct {
		name       string
		body       any
		svcErr     error
		wantStatus int
	}{
		{"200 happy path", domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, nil, http.StatusOK},
		{"400 missing email", map[string]string{"repo": "golang/go"}, nil, http.StatusBadRequest},
		{"400 invalid email", map[string]string{"email": "not-email", "repo": "golang/go"}, nil, http.StatusBadRequest},
		{"400 invalid repo format", domain.SubscribeRequest{Email: "a@b.com", Repo: "invalid"}, domain.ErrInvalidRepoFormat, http.StatusBadRequest},
		{"404 repo not on github", domain.SubscribeRequest{Email: "a@b.com", Repo: "ghost/ghost"}, domain.ErrRepoNotFound, http.StatusNotFound},
		{"409 already subscribed", domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, domain.ErrAlreadySubscribed, http.StatusConflict},
		{"503 github rate limited", domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"}, domain.ErrRateLimited, http.StatusServiceUnavailable},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{subscribeErr: tc.svcErr}
			r := newTestRouter(NewHandler(svc))

			w := doJSON(t, r, http.MethodPost, "/api/subscribe", tc.body)

			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestConfirm(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		svcErr     error
		wantStatus int
	}{
		{"200 valid token", "abc", nil, http.StatusOK},
		{"404 token not found", "missing", domain.ErrTokenNotFound, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{confirmErr: tc.svcErr}
			r := newTestRouter(NewHandler(svc))

			w := doJSON(t, r, http.MethodGet, "/api/confirm/"+tc.token, nil)

			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestUnsubscribe(t *testing.T) {
	tests := []struct {
		name       string
		token      string
		svcErr     error
		wantStatus int
	}{
		{"200 valid token", "tok", nil, http.StatusOK},
		{"404 token not found", "missing", domain.ErrTokenNotFound, http.StatusNotFound},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeService{unsubscribeErr: tc.svcErr}
			r := newTestRouter(NewHandler(svc))

			w := doJSON(t, r, http.MethodGet, "/api/unsubscribe/"+tc.token, nil)

			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == http.StatusOK && svc.unsubCalledTok != tc.token {
				t.Fatalf("svc got token %q, want %q", svc.unsubCalledTok, tc.token)
			}
		})
	}
}

func TestGetSubscriptions(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		svc        *fakeService
		wantStatus int
		wantLen    int
	}{
		{"200 returns list", "?email=a@b.com",
			&fakeService{getSubs: []domain.SubscriptionResponse{
				{Email: "a@b.com", Repo: "golang/go", Confirmed: true, LastSeenTag: "v1"},
			}}, http.StatusOK, 1},
		{"200 empty list", "?email=nobody@b.com", &fakeService{}, http.StatusOK, 0},
		{"400 missing email", "", &fakeService{getSubsErr: domain.ErrInvalidEmail}, http.StatusBadRequest, 0},
		{"400 invalid email", "?email=not-email", &fakeService{getSubsErr: domain.ErrInvalidEmail}, http.StatusBadRequest, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRouter(NewHandler(tc.svc))

			w := doJSON(t, r, http.MethodGet, "/api/subscriptions"+tc.query, nil)

			if w.Code != tc.wantStatus {
				t.Fatalf("status=%d, want %d, body=%s", w.Code, tc.wantStatus, w.Body.String())
			}
			if tc.wantStatus == http.StatusOK {
				var got []domain.SubscriptionResponse
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatalf("decode response: %v", err)
				}
				if len(got) != tc.wantLen {
					t.Fatalf("got %d subs, want %d", len(got), tc.wantLen)
				}
			}
		})
	}
}
