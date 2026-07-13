package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
)

type Engine struct {
	playlist PlaylistClient
	match    MatchClient
	account  AccountClient
	matcher  VideoMatcher
	now      func() time.Time
}

type Dependencies struct {
	Playlist PlaylistClient
	Match    MatchClient
	Account  AccountClient
	Matcher  VideoMatcher
	Now      func() time.Time
}

func New(deps Dependencies) (*Engine, error) {
	if deps.Playlist == nil || deps.Match == nil || deps.Account == nil || deps.Matcher == nil {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "new", Message: "all engine dependencies are required"}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Engine{playlist: deps.Playlist, match: deps.Match, account: deps.Account, matcher: deps.Matcher, now: deps.Now}, nil
}

func (e *Engine) Login(ctx context.Context, opts LoginOptions, observer Observer) (Account, error) {
	updates := serial(observer, e.now)
	account, err := e.account.Login(ctx, opts, func(update LoginUpdate) {
		kind := EventProgress
		if update.QRPayload != "" {
			kind = EventQR
		}
		updates.emit(ProgressEvent{Kind: kind, Operation: "login", Message: update.Status, QRPayload: update.QRPayload})
	})
	if err != nil {
		return Account{}, classifyContext("login", ErrorAuthentication, err)
	}
	return account, nil
}

func (e *Engine) ParsePlaylist(ctx context.Context, rawURL string, opts ParseOptions, observer Observer) ([]model.Song, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "parse playlist", Message: "playlist URL is required"}
	}
	updates := serial(observer, e.now)
	updates.emit(ProgressEvent{Kind: EventProgress, Operation: "parse_playlist", Message: "正在解析酷狗歌单"})
	songs, err := e.playlist.ParsePlaylist(ctx, rawURL, opts.BrowserPolicy)
	if err != nil {
		return nil, classifyContext("parse playlist", ErrorExtraction, err)
	}
	if len(songs) == 0 {
		return nil, &OperationError{Category: ErrorExtraction, Operation: "parse playlist", Message: "未能提取歌曲"}
	}
	updates.emit(ProgressEvent{Kind: EventProgress, Operation: "parse_playlist", Message: "歌单解析完成", Current: len(songs), Total: len(songs)})
	return append([]model.Song(nil), songs...), nil
}

func (e *Engine) Match(ctx context.Context, songs []model.Song, opts MatchOptions, observer Observer) ([]MatchOutcome, error) {
	if len(songs) == 0 {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "match", Message: "songs are required"}
	}
	opts = opts.normalized()
	if opts.Workers > len(songs) {
		opts.Workers = len(songs)
	}
	updates := serial(observer, e.now)
	outcomes := make([]MatchOutcome, len(songs))
	jobs := make(chan int)
	var workers sync.WaitGroup
	for worker := 0; worker < opts.Workers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				outcome := e.matchSong(ctx, songs[index], opts)
				if outcome.Failure != nil {
					outcome.Failure.Index = index
				}
				outcomes[index] = outcome
				event := ProgressEvent{Kind: EventSong, Operation: "match", Current: index + 1, Total: len(songs), Song: &outcomes[index].Song}
				if outcome.HasSelection {
					event.Match = &outcomes[index].Selected
				}
				updates.emit(event)
			}
		}()
	}

	feedCancelled := false
	for index := range songs {
		select {
		case jobs <- index:
		case <-ctx.Done():
			feedCancelled = true
		}
		if feedCancelled {
			break
		}
	}
	close(jobs)
	workers.Wait()
	if err := ctx.Err(); err != nil {
		return outcomes, classifyContext("match", ErrorCancelled, err)
	}

	failures := make([]ItemFailure, 0)
	for _, outcome := range outcomes {
		if outcome.Failure != nil {
			failures = append(failures, *outcome.Failure)
		}
	}
	if len(failures) > 0 {
		return outcomes, &BatchError{Category: ErrorNetwork, Failures: failures}
	}
	return outcomes, nil
}

