package application

import "errors"

var ErrLeaseLost = errors.New("delivery lease is no longer owned")

type ErrNoJob struct {
}

func (e ErrNoJob) Error() string {
	return "no job found"
}
