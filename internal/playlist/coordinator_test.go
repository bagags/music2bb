package playlist

import (
	"context"
	"errors"
	"net/url"
	"reflect"
	"testing"

	"github.com/gguage/music-to-bb/internal/model"
)

type fakePlaylistExtractor struct {
	name   string
	result RawResult
	err    error
	calls  int
}

func (e *fakePlaylistExtractor) Name() string { return e.name }
func (e *fakePlaylistExtractor) ExtractPlaylist(context.Context, Source) (RawResult, error) {
	e.calls++
	return e.result, e.err
}

type fakeBrowserExtractor struct {
	available       bool
	availabilityErr error
	result          RawResult
	err             error
	availableCalls  int
	extractCalls    int
}

func (e *fakeBrowserExtractor) Available(context.Context) (bool, error) {
	e.availableCalls++
	return e.available, e.availabilityErr
}

func (e *fakeBrowserExtractor) ExtractPlaylist(context.Context, Source) (RawResult, error) {
	e.extractCalls++
	return e.result, e.err
}

type cleanupNormalizer struct{}

func (cleanupNormalizer) Name() string { return "cleanup" }
func (cleanupNormalizer) NormalizeSongs(songs []model.Song) []model.Song {
	result := make([]model.Song, 0, len(songs))
	for _, song := range songs {
		if song.Name != "phantom" {
			result = append(result, song)
		}
	}
	return result
}

type suffixNormalizer struct{ calls *int }

func (suffixNormalizer) Name() string { return "suffix" }
func (n suffixNormalizer) NormalizeSongs(songs []model.Song) []model.Song {
	if n.calls != nil {
		*n.calls++
	}
	result := append([]model.Song(nil), songs...)
	for index := range result {
		result[index].Name += "!"
	}
	return result
}

func candidate(name, artist, hash string) TrackCandidate {
	return TrackCandidate{Fields: map[string]string{"name": name, "artist": artist}, Hash: hash}
}

func coordinatorForTest(t *testing.T, extractor *fakePlaylistExtractor, browser *fakeBrowserExtractor, normalizers ...SongNormalizer) *Coordinator {
	t.Helper()
	extractors := []PlaylistExtractor(nil)
	if extractor != nil {
		extractors = []PlaylistExtractor{extractor}
	}
	return coordinatorWithExtractors(t, extractors, browser, normalizers...)
}

func coordinatorWithExtractors(t *testing.T, extractors []PlaylistExtractor, browser *fakeBrowserExtractor, normalizers ...SongNormalizer) *Coordinator {
	t.Helper()
	identification, err := NewIdentificationRegistry(IdentificationRegistration{
		ProviderID: "known",
		Identifier: IdentifierFunc(func(value *url.URL) bool { return value.Hostname() == "known.test" }),
	})
	if err != nil {
		t.Fatal(err)
	}
	provider := ProviderOptimizations{PlaylistExtractors: extractors, SongNormalizers: normalizers}
	optimizations, err := NewOptimizationRegistry(OptimizationRegistration{ProviderID: "known", Optimizations: provider})
	if err != nil {
		t.Fatal(err)
	}
	return NewCoordinator(identification, optimizations, browser)
}

func TestCoordinatorOptimizedCompleteSkipsBrowserByPolicy(t *testing.T) {
	for _, policy := range []BrowserPolicy{BrowserAuto, BrowserNever} {
		t.Run(string(policy), func(t *testing.T) {
			extractor := &fakePlaylistExtractor{name: "direct", result: RawResult{Tracks: []TrackCandidate{candidate("Direct", "Artist", "")}, ExpectedTotal: 1}}
			browser := &fakeBrowserExtractor{available: true, result: RawResult{Tracks: []TrackCandidate{candidate("Browser", "Artist", "")}}}
			result, err := coordinatorForTest(t, extractor, browser).ParsePlaylist(context.Background(), "https://known.test/list", policy)
			if err != nil || len(result.Songs) != 1 || result.Songs[0].Name != "Direct" {
				t.Fatalf("ParsePlaylist = %#v, %v", result, err)
			}
			if browser.availableCalls != 0 || browser.extractCalls != 0 {
				t.Fatalf("browser calls = status %d extract %d", browser.availableCalls, browser.extractCalls)
			}
		})
	}
}

