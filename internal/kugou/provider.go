package kugou

import (
	"net/url"
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/playlist"
)

// ProviderID joins Kugou URL identification to its independently assembled
// extraction, title, and cleanup optimizations.
const ProviderID playlist.ProviderID = "kugou"

// URLIdentifier matches kugou.com and its real subdomains.
type URLIdentifier struct{}

func (URLIdentifier) MatchesURL(value *url.URL) bool {
	if value == nil {
		return false
	}
	hostname := strings.TrimRight(strings.ToLower(value.Hostname()), ".")
	return hostname == "kugou.com" || strings.HasSuffix(hostname, ".kugou.com")
}

// Identifier returns the pure Kugou URL identifier used by wiring.
func Identifier() playlist.Identifier { return URLIdentifier{} }

// IdentificationRegistration returns Kugou's explicit registry entry.
func IdentificationRegistration() playlist.IdentificationRegistration {
	return playlist.IdentificationRegistration{ProviderID: ProviderID, Identifier: Identifier()}
}

// NewTitleExtractor returns the reusable field-based title configuration that
// preserves Kugou's legacy source-key priority and filename splitting rules.
func NewTitleExtractor() *playlist.FieldTitleExtractor {
	return playlist.NewFieldTitleExtractor(playlist.FieldTitleOptions{
		OptimizationName:      "kugou-fields",
		TitleKeys:             []string{"songname", "name", "title", "songName", "filename", "FileName"},
		ArtistKeys:            []string{"singername", "author", "artist", "singerName"},
		SplitTitleKeys:        []string{"filename", "FileName"},
		SplitWhenArtistNames:  true,
		ArtistSeparator:       "、",
		FirstPresentFieldWins: true,
	})
}

// CleanupNormalizer applies Kugou phantom-row and duplicate cleanup to songs
// regardless of whether direct extraction or the generic browser produced
// them.
type CleanupNormalizer struct{}

func (CleanupNormalizer) Name() string { return "kugou-cleanup" }

func (CleanupNormalizer) NormalizeSongs(songs []model.Song) []model.Song {
	return CleanupSongs(songs)
}

// Optimizations returns Kugou's independently composable provider
// optimizations for explicit registry assembly.
func Optimizations(client *Client) playlist.ProviderOptimizations {
	result := playlist.ProviderOptimizations{
		TitleExtractors: []playlist.TitleExtractor{NewTitleExtractor()},
		SongNormalizers: []playlist.SongNormalizer{CleanupNormalizer{}},
	}
	if client != nil {
		result.PlaylistExtractors = []playlist.PlaylistExtractor{client}
	}
	return result
}

// OptimizationRegistration returns Kugou's explicit optimization registry
// entry.
func OptimizationRegistration(client *Client) playlist.OptimizationRegistration {
	return playlist.OptimizationRegistration{ProviderID: ProviderID, Optimizations: Optimizations(client)}
}

var (
	_ playlist.Identifier     = URLIdentifier{}
	_ playlist.SongNormalizer = CleanupNormalizer{}
)
