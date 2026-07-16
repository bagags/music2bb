package music2bb

import "time"

type Song struct {
	Name     string
	Artist   string
	Album    string
	Duration string
	Hash     string
	// SourceID is stable across playlist ordering and is used for checkpoint
	// alignment and cross-playlist manual decisions.
	SourceID string
}

func (s Song) SearchKeyword() string { return songToInternal(s).SearchKeyword() }

func (s Song) SearchKeywordFull() string { return songToInternal(s).SearchKeywordFull() }

func (s Song) AllSearchKeywords() []string {
	return append([]string(nil), songToInternal(s).AllSearchKeywords()...)
}

// StableSourceID returns SourceID or a deterministic source metadata
// fingerprint when a provider did not expose a native track identifier.
func (s Song) StableSourceID() string { return songToInternal(s).StableSourceID() }

type Video struct {
	BVID          string
	AID           int64
	Title         string
	Uploader      string
	Duration      string
	PlayCount     int64
	FavoriteCount int64
	DanmakuCount  int64
	Description   string
	Tags          []string
	IsOfficial    bool
	IsVerified    bool
}

func (v Video) URL() string { return "https://www.bilibili.com/video/" + v.BVID }

// MatchResult represents either a selected result returned by Match or one
// ranked candidate returned by SearchCandidates/in Candidates.
type MatchResult struct {
	Song        Song
	Video       *Video
	Score       float64
	TitleScore  float64
	ArtistScore float64
	// KeywordScore remains an alias of TitleScore for source compatibility.
	// Deprecated: use TitleScore.
	KeywordScore    float64
	QualityScore    float64
	OfficialScore   float64
	PopularityScore float64
	UploaderScore   float64
	Matched         bool
	HasSelection    bool
	ManualOverride  bool
	NeedsReview     bool
	ReviewReason    ReviewReason
	// SearchIdentity is the isolated identity used for the final search attempt.
	SearchIdentity SearchIdentity
	// SearchStatus distinguishes completed, halted, unsearched, exhausted, and failed work.
	SearchStatus SearchStatus
	// RemoteRequests counts budget-consuming remote search pages for this run.
	RemoteRequests int
	// CacheHits counts budget-free persistent search page hits.
	CacheHits int
	// RiskReason identifies the platform signal that halted this song, when any.
	RiskReason RiskControlReason
	Candidates []MatchResult
	Failure    *ItemFailure
}

// ReviewReason explains why a song could not be selected automatically.
type ReviewReason string

const (
	ReviewNone             ReviewReason = ""
	ReviewNoCandidates     ReviewReason = "no_candidates"
	ReviewSearchFailed     ReviewReason = "search_failed"
	ReviewWeakTitle        ReviewReason = "weak_title"
	ReviewArtistUnverified ReviewReason = "artist_unverified"
	ReviewAmbiguous        ReviewReason = "ambiguous"
	ReviewRiskControl      ReviewReason = "risk_control"
	ReviewNotSearched      ReviewReason = "not_searched"
	ReviewBudgetExhausted  ReviewReason = "budget_exhausted"
)

// SearchStatus describes how far remote search progressed for one song.
type SearchStatus string

const (
	// SearchStatusCompleted means adaptive matching reached a final reviewable outcome.
	SearchStatusCompleted SearchStatus = "completed"
	// SearchStatusRiskControl means the current request was rejected by platform risk control.
	SearchStatusRiskControl SearchStatus = "risk_control"
	// SearchStatusNotSearched means batch halt prevented remote work for this song.
	SearchStatusNotSearched SearchStatus = "not_searched"
	// SearchStatusBudgetExhausted means no further uncached page fit the per-song budget.
	SearchStatusBudgetExhausted SearchStatus = "budget_exhausted"
	// SearchStatusFailed means ordinary remote search attempts failed.
	SearchStatusFailed SearchStatus = "failed"
)

// RiskControlReason is a machine-readable Bilibili risk-control signal.
type RiskControlReason string

const (
	// RiskControlVoucher is a Gaia challenge response.
	RiskControlVoucher RiskControlReason = "voucher"
	// RiskControlHTTP412 is an HTTP 412 rejection.
	RiskControlHTTP412 RiskControlReason = "http_412"
	// RiskControlCode412 is Bilibili API code -412.
	RiskControlCode412 RiskControlReason = "code_-412"
	// RiskControlCode1200 is Bilibili API code -1200.
	RiskControlCode1200 RiskControlReason = "code_-1200"
)

type Favorite struct {
	ID         int64
	Title      string
	Count      int
	MediaCount int
}

type Account struct {
	ID   int64
	Name string
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

// AddResult reports favorite writes, failures, and checkpoint-idempotent skips.
type AddResult struct {
	FavoriteID int64
	Succeeded  []string
	Failed     []AddFailure
	// Skipped contains BVIDs already recorded as successful in the current
	// conversion checkpoint. They were not submitted again.
	Skipped []string
}

// WriteReceipt reports the result of one favorite-write attempt. Receipt
// events are emitted immediately so frontends can persist partial progress.
type WriteReceipt struct {
	FavoriteID int64
	BVID       string
	Succeeded  bool
	Reason     string
}

type ItemFailure struct {
	Index     int
	Operation string
	Item      string
	Reason    string
}

type EventKind string

const (
	EventProgress EventKind = "progress"
	EventWarning  EventKind = "warning"
	EventQR       EventKind = "qr"
	EventSong     EventKind = "song"
	EventVideo    EventKind = "video"
)

type ProgressEvent struct {
	Kind      EventKind
	Operation string
	Message   string
	Current   int
	Total     int
	Song      *Song
	Match     *MatchResult
	// Outcome contains the complete per-song match state for EventSong. Match
	// remains the selected candidate for compatibility with existing observers.
	Outcome *MatchResult
	// WriteReceipt is set on EventVideo for each favorite-write attempt.
	WriteReceipt *WriteReceipt
	QRPayload    string
	At           time.Time
}

type Observer interface {
	Observe(ProgressEvent)
}

type ObserverFunc func(ProgressEvent)

func (f ObserverFunc) Observe(event ProgressEvent) {
	if f != nil {
		f(event)
	}
}

type Cookie struct {
	Name   string
	Value  string
	Domain string
	Path   string
}

type StoredState struct {
	BlockKeywords     []string
	QualityKeywords   []string
	WeightedUploaders []string
	Cookies           []Cookie
	HasCookies        bool
}
