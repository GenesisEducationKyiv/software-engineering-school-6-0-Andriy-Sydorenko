package notifier

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSendEmailRESTHandler(t *testing.T) {
	const token = "secret-token"
	validBody := `{"recipient_email":"user@example.com","subject":"hi","html_body":"<p>hi</p>"}`

	tests := []struct {
		name       string
		body       string
		authHeader string
		mailerErr  error
		wantStatus int
		wantCalls  int
	}{
		{"ok", validBody, "Bearer " + token, nil, http.StatusNoContent, 1},
		{"missing field", `{"recipient_email":"u@e.com","subject":"hi"}`, "Bearer " + token, nil, http.StatusBadRequest, 0},
		{"bad json", `{not-json`, "Bearer " + token, nil, http.StatusBadRequest, 0},
		{"missing token", validBody, "", nil, http.StatusUnauthorized, 0},
		{"wrong token", validBody, "Bearer nope", nil, http.StatusUnauthorized, 0},
		{"transient smtp", validBody, "Bearer " + token, timeoutErr{}, http.StatusServiceUnavailable, 1},
		{"permanent smtp", validBody, "Bearer " + token, errors.New("550 rejected"), http.StatusInternalServerError, 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mailer := &stubMailer{err: tt.mailerErr}
			h := SendEmailRESTHandler(mailer, token)

			req := httptest.NewRequest(http.MethodPost, "/v1/send-email", strings.NewReader(tt.body))
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if mailer.calls != tt.wantCalls {
				t.Fatalf("mailer calls = %d, want %d", mailer.calls, tt.wantCalls)
			}
		})
	}

	t.Run("no-auth configured returns 204 without a token", func(t *testing.T) {
		mailer := &stubMailer{}
		h := SendEmailRESTHandler(mailer, "") // empty token disables auth
		req := httptest.NewRequest(http.MethodPost, "/v1/send-email", strings.NewReader(validBody))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusNoContent {
			t.Fatalf("status = %d, want 204 (body=%s)", rec.Code, rec.Body.String())
		}
		if mailer.calls != 1 {
			t.Fatalf("mailer calls = %d, want 1", mailer.calls)
		}
	})

	t.Run("non-POST returns 405 with Allow header", func(t *testing.T) {
		h := SendEmailRESTHandler(&stubMailer{}, "")
		req := httptest.NewRequest(http.MethodGet, "/v1/send-email", http.NoBody)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("status = %d, want 405", rec.Code)
		}
		if got := rec.Header().Get("Allow"); got != http.MethodPost {
			t.Fatalf("Allow = %q, want POST", got)
		}
	})
}
