package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
)

type fakePlaylist struct {
	songs           []model.Song
	expected        int
	browserFallback bool
}

func (f fakePlaylist) ParsePlaylist(_ context.Context, _ string, _ BrowserPolicy, onBrowserFallback func()) (PlaylistResult, error) {
	if f.browserFallback && onBrowserFallback != nil {
		onBrowserFallback()
	}
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

func (fakeMatcher) QueryPhases(song model.Song) []QueryPhase {
	phases := []QueryPhase{{Queries: song.AllSearchKeywords()}}
	if song.SearchKeyword() != song.SearchKeywordFull() {
		phases = append(phases, QueryPhase{Queries: []string{song.SearchKeyword()}})
	}
	return phases
}

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

func (m fakeMatcher) Rank(song model.Song, videos []model.Video, topK int) []model.MatchResult {
	return m.Match(song, videos, topK)
}

func (fakeMatcher) Decide(song model.Song, ranked []model.MatchResult, final bool) MatchDecision {
	for index, candidate := range ranked {
		if candidate.Video != nil && strings.Contains(strings.ToLower(candidate.Video.Title+" "+candidate.Video.Uploader), strings.ToLower(song.CleanArtist())) && song.CleanArtist() != "" {
			return MatchDecision{SelectedIndex: index}
		}
	}
	if !final {
		return MatchDecision{SelectedIndex: -1, Continue: true, ReviewReason: model.ReviewArtistUnverified}
	}
	reason := model.ReviewWeakTitle
	if len(ranked) == 0 {
		reason = model.ReviewNoCandidates
	}
	return MatchDecision{SelectedIndex: -1, ReviewReason: reason}
}

type scriptedSearch struct {
	mu      sync.Mutex
	queries []string
	results map[string][]model.Video
	errs    map[string]error
}

type ambiguityStrategy struct{}

func (ambiguityStrategy) QueryPhases(model.Song) []QueryPhase {
	return []QueryPhase{{Queries: []string{"title"}}}
}

func (ambiguityStrategy) Rank(song model.Song, videos []model.Video, limit int) []model.MatchResult {
	ranked := make([]model.MatchResult, 0, len(videos))
	for index := range videos {
		video := videos[index]
		ranked = append(ranked, model.MatchResult{Song: song, Video: &video, Matched: true, KeywordScore: 100, Score: 40 - float64(index*4)})
	}
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	return ranked
}

func (ambiguityStrategy) Decide(_ model.Song, ranked []model.MatchResult, _ bool) MatchDecision {
	if len(ranked) > 1 && ranked[0].Score-ranked[1].Score < 5 {
		return MatchDecision{SelectedIndex: -1, ReviewReason: model.ReviewAmbiguous}
	}
	return MatchDecision{SelectedIndex: 0}
}

type burstSearch struct {
	total   int32
	ready   atomic.Int32
	release chan struct{}
	once    sync.Once
}

func (s *burstSearch) SearchVideos(context.Context, string, int, int) ([]model.Video, error) {
	if s.ready.Add(1) == s.total {
		s.once.Do(func() { close(s.release) })
	}
	<-s.release
	return []model.Video{{BVID: "candidate", Title: "title"}}, nil
}

func (*burstSearch) VideoDetail(context.Context, string) (model.Video, error) {
	return model.Video{}, nil
}

func (s *scriptedSearch) SearchVideos(_ context.Context, keyword string, _, _ int) ([]model.Video, error) {
	s.mu.Lock()
	s.queries = append(s.queries, keyword)
	s.mu.Unlock()
	if err := s.errs[keyword]; err != nil {
		return nil, err
	}
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
		Strategy: fakeMatcher{},
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

func TestMatchTopKDoesNotHideAmbiguityFromDecision(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"title": {
			{BVID: "top", Title: "title", Uploader: "one"},
			{BVID: "runner-up", Title: "title", Uploader: "two"},
		},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: ambiguityStrategy{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "title"}}, MatchOptions{Workers: 1, SearchPages: 1, TopK: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if outcomes[0].HasSelection || !outcomes[0].NeedsReview || outcomes[0].ReviewReason != model.ReviewAmbiguous {
		t.Fatalf("top-k weakened ambiguity decision: %#v", outcomes[0])
	}
	if len(outcomes[0].Candidates) != 1 {
		t.Fatalf("retained candidates = %d, want top-k 1", len(outcomes[0].Candidates))
	}
}

func TestMatchUsesArtistQueryThenSelectsSafeTitleFallback(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"Shared Song Right Artist": {{BVID: "wrong", Title: "Shared Song", Uploader: "Other Artist"}},
		"Shared Song":              {{BVID: "right", Title: "Shared Song", Uploader: "Right Artist"}},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Shared Song", Artist: "Right Artist"}}, MatchOptions{Workers: 1, SearchPages: 1, TopK: 3}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 1 || !outcomes[0].HasSelection || outcomes[0].NeedsReview {
		t.Fatalf("safe fallback outcome = %#v", outcomes)
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
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Shared Song", Artist: "Right Artist"}}, MatchOptions{Workers: 1, SearchPages: 1}, nil)
	if err != nil || len(outcomes) != 1 || !outcomes[0].HasSelection || outcomes[0].NeedsReview {
		t.Fatalf("safe outcome = %#v, %v", outcomes, err)
	}
	search.mu.Lock()
	defer search.mu.Unlock()
	if got := fmt.Sprint(search.queries); got != "[Shared Song Right Artist]" {
		t.Fatalf("safe artist phase did not short-circuit: %s", got)
	}
}

func TestMatchWithoutArtistAlwaysNeedsReview(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"Mystery": {{BVID: "candidate", Title: "Mystery", Uploader: "unknown"}},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	var songEvents int
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Mystery"}}, MatchOptions{Workers: 1, SearchPages: 1}, ObserverFunc(func(event ProgressEvent) {
		if event.Kind == EventSong {
			songEvents++
		}
	}))
	if err != nil || len(outcomes) != 1 || outcomes[0].HasSelection || !outcomes[0].NeedsReview {
		t.Fatalf("artistless outcome = %#v, %v", outcomes, err)
	}
	if songEvents != 1 {
		t.Fatalf("song events = %d, want one ordered status event", songEvents)
	}
}

