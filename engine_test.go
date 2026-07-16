package music2bb

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/service"
)

type memoryStorage struct {
	mu    sync.Mutex
	state StoredState
}

func (s *memoryStorage) Load() (StoredState, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := s.state
	result.BlockKeywords = append([]string(nil), result.BlockKeywords...)
	result.QualityKeywords = append([]string(nil), result.QualityKeywords...)
	result.WeightedUploaders = append([]string(nil), result.WeightedUploaders...)
	result.Cookies = append([]Cookie(nil), result.Cookies...)
	return result, nil
}

func (s *memoryStorage) Save(state StoredState) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.state = state
	return nil
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time                                 { return c.now }
func (fakeClock) Sleep(ctx context.Context, _ time.Duration) error { return ctx.Err() }

type countingLimiter struct{ calls atomic.Int32 }

func (l *countingLimiter) Wait(ctx context.Context) error {
	l.calls.Add(1)
	return ctx.Err()
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func jsonResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     http.Header{"Content-Type": {"application/json"}},
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func testStorage() *memoryStorage {
	return &memoryStorage{state: StoredState{
		BlockKeywords: []string{"cover"}, QualityKeywords: []string{"official"},
		WeightedUploaders: []string{"trusted"}, HasCookies: true,
		Cookies: []Cookie{
			{Name: "buvid3", Value: "fingerprint", Domain: ".bilibili.com", Path: "/"},
			{Name: "bili_jct", Value: "csrf", Domain: ".bilibili.com", Path: "/"},
		},
	}}
}

func TestLogoutClearsStoredCookiesAndPreservesConfiguration(t *testing.T) {
	storage := testStorage()
	root := t.TempDir()
	engine, err := New(Config{ConfigDir: root + "/config", CacheDir: root + "/cache"}, WithStorage(storage))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })

	if err := engine.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	state, err := storage.Load()
	if err != nil {
		t.Fatal(err)
	}
	if state.HasCookies || len(state.Cookies) != 0 {
		t.Fatalf("cookies were not cleared: %#v", state.Cookies)
	}
	if !reflect.DeepEqual(state.BlockKeywords, []string{"cover"}) ||
		!reflect.DeepEqual(state.QualityKeywords, []string{"official"}) ||
		!reflect.DeepEqual(state.WeightedUploaders, []string{"trusted"}) {
		t.Fatalf("configuration changed during logout: %#v", state)
	}
}

func newTestEngine(t *testing.T, searchTransport http.RoundTripper, options ...Option) *Engine {
	t.Helper()
	accountCalls := atomic.Int32{}
	accountTransport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		accountCalls.Add(1)
		switch request.URL.Path {
		case "/x/web-interface/nav":
			return jsonResponse(`{"code":0,"data":{"mid":1,"uname":"tester","isLogin":true,"wbi_img":{"img_url":"https://i/imgkey.png","sub_url":"https://i/subkey.png"}}}`), nil
		case "/x/v3/fav/folder/created/list-all":
			return jsonResponse(`{"code":0,"data":{"list":[{"id":9,"title":"target","media_count":2}]}}`), nil
		default:
			return jsonResponse(`{"code":0,"data":{}}`), nil
		}
	})
	if searchTransport == nil {
		searchTransport = searchRoundTripper(0)
	}
	root := t.TempDir()
	base := []Option{
		WithStorage(testStorage()),
		WithHTTPClients(HTTPClients{
			Kugou: &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway", Body: io.NopCloser(bytes.NewReader(nil))}, nil
			})},
			BilibiliAccount: &http.Client{Transport: accountTransport},
			BilibiliSearch:  &http.Client{Transport: searchTransport},
		}),
		WithClock(fakeClock{now: time.Unix(1_700_000_000, 0)}),
	}
	base = append(base, options...)
	engine, err := New(Config{ConfigDir: root + "/config", CacheDir: root + "/cache"}, base...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = engine.Close() })
	return engine
}

