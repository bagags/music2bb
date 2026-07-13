package kugou

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/netx"
	"github.com/gguage/music-to-bb/internal/playlist"
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

	client := newTestClient(t, server,
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
	client := newTestClient(t, server, APIEndpoint{URL: server.URL + "/api", Parameters: true})
	songs, err := client.ParsePlaylist(context.Background(), server.URL+"/share?global_specialid=original")
	if err != nil || len(songs) != 1 || songs[0].Name != "Song" {
		t.Fatalf("ParsePlaylist = %#v, %v", songs, err)
	}
}

func TestParsePlaylistFallsBackToEmbeddedJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html><script>var songData = [{"songname":"Embedded","singername":"Singer"}];</script></html>`))
	}))
	defer server.Close()
	client := newTestClient(t, server)
	songs, err := client.ParsePlaylist(context.Background(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Embedded" {
		t.Fatalf("songs = %#v", songs)
	}
}

func TestParsePlaylistHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	client := newTestClient(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.ParsePlaylist(ctx, server.URL); err != context.Canceled {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestParsePlaylistCancellationWinsOverPartialAPIResult(t *testing.T) {
	var cancel context.CancelFunc
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/share":
			http.Redirect(w, r, "/resolved?specialid=42", http.StatusFound)
		case "/resolved":
			w.Write([]byte("<html></html>"))
		case "/api/partial":
			w.Write([]byte(`{"data":{"total":2,"info":[{"songname":"Partial","singername":"Singer"}]}}`))
		case "/api/cancel":
			cancel()
			<-r.Context().Done()
		}
	}))
	defer server.Close()

	client := newTestClient(t, server,
		APIEndpoint{URL: server.URL + "/api/partial"},
		APIEndpoint{URL: server.URL + "/api/cancel"},
	)
	ctx, stop := context.WithCancel(context.Background())
	cancel = stop
	defer stop()
	result, err := client.ParsePlaylistResult(ctx, server.URL+"/share")
	if err != context.Canceled || len(result.Songs) != 0 {
		t.Fatalf("ParsePlaylistResult = %#v, %v; want no partial result and context.Canceled", result, err)
	}
}

func TestParsePlaylistRejectsInvalidURL(t *testing.T) {
	client := New(nil)
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
		{URL: "https://mobileservice.kugou.com/api/v3/special/song", Parameters: true, Paginated: true},
		{URL: "https://www.kugou.com/yy/special/song/sid={playlist_id}", Method: http.MethodPost},
		{URL: "https://mobileservice.kugou.com/api/v3/plist/speciallist", Parameters: true},
		{URL: "https://mobileservice.kugou.com/api/v3/plist/list", Parameters: true},
		{URL: "https://m.kugou.com/plist/list/{playlist_id}"},
		{URL: "https://wwwapi.kugou.com/playlist/detail/{playlist_id}"},
	}
	if got := defaultAPIEndpoints(); !reflect.DeepEqual(got, want) {
		t.Fatalf("defaultAPIEndpoints = %#v, want %#v", got, want)
	}
}

func TestCurrentPublicEndpointUsesEmptyPost(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/sid=42" {
			w.Write([]byte("<html></html>"))
			return
		}
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if r.ContentLength != 0 {
			t.Errorf("content length = %d, want 0", r.ContentLength)
		}
		w.Write([]byte(`{"data":[{"songname":"Current","singername":"Singer","duration":185000}]}`))
	}))
	defer server.Close()
	client := newTestClient(t, server, APIEndpoint{URL: server.URL + "/sid={playlist_id}", Method: http.MethodPost})
	songs, err := client.ParsePlaylist(context.Background(), server.URL+"/?specialid=42")
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) != 1 || songs[0].Name != "Current" || songs[0].Duration != "3:05" {
		t.Fatalf("songs = %#v", songs)
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

func TestDecodeSongResponseDoesNotApplyBrowserUITextFilter(t *testing.T) {
	songs := decodeSongResponse([]byte(`{"data":{"info":[{"songname":"播放列表","singername":"Singer"}]}}`))
	if len(songs) != 1 || songs[0].Name != "播放列表" {
		t.Fatalf("decodeSongResponse = %#v", songs)
	}
}

func TestDecodeSongPagePreservesRawFieldsAndDecodedMetadata(t *testing.T) {
	result := decodeSongPage([]byte(`{
      "data":{"total":1,"info":[{
        "filename":"Singer A - Song / Mix",
        "singerinfo":[{"name":"Singer A"},{"name":"Singer B"}],
        "albuminfo":{"name":"Album"},
        "timelen":185000,
        "320hash":"hash-value"
      }]}
    }`))
	if result.ExpectedTotal != 1 || len(result.Tracks) != 1 {
		t.Fatalf("raw result = %#v", result)
	}
	track := result.Tracks[0]
	if track.Fields["filename"] != "Singer A - Song / Mix" ||
		!reflect.DeepEqual(track.ArtistNames, []string{"Singer A", "Singer B"}) ||
		track.Album != "Album" || track.Duration != "3:05" || track.Hash != "hash-value" {
		t.Fatalf("track = %#v", track)
	}
}

func TestPaginatedAPIParsesFilenameAndDeclaredTotal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/share" {
			http.Redirect(w, r, "/resolved?specialid=42", http.StatusFound)
			return
		}
		if r.URL.Path != "/api" {
			w.Write([]byte("<html></html>"))
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		start, count := 0, 20
		if page == 2 {
			start, count = 20, 10
		}
		items := make([]map[string]any, 0, count)
		for index := start; index < start+count; index++ {
			items = append(items, map[string]any{
				"filename": fmt.Sprintf("Artist %02d - Song %02d", index, index),
				"hash":     fmt.Sprintf("hash-%02d", index),
				"duration": 180,
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"total": 30, "info": items}})
	}))
	defer server.Close()

	client := newTestClient(t, server, APIEndpoint{URL: server.URL + "/api", Parameters: true, Paginated: true})
	result, err := client.ParsePlaylistResult(context.Background(), server.URL+"/share")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Songs) != 30 || result.ExpectedTotal != 30 {
		t.Fatalf("result = %d/%d, want 30/30", len(result.Songs), result.ExpectedTotal)
	}
	if result.Songs[0].Name != "Song 00" || result.Songs[0].Artist != "Artist 00" {
		t.Fatalf("first song = %#v", result.Songs[0])
	}
}

func TestPaginatedAPIContinuesWhenCleanupReplacesEarlierTrack(t *testing.T) {
	var pages []int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api" {
			w.Write([]byte("<html></html>"))
			return
		}
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pages = append(pages, page)
		var items []map[string]any
		switch page {
		case 1:
			items = []map[string]any{{"songname": "Song"}}
		case 2:
			items = []map[string]any{{"songname": "Song", "singername": "Artist"}}
		case 3:
			items = []map[string]any{{"songname": "Unique", "singername": "Artist"}}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"total": 2, "info": items}})
	}))
	defer server.Close()

	client := newTestClient(t, server, APIEndpoint{URL: server.URL + "/api", Parameters: true, Paginated: true})
	result, err := client.ParsePlaylistResult(context.Background(), server.URL+"/?specialid=42")
	if err != nil {
		t.Fatal(err)
	}
	want := []model.Song{{Name: "Song", Artist: "Artist"}, {Name: "Unique", Artist: "Artist"}}
	if !reflect.DeepEqual(result.Songs, want) || result.ExpectedTotal != 2 {
		t.Fatalf("result = %#v, want songs %#v total 2", result, want)
	}
	if !reflect.DeepEqual(pages, []int{1, 2, 3}) {
		t.Fatalf("pages = %v, want [1 2 3]", pages)
	}
}

func TestSignedCollectionPaginationReturnsAll109Songs(t *testing.T) {
	fixedNow := time.UnixMilli(1_700_000_000_123)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/share" {
			http.Redirect(w, r, "/resolved?specialid=-2147483648&global_specialid=collection-test", http.StatusFound)
			return
		}
		if r.URL.Path == "/resolved" {
			w.Write([]byte("<html></html>"))
			return
		}
		if r.URL.Path != "/collection" {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		wantSignature := collectionParams("collection-test", mustAtoi(t, query.Get("page")), collectionPageSize, fixedNow).Get("signature")
		if query.Get("signature") != wantSignature || query.Get("global_collection_id") != "collection-test" {
			t.Errorf("invalid signed query: %v", query)
		}
		page := mustAtoi(t, query.Get("page"))
		start, count := 0, 100
		if page == 2 {
			start, count = 100, 9
		}
		items := make([]map[string]any, 0, count)
		for index := start; index < start+count; index++ {
			items = append(items, map[string]any{
				"name":       fmt.Sprintf("Singer %03d - Song %03d & Mix", index, index),
				"hash":       fmt.Sprintf("HASH%03d", index),
				"timelen":    185000,
				"singerinfo": []map[string]any{{"name": fmt.Sprintf("Singer %03d", index)}},
			})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": 1, "data": map[string]any{"count": 109, "info": items}})
	}))
	defer server.Close()

	httpClient := netx.New(time.Second, 2, nil)
	httpClient.HTTP = server.Client()
	httpClient.MaxAttempts = 1
	client := New(httpClient,
		WithAPIEndpoints(nil),
		WithCollectionEndpoint(server.URL+"/collection"),
		WithNow(func() time.Time { return fixedNow }),
	)
	result, err := client.ParsePlaylistResult(context.Background(), server.URL+"/share")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Songs) != 109 || result.ExpectedTotal != 109 {
		t.Fatalf("result = %d/%d, want 109/109", len(result.Songs), result.ExpectedTotal)
	}
	if result.Songs[0].Name != "Song 000 & Mix" || result.Songs[0].Artist != "Singer 000" {
		t.Fatalf("first song = %#v", result.Songs[0])
	}
}

func TestCollectionParamsMatchesCurrentH5Signature(t *testing.T) {
	params := collectionParams("collection-test", 1, 100, time.UnixMilli(1_700_000_000_123))
	if got, want := params.Get("signature"), "e8e71dac95600a621cc3b9c1057b7e29"; got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

func TestExtractionHelpersUseNormalizedCountAndPreserveExpectedTotal(t *testing.T) {
	result := betterRawResult(
		playlist.RawResult{Tracks: candidatesFromSongs([]model.Song{{Name: "API"}}), ExpectedTotal: 109},
		playlist.RawResult{Tracks: candidatesFromSongs([]model.Song{{Name: "One"}, {Name: "Two"}})},
	)
	if len(result.Tracks) != 2 || result.ExpectedTotal != 109 {
		t.Fatalf("betterRawResult = %#v", result)
	}

	phantomHeavy := playlist.RawResult{Tracks: candidatesFromSongs([]model.Song{
		{Name: "Song", Artist: "Singer"}, {Name: "Song"}, {Name: "Singer"},
	})}
	valid := playlist.RawResult{Tracks: candidatesFromSongs([]model.Song{{Name: "One"}, {Name: "Two"}})}
	if got := betterRawResult(phantomHeavy, valid); len(got.Tracks) != 2 || got.Tracks[0].Fields["songname"] != "One" {
		t.Fatalf("betterRawResult chose raw rather than normalized count: %#v", got)
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

func TestCleanupSongsRetainsValidTitlePunctuation(t *testing.T) {
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
		{Name: "Duet/A", Artist: "Singer"},
		{Name: "Duplicate", Artist: "One"},
		{Name: "Duplicate", Artist: "Two"},
	}
	if got := CleanupSongs(input); !reflect.DeepEqual(got, want) {
		t.Fatalf("CleanupSongs = %#v, want %#v", got, want)
	}
}

func mustAtoi(t *testing.T, value string) int {
	t.Helper()
	number, err := strconv.Atoi(value)
	if err != nil {
		t.Fatalf("Atoi(%q): %v", value, err)
	}
	return number
}

func newTestClient(t *testing.T, server *httptest.Server, endpoints ...APIEndpoint) *Client {
	t.Helper()
	httpClient := netx.New(time.Second, 2, nil)
	httpClient.HTTP = server.Client()
	httpClient.MaxAttempts = 1
	options := []Option{WithAPIEndpoints(endpoints), WithCollectionEndpoint(server.URL + "/collection")}
	return New(httpClient, options...)
}

func candidatesFromSongs(songs []model.Song) []playlist.TrackCandidate {
	tracks := make([]playlist.TrackCandidate, 0, len(songs))
	for _, song := range songs {
		tracks = append(tracks, playlist.TrackCandidate{
			Fields: map[string]string{"songname": song.Name, "singername": song.Artist},
			Album:  song.Album, Duration: song.Duration, Hash: song.Hash,
		})
	}
	return tracks
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
