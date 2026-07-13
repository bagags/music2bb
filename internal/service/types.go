package service

import (
	"context"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
)

type Account struct {
	ID   int64
	Name string
}

type LoginOptions struct {
	UseStoredCookies bool
	AllowQR          bool
	Timeout          time.Duration
}

type ParseOptions struct {
	BrowserPolicy BrowserPolicy
}

type PlaylistResult struct {
	Songs         []model.Song
	ExpectedTotal int
}

type BrowserPolicy string

const (
	BrowserAuto   BrowserPolicy = "auto"
	BrowserNever  BrowserPolicy = "never"
	BrowserAlways BrowserPolicy = "always"
)

type MatchOptions struct {
	SearchPages int
	TopK        int
	Workers     int
}

func (o MatchOptions) normalized() MatchOptions {
	if o.SearchPages < 1 {
		o.SearchPages = 3
	}
	if o.TopK < 1 {
		o.TopK = 3
	}
	if o.Workers < 1 {
		o.Workers = 4
	}
	return o
}

type MatchOutcome struct {
	Song           model.Song
	Selected       model.MatchResult
	HasSelection   bool
	Candidates     []model.MatchResult
	Failure        *ItemFailure
	ManualOverride bool
}

type CreateFavoriteRequest struct {
	Title   string
	Intro   string
	Private bool
}

type AddFailure struct {
	BVID   string
	Reason string
}

type AddResult struct {
	FavoriteID int64
	Succeeded  []string
	Failed     []AddFailure
}

type LoginUpdate struct {
	QRPayload string
	Status    string
}

type PlaylistClient interface {
	ParsePlaylist(ctx context.Context, rawURL string, policy BrowserPolicy) (PlaylistResult, error)
}

type MatchClient interface {
	SearchVideos(ctx context.Context, keyword string, page, pageSize int) ([]model.Video, error)
	VideoDetail(ctx context.Context, bvid string) (model.Video, error)
}

type AccountClient interface {
	Login(ctx context.Context, opts LoginOptions, update func(LoginUpdate)) (Account, error)
	ListFavorites(ctx context.Context) ([]model.Favorite, error)
	CreateFavorite(ctx context.Context, request CreateFavoriteRequest) (model.Favorite, error)
	AddToFavorite(ctx context.Context, favoriteID int64, videos []model.Video) (AddResult, error)
}

type VideoMatcher interface {
	Match(song model.Song, videos []model.Video, topK int) []model.MatchResult
}