func searchRoundTripper(delay time.Duration) http.RoundTripper {
	return roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if delay > 0 {
			select {
			case <-request.Context().Done():
				return nil, request.Context().Err()
			case <-time.After(delay):
			}
		}
		query := request.URL.Query().Get("keyword")
		body := `{"code":0,"data":{"result":[{"result_type":"video","data":[{"bvid":"BV-` + url.QueryEscape(query) + `","aid":11,"title":"` + query + `","author":"trusted","play":1000,"favorites":100,"duration":"3:00"}]}]}}`
		return jsonResponse(body), nil
	})
}

func loginForTest(t *testing.T, engine *Engine) {
	t.Helper()
	account, err := engine.LoginWithOptions(context.Background(), LoginOptions{UseStoredCookies: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if account.ID != 1 || account.Name != "tester" {
		t.Fatalf("unexpected account: %#v", account)
	}
}

func TestInjectedBrowserStorageClockHTTPAndLimiter(t *testing.T) {
	limiter := &countingLimiter{}
	extractor := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		return []Song{{Name: "Injected Song", Artist: "Artist"}}, nil
	})
	engine := newTestEngine(t, nil, WithRateLimiter(limiter), WithBrowserExtractor(extractor))
	loginForTest(t, engine)
	songs, err := engine.ParsePlaylistWithOptions(context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Injected Song" {
		t.Fatalf("songs = %#v", songs)
	}
	if limiter.calls.Load() == 0 {
		t.Fatal("injected limiter was not used")
	}
}

type BrowserExtractorFunc func(context.Context, string) ([]Song, error)

func (f BrowserExtractorFunc) Extract(ctx context.Context, rawURL string) ([]Song, error) {
	return f(ctx, rawURL)
}

func TestUnknownProviderUsesInjectedBrowser(t *testing.T) {
	var calls atomic.Int32
	var extractedURL string
	extractor := BrowserExtractorFunc(func(_ context.Context, rawURL string) ([]Song, error) {
		calls.Add(1)
		extractedURL = rawURL
		return []Song{{
			Name: "Injected Song", Artist: "Artist", Album: "Album", Duration: "3:05", Hash: "hash",
		}}, nil
	})
	engine := newTestEngine(t, nil, WithBrowserExtractor(extractor))

	songs, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || extractedURL != "https://example.test/playlist" {
		t.Fatalf("injected calls = %d, URL = %q", calls.Load(), extractedURL)
	}
	if len(songs) != 1 || songs[0] != (Song{
		Name: "Injected Song", Artist: "Artist", Album: "Album", Duration: "3:05", Hash: "hash",
	}) {
		t.Fatalf("songs = %#v", songs)
	}
}

func TestUnknownProviderAutoWithoutBrowserReturnsBrowserError(t *testing.T) {
	engine := newTestEngine(t, nil)
	_, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if CategoryOf(err) != ErrorBrowser {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorBrowser, err)
	}
}

func TestUnknownProviderAlwaysWithoutBrowserReturnsBrowserError(t *testing.T) {
	engine := newTestEngine(t, nil)
	_, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAlways}, nil,
	)
	if CategoryOf(err) != ErrorBrowser {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorBrowser, err)
	}
}

func TestFailedKugouAutoWithoutBrowserRemainsExtractionError(t *testing.T) {
	engine := newTestEngine(t, nil)
	_, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://www.kugou.com/share?specialid=42", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if CategoryOf(err) != ErrorExtraction {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorExtraction, err)
	}
}

func TestUnknownProviderNeverReturnsExtractionWithoutCallingBrowser(t *testing.T) {
	var calls atomic.Int32
	extractor := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		calls.Add(1)
		return []Song{{Name: "must not be used"}}, nil
	})
	engine := newTestEngine(t, nil, WithBrowserExtractor(extractor))

	_, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserNever}, nil,
	)
	if CategoryOf(err) != ErrorExtraction {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorExtraction, err)
	}
	if calls.Load() != 0 {
		t.Fatalf("injected browser calls = %d, want 0", calls.Load())
	}
}

