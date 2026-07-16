// Package bilibili implements the unofficial Bilibili operations used by
// music2bb. It contains no terminal interaction and all network operations are
// cancellation-aware.
package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/netx"
)

var ErrNoCookieFile = errors.New("bilibili: cookie file does not exist")

type SearchCacheMissError struct{}

func (SearchCacheMissError) Error() string         { return "bilibili: search cache miss" }
func (SearchCacheMissError) SearchCacheMiss() bool { return true }

type Endpoints struct {
	Home                 string
	Nav                  string
	AnonymousSearch      string
	Search               string
	VideoDetail          string
	QRGenerate           string
	QRPoll               string
	FavoriteList         string
	FavoriteCreate       string
	FavoriteDeal         string
	FavoriteResourceList string
	FavoriteResourceDel  string
	FavoriteDelete       string
}

func DefaultEndpoints() Endpoints {
	return Endpoints{
		Home:                 "https://www.bilibili.com/",
		Nav:                  "https://api.bilibili.com/x/web-interface/nav",
		AnonymousSearch:      "https://api.bilibili.com/x/web-interface/search/all/v2",
		Search:               "https://api.bilibili.com/x/web-interface/wbi/search/type",
		VideoDetail:          "https://api.bilibili.com/x/web-interface/view",
		QRGenerate:           "https://passport.bilibili.com/x/passport-login/web/qrcode/generate",
		QRPoll:               "https://passport.bilibili.com/x/passport-login/web/qrcode/poll",
		FavoriteList:         "https://api.bilibili.com/x/v3/fav/folder/created/list-all",
		FavoriteCreate:       "https://api.bilibili.com/x/v3/fav/folder/add",
		FavoriteDeal:         "https://api.bilibili.com/x/v3/fav/resource/deal",
		FavoriteResourceList: "https://api.bilibili.com/x/v3/fav/resource/list",
		FavoriteResourceDel:  "https://api.bilibili.com/x/v3/fav/resource/batch-del",
		FavoriteDelete:       "https://api.bilibili.com/x/v3/fav/folder/del",
	}
}

func (e Endpoints) withDefaults() Endpoints {
	defaults := DefaultEndpoints()
	if e.Home == "" {
		e.Home = defaults.Home
	}
	if e.Nav == "" {
		e.Nav = defaults.Nav
	}
	if e.AnonymousSearch == "" {
		e.AnonymousSearch = defaults.AnonymousSearch
	}
	if e.Search == "" {
		e.Search = defaults.Search
	}
	if e.VideoDetail == "" {
		e.VideoDetail = defaults.VideoDetail
	}
	if e.QRGenerate == "" {
		e.QRGenerate = defaults.QRGenerate
	}
	if e.QRPoll == "" {
		e.QRPoll = defaults.QRPoll
	}
	if e.FavoriteList == "" {
		e.FavoriteList = defaults.FavoriteList
	}
	if e.FavoriteCreate == "" {
		e.FavoriteCreate = defaults.FavoriteCreate
	}
	if e.FavoriteDeal == "" {
		e.FavoriteDeal = defaults.FavoriteDeal
	}
	if e.FavoriteResourceList == "" {
		e.FavoriteResourceList = defaults.FavoriteResourceList
	}
	if e.FavoriteResourceDel == "" {
		e.FavoriteResourceDel = defaults.FavoriteResourceDel
	}
	if e.FavoriteDelete == "" {
		e.FavoriteDelete = defaults.FavoriteDelete
	}
	return e
}

type Config struct {
	Endpoints           Endpoints
	CookieFile          string
	AnonymousCookieFile string
	CookieStore         CookieStore
	AccountHTTP         *http.Client
	SearchHTTP          *http.Client
	Limiter             netx.Limiter
	SearchLimiter       netx.Limiter
	Timeout             time.Duration
	MaxAttempts         int
	CacheSize           int
	SearchCache         SearchCache
	Now                 func() time.Time
	Sleep               netx.Sleeper
	UserAgent           string
	WriteInterval       time.Duration
}

// CookieStore allows the reusable engine to persist authentication without
// coupling callers to the filesystem. Implementations must be safe for
// sequential Load/Save/Clear calls from a Client.
type CookieStore interface {
	Load() ([]CookieRecord, error)
	Save([]CookieRecord) error
	Clear() error
	Exists() bool
}

type Account struct {
	MID      int64
	Name     string
	LoggedIn bool
}

type EventKind string

const (
	EventQRPayload EventKind = "qr_payload"
	EventQRScanned EventKind = "qr_scanned"
	EventWarning   EventKind = "warning"
)

type Event struct {
	Kind      EventKind
	QRPayload string
	Message   string
}

type Observer interface {
	ObserveBilibili(Event)
}

