package service

import (
	"context"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
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
	Profile     MatchProfile
	Weights     *MatchWeights
}

type MatchProfile string

const (
	MatchProfileStandard  MatchProfile = "standard"
	MatchProfileClassical MatchProfile = "classical"
)

type MatchWeights struct {
	Title      float64
	Artist     float64
	Quality    float64
	Official   float64
	Popularity float64
	Uploader   float64
}

type CandidateSearchOptions struct {
	Limit   int
	Profile MatchProfile
	Weights *MatchWeights
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
	NeedsReview    bool
	ReviewReason   model.ReviewReason
}

type QueryPhase struct {
	Queries []string
}

type MatchDecision struct {
	// SelectedIndex is -1 when no candidate is selected.
	SelectedIndex int
	Continue      bool
	ReviewReason  model.ReviewReason
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
	ParsePlaylist(ctx context.Context, rawURL string, policy BrowserPolicy, onBrowserFallback func()) (PlaylistResult, error)
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

type MatchStrategy interface {
	QueryPhases(model.Song) []QueryPhase
	Rank(model.Song, []model.Video, int) []model.MatchResult
	Decide(model.Song, []model.MatchResult, bool) MatchDecision
}

// MatchStrategyResolver creates an immutable scorer for one public operation.
// Strategies that only support the standard defaults may omit this interface.
type MatchStrategyResolver interface {
	ResolveMatchStrategy(MatchProfile, *MatchWeights) (MatchStrategy, error)
}
