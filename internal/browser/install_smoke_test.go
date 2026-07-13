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

	"github.com/gguage/music-to-bb/internal/playlist"
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
		fmt.Fprint(w, `<!doctype html><html><body>
        <div class="song-item">
          <div class="song-name">DOM Smoke Song</div>
          <div class="artist">DOM Artist</div>
        </div>
        <script>
          window.songData = [{
            filename: "Smoke Artist - Browser Smoke Song",
            singerinfo: [],
            albuminfo: {name: "Smoke Album"},
            duration: 185000,
            hash: "smoke-hash",
            vip: false
          }];
        </script></body></html>`)
	}))
	defer page.Close()
	source, err := playlist.ParseSource(page.URL)
	if err != nil {
		t.Fatal(err)
	}
	result, err := NewExtractor(manager).ExtractPlaylist(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Tracks) != 2 {
		t.Fatalf("unexpected extracted candidates: %#v", result.Tracks)
	}
	track := result.Tracks[0]
	if track.Fields["filename"] != "Smoke Artist - Browser Smoke Song" || track.Fields["vip"] != "false" {
		t.Fatalf("source fields were not preserved: %#v", track.Fields)
	}
	if track.ArtistNames == nil || len(track.ArtistNames) != 0 {
		t.Fatalf("empty singerinfo presence was not preserved: %#v", track.ArtistNames)
	}
	if track.Album != "Smoke Album" || track.Duration != "3:05" || track.Hash != "smoke-hash" {
		t.Fatalf("metadata was not preserved: %#v", track)
	}
	domTrack := result.Tracks[1]
	if domTrack.Fields["name"] != "DOM Smoke Song" || domTrack.Fields["artist"] != "DOM Artist" || domTrack.VisibleText == "" {
		t.Fatalf("DOM candidate was not preserved after global data: %#v", domTrack)
	}
	songs := playlist.DecodeTracks(result.Tracks, nil)
	if len(songs) != 2 || songs[0].Name != "Browser Smoke Song" || songs[0].Artist != "Smoke Artist" ||
		songs[1].Name != "DOM Smoke Song" || songs[1].Artist != "DOM Artist" {
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
