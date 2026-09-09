package application

type ErrNoJob struct {
}

func (e ErrNoJob) Error() string {
	return "no job found"
}
