// Package matcher ranks Bilibili candidates using the behavior of the Python
// reference implementation. Matcher instances own immutable configuration and
// are safe for concurrent use when callers do not share mutable Video values.
package matcher

import (
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/service"
)

const (
	qualityKeywordScore = 15.0
	weightedUPScore     = 30.0
	verifiedUPScore     = 20.0
	matchThreshold      = 20.0
)

var (
	officialKeywords   = []string{"官方", "official", "Official", "OFFICIAL", "官方MV", "OfficialMV", "官方mv"}
	officialUPPatterns = []string{"官方", "Official", "Music", "Records", "Entertainment", "音乐", "唱片", "工作室"}
)

// Weights controls the four normalized components. UP scores are added
// directly, matching the Python formula.
type Weights struct {
	Keyword    float64
	Quality    float64
	Official   float64
	Popularity float64
}

// DefaultWeights returns the Python reference weights.
func DefaultWeights() Weights {
	return Weights{Keyword: 40, Quality: 25, Official: 20, Popularity: 15}
}

// Options contains per-engine matcher configuration. Slices are cloned by New.
type Options struct {
	Weights           Weights
	BlockKeywords     []string
	QualityKeywords   []string
	WeightedUploaders []string
}

// Matcher is an immutable scorer with no global configuration.
type Matcher struct {
	weights           Weights
	blockKeywords     []string
	qualityKeywords   []string
	weightedUploaders []string
}

// New constructs an independent matcher. A zero Weights value selects the
// Python defaults; partially specified weights intentionally leave other
// components at zero.
func New(options Options) *Matcher {
	weights := options.Weights
	if weights == (Weights{}) {
		weights = DefaultWeights()
	}
	return &Matcher{
		weights:           weights,
		blockKeywords:     append([]string(nil), options.BlockKeywords...),
		qualityKeywords:   append([]string(nil), options.QualityKeywords...),
		weightedUploaders: append([]string(nil), options.WeightedUploaders...),
	}
}

// Weights returns a copy of the active weights.
func (m *Matcher) Weights() Weights {
	return m.weights
}

// ComputeKeywordScore calculates song/title similarity on the 0-100 scale.
func (m *Matcher) ComputeKeywordScore(song model.Song, video model.Video) float64 {
	title := strings.ToLower(video.Title)
	songName := strings.ToLower(strings.TrimSpace(song.Name))
	artistName := strings.ToLower(strings.TrimSpace(song.Artist))
	if songName == "" {
		return 0
	}

	songNameClean := cleanForMatch(songName)
	artistClean := cleanForMatch(artistName)
	titleClean := cleanForMatch(title)
	titleWords := wordSet(title)

	nameInTitle := strings.Contains(titleClean, songNameClean)
	artistInTitle := artistClean == "" || strings.Contains(titleClean, artistClean)
	var score float64
	switch {
	case nameInTitle && artistInTitle:
		score = 100
	case nameInTitle:
		score = 70
	case FuzzyContains(title, songName):
		score = 50
	default:
		songWords := wordSet(songName)
		if len(songWords) > 0 {
			score = float64(overlapCount(songWords, titleWords)) / float64(len(songWords)) * 40
		}
	}

	if artistClean != "" && !artistInTitle {
		artistWords := wordSet(artistClean)
		if len(artistWords) > 0 {
			artistOverlap := overlapCount(artistWords, titleWords)
			if float64(artistOverlap) >= float64(len(artistWords))*0.5 {
				score = math.Max(score, math.Min(score+15, 85))
			}
		}
	}

	allSongWords := wordSet(songName + " " + artistName)
	if len(allSongWords) > 0 {
		wordOverlap := float64(overlapCount(allSongWords, titleWords)) / float64(len(allSongWords))
		score = math.Max(score, wordOverlap*80)
	}
	return score
}

// ComputeQualityScore adds 15 for every configured keyword present in the
// title, description, or tags. Duplicate case variants deliberately stack.
func (m *Matcher) ComputeQualityScore(video *model.Video) float64 {
	if video == nil {
		return 0
	}
	text := strings.ToLower(video.Title + " " + video.Description + " " + strings.Join(video.Tags, " "))
	var score float64
	for _, keyword := range m.qualityKeywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			score += qualityKeywordScore
		}
	}
	video.QualityScore = score
	return score
}

