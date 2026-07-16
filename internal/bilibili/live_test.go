//go:build live

package bilibili

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

type liveCaptureTransport struct {
	requestURL string
	cookies    []string
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
	t.cookies = append(t.cookies, request.URL.Path+"\t"+request.Header.Get("Cookie"))
	t.response = append(t.response[:0], data...)
	return response, nil
}

// This opt-in anonymous canary performs a real search after seeding the
// account jar with sentinel credentials. It proves the outgoing anonymous
// search path does not carry account cookies. Run it with:
//
//	MUSIC2BB_RUN_ANON_SEARCH_CANARY=1 MUSIC2BB_TEST_SEARCH_QUERY='贝多芬 第五交响曲' go test -count=1 -tags=live ./internal/bilibili -run TestLiveAnonymousSearchExcludesAccountCookies
func TestLiveAnonymousSearchExcludesAccountCookies(t *testing.T) {
	if os.Getenv("MUSIC2BB_RUN_ANON_SEARCH_CANARY") != "1" {
		t.Skip("MUSIC2BB_RUN_ANON_SEARCH_CANARY is not set to 1")
	}
	query := os.Getenv("MUSIC2BB_TEST_SEARCH_QUERY")
	if query == "" {
		t.Skip("MUSIC2BB_TEST_SEARCH_QUERY is not set")
	}
	capture := &liveCaptureTransport{}
	client, err := New(Config{Timeout: 30 * time.Second, SearchHTTP: &http.Client{Transport: capture}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	client.applyCookieString("SESSDATA=account-sentinel; bili_jct=csrf-sentinel; DedeUserID=123")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := client.Search(ctx, query, SearchOptions{Page: 1, PageSize: 5, SearchType: "video", Order: "totalrank", Identity: SearchIdentityAnonymous}); err != nil {
		response := capture.response
		if len(response) > 2048 {
			response = response[:2048]
		}
		t.Fatalf("anonymous search: %v; request=%s cookies=%q response=%s", err, capture.requestURL, capture.cookies, response)
	}
	if len(capture.cookies) == 0 {
		t.Fatal("anonymous canary captured no outgoing requests")
	}
	for _, request := range capture.cookies {
		for _, forbidden := range []string{"SESSDATA=", "bili_jct=", "DedeUserID="} {
			if strings.Contains(request, forbidden) {
				t.Fatalf("anonymous request contains account cookie %s: %s", forbidden, request)
			}
		}
	}
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

// This read-only canary catches authenticated Bilibili WBI search and signing
// drift.
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
	videos, err := client.Search(ctx, query, SearchOptions{Page: 1, PageSize: 20, SearchType: "video", Order: "totalrank", Identity: SearchIdentitySession})
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
