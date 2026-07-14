package music2bb

import (
	"context"
	"net/http"
	"time"
)

type BrowserPolicy string

const (
	BrowserAuto   BrowserPolicy = "auto"
	BrowserNever  BrowserPolicy = "never"
	BrowserAlways BrowserPolicy = "always"
)

type Config struct {
	ConfigDir     string
	CacheDir      string
	HTTPTimeout   time.Duration
	RatePerSecond float64
	Login         LoginOptions
	Browser       BrowserOptions
}

type LoginOptions struct {
	UseStoredCookies bool
	AllowQR          bool
	Timeout          time.Duration
}

type BrowserOptions struct {
	Policy BrowserPolicy
}

type ParseOptions struct {
	BrowserPolicy BrowserPolicy
}

type MatchOptions struct {
	SearchPages int
	TopK        int
	Workers     int
}

type HTTPClients struct {
	Kugou           *http.Client
	AppleMusic      *http.Client
	BilibiliAccount *http.Client
	BilibiliSearch  *http.Client
}

type Clock interface {
	Now() time.Time
	Sleep(context.Context, time.Duration) error
}

type RateLimiter interface {
	Wait(context.Context) error
}

type Storage interface {
	Load() (StoredState, error)
	Save(StoredState) error
}

type BrowserExtractor interface {
	Extract(context.Context, string) ([]Song, error)
}

type Option func(*newOptions) error

type newOptions struct {
	http             HTTPClients
	clock            Clock
	limiter          RateLimiter
	storage          Storage
	browserExtractor BrowserExtractor
}

func WithHTTPClients(clients HTTPClients) Option {
	return func(options *newOptions) error { options.http = clients; return nil }
}

func WithClock(clock Clock) Option {
	return func(options *newOptions) error { options.clock = clock; return nil }
}

func WithRateLimiter(limiter RateLimiter) Option {
	return func(options *newOptions) error { options.limiter = limiter; return nil }
}

func WithStorage(storage Storage) Option {
	return func(options *newOptions) error { options.storage = storage; return nil }
}

func WithBrowserExtractor(extractor BrowserExtractor) Option {
	return func(options *newOptions) error { options.browserExtractor = extractor; return nil }
}
