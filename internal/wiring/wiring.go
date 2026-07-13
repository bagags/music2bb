// Package wiring assembles the production implementation while keeping the
// orchestration and public API independently testable.
package wiring

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/gguage/music-to-bb/internal/bilibili"
	"github.com/gguage/music-to-bb/internal/browser"
	"github.com/gguage/music-to-bb/internal/config"
	"github.com/gguage/music-to-bb/internal/kugou"
	"github.com/gguage/music-to-bb/internal/matcher"
	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/netx"
	"github.com/gguage/music-to-bb/internal/service"
)

type Options struct {
	State            config.Options
	LoadedState      *config.Config
	RatePerSecond    float64
	HTTPTimeout      time.Duration
	Limiter          netx.Limiter
	KugouHTTP        *http.Client
	AccountHTTP      *http.Client
	SearchHTTP       *http.Client
	Now              func() time.Time
	Sleep            netx.Sleeper
	CookieStore      bilibili.CookieStore
	BrowserManager   *browser.Manager
	BrowserExtractor kugou.Extractor
}

type Components struct {
	Engine  *service.Engine
	Browser *browser.Manager
	State   config.Config
	close   func()
}

func (c *Components) Close() {
	if c != nil && c.close != nil {
		c.close()
	}
}

func New(options Options) (*Components, error) {
	var state config.Config
	if options.LoadedState != nil {
		state = cloneConfig(*options.LoadedState)
	} else {
		var err error
		state, err = config.Load(options.State)
		if err != nil {
			return nil, err
		}
	}
	manager := options.BrowserManager
	if manager == nil {
		var err error
		manager, err = browser.NewManager(filepath.Join(state.CacheDir, "browser"))
		if err != nil {
			return nil, err
		}
	}
	limiter := options.Limiter
	if limiter == nil {
		limiter = netx.NewTokenLimiter(options.RatePerSecond, 4)
	}
	timeout := options.HTTPTimeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	kugouHTTP := netx.New(timeout, 8, limiter)
	if options.KugouHTTP != nil {
		kugouHTTP.HTTP = options.KugouHTTP
	}
	directKugou := kugou.New(kugouHTTP, nil)
	extractor := options.BrowserExtractor
	externalBrowser := extractor != nil
	if extractor == nil {
		extractor = browser.NewExtractor(manager)
	}
	browserKugou := kugou.New(kugouHTTP, extractor)
	bili, err := bilibili.New(bilibili.Config{
		CookieFile:    state.CookieFile,
		CookieStore:   options.CookieStore,
		AccountHTTP:   options.AccountHTTP,
		SearchHTTP:    options.SearchHTTP,
		Limiter:       limiter,
		Timeout:       timeout,
		Now:           options.Now,
		Sleep:         options.Sleep,
		WriteInterval: 150 * time.Millisecond,
	})
	if err != nil {
		kugouHTTP.CloseIdleConnections()
		return nil, err
	}
	scorer := matcher.New(matcher.Options{
		BlockKeywords:     state.BlockKeywords,
		QualityKeywords:   state.QualityKeywords,
		WeightedUploaders: state.WeightedUploaders,
	})
	adapter := &bilibiliAdapter{client: bili}
	engine, err := service.New(service.Dependencies{
		Playlist: &playlistAdapter{direct: directKugou, browser: browserKugou, manager: manager, externalBrowser: externalBrowser},
		Match:    adapter,
		Account:  adapter,
		Matcher:  scorer,
		Now:      options.Now,
	})
	if err != nil {
		bili.CloseIdleConnections()
		kugouHTTP.CloseIdleConnections()
		return nil, err
	}
	return &Components{
		Engine:  engine,
		Browser: manager,
		State:   state,
		close: func() {
			bili.CloseIdleConnections()
			kugouHTTP.CloseIdleConnections()
		},
	}, nil
}

func cloneConfig(source config.Config) config.Config {
	source.BlockKeywords = append([]string(nil), source.BlockKeywords...)
	source.QualityKeywords = append([]string(nil), source.QualityKeywords...)
	source.WeightedUploaders = append([]string(nil), source.WeightedUploaders...)
	source.Migration.Copied = append([]string(nil), source.Migration.Copied...)
	return source
}

