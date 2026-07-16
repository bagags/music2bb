package bilibili

import (
	"net/http"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"github.com/bagags/music2bb-go/internal/netx"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/125.0.0.0 Safari/537.36"

type Client struct {
	endpoints            Endpoints
	cookieFile           string
	cookieStore          CookieStore
	anonymousCookieStore CookieStore
	account              *netx.Client
	search               *netx.Client
	sessionSearch        *netx.Client
	accountJar           *persistentJar
	searchJar            *persistentJar
	now                  func() time.Time
	sleep                netx.Sleeper
	userAgent            string
	writeDelay           time.Duration
	searchCache          SearchCache

	fingerprintMu    sync.Mutex
	fingerprintReady map[SearchIdentity]bool

	wbiMu      sync.Mutex
	wbi        map[SearchIdentity]wbiState
	identityMu sync.Mutex
	sessionMID int64

	cacheMu    sync.Mutex
	cache      map[searchKey]SearchCacheEntry
	cacheOrder []searchKey
	cacheSize  int
	inflight   map[searchKey]*searchFlight
}

func New(config Config) (*Client, error) {
	endpoints := config.Endpoints.withDefaults()
	accountJar, err := newPersistentJar()
	if err != nil {
		return nil, err
	}
	searchJar, err := newAnonymousJar()
	if err != nil {
		return nil, err
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	account := netx.New(timeout, 8, config.Limiter)
	searchLimiter := config.SearchLimiter
	if searchLimiter == nil {
		searchLimiter = config.Limiter
	}
	search := netx.New(timeout, 8, searchLimiter)
	sessionSearch := netx.New(timeout, 8, searchLimiter)
	if config.AccountHTTP == nil {
		account.HTTP.Jar = accountJar
	} else {
		account.HTTP = cloneHTTPClient(config.AccountHTTP, timeout, accountJar)
	}
	if config.SearchHTTP == nil {
		search.HTTP.Jar = searchJar
		sessionSearch.HTTP.Jar = accountJar
	} else {
		search.HTTP = cloneHTTPClient(config.SearchHTTP, timeout, searchJar)
		sessionSearch.HTTP = cloneHTTPClient(config.SearchHTTP, timeout, accountJar)
	}
	if config.MaxAttempts > 0 {
		account.MaxAttempts = config.MaxAttempts
		search.MaxAttempts = config.MaxAttempts
		sessionSearch.MaxAttempts = config.MaxAttempts
	}
	if config.Sleep != nil {
		account.Sleep = config.Sleep
		search.Sleep = config.Sleep
		sessionSearch.Sleep = config.Sleep
	}
	now := config.Now
	if now == nil {
		now = time.Now
	}
	sleep := config.Sleep
	if sleep == nil {
		sleep = sleepContext
	}
	cacheSize := config.CacheSize
	if cacheSize <= 0 {
		cacheSize = 100
	}
	userAgent := config.UserAgent
	if userAgent == "" {
		userAgent = defaultUserAgent
	}
	cookieStore := config.CookieStore
	if cookieStore == nil {
		cookieStore = fileCookieStore{path: config.CookieFile}
	}
	anonymousCookieStore := CookieStore(fileCookieStore{path: config.AnonymousCookieFile})
	if config.AnonymousCookieFile == "" && config.CookieFile != "" {
		anonymousCookieStore = fileCookieStore{path: filepath.Join(filepath.Dir(config.CookieFile), "bilibili-anonymous.json")}
	}
	if anonymousCookieStore.Exists() {
		records, loadErr := anonymousCookieStore.Load()
		if loadErr != nil {
			return nil, loadErr
		}
		home, parseErr := url.Parse(endpoints.Home)
		if parseErr != nil {
			return nil, parseErr
		}
		searchJar.load(filterAnonymousCookies(records), home)
	}
	return &Client{
		endpoints: endpoints, cookieFile: config.CookieFile, cookieStore: cookieStore, anonymousCookieStore: anonymousCookieStore,
		account: account, search: search, sessionSearch: sessionSearch, accountJar: accountJar, searchJar: searchJar,
		now: now, sleep: sleep, userAgent: userAgent, writeDelay: config.WriteInterval,
		fingerprintReady: make(map[SearchIdentity]bool), wbi: make(map[SearchIdentity]wbiState),
		searchCache: config.SearchCache,
		cache:       make(map[searchKey]SearchCacheEntry), cacheSize: cacheSize,
		inflight: make(map[searchKey]*searchFlight),
	}, nil
}

func cloneHTTPClient(source *http.Client, timeout time.Duration, jar http.CookieJar) *http.Client {
	clone := *source
	clone.Jar = jar
	if clone.Timeout == 0 {
		clone.Timeout = timeout
	}
	return &clone
}

func (c *Client) CloseIdleConnections() {
	if c == nil {
		return
	}
	c.account.CloseIdleConnections()
	c.search.CloseIdleConnections()
	c.sessionSearch.CloseIdleConnections()
}
