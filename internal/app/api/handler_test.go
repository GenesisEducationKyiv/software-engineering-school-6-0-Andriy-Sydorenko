package api

import (
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
	r.GET("/api/subscriptions", h.GetSubscriptions)
	return r, svc
}

func doGET(t *testing.T, r *gin.Engine, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
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

			w := doGET(t, r, "/api/subscriptions?email=a@b.com")
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

			w := doGET(t, r, "/api/subscriptions?email=nobody@b.com")
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

			w := doGET(t, r, "/api/subscriptions?email=not-email")
			assert.Equal(t, http.StatusBadRequest, w.Code)
		},
	)
}
