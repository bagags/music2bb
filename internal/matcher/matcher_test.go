package matcher

import (
	"math"
	"testing"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/service"
)

func resolvedMatcher(t *testing.T, base *Matcher, profile service.MatchProfile, weights *service.MatchWeights) *Matcher {
	t.Helper()
	strategy, err := base.ResolveMatchStrategy(profile, weights)
	if err != nil {
		t.Fatal(err)
	}
	resolved, ok := strategy.(*Matcher)
	if !ok {
		t.Fatalf("resolved strategy type = %T", strategy)
	}
	return resolved
}

func TestResolveWeightsAndValidation(t *testing.T) {
	t.Parallel()
	base := New(Options{})
	standard := resolvedMatcher(t, base, service.MatchProfileStandard, nil).Weights()
	classical := resolvedMatcher(t, base, service.MatchProfileClassical, nil).Weights()
	if !weightsClose(standard, service.MatchWeights{Title: .40, Artist: .25, Quality: .10, Official: .10, Popularity: .10, Uploader: .05}) {
		t.Fatalf("standard weights = %#v", standard)
	}
	if !weightsClose(classical, service.MatchWeights{Title: .55, Artist: .10, Quality: .10, Official: .10, Popularity: .10, Uploader: .05}) {
		t.Fatalf("classical weights = %#v", classical)
	}

	custom := service.MatchWeights{Title: 2, Artist: 1, Uploader: 1}
	normalized := resolvedMatcher(t, base, service.MatchProfileClassical, &custom).Weights()
	if normalized != (service.MatchWeights{Title: .5, Artist: .25, Uploader: .25}) {
		t.Fatalf("custom weights = %#v", normalized)
	}
	if custom != (service.MatchWeights{Title: 2, Artist: 1, Uploader: 1}) {
		t.Fatalf("custom weights were mutated: %#v", custom)
	}
	large := service.MatchWeights{Title: math.MaxFloat64, Artist: math.MaxFloat64}
	if got := resolvedMatcher(t, base, service.MatchProfileStandard, &large).Weights(); got.Title != .5 || got.Artist != .5 {
		t.Fatalf("large relative weights = %#v", got)
	}

	invalid := []service.MatchWeights{
		{},
		{Title: -1, Artist: 2},
		{Title: math.NaN()},
		{Title: math.Inf(1)},
	}
	for _, weights := range invalid {
		if _, err := base.ResolveMatchStrategy(service.MatchProfileStandard, &weights); err == nil {
			t.Fatalf("invalid weights accepted: %#v", weights)
		}
	}
	if _, err := base.ResolveMatchStrategy(service.MatchProfile("unknown"), nil); err == nil {
		t.Fatal("unknown profile accepted")
	}
}

func weightsClose(left, right service.MatchWeights) bool {
	leftValues := []float64{left.Title, left.Artist, left.Quality, left.Official, left.Popularity, left.Uploader}
	rightValues := []float64{right.Title, right.Artist, right.Quality, right.Official, right.Popularity, right.Uploader}
	for index := range leftValues {
		if math.Abs(leftValues[index]-rightValues[index]) > 1e-12 {
			return false
		}
	}
	return true
}

