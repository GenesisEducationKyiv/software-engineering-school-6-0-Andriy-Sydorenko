package notifierclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPSender_SendEmail(t *testing.T) {
	t.Run("success posts json with bearer and returns nil", func(t *testing.T) {
		var gotAuth, gotBody string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotAuth = r.Header.Get("Authorization")
			b, _ := io.ReadAll(r.Body)
			gotBody = string(b)
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		s := NewHTTPSender(srv.URL, "tok", time.Second)
		if err := s.SendEmail(context.Background(), "u@e.com", "subj", "<p>b</p>"); err != nil {
			t.Fatalf("SendEmail() error = %v", err)
		}
		if gotAuth != "Bearer tok" {
			t.Fatalf("auth header = %q, want %q", gotAuth, "Bearer tok")
		}
		var req map[string]string
		if err := json.Unmarshal([]byte(gotBody), &req); err != nil {
			t.Fatalf("body not json: %v (%s)", err, gotBody)
		}
		if req["recipient_email"] != "u@e.com" || req["subject"] != "subj" || req["html_body"] != "<p>b</p>" {
			t.Fatalf("unexpected body: %s", gotBody)
		}
	})

	t.Run("non-2xx returns error with status and message", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"recipient_email, subject and html_body are required"}`))
		}))
		defer srv.Close()

		s := NewHTTPSender(srv.URL, "tok", time.Second)
		err := s.SendEmail(context.Background(), "u@e.com", "subj", "<p>b</p>")
		if err == nil {
			t.Fatal("expected error for 400 response, got nil")
		}
		if !strings.Contains(err.Error(), "400") {
			t.Fatalf("error should mention status 400, got: %v", err)
		}
	})

	t.Run("empty token omits Authorization header", func(t *testing.T) {
		var hadAuth bool
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, hadAuth = r.Header["Authorization"]
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		s := NewHTTPSender(srv.URL, "", time.Second)
		if err := s.SendEmail(context.Background(), "u@e.com", "subj", "<p>b</p>"); err != nil {
			t.Fatalf("SendEmail() error = %v", err)
		}
		if hadAuth {
			t.Fatal("Authorization header should be absent when token is empty")
		}
	})

	t.Run("cancelled context returns error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNoContent)
		}))
		defer srv.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := NewHTTPSender(srv.URL, "", time.Second).SendEmail(ctx, "u@e.com", "subj", "<p>b</p>"); err == nil {
			t.Fatal("expected error for cancelled context")
		}
	})
}
