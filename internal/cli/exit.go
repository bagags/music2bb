package cli

import (
	"errors"

	"github.com/gguage/music-to-bb/internal/service"
)

const (
	ExitSuccess        = 0
	ExitInternal       = 1
	ExitInvalidInput   = 2
	ExitAuthentication = 3
	ExitExtraction     = 4
	ExitNoMatches      = 5
	ExitPartialWrite   = 6
	ExitWriteFailure   = 7
	ExitCancelled      = 130
)

func exitFor(err error) int {
	if err == nil {
		return ExitSuccess
	}
	var batch *service.BatchError
	if errors.As(err, &batch) && batch.Category == service.ErrorNetwork {
		return ExitExtraction
	}
	switch service.CategoryOf(err) {
	case service.ErrorInvalidInput:
		return ExitInvalidInput
	case service.ErrorAuthentication:
		return ExitAuthentication
	case service.ErrorExtraction, service.ErrorBrowser, service.ErrorNetwork:
		return ExitExtraction
	case service.ErrorNoMatches:
		return ExitNoMatches
	case service.ErrorPartialWrite:
		return ExitPartialWrite
	case service.ErrorWriteFailed:
		return ExitWriteFailure
	case service.ErrorCancelled:
		return ExitCancelled
	default:
		return ExitInternal
	}
}