func TestInjectedBrowserFailureReturnsExtractionError(t *testing.T) {
	cause := errors.New("injected extraction failed")
	extractor := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		return nil, cause
	})
	engine := newTestEngine(t, nil, WithBrowserExtractor(extractor))

	_, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if CategoryOf(err) != ErrorExtraction {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorExtraction, err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("error %v does not contain injected cause", err)
	}
}

func TestInjectedBrowserPartialSongSurvivesNonContextError(t *testing.T) {
	cause := errors.New("late injected failure")
	extractor := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		return []Song{{Name: "Useful Partial", Artist: "Artist"}}, cause
	})
	engine := newTestEngine(t, nil, WithBrowserExtractor(extractor))

	songs, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if err != nil {
		t.Fatalf("partial extraction returned error: %v", err)
	}
	if len(songs) != 1 || songs[0].Name != "Useful Partial" || songs[0].Artist != "Artist" {
		t.Fatalf("songs = %#v", songs)
	}
}

func TestInjectedBrowserCancellationWinsOverPartialSong(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	extractor := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		cancel()
		return []Song{{Name: "Discarded Partial"}}, errors.New("late injected failure")
	})
	engine := newTestEngine(t, nil, WithBrowserExtractor(extractor))

	songs, err := engine.ParsePlaylistWithOptions(
		ctx, "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if CategoryOf(err) != ErrorCancelled {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorCancelled, err)
	}
	if len(songs) != 0 {
		t.Fatalf("cancelled extraction returned songs: %#v", songs)
	}
}

func TestCompleteKugouEmbeddedExtractionSkipsInjectedBrowser(t *testing.T) {
	var browserCalls atomic.Int32
	extractor := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		browserCalls.Add(1)
		return []Song{{Name: "must not be used"}}, nil
	})
	kugouHTTP := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Hostname() == "www.kugou.com" && request.URL.Path == "/share" {
			return jsonResponse(`<html><script>var songData = [{"songname":"Embedded Song","singername":"Embedded Artist"}];</script></html>`), nil
		}
		return jsonResponse(`{"data":{"info":[]}}`), nil
	})}
	engine := newTestEngine(t, nil,
		WithHTTPClients(HTTPClients{Kugou: kugouHTTP}),
		WithBrowserExtractor(extractor),
	)

	songs, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://www.kugou.com/share?specialid=42", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Embedded Song" || songs[0].Artist != "Embedded Artist" {
		t.Fatalf("songs = %#v", songs)
	}
	if browserCalls.Load() != 0 {
		t.Fatalf("injected browser calls = %d, want 0", browserCalls.Load())
	}
}

func TestCompleteAppleMusicExtractionSkipsBrowserWithNeverPolicy(t *testing.T) {
	var browserCalls atomic.Int32
	browser := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		browserCalls.Add(1)
		return []Song{{Name: "must not be used"}}, nil
	})
	appleHTTP := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(appleMusicTestPage("pl.complete", 1, `{"title":"Direct Apple Song","artistName":"Apple Artist","tertiaryLinks":[{"title":"Apple Album"}],"duration":185000}`)), nil
	})}
	engine := newTestEngine(t, nil,
		WithHTTPClients(HTTPClients{AppleMusic: appleHTTP}),
		WithBrowserExtractor(browser),
	)

	songs, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://music.apple.com/us/playlist/complete/pl.complete", ParseOptions{BrowserPolicy: BrowserNever}, nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := []Song{{Name: "Direct Apple Song", Artist: "Apple Artist", Album: "Apple Album", Duration: "3:05"}}
	if !reflect.DeepEqual(songs, want) {
		t.Fatalf("songs = %#v, want %#v", songs, want)
	}
	if browserCalls.Load() != 0 {
		t.Fatalf("injected browser calls = %d, want 0", browserCalls.Load())
	}
}

