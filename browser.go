package music2bb

import (
	"context"
	"errors"

	"github.com/bagags/music2bb-go/internal/browser"
)

type BrowserStatus struct {
	CacheDir          string
	Platform          string
	Version           string
	Revision          int
	ChromiumCommit    string
	SourceURL         string
	LicenseURL        string
	ArchiveURL        string
	ArchiveGeneration string
	PublishedAt       string
	ApproxBytes       int64
	ExecutablePath    string
	ExpectedSHA256    string
	ArchiveSHA256     string
	ExecutableSHA256  string
	Present           bool
	Installed         bool
	Verified          bool
	// Deprecated: Chromium is never embedded in music2bb binaries.
	Bundled bool
	Source  BrowserSource
}

// BrowserSource identifies how the selected browser is provisioned.
type BrowserSource string

const (
	BrowserSourceSystem  BrowserSource = "system"
	BrowserSourceManaged BrowserSource = "managed"
)

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
		CacheDir: status.CacheDir, Platform: status.Platform, Version: status.Version,
		Revision: status.Revision, ChromiumCommit: status.ChromiumCommit,
		SourceURL: status.SourceURL, LicenseURL: status.LicenseURL, ArchiveURL: status.ArchiveURL,
		ArchiveGeneration: status.ArchiveGeneration, PublishedAt: status.PublishedAt,
		ApproxBytes:    status.ApproxBytes,
		ExecutablePath: status.ExecutablePath, ExpectedSHA256: status.ExpectedSHA256,
		ArchiveSHA256: status.ArchiveSHA256, ExecutableSHA256: status.ExecutableSHA256,
		Present: status.Present, Installed: status.Installed, Verified: status.Verified, Bundled: false,
		Source: BrowserSource(status.Source),
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
