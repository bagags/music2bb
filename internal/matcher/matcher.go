// Package matcher ranks Bilibili candidates using configurable scoring
// components. Matcher instances own immutable configuration and are safe for
// concurrent use when callers do not share mutable Video values.
package matcher

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	catalogue "github.com/bagags/classical-catalogue-id-parser"
	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/service"
)

const (
	qualityKeywordScore = 15.0
	weightedUPScore     = 30.0
	verifiedUPScore     = 20.0
	matchThreshold      = 20.0
	officialMaximum     = 30.0
	popularityMaximum   = 70.0
	uploaderMaximum     = 50.0
)

var (
	officialKeywords   = []string{"官方", "official", "Official", "OFFICIAL", "官方MV", "OfficialMV", "官方mv"}
	officialUPPatterns = []string{"官方", "Official", "Music", "Records", "Entertainment", "音乐", "唱片", "工作室"}
)

// Options contains per-engine matcher configuration. Slices are cloned by New.
type Options struct {
	BlockKeywords     []string
	QualityKeywords   []string
	WeightedUploaders []string
}

// Matcher is an immutable scorer with no global configuration.
type Matcher struct {
	profile           service.MatchProfile
	weights           service.MatchWeights
	blockKeywords     []string
	qualityKeywords   []string
	weightedUploaders []string
}

// New constructs an independent standard-profile matcher and configuration
// factory.
func New(options Options) *Matcher {
	weights, _ := normalizeWeights(standardWeights())
	return &Matcher{
		profile:           service.MatchProfileStandard,
		weights:           weights,
		blockKeywords:     append([]string(nil), options.BlockKeywords...),
		qualityKeywords:   append([]string(nil), options.QualityKeywords...),
		weightedUploaders: append([]string(nil), options.WeightedUploaders...),
	}
}

// Weights returns a copy of the active weights.
func (m *Matcher) Weights() service.MatchWeights {
	return m.weights
}

// ResolveMatchStrategy creates an immutable scorer for one match/search call.
func (m *Matcher) ResolveMatchStrategy(profile service.MatchProfile, custom *service.MatchWeights) (service.MatchStrategy, error) {
	weights := standardWeights()
	switch profile {
	case service.MatchProfileStandard:
	case service.MatchProfileClassical:
		weights = classicalWeights()
	default:
		return nil, fmt.Errorf("unknown match profile %q", profile)
	}
	if custom != nil {
		weights = *custom
	}
	normalized, err := normalizeWeights(weights)
	if err != nil {
		return nil, err
	}
	return &Matcher{
		profile: profile, weights: normalized,
		blockKeywords: m.blockKeywords, qualityKeywords: m.qualityKeywords, weightedUploaders: m.weightedUploaders,
	}, nil
}

func standardWeights() service.MatchWeights {
	return service.MatchWeights{Title: 40, Artist: 25, Quality: 10, Official: 10, Popularity: 10, Uploader: 5}
}

func classicalWeights() service.MatchWeights {
	return service.MatchWeights{Title: 55, Artist: 10, Quality: 10, Official: 10, Popularity: 10, Uploader: 5}
}

func normalizeWeights(weights service.MatchWeights) (service.MatchWeights, error) {
	values := []*float64{&weights.Title, &weights.Artist, &weights.Quality, &weights.Official, &weights.Popularity, &weights.Uploader}
	var maximum float64
	for _, value := range values {
		if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
			return service.MatchWeights{}, fmt.Errorf("match weights must be finite and non-negative")
		}
		maximum = math.Max(maximum, *value)
	}
	if maximum == 0 {
		return service.MatchWeights{}, fmt.Errorf("at least one match weight must be positive")
	}
	var sum float64
	for _, value := range values {
		*value /= maximum
		sum += *value
	}
	for _, value := range values {
		*value /= sum
	}
	return weights, nil
}