func TestIncompleteAndFailedAppleMusicExtractionFollowBrowserPolicy(t *testing.T) {
	tests := []struct {
		name       string
		appleReply *http.Response
		browser    []Song
		want       []Song
	}{
		{
			name:       "incomplete merges useful direct songs",
			appleReply: jsonResponse(appleMusicTestPage("pl.partial", 2, `{"title":"Direct Partial","artistName":"Direct Artist"}`)),
			browser:    []Song{{Name: "Browser Completion", Artist: "Browser Artist"}},
			want:       []Song{{Name: "Direct Partial", Artist: "Direct Artist"}, {Name: "Browser Completion", Artist: "Browser Artist"}},
		},
		{
			name: "failed direct extraction uses browser",
			appleReply: &http.Response{
				StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing")),
			},
			browser: []Song{{Name: "Browser Only", Artist: "Browser Artist"}},
			want:    []Song{{Name: "Browser Only", Artist: "Browser Artist"}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var browserCalls atomic.Int32
			appleHTTP := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
				return test.appleReply, nil
			})}
			browser := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
				browserCalls.Add(1)
				return append([]Song(nil), test.browser...), nil
			})
			engine := newTestEngine(t, nil,
				WithHTTPClients(HTTPClients{AppleMusic: appleHTTP}),
				WithBrowserExtractor(browser),
			)
			songs, err := engine.ParsePlaylistWithOptions(
				context.Background(), "https://music.apple.com/us/playlist/test/pl.partial", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(songs, test.want) {
				t.Fatalf("songs = %#v, want %#v", songs, test.want)
			}
			if browserCalls.Load() != 1 {
				t.Fatalf("injected browser calls = %d, want 1", browserCalls.Load())
			}
		})
	}
}

func TestUnknownProviderStillUsesGenericBrowser(t *testing.T) {
	var appleCalls atomic.Int32
	appleHTTP := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		appleCalls.Add(1)
		return jsonResponse(appleMusicTestPage("pl.unused", 1, `{"title":"Wrong","artistName":"Wrong"}`)), nil
	})}
	browser := BrowserExtractorFunc(func(context.Context, string) ([]Song, error) {
		return []Song{{Name: "Generic Song", Artist: "Generic Artist"}}, nil
	})
	engine := newTestEngine(t, nil,
		WithHTTPClients(HTTPClients{AppleMusic: appleHTTP}),
		WithBrowserExtractor(browser),
	)
	songs, err := engine.ParsePlaylistWithOptions(
		context.Background(), "https://example.test/playlist", ParseOptions{BrowserPolicy: BrowserAuto}, nil,
	)
	if err != nil || len(songs) != 1 || songs[0].Name != "Generic Song" {
		t.Fatalf("ParsePlaylistWithOptions = %#v, %v", songs, err)
	}
	if appleCalls.Load() != 0 {
		t.Fatalf("Apple HTTP calls = %d, want 0", appleCalls.Load())
	}
}

func appleMusicTestPage(playlistID string, trackCount int, items string) string {
	return fmt.Sprintf(`<script id="serialized-server-data">{"data":[{"data":{"sections":[{"itemKind":"containerDetailHeaderLockup","items":[{"trackCount":%d,"contentDescriptor":{"kind":"playlist","identifiers":{"storeAdamID":%q}}}]},{"itemKind":"trackLockup","containerContentDescriptor":{"kind":"playlist","identifiers":{"storeAdamID":%q}},"items":[%s]}]}}]}</script>`, trackCount, playlistID, playlistID, items)
}

func TestMatchPreservesOrderAndSerializesObserver(t *testing.T) {
	engine := newTestEngine(t, searchRoundTripper(time.Millisecond))
	loginForTest(t, engine)
	songs := []Song{{Name: "one", Artist: "artist"}, {Name: "two", Artist: "artist"}, {Name: "three", Artist: "artist"}, {Name: "four", Artist: "artist"}}
	var inside atomic.Int32
	var concurrent atomic.Bool
	var eventCount atomic.Int32
	observer := ObserverFunc(func(event ProgressEvent) {
		if event.Kind != EventSong {
			return
		}
		if inside.Add(1) != 1 {
			concurrent.Store(true)
		}
		time.Sleep(time.Millisecond)
		eventCount.Add(1)
		inside.Add(-1)
	})
	results, err := engine.Match(context.Background(), songs, MatchOptions{SearchPages: 1, TopK: 2, Workers: 4}, observer)
	if err != nil {
		t.Fatal(err)
	}
	if concurrent.Load() {
		t.Fatal("public observer was invoked concurrently")
	}
	if eventCount.Load() != int32(len(songs)) {
		t.Fatalf("events = %d, want %d", eventCount.Load(), len(songs))
	}
	for index, result := range results {
		if result.Song.Name != songs[index].Name || !result.HasSelection || result.Video == nil {
			t.Fatalf("result %d = %#v", index, result)
		}
	}
}

