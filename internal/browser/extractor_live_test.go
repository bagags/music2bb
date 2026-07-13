//go:build live

package browser

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"
)

// This opt-in smoke test uses an existing verified browser by default. Setting
// MUSIC2BB_TEST_BROWSER_ARCHIVE installs the pinned archive into an isolated cache
// first, which keeps release validation independent of workstation state.
func TestLiveKugouExtraction(t *testing.T) {
	rawURL := os.Getenv("MUSIC2BB_TEST_KUGOU_URL")
	if rawURL == "" {
		t.Skip("MUSIC2BB_TEST_KUGOU_URL is not set")
	}
	manager := liveTestManager(t)
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !status.Verified {
		t.Skip("verified browser is not installed")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	songs, err := NewExtractor(manager).Extract(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) == 0 {
		t.Fatal("live browser extraction returned no songs")
	}
}

func liveTestManager(t *testing.T) *Manager {
	t.Helper()
	archivePath := os.Getenv("MUSIC2BB_TEST_BROWSER_ARCHIVE")
	if archivePath == "" {
		manager, err := NewManager("")
		if err != nil {
			t.Fatal(err)
		}
		return manager
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadEmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(),
		Platform: currentPlatform(),
		Manifest: manifest,
		HTTPClient: &http.Client{Transport: liveArchiveTransport{
			path: archivePath,
			size: info.Size(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, err := manager.Install(ctx, InstallOptions{Approved: true}); err != nil {
		t.Fatalf("install pinned browser: %v", err)
	}
	return manager
}

type liveArchiveTransport struct {
	path string
	size int64
}

func (transport liveArchiveTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	file, err := os.Open(transport.path)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        http.StatusText(http.StatusOK),
		Header:        make(http.Header),
		Body:          file,
		ContentLength: transport.size,
		Request:       request,
	}, nil
}
