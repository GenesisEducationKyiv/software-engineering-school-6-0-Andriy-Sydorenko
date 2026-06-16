// Package bench measures the one real network hop in this app — the core→notifier
// SendEmail call — over two transports (gRPC/Protobuf vs HTTP/JSON), both behind
// the same constant-time bearer auth, so the comparison isolates transport cost.
package bench

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/notifier"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/notifierpb"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/observability/grpcmw"
)

const (
	benchToken  = "bench-token"
	warmupCalls = 5
)

// payloads sweeps html_body size — our SendEmail is single-recipient, so payload
// size (not recipient count) is what scales serialization/transport cost.
var payloads = []struct {
	name string
	size int
}{
	{"1KB", 1 << 10},
	{"10KB", 10 << 10},
	{"100KB", 100 << 10},
}

func makePayload(n int) string { return strings.Repeat("a", n) }

// noopMailer returns nil so benchmarks measure transport, not SMTP.
type noopMailer struct{}

func (noopMailer) Send(ctx context.Context, to, subject, htmlBody string) error { return nil }

type harness struct {
	grpcClient notifierpb.NotifierServiceClient
	httpClient *http.Client
	httpURL    string
}

func newHarness(tb testing.TB) *harness {
	tb.Helper()
	// Silence the notifier's per-call slog so we measure transport, not log I/O.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	h := &harness{}
	h.startGRPC(tb)
	h.startHTTP(tb)
	return h
}

func (h *harness) startGRPC(tb testing.TB) {
	tb.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("grpc listen: %v", err)
	}
	srv := grpc.NewServer(grpc.ChainUnaryInterceptor(grpcmw.AuthServerInterceptor(benchToken)))
	notifierpb.RegisterNotifierServiceServer(srv, notifier.NewGRPCServer(noopMailer{}))
	go func() { _ = srv.Serve(lis) }()
	tb.Cleanup(srv.Stop)

	conn, err := grpc.NewClient(lis.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithChainUnaryInterceptor(grpcmw.AuthClientInterceptor(benchToken)),
	)
	if err != nil {
		tb.Fatalf("grpc dial: %v", err)
	}
	tb.Cleanup(func() { _ = conn.Close() })
	h.grpcClient = notifierpb.NewNotifierServiceClient(conn)
}

type sendRequest struct {
	Recipient string `json:"recipient"`
	Subject   string `json:"subject"`
	HTML      string `json:"html"`
}

func (h *harness) startHTTP(tb testing.TB) {
	tb.Helper()
	want := []byte("Bearer " + benchToken)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /send", func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, want) != 1 {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req sendRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := (noopMailer{}).Send(r.Context(), req.Recipient, req.Subject, req.HTML); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		tb.Fatalf("http listen: %v", err)
	}
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(lis) }()
	tb.Cleanup(func() { _ = srv.Close() })

	h.httpURL = "http://" + lis.Addr().String() + "/send"
	h.httpClient = &http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
	}
}

func (h *harness) grpcSend(ctx context.Context, html string) error {
	_, err := h.grpcClient.SendEmail(ctx, &notifierpb.SendEmailRequest{
		RecipientEmail: "bench@example.com",
		Subject:        "bench",
		HtmlBody:       html,
	})
	return err
}

func (h *harness) httpSend(ctx context.Context, html string) error {
	body, err := json.Marshal(sendRequest{Recipient: "bench@example.com", Subject: "bench", HTML: html})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, h.httpURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+benchToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := h.httpClient.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.Copy(io.Discard, resp.Body) // drain so keep-alive can reuse the conn
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http status %d", resp.StatusCode)
	}
	return nil
}
