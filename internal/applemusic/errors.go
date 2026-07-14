package applemusic

import "fmt"

type ErrorKind string

const (
	ErrorHTTP       ErrorKind = "http_error"
	ErrorExtraction ErrorKind = "extraction_failed"
)

// Error is the internal typed failure returned by Apple Music extraction.
type Error struct {
	Kind ErrorKind
	Op   string
	Err  error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Err == nil {
		return fmt.Sprintf("apple music %s: %s", e.Op, e.Kind)
	}
	return fmt.Sprintf("apple music %s: %s: %v", e.Op, e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

func IsKind(err error, kind ErrorKind) bool {
	for err != nil {
		if typed, ok := err.(*Error); ok {
			if typed.Kind == kind {
				return true
			}
			err = typed.Err
			continue
		}
		break
	}
	return false
}
