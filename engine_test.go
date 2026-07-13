package music2bb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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
