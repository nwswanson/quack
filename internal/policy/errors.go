package policy

type ForbiddenError struct {
	Reason string
}

func (e ForbiddenError) Error() string {
	return e.Reason
}
