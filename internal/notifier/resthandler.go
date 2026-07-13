package notifier

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"
)

const maxRESTBodyBytes = 1 << 20 // 1 MiB

type restSendEmailRequest struct {
	RecipientEmail string `json:"recipient_email"`
	Subject        string `json:"subject"`
	HTMLBody       string `json:"html_body"`
}

func SendEmailRESTHandler(mailer Mailer, token string) http.HandlerFunc {
	want := []byte(token)
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", http.MethodPost)
			writeJSONError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if token != "" {
			got := []byte(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
			if subtle.ConstantTimeCompare(got, want) != 1 {
				writeJSONError(w, http.StatusUnauthorized, "invalid or missing token")
				return
			}
		}

		var req restSendEmailRequest
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRESTBodyBytes))
		if err := dec.Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid JSON body")
			return
		}
		if req.RecipientEmail == "" || req.Subject == "" || req.HTMLBody == "" {
			writeJSONError(
				w,
				http.StatusBadRequest,
				"recipient_email, subject and html_body are required",
			)
			return
		}

		if err := mailer.Send(
			r.Context(),
			req.RecipientEmail,
			req.Subject,
			req.HTMLBody,
		); err != nil {
			slog.ErrorContext(
				r.Context(), "send email failed (rest)",
				"recipient", maskEmail(req.RecipientEmail), "err", err,
			)
			if isTransientSMTP(err) {
				writeJSONError(w, http.StatusServiceUnavailable, "mailer temporarily unavailable")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, "failed to send email")
			return
		}

		slog.InfoContext(
			r.Context(),
			"email sent (rest)",
			"recipient",
			maskEmail(req.RecipientEmail),
		)
		w.WriteHeader(http.StatusNoContent)
	}
}

func writeJSONError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
