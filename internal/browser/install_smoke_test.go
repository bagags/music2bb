//go:build browser_install

package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

func TestPinnedArchiveInstallLaunchAndExtraction(t *testing.T) {
	archivePath := os.Getenv("MUSIC2BB_TEST_BROWSER_ARCHIVE")
	if archivePath == "" {
		t.Skip("MUSIC2BB_TEST_BROWSER_ARCHIVE is not set")
	}
	info, err := os.Stat(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadEmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	artifact, ok := manifest.Artifacts[currentPlatform()]
	if !ok {
		t.Skipf("no pinned artifact for %s", currentPlatform())
	}
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir:   t.TempDir(),
		Platform:   currentPlatform(),
		Manifest:   manifest,
		HTTPClient: &http.Client{Transport: archiveTransport{path: archivePath, size: info.Size()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	status, err := manager.Install(ctx, InstallOptions{Approved: true})
	if err != nil {
		t.Fatalf("install pinned revision %d: %v", artifact.Revision, err)
	}
	if !status.Installed || !status.Verified {
		t.Fatalf("installed browser is not verified: %#v", status)
	}

	page := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body><script>
          window.songData = [{songname: "Browser Smoke Song", singername: "Smoke Artist"}];
        </script></body></html>`)
	}))
	defer page.Close()
	songs, err := NewExtractor(manager).Extract(ctx, page.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Browser Smoke Song" || songs[0].Artist != "Smoke Artist" {
		t.Fatalf("unexpected extracted songs: %#v", songs)
	}
}

type archiveTransport struct {
	path string
	size int64
}

func (t archiveTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	file, err := os.Open(t.path)
	if err != nil {
		return nil, err
	}
	return &http.Response{
		StatusCode:    http.StatusOK,
		Status:        http.StatusText(http.StatusOK),
		Header:        make(http.Header),
		Body:          file,
		ContentLength: t.size,
		Request:       request,
	}, nil
}
