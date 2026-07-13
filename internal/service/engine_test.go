package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
)

type fakePlaylist struct {
	songs    []model.Song
	expected int
}

func (f fakePlaylist) ParsePlaylist(context.Context, string, BrowserPolicy) (PlaylistResult, error) {
	return PlaylistResult{Songs: f.songs, ExpectedTotal: f.expected}, nil
}

type fakeRemote struct {
	active    atomic.Int32
	maxActive atomic.Int32
	delay     time.Duration
	add       AddResult
	addErr    error
}

func (f *fakeRemote) SearchVideos(ctx context.Context, keyword string, _, _ int) ([]model.Video, error) {
	active := f.active.Add(1)
	defer f.active.Add(-1)
	for {
		maximum := f.maxActive.Load()
		if active <= maximum || f.maxActive.CompareAndSwap(maximum, active) {
			break
		}
	}
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	return []model.Video{{BVID: "BV-" + keyword, Title: keyword, Uploader: "artist"}}, nil
}

func (f *fakeRemote) VideoDetail(context.Context, string) (model.Video, error) {
	return model.Video{}, nil
}

func (f *fakeRemote) Login(context.Context, LoginOptions, func(LoginUpdate)) (Account, error) {
	return Account{ID: 1, Name: "tester"}, nil
}

func (f *fakeRemote) ListFavorites(context.Context) ([]model.Favorite, error) {
	return []model.Favorite{{ID: 1, Title: "test"}}, nil
}

func (f *fakeRemote) CreateFavorite(_ context.Context, req CreateFavoriteRequest) (model.Favorite, error) {
	return model.Favorite{ID: 2, Title: req.Title}, nil
}

func (f *fakeRemote) AddToFavorite(context.Context, int64, []model.Video) (AddResult, error) {
	return f.add, f.addErr
}

type fakeMatcher struct{}

func (fakeMatcher) Match(song model.Song, videos []model.Video, topK int) []model.MatchResult {
	results := make([]model.MatchResult, 0, len(videos))
	for index := range videos {
		video := videos[index]
		results = append(results, model.MatchResult{Song: song, Video: &video, Score: float64(len(videos) - index), KeywordScore: 100, Matched: true})
	}
	if len(results) > topK {
		results = results[:topK]
	}
	return results
}

type scriptedSearch struct {
	mu      sync.Mutex
	queries []string
	results map[string][]model.Video
}

func (s *scriptedSearch) SearchVideos(_ context.Context, keyword string, _, _ int) ([]model.Video, error) {
	s.mu.Lock()
	s.queries = append(s.queries, keyword)
	s.mu.Unlock()
	return append([]model.Video(nil), s.results[keyword]...), nil
}

func (s *scriptedSearch) VideoDetail(context.Context, string) (model.Video, error) {
	return model.Video{}, nil
}

