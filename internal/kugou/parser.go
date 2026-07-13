package kugou

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/playlist"
)

var jsonScriptPattern = regexp.MustCompile(`(?is)<script[^>]*(?:application/json|__NEXT_DATA__)[^>]*>(.*?)</script>`)

var listKeys = []string{"info", "songs", "list", "songlist", "songList", "data"}

// ExtractEmbeddedTracks parses the JSON assignment and JSON script shapes
// used by Kugou pages into provider-neutral raw track candidates.
func ExtractEmbeddedTracks(pageHTML string) []playlist.TrackCandidate {
	candidates := make([]string, 0, 8)
	for _, match := range jsonScriptPattern.FindAllStringSubmatch(pageHTML, -1) {
		if len(match) == 2 {
			candidates = append(candidates, html.UnescapeString(strings.TrimSpace(match[1])))
		}
	}
	for _, marker := range []string{"var songData", "playlistData", `"songs"`} {
		searchFrom := 0
		for {
			index := strings.Index(pageHTML[searchFrom:], marker)
			if index < 0 {
				break
			}
			index += searchFrom + len(marker)
			if candidate, ok := balancedJSONAfter(pageHTML, index); ok {
				candidates = append(candidates, candidate)
			}
			searchFrom = index
		}
	}

	for _, candidate := range candidates {
		var value any
		decoder := json.NewDecoder(strings.NewReader(candidate))
		decoder.UseNumber()
		if err := decoder.Decode(&value); err != nil {
			continue
		}
		items := findSongItems(value, 0)
		if tracks := trackCandidatesFromItems(items); len(tracks) > 0 {
			return tracks
		}
	}
	return nil
}

// ExtractEmbeddedSongs is retained for fixture and parity callers. Production
// ingestion uses ExtractEmbeddedTracks and decodes through registered title
// capabilities.
func ExtractEmbeddedSongs(pageHTML string) []model.Song {
	return playlist.DecodeTracks(ExtractEmbeddedTracks(pageHTML), []playlist.TitleExtractor{NewTitleExtractor()})
}

func balancedJSONAfter(text string, offset int) (string, bool) {
	if offset < 0 || offset >= len(text) {
		return "", false
	}
	start := -1
	for index := offset; index < len(text); index++ {
		switch text[index] {
		case '[', '{':
			start = index
		}
		if start >= 0 {
			break
		}
		if text[index] == '<' || text[index] == ';' {
			return "", false
		}
	}
	if start < 0 {
		return "", false
	}
	stack := make([]byte, 0, 8)
	inString := false
	escaped := false
	for index := start; index < len(text); index++ {
		char := text[index]
		if inString {
			if escaped {
				escaped = false
				continue
			}
			if char == '\\' {
				escaped = true
				continue
			}
			if char == '"' {
				inString = false
			}
			continue
		}
		switch char {
		case '"':
			inString = true
		case '[', '{':
			stack = append(stack, char)
		case ']', '}':
			if len(stack) == 0 || (char == ']' && stack[len(stack)-1] != '[') || (char == '}' && stack[len(stack)-1] != '{') {
				return "", false
			}
			stack = stack[:len(stack)-1]
			if len(stack) == 0 {
				return text[start : index+1], true
			}
		}
	}
	return "", false
}

func decodeSongResponse(payload []byte) []model.Song {
	return playlist.DecodeTracks(decodeSongPage(payload).Tracks, []playlist.TitleExtractor{NewTitleExtractor()})
}

func decodeSongPage(payload []byte) playlist.RawResult {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return playlist.RawResult{}
	}
	return playlist.RawResult{
		Tracks:        trackCandidatesFromItems(findSongItems(value, 0)),
		ExpectedTotal: findExpectedTotal(value, 0),
	}
}

func findSongItems(value any, depth int) []any {
	if depth > 10 || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case []any:
		for _, item := range typed {
			if songMap, ok := item.(map[string]any); ok && hasSongNameKey(songMap) {
				return typed
			}
		}
		for _, item := range typed {
			if found := findSongItems(item, depth+1); len(found) > 0 {
				return found
			}
		}
	case map[string]any:
		for _, key := range listKeys {
			if child, ok := typed[key]; ok {
				if found := findSongItems(child, depth+1); len(found) > 0 {
					return found
				}
			}
		}
	}
	return nil
}