// ComputeOfficialScore applies title/uploader and endpoint metadata signals.
func (m *Matcher) ComputeOfficialScore(video model.Video) float64 {
	text := strings.ToLower(video.Title + " " + video.Uploader)
	var score float64
	for _, keyword := range officialKeywords {
		if strings.Contains(text, strings.ToLower(keyword)) {
			score += 20
			break
		}
	}
	for _, pattern := range officialUPPatterns {
		if strings.Contains(strings.ToLower(video.Uploader), strings.ToLower(pattern)) {
			score += 15
			break
		}
	}
	if video.IsOfficial {
		score = math.Max(score, 25)
	}
	return math.Min(score, 30)
}

// ComputePopularityScore applies logarithmic play/favorite signals and the
// strict greater-than-ten-percent favorite-rate bonus.
func (m *Matcher) ComputePopularityScore(video model.Video) float64 {
	var score float64
	if video.PlayCount > 0 {
		score += math.Min(30, math.Log10(math.Max(float64(video.PlayCount), 1))*5)
	}
	if video.FavoriteCount > 0 {
		score += math.Min(25, math.Log10(math.Max(float64(video.FavoriteCount), 1))*5)
	}
	if video.PlayCount > 0 && video.FavoriteCount > 0 {
		rate := float64(video.FavoriteCount) / float64(video.PlayCount)
		if rate > 0.1 {
			score += math.Min(15, rate*50)
		}
	}
	return score
}

// ComputeUPScore adds exact, case-sensitive uploader and verification bonuses.
func (m *Matcher) ComputeUPScore(video model.Video) float64 {
	var score float64
	for _, uploader := range m.weightedUploaders {
		if video.Uploader == uploader {
			score += weightedUPScore
			break
		}
	}
	if video.IsVerified {
		score += verifiedUPScore
	}
	return score
}

// IsBlocked reports whether title or description contains a configured block
// keyword. Single-rune keywords require Unicode word boundaries.
func (m *Matcher) IsBlocked(video model.Video) bool {
	text := strings.ToLower(video.Title + " " + video.Description)
	for _, keyword := range m.blockKeywords {
		lowerKeyword := strings.ToLower(keyword)
		if utf8.RuneCountInString(keyword) == 1 {
			if containsStandaloneRune(text, []rune(lowerKeyword)[0]) {
				return true
			}
		} else if strings.Contains(text, lowerKeyword) {
			return true
		}
	}
	return false
}

// Match scores unblocked candidates, sorts them stably by descending total,
// and retains at most topK. A candidate is valid when keyword score is at least
// 20, including the threshold exactly.
func (m *Matcher) Match(song model.Song, videos []model.Video, topK int) []model.MatchResult {
	if topK <= 0 || len(videos) == 0 {
		return []model.MatchResult{}
	}
	results := make([]model.MatchResult, 0, len(videos))
	for index := range videos {
		video := &videos[index]
		if m.IsBlocked(*video) {
			continue
		}
		keyword := m.ComputeKeywordScore(song, *video)
		quality := m.ComputeQualityScore(video)
		official := m.ComputeOfficialScore(*video)
		popularity := m.ComputePopularityScore(*video)
		up := m.ComputeUPScore(*video)
		total := keyword*m.weights.Keyword/100 +
			quality*m.weights.Quality/100 +
			official*m.weights.Official/100 +
			popularity*m.weights.Popularity/100 + up
		results = append(results, model.MatchResult{
			Song:            song,
			Video:           video,
			Score:           total,
			KeywordScore:    keyword,
			QualityScore:    quality,
			OfficialScore:   official,
			PopularityScore: popularity,
			UploaderScore:   up,
			Matched:         keyword >= matchThreshold,
		})
	}
	sort.SliceStable(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})
	if topK < len(results) {
		results = results[:topK]
	}
	return results
}

// QueryPhases returns artist-aware queries followed by a title-only fallback.
func (m *Matcher) QueryPhases(song model.Song) []service.QueryPhase {
	title := strings.TrimSpace(song.SearchKeyword())
	artistQueries := make([]string, 0, 3)
	for _, query := range append([]string{song.SearchKeywordFull()}, song.AllSearchKeywords()...) {
		query = strings.TrimSpace(query)
		if query == "" || query == title || containsStringFold(artistQueries, query) {
			continue
		}
		artistQueries = append(artistQueries, query)
	}
	phases := make([]service.QueryPhase, 0, 2)
	if len(artistQueries) > 0 {
		phases = append(phases, service.QueryPhase{Queries: artistQueries})
	}
	if title != "" {
		phases = append(phases, service.QueryPhase{Queries: []string{title}})
	}
	return phases
}