type ObserverFunc func(Event)

func (f ObserverFunc) ObserveBilibili(event Event) {
	if f != nil {
		f(event)
	}
}

type LoginOptions struct {
	Timeout      time.Duration
	PollInterval time.Duration
	Observer     Observer
}

type SearchOptions struct {
	Page        int
	PageSize    int
	SearchType  string
	Order       string
	Identity    SearchIdentity
	CachePolicy SearchCachePolicy
	CacheOnly   bool
}

type SearchIdentity string

const (
	SearchIdentityAnonymous SearchIdentity = "anonymous"
	SearchIdentitySession   SearchIdentity = "session"
)

type SearchCachePolicy string

const (
	SearchCacheDefault SearchCachePolicy = ""
	SearchCacheBypass  SearchCachePolicy = "bypass"
	SearchCacheRefresh SearchCachePolicy = "refresh"
)

type SearchCacheKey struct {
	Query       string         `json:"query"`
	Page        int            `json:"page"`
	PageSize    int            `json:"pageSize"`
	SearchType  string         `json:"searchType"`
	Order       string         `json:"order"`
	Identity    SearchIdentity `json:"identity"`
	IdentityKey string         `json:"identityKey"`
}

type SearchCacheEntry struct {
	Key      SearchCacheKey `json:"key"`
	Videos   []model.Video  `json:"videos"`
	StoredAt time.Time      `json:"storedAt"`
}

type SearchCache interface {
	Get(context.Context, SearchCacheKey) (SearchCacheEntry, bool, error)
	Put(context.Context, SearchCacheEntry) error
}

type SearchResult struct {
	Videos        []model.Video
	CacheHit      bool
	RemoteRequest bool
}

type RiskControlReason string

const (
	RiskControlVoucher  RiskControlReason = "voucher"
	RiskControlHTTP412  RiskControlReason = "http_412"
	RiskControlCode412  RiskControlReason = "code_-412"
	RiskControlCode1200 RiskControlReason = "code_-1200"
)

type CreateFavoriteRequest struct {
	Title   string
	Intro   string
	Private bool
}

type AddFailure struct {
	Index  int
	BVID   string
	Reason string
	Err    error
}

type AddResult struct {
	FavoriteID int64
	Succeeded  []string
	Failed     []AddFailure
}

// WriteReceipt reports one completed favorite-write attempt.
type WriteReceipt struct {
	FavoriteID int64
	BVID       string
	Succeeded  bool
	Reason     string
}

func (r AddResult) Error() error {
	if len(r.Failed) == 0 {
		return nil
	}
	return &PartialWriteError{Succeeded: len(r.Succeeded), Failed: append([]AddFailure(nil), r.Failed...)}
}

type PartialWriteError struct {
	Succeeded int
	Failed    []AddFailure
}

func (e *PartialWriteError) Error() string {
	return fmt.Sprintf("bilibili: favorite write completed with %d successes and %d failures", e.Succeeded, len(e.Failed))
}

type FavoriteResource struct {
	AID   int64
	BVID  string
	Title string
}

type APIError struct {
	Operation   string
	StatusCode  int
	Code        int64
	Message     string
	RequestID   string
	RiskControl bool
	RiskReason  RiskControlReason
	Err         error
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	suffix := ""
	if e.RequestID != "" {
		suffix = fmt.Sprintf(" (request %s)", e.RequestID)
	}
	if e.StatusCode != 0 && e.Message != "" && e.Code != 0 {
		return fmt.Sprintf("bilibili %s: HTTP %d, code %d: %s%s", e.Operation, e.StatusCode, e.Code, e.Message, suffix)
	}
	if e.StatusCode != 0 && e.Message != "" {
		return fmt.Sprintf("bilibili %s: HTTP %d: %s%s", e.Operation, e.StatusCode, e.Message, suffix)
	}
	if e.Message != "" {
		if e.Code != 0 {
			return fmt.Sprintf("bilibili %s: %s (code %d)%s", e.Operation, e.Message, e.Code, suffix)
		}
		return fmt.Sprintf("bilibili %s: %s%s", e.Operation, e.Message, suffix)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("bilibili %s: HTTP %d%s", e.Operation, e.StatusCode, suffix)
	}
	return fmt.Sprintf("bilibili %s: %v%s", e.Operation, e.Err, suffix)
}

func (e *APIError) Unwrap() error { return e.Err }

// BatchFatal reports request-contract or risk-control failures that should stop
// a multi-song search rather than repeating the same rejected request.
func (e *APIError) BatchFatal() bool {
	return e != nil && e.Operation == "search" && e.RiskReason != ""
}

func (e *APIError) RiskControlReason() string {
	if e == nil {
		return ""
	}
	return string(e.RiskReason)
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
