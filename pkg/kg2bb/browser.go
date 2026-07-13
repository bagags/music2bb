package kg2bb

import (
	"context"
	"errors"

	"github.com/gguage/music-to-bb/internal/browser"
)

type BrowserStatus struct {
	CacheDir         string
	Platform         string
	Revision         int
	ExecutablePath   string
	ExpectedSHA256   string
	ArchiveSHA256    string
	ExecutableSHA256 string
	Present          bool
	Installed        bool
	Verified         bool
}

type BrowserInstallOptions struct {
	Approved       bool
	NonInteractive bool
}

type BrowserManager struct{ manager *browser.Manager }

func (m *BrowserManager) Status(ctx context.Context) (BrowserStatus, error) {
	status, err := m.manager.Status(ctx)
	return browserStatusFromInternal(status), wrapBrowserError("browser status", err)
}

func (m *BrowserManager) Install(ctx context.Context, approved bool) (BrowserStatus, error) {
	return m.InstallWithOptions(ctx, BrowserInstallOptions{Approved: approved, NonInteractive: !approved})
}

func (m *BrowserManager) InstallWithOptions(ctx context.Context, options BrowserInstallOptions) (BrowserStatus, error) {
	status, err := m.manager.Install(ctx, browser.InstallOptions{Approved: options.Approved, NonInteractive: options.NonInteractive})
	return browserStatusFromInternal(status), wrapBrowserError("browser install", err)
}

func (m *BrowserManager) Clear(ctx context.Context) error {
	return wrapBrowserError("browser clear", m.manager.Clear(ctx))
}

func browserStatusFromInternal(status browser.Status) BrowserStatus {
	return BrowserStatus{
		CacheDir: status.CacheDir, Platform: status.Platform, Revision: status.Revision,
		ExecutablePath: status.ExecutablePath, ExpectedSHA256: status.ExpectedSHA256,
		ArchiveSHA256: status.ArchiveSHA256, ExecutableSHA256: status.ExecutableSHA256,
		Present: status.Present, Installed: status.Installed, Verified: status.Verified,
	}
}

func wrapBrowserError(operation string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &Error{Category: ErrorCancelled, Operation: operation, Err: err}
	}
	return &Error{Category: ErrorBrowser, Operation: operation, Err: err}
}
