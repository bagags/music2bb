package bilibili

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func endpoints(base string) Endpoints {
	return Endpoints{
		Home: base + "/home", Nav: base + "/nav", Search: base + "/search",
		VideoDetail: base + "/detail", QRGenerate: base + "/qr/generate", QRPoll: base + "/qr/poll",
		FavoriteList: base + "/favorites", FavoriteCreate: base + "/favorites/create",
		FavoriteDeal: base + "/favorites/deal", FavoriteResourceList: base + "/favorites/resources",
		FavoriteResourceDel: base + "/favorites/resources/delete", FavoriteDelete: base + "/favorites/delete",
	}
}

func testClient(t *testing.T, server *httptest.Server, config Config) *Client {
	t.Helper()
	config.Endpoints = endpoints(server.URL)
	config.AccountHTTP = server.Client()
	config.SearchHTTP = server.Client()
	config.CookieFile = filepath.Join(t.TempDir(), "bilibili.json")
	if config.Sleep == nil {
		config.Sleep = func(context.Context, time.Duration) error { return nil }
	}
	if config.MaxAttempts == 0 {
		config.MaxAttempts = 1
	}
	client, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.CloseIdleConnections)
	return client
}

func writeJSON(w http.ResponseWriter, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.Write(data)
}

func TestSearchUsesOnlyFingerprintCookiesAndDeduplicatesConcurrentRequests(t *testing.T) {
	var searchCalls atomic.Int32
	searchFixture := fixture(t, "search.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home":
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "fingerprint", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "secret", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "bili_jct", Value: "csrf", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/search":
			searchCalls.Add(1)
			if cookie, err := r.Cookie("buvid3"); err != nil || cookie.Value != "fingerprint" {
				t.Errorf("search fingerprint cookie = %v, %v", cookie, err)
			}
			if _, err := r.Cookie("SESSDATA"); err == nil {
				t.Error("authenticated SESSDATA leaked into search request")
			}
			if _, err := r.Cookie("bili_jct"); err == nil {
				t.Error("CSRF cookie leaked into search request")
			}
			writeJSON(w, searchFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{})

	const goroutines = 12
	results := make([][]model.Video, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], errs[index] = client.Search(context.Background(), "fixture", SearchOptions{})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if calls := searchCalls.Load(); calls != 1 {
		t.Fatalf("search calls = %d, want 1", calls)
	}
	if got := results[0][0]; got.Title != "Fixture Song" || got.PlayCount != 1200 || !got.IsVerified || !got.IsOfficial {
		t.Fatalf("parsed video = %#v", got)
	}
	results[0][0].Tags[0] = "mutated"
	cached, err := client.Search(context.Background(), "fixture", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cached[0].Tags[0] == "mutated" {
		t.Fatal("cache returned an aliased tag slice")
	}
}

func TestCookiePersistenceIsPythonCompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/home" {
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "persisted", Path: "/"})
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/search" {
			writeJSON(w, fixture(t, "search.json"))
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := testClient(t, server, Config{})
	if _, err := client.Search(context.Background(), "fixture", SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := client.SaveCookies(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(client.cookieFile)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal(data, &records); err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0]["name"] != "buvid3" || records[0]["path"] != "/" {
		t.Fatalf("cookie JSON = %s", data)
	}
	if info, err := os.Stat(client.cookieFile); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cookie permissions = %v, err = %v", info.Mode().Perm(), err)
	}

	reloaded, err := New(Config{Endpoints: endpoints(server.URL), CookieFile: client.cookieFile, AccountHTTP: server.Client(), SearchHTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if ok, err := reloaded.LoadCookies(); err != nil || !ok {
		t.Fatalf("LoadCookies() = %v, %v", ok, err)
	}
}

func TestQRLoginEmitsPayloadAndUsesInjectedSleep(t *testing.T) {
	var polls atomic.Int32
	navFixture := fixture(t, "nav.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/qr/generate":
			io.WriteString(w, `{"code":0,"data":{"url":"https://qr.example/payload","qrcode_key":"key"}}`)
		case "/qr/poll":
			switch polls.Add(1) {
			case 1:
				io.WriteString(w, `{"code":0,"data":{"code":86101}}`)
			case 2:
				io.WriteString(w, `{"code":0,"data":{"code":86090}}`)
			default:
				io.WriteString(w, `{"code":0,"data":{"code":0,"cookie":"bili_jct=csrf; SESSDATA=session"}}`)
			}
		case "/nav":
			writeJSON(w, navFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	var sleeps atomic.Int32
	client := testClient(t, server, Config{Sleep: func(context.Context, time.Duration) error { sleeps.Add(1); return nil }})
	var events []Event
	account, err := client.QRLogin(context.Background(), LoginOptions{
		Timeout: time.Minute, PollInterval: time.Second,
		Observer: ObserverFunc(func(event Event) { events = append(events, event) }),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !account.LoggedIn || account.MID != 12345 || account.Name != "fixture-user" {
		t.Fatalf("account = %#v", account)
	}
	if sleeps.Load() != 2 || polls.Load() != 3 {
		t.Fatalf("sleeps/polls = %d/%d", sleeps.Load(), polls.Load())
	}
	if len(events) != 2 || events[0].Kind != EventQRPayload || events[0].QRPayload == "" || events[1].Kind != EventQRScanned {
		t.Fatalf("events = %#v", events)
	}
}

func TestWBISigningMatchesReferenceVector(t *testing.T) {
	params := url.Values{"foo": {"114"}, "bar": {"514"}, "zab": {"1919810"}}
	signed := signWBI(params, "7cd084941338484aae1ad9425b84077c", "4932caff0ff746eab6f01bf08b70ac45", 1702204169)
	if got, want := signed.Get("w_rid"), "8f6f2b5b3d485fe1886cec6a0be8c5d4"; got != want {
		t.Fatalf("w_rid = %q, want %q", got, want)
	}
	if params.Get("wts") != "" {
		t.Fatal("signWBI mutated its input")
	}
}

func TestFavoriteOperationsPreserveOrderedPartialFailuresAndDoNotRetryWrites(t *testing.T) {
	navFixture := fixture(t, "nav.json")
	detailFixture := fixture(t, "video_detail.json")
	favoritesFixture := fixture(t, "favorites.json")
	var dealCalls sync.Map
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nav":
			writeJSON(w, navFixture)
		case "/detail":
			writeJSON(w, detailFixture)
		case "/favorites":
			writeJSON(w, favoritesFixture)
		case "/favorites/create":
			if err := r.ParseForm(); err != nil {
				t.Error(err)
			}
			if r.Form.Get("privacy") != "1" || r.Form.Get("csrf") != "csrf" {
				t.Errorf("create form = %v", r.Form)
			}
			io.WriteString(w, `{"code":0,"data":{"id":900}}`)
		case "/favorites/deal":
			r.ParseForm()
			rid := r.Form.Get("rid")
			counter, _ := dealCalls.LoadOrStore(rid, new(atomic.Int32))
			counter.(*atomic.Int32).Add(1)
			if r.Form.Get("w_rid") == "" || r.Form.Get("csrf") != "csrf" {
				t.Errorf("deal form missing signature or csrf: %v", r.Form)
			}
			switch rid {
			case "22":
				io.WriteString(w, `{"code":-400,"message":"fixture rejection"}`)
			case "33":
				http.Error(w, "ambiguous", http.StatusServiceUnavailable)
			default:
				io.WriteString(w, `{"code":0,"data":{}}`)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{MaxAttempts: 3, Now: func() time.Time { return time.Unix(1702204169, 0) }})
	client.applyCookieString("bili_jct=csrf; SESSDATA=session")

	video, err := client.VideoDetail(context.Background(), "BV1fixture")
	if err != nil || video.AID != 101 || video.Duration != "3:05" {
		t.Fatalf("VideoDetail = %#v, %v", video, err)
	}
	favorites, err := client.ListFavorites(context.Background())
	if err != nil || len(favorites) != 1 || favorites[0].ID != 765 || favorites[0].MediaCount != 2 {
		t.Fatalf("ListFavorites = %#v, %v", favorites, err)
	}
	created, err := client.CreateFavorite(context.Background(), CreateFavoriteRequest{Title: " Canary ", Private: true})
	if err != nil || created.ID != 900 || created.Title != "Canary" {
		t.Fatalf("CreateFavorite = %#v, %v", created, err)
	}
	result, err := client.AddToFavorite(context.Background(), 900, []model.Video{
		{BVID: "BV-ok", AID: 11}, {BVID: "BV-api", AID: 22}, {BVID: "BV-http", AID: 33},
	})
	var partial *PartialWriteError
	if !errors.As(err, &partial) {
		t.Fatalf("AddToFavorite error = %T %v", err, err)
	}
	if !reflect.DeepEqual(result.Succeeded, []string{"BV-ok"}) || len(result.Failed) != 2 || result.Failed[0].Index != 1 || result.Failed[1].Index != 2 {
		t.Fatalf("AddToFavorite result = %#v", result)
	}
	dealCalls.Range(func(key, value any) bool {
		if calls := value.(*atomic.Int32).Load(); calls != 1 {
			t.Errorf("POST rid %s called %d times, want 1", key, calls)
		}
		return true
	})
}

func TestCanaryCleanupOperations(t *testing.T) {
	var formsMu sync.Mutex
	forms := map[string]url.Values{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/favorites/resources":
			io.WriteString(w, `{"code":0,"data":{"medias":[{"id":101,"bvid":"BV1fixture","title":"Fixture"}],"has_more":false}}`)
		case "/favorites/resources/delete", "/favorites/delete":
			r.ParseForm()
			formsMu.Lock()
			forms[r.URL.Path] = r.Form
			formsMu.Unlock()
			io.WriteString(w, `{"code":0,"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{})
	client.applyCookieString("bili_jct=csrf")
	resources, err := client.ListFavoriteResources(context.Background(), 900)
	if err != nil || len(resources) != 1 || resources[0].AID != 101 {
		t.Fatalf("ListFavoriteResources = %#v, %v", resources, err)
	}
	if err := client.RemoveFavoriteResources(context.Background(), 900, []int64{101}); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteFavorite(context.Background(), 900); err != nil {
		t.Fatal(err)
	}
	if got := forms["/favorites/resources/delete"].Get("resources"); got != "101:2" {
		t.Fatalf("resources = %q", got)
	}
	if got := forms["/favorites/delete"].Get("media_ids"); got != "900" {
		t.Fatalf("media_ids = %q", got)
	}
}

func TestQRLoginHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/generate") {
			io.WriteString(w, `{"code":0,"data":{"url":"payload","qrcode_key":"key"}}`)
			return
		}
		io.WriteString(w, `{"code":0,"data":{"code":86101}}`)
	}))
	defer server.Close()
	client := testClient(t, server, Config{Sleep: func(ctx context.Context, _ time.Duration) error {
		<-ctx.Done()
		return ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := client.QRLogin(ctx, LoginOptions{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

func TestQRLoginUsesInjectedClockForTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/generate") {
			io.WriteString(w, `{"code":0,"data":{"url":"payload","qrcode_key":"key"}}`)
			return
		}
		io.WriteString(w, `{"code":0,"data":{"code":86101}}`)
	}))
	defer server.Close()
	current := time.Unix(100, 0)
	client := testClient(t, server, Config{
		Now: func() time.Time { return current },
		Sleep: func(context.Context, time.Duration) error {
			current = current.Add(10 * time.Second)
			return nil
		},
	})
	_, err := client.QRLogin(context.Background(), LoginOptions{Timeout: 5 * time.Second, PollInterval: time.Second})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}