func (e *Engine) matchSong(ctx context.Context, song model.Song, opts MatchOptions) MatchOutcome {
	outcome := MatchOutcome{Song: song}
	queries := song.AllSearchKeywords()
	primary := song.SearchKeywordFull()
	if len(queries) == 0 || queries[0] != primary {
		queries = append([]string{primary}, queries...)
	}
	queries = uniqueStrings(queries)
	allVideos := make([]model.Video, 0)
	seenVideos := make(map[string]struct{})
	for _, query := range queries {
		videos := make([]model.Video, 0, opts.SearchPages*20)
		for page := 1; page <= opts.SearchPages; page++ {
			pageVideos, err := e.match.SearchVideos(ctx, query, page, 20)
			if err != nil {
				outcome.Failure = &ItemFailure{Operation: "search", Item: song.Name, Reason: err.Error()}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					return outcome
				}
				continue
			}
			videos = append(videos, pageVideos...)
			for _, video := range pageVideos {
				key := video.BVID
				if key == "" {
					key = video.Title + "\x00" + video.Uploader
				}
				if _, ok := seenVideos[key]; !ok {
					seenVideos[key] = struct{}{}
					allVideos = append(allVideos, video)
				}
			}
		}
		candidates := e.matcher.Match(song, videos, opts.TopK)
		if len(candidates) > 0 && candidates[0].Matched {
			outcome.Selected = candidates[0]
			outcome.HasSelection = true
			outcome.Candidates = append([]model.MatchResult(nil), candidates...)
			return outcome
		}
	}
	// Preserve useful review candidates even when the automatic threshold was
	// not reached. The selected field remains empty.
	outcome.Candidates = e.matcher.Match(song, allVideos, opts.TopK)
	return outcome
}

func (e *Engine) SearchCandidates(ctx context.Context, song model.Song, query string, limit int) ([]model.MatchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "search", Message: "query is required"}
	}
	if limit < 1 {
		limit = 10
	}
	videos, err := e.match.SearchVideos(ctx, query, 1, limit)
	if err != nil {
		return nil, classifyContext("search", ErrorNetwork, err)
	}
	return e.matcher.Match(song, videos, len(videos)), nil
}

func (e *Engine) VideoDetail(ctx context.Context, bvid string) (model.Video, error) {
	video, err := e.match.VideoDetail(ctx, bvid)
	if err != nil {
		return model.Video{}, classifyContext("video detail", ErrorNetwork, err)
	}
	return video, nil
}

func (e *Engine) ListFavorites(ctx context.Context) ([]model.Favorite, error) {
	favorites, err := e.account.ListFavorites(ctx)
	if err != nil {
		return nil, classifyContext("list favorites", ErrorAuthentication, err)
	}
	return append([]model.Favorite(nil), favorites...), nil
}

func (e *Engine) CreateFavorite(ctx context.Context, request CreateFavoriteRequest) (model.Favorite, error) {
	if strings.TrimSpace(request.Title) == "" {
		return model.Favorite{}, &OperationError{Category: ErrorInvalidInput, Operation: "create favorite", Message: "title is required"}
	}
	favorite, err := e.account.CreateFavorite(ctx, request)
	if err != nil {
		return model.Favorite{}, classifyContext("create favorite", ErrorWriteFailed, err)
	}
	return favorite, nil
}

func (e *Engine) AddToFavorite(ctx context.Context, favoriteID int64, outcomes []MatchOutcome, observer Observer) (AddResult, error) {
	if favoriteID <= 0 {
		return AddResult{}, &OperationError{Category: ErrorInvalidInput, Operation: "add favorite", Message: "favorite ID must be positive"}
	}
	videos := make([]model.Video, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.HasSelection && outcome.Selected.Video != nil {
			videos = append(videos, *outcome.Selected.Video)
		}
	}
	if len(videos) == 0 {
		return AddResult{FavoriteID: favoriteID}, &OperationError{Category: ErrorNoMatches, Operation: "add favorite", Message: "no selected videos"}
	}
	updates := serial(observer, e.now)
	result, err := e.account.AddToFavorite(ctx, favoriteID, videos)
	for index, bvid := range result.Succeeded {
		updates.emit(ProgressEvent{Kind: EventVideo, Operation: "add_favorite", Message: bvid, Current: index + 1, Total: len(videos)})
	}
	if err != nil {
		category := ErrorPartialWrite
		if len(result.Succeeded) == 0 {
			category = ErrorWriteFailed
		}
		return result, classifyContext("add favorite", category, err)
	}
	if len(result.Failed) > 0 {
		category := ErrorPartialWrite
		if len(result.Succeeded) == 0 {
			category = ErrorWriteFailed
		}
		return result, &OperationError{Category: category, Operation: "add favorite", Message: fmt.Sprintf("%d video(s) failed", len(result.Failed))}
	}
	return result, nil
}

func classifyContext(operation string, fallback ErrorCategory, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return &OperationError{Category: ErrorCancelled, Operation: operation, Err: err}
	}
	var operationErr *OperationError
	if errors.As(err, &operationErr) {
		return operationErr
	}
	return &OperationError{Category: fallback, Operation: operation, Err: err}
}

func uniqueStrings(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
