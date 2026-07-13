package kugou

import (
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
)

// CleanupSongs mirrors the Python phantom-entry cleanup exactly: comparisons
// and de-duplication are case-sensitive and preserve the first input entry.
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
		if strings.ContainsAny(song.Name, "、,&/，") {
			continue
		}
		key := song.Name + "|" + song.Artist
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		cleaned = append(cleaned, song)
	}
	return cleaned
}
