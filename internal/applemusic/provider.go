// Package applemusic implements Apple Music playlist and album extraction optimizations.
package applemusic

import (
	"net/url"
	"strings"

	"github.com/bagags/music2bb-go/internal/playlist"
)

// ProviderID joins Apple Music URL identification to its independently
// registered collection extraction optimization.
const ProviderID playlist.ProviderID = "apple-music"

// URLIdentifier matches only music.apple.com and its hostname boundary.
type URLIdentifier struct{}

func (URLIdentifier) MatchesURL(value *url.URL) bool {
	if value == nil {
		return false
	}
	hostname := strings.TrimRight(strings.ToLower(value.Hostname()), ".")
	return hostname == "music.apple.com"
}

// Identifier returns the pure Apple Music URL identifier used by wiring.
func Identifier() playlist.Identifier { return URLIdentifier{} }

// IdentificationRegistration returns Apple Music's explicit registry entry.
func IdentificationRegistration() playlist.IdentificationRegistration {
	return playlist.IdentificationRegistration{ProviderID: ProviderID, Identifier: Identifier()}
}

// Optimizations returns Apple Music's independently composable provider
// optimizations for explicit registry assembly.
func Optimizations(client *Client) playlist.ProviderOptimizations {
	result := playlist.ProviderOptimizations{}
	if client != nil {
		result.PlaylistExtractors = []playlist.PlaylistExtractor{client}
	}
	return result
}

// OptimizationRegistration returns Apple Music's explicit optimization
// registry entry.
func OptimizationRegistration(client *Client) playlist.OptimizationRegistration {
	return playlist.OptimizationRegistration{ProviderID: ProviderID, Optimizations: Optimizations(client)}
}

var _ playlist.Identifier = URLIdentifier{}