func TestCancellationReturnsTypedErrorAndPartialSnapshots(t *testing.T) {
	blocking := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})
	engine := newTestEngine(t, blocking)
	loginForTest(t, engine)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	results, err := engine.Match(ctx, []Song{{Name: "one"}, {Name: "two"}}, MatchOptions{Workers: 2}, nil)
	if len(results) != 2 {
		t.Fatalf("partial results = %d, want 2", len(results))
	}
	if CategoryOf(err) != ErrorCancelled {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorCancelled, err)
	}
}

func TestInvalidInputUsesMachineReadableCategory(t *testing.T) {
	engine := newTestEngine(t, nil)
	_, err := engine.ParsePlaylist(context.Background(), "not a URL", nil)
	if CategoryOf(err) != ErrorInvalidInput {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorInvalidInput, err)
	}
	_, err = engine.ParsePlaylistWithOptions(context.Background(), "https://example.test/list", ParseOptions{BrowserPolicy: BrowserPolicy("sometimes")}, nil)
	if CategoryOf(err) != ErrorInvalidInput {
		t.Fatalf("invalid policy category = %q, want %q (err=%v)", CategoryOf(err), ErrorInvalidInput, err)
	}
}

func TestMatchWeightPresetsAndInvalidConfigurations(t *testing.T) {
	t.Parallel()
	if got := StandardMatchWeights(); got != (MatchWeights{Title: 40, Artist: 25, Quality: 10, Official: 10, Popularity: 10, Uploader: 5}) {
		t.Fatalf("standard preset = %#v", got)
	}
	if got := ClassicalMatchWeights(); got != (MatchWeights{Title: 55, Artist: 10, Quality: 10, Official: 10, Popularity: 10, Uploader: 5}) {
		t.Fatalf("classical preset = %#v", got)
	}

	var searchCalls atomic.Int32
	engine := newTestEngine(t, roundTripperFunc(func(*http.Request) (*http.Response, error) {
		searchCalls.Add(1)
		return jsonResponse(`{"code":0,"data":{"result":[]}}`), nil
	}))
	invalidWeights := []MatchWeights{
		{},
		{Title: -1, Artist: 2},
		{Title: math.NaN()},
		{Title: math.Inf(1)},
	}
	for _, weights := range invalidWeights {
		_, err := engine.Match(context.Background(), []Song{{Name: "song"}}, MatchOptions{Weights: &weights}, nil)
		if CategoryOf(err) != ErrorInvalidInput {
			t.Fatalf("invalid weights %#v category = %q (err=%v)", weights, CategoryOf(err), err)
		}
	}
	_, err := engine.SearchCandidatesWithOptions(context.Background(), Song{Name: "song"}, "query", CandidateSearchOptions{Profile: MatchProfile("unknown")})
	if CategoryOf(err) != ErrorInvalidInput {
		t.Fatalf("unknown profile category = %q (err=%v)", CategoryOf(err), err)
	}
	if searchCalls.Load() != 0 {
		t.Fatalf("invalid configuration reached backend %d time(s)", searchCalls.Load())
	}
}

func profileSearchRoundTripper() http.RoundTripper {
	return roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return jsonResponse(`{"code":0,"data":{"result":[{"result_type":"video","data":[` +
			`{"bvid":"BV-exact","aid":1,"title":"Moon Light Sonata","author":"Other Performer"},` +
			`{"bvid":"BV-artist","aid":2,"title":"Moon Light","author":"Right Artist"}` +
			`] }]}}`), nil
	})
}

