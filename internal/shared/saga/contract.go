package saga

// Core NATS request-reply subjects (commands + compensations).
const (
	SubjCatalogRegister    = "saga.catalog.register"
	SubjCatalogRelease     = "saga.catalog.release"
	SubjSubscriptionCreate = "saga.subscription.create"

	// Direct subscription commands the orchestrator's confirm/unsubscribe pages
	// issue (request-reply; not saga steps — nothing to compensate).
	SubjSubscriptionConfirm     = "subscription.confirm"
	SubjSubscriptionUnsubscribe = "subscription.unsubscribe"

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

type ConfirmCommand struct {
	Token string `json:"token"`
}

type UnsubscribeCommand struct {
	Token string `json:"token"`
}

type Reply struct {
	OK   bool   `json:"ok"`
	Code string `json:"code,omitempty"`
}

const (
	CodeRepoNotFound      = "repo_not_found"
	CodeRateLimited       = "rate_limited"
	CodeAlreadySubscribed = "already_subscribed"
	CodeTokenNotFound     = "token_not_found"
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

type ConfirmationRequestedEvent struct {
	Email        string `json:"email"`
	Repo         string `json:"repo"`
	ConfirmToken string `json:"confirm_token"`
	UnsubToken   string `json:"unsub_token"`
}
