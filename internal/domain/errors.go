package domain

import "errors"

var (
	ErrInvalidRequest        = errors.New("invalid request")
	ErrAccountNotFound       = errors.New("account not found")
	ErrAuthorizationNotFound = errors.New("authorization not found")
	ErrIdempotencyConflict   = errors.New("idempotency key reused with a different payload")
)