func TestMatchDeduplicatesAggregateAcrossPhases(t *testing.T) {
	search := &scriptedSearch{results: map[string][]model.Video{
		"Shared Song Right Artist": {
			{BVID: "duplicate", Title: "Shared Song", Uploader: "Other"},
		},
		"Shared Song": {
			{BVID: "duplicate", Title: "Shared Song duplicate", Uploader: "Other"},
			{BVID: "unique", Title: "Shared Song", Uploader: "Another"},
		},
	}}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Shared Song", Artist: "Right Artist"}}, MatchOptions{Workers: 1, SearchPages: 1, TopK: 10}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes[0].Candidates) != 2 {
		t.Fatalf("deduplicated candidates = %#v", outcomes[0].Candidates)
	}
	if outcomes[0].Candidates[0].Video.Title != "Shared Song" {
		t.Fatalf("first-seen duplicate was not preserved: %#v", outcomes[0].Candidates[0].Video)
	}
}

func TestMatchReviewReasonsForEmptyAndFailedSearches(t *testing.T) {
	tests := []struct {
		name       string
		search     *scriptedSearch
		wantReason model.ReviewReason
		wantError  bool
	}{
		{
			name: "empty", search: &scriptedSearch{results: map[string][]model.Video{}},
			wantReason: model.ReviewNoCandidates,
		},
		{
			name: "failed", search: &scriptedSearch{results: map[string][]model.Video{}, errs: map[string]error{"Song Artist": errors.New("offline"), "Song": errors.New("offline")}},
			wantReason: model.ReviewSearchFailed, wantError: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			account := &fakeRemote{}
			engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: tt.search, Account: account, Strategy: fakeMatcher{}})
			if err != nil {
				t.Fatal(err)
			}
			outcomes, matchErr := engine.Match(context.Background(), []model.Song{{Name: "Song", Artist: "Artist"}}, MatchOptions{Workers: 1, SearchPages: 1}, nil)
			if (matchErr != nil) != tt.wantError {
				t.Fatalf("error = %v, wantError=%v", matchErr, tt.wantError)
			}
			if len(outcomes) != 1 || !outcomes[0].NeedsReview || outcomes[0].ReviewReason != tt.wantReason {
				t.Fatalf("outcome = %#v", outcomes)
			}
		})
	}
}

