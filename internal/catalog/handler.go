package catalog

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/natsbus"
	"github.com/Andriy-Sydorenko/repo-release-notifier/internal/shared/saga"
)

// RepoValidator checks a repo exists on GitHub (the moved GitHub client satisfies this).
type RepoValidator interface {
	ValidateRepo(ctx context.Context, owner, name string) error
}

// Store is the registration persistence the saga handlers need.
type Store interface {
	Register(ctx context.Context, subscriptionID, repo string) error
	Release(ctx context.Context, subscriptionID string) error
}

// Handler serves the catalog.register / catalog.release saga commands.
type Handler struct {
	store     Store
	validator RepoValidator
}

func NewHandler(store Store, validator RepoValidator) *Handler {
	return &Handler{store: store, validator: validator}
}

// Register validates the repo on GitHub then registers it. Forward step of the
// subscribe saga; fail-fast on a bad repo so nothing downstream needs compensating.
func (h *Handler) Register(ctx context.Context, data []byte) (any, error) {
	var cmd saga.RegisterRepoCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, fmt.Errorf("unmarshal register command: %w", err)
	}
	owner, name, ok := splitRepo(cmd.Repo)
	if !ok {
		return saga.Reply{OK: false, Code: saga.CodeRepoNotFound}, nil
	}
	if err := h.validator.ValidateRepo(ctx, owner, name); err != nil {
		if errors.Is(err, ErrRateLimited) {
			return saga.Reply{OK: false, Code: saga.CodeRateLimited}, nil
		}
		return saga.Reply{OK: false, Code: saga.CodeRepoNotFound}, nil
	}
	if err := h.store.Register(ctx, cmd.SubscriptionID, cmd.Repo); err != nil {
		return nil, err
	}
	return saga.Reply{OK: true}, nil
}

// Release is the compensation for Register: drop the registration. Idempotent.
func (h *Handler) Release(ctx context.Context, data []byte) (any, error) {
	var cmd saga.ReleaseRepoCommand
	if err := json.Unmarshal(data, &cmd); err != nil {
		return nil, fmt.Errorf("unmarshal release command: %w", err)
	}
	if err := h.store.Release(ctx, cmd.SubscriptionID); err != nil {
		return nil, err
	}
	return saga.Reply{OK: true}, nil
}

// OnSubscriptionRemoved is the events.subscription.removed handler: drop the
// registration so the scanner stops polling a repo the unsubscriber left.
// Idempotent; a malformed payload is permanent (drop, don't retry).
func (h *Handler) OnSubscriptionRemoved(ctx context.Context, _ string, data []byte) error {
	var evt saga.SubscriptionRemovedEvent
	if err := json.Unmarshal(data, &evt); err != nil {
		return fmt.Errorf("%w: unmarshal subscription.removed: %w", natsbus.ErrPermanent, err)
	}
	return h.store.Release(ctx, evt.SubscriptionID)
}

func splitRepo(s string) (owner, name string, ok bool) {
	parts := strings.SplitN(s, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}