// ComputeTitleScore calculates song/title similarity on the 0-100 scale.
func (m *Matcher) ComputeTitleScore(song model.Song, video model.Video) float64 {
	if m.profile == service.MatchProfileClassical && catalogue.SharedReference(song.Name, video.Title) {
		return 100
	}
	return similarityScore(song.Name, video.Title)
}

// ComputeKeywordScore is retained as an internal compatibility alias.
func (m *Matcher) ComputeKeywordScore(song model.Song, video model.Video) float64 {
	return m.ComputeTitleScore(song, video)
}

// ComputeArtistScore returns the strongest match for any individual source
// artist credit or known alias against the candidate title and uploader.
func (m *Matcher) ComputeArtistScore(song model.Song, video model.Video) float64 {
	evidence := video.Title + " " + video.Uploader
	var best float64
	for _, artist := range song.ArtistKeywords() {
		best = math.Max(best, similarityScore(artist, evidence))
	}
	return best
}

// ComputeQualityScore adds 15 for every configured keyword present in the
// title, description, or tags. Case-insensitive duplicates count once.
func (m *Matcher) ComputeQualityScore(video *model.Video) float64 {
	if video == nil {
		return 0
	}
	text := strings.ToLower(video.Title + " " + video.Description + " " + strings.Join(video.Tags, " "))
	var score float64
	seen := make(map[string]struct{}, len(m.qualityKeywords))
	for _, keyword := range m.qualityKeywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		if _, ok := seen[keyword]; ok {
			continue
		}
		seen[keyword] = struct{}{}
		if strings.Contains(text, keyword) {
			score += qualityKeywordScore
		}
	}
	score = math.Min(score, 100)
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
	return normalizeComponent(score, officialMaximum)
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
	return normalizeComponent(score, popularityMaximum)
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
	return normalizeComponent(score, uploaderMaximum)
}