func TestMatchContinuesAfterPartialSearchFailure(t *testing.T) {
	search := &scriptedSearch{
		results: map[string][]model.Video{"Shared Song": {{BVID: "safe", Title: "Shared Song - Right Artist"}}},
		errs:    map[string]error{"Shared Song Right Artist": errors.New("temporary")},
	}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: fakeMatcher{}})
	if err != nil {
		t.Fatal(err)
	}
	outcomes, err := engine.Match(context.Background(), []model.Song{{Name: "Shared Song", Artist: "Right Artist"}}, MatchOptions{Workers: 1, SearchPages: 1}, nil)
	if err != nil || !outcomes[0].HasSelection || outcomes[0].Failure != nil {
		t.Fatalf("partial failure outcome = %#v, %v", outcomes, err)
	}
}

func TestParsePlaylistWarnsAndContinuesWhenIncomplete(t *testing.T) {
	remote := &fakeRemote{}
	engine, err := New(Dependencies{
		Playlist: fakePlaylist{songs: []model.Song{{Name: "one", Artist: "artist"}}, expected: 109},
		Match:    remote, Account: remote, Strategy: fakeMatcher{},
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

func TestParsePlaylistNotifiesWhenHTTPFallsBackToChromium(t *testing.T) {
	remote := &fakeRemote{}
	engine, err := New(Dependencies{
		Playlist: fakePlaylist{songs: []model.Song{{Name: "one"}}, browserFallback: true},
		Match:    remote, Account: remote, Strategy: fakeMatcher{},
	})
	if err != nil {
		t.Fatal(err)
	}
	var messages []string
	_, err = engine.ParsePlaylist(context.Background(), "https://example.test/list", ParseOptions{}, ObserverFunc(func(event ProgressEvent) {
		if event.Kind == EventWarning {
			messages = append(messages, event.Message)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := "HTTP 解析失败或结果不完整，正在自动切换到 Chromium。"
	if len(messages) != 1 || messages[0] != want {
		t.Fatalf("warning messages = %#v, want %q", messages, want)
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

func TestMatchProgressCompletionIsMonotonic(t *testing.T) {
	const songCount = 256
	search := &burstSearch{total: songCount, release: make(chan struct{})}
	account := &fakeRemote{}
	engine, err := New(Dependencies{Playlist: fakePlaylist{}, Match: search, Account: account, Strategy: ambiguityStrategy{}})
	if err != nil {
		t.Fatal(err)
	}
	songs := make([]model.Song, songCount)
	for index := range songs {
		songs[index] = model.Song{Name: fmt.Sprintf("song-%d", index)}
	}
	currents := make([]int, 0, songCount)
	_, err = engine.Match(context.Background(), songs, MatchOptions{Workers: songCount, SearchPages: 1, TopK: 1}, ObserverFunc(func(event ProgressEvent) {
		if event.Kind == EventSong && event.Operation == "match" {
			currents = append(currents, event.Current)
		}
	}))
	if err != nil {
		t.Fatal(err)
	}
	for index, current := range currents {
		if want := index + 1; current != want {
			t.Fatalf("progress event %d reported %d, want %d; sequence=%v", index, current, want, currents)
		}
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
