package browser

import (
	"errors"
	"fmt"
)

// ErrorKind is a stable, machine-readable browser failure category.
type ErrorKind string

const (
	ErrorUnsupportedPlatform ErrorKind = "unsupported_platform"
	ErrorInvalidExecutable   ErrorKind = "invalid_executable"
	ErrorNotInstalled        ErrorKind = "not_installed"
	ErrorApprovalRequired    ErrorKind = "approval_required"
	ErrorNonInteractive      ErrorKind = "non_interactive_install_denied"
	ErrorUnverifiedArtifact  ErrorKind = "unverified_artifact"
	ErrorChecksumMismatch    ErrorKind = "checksum_mismatch"
	ErrorDownload            ErrorKind = "download_failed"
	ErrorInstall             ErrorKind = "install_failed"
	ErrorClear               ErrorKind = "clear_failed"
	ErrorLaunch              ErrorKind = "launch_failed"
	ErrorExtraction          ErrorKind = "extraction_failed"
)

// Error adds a stable category and operation to an underlying failure.
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
		return fmt.Sprintf("browser %s: %s", e.Op, e.Kind)
	}
	return fmt.Sprintf("browser %s: %s: %v", e.Op, e.Kind, e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// IsKind reports whether err contains a browser Error with the requested kind.
func IsKind(err error, kind ErrorKind) bool {
	if err == nil {
		return false
	}
	if typed, ok := err.(*Error); ok {
		return typed.Kind == kind || IsKind(typed.Err, kind)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		for _, nested := range joined.Unwrap() {
			if IsKind(nested, kind) {
				return true
			}
		}
		return false
	}
	return IsKind(errors.Unwrap(err), kind)
}
