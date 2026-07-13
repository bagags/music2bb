// Package bilibili implements the unofficial Bilibili operations used by
// kg2bb. It contains no terminal interaction and all network operations are
// cancellation-aware.
package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gguage/music-to-bb/internal/netx"
)

var ErrNoCookieFile = errors.New("bilibili: cookie file does not exist")

type Endpoints struct {
	Home                 string
	Nav                  string
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
		Search:               "https://api.bilibili.com/x/web-interface/search/all/v2",
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
	Endpoints     Endpoints
	CookieFile    string
	AccountHTTP   *http.Client
	SearchHTTP    *http.Client
	Limiter       netx.Limiter
	Timeout       time.Duration
	MaxAttempts   int
	CacheSize     int
	Now           func() time.Time
	Sleep         netx.Sleeper
	UserAgent     string
	WriteInterval time.Duration
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
	Page       int
	PageSize   int
	SearchType int
	Order      string
}

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
	Operation  string
	StatusCode int
	Code       int64
	Message    string
	Err        error
}

func (e *APIError) Error() string {
	if e == nil {
		return "<nil>"
	}
	if e.Message != "" {
		return fmt.Sprintf("bilibili %s: %s (code %d)", e.Operation, e.Message, e.Code)
	}
	if e.StatusCode != 0 {
		return fmt.Sprintf("bilibili %s: HTTP %d", e.Operation, e.StatusCode)
	}
	return fmt.Sprintf("bilibili %s: %v", e.Operation, e.Err)
}

func (e *APIError) Unwrap() error { return e.Err }

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
