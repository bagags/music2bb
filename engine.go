package music2bb

import (
	"context"
	"errors"
	"time"

	"github.com/gguage/music-to-bb/internal/bilibili"
	"github.com/gguage/music-to-bb/internal/config"
	"github.com/gguage/music-to-bb/internal/playlist"
	"github.com/gguage/music-to-bb/internal/service"
	"github.com/gguage/music-to-bb/internal/wiring"
)

type Engine struct {
	service       *service.Engine
	components    *wiring.Components
	loginDefaults LoginOptions
	parseDefaults ParseOptions
	browser       *BrowserManager
}

func New(cfg Config, options ...Option) (*Engine, error) {
	resolved := newOptions{}
	for _, option := range options {
		if option == nil {
			continue
		}
		if err := option(&resolved); err != nil {
			return nil, &Error{Category: ErrorInvalidInput, Operation: "new", Err: err}
		}
	}
	wiringOptions := wiring.Options{
		State:         config.Options{Dir: cfg.ConfigDir, CacheDir: cfg.CacheDir},
		RatePerSecond: cfg.RatePerSecond,
		HTTPTimeout:   cfg.HTTPTimeout,
		Limiter:       resolved.limiter,
		KugouHTTP:     resolved.http.Kugou,
		AccountHTTP:   resolved.http.BilibiliAccount,
		SearchHTTP:    resolved.http.BilibiliSearch,
	}
	if resolved.clock != nil {
		wiringOptions.Now = resolved.clock.Now
		wiringOptions.Sleep = resolved.clock.Sleep
	}
	if resolved.browserExtractor != nil {
		wiringOptions.BrowserExtractor = browserExtractorAdapter{extractor: resolved.browserExtractor}
	}
	if resolved.storage != nil {
		stored, err := resolved.storage.Load()
		if err != nil {
			return nil, &Error{Category: ErrorInternal, Operation: "load storage", Err: err}
		}
		paths, err := config.Resolve(wiringOptions.State)
		if err != nil {
			return nil, &Error{Category: ErrorInvalidInput, Operation: "resolve storage", Err: err}
		}
		loaded := config.Config{
			Paths:             paths,
			BlockKeywords:     append([]string(nil), stored.BlockKeywords...),
			QualityKeywords:   append([]string(nil), stored.QualityKeywords...),
			WeightedUploaders: append([]string(nil), stored.WeightedUploaders...),
		}
		wiringOptions.LoadedState = &loaded
		wiringOptions.CookieStore = storageCookieAdapter{storage: resolved.storage}
	}
	components, err := wiring.New(wiringOptions)
	if err != nil {
		return nil, &Error{Category: ErrorInternal, Operation: "new", Err: err}
	}
	login := cfg.Login
	if login == (LoginOptions{}) {
		login = LoginOptions{UseStoredCookies: true, AllowQR: true, Timeout: 3 * time.Minute}
	}
	policy := cfg.Browser.Policy
	if policy == "" {
		policy = BrowserAuto
	}
	return &Engine{
		service:       components.Engine,
		components:    components,
		loginDefaults: login,
		parseDefaults: ParseOptions{BrowserPolicy: policy},
		browser:       &BrowserManager{manager: components.Browser},
	}, nil
}

func (e *Engine) Close() error {
	if e != nil && e.components != nil {
		e.components.Close()
	}
	return nil
}

func (e *Engine) Browser() *BrowserManager { return e.browser }

func (e *Engine) Login(ctx context.Context, observer Observer) (Account, error) {
	return e.LoginWithOptions(ctx, e.loginDefaults, observer)
}

func (e *Engine) LoginWithOptions(ctx context.Context, options LoginOptions, observer Observer) (Account, error) {
	account, err := e.service.Login(ctx, service.LoginOptions{
		UseStoredCookies: options.UseStoredCookies,
		AllowQR:          options.AllowQR,
		Timeout:          options.Timeout,
	}, observerAdapter(observer))
	return Account{ID: account.ID, Name: account.Name}, wrapError(err)
}

func (e *Engine) ParsePlaylist(ctx context.Context, rawURL string, observer Observer) ([]Song, error) {
	return e.ParsePlaylistWithOptions(ctx, rawURL, e.parseDefaults, observer)
}

