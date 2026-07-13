package kugou

import (
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
)

// CleanupSongs removes browser phantom entries and exact duplicates while
// preserving punctuation that is valid inside real song titles.
func CleanupSongs(songs []model.Song) []model.Song {
	if len(songs) == 0 {
		return songs
	}
	artistSet := make(map[string]struct{}, len(songs))
	nameWithArtist := make(map[string]struct{}, len(songs))
	for _, song := range songs {
		if strings.TrimSpace(song.Artist) == "" {
			continue
		}
		artistSet[strings.TrimSpace(song.Artist)] = struct{}{}
		nameWithArtist[song.Name] = struct{}{}
	}

	cleaned := make([]model.Song, 0, len(songs))
	seen := make(map[string]struct{}, len(songs))
	for _, song := range songs {
		if _, isArtist := artistSet[song.Name]; isArtist {
			continue
		}
		if strings.TrimSpace(song.Artist) == "" {
			if _, hasArtistVariant := nameWithArtist[song.Name]; hasArtistVariant {
				continue
			}
		}
		key := songIdentity(song)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, song)
	}
	return cleaned
}
