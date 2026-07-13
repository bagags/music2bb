package parity_test

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"

	"github.com/gguage/music-to-bb/internal/kugou"
	"github.com/gguage/music-to-bb/internal/matcher"
	"github.com/gguage/music-to-bb/internal/model"
)

type goldenFixture struct {
	Schema        int                 `json:"schema"`
	Normalization []normalizationCase `json:"normalization"`
	Matcher       matcherFixture      `json:"matcher"`
	Cleanup       cleanupFixture      `json:"cleanup"`
}

type songFixture struct {
	Name   string `json:"name"`
	Artist string `json:"artist"`
}

func (song songFixture) model() model.Song {
	return model.Song{Name: song.Name, Artist: song.Artist}
}

type normalizationCase struct {
	Input             songFixture `json:"input"`
	CleanName         string      `json:"clean_name"`
	CleanArtist       string      `json:"clean_artist"`
	SearchKeyword     string      `json:"search_keyword"`
	SearchKeywordFull string      `json:"search_keyword_full"`
	AllSearchKeywords []string    `json:"all_search_keywords"`
}

type videoFixture struct {
	BVID          string `json:"bvid"`
	Title         string `json:"title"`
	Uploader      string `json:"uploader"`
	PlayCount     int64  `json:"play_count"`
	FavoriteCount int64  `json:"favorite_count"`
	IsOfficial    bool   `json:"is_official"`
	IsVerified    bool   `json:"is_verified"`
}

func (video videoFixture) model() model.Video {
	return model.Video{
		BVID: video.BVID, Title: video.Title, Uploader: video.Uploader,
		PlayCount: video.PlayCount, FavoriteCount: video.FavoriteCount,
		IsOfficial: video.IsOfficial, IsVerified: video.IsVerified,
	}
}

type scoreFixture struct {
	BVID            string  `json:"bvid"`
	Blocked         bool    `json:"blocked"`
	Score           float64 `json:"score"`
	KeywordScore    float64 `json:"keyword_score"`
	QualityScore    float64 `json:"quality_score"`
	OfficialScore   float64 `json:"official_score"`
	PopularityScore float64 `json:"popularity_score"`
	UPScore         float64 `json:"up_score"`
	Matched         bool    `json:"matched"`
}

type matcherFixture struct {
	BlockKeywords     []string       `json:"block_keywords"`
	QualityKeywords   []string       `json:"quality_keywords"`
	WeightedUploaders []string       `json:"weighted_uploaders"`
	Song              songFixture    `json:"song"`
	Videos            []videoFixture `json:"videos"`
	Components        []scoreFixture `json:"components"`
	Ranking           []scoreFixture `json:"ranking"`
}

type cleanupFixture struct {
	Input  []songFixture `json:"input"`
	Output []songFixture `json:"output"`
}

