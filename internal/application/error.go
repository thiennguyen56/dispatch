package application

import "errors"

var ErrLeaseLost = errors.New("delivery lease is no longer owned")
var ErrIdempotencyKeyExists = errors.New("idempotency key already exists")

type ErrNoJob struct {
}

func (e ErrNoJob) Error() string {
	return "no job found"
}