func TestComputeKeywordScore(t *testing.T) {
	t.Parallel()
	m := New(Options{})
	tests := []struct {
		name  string
		song  model.Song
		video model.Video
		want  float64
	}{
		{name: "empty song", song: model.Song{}, video: model.Video{Title: "anything"}, want: 0},
		{name: "name and artist", song: model.Song{Name: "Hello", Artist: "World"}, video: model.Video{Title: "Hello World MV"}, want: 100},
		{name: "name only", song: model.Song{Name: "Hello", Artist: "World"}, video: model.Video{Title: "Hello"}, want: 100},
		{name: "fuzzy", song: model.Song{Name: "abcdef"}, video: model.Video{Title: "abcxef"}, want: 80},
		{name: "unicode word overlap", song: model.Song{Name: "星 海"}, video: model.Video{Title: "星"}, want: 50},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := m.ComputeKeywordScore(tt.song, tt.video); got != tt.want {
				t.Fatalf("ComputeKeywordScore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestComponentScores(t *testing.T) {
	t.Parallel()
	m := New(Options{
		QualityKeywords:   []string{"MV", "mv", "flac"},
		WeightedUploaders: []string{"OfficialUploader"},
	})
	t.Run("quality case variants deduplicate", func(t *testing.T) {
		video := model.Video{Title: "Song MV"}
		if got := m.ComputeQualityScore(&video); got != 15 {
			t.Fatalf("quality = %v, want 15", got)
		}
		if video.QualityScore != 15 {
			t.Fatalf("video quality field = %v", video.QualityScore)
		}
	})
	t.Run("official is capped", func(t *testing.T) {
		video := model.Video{Title: "Official MV", Uploader: "Music Records"}
		if got := m.ComputeOfficialScore(video); got != 100 {
			t.Fatalf("official = %v, want 100", got)
		}
	})
	t.Run("official endpoint signal", func(t *testing.T) {
		want := 25.0 / 30 * 100
		if got := m.ComputeOfficialScore(model.Video{IsOfficial: true}); math.Abs(got-want) > 1e-12 {
			t.Fatalf("official = %v, want %v", got, want)
		}
	})
	t.Run("popularity strict rate boundary", func(t *testing.T) {
		video := model.Video{PlayCount: 100_000, FavoriteCount: 10_000}
		want := 45.0 / 70 * 100
		if got := m.ComputePopularityScore(video); math.Abs(got-want) > 1e-12 {
			t.Fatalf("popularity = %v, want %v", got, want)
		}
	})
	t.Run("popularity rate bonus", func(t *testing.T) {
		video := model.Video{PlayCount: 100, FavoriteCount: 20}
		want := (10 + math.Log10(20)*5 + 10) / 70 * 100
		if got := m.ComputePopularityScore(video); math.Abs(got-want) > 1e-12 {
			t.Fatalf("popularity = %v, want %v", got, want)
		}
	})
	t.Run("uploader exact and verified", func(t *testing.T) {
		video := model.Video{Uploader: "OfficialUploader", IsVerified: true}
		if got := m.ComputeUPScore(video); got != 100 {
			t.Fatalf("uploader = %v, want 100", got)
		}
	})
	t.Run("uploader remains case sensitive", func(t *testing.T) {
		if got := m.ComputeUPScore(model.Video{Uploader: "officialuploader"}); got != 0 {
			t.Fatalf("uploader = %v, want 0", got)
		}
	})
}

func TestComponentScoresStayWithinNormalizedBounds(t *testing.T) {
	t.Parallel()
	keywords := []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}
	m := New(Options{QualityKeywords: keywords, WeightedUploaders: []string{"Official Music"}})
	video := model.Video{
		Title:    "Official one two three four five six seven eight",
		Uploader: "Official Music", Description: "official", Tags: keywords,
		PlayCount: math.MaxInt64, FavoriteCount: math.MaxInt64, IsOfficial: true, IsVerified: true,
	}
	scores := []float64{
		m.ComputeTitleScore(model.Song{Name: "Official"}, video),
		m.ComputeArtistScore(model.Song{Artist: "Official Music"}, video),
		m.ComputeQualityScore(&video),
		m.ComputeOfficialScore(video),
		m.ComputePopularityScore(video),
		m.ComputeUPScore(video),
	}
	for index, score := range scores {
		if score < 0 || score > 100 {
			t.Fatalf("component %d = %v, want 0..100", index, score)
		}
	}
	if scores[2] != 100 {
		t.Fatalf("quality cap = %v, want 100", scores[2])
	}
}

func TestArtistScoreUsesIndividualCreditsAndAliases(t *testing.T) {
	t.Parallel()
	m := New(Options{})
	song := model.Song{Name: "Song", Artist: "初音ミク / Second Artist"}
	if got := m.ComputeArtistScore(song, model.Video{Title: "Song", Uploader: "Second Artist"}); got != 100 {
		t.Fatalf("second artist score = %v, want 100", got)
	}
	if got := m.ComputeArtistScore(song, model.Video{Title: "Song", Uploader: "Hatsune Miku"}); got != 100 {
		t.Fatalf("alias artist score = %v, want 100", got)
	}
	if got := m.ComputeArtistScore(model.Song{Artist: "abcdef"}, model.Video{Uploader: "abcxef"}); got != 80 {
		t.Fatalf("fuzzy artist score = %v, want 80", got)
	}
	if got := m.ComputeArtistScore(model.Song{Artist: "First Second Third"}, model.Video{Uploader: "First Second"}); math.Abs(got-200.0/3.0) > 1e-12 {
		t.Fatalf("artist token coverage = %v, want %v", got, 200.0/3.0)
	}
}

func TestNewClonesConfiguration(t *testing.T) {
	t.Parallel()
	blocks := []string{"cover"}
	quality := []string{"MV"}
	uploaders := []string{"Uploader"}
	m := New(Options{BlockKeywords: blocks, QualityKeywords: quality, WeightedUploaders: uploaders})
	blocks[0] = "different"
	quality[0] = "different"
	uploaders[0] = "different"

	video := model.Video{Title: "Song cover MV", Uploader: "Uploader"}
	if !m.IsBlocked(video) {
		t.Error("matcher observed mutation of source block slice")
	}
	video.Title = "Song MV"
	if got := m.ComputeQualityScore(&video); got != 15 {
		t.Errorf("quality = %v, want 15", got)
	}
	if got := m.ComputeUPScore(video); got != 60 {
		t.Errorf("uploader = %v, want 60", got)
	}
}

func TestBlocking(t *testing.T) {
	t.Parallel()
	m := New(Options{BlockKeywords: []string{"cover", "弹"}})
	tests := []struct {
		name  string
		title string
		want  bool
	}{
		{name: "multi rune substring", title: "Song COVER version", want: true},
		{name: "single rune standalone", title: "Song（弹）", want: true},
		{name: "single rune in word", title: "弹唱版本", want: false},
		{name: "clear", title: "Official song", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := m.IsBlocked(model.Video{Title: tt.title}); got != tt.want {
				t.Fatalf("IsBlocked() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMatchRankingThresholdAndTopK(t *testing.T) {
	t.Parallel()
	m := New(Options{
		BlockKeywords:     []string{"cover"},
		QualityKeywords:   []string{"official"},
		WeightedUploaders: []string{"boosted"},
	})
	song := model.Song{Name: "one two three four five"}
	videos := []model.Video{
		{BVID: "blocked", Title: "one two three four five cover"},
		{BVID: "threshold", Title: "one"},
		{BVID: "best", Title: "one two three four five official", Uploader: "boosted"},
		{BVID: "below", Title: "unrelated"},
	}
	results := m.Match(song, videos, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Video.BVID != "best" {
		t.Errorf("first result = %q, want best", results[0].Video.BVID)
	}
	if want := 51.0 + 1.0/6.0; math.Abs(results[0].Score-want) > 1e-12 {
		t.Errorf("best score = %v, want %v", results[0].Score, want)
	}
	if results[1].Video.BVID != "threshold" {
		t.Fatalf("second result = %q, want threshold", results[1].Video.BVID)
	}
	if results[1].Score != 8 || results[1].TitleScore != 20 || results[1].KeywordScore != 20 || !results[1].Matched {
		t.Errorf("threshold result = total %v keyword %v matched %v", results[1].Score, results[1].KeywordScore, results[1].Matched)
	}
	allResults := m.Match(song, videos, len(videos))
	if len(allResults) != 3 {
		t.Fatalf("all results length = %d, want 3 unblocked candidates", len(allResults))
	}
	if got := allResults[2]; got.Video.BVID != "below" || got.Matched {
		t.Errorf("unmatched result = video %q matched %v", got.Video.BVID, got.Matched)
	}
	if got := m.Match(song, videos, 0); len(got) != 0 {
		t.Errorf("topK zero returned %d results", len(got))
	}
}

func TestMatchSortIsStableForTies(t *testing.T) {
	t.Parallel()
	m := New(Options{})
	videos := []model.Video{{BVID: "first", Title: "song"}, {BVID: "second", Title: "song"}}
	results := m.Match(model.Song{Name: "song"}, videos, 2)
	if results[0].Video.BVID != "first" || results[1].Video.BVID != "second" {
		t.Fatalf("tie order changed: %s, %s", results[0].Video.BVID, results[1].Video.BVID)
	}
}

func TestProfileRankingStandardPrefersArtistAndClassicalPrefersTitle(t *testing.T) {
	t.Parallel()
	base := New(Options{})
	song := model.Song{Name: "Moon Light Sonata", Artist: "Right Artist"}
	videos := []model.Video{
		{BVID: "exact-title", Title: "Moon Light Sonata", Uploader: "Other Performer"},
		{BVID: "artist", Title: "Moon Light", Uploader: "Right Artist"},
	}
	standard := resolvedMatcher(t, base, service.MatchProfileStandard, nil).Match(song, videos, len(videos))
	classical := resolvedMatcher(t, base, service.MatchProfileClassical, nil).Match(song, videos, len(videos))
	if standard[0].Video.BVID != "artist" {
		t.Fatalf("standard ranking = %s first, want artist", standard[0].Video.BVID)
	}
	if classical[0].Video.BVID != "exact-title" {
		t.Fatalf("classical ranking = %s first, want exact-title", classical[0].Video.BVID)
	}
	for _, result := range append(standard, classical...) {
		if result.TitleScore != result.KeywordScore {
			t.Fatalf("keyword alias = %v, title = %v", result.KeywordScore, result.TitleScore)
		}
	}
}

func TestClassicalDecisionWaitsForFallbackAndReviewsCloseRecordings(t *testing.T) {
	t.Parallel()
	classical := resolvedMatcher(t, New(Options{}), service.MatchProfileClassical, nil)
	video := func(id string) *model.Video { return &model.Video{BVID: id, Title: "Moon Light Sonata"} }
	strong := model.MatchResult{Video: video("strong"), Matched: true, TitleScore: 100, KeywordScore: 100, ArtistScore: 100, Score: 55}
	if got := classical.Decide(model.Song{Name: "Moon Light Sonata"}, []model.MatchResult{strong}, false); !got.Continue || got.SelectedIndex != -1 {
		t.Fatalf("classical artist phase decision = %#v", got)
	}
	if got := classical.Decide(model.Song{Name: "Moon Light Sonata"}, []model.MatchResult{strong}, true); got.SelectedIndex != 0 || got.Continue {
		t.Fatalf("classical strong fallback decision = %#v", got)
	}
	close := []model.MatchResult{
		strong,
		{Video: video("runner"), Matched: true, TitleScore: 100, KeywordScore: 100, Score: 52},
	}
	if got := classical.Decide(model.Song{Name: "Moon Light Sonata"}, close, true); got.ReviewReason != model.ReviewAmbiguous || got.SelectedIndex != -1 {
		t.Fatalf("classical close-recording decision = %#v", got)
	}
}

func TestBalancedQueryPhases(t *testing.T) {
	t.Parallel()
	strategy := New(Options{})
	phases := strategy.QueryPhases(model.Song{Name: "Song", Artist: "初音ミク"})
	if len(phases) != 2 {
		t.Fatalf("phases = %#v, want artist and title phases", phases)
	}
	if got := phases[len(phases)-1].Queries; len(got) != 1 || got[0] != "Song" {
		t.Fatalf("title fallback = %#v", got)
	}
	for _, query := range phases[0].Queries {
		if query == "Song" {
			t.Fatalf("title-only query leaked into artist phase: %#v", phases)
		}
	}
}

func TestBalancedDecision(t *testing.T) {
	t.Parallel()
	strategy := New(Options{})
	video := func(bvid, title, uploader string) *model.Video {
		return &model.Video{BVID: bvid, Title: title, Uploader: uploader}
	}
	tests := []struct {
		name   string
		song   model.Song
		ranked []model.MatchResult
		final  bool
		index  int
		cont   bool
		reason model.ReviewReason
	}{
		{
			name: "artist evidence remains safe", song: model.Song{Name: "Shared", Artist: "Right Artist"},
			ranked: []model.MatchResult{{Video: video("artist", "Shared - Right Artist", "music"), Matched: true, TitleScore: 70, KeywordScore: 70, ArtistScore: 100, Score: 53}},
			index:  0,
		},
		{
			name: "strong title-only winner", song: model.Song{Name: "Mystery", Artist: "Unknown"},
			ranked: []model.MatchResult{{Video: video("top", "Mystery", "someone"), Matched: true, KeywordScore: 100, Score: 40}},
			final:  true, index: 0,
		},
		{
			name: "missing runner-up is zero", song: model.Song{Name: "Mystery"},
			ranked: []model.MatchResult{{Video: video("top", "Mystery", "someone"), Matched: true, KeywordScore: 70, Score: 35}},
			final:  true, index: 0,
		},
		{
			name: "close results are ambiguous", song: model.Song{Name: "Mystery"},
			ranked: []model.MatchResult{
				{Video: video("top", "Mystery", "one"), Matched: true, KeywordScore: 100, Score: 40},
				{Video: video("runner", "Mystery", "two"), Matched: true, KeywordScore: 100, Score: 36},
			},
			final: true, index: -1, reason: model.ReviewAmbiguous,
		},
		{
			name: "weak keyword", song: model.Song{Name: "Mystery"},
			ranked: []model.MatchResult{{Video: video("weak", "Other", "one"), KeywordScore: 69, Score: 60}},
			final:  true, index: -1, reason: model.ReviewWeakTitle,
		},
		{
			name: "weak total", song: model.Song{Name: "Mystery"},
			ranked: []model.MatchResult{{Video: video("weak", "Mystery", "one"), KeywordScore: 100, Score: 34.9}},
			final:  true, index: -1, reason: model.ReviewWeakTitle,
		},
		{
			name: "empty final", song: model.Song{Name: "Mystery"}, final: true,
			index: -1, reason: model.ReviewNoCandidates,
		},
		{
			name: "continue after artist phase", song: model.Song{Name: "Mystery", Artist: "Unknown"},
			ranked: []model.MatchResult{{Video: video("candidate", "Mystery", "someone"), Matched: true, KeywordScore: 100, Score: 40}},
			index:  -1, cont: true, reason: model.ReviewArtistUnverified,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := strategy.Decide(tt.song, tt.ranked, tt.final)
			if got.SelectedIndex != tt.index || got.Continue != tt.cont || got.ReviewReason != tt.reason {
				t.Fatalf("Decide() = %#v, want index=%d continue=%v reason=%q", got, tt.index, tt.cont, tt.reason)
			}
		})
	}
}

func TestFuzzyContains(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		text   string
		target string
		want   bool
	}{
		{name: "exact after punctuation cleanup", text: "a（b） c", target: "abc", want: true},
		{name: "eighty percent", text: "abcxef", target: "abcdef", want: true},
		{name: "below threshold", text: "abxxef", target: "abcdef", want: false},
		{name: "single rune", text: "星海", target: "星", want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := FuzzyContains(tt.text, tt.target); got != tt.want {
				t.Fatalf("FuzzyContains() = %v, want %v", got, tt.want)
			}
		})
	}
}
