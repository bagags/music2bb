//go:build live

package applemusic

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/playlist"
)

func TestLiveAppleMusicExtractionMatchesDeclaredCount(t *testing.T) {
	rawURL := os.Getenv("MUSIC2BB_TEST_APPLE_MUSIC_URL")
	if rawURL == "" {
		t.Skip("MUSIC2BB_TEST_APPLE_MUSIC_URL is not set")
	}
	source, err := playlist.ParseSource(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if !Identifier().MatchesURL(source.URL()) {
		t.Fatal("MUSIC2BB_TEST_APPLE_MUSIC_URL must use music.apple.com")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	result, err := New(nil).ExtractPlaylist(ctx, source)
	if err != nil {
		t.Fatal(err)
	}
	songs := playlist.DecodeTracks(result.Tracks, nil)
	if result.ExpectedTotal <= 0 {
		t.Fatalf("page did not declare a positive trackCount: %#v", result)
	}
	if len(songs) != result.ExpectedTotal {
		t.Fatalf("extracted %d songs, page declared %d", len(songs), result.ExpectedTotal)
	}
}