func (e *Engine) ParsePlaylistWithOptions(ctx context.Context, rawURL string, options ParseOptions, observer Observer) ([]Song, error) {
	songs, err := e.service.ParsePlaylist(ctx, rawURL, service.ParseOptions{BrowserPolicy: service.BrowserPolicy(options.BrowserPolicy)}, observerAdapter(observer))
	return songsFromInternal(songs), wrapError(err)
}

func (e *Engine) Match(ctx context.Context, songs []Song, options MatchOptions, observer Observer) ([]MatchResult, error) {
	internalSongs := songsToInternal(songs)
	results, err := e.service.Match(ctx, internalSongs, service.MatchOptions{
		SearchPages: options.SearchPages,
		TopK:        options.TopK,
		Workers:     options.Workers,
	}, observerAdapter(observer))
	return outcomesFromInternal(results), wrapError(err)
}

func (e *Engine) SearchCandidates(ctx context.Context, song Song, query string, limit int) ([]MatchResult, error) {
	results, err := e.service.SearchCandidates(ctx, songToInternal(song), query, limit)
	return candidatesFromInternal(results), wrapError(err)
}

func (e *Engine) VideoDetail(ctx context.Context, bvid string) (Video, error) {
	video, err := e.service.VideoDetail(ctx, bvid)
	return videoFromInternal(video), wrapError(err)
}

func (e *Engine) ListFavorites(ctx context.Context) ([]Favorite, error) {
	favorites, err := e.service.ListFavorites(ctx)
	return favoritesFromInternal(favorites), wrapError(err)
}

func (e *Engine) CreateFavorite(ctx context.Context, request CreateFavoriteRequest) (Favorite, error) {
	favorite, err := e.service.CreateFavorite(ctx, service.CreateFavoriteRequest{Title: request.Title, Intro: request.Intro, Private: request.Private})
	return favoriteFromInternal(favorite), wrapError(err)
}

func (e *Engine) AddToFavorite(ctx context.Context, favoriteID int64, matches []MatchResult, observer Observer) (AddResult, error) {
	outcomes := make([]service.MatchOutcome, len(matches))
	for index, match := range matches {
		outcomes[index] = outcomeToInternal(match)
	}
	result, err := e.service.AddToFavorite(ctx, favoriteID, outcomes, observerAdapter(observer))
	converted := AddResult{FavoriteID: result.FavoriteID, Succeeded: append([]string(nil), result.Succeeded...)}
	for _, failure := range result.Failed {
		converted.Failed = append(converted.Failed, AddFailure{BVID: failure.BVID, Reason: failure.Reason})
	}
	return converted, wrapError(err)
}

type browserExtractorAdapter struct{ extractor BrowserExtractor }

func (a browserExtractorAdapter) Available(context.Context) (bool, error) { return true, nil }

func (a browserExtractorAdapter) ExtractPlaylist(ctx context.Context, source playlist.Source) (playlist.RawResult, error) {
	songs, err := a.extractor.Extract(ctx, source.String())
	result := playlist.RawResult{Tracks: make([]playlist.TrackCandidate, len(songs))}
	for index, song := range songs {
		result.Tracks[index] = playlist.TrackCandidate{
			Fields: map[string]string{"name": song.Name, "artist": song.Artist},
			Album:  song.Album, Duration: song.Duration, Hash: song.Hash,
		}
	}
	return result, err
}

var _ playlist.BrowserExtractor = browserExtractorAdapter{}

type storageCookieAdapter struct{ storage Storage }

func (a storageCookieAdapter) Load() ([]bilibili.CookieRecord, error) {
	state, err := a.storage.Load()
	if err != nil {
		return nil, err
	}
	if !state.HasCookies {
		return nil, bilibili.ErrNoCookieFile
	}
	records := make([]bilibili.CookieRecord, len(state.Cookies))
	for index, cookie := range state.Cookies {
		records[index] = bilibili.CookieRecord{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path}
	}
	return records, nil
}

func (a storageCookieAdapter) Save(records []bilibili.CookieRecord) error {
	state, err := a.storage.Load()
	if err != nil && !errors.Is(err, bilibili.ErrNoCookieFile) {
		return err
	}
	state.HasCookies = true
	state.Cookies = make([]Cookie, len(records))
	for index, record := range records {
		state.Cookies[index] = Cookie{Name: record.Name, Value: record.Value, Domain: record.Domain, Path: record.Path}
	}
	return a.storage.Save(state)
}

func (a storageCookieAdapter) Exists() bool {
	state, err := a.storage.Load()
	return err == nil && state.HasCookies
}
