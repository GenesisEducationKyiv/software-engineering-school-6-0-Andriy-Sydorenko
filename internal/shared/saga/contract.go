package saga

// Core NATS request-reply subjects (commands + compensations).
const (
	SubjCatalogRegister    = "saga.catalog.register"
	SubjCatalogRelease     = "saga.catalog.release"
	SubjSubscriptionCreate = "saga.subscription.create"
	SubjSubscriptionCancel = "saga.subscription.cancel"

	QueueCatalog      = "catalog"
	QueueSubscription = "subscription"
)

// JetStream event stream + subjects (durable, at-least-once).
const (
	EventsStreamName          = "EVENTS"
	EventsStreamSubject       = "events.>"
	SubjReleaseDetected       = "events.release.detected"
	SubjSubscriptionRemoved   = "events.subscription.removed"
	SubjConfirmationRequested = "events.confirmation.requested"

	DurableReleaseConsumer      = "subscription-release"
	DurableRemovedConsumer      = "catalog-removed"
	DurableConfirmationConsumer = "subscription-confirmation"
)

type RegisterRepoCommand struct {
	SagaID         string `json:"saga_id"`
	SubscriptionID string `json:"subscription_id"`
	Repo           string `json:"repo"`
}

type ReleaseRepoCommand struct {
	SubscriptionID string `json:"subscription_id"`
}

type CreateSubscriptionCommand struct {
	SagaID         string `json:"saga_id"`
	SubscriptionID string `json:"subscription_id"`
	Email          string `json:"email"`
	Repo           string `json:"repo"`
	ConfirmToken   string `json:"confirm_token"`
	UnsubToken     string `json:"unsub_token"`
}

type CancelSubscriptionCommand struct {
	SubscriptionID string `json:"subscription_id"`
}

// Reply is the uniform request-reply response. Code is a machine-readable
// domain code ("" on success); participants never put raw error strings on the wire.
type Reply struct {
	OK   bool   `json:"ok"`
	Code string `json:"code,omitempty"`
}

const (
	CodeRepoNotFound      = "repo_not_found"
	CodeRateLimited       = "rate_limited"
	CodeAlreadySubscribed = "already_subscribed"
	CodeInternal          = "internal"
)

type ReleaseDetectedEvent struct {
	Repo    string `json:"repo"`
	Tag     string `json:"tag"`
	EventID string `json:"event_id"`
}

type SubscriptionRemovedEvent struct {
	SubscriptionID string `json:"subscription_id"`
	Repo           string `json:"repo"`
}

// ConfirmationRequestedEvent is the saga's post-commit terminal step: the
// orchestrator emits it once the subscription is committed, and the subscription
// service renders + publishes the confirmation email.
type ConfirmationRequestedEvent struct {
	Email        string `json:"email"`
	Repo         string `json:"repo"`
	ConfirmToken string `json:"confirm_token"`
	UnsubToken   string `json:"unsub_token"`
}
