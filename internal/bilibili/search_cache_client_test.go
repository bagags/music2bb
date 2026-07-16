package bilibili

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type cacheRoundTripper func(*http.Request) (*http.Response, error)

func (f cacheRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestClientPersistentSearchCacheTTLRefreshAndAnonymousPartition(t *testing.T) {
	root := t.TempDir()
	anonymousPath := filepath.Join(root, "anonymous.json")
	writeAnonymousDeviceCookie(t, anonymousPath, "device-one")
	current := time.Unix(1000, 0).UTC()
	var searchCalls atomic.Int32
	transport := cacheRoundTripper(func(request *http.Request) (*http.Response, error) {
		body := `{"code":-101,"data":{"wbi_img":{"img_url":"https://i/7cd084941338484aae1ad9425b84077c.png","sub_url":"https://i/4932caff0ff746eab6f01bf08b70ac45.png"}}}`
		status := http.StatusOK
		switch request.URL.Path {
		case "/search":
			searchCalls.Add(1)
			keyword := request.URL.Query().Get("keyword")
			switch keyword {
			case "empty":
				body = `{"code":0,"data":{"result":[]}}`
			case "error":
				status, body = http.StatusInternalServerError, `{"code":-500,"message":"failure"}`
			default:
				body = fmt.Sprintf(`{"code":0,"data":{"result":[{"bvid":"BV-%s","title":"%s"}]}}`, keyword, keyword)
			}
		case "/nav":
		default:
			return nil, fmt.Errorf("unexpected request %s", request.URL)
		}
		return &http.Response{
			StatusCode: status, Status: fmt.Sprintf("%d", status), Header: make(http.Header),
			Body: io.NopCloser(strings.NewReader(body)), Request: request,
		}, nil
	})
	cache := NewFileSearchCache(filepath.Join(root, "search", "v1"))
	newClient := func() *Client {
		client, err := New(Config{
			Endpoints: endpoints("https://example.test"), AnonymousCookieFile: anonymousPath,
			AccountHTTP: &http.Client{Transport: transport}, SearchHTTP: &http.Client{Transport: transport},
			SearchCache: cache, Now: func() time.Time { return current }, MaxAttempts: 1,
		})
		if err != nil {
			t.Fatal(err)
		}
		return client
	}

	first := newClient()
	result, err := first.SearchWithResult(context.Background(), "query", SearchOptions{})
	if err != nil || result.CacheHit || searchCalls.Load() != 1 {
		t.Fatalf("first search = %#v, %v, calls %d", result, err, searchCalls.Load())
	}
	first.CloseIdleConnections()

	second := newClient()
	result, err = second.SearchWithResult(context.Background(), "query", SearchOptions{})
	if err != nil || !result.CacheHit || searchCalls.Load() != 1 {
		t.Fatalf("persistent hit = %#v, %v, calls %d", result, err, searchCalls.Load())
	}
	current = current.Add(7 * 24 * time.Hour)
	result, err = second.SearchWithResult(context.Background(), "query", SearchOptions{})
	if err != nil || result.CacheHit || searchCalls.Load() != 2 {
		t.Fatalf("expired positive = %#v, %v, calls %d", result, err, searchCalls.Load())
	}

	if result, err = second.SearchWithResult(context.Background(), "empty", SearchOptions{}); err != nil || result.CacheHit {
		t.Fatalf("first empty search = %#v, %v", result, err)
	}
	emptyCalls := searchCalls.Load()
	current = current.Add(time.Hour - time.Nanosecond)
	if result, err = second.SearchWithResult(context.Background(), "empty", SearchOptions{}); err != nil || !result.CacheHit || searchCalls.Load() != emptyCalls {
		t.Fatalf("fresh empty = %#v, %v, calls %d", result, err, searchCalls.Load())
	}
	current = current.Add(time.Nanosecond)
	if result, err = second.SearchWithResult(context.Background(), "empty", SearchOptions{}); err != nil || result.CacheHit || searchCalls.Load() != emptyCalls+1 {
		t.Fatalf("expired empty = %#v, %v, calls %d", result, err, searchCalls.Load())
	}

	beforeErrors := searchCalls.Load()
	for range 2 {
		if _, err := second.SearchWithResult(context.Background(), "error", SearchOptions{}); err == nil {
			t.Fatal("expected remote search error")
		}
	}
	if searchCalls.Load() != beforeErrors+2 {
		t.Fatalf("errors were cached; calls = %d", searchCalls.Load())
	}

	beforeRefresh := searchCalls.Load()
	if result, err = second.SearchWithResult(context.Background(), "query", SearchOptions{CachePolicy: SearchCacheRefresh}); err != nil || result.CacheHit || searchCalls.Load() != beforeRefresh+1 {
		t.Fatalf("refresh = %#v, %v, calls %d", result, err, searchCalls.Load())
	}
	writeAnonymousDeviceCookie(t, anonymousPath, "device-two")
	third := newClient()
	if result, err = third.SearchWithResult(context.Background(), "query", SearchOptions{}); err != nil || result.CacheHit || searchCalls.Load() != beforeRefresh+2 {
		t.Fatalf("new anonymous partition = %#v, %v, calls %d", result, err, searchCalls.Load())
	}
}

func writeAnonymousDeviceCookie(t *testing.T, path, value string) {
	t.Helper()
	if err := (fileCookieStore{path: path}).Save([]CookieRecord{{Name: "buvid3", Value: value, Domain: "example.test", Path: "/"}}); err != nil {
		t.Fatal(err)
	}
}
