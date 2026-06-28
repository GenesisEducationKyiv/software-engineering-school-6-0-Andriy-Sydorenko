package domain

// State is a saga's position in its lifecycle. Terminal states (DONE, ABORTED,
// COMPENSATED) are excluded from the recovery sweep.
type State string

const (
	StateStarted      State = "STARTED"
	StateCatalogOK    State = "CATALOG_OK"
	StateSubPending   State = "SUBSCRIPTION_PENDING"
	StateCommitted    State = "COMMITTED"
	StateDone         State = "DONE"
	StateAborted      State = "ABORTED"
	StateCompensating State = "COMPENSATING"
	StateCompensated  State = "COMPENSATED"
)

// SagaPayload is everything a saga needs to resume after a crash.
type SagaPayload struct {
	Email        string `json:"email"`
	Repo         string `json:"repo"`
	ConfirmToken string `json:"confirm_token"`
	UnsubToken   string `json:"unsub_token"`
}

type SagaRecord struct {
	SagaID         string
	State          State
	SubscriptionID string
	Payload        SagaPayload
}
