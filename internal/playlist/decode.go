package playlist

import (
	"strings"
	"unicode/utf8"

	"github.com/bagags/music2bb-go/internal/model"
)

// FieldTitleOptions configure a reusable source-field title extractor.
type FieldTitleOptions struct {
	OptimizationName      string
	TitleKeys             []string
	ArtistKeys            []string
	SplitTitleKeys        []string
	SplitWhenArtistNames  bool
	ArtistSeparator       string
	FirstPresentFieldWins bool
}

// FieldTitleExtractor decodes titles from configured scalar source fields.
type FieldTitleExtractor struct {
	options FieldTitleOptions
}

// NewFieldTitleExtractor returns a reusable configurable title extractor.
func NewFieldTitleExtractor(options FieldTitleOptions) *FieldTitleExtractor {
	options.TitleKeys = append([]string(nil), options.TitleKeys...)
	options.ArtistKeys = append([]string(nil), options.ArtistKeys...)
	options.SplitTitleKeys = append([]string(nil), options.SplitTitleKeys...)
	if options.ArtistSeparator == "" {
		options.ArtistSeparator = "、"
	}
	return &FieldTitleExtractor{options: options}
}

func (e *FieldTitleExtractor) Name() string {
	if e == nil || e.options.OptimizationName == "" {
		return "field-title"
	}
	return e.options.OptimizationName
}

func (e *FieldTitleExtractor) ExtractTitle(candidate TrackCandidate) (ExtractedTitle, bool) {
	title, ok, _ := e.extractTitle(candidate)
	return title, ok
}

func (e *FieldTitleExtractor) extractTitle(candidate TrackCandidate) (ExtractedTitle, bool, bool) {
	if e == nil {
		return ExtractedTitle{}, false, false
	}
	nameKey, name := configuredField(candidate.Fields, e.options.TitleKeys, e.options.FirstPresentFieldWins)
	artistKey, artist := configuredField(candidate.Fields, e.options.ArtistKeys, e.options.FirstPresentFieldWins)
	_ = artistKey
	name = strings.TrimSpace(name)
	artist = strings.TrimSpace(artist)
	if artist == "" && candidate.ArtistNames != nil {
		names := make([]string, 0, len(candidate.ArtistNames))
		for _, value := range candidate.ArtistNames {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				names = append(names, trimmed)
			}
		}
		artist = strings.Join(names, e.options.ArtistSeparator)
	}
	if containsString(e.options.SplitTitleKeys, nameKey) || (e.options.SplitWhenArtistNames && candidate.ArtistNames != nil) {
		if filenameArtist, filenameTitle, ok := splitArtistTitle(name); ok {
			name = filenameTitle
			if artist == "" {
				artist = filenameArtist
			}
		}
	}
	if name == "" {
		return ExtractedTitle{}, false, e.options.FirstPresentFieldWins && nameKey != ""
	}
	return ExtractedTitle{Name: name, Artist: artist}, true, false
}

func configuredField(fields map[string]string, keys []string, firstPresent bool) (string, string) {
	for _, key := range keys {
		value, exists := fields[key]
		if !exists {
			continue
		}
		if firstPresent || strings.TrimSpace(value) != "" {
			return key, value
		}
	}
	return "", ""
}

func containsString(values []string, wanted string) bool {
	for _, value := range values {
		if value == wanted {
			return true
		}
	}
	return false
}

