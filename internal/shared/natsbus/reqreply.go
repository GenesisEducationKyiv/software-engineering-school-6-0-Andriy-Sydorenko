package natsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/nats-io/nats.go"
)

// internalErrReply is sent when a handler itself fails, so a requester is never left hanging.
var internalErrReply = []byte(`{"ok":false,"code":"internal"}`)

// RequestJSON marshals req, issues a Core NATS request (deadline from ctx), and unmarshals the reply into out.
func RequestJSON(ctx context.Context, nc *nats.Conn, subject string, req, out any) error {
	data, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal %s request: %w", subject, err)
	}
	msg, err := nc.RequestWithContext(ctx, subject, data)
	if err != nil {
		return fmt.Errorf("request %s: %w", subject, err)
	}
	if err := json.Unmarshal(msg.Data, out); err != nil {
		return fmt.Errorf("unmarshal %s reply: %w", subject, err)
	}
	return nil
}

// HandlerFunc decodes raw request bytes and returns the reply payload to marshal.
type HandlerFunc func(ctx context.Context, data []byte) (any, error)

// RespondJSON queue-subscribes and replies with the handler's marshalled result.
// On handler error it logs and sends a generic internal reply.
func RespondJSON(nc *nats.Conn, subject, queue string, h HandlerFunc) (*nats.Subscription, error) {
	return nc.QueueSubscribe(subject, queue, func(m *nats.Msg) {
		resp, err := h(context.Background(), m.Data)
		if err != nil {
			slog.Error("reqreply handler failed", "subject", subject, "err", err)
			_ = m.Respond(internalErrReply)
			return
		}
		out, err := json.Marshal(resp)
		if err != nil {
			slog.Error("reqreply marshal failed", "subject", subject, "err", err)
			_ = m.Respond(internalErrReply)
			return
		}
		_ = m.Respond(out)
	})
}
