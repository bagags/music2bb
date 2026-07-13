package playlist

import (
	"reflect"
	"testing"

	"github.com/gguage/music-to-bb/internal/model"
)

type fixedTitleExtractor struct {
	name  string
	title ExtractedTitle
	ok    bool
}

func (e fixedTitleExtractor) Name() string { return e.name }
func (e fixedTitleExtractor) ExtractTitle(TrackCandidate) (ExtractedTitle, bool) {
	return e.title, e.ok
}

func TestDecodeTracksUsesProviderTitleBeforeCommonFallback(t *testing.T) {
	candidates := []TrackCandidate{{
		Fields: map[string]string{"name": "Common", "artist": "Common Artist"},
		Album:  " Album ", Duration: "3:05", Hash: "hash",
	}}
	provider := fixedTitleExtractor{name: "provider", title: ExtractedTitle{Name: "Provider", Artist: "Artist"}, ok: true}
	want := []model.Song{{Name: "Provider", Artist: "Artist", Album: "Album", Duration: "3:05", Hash: "hash"}}
	if got := DecodeTracks(candidates, []TitleExtractor{provider}); !reflect.DeepEqual(got, want) {
		t.Fatalf("DecodeTracks = %#v, want %#v", got, want)
	}
}

func TestDecodeTracksFallsBackAndPreservesFieldSplitRules(t *testing.T) {
	extractor := NewFieldTitleExtractor(FieldTitleOptions{
		OptimizationName:      "configured",
		TitleKeys:             []string{"songname", "filename", "FileName"},
		ArtistKeys:            []string{"singername"},
		SplitTitleKeys:        []string{"filename", "FileName"},
		SplitWhenArtistNames:  true,
		FirstPresentFieldWins: true,
	})
	tests := []struct {
		name      string
		candidate TrackCandidate
		want      ExtractedTitle
		ok        bool
	}{
		{name: "filename", candidate: TrackCandidate{Fields: map[string]string{"filename": "Singer - Song - Mix"}}, want: ExtractedTitle{Name: "Song - Mix", Artist: "Singer"}, ok: true},
		{name: "explicit artist wins", candidate: TrackCandidate{Fields: map[string]string{"FileName": "File Singer - Song", "singername": "Explicit"}}, want: ExtractedTitle{Name: "Song", Artist: "Explicit"}, ok: true},
		{name: "nested names trigger split", candidate: TrackCandidate{Fields: map[string]string{"songname": "File Singer - Song"}, ArtistNames: []string{"One", "Two"}}, want: ExtractedTitle{Name: "Song", Artist: "One、Two"}, ok: true},
		{name: "first present remains authoritative", candidate: TrackCandidate{Fields: map[string]string{"songname": "", "filename": "Singer - Song"}}, ok: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := extractor.ExtractTitle(test.candidate)
			if ok != test.ok || got != test.want {
				t.Fatalf("ExtractTitle = %#v, %v; want %#v, %v", got, ok, test.want, test.ok)
			}
		})
	}

	fallback := DecodeTracks([]TrackCandidate{{VisibleText: "Visible Song - Visible Artist"}}, nil)
	if len(fallback) != 1 || fallback[0].Name != "Visible Song" || fallback[0].Artist != "Visible Artist" {
		t.Fatalf("common visible fallback = %#v", fallback)
	}

	authoritative := DecodeTracks([]TrackCandidate{{Fields: map[string]string{"songname": "", "name": "Common must not win"}}}, []TitleExtractor{extractor})
	if len(authoritative) != 0 {
		t.Fatalf("authoritative empty provider field fell through to common decoder: %#v", authoritative)
	}
}

func TestMergeSongsPreservesDirectPrecedenceAndLegacyIdentity(t *testing.T) {
	direct := []model.Song{{Name: "Direct", Artist: "Artist", Hash: "ABC"}, {Name: "Same", Artist: "Singer", Album: "direct"}}
	browser := []model.Song{
		{Name: "Different", Artist: "Other", Hash: "abc"},
		{Name: " Same ", Artist: "Singer", Album: "browser"},
		{Name: "New", Artist: "Singer"},
	}
	want := append(append([]model.Song(nil), direct...), model.Song{Name: "New", Artist: "Singer"})
	if got := MergeSongs(direct, browser); !reflect.DeepEqual(got, want) {
		t.Fatalf("MergeSongs = %#v, want %#v", got, want)
	}
}
