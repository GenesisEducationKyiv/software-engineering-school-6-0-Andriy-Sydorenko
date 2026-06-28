package notifierclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPSender is the REST/JSON alternative to the gRPC *Client; both satisfy service.EmailSender.
type HTTPSender struct {
	url    string
	token  string
	client *http.Client
}

func NewHTTPSender(baseURL, token string) *HTTPSender {
	return &HTTPSender{
		url:    strings.TrimRight(baseURL, "/") + "/v1/send-email",
		token:  token,
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (s *HTTPSender) SendEmail(ctx context.Context, recipientEmail, subject, htmlBody string) error {
	payload, err := json.Marshal(restSendEmailBody{
		RecipientEmail: recipientEmail,
		Subject:        subject,
		HTMLBody:       htmlBody,
	})
	if err != nil {
		return fmt.Errorf("marshal send-email request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("build send-email request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.token != "" {
		req.Header.Set("Authorization", "Bearer "+s.token)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("send-email request: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusNoContent {
		return nil
	}
	return fmt.Errorf("notifier rest send-email: status %d: %s", resp.StatusCode, readErrorMessage(resp.Body))
}

type restSendEmailBody struct {
	RecipientEmail string `json:"recipient_email"`
	Subject        string `json:"subject"`
	HTMLBody       string `json:"html_body"`
}

func readErrorMessage(body io.Reader) string {
	data, _ := io.ReadAll(io.LimitReader(body, 4096))
	var e struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(data, &e); err == nil && e.Error != "" {
		return e.Error
	}
	if msg := strings.TrimSpace(string(data)); msg != "" {
		return msg
	}
	return "unknown error"
}
