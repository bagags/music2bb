package bilibili

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
)

func TestFileSearchCacheRoundTripAndCorruptionQuarantine(t *testing.T) {
	dir := t.TempDir()
	cache := NewFileSearchCache(dir).(*fileSearchCache)
	entry := SearchCacheEntry{
		Key: SearchCacheKey{
			Query: "query", Page: 1, PageSize: 20, SearchType: "video", Order: "totalrank",
			Identity: SearchIdentityAnonymous, IdentityKey: "anonymous:fingerprint",
		},
		Videos:   []model.Video{{BVID: "BV1", Title: "Result", Tags: []string{"tag"}}},
		StoredAt: time.Unix(1000, 0).UTC(),
	}
	if err := cache.Put(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	loaded, ok, err := cache.Get(context.Background(), entry.Key)
	if err != nil || !ok || !reflect.DeepEqual(loaded, entry) {
		t.Fatalf("Get = %#v, %v, %v", loaded, ok, err)
	}
	loaded.Videos[0].Tags[0] = "mutated"
	reloaded, _, _ := cache.Get(context.Background(), entry.Key)
	if reloaded.Videos[0].Tags[0] != "tag" {
		t.Fatal("filesystem cache returned aliased video tags")
	}

	path, err := cache.path(entry.Key)
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("search cache mode = %v", info.Mode().Perm())
	}
	if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.Get(context.Background(), entry.Key); err != nil || ok {
		t.Fatalf("corrupt Get = ok %v, err %v", ok, err)
	}
	quarantined, err := filepath.Glob(path + ".corrupt-*")
	if err != nil || len(quarantined) != 1 {
		t.Fatalf("quarantined files = %#v, err = %v", quarantined, err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt cache remains at active path: %v", err)
	}
	if err := cache.Put(context.Background(), entry); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := cache.Get(context.Background(), entry.Key); err != nil || !ok {
		t.Fatalf("rebuilt cache = ok %v, err %v", ok, err)
	}
}

func TestResetAnonymousIdentityKeepsAccountCookiesAndSessionPartition(t *testing.T) {
	root := t.TempDir()
	anonymousPath := filepath.Join(root, "anonymous.json")
	if err := (fileCookieStore{path: anonymousPath}).Save([]CookieRecord{{Name: "buvid3", Value: "device-one", Domain: "example.test", Path: "/"}}); err != nil {
		t.Fatal(err)
	}
	client, err := New(Config{
		Endpoints: endpoints("https://example.test"), CookieFile: filepath.Join(root, "account.json"), AnonymousCookieFile: anonymousPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	client.applyCookieString("DedeUserID=42; SESSDATA=secret; bili_jct=csrf")
	anonymousKey := client.searchIdentityKey(SearchIdentityAnonymous)
	sessionKey := client.searchIdentityKey(SearchIdentitySession)
	if anonymousKey == "" || sessionKey != "mid:42" {
		t.Fatalf("identity keys = %q and %q", anonymousKey, sessionKey)
	}
	if err := client.ResetAnonymousIdentity(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := client.searchIdentityKey(SearchIdentityAnonymous); got != "" {
		t.Fatalf("anonymous identity after reset = %q", got)
	}
	if got := client.searchIdentityKey(SearchIdentitySession); got != sessionKey {
		t.Fatalf("session identity changed from %q to %q", sessionKey, got)
	}
	home, _ := url.Parse(client.endpoints.Home)
	if cookie, err := findCookie(client.accountJar.Cookies(home), "SESSDATA"); err != nil || cookie.Value != "secret" {
		t.Fatalf("account cookie after reset = %#v, %v", cookie, err)
	}
	if _, err := os.Stat(anonymousPath); !os.IsNotExist(err) {
		t.Fatalf("anonymous identity file remains: %v", err)
	}
}

func TestSearchCacheTTLUsesPositiveAndNegativeDurations(t *testing.T) {
	now := time.Unix(1000, 0)
	positive := SearchCacheEntry{StoredAt: now, Videos: []model.Video{{BVID: "BV1"}}}
	negative := SearchCacheEntry{StoredAt: now, Videos: []model.Video{}}
	if !searchCacheEntryFresh(positive, now.Add(7*24*time.Hour-time.Nanosecond)) || searchCacheEntryFresh(positive, now.Add(7*24*time.Hour)) {
		t.Fatal("positive cache TTL is not a hard seven-day expiry")
	}
	if !searchCacheEntryFresh(negative, now.Add(time.Hour-time.Nanosecond)) || searchCacheEntryFresh(negative, now.Add(time.Hour)) {
		t.Fatal("negative cache TTL is not a hard one-hour expiry")
	}
}

func TestFileSearchCachePartitionsAnonymousFingerprintsAndSessionMIDs(t *testing.T) {
	cache := NewFileSearchCache(t.TempDir())
	base := SearchCacheKey{Query: "q", Page: 1, PageSize: 20, SearchType: "video", Order: "totalrank"}
	first := base
	first.Identity, first.IdentityKey = SearchIdentityAnonymous, "anonymous:first"
	if err := cache.Put(context.Background(), SearchCacheEntry{Key: first, StoredAt: time.Now(), Videos: []model.Video{{BVID: "BV1"}}}); err != nil {
		t.Fatal(err)
	}
	for _, other := range []SearchCacheKey{
		func() SearchCacheKey {
			key := base
			key.Identity, key.IdentityKey = SearchIdentityAnonymous, "anonymous:second"
			return key
		}(),
		func() SearchCacheKey {
			key := base
			key.Identity, key.IdentityKey = SearchIdentitySession, "mid:1"
			return key
		}(),
		func() SearchCacheKey {
			key := base
			key.Identity, key.IdentityKey = SearchIdentitySession, "mid:2"
			return key
		}(),
	} {
		if _, ok, err := cache.Get(context.Background(), other); err != nil || ok {
			t.Fatalf("partition %q reused first entry: ok=%v err=%v", other.IdentityKey, ok, err)
		}
	}
}