func TestCoordinatorAppliesNormalizerChainOnceToReturnedSongs(t *testing.T) {
	calls := 0
	extractor := &fakePlaylistExtractor{name: "direct", result: RawResult{Tracks: []TrackCandidate{candidate("Direct", "Artist", "")}, ExpectedTotal: 2}}
	browser := &fakeBrowserExtractor{available: true, result: RawResult{Tracks: []TrackCandidate{candidate("Browser", "Artist", "")}}}
	result, err := coordinatorForTest(t, extractor, browser, suffixNormalizer{calls: &calls}).ParsePlaylist(context.Background(), "https://known.test/list", BrowserAuto)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Songs) != 2 || result.Songs[0].Name != "Direct!" || result.Songs[1].Name != "Browser!" {
		t.Fatalf("songs = %#v, want one normalization pass per source", result.Songs)
	}
	if calls != 2 {
		t.Fatalf("normalizer calls = %d, want one per extracted source", calls)
	}
}

func TestCoordinatorAlwaysPreflightsButDoesNotForceFallback(t *testing.T) {
	extractor := &fakePlaylistExtractor{name: "direct", result: RawResult{Tracks: []TrackCandidate{candidate("Direct", "Artist", "")}}}
	browser := &fakeBrowserExtractor{available: true}
	result, err := coordinatorForTest(t, extractor, browser).ParsePlaylist(context.Background(), "https://known.test/list", BrowserAlways)
	if err != nil || len(result.Songs) != 1 {
		t.Fatalf("ParsePlaylist = %#v, %v", result, err)
	}
	if browser.availableCalls != 1 || browser.extractCalls != 0 {
		t.Fatalf("browser calls = status %d extract %d", browser.availableCalls, browser.extractCalls)
	}
}

func TestCoordinatorLaterCompleteExtractorReplacesStalePartialTotal(t *testing.T) {
	for _, expectedTotal := range []int{0, 2} {
		t.Run(string(rune('0'+expectedTotal)), func(t *testing.T) {
			partial := &fakePlaylistExtractor{name: "partial", result: RawResult{
				Tracks: []TrackCandidate{candidate("Stale Partial", "Artist", "")}, ExpectedTotal: 100,
			}}
			complete := &fakePlaylistExtractor{name: "complete", result: RawResult{
				Tracks: []TrackCandidate{candidate("Complete One", "Artist", ""), candidate("Complete Two", "Artist", "")}, ExpectedTotal: expectedTotal,
			}}
			browser := &fakeBrowserExtractor{available: true, result: RawResult{Tracks: []TrackCandidate{candidate("Browser", "Artist", "")}}}
			result, err := coordinatorWithExtractors(t, []PlaylistExtractor{partial, complete}, browser).ParsePlaylist(context.Background(), "https://known.test/list", BrowserAuto)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Songs) != 2 || result.Songs[0].Name != "Complete One" || result.ExpectedTotal != expectedTotal {
				t.Fatalf("result = %#v", result)
			}
			if browser.availableCalls != 0 || browser.extractCalls != 0 {
				t.Fatalf("browser calls = status %d extract %d", browser.availableCalls, browser.extractCalls)
			}
		})
	}
}

func TestCoordinatorPartialFallsBackMergesAndNormalizes(t *testing.T) {
	extractor := &fakePlaylistExtractor{name: "direct", result: RawResult{
		Tracks: []TrackCandidate{candidate("Direct", "Artist", "hash-1"), candidate("phantom", "", "")}, ExpectedTotal: 3,
	}}
	browser := &fakeBrowserExtractor{available: true, result: RawResult{Tracks: []TrackCandidate{
		candidate("Duplicate Hash", "Other", "HASH-1"), candidate("Browser", "Artist", "hash-2"), candidate("Third", "Artist", "hash-3"),
	}}}
	result, err := coordinatorForTest(t, extractor, browser, cleanupNormalizer{}).ParsePlaylist(context.Background(), "https://known.test/list", BrowserAuto)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Song{{Name: "Direct", Artist: "Artist", Hash: "hash-1"}, {Name: "Browser", Artist: "Artist", Hash: "hash-2"}, {Name: "Third", Artist: "Artist", Hash: "hash-3"}}
	if !reflect.DeepEqual(result.Songs, want) || result.ExpectedTotal != 3 {
		t.Fatalf("result = %#v, want songs %#v total 3", result, want)
	}
}