type playlistAdapter struct {
	direct          *kugou.Client
	browser         *kugou.Client
	manager         *browser.Manager
	externalBrowser bool
}

func (a *playlistAdapter) ParsePlaylist(ctx context.Context, rawURL string, policy service.BrowserPolicy) ([]model.Song, error) {
	client := a.direct
	switch policy {
	case "", service.BrowserAuto:
		if a.externalBrowser {
			client = a.browser
		} else if status, err := a.manager.Status(ctx); err == nil && status.Installed {
			client = a.browser
		}
	case service.BrowserNever:
	case service.BrowserAlways:
		if a.externalBrowser {
			client = a.browser
			break
		}
		status, err := a.manager.Status(ctx)
		if err != nil {
			return nil, &service.OperationError{Category: service.ErrorBrowser, Operation: "browser status", Err: err}
		}
		if !status.Installed {
			return nil, &service.OperationError{Category: service.ErrorBrowser, Operation: "parse playlist", Message: "verified browser is not installed"}
		}
		client = a.browser
	default:
		return nil, &service.OperationError{Category: service.ErrorInvalidInput, Operation: "parse playlist", Message: "invalid browser policy"}
	}
	songs, err := client.ParsePlaylist(ctx, rawURL)
	if kugou.IsKind(err, kugou.ErrorInvalidURL) {
		return nil, &service.OperationError{Category: service.ErrorInvalidInput, Operation: "parse playlist", Err: err}
	}
	return songs, err
}

type bilibiliAdapter struct {
	client *bilibili.Client
}

func (a *bilibiliAdapter) Login(ctx context.Context, options service.LoginOptions, update func(service.LoginUpdate)) (service.Account, error) {
	if options.UseStoredCookies {
		if _, err := a.client.LoadCookies(); err != nil && !errors.Is(err, bilibili.ErrNoCookieFile) {
			return service.Account{}, err
		}
		if account, err := a.client.Account(ctx); err == nil && account.LoggedIn {
			return service.Account{ID: account.MID, Name: account.Name}, nil
		}
	}
	if !options.AllowQR {
		return service.Account{}, &service.OperationError{Category: service.ErrorAuthentication, Operation: "login", Message: "stored login is unavailable and QR login is disabled"}
	}
	account, err := a.client.QRLogin(ctx, bilibili.LoginOptions{
		Timeout: options.Timeout,
		Observer: bilibili.ObserverFunc(func(event bilibili.Event) {
			if update != nil {
				update(service.LoginUpdate{QRPayload: event.QRPayload, Status: event.Message})
			}
		}),
	})
	if err != nil {
		return service.Account{}, err
	}
	return service.Account{ID: account.MID, Name: account.Name}, nil
}

func (a *bilibiliAdapter) SearchVideos(ctx context.Context, query string, page, pageSize int) ([]model.Video, error) {
	return a.client.Search(ctx, query, bilibili.SearchOptions{Page: page, PageSize: pageSize, SearchType: 1, Order: "totalrank"})
}

func (a *bilibiliAdapter) VideoDetail(ctx context.Context, bvid string) (model.Video, error) {
	return a.client.VideoDetail(ctx, bvid)
}

func (a *bilibiliAdapter) ListFavorites(ctx context.Context) ([]model.Favorite, error) {
	return a.client.ListFavorites(ctx)
}

func (a *bilibiliAdapter) CreateFavorite(ctx context.Context, request service.CreateFavoriteRequest) (model.Favorite, error) {
	return a.client.CreateFavorite(ctx, bilibili.CreateFavoriteRequest{
		Title:   request.Title,
		Intro:   request.Intro,
		Private: request.Private,
	})
}

func (a *bilibiliAdapter) AddToFavorite(ctx context.Context, favoriteID int64, videos []model.Video) (service.AddResult, error) {
	result, err := a.client.AddToFavorite(ctx, favoriteID, videos)
	converted := service.AddResult{FavoriteID: result.FavoriteID, Succeeded: append([]string(nil), result.Succeeded...)}
	for _, failure := range result.Failed {
		converted.Failed = append(converted.Failed, service.AddFailure{BVID: failure.BVID, Reason: failure.Reason})
	}
	return converted, err
}
