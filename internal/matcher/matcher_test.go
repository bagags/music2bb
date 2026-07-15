package matcher

import (
	"math"
	"testing"

	"github.com/bagags/music2bb-go/internal/model"
)

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
		{name: "name only", song: model.Song{Name: "Hello", Artist: "World"}, video: model.Video{Title: "Hello"}, want: 70},
		{name: "fuzzy", song: model.Song{Name: "abcdef"}, video: model.Video{Title: "abcxef"}, want: 50},
		{name: "unicode word overlap", song: model.Song{Name: "星 海"}, video: model.Video{Title: "星"}, want: 40},
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
	t.Run("quality case variants stack", func(t *testing.T) {
		video := model.Video{Title: "Song MV"}
		if got := m.ComputeQualityScore(&video); got != 30 {
			t.Fatalf("quality = %v, want 30", got)
		}
		if video.QualityScore != 30 {
			t.Fatalf("video quality field = %v", video.QualityScore)
		}
	})
	t.Run("official is capped", func(t *testing.T) {
		video := model.Video{Title: "Official MV", Uploader: "Music Records"}
		if got := m.ComputeOfficialScore(video); got != 30 {
			t.Fatalf("official = %v, want 30", got)
		}
	})
	t.Run("official endpoint signal", func(t *testing.T) {
		if got := m.ComputeOfficialScore(model.Video{IsOfficial: true}); got != 25 {
			t.Fatalf("official = %v, want 25", got)
		}
	})
	t.Run("popularity strict rate boundary", func(t *testing.T) {
		video := model.Video{PlayCount: 100_000, FavoriteCount: 10_000}
		if got := m.ComputePopularityScore(video); got != 45 {
			t.Fatalf("popularity = %v, want 45", got)
		}
	})
	t.Run("popularity rate bonus", func(t *testing.T) {
		video := model.Video{PlayCount: 100, FavoriteCount: 20}
		want := 10 + math.Log10(20)*5 + 10
		if got := m.ComputePopularityScore(video); math.Abs(got-want) > 1e-12 {
			t.Fatalf("popularity = %v, want %v", got, want)
		}
	})
	t.Run("uploader exact and verified", func(t *testing.T) {
		video := model.Video{Uploader: "OfficialUploader", IsVerified: true}
		if got := m.ComputeUPScore(video); got != 50 {
			t.Fatalf("uploader = %v, want 50", got)
		}
	})
	t.Run("uploader remains case sensitive", func(t *testing.T) {
		if got := m.ComputeUPScore(model.Video{Uploader: "officialuploader"}); got != 0 {
			t.Fatalf("uploader = %v, want 0", got)
		}
	})
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
	if got := m.ComputeUPScore(video); got != 30 {
		t.Errorf("uploader = %v, want 30", got)
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
	song := model.Song{Name: "one two three four"}
	videos := []model.Video{
		{BVID: "blocked", Title: "one two three four cover"},
		{BVID: "threshold", Title: "one"},
		{BVID: "best", Title: "one two three four official", Uploader: "boosted"},
		{BVID: "below", Title: "unrelated"},
	}
	results := m.Match(song, videos, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].Video.BVID != "best" {
		t.Errorf("first result = %q, want best", results[0].Video.BVID)
	}
	if results[0].Score != 77.75 {
		t.Errorf("best score = %v, want 77.75", results[0].Score)
	}
	if results[1].Video.BVID != "threshold" {
		t.Fatalf("second result = %q, want threshold", results[1].Video.BVID)
	}
	if results[1].Score != 8 || results[1].KeywordScore != 20 || !results[1].Matched {
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
			ranked: []model.MatchResult{{Video: video("artist", "Shared - Right Artist", "music"), Matched: true, KeywordScore: 70, Score: 28}},
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
