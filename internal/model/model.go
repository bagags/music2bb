// Package model contains the presentation-independent data used by the
// converter. It deliberately has no I/O or process-level behavior.
package model

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// QualityLevel classifies candidate audio quality.
type QualityLevel int

const (
	QualityLow QualityLevel = iota + 1
	QualityStandard
	QualityHigh
	QualityLossless
	QualityHiRes
)

// Song is a track extracted from an online playlist.
type Song struct {
	Name     string
	Artist   string
	Album    string
	Duration string
	Hash     string
	SourceID string
}

// StableSourceID returns a provider identity when available. Kugou hashes are
// stable source identifiers; metadata-only sources use a normalized SHA-256
// fingerprint so playlist reordering does not affect identity.
func (s Song) StableSourceID() string {
	if sourceID := strings.TrimSpace(s.SourceID); sourceID != "" {
		return sourceID
	}
	if hash := strings.TrimSpace(s.Hash); hash != "" {
		return "kugou:" + strings.ToUpper(hash)
	}
	parts := []string{s.Name, s.Artist, s.Album, s.Duration}
	for index := range parts {
		parts[index] = strings.ToLower(strings.Join(strings.Fields(parts[index]), " "))
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "metadata:" + hex.EncodeToString(digest[:])
}

// Video is a Bilibili search result or video-detail record.
type Video struct {
	BVID          string
	Title         string
	Uploader      string
	Duration      string
	PlayCount     int64
	FavoriteCount int64
	DanmakuCount  int64
	Description   string
	Tags          []string
	IsOfficial    bool
	IsVerified    bool
	QualityScore  float64
	AID           int64
}

// URL returns the canonical Bilibili video URL.
func (v Video) URL() string {
	return "https://www.bilibili.com/video/" + v.BVID
}

// MatchResult describes how one video candidate scored for a song.
type MatchResult struct {
	Song            Song
	Video           *Video
	Score           float64
	TitleScore      float64
	ArtistScore     float64
	KeywordScore    float64
	QualityScore    float64
	OfficialScore   float64
	PopularityScore float64
	UploaderScore   float64
	Matched         bool
	ManualOverride  bool
	ReviewReason    ReviewReason
}

// ReviewReason explains why a song could not be selected automatically.
type ReviewReason string

const (
	ReviewNone             ReviewReason = ""
	ReviewNoCandidates     ReviewReason = "no_candidates"
	ReviewSearchFailed     ReviewReason = "search_failed"
	ReviewWeakTitle        ReviewReason = "weak_title"
	ReviewArtistUnverified ReviewReason = "artist_unverified"
	ReviewAmbiguous        ReviewReason = "ambiguous"
	ReviewRiskControl      ReviewReason = "risk_control"
	ReviewNotSearched      ReviewReason = "not_searched"
	ReviewBudgetExhausted  ReviewReason = "budget_exhausted"
)

// Favorite is a Bilibili favorites folder.
type Favorite struct {
	ID         int64
	Title      string
	Count      int
	MediaCount int
}

type artistAlias struct {
	needle  string
	aliases []string
}

// Keep this ordered because the first matching entry wins, including HOYO
// matching before HOYO-MiX.
var artistAliases = []artistAlias{
	{needle: "知更鸟", aliases: []string{"Robin", "知更鸟", "崩坏星穹铁道"}},
	{needle: "HOYO", aliases: []string{"HOYO-MiX", "米哈游", "miHoYo"}},
	{needle: "HOYO-MiX", aliases: []string{"HOYO-MiX", "米哈游", "miHoYo"}},
	{needle: "初音ミク", aliases: []string{"初音未来", "Miku", "Hatsune Miku"}},
	{needle: "ミク", aliases: []string{"初音未来", "Miku"}},
}

// Normalization treats Unicode separator runes plus these ASCII and NEL
// controls as whitespace. Keeping the class explicit avoids Go regexp's
// ASCII-only \s.
const whitespaceClass = `[\p{Z}\t\n\f\r\x0B\x{0085}]`

var (
	fromTitlePattern       = regexp.MustCompile(`\(From` + whitespaceClass + `+"([^"]+)"\)`)
	fromTitleRemove        = regexp.MustCompile(whitespaceClass + `*\(From` + whitespaceClass + `+"[^"]+"\)` + whitespaceClass + `*`)
	trailingVersion        = regexp.MustCompile(whitespaceClass + `*\p{Nd}+$`)
	parenthesesPattern     = regexp.MustCompile(whitespaceClass + `*\([^)]*\)` + whitespaceClass + `*`)
	wideParenthesesPattern = regexp.MustCompile(whitespaceClass + `*（[^）]*）` + whitespaceClass + `*`)
	bracketsPattern        = regexp.MustCompile(whitespaceClass + `*\[[^\]]*\]` + whitespaceClass + `*`)
	wideBracketsPattern    = regexp.MustCompile(whitespaceClass + `*【[^】]*】` + whitespaceClass + `*`)
	featPattern            = regexp.MustCompile(`(?i)` + whitespaceClass + `*feat\.?` + whitespaceClass + `*.*$`)
	hyphenSuffixPattern    = regexp.MustCompile(whitespaceClass + `*-` + whitespaceClass + `*.*$`)
	spacesPattern          = regexp.MustCompile(whitespaceClass + `+`)
	artistParentheses      = regexp.MustCompile(`\([^)]*\)`)
	artistWideParentheses  = regexp.MustCompile(`（[^）]*）`)
)

// CleanName applies suffix normalization in a deterministic order. In
// particular, a From suffix is extracted before other suffixes.
func (s Song) CleanName() string {
	name := strings.TrimSpace(s.Name)
	if match := fromTitlePattern.FindStringSubmatch(name); len(match) == 2 {
		fromKeyword := strings.TrimSpace(match[1])
		name = fromTitleRemove.ReplaceAllString(name, "")
		fromKeyword = strings.TrimSpace(trailingVersion.ReplaceAllString(fromKeyword, ""))
		if fromKeyword != "" {
			name += " " + fromKeyword
		}
	}

	name = parenthesesPattern.ReplaceAllString(name, " ")
	name = wideParenthesesPattern.ReplaceAllString(name, " ")
	name = bracketsPattern.ReplaceAllString(name, " ")
	name = wideBracketsPattern.ReplaceAllString(name, " ")
	name = featPattern.ReplaceAllString(name, "")
	name = hyphenSuffixPattern.ReplaceAllString(name, "")
	return strings.TrimSpace(spacesPattern.ReplaceAllString(name, " "))
}

// CleanArtist selects the first primary artist while retaining HOYO and miHoYo
// search cues.
func (s Song) CleanArtist() string {
	artist := strings.TrimSpace(s.Artist)
	keepCandidates := []string{"HOYO", "Hoyo", "hoyo", "米哈游", "miHoYo", "mihoyo"}
	kept := make([]string, 0, len(keepCandidates))
	for _, keyword := range keepCandidates {
		if strings.Contains(artist, keyword) {
			kept = append(kept, keyword)
		}
	}

	artist = artistParentheses.ReplaceAllString(artist, "")
	artist = artistWideParentheses.ReplaceAllString(artist, "")
	for _, separator := range []string{",", "、", "/", "&"} {
		if index := strings.Index(artist, separator); index >= 0 {
			artist = strings.TrimSpace(artist[:index])
			break
		}
	}
	for _, keyword := range kept {
		if !strings.Contains(artist, keyword) {
			artist += " " + keyword
		}
	}
	return strings.TrimSpace(artist)
}

// SearchKeyword is the normalized song name.
func (s Song) SearchKeyword() string {
	return s.CleanName()
}

// SearchKeywordFull is the primary name-and-artist query. Artist aliases are
// generated for fallback searches, but the original cleaned artist remains
// the primary query.
func (s Song) SearchKeywordFull() string {
	name := s.CleanName()
	artist := s.CleanArtist()
	if artist == "" {
		return name
	}
	return name + " " + artist
}

// AllSearchKeywords returns up to three queries in deterministic priority order.
func (s Song) AllSearchKeywords() []string {
	name := s.CleanName()
	artist := s.CleanArtist()
	keywords := make([]string, 0, 4)
	if artist != "" {
		keywords = append(keywords, name+" "+artist)
	}
	for _, entry := range artistAliases {
		if strings.Contains(s.Artist, entry.needle) || strings.Contains(artist, entry.needle) {
			for _, alias := range entry.aliases {
				keywords = append(keywords, name+" "+alias)
			}
			break
		}
	}
	if len(keywords) == 0 {
		keywords = append(keywords, name)
	}
	if len(keywords) > 3 {
		keywords = keywords[:3]
	}
	return keywords
}

// ArtistKeywords returns the cleaned source artist and known aliases that can
// be used as independent evidence when deciding whether an automatic match is
// safe. The order is deterministic and duplicates are removed.
func (s Song) ArtistKeywords() []string {
	artist := s.CleanArtist()
	keywords := make([]string, 0, 8)
	if artist != "" {
		keywords = append(keywords, artist)
	}
	for _, credit := range splitArtistCredits(s.Artist) {
		credit = artistParentheses.ReplaceAllString(credit, "")
		credit = artistWideParentheses.ReplaceAllString(credit, "")
		if credit = strings.TrimSpace(credit); credit != "" {
			keywords = append(keywords, credit)
		}
	}
	for _, entry := range artistAliases {
		if strings.Contains(s.Artist, entry.needle) || strings.Contains(artist, entry.needle) {
			keywords = append(keywords, entry.aliases...)
			break
		}
	}
	result := make([]string, 0, len(keywords))
	seen := make(map[string]struct{}, len(keywords))
	for _, keyword := range keywords {
		keyword = strings.TrimSpace(keyword)
		if keyword == "" {
			continue
		}
		key := strings.ToLower(keyword)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, keyword)
	}
	return result
}

func splitArtistCredits(artist string) []string {
	return strings.FieldsFunc(artist, func(r rune) bool {
		switch r {
		case ',', '，', '、', '/', '&', '＆', ';', '；':
			return true
		default:
			return false
		}
	})
}
