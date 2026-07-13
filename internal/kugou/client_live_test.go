//go:build live

package kugou

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/netx"
)

func TestLiveDirectExtraction(t *testing.T) {
	rawURL := os.Getenv("KG2BB_TEST_KUGOU_URL")
	if rawURL == "" {
		t.Skip("KG2BB_TEST_KUGOU_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	sharedHTTP := netx.New(15*time.Second, 4, netx.NewTokenLimiter(4, 1))
	defer sharedHTTP.CloseIdleConnections()
	songs, err := New(sharedHTTP, nil).ParsePlaylist(ctx, rawURL)
	if err != nil {
		t.Fatal(err)
	}
	if len(songs) == 0 {
		t.Fatal("live direct extraction returned no songs")
	}
}
