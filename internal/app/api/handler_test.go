package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/api/mocks"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/app/domain"
)

func init() {
	gin.SetMode(gin.TestMode)
}

func newTestRouter(t *testing.T) (*gin.Engine, *mocks.MockService) {
	t.Helper()
	ctrl := gomock.NewController(t)
	svc := mocks.NewMockService(ctrl)
	h := NewHandler(svc)

	r := gin.New()
	r.POST("/api/subscribe", h.Subscribe)
	r.GET("/api/confirm/:token", h.ConfirmSubscription)
	r.GET("/api/unsubscribe/:token", h.Unsubscribe)
	r.GET("/api/subscriptions", h.GetSubscriptions)
	return r, svc
}

func doJSON(t *testing.T, r *gin.Engine, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestSubscribe(t *testing.T) {
	t.Run(
		"200 happy path", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().Subscribe(
				gomock.Any(),
				domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
			).Return(nil)

			w := doJSON(
				t,
				r,
				http.MethodPost,
				"/api/subscribe",
				domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
			)
			assert.Equal(t, http.StatusOK, w.Code)
		},
	)

	t.Run(
		"400 missing email — no service call", func(t *testing.T) {
			// Binding rejects at the handler boundary; svc must not be called.
			r, _ := newTestRouter(t)
			w := doJSON(
				t,
				r,
				http.MethodPost,
				"/api/subscribe",
				map[string]string{"repo": "golang/go"},
			)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		},
	)

	t.Run(
		"400 invalid email — no service call", func(t *testing.T) {
			r, _ := newTestRouter(t)
			w := doJSON(
				t,
				r,
				http.MethodPost,
				"/api/subscribe",
				map[string]string{"email": "not-email", "repo": "golang/go"},
			)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		},
	)

	cases := []struct {
		name       string
		svcErr     error
		wantStatus int
	}{
		{"400 invalid repo format", domain.ErrInvalidRepoFormat, http.StatusBadRequest},
		{"404 repo not on github", domain.ErrRepoNotFound, http.StatusNotFound},
		{"409 already subscribed", domain.ErrAlreadySubscribed, http.StatusConflict},
		{"503 github rate limited", domain.ErrRateLimited, http.StatusServiceUnavailable},
	}
	for _, tc := range cases {
		t.Run(
			tc.name, func(t *testing.T) {
				r, svc := newTestRouter(t)
				svc.EXPECT().Subscribe(gomock.Any(), gomock.Any()).Return(tc.svcErr)

				w := doJSON(
					t,
					r,
					http.MethodPost,
					"/api/subscribe",
					domain.SubscribeRequest{Email: "a@b.com", Repo: "golang/go"},
				)
				assert.Equal(t, tc.wantStatus, w.Code)
			},
		)
	}
}

func TestConfirm(t *testing.T) {
	t.Run(
		"200 valid token", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().ConfirmSubscription(gomock.Any(), "abc").Return(nil)

			w := doJSON(t, r, http.MethodGet, "/api/confirm/abc", nil)
			assert.Equal(t, http.StatusOK, w.Code)
		},
	)

	t.Run(
		"404 token not found", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().ConfirmSubscription(
				gomock.Any(),
				"missing",
			).Return(domain.ErrTokenNotFound)

			w := doJSON(t, r, http.MethodGet, "/api/confirm/missing", nil)
			assert.Equal(t, http.StatusNotFound, w.Code)
		},
	)
}

func TestUnsubscribe(t *testing.T) {
	t.Run(
		"200 valid token passes through", func(t *testing.T) {
			// Exact-match arg: path param forwarded verbatim.
			r, svc := newTestRouter(t)
			svc.EXPECT().Unsubscribe(gomock.Any(), "tok").Return(nil)

			w := doJSON(t, r, http.MethodGet, "/api/unsubscribe/tok", nil)
			assert.Equal(t, http.StatusOK, w.Code)
		},
	)

	t.Run(
		"404 token not found", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().Unsubscribe(gomock.Any(), gomock.Any()).Return(domain.ErrTokenNotFound)

			w := doJSON(t, r, http.MethodGet, "/api/unsubscribe/missing", nil)
			assert.Equal(t, http.StatusNotFound, w.Code)
		},
	)
}

func TestGetSubscriptions(t *testing.T) {
	t.Run(
		"200 returns list", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().GetSubscriptions(gomock.Any(), "a@b.com").Return(
				[]domain.SubscriptionResponse{
					{Email: "a@b.com", Repo: "golang/go", Confirmed: true},
				}, nil,
			)

			w := doJSON(t, r, http.MethodGet, "/api/subscriptions?email=a@b.com", nil)
			require.Equal(t, http.StatusOK, w.Code)

			var got []domain.SubscriptionResponse
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
			assert.Len(t, got, 1)
		},
	)

	t.Run(
		"200 empty list", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().GetSubscriptions(gomock.Any(), gomock.Any()).Return(nil, nil)

			w := doJSON(t, r, http.MethodGet, "/api/subscriptions?email=nobody@b.com", nil)
			assert.Equal(t, http.StatusOK, w.Code)
		},
	)

	t.Run(
		"400 invalid email", func(t *testing.T) {
			r, svc := newTestRouter(t)
			svc.EXPECT().GetSubscriptions(gomock.Any(), gomock.Any()).Return(
				nil,
				domain.ErrInvalidEmail,
			)

			w := doJSON(t, r, http.MethodGet, "/api/subscriptions?email=not-email", nil)
			assert.Equal(t, http.StatusBadRequest, w.Code)
		},
	)
}
