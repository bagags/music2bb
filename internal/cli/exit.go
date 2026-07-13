package cli

import (
	"errors"

	music2bb "github.com/gguage/music-to-bb"
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
	var batch *music2bb.BatchError
	if errors.As(err, &batch) && batch.Category == music2bb.ErrorNetwork {
		return ExitExtraction
	}
	switch music2bb.CategoryOf(err) {
	case music2bb.ErrorInvalidInput:
		return ExitInvalidInput
	case music2bb.ErrorAuthentication:
		return ExitAuthentication
	case music2bb.ErrorExtraction, music2bb.ErrorBrowser, music2bb.ErrorNetwork:
		return ExitExtraction
	case music2bb.ErrorNoMatches:
		return ExitNoMatches
	case music2bb.ErrorPartialWrite:
		return ExitPartialWrite
	case music2bb.ErrorWriteFailed:
		return ExitWriteFailure
	case music2bb.ErrorCancelled:
		return ExitCancelled
	default:
		return ExitInternal
	}
}
