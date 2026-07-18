package applemusic

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/netx"
	"github.com/bagags/music2bb-go/internal/playlist"
)

func TestExtractPlaylistFromSerializedServerData(t *testing.T) {
	pageHTML := readFixture(t, "testdata/playlist.html")
	result, err := extractSerializedPlaylist(pageHTML, mustURL(t, "https://music.apple.com/us/playlist/fixture/pl.fixture"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpectedTotal != 5 {
		t.Fatalf("ExpectedTotal = %d, want 5", result.ExpectedTotal)
	}
	songs := playlist.DecodeTracks(result.Tracks, nil)
	want := []model.Song{
		{Name: "First Song", Artist: "First Artist", Album: "First Album", Duration: "3:05"},
		{Name: "Collaboration", Artist: "Artist One & Artist Two", Album: "Shared Album", Duration: "1:01"},
		{Name: "Duplicate Title", Artist: "Artist A", Duration: "2:00"},
		{Name: "Duplicate Title", Artist: "Artist B", Duration: "2:01"},
		{Name: "This deliberately very long Apple Music track title is more than one hundred characters and must remain intact in direct extraction", Artist: "Long Artist", Duration: "1:00"},
	}
	if !reflect.DeepEqual(songs, want) {
		t.Fatalf("songs = %#v, want %#v", songs, want)
	}
}

func TestExtractAlbumCombinesTrackSectionsAndUsesHeaderTitle(t *testing.T) {
	pageHTML := readFixture(t, "testdata/album.html")
	result, err := extractSerializedPlaylist(pageHTML, mustURL(t, "https://music.apple.com/cn/album/mozart-symphonies-nos-35-41/968876434"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpectedTotal != 3 {
		t.Fatalf("ExpectedTotal = %d, want 3", result.ExpectedTotal)
	}
	songs := playlist.DecodeTracks(result.Tracks, nil)
	if len(songs) != 3 {
		t.Fatalf("songs = %#v, want 3 album tracks", songs)
	}
	if songs[0].Name != `Symphony No. 35 in D Major, K. 385 "Haffner": I. Allegro con spirito` ||
		songs[2].Name != `Symphony No. 36 in C Major, K. 425 "Linz": I. Adagio - Allegro spiritoso` {
		t.Fatalf("songs are not in section order: %#v", songs)
	}
	for _, song := range songs {
		if song.Album != "Mozart: Symphonies Nos. 35-41" {
			t.Fatalf("song album = %q, want header title", song.Album)
		}
		if !strings.HasPrefix(song.SourceID, "applemusic:") {
			t.Fatalf("song SourceID = %q", song.SourceID)
		}
	}
}

func TestExtractCollectionRejectsUnsupportedAppleMusicURLs(t *testing.T) {
	tests := []string{
		"https://music.apple.com/cn/artist/wolfgang-amadeus-mozart/164663",
		"https://music.apple.com/cn/album/missing-id/not-a-number",
		"https://music.apple.com/us/playlist/missing-id/not-a-playlist-id",
	}
	for _, rawURL := range tests {
		t.Run(rawURL, func(t *testing.T) {
			if _, err := extractSerializedPlaylist(readFixture(t, "testdata/album.html"), mustURL(t, rawURL)); err == nil {
				t.Fatal("expected unsupported collection URL error")
			}
		})
	}
}

func TestExtractPlaylistReturnsDeclaredPartialResult(t *testing.T) {
	result, err := extractSerializedPlaylist(readFixture(t, "testdata/partial.html"), mustURL(t, "https://music.apple.com/us/playlist/partial/pl.partial"))
	if err != nil {
		t.Fatal(err)
	}
	songs := playlist.DecodeTracks(result.Tracks, nil)
	if result.ExpectedTotal != 3 || len(songs) != 2 || songs[0].Name != "Partial One" || songs[1].Name != "Partial Two" {
		t.Fatalf("partial result = %#v, songs = %#v", result, songs)
	}
}

func TestExtractPlaylistFallsBackToRawTrackItemCount(t *testing.T) {
	pageHTML := applePage("pl.fallback", -1, `
        {"title":"One","artistName":"Artist"},
        {"title":"Two","artistName":"Artist"}`)
	result, err := extractSerializedPlaylist([]byte(pageHTML), mustURL(t, "https://music.apple.com/us/playlist/fallback/pl.fallback"))
	if err != nil {
		t.Fatal(err)
	}
	if result.ExpectedTotal != 2 {
		t.Fatalf("ExpectedTotal = %d, want raw item count 2", result.ExpectedTotal)
	}
}

func TestExtractPlaylistUsesStoreAdamIDAsStableSourceIdentity(t *testing.T) {
	pageHTML := applePage("pl.identity", 1, `
		{"title":"Song","artistName":"Artist","contentDescriptor":{"kind":"song","identifiers":{"storeAdamID":"123456"}}}`)
	result, err := extractSerializedPlaylist([]byte(pageHTML), mustURL(t, "https://music.apple.com/us/playlist/identity/pl.identity"))
	if err != nil {
		t.Fatal(err)
	}
	songs := playlist.DecodeTracks(result.Tracks, nil)
	if len(songs) != 1 || songs[0].SourceID != "applemusic:123456" {
		t.Fatalf("songs = %#v", songs)
	}
}

func TestExtractPlaylistRejectsMissingMalformedAndMismatchedState(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{name: "missing", html: `<script id="other">{}</script>`},
		{name: "data id is not exact script id", html: `<script data-id="serialized-server-data">{}</script>`},
		{name: "malformed", html: `<script id="serialized-server-data">{</script>`},
		{name: "mismatched section", html: applePage("pl.other", 1, `{"title":"Wrong","artistName":"Wrong"}`)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := extractSerializedPlaylist([]byte(test.html), mustURL(t, "https://music.apple.com/us/playlist/fixture/pl.fixture")); err == nil {
				t.Fatal("expected extraction error")
			}
		})
	}
}

func TestClientReportsHTTPFailureAndCancellation(t *testing.T) {
	t.Run("HTTP failure", func(t *testing.T) {
		client := testClient(&http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusNotFound, Status: "404 Not Found", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("missing")),
			}, nil
		})})
		source := mustSource(t, "https://music.apple.com/us/playlist/missing/pl.missing")
		if _, err := client.ExtractPlaylist(context.Background(), source); !IsKind(err, ErrorHTTP) {
			t.Fatalf("error = %v, want HTTP error", err)
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		client := testClient(http.DefaultClient)
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := client.ExtractPlaylist(ctx, mustSource(t, "https://music.apple.com/us/playlist/cancel/pl.cancel")); !errors.Is(err, context.Canceled) {
			t.Fatalf("error = %v, want context.Canceled", err)
		}
	})
}