func hasSongNameKey(item map[string]any) bool {
	for _, key := range []string{"songname", "name", "title", "songName", "filename", "FileName"} {
		if value, exists := item[key]; exists && strings.TrimSpace(stringValue(value)) != "" {
			return true
		}
	}
	return false
}

func findExpectedTotal(value any, depth int) int {
	if depth > 10 || value == nil {
		return 0
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, key := range []string{"total", "count"} {
			if total := intValue(typed[key]); total > 0 {
				return total
			}
		}
		for _, key := range listKeys {
			if child, ok := typed[key]; ok {
				if total := findExpectedTotal(child, depth+1); total > 0 {
					return total
				}
			}
		}
	}
	return 0
}

func trackCandidatesFromItems(items []any) []playlist.TrackCandidate {
	tracks := make([]playlist.TrackCandidate, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for _, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok {
			continue
		}
		candidate := trackCandidate(item)
		song, ok := decodeCandidate(candidate)
		if !ok {
			continue
		}
		key := song.Name + "|" + song.Artist
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		tracks = append(tracks, candidate)
	}
	return tracks
}

func trackCandidate(item map[string]any) playlist.TrackCandidate {
	fields := make(map[string]string, len(item))
	for key, value := range item {
		if scalar, ok := scalarValue(value); ok {
			fields[key] = scalar
		}
	}
	return playlist.TrackCandidate{
		Fields:      fields,
		ArtistNames: artistNames(item["singerinfo"]),
		Album:       albumName(item),
		Duration:    formatDuration(firstValue(item, "duration", "timelength", "timelen")),
		Hash:        firstExisting(item, "hash", "320hash", "filehash"),
	}
}

func scalarValue(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", true
	case string:
		return typed, true
	case json.Number:
		return typed.String(), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case int:
		return strconv.Itoa(typed), true
	case int64:
		return strconv.FormatInt(typed, 10), true
	case bool:
		return strconv.FormatBool(typed), true
	default:
		return "", false
	}
}

func artistNames(value any) []string {
	if value == nil {
		return nil
	}
	names := []string{}
	items, ok := value.([]any)
	if !ok {
		return names
	}
	for _, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name := strings.TrimSpace(stringValue(item["name"]))
		if name != "" {
			names = append(names, name)
		}
	}
	return names
}

func firstExisting(item map[string]any, keys ...string) string {
	return stringValue(firstValue(item, keys...))
}

func firstValue(item map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, exists := item[key]; exists {
			return value
		}
	}
	return nil
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case json.Number:
		integer, _ := typed.Int64()
		return int(integer)
	case float64:
		return int(typed)
	case int:
		return typed
	case int64:
		return int(typed)
	case string:
		integer, _ := strconv.Atoi(typed)
		return integer
	default:
		return 0
	}
}

func albumName(item map[string]any) string {
	if album := strings.TrimSpace(firstExisting(item, "album_name", "albumname", "album")); album != "" {
		return album
	}
	if info, ok := item["albuminfo"].(map[string]any); ok {
		return strings.TrimSpace(stringValue(info["name"]))
	}
	return ""
}

func formatDuration(value any) string {
	var seconds int64
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			seconds = integer
		} else if decimal, err := typed.Float64(); err == nil {
			seconds = int64(decimal)
		} else {
			return ""
		}
	case float64:
		seconds = int64(typed)
	case int:
		seconds = int64(typed)
	case int64:
		seconds = typed
	case string:
		integer, err := strconv.ParseInt(typed, 10, 64)
		if err != nil {
			return ""
		}
		seconds = integer
	default:
		return ""
	}
	// The current public Kugou playlist endpoint reports milliseconds while
	// legacy endpoints report seconds. Values beyond a day cannot reasonably
	// be seconds for playlist tracks and are normalized to seconds here.
	if seconds >= 24*60*60 {
		seconds /= 1000
	}
	minutes := seconds / 60
	remainder := seconds % 60
	if remainder < 0 {
		minutes--
		remainder += 60
	}
	return fmt.Sprintf("%d:%02d", minutes, remainder)
}
