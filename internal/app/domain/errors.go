package domain

import "errors"

var (
	// ErrTokenNotFound is returned by the confirm/unsubscribe NATS handlers and
	// mapped to saga.CodeTokenNotFound — it never travels over this service's HTTP.
	ErrTokenNotFound = errors.New("token not found")
	ErrInvalidEmail  = errors.New("invalid email format")
)
