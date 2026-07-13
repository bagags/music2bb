// Package playlist identifies playlist providers and coordinates typed
// extraction optimizations with the controlled browser fallback.
package playlist

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
)

// ProviderID joins provider identification to independently registered
// optimizations.
type ProviderID string

// GenericProviderID identifies a valid URL for which no provider identifier
// matched.
const GenericProviderID ProviderID = "generic"

// Source is a centrally validated HTTP(S) playlist source.
type Source struct {
	rawURL string
	url    url.URL
}

// ParseSource validates a raw playlist URL before provider identification or
// extraction is attempted.
func ParseSource(rawURL string) (Source, error) {
	trimmed := strings.TrimSpace(rawURL)
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return Source{}, err
	}
	if !strings.EqualFold(parsed.Scheme, "http") && !strings.EqualFold(parsed.Scheme, "https") {
		return Source{}, errors.New("URL must use http or https")
	}
	if parsed.Host == "" || parsed.Hostname() == "" {
		return Source{}, errors.New("URL must include a host")
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	return Source{rawURL: parsed.String(), url: *parsed}, nil
}

// String returns the normalized source URL.
func (s Source) String() string { return s.rawURL }

// URL returns a copy of the parsed source URL.
func (s Source) URL() *url.URL {
	copy := s.url
	return &copy
}

// Identifier is a pure URL predicate used only for provider identification.
type Identifier interface {
	MatchesURL(*url.URL) bool
}

// IdentifierFunc adapts a function to Identifier.
type IdentifierFunc func(*url.URL) bool

func (f IdentifierFunc) MatchesURL(value *url.URL) bool { return f != nil && f(value) }

// TrackCandidate preserves source field names and decoded metadata until the
// provider-aware title stage selects a song title and artist.
type TrackCandidate struct {
	Fields            map[string]string `json:"fields"`
	ArtistNames       []string          `json:"artistNames"`
	VisibleText       string            `json:"visibleText"`
	Album             string            `json:"album"`
	Duration          string            `json:"duration"`
	Hash              string            `json:"hash"`
	FilterNonSongText bool              `json:"-"`
	MaxTitleLength    int               `json:"-"`
}

// Clone returns a caller-owned copy of the candidate.
func (c TrackCandidate) Clone() TrackCandidate {
	cloned := c
	if c.Fields != nil {
		cloned.Fields = make(map[string]string, len(c.Fields))
		for key, value := range c.Fields {
			cloned.Fields[key] = value
		}
	}
	cloned.ArtistNames = append([]string(nil), c.ArtistNames...)
	if c.ArtistNames != nil && cloned.ArtistNames == nil {
		cloned.ArtistNames = []string{}
	}
	return cloned
}

// RawResult is the provider-neutral output of a playlist extraction attempt.
type RawResult struct {
	Tracks        []TrackCandidate
	ExpectedTotal int
}

// Result is the decoded playlist returned by the coordinator.
type Result struct {
	Songs         []model.Song
	ExpectedTotal int
}

// ExtractedTitle is a title capability's decoded title and artist pair.
type ExtractedTitle struct {
	Name   string
	Artist string
}

// Optimization provides the shared diagnostic identity for typed
// optimization categories.
type Optimization interface {
	Name() string
}

// PlaylistExtractor extracts ordered raw track candidates for a provider.
type PlaylistExtractor interface {
	Optimization
	ExtractPlaylist(context.Context, Source) (RawResult, error)
}

// TitleExtractor decodes a title and artist from a raw track candidate.
type TitleExtractor interface {
	Optimization
	ExtractTitle(TrackCandidate) (ExtractedTitle, bool)
}

// SongNormalizer composes provider cleanup after title decoding.
type SongNormalizer interface {
	Optimization
	NormalizeSongs([]model.Song) []model.Song
}

// ProviderOptimizations contains optional, independently composable
// optimization categories for one provider.
type ProviderOptimizations struct {
	PlaylistExtractors []PlaylistExtractor
	TitleExtractors    []TitleExtractor
	SongNormalizers    []SongNormalizer
}

// BrowserExtractor is the coordinator-owned generic browser fallback. It is
// deliberately separate from provider optimizations.
type BrowserExtractor interface {
	Available(context.Context) (bool, error)
	ExtractPlaylist(context.Context, Source) (RawResult, error)
}

// BrowserPolicy controls whether the coordinator may use the browser
// fallback.
type BrowserPolicy string

const (
	BrowserAuto   BrowserPolicy = "auto"
	BrowserNever  BrowserPolicy = "never"
	BrowserAlways BrowserPolicy = "always"
)
