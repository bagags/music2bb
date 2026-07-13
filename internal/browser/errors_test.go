package browser

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsKindTraversesWrappedAndJoinedErrors(t *testing.T) {
	err := fmt.Errorf("outer: %w", errors.Join(
		&Error{Kind: ErrorLaunch, Op: "launch", Err: errors.New("launch")},
		&Error{Kind: ErrorNotInstalled, Op: "executable", Err: errors.New("missing")},
	))
	if !IsKind(err, ErrorLaunch) || !IsKind(err, ErrorNotInstalled) {
		t.Fatalf("IsKind did not traverse wrapped joined errors: %v", err)
	}
	if IsKind(err, ErrorChecksumMismatch) {
		t.Fatalf("IsKind reported an absent kind: %v", err)
	}
}
