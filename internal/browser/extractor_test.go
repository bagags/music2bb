package browser

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/playlist"
	"github.com/go-rod/rod"
)

func TestDecodeBrowserResultPreservesRawCandidates(t *testing.T) {
	payload := `{
      "tracks": [
        {
          "fields": {"FileName":" Source Artist - Song ","custom_id":"42"},
          "artistNames": [],
          "visibleText": " Song\nSource Artist ",
          "album": " Album ",
          "duration": "3:05",
          "hash": "abc"
        },
        {
          "fields": {"name":"Other","artist":"Singer"},
          "artistNames": null,
          "visibleText": "Other - Singer"
        }
      ],
      "expectedTotal": 7
    }`
	want := playlist.RawResult{
		Tracks: []playlist.TrackCandidate{
			{
				Fields:      map[string]string{"FileName": " Source Artist - Song ", "custom_id": "42"},
				ArtistNames: []string{}, VisibleText: " Song\nSource Artist ",
				Album: " Album ", Duration: "3:05", Hash: "abc",
				FilterNonSongText: true, MaxTitleLength: 100,
			},
			{
				Fields:      map[string]string{"name": "Other", "artist": "Singer"},
				VisibleText: "Other - Singer", FilterNonSongText: true, MaxTitleLength: 100,
			},
		},
		ExpectedTotal: 7,
	}
	got, err := decodeBrowserResult(payload)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decodeBrowserResult = %#v, want %#v", got, want)
	}
	if got.Tracks[0].ArtistNames == nil {
		t.Fatal("present but empty singerinfo lost its presence")
	}
	if got.Tracks[1].ArtistNames != nil {
		t.Fatal("absent singerinfo became present")
	}
}

func TestBrowserCandidatesFilterAndDeduplicateAfterTitleExtraction(t *testing.T) {
	candidates := []playlist.TrackCandidate{
		{Fields: map[string]string{"filename": "File Artist - Song - Live"}, ArtistNames: []string{" Singer A ", "Singer B"}},
		{Fields: map[string]string{"filename": "File Artist - Song - Live"}, ArtistNames: []string{"Singer A", "Singer B"}},
		{Fields: map[string]string{"name": "正在加载"}},
		{Fields: map[string]string{"name": strings.Repeat("x", 100)}},
		{VisibleText: "DOM Song\nDOM Artist"},
	}
	payload, err := json.Marshal(browserResult{Tracks: candidates})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := decodeBrowserResult(string(payload))
	if err != nil {
		t.Fatal(err)
	}
	got := playlist.DecodeTracks(raw.Tracks, nil)
	want := []model.Song{
		{Name: "Song - Live", Artist: "Singer A、Singer B"},
		{Name: "DOM Song", Artist: "DOM Artist"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("decoded browser tracks = %#v, want %#v", got, want)
	}
}

func TestExtractorNeverInstallsBrowser(t *testing.T) {
	manifest := Manifest{Schema: 1, Artifacts: map[string]Artifact{
		"test/amd64": {Revision: 7, Executable: "test/chrome"},
	}}
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "test/amd64", Manifest: manifest,
	})
	if err != nil {
		t.Fatal(err)
	}
	extractor := NewExtractor(manager)
	available, err := extractor.Available(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if available {
		t.Fatal("uninstalled browser reported as available")
	}
	source, err := playlist.ParseSource("https://example.test")
	if err != nil {
		t.Fatal(err)
	}
	_, err = extractor.ExtractPlaylist(context.Background(), source)
	if !IsKind(err, ErrorNotInstalled) {
		t.Fatalf("ExtractPlaylist error = %v, want %s", err, ErrorNotInstalled)
	}
}

func TestExtractorWithoutManagerIsUnavailable(t *testing.T) {
	available, err := (*Extractor)(nil).Available(context.Background())
	if err != nil || available {
		t.Fatalf("Available = %v, %v; want false, nil", available, err)
	}
}

func TestNavigateWithRetryRetriesTransientAbort(t *testing.T) {
	calls := 0
	err := navigateWithRetry(context.Background(), func() error {
		calls++
		if calls == 1 {
			return &rod.NavigationError{Reason: "net::ERR_ABORTED"}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("navigate calls = %d, want 2", calls)
	}
}

func TestNavigateWithRetryDoesNotRetryOtherFailures(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "ordinary error", err: errors.New("navigation failed")},
		{name: "other Rod navigation error", err: &rod.NavigationError{Reason: "net::ERR_NAME_NOT_RESOLVED"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			err := navigateWithRetry(context.Background(), func() error {
				calls++
				return test.err
			})
			if !errors.Is(err, test.err) {
				t.Fatalf("navigate error = %v, want %v", err, test.err)
			}
			if calls != 1 {
				t.Fatalf("navigate calls = %d, want 1", calls)
			}
		})
	}
}

func TestNavigateWithRetryHonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err := navigateWithRetry(ctx, func() error {
		calls++
		return &rod.NavigationError{Reason: "net::ERR_ABORTED"}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("navigate error = %v, want %v", err, context.Canceled)
	}
	if calls != 1 {
		t.Fatalf("navigate calls = %d, want 1", calls)
	}
}
