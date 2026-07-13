package service

import (
	"errors"
	"fmt"
)

type ErrorCategory string

const (
	ErrorInvalidInput   ErrorCategory = "invalid_input"
	ErrorAuthentication ErrorCategory = "authentication"
	ErrorExtraction     ErrorCategory = "extraction"
	ErrorNoMatches      ErrorCategory = "no_matches"
	ErrorPartialWrite   ErrorCategory = "partial_write"
	ErrorWriteFailed    ErrorCategory = "write_failed"
	ErrorBrowser        ErrorCategory = "browser_required"
	ErrorNetwork        ErrorCategory = "network"
	ErrorCancelled      ErrorCategory = "cancelled"
	ErrorInternal       ErrorCategory = "internal"
)

type OperationError struct {
	Category  ErrorCategory
	Operation string
	Message   string
	Err       error
}

func (e *OperationError) Error() string {
	if e == nil {
		return "<nil>"
	}
	message := e.Message
	if message == "" && e.Err != nil {
		message = e.Err.Error()
	}
	if e.Operation == "" {
		return fmt.Sprintf("%s: %s", e.Category, message)
	}
	return fmt.Sprintf("%s: %s: %s", e.Category, e.Operation, message)
}

func (e *OperationError) Unwrap() error { return e.Err }

func CategoryOf(err error) ErrorCategory {
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		return operationErr.Category
	}
	return ErrorInternal
}

type ItemFailure struct {
	Index     int
	Operation string
	Item      string
	Reason    string
}

type BatchError struct {
	Category ErrorCategory
	Failures []ItemFailure
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("%s: %d item(s) failed", e.Category, len(e.Failures))
}
