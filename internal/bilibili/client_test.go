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

	"github.com/bagags/music2bb-go/internal/model"
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
		Home: base + "/home", Nav: base + "/nav", AnonymousSearch: base + "/search", Search: base + "/search",
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

func TestDefaultSearchEndpointsSeparateAnonymousH5AndSessionWBI(t *testing.T) {
	if got, want := DefaultEndpoints().AnonymousSearch, "https://api.bilibili.com/x/web-interface/search/all/v2"; got != want {
		t.Fatalf("anonymous search endpoint = %q, want %q", got, want)
	}
	if got, want := DefaultEndpoints().Search, "https://api.bilibili.com/x/web-interface/wbi/search/type"; got != want {
		t.Fatalf("session search endpoint = %q, want %q", got, want)
	}
}

func TestAnonymousSearchExcludesAccountCookiesAndDeduplicatesConcurrentRequests(t *testing.T) {
	var searchCalls atomic.Int32
	searchFixture := fixture(t, "search.json")
	navFixture := fixture(t, "nav.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home":
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "fingerprint", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "b_nut", Value: "anonymous", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "SESSDATA", Value: "secret", Path: "/"})
			http.SetCookie(w, &http.Cookie{Name: "bili_jct", Value: "csrf", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/nav":
			writeJSON(w, navFixture)
		case "/search":
			searchCalls.Add(1)
			if cookie, err := r.Cookie("buvid3"); err != nil || cookie.Value != "fingerprint" {
				t.Errorf("search fingerprint cookie = %v, %v", cookie, err)
			}
			if cookie, err := r.Cookie("b_nut"); err != nil || cookie.Value != "anonymous" {
				t.Errorf("search anonymous cookie = %v, %v", cookie, err)
			}
			if cookie, err := r.Cookie("SESSDATA"); !errors.Is(err, http.ErrNoCookie) {
				t.Errorf("anonymous search leaked session cookie = %v, %v", cookie, err)
			}
			if cookie, err := r.Cookie("bili_jct"); !errors.Is(err, http.ErrNoCookie) {
				t.Errorf("anonymous search leaked CSRF cookie = %v, %v", cookie, err)
			}
			if got := r.URL.Query().Get("keyword"); got != "中文 fixture" {
				t.Errorf("search keyword = %q", got)
			}
			if got := r.URL.Query().Get("platform"); got != "h5" {
				t.Errorf("search platform = %q", got)
			}
			if got := r.URL.Query().Get("web_location"); got != "1430654" {
				t.Errorf("search web_location = %q", got)
			}
			if r.URL.Query().Get("wts") != "" || r.URL.Query().Get("w_rid") != "" {
				t.Errorf("anonymous H5 search was unexpectedly signed: %v", r.URL.Query())
			}
			writeJSON(w, searchFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{Now: func() time.Time { return time.Unix(1702204169, 0) }})

	const goroutines = 12
	results := make([][]model.Video, goroutines)
	errs := make([]error, goroutines)
	var wg sync.WaitGroup
	for index := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[index], errs[index] = client.Search(context.Background(), "中文 fixture", SearchOptions{})
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
	cached, err := client.Search(context.Background(), "中文 fixture", SearchOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if cached[0].Tags[0] == "mutated" {
		t.Fatal("cache returned an aliased tag slice")
	}
	anonymousPath := filepath.Join(filepath.Dir(client.cookieFile), "bilibili-anonymous.json")
	persisted, err := os.ReadFile(anonymousPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(persisted), "SESSDATA") || strings.Contains(string(persisted), "bili_jct") || strings.Contains(string(persisted), "secret") {
		t.Fatalf("anonymous fingerprint file contains account state: %s", persisted)
	}
}

func TestSearchIdentityKeepsSessionAndAnonymousJarsIsolated(t *testing.T) {
	searchFixture := fixture(t, "search.json")
	navFixture := fixture(t, "nav.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home":
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "device", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/nav":
			writeJSON(w, navFixture)
		case "/search":
			_, sessionErr := r.Cookie("SESSDATA")
			_, csrfErr := r.Cookie("bili_jct")
			if r.URL.Query().Get("keyword") == "session" {
				if sessionErr != nil || csrfErr != nil {
					t.Errorf("session identity cookies missing: SESSDATA=%v bili_jct=%v", sessionErr, csrfErr)
				}
				if r.URL.Query().Get("wts") == "" || r.URL.Query().Get("w_rid") == "" {
					t.Errorf("session search query is unsigned: %v", r.URL.Query())
				}
				if got := r.URL.Query().Get("web_location"); got != "1430654" {
					t.Errorf("session search web_location = %q", got)
				}
			} else if !errors.Is(sessionErr, http.ErrNoCookie) || !errors.Is(csrfErr, http.ErrNoCookie) {
				t.Errorf("anonymous identity leaked account cookies: SESSDATA=%v bili_jct=%v", sessionErr, csrfErr)
			} else if got := r.URL.Query().Get("platform"); got != "h5" {
				t.Errorf("anonymous search platform = %q", got)
			}
			writeJSON(w, searchFixture)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{})
	client.applyCookieString("SESSDATA=secret; bili_jct=csrf")
	if _, err := client.Search(context.Background(), "session", SearchOptions{Identity: SearchIdentitySession}); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Search(context.Background(), "anonymous", SearchOptions{Identity: SearchIdentityAnonymous}); err != nil {
		t.Fatal(err)
	}
}

func TestCookiePersistenceIsPythonCompatible(t *testing.T) {
	navFixture := fixture(t, "nav.json")
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
		if r.URL.Path == "/nav" {
			writeJSON(w, navFixture)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	client := testClient(t, server, Config{})
	if _, err := client.Search(context.Background(), "fixture", SearchOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := client.SaveAnonymousCookies(); err != nil {
		t.Fatal(err)
	}
	anonymousPath := filepath.Join(filepath.Dir(client.cookieFile), "bilibili-anonymous.json")
	data, err := os.ReadFile(anonymousPath)
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
	if info, err := os.Stat(anonymousPath); err != nil || info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("cookie permissions = %v, err = %v", info.Mode().Perm(), err)
	}

	reloaded, err := New(Config{Endpoints: endpoints(server.URL), CookieFile: client.cookieFile, AnonymousCookieFile: anonymousPath, AccountHTTP: server.Client(), SearchHTTP: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	home, err := url.Parse(reloaded.endpoints.Home)
	if err != nil {
		t.Fatal(err)
	}
	if cookie, err := findCookie(reloaded.searchJar.Cookies(home), "buvid3"); err != nil || cookie.Value != "persisted" {
		t.Fatalf("reloaded anonymous fingerprint = %v, %v", cookie, err)
	}
}

func findCookie(cookies []*http.Cookie, name string) (*http.Cookie, error) {
	for _, cookie := range cookies {
		if cookie.Name == name {
			return cookie, nil
		}
	}
	return nil, http.ErrNoCookie
}

func TestLogoutClearsPersistedAndInMemoryCookies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client := testClient(t, server, Config{})
	client.applyCookieString("bili_jct=csrf; SESSDATA=session")
	if err := client.SaveCookies(); err != nil {
		t.Fatal(err)
	}
	if err := client.Logout(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(client.cookieFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("cookie file still exists: %v", err)
	}
	home, err := url.Parse(client.endpoints.Home)
	if err != nil {
		t.Fatal(err)
	}
	if cookies := client.accountJar.Cookies(home); len(cookies) != 0 {
		t.Fatalf("in-memory cookies = %#v", cookies)
	}
	if ok, err := client.LoadCookies(); ok || !errors.Is(err, ErrNoCookieFile) {
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

func TestWBISigningPreservesUnicodeAndSpaces(t *testing.T) {
	params := url.Values{"keyword": {"贝多芬! 第五(交响)*曲'"}}
	signed := signWBI(params, "7cd084941338484aae1ad9425b84077c", "4932caff0ff746eab6f01bf08b70ac45", 1702204169)
	if got, want := signed.Get("keyword"), "贝多芬 第五交响曲"; got != want {
		t.Fatalf("signed keyword = %q, want %q", got, want)
	}
	if got := params.Get("keyword"); got != "贝多芬! 第五(交响)*曲'" {
		t.Fatalf("signWBI mutated original keyword: %q", got)
	}
	if signed.Get("w_rid") == "" || signed.Get("wts") != "1702204169" {
		t.Fatalf("signed params = %v", signed)
	}
}

func TestWBIKeysAcceptAnonymousNavDataWithoutWeakeningAccountChecks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/nav" {
			http.NotFound(w, r)
			return
		}
		io.WriteString(w, `{"code":-101,"message":"账号未登录","data":{"isLogin":false,"wbi_img":{"img_url":"https://i/7cd084941338484aae1ad9425b84077c.png","sub_url":"https://i/4932caff0ff746eab6f01bf08b70ac45.png"}}}`)
	}))
	defer server.Close()
	client := testClient(t, server, Config{})

	img, sub, err := client.wbiKeys(context.Background(), SearchIdentityAnonymous)
	if err != nil || img != "7cd084941338484aae1ad9425b84077c" || sub != "4932caff0ff746eab6f01bf08b70ac45" {
		t.Fatalf("wbiKeys = %q, %q, %v", img, sub, err)
	}
	_, err = client.Account(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != -101 {
		t.Fatalf("Account error = %T %v", err, err)
	}
}

func TestAddToFavoriteUsesSessionWBIIdentity(t *testing.T) {
	navFixture := fixture(t, "nav.json")
	var navHadSession atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/nav":
			if _, err := r.Cookie("SESSDATA"); err == nil {
				navHadSession.Store(true)
			}
			writeJSON(w, navFixture)
		case "/favorites/deal":
			io.WriteString(w, `{"code":0,"data":{}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{Now: func() time.Time { return time.Unix(1702204169, 0) }})
	client.applyCookieString("bili_jct=csrf; SESSDATA=session")
	result, err := client.AddToFavorite(context.Background(), 900, []model.Video{{BVID: "BV1fixture", AID: 101}})
	if err != nil || !reflect.DeepEqual(result.Succeeded, []string{"BV1fixture"}) {
		t.Fatalf("AddToFavorite = %#v, %v", result, err)
	}
	if !navHadSession.Load() {
		t.Fatal("favorite WBI keys were fetched without the authenticated session")
	}
}

func TestHTTPErrorPreservesBilibiliDetails(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/detail" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("x-sec-request-id", "request-412")
		w.WriteHeader(http.StatusPreconditionFailed)
		io.WriteString(w, `{"code":-412,"message":"request was banned","ttl":1}`)
	}))
	defer server.Close()
	client := testClient(t, server, Config{})

	_, err := client.VideoDetail(context.Background(), "BV1fixture")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("VideoDetail error = %T %v", err, err)
	}
	if apiErr.StatusCode != http.StatusPreconditionFailed || apiErr.Code != -412 || apiErr.Message != "request was banned" || apiErr.RequestID != "request-412" {
		t.Fatalf("APIError = %#v", apiErr)
	}
	if got, want := err.Error(), "bilibili video detail: HTTP 412, code -412: request was banned (request request-412)"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
	}
}

func TestSearchReportsGaiaRiskControlVoucher(t *testing.T) {
	navFixture := fixture(t, "nav.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/home":
			http.SetCookie(w, &http.Cookie{Name: "buvid3", Value: "fingerprint", Path: "/"})
			w.WriteHeader(http.StatusOK)
		case "/nav":
			writeJSON(w, navFixture)
		case "/search":
			io.WriteString(w, `{"code":0,"message":"OK","data":{"v_voucher":"opaque-challenge","result":[{"bvid":"BVpartial"}]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	client := testClient(t, server, Config{})

	_, err := client.Search(context.Background(), "fixture", SearchOptions{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.RiskControl || !apiErr.BatchFatal() {
		t.Fatalf("Search error = %T %#v", err, err)
	}
	if got, want := err.Error(), "bilibili search: risk-control challenge required"; got != want {
		t.Fatalf("error = %q, want %q", got, want)
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
	var receipts []WriteReceipt
	result, err := client.AddToFavoriteWithReceipts(context.Background(), 900, []model.Video{
		{BVID: "BV-ok", AID: 11}, {BVID: "BV-api", AID: 22}, {BVID: "BV-http", AID: 33},
	}, func(receipt WriteReceipt) { receipts = append(receipts, receipt) })
	var partial *PartialWriteError
	if !errors.As(err, &partial) {
		t.Fatalf("AddToFavorite error = %T %v", err, err)
	}
	if !reflect.DeepEqual(result.Succeeded, []string{"BV-ok"}) || len(result.Failed) != 2 || result.Failed[0].Index != 1 || result.Failed[1].Index != 2 {
		t.Fatalf("AddToFavorite result = %#v", result)
	}
	if len(receipts) != 3 || !receipts[0].Succeeded || receipts[0].BVID != "BV-ok" || receipts[1].Succeeded || receipts[1].BVID != "BV-api" || receipts[2].Succeeded || receipts[2].BVID != "BV-http" {
		t.Fatalf("write receipts = %#v", receipts)
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