func TestCoordinatorPartialSurvivesNonContextFallbackFailure(t *testing.T) {
	extractor := &fakePlaylistExtractor{name: "direct", result: RawResult{Tracks: []TrackCandidate{candidate("Partial", "Artist", "")}, ExpectedTotal: 2}, err: errors.New("direct partial")}
	browser := &fakeBrowserExtractor{available: true, err: errors.New("browser failed")}
	result, err := coordinatorForTest(t, extractor, browser).ParsePlaylist(context.Background(), "https://known.test/list", BrowserAuto)
	if err != nil || len(result.Songs) != 1 || result.ExpectedTotal != 2 {
		t.Fatalf("ParsePlaylist = %#v, %v", result, err)
	}
}

func TestCoordinatorBrowserAvailabilityAndPolicyMatrix(t *testing.T) {
	tests := []struct {
		name      string
		rawURL    string
		policy    BrowserPolicy
		extractor *fakePlaylistExtractor
		wantKind  ErrorKind
		status    int
		extract   int
	}{
		{name: "generic auto", rawURL: "https://generic.test/list", policy: BrowserAuto, wantKind: ErrorBrowser, status: 1},
		{name: "generic never", rawURL: "https://generic.test/list", policy: BrowserNever, wantKind: ErrorExtraction},
		{name: "known empty auto", rawURL: "https://known.test/list", policy: BrowserAuto, wantKind: ErrorBrowser, status: 1},
		{name: "known failed auto", rawURL: "https://known.test/list", policy: BrowserAuto, extractor: &fakePlaylistExtractor{name: "direct", err: errors.New("failed")}, wantKind: ErrorExtraction, status: 1},
		{name: "always missing", rawURL: "https://known.test/list", policy: BrowserAlways, extractor: &fakePlaylistExtractor{name: "direct", result: RawResult{Tracks: []TrackCandidate{candidate("Would Work", "", "")}}}, wantKind: ErrorBrowser, status: 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			browser := &fakeBrowserExtractor{}
			_, err := coordinatorForTest(t, test.extractor, browser).ParsePlaylist(context.Background(), test.rawURL, test.policy)
			if !IsKind(err, test.wantKind) {
				t.Fatalf("error = %v, want %s", err, test.wantKind)
			}
			if browser.availableCalls != test.status || browser.extractCalls != test.extract {
				t.Fatalf("browser calls = status %d extract %d", browser.availableCalls, browser.extractCalls)
			}
		})
	}
}

func TestCoordinatorCancellationWinsOverPartialResult(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	extractor := &fakePlaylistExtractor{name: "direct", result: RawResult{Tracks: []TrackCandidate{candidate("Partial", "", "")}, ExpectedTotal: 2}}
	browser := &fakeBrowserExtractor{available: true, result: RawResult{Tracks: []TrackCandidate{candidate("Browser", "", "")}}}
	coordinator := coordinatorForTest(t, extractor, browser)
	cancel()
	if _, err := coordinator.ParsePlaylist(ctx, "https://known.test/list", BrowserAuto); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestCoordinatorRejectsInputBeforeBrowserPreflight(t *testing.T) {
	browser := &fakeBrowserExtractor{available: true}
	coordinator := coordinatorForTest(t, nil, browser)
	for _, rawURL := range []string{"not a URL", "file:///tmp/list"} {
		if _, err := coordinator.ParsePlaylist(context.Background(), rawURL, BrowserAlways); !IsKind(err, ErrorInvalidInput) {
			t.Fatalf("ParsePlaylist(%q) error = %v", rawURL, err)
		}
	}
	if _, err := coordinator.ParsePlaylist(context.Background(), "https://known.test/list", BrowserPolicy("sometimes")); !IsKind(err, ErrorInvalidInput) {
		t.Fatalf("invalid policy error = %v", err)
	}
	if browser.availableCalls != 0 {
		t.Fatalf("browser status calls = %d, want 0", browser.availableCalls)
	}
}