func TestCandidateSearchProfilesAreIsolatedPerConcurrentCall(t *testing.T) {
	engine := newTestEngine(t, profileSearchRoundTripper(), WithRateLimiter(&countingLimiter{}))
	song := Song{Name: "Moon Light Sonata", Artist: "Right Artist"}

	legacy, err := engine.SearchCandidates(context.Background(), song, "query", 2)
	if err != nil {
		t.Fatal(err)
	}
	standard, err := engine.SearchCandidatesWithOptions(context.Background(), song, "query", CandidateSearchOptions{Limit: 2, Profile: MatchProfileStandard})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(legacy, standard) {
		t.Fatalf("compatibility wrapper differs:\nlegacy=%#v\nstandard=%#v", legacy, standard)
	}
	titleOnly := MatchWeights{Title: 2}
	titleRanked, err := engine.SearchCandidatesWithOptions(context.Background(), song, "query", CandidateSearchOptions{Limit: 2, Weights: &titleOnly})
	if err != nil || titleRanked[0].Video == nil || titleRanked[0].Video.BVID != "BV-exact" {
		t.Fatalf("title-only custom ranking = %#v, %v", titleRanked, err)
	}
	artistOnly := MatchWeights{Artist: 9}
	artistRanked, err := engine.SearchCandidatesWithOptions(context.Background(), song, "query", CandidateSearchOptions{Limit: 2, Weights: &artistOnly})
	if err != nil || artistRanked[0].Video == nil || artistRanked[0].Video.BVID != "BV-artist" {
		t.Fatalf("artist-only custom ranking = %#v, %v", artistRanked, err)
	}
	if titleOnly != (MatchWeights{Title: 2}) || artistOnly != (MatchWeights{Artist: 9}) {
		t.Fatalf("public custom weights mutated: title=%#v artist=%#v", titleOnly, artistOnly)
	}

	const calls = 24
	var group sync.WaitGroup
	errs := make(chan error, calls)
	for index := 0; index < calls; index++ {
		profile := MatchProfileStandard
		want := "BV-artist"
		if index%2 == 1 {
			profile = MatchProfileClassical
			want = "BV-exact"
		}
		group.Add(1)
		go func(profile MatchProfile, want string) {
			defer group.Done()
			results, searchErr := engine.SearchCandidatesWithOptions(context.Background(), song, "query", CandidateSearchOptions{Limit: 2, Profile: profile})
			if searchErr != nil {
				errs <- searchErr
				return
			}
			if len(results) != 2 || results[0].Video == nil || results[0].Video.BVID != want {
				errs <- fmt.Errorf("profile %q first result = %#v, want %s", profile, results, want)
			}
		}(profile, want)
	}
	group.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestPublicMatchScoresExposeTitleArtistAndKeywordAlias(t *testing.T) {
	t.Parallel()
	video := model.Video{BVID: "BV1"}
	converted := candidateFromInternal(model.MatchResult{
		Video: &video, Score: 70, TitleScore: 88, ArtistScore: 77, KeywordScore: 88,
	})
	if converted.TitleScore != 88 || converted.ArtistScore != 77 || converted.KeywordScore != converted.TitleScore {
		t.Fatalf("public scores = %#v", converted)
	}
}

func TestClassicalMatchReachesTitleFallbackAndAcceptsDifferentRecording(t *testing.T) {
	var mu sync.Mutex
	var queries []string
	transport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		query := request.URL.Query().Get("keyword")
		mu.Lock()
		queries = append(queries, query)
		mu.Unlock()
		if query == "Moon Light Sonata" {
			return jsonResponse(`{"code":0,"data":{"result":[{"result_type":"video","data":[{"bvid":"BV-different","aid":2,"title":"Moon Light Sonata","author":"Other Performer"}]}]}}`), nil
		}
		return jsonResponse(`{"code":0,"data":{"result":[{"result_type":"video","data":[{"bvid":"BV-weak","aid":1,"title":"Moon Light","author":"Other Performer"}]}]}}`), nil
	})
	engine := newTestEngine(t, transport, WithRateLimiter(&countingLimiter{}))
	outcomes, err := engine.Match(context.Background(), []Song{{Name: "Moon Light Sonata", Artist: "Right Artist"}}, MatchOptions{
		SearchPages: 1, TopK: 3, Workers: 1, Profile: MatchProfileClassical,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].HasSelection || outcomes[0].Video == nil || outcomes[0].Video.BVID != "BV-different" {
		t.Fatalf("classical outcome = %#v", outcomes)
	}
	mu.Lock()
	defer mu.Unlock()
	if got, want := fmt.Sprint(queries), "[Moon Light Sonata Right Artist Moon Light Sonata]"; got != want {
		t.Fatalf("queries = %s, want %s", got, want)
	}
}

