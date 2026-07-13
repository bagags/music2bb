//go:build live

package kugou

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/netx"
)

func TestLiveDirectExtraction(t *testing.T) {
	rawURL := os.Getenv("MUSIC2BB_TEST_KUGOU_URL")
	if rawURL == "" {
		t.Skip("MUSIC2BB_TEST_KUGOU_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sharedHTTP := netx.New(15*time.Second, 4, netx.NewTokenLimiter(4, 1))
	defer sharedHTTP.CloseIdleConnections()
	songs, err := New(sharedHTTP).ParsePlaylist(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) == 0 {
		t.Fatal("live direct extraction returned no songs")
	}
	if rawCount := os.Getenv("MUSIC2BB_TEST_KUGOU_COUNT"); rawCount != "" {
		expected, err := strconv.Atoi(rawCount)
		if err != nil {
			t.Fatalf("invalid MUSIC2BB_TEST_KUGOU_COUNT: %v", err)
		}
		if len(songs) != expected {
			t.Fatalf("live direct extraction returned %d songs, want %d", len(songs), expected)
		}
	}
}
