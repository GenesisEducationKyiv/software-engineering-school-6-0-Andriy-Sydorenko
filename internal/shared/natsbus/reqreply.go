package natsbus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// internalErrReply is sent when a handler itself fails, so a requester is never left hanging.
var internalErrReply = []byte(`{"ok":false,"code":"internal"}`)

const handlerTimeout = 15 * time.Second

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

func RespondJSON(nc *nats.Conn, subject, queue string, h HandlerFunc) (*nats.Subscription, error) {
	return nc.QueueSubscribe(
		subject, queue, func(m *nats.Msg) {
			ctx, cancel := context.WithTimeout(context.Background(), handlerTimeout)
			defer cancel()
			resp, err := h(ctx, m.Data)
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
		},
	)
}
