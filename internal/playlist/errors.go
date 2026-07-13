package playlist

import (
	"errors"
	"fmt"
)

// CapabilityCategory identifies the typed optimization stage that failed.
type CapabilityCategory string

const (
	CapabilityPlaylistExtraction CapabilityCategory = "playlist_extraction"
	CapabilityBrowserExtraction  CapabilityCategory = "browser_extraction"
)

// AttemptError retains provider and optimization diagnostics without adding a
// public error category.
type AttemptError struct {
	ProviderID   ProviderID
	Category     CapabilityCategory
	Optimization string
	Err          error
}

func (e *AttemptError) Error() string {
	if e == nil {
		return "<nil>"
	}
	return fmt.Sprintf("provider %q %s attempt %q: %v", e.ProviderID, e.Category, e.Optimization, e.Err)
}

func (e *AttemptError) Unwrap() error { return e.Err }

// ErrorKind is the coordinator's internal error classification.
type ErrorKind string

const (
	ErrorInvalidInput ErrorKind = "invalid_input"
	ErrorExtraction   ErrorKind = "extraction"
	ErrorBrowser      ErrorKind = "browser_required"
	ErrorInternal     ErrorKind = "internal"
)

// Error classifies coordinator failures for the wiring boundary.
type Error struct {
	Kind    ErrorKind
	Op      string
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := e.Message
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if e.Op == "" {
		return fmt.Sprintf("playlist %s: %s", e.Kind, message)
	}
	return fmt.Sprintf("playlist %s: %s: %s", e.Op, e.Kind, message)
}

func (e *Error) Unwrap() error { return e.Err }

// IsKind reports whether err contains a playlist Error of the requested kind.
func IsKind(err error, kind ErrorKind) bool {
	var typed *Error
	return errors.As(err, &typed) && typed.Kind == kind
}
