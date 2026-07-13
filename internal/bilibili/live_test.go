//go:build live

package bilibili

import (
	"context"
	"os"
	"testing"
	"time"
)

// This test is intentionally excluded from the default suite. Run it with:
//
//	KG2BB_TEST_BVID=BV... go test -tags=live ./internal/bilibili -run TestLiveReadOnlyVideoDetail
func TestLiveReadOnlyVideoDetail(t *testing.T) {
	bvid := os.Getenv("KG2BB_TEST_BVID")
	if bvid == "" {
		t.Skip("KG2BB_TEST_BVID is not set")
	}
	client, err := New(Config{Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	video, err := client.VideoDetail(ctx, bvid)
	if err != nil {
		t.Fatal(err)
	}
	if video.BVID != bvid || video.AID == 0 || video.Title == "" {
		t.Fatalf("incomplete live video response: %#v", video)
	}
}