// Rank scores the aggregate candidates collected by the service.
func (m *Matcher) Rank(song model.Song, videos []model.Video, topK int) []model.MatchResult {
	return m.Match(song, videos, topK)
}

// Decide preserves artist-evidence selection and applies the balanced
// title-only thresholds after the final query phase.
func (m *Matcher) Decide(song model.Song, ranked []model.MatchResult, finalPhase bool) service.MatchDecision {
	decision := service.MatchDecision{SelectedIndex: -1, Continue: !finalPhase}
	for index, candidate := range ranked {
		if candidate.Matched && candidate.Video != nil && hasArtistEvidence(song, *candidate.Video) {
			decision.SelectedIndex = index
			decision.Continue = false
			return decision
		}
	}
	if !finalPhase {
		decision.ReviewReason = model.ReviewArtistUnverified
		return decision
	}
	if len(ranked) == 0 || ranked[0].Video == nil {
		decision.ReviewReason = model.ReviewNoCandidates
		return decision
	}
	top := ranked[0]
	if top.KeywordScore < 70 || top.Score < 35 {
		decision.ReviewReason = model.ReviewWeakTitle
		return decision
	}
	runnerUp := 0.0
	if len(ranked) > 1 {
		runnerUp = ranked[1].Score
	}
	if top.Score-runnerUp < 5 {
		decision.ReviewReason = model.ReviewAmbiguous
		return decision
	}
	decision.SelectedIndex = 0
	return decision
}

// FuzzyContains reproduces the Python sliding-window 80-percent comparison.
func FuzzyContains(text, target string) bool {
	textRunes := []rune(cleanForMatch(text))
	targetRunes := []rune(cleanForMatch(target))
	if len(targetRunes) < 2 {
		return strings.Contains(string(textRunes), string(targetRunes))
	}
	window := len(targetRunes)
	for start := 0; start+window <= len(textRunes); start++ {
		matches := 0
		for index := range targetRunes {
			if textRunes[start+index] == targetRunes[index] {
				matches++
			}
		}
		if float64(matches)/float64(len(targetRunes)) >= 0.8 {
			return true
		}
	}
	return false
}

func cleanForMatch(value string) string {
	return strings.Map(func(r rune) rune {
		switch r {
		case '(', ')', '（', '）', '[', ']', '【', '】':
			return -1
		default:
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}
	}, value)
}

func wordSet(value string) map[string]struct{} {
	words := make(map[string]struct{})
	var word strings.Builder
	flush := func() {
		if word.Len() > 0 {
			words[word.String()] = struct{}{}
			word.Reset()
		}
	}
	for _, r := range value {
		if isWordRune(r) {
			word.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return words
}

func overlapCount(left, right map[string]struct{}) int {
	count := 0
	for word := range left {
		if _, ok := right[word]; ok {
			count++
		}
	}
	return count
}

func containsStandaloneRune(text string, target rune) bool {
	runes := []rune(text)
	for index, current := range runes {
		if current != target {
			continue
		}
		leftBoundary := index == 0 || !isWordRune(runes[index-1])
		rightBoundary := index == len(runes)-1 || !isWordRune(runes[index+1])
		if leftBoundary && rightBoundary {
			return true
		}
	}
	return false
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

func containsStringFold(values []string, target string) bool {
	for _, value := range values {
		if strings.EqualFold(value, target) {
			return true
		}
	}
	return false
}

func hasArtistEvidence(song model.Song, video model.Video) bool {
	artistKeywords := song.ArtistKeywords()
	if len(artistKeywords) == 0 {
		return false
	}
	evidence := normalizeEvidence(video.Title + " " + video.Uploader)
	for _, keyword := range artistKeywords {
		normalized := normalizeEvidence(keyword)
		if len([]rune(normalized)) >= 2 && strings.Contains(evidence, normalized) {
			return true
		}
	}
	return false
}

func normalizeEvidence(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

var _ service.MatchStrategy = (*Matcher)(nil)