func newTestEngine(t *testing.T, remote *fakeRemote) *Engine {
	t.Helper()
	engine, err := New(Dependencies{
		Playlist: fakePlaylist{songs: []model.Song{{Name: "one"}}},
		Match:    remote,
		Account:  remote,
		Matcher:  fakeMatcher{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

func TestMatchBoundsWorkersAndPreservesOrder(t *testing.T) {
	remote := &fakeRemote{delay: 10 * time.Millisecond}
	engine := newTestEngine(t, remote)
	songs := make([]model.Song, 12)
	for index := range songs {
		songs[index] = model.Song{Name: fmt.Sprintf("song-%02d", index), Artist: "artist"}
	}
	outcomes, err := engine.Match(context.Background(), songs, MatchOptions{Workers: 3, SearchPages: 1, TopK: 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if remote.maxActive.Load() > 3 {
		t.Fatalf("maximum concurrent searches = %d, want <= 3", remote.maxActive.Load())
	}
	for index, outcome := range outcomes {
		if outcome.Song.Name != songs[index].Name {
			t.Fatalf("outcome %d = %q, want %q", index, outcome.Song.Name, songs[index].Name)
		}
		if len(outcome.Candidates) != 1 || !outcome.HasSelection {
			t.Fatalf("outcome %d did not retain/select candidates: %#v", index, outcome)
		}
	}
}

func TestMatchUsesArtistQueryThenQueuesTitleFallbackForReview(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"Shared Song Right Artist": {{BVID: "wrong", Title: "Shared Song", Uploader: "Other Artist"}},
		"Shared Song":              {{BVID: "right", Title: "Shared Song", Uploader: "Right Artist"}},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Matcher: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Shared Song", Artist: "Right Artist"}}, MatchOptions{Workers: 1, SearchPages: 1, TopK: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || outcomes[0].HasSelection || !outcomes[0].NeedsReview {
		t.Fatalf("unsafe fallback outcome = %#v", outcomes)
	}
	search.mu.Lock()
	defer search.mu.Unlock()
	want := []string{"Shared Song Right Artist", "Shared Song"}
	if fmt.Sprint(search.queries) != fmt.Sprint(want) {
		t.Fatalf("queries = %#v, want %#v", search.queries, want)
	}
}

func TestMatchAutoSelectsOnlyWithArtistEvidence(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"Shared Song Right Artist": {{BVID: "right", Title: "Shared Song - Right Artist", Uploader: "music"}},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Matcher: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Shared Song", Artist: "Right Artist"}}, MatchOptions{Workers: 1, SearchPages: 1}, nil)
	if err != nil || len(outcomes) != 1 || !outcomes[0].HasSelection || outcomes[0].NeedsReview {
		t.Fatalf("safe outcome = %#v, %v", outcomes, err)
	}
}

func TestMatchWithoutArtistAlwaysNeedsReview(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"Mystery": {{BVID: "candidate", Title: "Mystery", Uploader: "unknown"}},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Matcher: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	var warned bool
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Mystery"}}, MatchOptions{Workers: 1, SearchPages: 1}, ObserverFunc(func(event ProgressEvent) {
		if event.Kind == EventWarning {
			warned = true
		}
	}))
	if err != nil || len(outcomes) != 1 || outcomes[0].HasSelection || !outcomes[0].NeedsReview {
		t.Fatalf("artistless outcome = %#v, %v", outcomes, err)
	}
	if !warned {
		t.Fatal("artistless outcome did not emit a warning")
	}
}

func TestParsePlaylistWarnsAndContinuesWhenIncomplete(t *testing.T) {
	remote := &fakeRemote{}
	engine, err := New(Dependencies{
		Playlist: fakePlaylist{songs: []model.Song{{Name: "one", Artist: "artist"}}, expected: 109},
		Match:    remote, Account: remote, Matcher: fakeMatcher{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var events []ProgressEvent
	songs, err := engine.ParsePlaylist(context.Background(), "https://example.test/list", ParseOptions{}, ObserverFunc(func(event ProgressEvent) {
		events = append(events, event)
	}))
	if err != nil || len(songs) != 1 {
		t.Fatalf("ParsePlaylist = %#v, %v", songs, err)
	}
	if len(events) == 0 || events[0].Kind != EventProgress || events[0].Operation != "parse_playlist" || events[0].Message != "正在解析歌单" {
		t.Fatalf("initial parse event = %#v", events)
	}
	found := false
	for _, event := range events {
		if event.Kind == EventWarning && event.Operation == "parse_playlist" && event.Current == 1 && event.Total == 109 &&
			event.Message == "警告：歌单抓取不完整，实际 1 / 预期 109 首；将继续处理已获取歌曲" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing incomplete warning: %#v", events)
	}
}

func TestMatchSerializesObserver(t *testing.T) {
	remote := &fakeRemote{delay: time.Millisecond}
	engine := newTestEngine(t, remote)
	var inside atomic.Int32
	var concurrent atomic.Bool
	var eventsMu sync.Mutex
	events := make([]ProgressEvent, 0)
	observer := ObserverFunc(func(event ProgressEvent) {
		if inside.Add(1) != 1 {
			concurrent.Store(true)
		}
		time.Sleep(time.Millisecond)
		eventsMu.Lock()
		events = append(events, event)
		eventsMu.Unlock()
		inside.Add(-1)
	})
	songs := []model.Song{
		{Name: "a", Artist: "artist"}, {Name: "b", Artist: "artist"},
		{Name: "c", Artist: "artist"}, {Name: "d", Artist: "artist"},
	}
	if _, err := engine.Match(context.Background(), songs, MatchOptions{Workers: 4, SearchPages: 1}, observer); err != nil {
		t.Fatal(err)
	}
	if concurrent.Load() {
		t.Fatal("observer was called concurrently")
	}
	if len(events) != len(songs) {
		t.Fatalf("events = %d, want %d", len(events), len(songs))
	}
}

func TestMatchReturnsPartialResultsOnCancellation(t *testing.T) {
	remote := &fakeRemote{delay: time.Second}
	engine := newTestEngine(t, remote)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	outcomes, err := engine.Match(ctx, []model.Song{{Name: "a"}, {Name: "b"}}, MatchOptions{Workers: 2}, nil)
	if len(outcomes) != 2 {
		t.Fatalf("outcomes = %d, want 2", len(outcomes))
	}
	if CategoryOf(err) != ErrorCancelled {
		t.Fatalf("error category = %q, want %q (err=%v)", CategoryOf(err), ErrorCancelled, err)
	}
}

func TestAddToFavoriteClassifiesPartialFailure(t *testing.T) {
	remote := &fakeRemote{
		add:    AddResult{FavoriteID: 4, Succeeded: []string{"BV1"}, Failed: []AddFailure{{BVID: "BV2", Reason: "denied"}}},
		addErr: errors.New("one failed"),
	}
	engine := newTestEngine(t, remote)
	video1 := model.Video{BVID: "BV1"}
	video2 := model.Video{BVID: "BV2"}
	outcomes := []MatchOutcome{
		{HasSelection: true, Selected: model.MatchResult{Video: &video1, Matched: true}},
		{HasSelection: true, Selected: model.MatchResult{Video: &video2, Matched: true}},
	}
	result, err := engine.AddToFavorite(context.Background(), 4, outcomes, nil)
	if len(result.Succeeded) != 1 || len(result.Failed) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if CategoryOf(err) != ErrorPartialWrite {
		t.Fatalf("category = %q, want %q", CategoryOf(err), ErrorPartialWrite)
	}
}