func splitArtistTitle(value string) (string, string, bool) {
	parts := strings.SplitN(value, " - ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	artist := strings.TrimSpace(parts[0])
	title := strings.TrimSpace(parts[1])
	return artist, title, artist != "" && title != ""
}

type commonTitleExtractor struct{}

func (commonTitleExtractor) Name() string { return "common-field-text" }

func (commonTitleExtractor) ExtractTitle(candidate TrackCandidate) (ExtractedTitle, bool) {
	fieldExtractor := NewFieldTitleExtractor(FieldTitleOptions{
		OptimizationName:     "common-fields",
		TitleKeys:            []string{"songname", "name", "title", "songName", "filename", "FileName"},
		ArtistKeys:           []string{"singername", "author", "artist", "singerName"},
		SplitTitleKeys:       []string{"filename", "FileName"},
		SplitWhenArtistNames: true,
		ArtistSeparator:      "、",
	})
	if title, ok := fieldExtractor.ExtractTitle(candidate); ok {
		return title, true
	}
	text := strings.TrimSpace(candidate.VisibleText)
	if text == "" {
		return ExtractedTitle{}, false
	}
	lines := strings.FieldsFunc(text, func(r rune) bool { return r == '\n' || r == '\r' })
	cleaned := make([]string, 0, len(lines))
	for _, line := range lines {
		if line = strings.TrimSpace(line); line != "" {
			cleaned = append(cleaned, line)
		}
	}
	if len(cleaned) >= 2 {
		return ExtractedTitle{Name: cleaned[0], Artist: cleaned[1]}, true
	}
	if name, artist, ok := splitVisibleText(text); ok {
		return ExtractedTitle{Name: name, Artist: artist}, true
	}
	return ExtractedTitle{Name: text}, true
}

func splitVisibleText(value string) (string, string, bool) {
	parts := strings.SplitN(value, " - ", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	name := strings.TrimSpace(parts[0])
	artist := strings.TrimSpace(parts[1])
	return name, artist, name != "" && artist != ""
}

// DecodeTracks applies provider title extractors first and the common field
// and visible-text extractor last. Invalid and duplicate decoded songs are
// discarded in stable order.
func DecodeTracks(candidates []TrackCandidate, titleExtractors []TitleExtractor) []model.Song {
	extractors := append([]TitleExtractor(nil), titleExtractors...)
	extractors = append(extractors, commonTitleExtractor{})
	songs := make([]model.Song, 0, len(candidates))
	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		var title ExtractedTitle
		valid := false
		for _, extractor := range extractors {
			if extractor == nil {
				continue
			}
			var current ExtractedTitle
			var ok, terminal bool
			if configured, isConfigured := extractor.(interface {
				extractTitle(TrackCandidate) (ExtractedTitle, bool, bool)
			}); isConfigured {
				current, ok, terminal = configured.extractTitle(candidate.Clone())
			} else {
				current, ok = extractor.ExtractTitle(candidate.Clone())
			}
			current.Name = strings.TrimSpace(current.Name)
			current.Artist = strings.TrimSpace(current.Artist)
			if !ok || current.Name == "" {
				if terminal {
					break
				}
				continue
			}
			title = current
			valid = true
			break
		}
		if !valid {
			continue
		}
		if candidate.FilterNonSongText && IsNonSongText(title.Name) {
			continue
		}
		if candidate.MaxTitleLength > 0 && utf8.RuneCountInString(title.Name) >= candidate.MaxTitleLength {
			continue
		}
		key := title.Name + "\x00" + title.Artist
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		songs = append(songs, model.Song{
			Name: title.Name, Artist: title.Artist,
			Album: strings.TrimSpace(candidate.Album), Duration: strings.TrimSpace(candidate.Duration), Hash: strings.TrimSpace(candidate.Hash),
			SourceID: strings.TrimSpace(candidate.SourceID),
		})
	}
	return songs
}

// IsNonSongText reports common browser UI labels that are not song titles.
func IsNonSongText(text string) bool {
	for _, skipped := range []string{
		"全部", "播放", "VIP", "收藏", "歌单", "分享", "下载", "评论", "首歌曲",
		"正在加载", "加载中", "Loading", "暂无", "没有更多", "已到底",
	} {
		if strings.Contains(text, skipped) {
			return true
		}
	}
	return false
}

func normalizeSongs(songs []model.Song, normalizers []SongNormalizer) []model.Song {
	result := append([]model.Song(nil), songs...)
	for _, normalizer := range normalizers {
		if normalizer != nil {
			result = normalizer.NormalizeSongs(append([]model.Song(nil), result...))
		}
	}
	return result
}

// MergeSongs appends additional songs in order while giving existing songs
// precedence. Hash and trimmed title/artist identities match the legacy Kugou
// merge behavior.
func MergeSongs(existing, additional []model.Song) []model.Song {
	merged := append([]model.Song(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(additional))
	seenNames := make(map[string]struct{}, len(existing)+len(additional))
	for _, song := range existing {
		seen[songIdentity(song)] = struct{}{}
		seenNames[songNameIdentity(song)] = struct{}{}
	}
	for _, song := range additional {
		key := songIdentity(song)
		if _, exists := seen[key]; exists {
			continue
		}
		nameKey := songNameIdentity(song)
		if _, exists := seenNames[nameKey]; exists {
			continue
		}
		seen[key] = struct{}{}
		seenNames[nameKey] = struct{}{}
		merged = append(merged, song)
	}
	return merged
}

func songNameIdentity(song model.Song) string {
	return strings.TrimSpace(song.Name) + "\x00" + strings.TrimSpace(song.Artist)
}

func songIdentity(song model.Song) string {
	if hash := strings.TrimSpace(song.Hash); hash != "" {
		return "hash:" + strings.ToUpper(hash)
	}
	return "song:" + songNameIdentity(song)
}