func TestConcurrentCallsAreRaceSafe(t *testing.T) {
	engine := newTestEngine(t, searchRoundTripper(time.Millisecond))
	loginForTest(t, engine)
	var group sync.WaitGroup
	for index := 0; index < 8; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			results, err := engine.SearchCandidates(context.Background(), Song{Name: "song"}, "query", 5)
			if err != nil || len(results) != 1 {
				t.Errorf("call %d: results=%#v err=%v", index, results, err)
			}
		}(index)
	}
	group.Wait()
}

func TestErrorUnwrap(t *testing.T) {
	cause := errors.New("cause")
	err := &Error{Category: ErrorNetwork, Operation: "test", Err: cause}
	if !errors.Is(err, cause) {
		t.Fatal("typed error does not unwrap")
	}
}

func TestReviewReasonCrossesPublicBoundary(t *testing.T) {
	t.Parallel()
	outcomes := outcomesFromInternal([]service.MatchOutcome{{
		Song: model.Song{Name: "shared"}, NeedsReview: true, ReviewReason: model.ReviewAmbiguous,
	}})
	if len(outcomes) != 1 || !outcomes[0].NeedsReview || outcomes[0].ReviewReason != ReviewAmbiguous {
		t.Fatalf("public outcome = %#v", outcomes)
	}
	if string(ReviewSearchFailed) != "search_failed" || string(ReviewArtistUnverified) != "artist_unverified" {
		t.Fatal("public review-reason wire values changed")
	}
}

func TestAddToFavoriteReturnsPartialResultAndTypedError(t *testing.T) {
	var writes atomic.Int32
	accountTransport := roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		switch request.URL.Path {
		case "/x/web-interface/nav":
			return jsonResponse(`{"code":0,"data":{"mid":1,"uname":"tester","isLogin":true,"wbi_img":{"img_url":"https://i/abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789ab.png","sub_url":"https://i/bcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abc.png"}}}`), nil
		case "/x/v3/fav/resource/deal":
			if writes.Add(1) == 1 {
				return jsonResponse(`{"code":0,"data":{}}`), nil
			}
			return jsonResponse(`{"code":-400,"message":"rejected","data":{}}`), nil
		default:
			return jsonResponse(`{"code":0,"data":{}}`), nil
		}
	})
	root := t.TempDir()
	engine, err := New(Config{ConfigDir: root + "/config", CacheDir: root + "/cache"},
		WithStorage(testStorage()),
		WithClock(fakeClock{now: time.Unix(1_700_000_000, 0)}),
		WithHTTPClients(HTTPClients{
			BilibiliAccount: &http.Client{Transport: accountTransport},
			BilibiliSearch:  &http.Client{Transport: searchRoundTripper(0)},
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	loginForTest(t, engine)
	matches := []MatchResult{
		{Song: Song{Name: "one"}, Video: &Video{BVID: "BV1", AID: 1}, Matched: true, HasSelection: true},
		{Song: Song{Name: "two"}, Video: &Video{BVID: "BV2", AID: 2}, Matched: true, HasSelection: true},
	}
	result, err := engine.AddToFavorite(context.Background(), 9, matches, nil)
	if len(result.Succeeded) != 1 || len(result.Failed) != 1 {
		t.Fatalf("partial result = %#v", result)
	}
	if CategoryOf(err) != ErrorPartialWrite {
		t.Fatalf("category = %q, want %q (err=%v)", CategoryOf(err), ErrorPartialWrite, err)
	}
}