func TestGoMatchesCapturedPythonGolden(t *testing.T) {
	fixture := loadFixture(t)
	if fixture.Schema != 1 {
		t.Fatalf("fixture schema = %d, want 1", fixture.Schema)
	}
	t.Run("normalization and search variants", func(t *testing.T) {
		for _, test := range fixture.Normalization {
			song := test.Input.model()
			if got := song.CleanName(); got != test.CleanName {
				t.Errorf("CleanName(%q) = %q, want %q", song.Name, got, test.CleanName)
			}
			if got := song.CleanArtist(); got != test.CleanArtist {
				t.Errorf("CleanArtist(%q) = %q, want %q", song.Artist, got, test.CleanArtist)
			}
			if got := song.SearchKeyword(); got != test.SearchKeyword {
				t.Errorf("SearchKeyword(%q) = %q, want %q", song.Name, got, test.SearchKeyword)
			}
			if got := song.SearchKeywordFull(); got != test.SearchKeywordFull {
				t.Errorf("SearchKeywordFull(%q) = %q, want %q", song.Name, got, test.SearchKeywordFull)
			}
			if got := song.AllSearchKeywords(); !reflect.DeepEqual(got, test.AllSearchKeywords) {
				t.Errorf("AllSearchKeywords(%q) = %#v, want %#v", song.Name, got, test.AllSearchKeywords)
			}
		}
	})

	t.Run("filtering scores ranking and threshold", func(t *testing.T) {
		options := matcher.Options{
			BlockKeywords: fixture.Matcher.BlockKeywords, QualityKeywords: fixture.Matcher.QualityKeywords,
			WeightedUploaders: fixture.Matcher.WeightedUploaders,
		}
		scorer := matcher.New(options)
		videos := make([]model.Video, len(fixture.Matcher.Videos))
		byID := make(map[string]model.Video, len(videos))
		for index, video := range fixture.Matcher.Videos {
			videos[index] = video.model()
			byID[video.BVID] = videos[index]
		}
		song := fixture.Matcher.Song.model()
		for _, want := range fixture.Matcher.Components {
			video := byID[want.BVID]
			if got := scorer.IsBlocked(video); got != want.Blocked {
				t.Errorf("IsBlocked(%s) = %v, want %v", want.BVID, got, want.Blocked)
			}
			if want.Blocked {
				continue
			}
			assertNear(t, want.BVID+" keyword", scorer.ComputeKeywordScore(song, video), want.KeywordScore)
			assertNear(t, want.BVID+" quality", scorer.ComputeQualityScore(&video), want.QualityScore)
			assertNear(t, want.BVID+" official", scorer.ComputeOfficialScore(video), want.OfficialScore)
			assertNear(t, want.BVID+" popularity", scorer.ComputePopularityScore(video), want.PopularityScore)
			assertNear(t, want.BVID+" uploader", scorer.ComputeUPScore(video), want.UPScore)
		}
		got := scorer.Match(song, videos, len(videos))
		if len(got) != len(fixture.Matcher.Ranking) {
			t.Fatalf("ranking length = %d, want %d", len(got), len(fixture.Matcher.Ranking))
		}
		for index, want := range fixture.Matcher.Ranking {
			result := got[index]
			if result.Video == nil || result.Video.BVID != want.BVID {
				t.Fatalf("ranking[%d] video = %#v, want %s", index, result.Video, want.BVID)
			}
			assertNear(t, want.BVID+" total", result.Score, want.Score)
			assertNear(t, want.BVID+" keyword", result.KeywordScore, want.KeywordScore)
			assertNear(t, want.BVID+" quality", result.QualityScore, want.QualityScore)
			assertNear(t, want.BVID+" official", result.OfficialScore, want.OfficialScore)
			assertNear(t, want.BVID+" popularity", result.PopularityScore, want.PopularityScore)
			assertNear(t, want.BVID+" uploader", result.UploaderScore, want.UPScore)
			if result.Matched != want.Matched {
				t.Errorf("%s matched = %v, want %v", want.BVID, result.Matched, want.Matched)
			}
		}
	})

	t.Run("playlist cleanup", func(t *testing.T) {
		input := make([]model.Song, len(fixture.Cleanup.Input))
		want := make([]model.Song, len(fixture.Cleanup.Output))
		for index, song := range fixture.Cleanup.Input {
			input[index] = song.model()
		}
		for index, song := range fixture.Cleanup.Output {
			want[index] = song.model()
		}
		if got := kugou.CleanupSongs(input); !reflect.DeepEqual(got, want) {
			t.Fatalf("CleanupSongs = %#v, want %#v", got, want)
		}
	})
}

func loadFixture(t *testing.T) goldenFixture {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve test source path")
	}
	payload, err := os.ReadFile(filepath.Join(filepath.Dir(source), "..", "..", "testdata", "python_golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture goldenFixture
	if err := json.Unmarshal(payload, &fixture); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func assertNear(t *testing.T, label string, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("%s = %.15g, want %.15g", label, got, want)
	}
}
