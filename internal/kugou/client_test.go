package kugou

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/netx"
)

func TestParsePlaylistUsesResolvedIDAndFixedEndpointOrder(t *testing.T) {
	var mu sync.Mutex
	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		calls = append(calls, r.URL.Path)
		mu.Unlock()
		switch r.URL.Path {
		case "/share":
			http.Redirect(w, r, "/resolved?specialid=42", http.StatusFound)
		case "/resolved":
			w.Write([]byte("<html></html>"))
		case "/api/first":
			if r.URL.Query().Get("specialid") != "42" || r.URL.Query().Get("pagesize") != "200" || r.URL.Query().Get("page") != "1" {
				t.Errorf("first endpoint query = %v", r.URL.Query())
			}
			w.Write([]byte(`{"data":{"info":[]}}`))
		case "/api/42":
			w.Write([]byte(`{"data":{"data":{"songs":[{"songname":"Track","singername":"Artist","album_name":"Album","duration":185,"hash":"abc"}]}}}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := newTestClient(t, server, nil,
		APIEndpoint{URL: server.URL + "/api/first", Parameters: true},
		APIEndpoint{URL: server.URL + "/api/{playlist_id}"},
		APIEndpoint{URL: server.URL + "/api/should-not-run"},
	)
	songs, err := client.ParsePlaylist(context.Background(), server.URL+"/share")
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Song{{Name: "Track", Artist: "Artist", Album: "Album", Duration: "3:05", Hash: "abc"}}
	if !reflect.DeepEqual(songs, want) {
		t.Fatalf("songs = %#v, want %#v", songs, want)
	}
	mu.Lock()
	defer mu.Unlock()
	wantCalls := []string{"/share", "/resolved", "/api/first", "/api/42"}
	if !reflect.DeepEqual(calls, wantCalls) {
		t.Fatalf("calls = %v, want %v", calls, wantCalls)
	}
}

func TestParsePlaylistUsesOriginalIDWhenRedirectDropsQuery(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/share":
			http.Redirect(w, r, "/resolved", http.StatusFound)
		case "/resolved":
			w.Write([]byte("empty"))
		case "/api":
			if got := r.URL.Query().Get("specialid"); got != "original" {
				t.Errorf("specialid = %q, want original", got)
			}
			w.Write([]byte(`{"data":{"info":[{"name":"Song","author":"Singer"}]}}`))
		}
	}))
	defer server.Close()
	client := newTestClient(t, server, nil, APIEndpoint{URL: server.URL + "/api", Parameters: true})
	songs, err := client.ParsePlaylist(context.Background(), server.URL+"/share?global_specialid=original")
	if err != nil || len(songs) != 1 || songs[0].Name != "Song" {
		t.Fatalf("ParsePlaylist = %#v, %v", songs, err)
	}
}

func TestParsePlaylistFallsBackToEmbeddedJSONBeforeBrowser(t *testing.T) {
	browser := &stubExtractor{songs: []model.Song{{Name: "Browser should not run"}}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><script>var songData = [{"songname":"Embedded","singername":"Singer"}];</script></html>`))
	}))
	defer server.Close()
	client := newTestClient(t, server, browser)
	songs, err := client.ParsePlaylist(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Embedded" {
		t.Fatalf("songs = %#v", songs)
	}
	if browser.calls != 0 {
		t.Fatalf("browser calls = %d, want 0", browser.calls)
	}
}

func TestParsePlaylistUsesBrowserOnlyAfterDirectMethodsFail(t *testing.T) {
	browser := &stubExtractor{songs: []model.Song{
		{Name: "Song", Artist: "Singer"},
		{Name: "Song"},
		{Name: "Singer"},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("<html>no data</html>"))
	}))
	defer server.Close()
	client := newTestClient(t, server, browser)
	songs, err := client.ParsePlaylist(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Song{{Name: "Song", Artist: "Singer"}}
	if !reflect.DeepEqual(songs, want) {
		t.Fatalf("songs = %#v, want %#v", songs, want)
	}
	if browser.calls != 1 {
		t.Fatalf("browser calls = %d, want 1", browser.calls)
	}
}

func TestParsePlaylistHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newTestClient(t, server, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ParsePlaylist(ctx, server.URL); err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestParsePlaylistRejectsInvalidURL(t *testing.T) {
	client := New(nil, nil)
	if _, err := client.ParsePlaylist(context.Background(), "not a URL"); !IsKind(err, ErrorInvalidURL) {
		t.Fatalf("error = %v, want %s", err, ErrorInvalidURL)
	}
}

func TestPlaylistID(t *testing.T) {
	tests := []struct {
		url  string
		want string
	}{
		{"https://example.test/?specialid=12&global_specialid=34", "12"},
		{"https://example.test/?specialid=-2147483648&global_specialid=34", "34"},
		{"https://example.test/?global_specialid=global", "global"},
		{"https://example.test/", ""},
	}
	for _, test := range tests {
		t.Run(test.want, func(t *testing.T) {
			if got := PlaylistID(test.url); got != test.want {
				t.Fatalf("PlaylistID(%q) = %q, want %q", test.url, got, test.want)
			}
		})
	}
}

func TestDefaultAPIEndpointOrder(t *testing.T) {
	want := []APIEndpoint{
		{URL: "https://mobileservice.kugou.com/api/v3/plist/speciallist", Parameters: true},
		{URL: "https://mobileservice.kugou.com/api/v3/plist/list", Parameters: true},
		{URL: "https://m.kugou.com/plist/list/{playlist_id}"},
		{URL: "https://wwwapi.kugou.com/playlist/detail/{playlist_id}"},
		{URL: "https://mobileservice.kugou.com/api/v3/special/song", Parameters: true},
	}
	if got := defaultAPIEndpoints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultAPIEndpoints = %#v, want %#v", got, want)
	}
}

func TestDecodeSongResponseNestedShapes(t *testing.T) {
	tests := []string{
		`{"data":{"info":[{"songname":"A","singername":"B"}]}}`,
		`{"data":{"list":{"songs":[{"name":"A","author":"B"}]}}}`,
		`{"songList":{"data":[{"title":"A","artist":"B"}]}}`,
		`[{"songName":"A","singerName":"B"}]`,
	}
	for _, payload := range tests {
		songs := decodeSongResponse([]byte(payload))
		if len(songs) != 1 || songs[0].Name != "A" || songs[0].Artist != "B" {
			t.Fatalf("decodeSongResponse(%s) = %#v", payload, songs)
		}
	}
}

func TestExtractEmbeddedSongs(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{"song data", `<script>var songData = [{"name":"A","author":"B","nested":{"x":[1,2]}}];</script>`},
		{"playlist data", `<script>playlistData = {"list":[{"title":"A","artist":"B"}]};</script>`},
		{"application json", `<script type="application/json">{"props":{"data":{"songs":[{"songName":"A","singerName":"B"}]}}}</script>`},
		{"songs property", `<script>window.x = {"songs":[{"songname":"A","singername":"B"}]};</script>`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			songs := ExtractEmbeddedSongs(test.html)
			if len(songs) != 1 || songs[0].Name != "A" || songs[0].Artist != "B" {
				t.Fatalf("songs = %#v", songs)
			}
		})
	}
}

func TestCleanupSongsPythonParity(t *testing.T) {
	input := []model.Song{
		{Name: "Keep", Artist: "Singer"},
		{Name: "Keep"},
		{Name: "Singer", Artist: "Other"},
		{Name: "Duet/A", Artist: "Singer"},
		{Name: "Duplicate", Artist: "One"},
		{Name: "Duplicate", Artist: "One"},
		{Name: "Duplicate", Artist: "Two"},
	}
	want := []model.Song{
		{Name: "Keep", Artist: "Singer"},
		{Name: "Duplicate", Artist: "One"},
		{Name: "Duplicate", Artist: "Two"},
	}
	if got := CleanupSongs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("CleanupSongs = %#v, want %#v", got, want)
	}
}

type stubExtractor struct {
	songs []model.Song
	err   error
	calls int
}

func (s *stubExtractor) Extract(context.Context, string) ([]model.Song, error) {
	s.calls++
	return append([]model.Song(nil), s.songs...), s.err
}

func newTestClient(t *testing.T, server *httptest.Server, browser Extractor, endpoints ...APIEndpoint) *Client {
	t.Helper()
	httpClient := netx.New(time.Second, 2, nil)
	httpClient.HTTP = server.Client()
	httpClient.MaxAttempts = 1
	options := []Option{WithAPIEndpoints(endpoints)}
	return New(httpClient, browser, options...)
}

func TestJSONFixturesRemainValid(t *testing.T) {
	// This guards accidental typos in inline response fixtures, which otherwise
	// look like extraction failures rather than malformed test data.
	fixtures := []string{
		`{"data":{"info":[]}}`,
		`{"data":{"data":{"songs":[{"songname":"Track"}]}}}`,
	}
	for _, fixture := range fixtures {
		var value any
		if err := json.Unmarshal([]byte(fixture), &value); err != nil {
			t.Fatalf("invalid JSON fixture %q: %v", fixture, err)
		}
	}
}
