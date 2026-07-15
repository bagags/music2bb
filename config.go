package music2bb

import (
	"context"
	"net/http"
	"time"
)

// MatchProfile selects a built-in matching policy and weight preset.
type MatchProfile string

const (
	MatchProfileStandard  MatchProfile = "standard"
	MatchProfileClassical MatchProfile = "classical"
)

// MatchWeights controls the relative contribution of each normalized
// matching component. Values may use any non-negative scale with at least one
// positive entry; each call clones and normalizes them by their sum.
type MatchWeights struct {
	Title      float64
	Artist     float64
	Quality    float64
	Official   float64
	Popularity float64
	Uploader   float64
}

// StandardMatchWeights returns the artist-oriented standard preset.
func StandardMatchWeights() MatchWeights {
	return MatchWeights{Title: 40, Artist: 25, Quality: 10, Official: 10, Popularity: 10, Uploader: 5}
}

// ClassicalMatchWeights returns the title-oriented classical preset.
func ClassicalMatchWeights() MatchWeights {
	return MatchWeights{Title: 55, Artist: 10, Quality: 10, Official: 10, Popularity: 10, Uploader: 5}
}

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
	Profile     MatchProfile
	Weights     *MatchWeights
}

// CandidateSearchOptions controls ranking for one manual candidate search.
type CandidateSearchOptions struct {
	Limit   int
	Profile MatchProfile
	Weights *MatchWeights
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
