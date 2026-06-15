package policy

type Error struct {
	Message string
}

func (e Error) Error() string {
	return e.Message
}

func newError(message string) error {
	return Error{Message: message}
}
