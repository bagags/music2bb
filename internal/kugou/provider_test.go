package kugou

import (
	"net/url"
	"reflect"
	"testing"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/playlist"
)

func TestURLIdentifierMatchesOnlyKugouHostBoundary(t *testing.T) {
	tests := []struct {
		rawURL string
		want   bool
	}{
		{"https://kugou.com/list", true},
		{"https://www.kugou.com/list", true},
		{"https://M.KUGOU.COM./list", true},
		{"https://kugou.com.evil.test/list", false},
		{"https://notkugou.com/list", false},
		{"https://example.test/list", false},
	}
	identifier := Identifier()
	for _, test := range tests {
		t.Run(test.rawURL, func(t *testing.T) {
			parsed, err := url.Parse(test.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			if got := identifier.MatchesURL(parsed); got != test.want {
				t.Fatalf("MatchesURL(%q) = %t, want %t", test.rawURL, got, test.want)
			}
		})
	}
	if identifier.MatchesURL(nil) {
		t.Fatal("nil URL matched Kugou")
	}
}

func TestTitleExtractorPreservesKugouPriorityAndSplitting(t *testing.T) {
	tests := []struct {
		name      string
		candidate playlist.TrackCandidate
		want      playlist.ExtractedTitle
		ok        bool
	}{
		{
			name:      "filename supplies artist",
			candidate: playlist.TrackCandidate{Fields: map[string]string{"filename": " Artist - Song - Live "}},
			want:      playlist.ExtractedTitle{Name: "Song - Live", Artist: "Artist"},
			ok:        true,
		},
		{
			name:      "explicit artist wins",
			candidate: playlist.TrackCandidate{Fields: map[string]string{"FileName": "Filename Artist - Song", "singername": "Explicit Artist"}},
			want:      playlist.ExtractedTitle{Name: "Song", Artist: "Explicit Artist"},
			ok:        true,
		},
		{
			name: "nested artists trigger safe split",
			candidate: playlist.TrackCandidate{
				Fields: map[string]string{"name": "Prefix - Song / Mix"}, ArtistNames: []string{"Singer A", "Singer B"},
			},
			want: playlist.ExtractedTitle{Name: "Song / Mix", Artist: "Singer A、Singer B"},
			ok:   true,
		},
		{
			name:      "first present key remains authoritative",
			candidate: playlist.TrackCandidate{Fields: map[string]string{"songname": "", "name": "Fallback"}},
			ok:        false,
		},
		{
			name:      "punctuation without delimiter is untouched",
			candidate: playlist.TrackCandidate{Fields: map[string]string{"filename": "Song/A – Mix", "author": "Singer"}},
			want:      playlist.ExtractedTitle{Name: "Song/A – Mix", Artist: "Singer"},
			ok:        true,
		},
	}
	extractor := NewTitleExtractor()
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, ok := extractor.ExtractTitle(test.candidate)
			if ok != test.ok || !reflect.DeepEqual(got, test.want) {
				t.Fatalf("ExtractTitle() = %#v, %t, want %#v, %t", got, ok, test.want, test.ok)
			}
		})
	}
}

func TestCleanupNormalizerAndRegistration(t *testing.T) {
	input := []model.Song{
		{Name: "Song", Artist: "Singer"},
		{Name: "Song"},
		{Name: "Singer"},
	}
	want := []model.Song{{Name: "Song", Artist: "Singer"}}
	if got := (CleanupNormalizer{}).NormalizeSongs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("NormalizeSongs() = %#v, want %#v", got, want)
	}

	client := &Client{}
	registration := OptimizationRegistration(client)
	if registration.ProviderID != ProviderID || len(registration.Optimizations.PlaylistExtractors) != 1 ||
		len(registration.Optimizations.TitleExtractors) != 1 || len(registration.Optimizations.SongNormalizers) != 1 {
		t.Fatalf("OptimizationRegistration() = %#v", registration)
	}
	identification := IdentificationRegistration()
	if identification.ProviderID != ProviderID || identification.Identifier == nil {
		t.Fatalf("IdentificationRegistration() = %#v", identification)
	}
}