func TestClientFetchesSharePageWithExpectedHeaders(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.UserAgent(), "Mozilla") || !strings.Contains(request.Header.Get("Accept"), "text/html") {
			t.Errorf("unexpected request headers: %v", request.Header)
		}
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(applePage("pl.headers", 1, `{"title":"Header Song","artistName":"Header Artist"}`))),
		}, nil
	})}
	result, err := testClient(httpClient).ExtractPlaylist(context.Background(), mustSource(t, "https://music.apple.com/us/playlist/headers/pl.headers"))
	if err != nil || len(playlist.DecodeTracks(result.Tracks, nil)) != 1 {
		t.Fatalf("ExtractPlaylist = %#v, %v", result, err)
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func testClient(httpClient *http.Client) *Client {
	shared := netx.New(time.Second, 2, nil)
	shared.HTTP = httpClient
	shared.MaxAttempts = 1
	return New(shared)
}

func mustSource(t *testing.T, rawURL string) playlist.Source {
	t.Helper()
	source, err := playlist.ParseSource(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func mustURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func readFixture(t *testing.T, path string) []byte {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func applePage(playlistID string, trackCount int, items string) string {
	header := ""
	if trackCount >= 0 {
		header = fmt.Sprintf(`{"itemKind":"containerDetailHeaderLockup","items":[{"trackCount":%d,"contentDescriptor":{"kind":"playlist","identifiers":{"storeAdamID":%q}}}]},`, trackCount, playlistID)
	}
	return fmt.Sprintf(`<script id="serialized-server-data">{"data":[{"data":{"sections":[%s{"itemKind":"trackLockup","containerContentDescriptor":{"kind":"playlist","identifiers":{"storeAdamID":%q}},"items":[%s]}]}}]}</script>`, header, playlistID, items)
}
