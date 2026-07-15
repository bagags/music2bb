package music2bb

import "time"

type Song struct {
	Name     string
	Artist   string
	Album    string
	Duration string
	Hash     string
}

func (s Song) SearchKeyword() string { return songToInternal(s).SearchKeyword() }

func (s Song) SearchKeywordFull() string { return songToInternal(s).SearchKeywordFull() }

func (s Song) AllSearchKeywords() []string {
	return append([]string(nil), songToInternal(s).AllSearchKeywords()...)
}

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
	Song            Song
	Video           *Video
	Score           float64
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
	Candidates      []MatchResult
	Failure         *ItemFailure
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

type AddResult struct {
	FavoriteID int64
	Succeeded  []string
	Failed     []AddFailure
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
	QRPayload string
	At        time.Time
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
