//go:build live

package bilibili

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"testing"
	"time"
)

type liveCaptureTransport struct {
	requestURL string
	response   []byte
}

func (t *liveCaptureTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := http.DefaultTransport.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 16<<20))
	response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	t.requestURL = request.URL.String()
	t.response = append(t.response[:0], data...)
	return response, nil
}

// This test is intentionally excluded from the default suite. Run it with:
//
//	MUSIC2BB_TEST_BVID=BV... go test -tags=live ./internal/bilibili -run TestLiveReadOnlyVideoDetail
func TestLiveReadOnlyVideoDetail(t *testing.T) {
	bvid := os.Getenv("MUSIC2BB_TEST_BVID")
	if bvid == "" {
		t.Skip("MUSIC2BB_TEST_BVID is not set")
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

// This read-only canary catches Bilibili search endpoint and signing drift.
// Run it with:
//
//	MUSIC2BB_TEST_SEARCH_QUERY='贝多芬 第五交响曲' MUSIC2BB_TEST_COOKIE_FILE=/path/to/bilibili.json go test -tags=live ./internal/bilibili -run TestLiveReadOnlySearch
func TestLiveReadOnlySearch(t *testing.T) {
	query := os.Getenv("MUSIC2BB_TEST_SEARCH_QUERY")
	if query == "" {
		t.Skip("MUSIC2BB_TEST_SEARCH_QUERY is not set")
	}
	capture := &liveCaptureTransport{}
	client, err := New(Config{Timeout: 30 * time.Second, CookieFile: os.Getenv("MUSIC2BB_TEST_COOKIE_FILE"), SearchHTTP: &http.Client{Transport: capture}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if client.cookieFile != "" {
		if ok, err := client.LoadCookies(); err != nil || !ok {
			t.Fatalf("load cookies: loaded=%v err=%v", ok, err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	videos, err := client.Search(ctx, query, SearchOptions{Page: 1, PageSize: 20, SearchType: "video", Order: "totalrank"})
	if err != nil {
		t.Fatal(err)
	}
	if len(videos) == 0 || videos[0].BVID == "" || videos[0].Title == "" {
		response := capture.response
		if len(response) > 2048 {
			response = response[:2048]
		}
		t.Fatalf("incomplete live search response: %#v; request=%s response=%s", videos, capture.requestURL, response)
	}
}
