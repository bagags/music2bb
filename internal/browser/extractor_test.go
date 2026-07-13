package browser

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/playlist"
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
