package bilibili

import (
	"net/http"
	"net/http/cookiejar"
	"sync"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/netx"
)

const defaultUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/125.0.0.0 Safari/537.36"

type Client struct {
	endpoints  Endpoints
	cookieFile string
	account    *netx.Client
	search     *netx.Client
	accountJar *persistentJar
	searchJar  http.CookieJar
	now        func() time.Time
	sleep      netx.Sleeper
	userAgent  string
	writeDelay time.Duration

	fingerprintMu    sync.Mutex
	fingerprintReady bool

	wbiMu     sync.Mutex
	wbiImgKey string
	wbiSubKey string
	wbiAt     time.Time

	cacheMu    sync.Mutex
	cache      map[searchKey][]model.Video
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
	searchJar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	timeout := config.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	account := netx.New(timeout, 8, config.Limiter)
	search := netx.New(timeout, 8, config.Limiter)
	if config.AccountHTTP == nil {
		account.HTTP.Jar = accountJar
	} else {
		account.HTTP = cloneHTTPClient(config.AccountHTTP, timeout, accountJar)
	}
	if config.SearchHTTP == nil {
		search.HTTP.Jar = searchJar
	} else {
		search.HTTP = cloneHTTPClient(config.SearchHTTP, timeout, searchJar)
	}
	if config.MaxAttempts > 0 {
		account.MaxAttempts = config.MaxAttempts
		search.MaxAttempts = config.MaxAttempts
	}
	if config.Sleep != nil {
		account.Sleep = config.Sleep
		search.Sleep = config.Sleep
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
	return &Client{
		endpoints: endpoints, cookieFile: config.CookieFile,
		account: account, search: search, accountJar: accountJar, searchJar: searchJar,
		now: now, sleep: sleep, userAgent: userAgent, writeDelay: config.WriteInterval,
		cache: make(map[searchKey][]model.Video), cacheSize: cacheSize,
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
}
