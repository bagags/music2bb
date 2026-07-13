package music2bb

import (
	"context"
	"errors"
	"fmt"

	"github.com/gguage/music-to-bb/internal/service"
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

type Error struct {
	Category  ErrorCategory
	Operation string
	Message   string
	Err       error
}

func (e *Error) Error() string {
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

func (e *Error) Unwrap() error { return e.Err }

type BatchError struct {
	Category ErrorCategory
	Failures []ItemFailure
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("%s: %d item(s) failed", e.Category, len(e.Failures))
}

func CategoryOf(err error) ErrorCategory {
	var public *Error
	if errors.As(err, &public) {
		return public.Category
	}
	var batch *BatchError
	if errors.As(err, &batch) {
		return batch.Category
	}
	return ErrorInternal
}

func wrapError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Category: ErrorCancelled, Err: err}
	}
	var operation *service.OperationError
	if errors.As(err, &operation) {
		return &Error{
			Category:  categoryFromService(operation.Category),
			Operation: operation.Operation,
			Message:   operation.Message,
			Err:       operation.Err,
		}
	}
	var batch *service.BatchError
	if errors.As(err, &batch) {
		failures := make([]ItemFailure, len(batch.Failures))
		for index, failure := range batch.Failures {
			failures[index] = ItemFailure{Index: failure.Index, Operation: failure.Operation, Item: failure.Item, Reason: failure.Reason}
		}
		return &BatchError{Category: categoryFromService(batch.Category), Failures: failures}
	}
	return &Error{Category: ErrorInternal, Err: err}
}

func categoryFromService(category service.ErrorCategory) ErrorCategory {
	return ErrorCategory(category)
}