func normalizeComponent(score, maximum float64) float64 {
	return math.Min(math.Max(score, 0), maximum) / maximum * 100
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
// and retains at most topK. A candidate is valid when title score is at least
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
		title := m.ComputeTitleScore(song, *video)
		artist := m.ComputeArtistScore(song, *video)
		quality := m.ComputeQualityScore(video)
		official := m.ComputeOfficialScore(*video)
		popularity := m.ComputePopularityScore(*video)
		up := m.ComputeUPScore(*video)
		total := title*m.weights.Title +
			artist*m.weights.Artist +
			quality*m.weights.Quality +
			official*m.weights.Official +
			popularity*m.weights.Popularity +
			up*m.weights.Uploader
		results = append(results, model.MatchResult{
			Song:            song,
			Video:           video,
			Score:           total,
			TitleScore:      title,
			ArtistScore:     artist,
			KeywordScore:    title,
			QualityScore:    quality,
			OfficialScore:   official,
			PopularityScore: popularity,
			UploaderScore:   up,
			Matched:         title >= matchThreshold,
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

// QueryPhases returns profile-aware query expansion order. The service walks
// the flattened queries page-width-first and re-scores after every response.
func (m *Matcher) QueryPhases(song model.Song) []service.QueryPhase {
	title := strings.TrimSpace(song.SearchKeyword())
	full := strings.TrimSpace(song.SearchKeywordFull())
	aliases := make([]string, 0, 3)
	for _, query := range song.AllSearchKeywords() {
		query = strings.TrimSpace(query)
		if query == "" || query == title || strings.EqualFold(query, full) || containsStringFold(aliases, query) {
			continue
		}
		aliases = append(aliases, query)
	}
	ordered := make([]string, 0, 2+len(aliases))
	if m.profile == service.MatchProfileClassical {
		ordered = append(ordered, title, full)
	} else {
		ordered = append(ordered, full, title)
	}
	ordered = append(ordered, aliases...)
	phases := make([]service.QueryPhase, 0, len(ordered))
	seen := make([]string, 0, len(ordered))
	for _, query := range ordered {
		query = strings.TrimSpace(query)
		if query == "" || containsStringFold(seen, query) {
			continue
		}
		seen = append(seen, query)
		phases = append(phases, service.QueryPhase{Queries: []string{query}})
	}
	return phases
}

// Rank scores the aggregate candidates collected by the service.
func (m *Matcher) Rank(song model.Song, videos []model.Video, topK int) []model.MatchResult {
	return m.Match(song, videos, topK)
}

// Decide applies profile-specific early-selection and final thresholds.
func (m *Matcher) Decide(_ model.Song, ranked []model.MatchResult, finalPhase bool) service.MatchDecision {
	decision := service.MatchDecision{SelectedIndex: -1, Continue: !finalPhase}
	if m.profile == service.MatchProfileStandard && !finalPhase {
		for index, candidate := range ranked {
			if candidate.Matched && candidate.Video != nil && candidate.ArtistScore == 100 {
				decision.SelectedIndex = index
				decision.Continue = false
				return decision
			}
		}
	}
	if m.profile == service.MatchProfileClassical && meetsCandidateThreshold(ranked, 45) {
		if candidateHasMargin(ranked) {
			decision.SelectedIndex = 0
			decision.Continue = false
			return decision
		}
		if !finalPhase {
			decision.ReviewReason = model.ReviewAmbiguous
			return decision
		}
	}
	if !finalPhase {
		if m.profile == service.MatchProfileStandard {
			decision.ReviewReason = model.ReviewArtistUnverified
		}
		return decision
	}
	if len(ranked) == 0 || ranked[0].Video == nil {
		decision.ReviewReason = model.ReviewNoCandidates
		return decision
	}
	minimumTotal := 35.0
	if m.profile == service.MatchProfileClassical {
		minimumTotal = 45
	}
	if !meetsCandidateThreshold(ranked, minimumTotal) {
		decision.ReviewReason = model.ReviewWeakTitle
		return decision
	}
	if !candidateHasMargin(ranked) {
		decision.ReviewReason = model.ReviewAmbiguous
		return decision
	}
	decision.SelectedIndex = 0
	return decision
}

func meetsCandidateThreshold(ranked []model.MatchResult, minimumTotal float64) bool {
	if len(ranked) == 0 || ranked[0].Video == nil {
		return false
	}
	top := ranked[0]
	if effectiveTitleScore(top) < 70 || top.Score < minimumTotal {
		return false
	}
	return effectiveTitleScore(top) >= 70 && top.Score >= minimumTotal
}

func candidateHasMargin(ranked []model.MatchResult) bool {
	if len(ranked) == 0 {
		return false
	}
	runnerUp := 0.0
	if len(ranked) > 1 {
		runnerUp = ranked[1].Score
	}
	return ranked[0].Score-runnerUp >= 5
}

// FuzzyContains applies a sliding-window 80-percent comparison.
func FuzzyContains(text, target string) bool {
	textRunes := []rune(normalizeEvidence(text))
	targetRunes := []rune(normalizeEvidence(target))
	if len(targetRunes) == 0 {
		return false
	}
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

func similarityScore(target, evidence string) float64 {
	target = strings.TrimSpace(target)
	if target == "" {
		return 0
	}
	normalizedTarget := normalizeEvidence(target)
	normalizedEvidence := normalizeEvidence(evidence)
	if normalizedTarget != "" && strings.Contains(normalizedEvidence, normalizedTarget) {
		return 100
	}
	if FuzzyContains(evidence, target) {
		return 80
	}
	targetWords := wordSet(strings.ToLower(target))
	if len(targetWords) == 0 {
		return 0
	}
	evidenceWords := wordSet(strings.ToLower(evidence))
	return float64(overlapCount(targetWords, evidenceWords)) / float64(len(targetWords)) * 100
}

func effectiveTitleScore(result model.MatchResult) float64 {
	if result.TitleScore != 0 || result.KeywordScore == 0 {
		return result.TitleScore
	}
	return result.KeywordScore
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

func normalizeEvidence(value string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, value)
}

var _ service.MatchStrategy = (*Matcher)(nil)
var _ service.MatchStrategyResolver = (*Matcher)(nil)
