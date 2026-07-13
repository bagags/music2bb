package cli

import (
	"errors"

	kg2bb "github.com/gguage/music-to-bb"
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
	var batch *kg2bb.BatchError
	if errors.As(err, &batch) && batch.Category == kg2bb.ErrorNetwork {
		return ExitExtraction
	}
	switch kg2bb.CategoryOf(err) {
	case kg2bb.ErrorInvalidInput:
		return ExitInvalidInput
	case kg2bb.ErrorAuthentication:
		return ExitAuthentication
	case kg2bb.ErrorExtraction, kg2bb.ErrorBrowser, kg2bb.ErrorNetwork:
		return ExitExtraction
	case kg2bb.ErrorNoMatches:
		return ExitNoMatches
	case kg2bb.ErrorPartialWrite:
		return ExitPartialWrite
	case kg2bb.ErrorWriteFailed:
		return ExitWriteFailure
	case kg2bb.ErrorCancelled:
		return ExitCancelled
	default:
		return ExitInternal
	}
}
