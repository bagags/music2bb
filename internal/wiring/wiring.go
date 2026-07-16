// Package wiring assembles the production implementation while keeping the
// orchestration and public API independently testable.
package wiring

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"time"

	"github.com/bagags/music2bb-go/internal/applemusic"
	"github.com/bagags/music2bb-go/internal/bilibili"
	"github.com/bagags/music2bb-go/internal/browser"
	"github.com/bagags/music2bb-go/internal/config"
	"github.com/bagags/music2bb-go/internal/kugou"
	"github.com/bagags/music2bb-go/internal/matcher"
	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/netx"
	"github.com/bagags/music2bb-go/internal/playlist"
	"github.com/bagags/music2bb-go/internal/service"
)

type Options struct {
	State            config.Options
	LoadedState      *config.Config
	RatePerSecond    float64
	HTTPTimeout      time.Duration
	Limiter          netx.Limiter
	KugouHTTP        *http.Client
	AppleMusicHTTP   *http.Client
	AccountHTTP      *http.Client
	SearchHTTP       *http.Client
	Now              func() time.Time
	Sleep            netx.Sleeper
	CookieStore      bilibili.CookieStore
	BrowserManager   *browser.Manager
	BrowserExtractor playlist.BrowserExtractor
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
	directKugou := kugou.New(kugouHTTP)
	appleMusicHTTP := netx.New(timeout, 8, limiter)
	if options.AppleMusicHTTP != nil {
		appleMusicHTTP.HTTP = options.AppleMusicHTTP
	}
	directAppleMusic := applemusic.New(appleMusicHTTP)
	extractor := options.BrowserExtractor
	if extractor == nil {
		extractor = browser.NewExtractor(manager)
	}
	identification, err := playlist.NewIdentificationRegistry(
		kugou.IdentificationRegistration(),
		applemusic.IdentificationRegistration(),
	)
	if err != nil {
		kugouHTTP.CloseIdleConnections()
		appleMusicHTTP.CloseIdleConnections()
		return nil, err
	}
	optimizations, err := playlist.NewOptimizationRegistry(
		kugou.OptimizationRegistration(directKugou),
		applemusic.OptimizationRegistration(directAppleMusic),
	)
	if err != nil {
		kugouHTTP.CloseIdleConnections()
		appleMusicHTTP.CloseIdleConnections()
		return nil, err
	}
	coordinator := playlist.NewCoordinator(identification, optimizations, extractor)
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
		appleMusicHTTP.CloseIdleConnections()
		return nil, err
	}
	scorer := matcher.New(matcher.Options{
		BlockKeywords:     state.BlockKeywords,
		QualityKeywords:   state.QualityKeywords,
		WeightedUploaders: state.WeightedUploaders,
	})
	adapter := &bilibiliAdapter{client: bili}
	engine, err := service.New(service.Dependencies{
		Playlist: &playlistAdapter{coordinator: coordinator},
		Match:    adapter,
		Account:  adapter,
		Strategy: scorer,
		Now:      options.Now,
	})
	if err != nil {
		bili.CloseIdleConnections()
		kugouHTTP.CloseIdleConnections()
		appleMusicHTTP.CloseIdleConnections()
		return nil, err
	}
	return &Components{
		Engine:  engine,
		Browser: manager,
		State:   state,
		close: func() {
			bili.CloseIdleConnections()
			kugouHTTP.CloseIdleConnections()
			appleMusicHTTP.CloseIdleConnections()
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
	coordinator *playlist.Coordinator
}

func (a *playlistAdapter) ParsePlaylist(ctx context.Context, rawURL string, policy service.BrowserPolicy, onBrowserFallback func()) (service.PlaylistResult, error) {
	result, err := a.coordinator.ParsePlaylistWithOptions(ctx, rawURL, playlist.ParseOptions{
		BrowserPolicy: playlist.BrowserPolicy(policy), OnBrowserFallback: onBrowserFallback,
	})
	converted := service.PlaylistResult{Songs: result.Songs, ExpectedTotal: result.ExpectedTotal}
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return converted, err
	}
	category := service.ErrorExtraction
	switch {
	case playlist.IsKind(err, playlist.ErrorInvalidInput):
		category = service.ErrorInvalidInput
	case playlist.IsKind(err, playlist.ErrorBrowser), browser.IsKind(err, browser.ErrorNotInstalled):
		category = service.ErrorBrowser
	case playlist.IsKind(err, playlist.ErrorInternal):
		category = service.ErrorInternal
	}
	return converted, &service.OperationError{Category: category, Operation: "parse playlist", Err: err}
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
	return a.client.Search(ctx, query, bilibili.SearchOptions{Page: page, PageSize: pageSize, SearchType: "video", Order: "totalrank"})
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
