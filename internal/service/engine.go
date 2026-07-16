package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
)

type Engine struct {
	playlist PlaylistClient
	match    MatchClient
	account  AccountClient
	strategy MatchStrategy
	now      func() time.Time
}

type Dependencies struct {
	Playlist PlaylistClient
	Match    MatchClient
	Account  AccountClient
	Strategy MatchStrategy
	Now      func() time.Time
}

func New(deps Dependencies) (*Engine, error) {
	if deps.Playlist == nil || deps.Match == nil || deps.Account == nil || deps.Strategy == nil {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "new", Message: "all engine dependencies are required"}
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	return &Engine{playlist: deps.Playlist, match: deps.Match, account: deps.Account, strategy: deps.Strategy, now: deps.Now}, nil
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

func (e *Engine) Logout(ctx context.Context) error {
	return classifyContext("logout", ErrorInternal, e.account.Logout(ctx))
}

func (e *Engine) ParsePlaylist(ctx context.Context, rawURL string, opts ParseOptions, observer Observer) ([]model.Song, error) {
	if strings.TrimSpace(rawURL) == "" {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "parse playlist", Message: "playlist URL is required"}
	}
	updates := serial(observer, e.now)
	updates.emit(ProgressEvent{Kind: EventProgress, Operation: "parse_playlist", Message: "正在解析歌单"})
	result, err := e.playlist.ParsePlaylist(ctx, rawURL, opts.BrowserPolicy, func() {
		updates.emit(ProgressEvent{
			Kind: EventWarning, Operation: "parse_playlist",
			Message: "HTTP 解析失败或结果不完整，正在自动切换到 Chromium。",
		})
	})
	if err != nil {
		return nil, classifyContext("parse playlist", ErrorExtraction, err)
	}
	songs := result.Songs
	if len(songs) == 0 {
		return nil, &OperationError{Category: ErrorExtraction, Operation: "parse playlist", Message: "未能提取歌曲"}
	}
	total := result.ExpectedTotal
	if total <= 0 {
		total = len(songs)
	}
	if result.ExpectedTotal > len(songs) {
		updates.emit(ProgressEvent{
			Kind: EventWarning, Operation: "parse_playlist",
			Message: fmt.Sprintf("警告：歌单抓取不完整，实际 %d / 预期 %d 首；将继续处理已获取歌曲", len(songs), result.ExpectedTotal),
			Current: len(songs), Total: result.ExpectedTotal,
		})
	}
	updates.emit(ProgressEvent{Kind: EventProgress, Operation: "parse_playlist", Message: "歌单解析完成", Current: len(songs), Total: total})
	return append([]model.Song(nil), songs...), nil
}

func (e *Engine) Match(ctx context.Context, songs []model.Song, opts MatchOptions, observer Observer) ([]MatchOutcome, error) {
	if len(songs) == 0 {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "match", Message: "songs are required"}
	}
	strategy, err := e.resolveMatchStrategy("match", opts.Profile, opts.Weights)
	if err != nil {
		return nil, err
	}
	opts = opts.normalized()
	if opts.SearchIdentity != SearchIdentityAnonymous && opts.SearchIdentity != SearchIdentitySession {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "match", Message: fmt.Sprintf("unknown search identity %q", opts.SearchIdentity)}
	}
	if opts.SearchCachePolicy != SearchCacheDefault && opts.SearchCachePolicy != SearchCacheBypass && opts.SearchCachePolicy != SearchCacheRefresh {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "match", Message: fmt.Sprintf("unknown search cache policy %q", opts.SearchCachePolicy)}
	}
	if opts.Workers > len(songs) {
		opts.Workers = len(songs)
	}
	matchCtx, cancelMatch := context.WithCancelCause(ctx)
	defer cancelMatch(nil)
	updates := serial(observer, e.now)
	outcomes := make([]MatchOutcome, len(songs))
	for index, song := range songs {
		outcomes[index] = MatchOutcome{
			Song: song, NeedsReview: true, ReviewReason: model.ReviewNotSearched,
			SearchIdentity: opts.SearchIdentity, SearchStatus: SearchStatusNotSearched,
		}
	}
	jobs := make(chan int)
	var progressMu sync.Mutex
	completed := 0
	var workers sync.WaitGroup
	for worker := 0; worker < opts.Workers; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := range jobs {
				outcome, fatalErr := e.matchSong(matchCtx, strategy, songs[index], opts)
				if outcome.Failure != nil {
					outcome.Failure.Index = index
				}
				outcomes[index] = outcome
				if fatalErr != nil {
					cancelMatch(fatalErr)
				}
				progressMu.Lock()
				completed++
				event := ProgressEvent{Kind: EventSong, Operation: "match", Current: completed, Total: len(songs), Song: &outcomes[index].Song, Outcome: &outcomes[index]}
				if outcome.HasSelection {
					event.Match = &outcomes[index].Selected
				}
				updates.emit(event)
				progressMu.Unlock()
			}
		}()
	}

	feedCancelled := false
	for index := range songs {
		if matchCtx.Err() != nil {
			feedCancelled = true
			break
		}
		select {
		case jobs <- index:
		case <-matchCtx.Done():
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

	cause := context.Cause(matchCtx)
	if reason := riskControlReason(cause); reason != "" {
		return outcomes, &BatchError{
			Category: ErrorNetwork, HaltReason: reason, SearchIdentity: opts.SearchIdentity,
		}
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

func (e *Engine) matchSong(ctx context.Context, strategy MatchStrategy, song model.Song, opts MatchOptions) (MatchOutcome, error) {
	outcome := MatchOutcome{
		Song: song, NeedsReview: true, ReviewReason: model.ReviewNotSearched,
		SearchIdentity: opts.SearchIdentity, SearchStatus: SearchStatusNotSearched,
	}
	queries := flattenQueries(strategy.QueryPhases(song))
	allVideos := make([]model.Video, 0)
	seenVideos := make(map[string]struct{})
	var lastSearchErr error
	budgetLimited := false
	for page := 1; page <= opts.SearchPages; page++ {
		for _, query := range queries {
			cacheOnly := outcome.RemoteRequests >= opts.SearchBudget
			if cacheOnly && opts.SearchCachePolicy != SearchCacheDefault {
				budgetLimited = true
				break
			}
			response, err := e.searchVideos(ctx, SearchRequest{
				Keyword: query, Page: page, PageSize: 20, Identity: opts.SearchIdentity, CachePolicy: opts.SearchCachePolicy, CacheOnly: cacheOnly,
			})
			if isSearchCacheMiss(err) {
				budgetLimited = true
				continue
			}
			if response.CacheHit {
				outcome.CacheHits++
			} else if response.RemoteRequest {
				outcome.RemoteRequests++
			}
			if err != nil {
				lastSearchErr = err
				if reason := riskControlReason(err); reason != "" {
					outcome.Candidates = retainCandidates(strategy.Rank(song, append([]model.Video(nil), allVideos...), len(allVideos)), opts.TopK)
					outcome.SearchStatus = SearchStatusRiskControl
					outcome.ReviewReason = model.ReviewRiskControl
					outcome.RiskReason = reason
					return outcome, err
				}
				if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
					cause := context.Cause(ctx)
					if cause != nil && !errors.Is(cause, context.Canceled) && !errors.Is(cause, context.DeadlineExceeded) {
						outcome.Candidates = retainCandidates(strategy.Rank(song, append([]model.Video(nil), allVideos...), len(allVideos)), opts.TopK)
						outcome.SearchStatus = SearchStatusNotSearched
						outcome.ReviewReason = model.ReviewNotSearched
						return outcome, nil
					}
					outcome.Failure = &ItemFailure{Operation: "search", Item: song.Name, Reason: err.Error()}
					outcome.NeedsReview = true
					outcome.ReviewReason = model.ReviewSearchFailed
					outcome.SearchStatus = SearchStatusFailed
					return outcome, nil
				}
				if isBatchFatal(err) {
					outcome.Failure = &ItemFailure{Operation: "search", Item: song.Name, Reason: err.Error()}
					outcome.NeedsReview = true
					outcome.ReviewReason = model.ReviewSearchFailed
					outcome.SearchStatus = SearchStatusFailed
					return outcome, err
				}
				continue
			}
			pageVideos := response.Videos
			for _, video := range pageVideos {
				key := video.BVID
				if key == "" {
					key = video.Title + "\x00" + video.Uploader
				}
				if _, ok := seenVideos[key]; ok {
					continue
				}
				seenVideos[key] = struct{}{}
				allVideos = append(allVideos, video)
			}
			ranked := strategy.Rank(song, append([]model.Video(nil), allVideos...), len(allVideos))
			decision := strategy.Decide(song, ranked, false)
			outcome.Candidates = retainCandidates(ranked, opts.TopK)
			if decision.SelectedIndex >= 0 && decision.SelectedIndex < len(ranked) {
				outcome.Selected = ranked[decision.SelectedIndex]
				outcome.HasSelection = outcome.Selected.Video != nil
				outcome.NeedsReview = !outcome.HasSelection
				outcome.ReviewReason = model.ReviewNone
				outcome.SearchStatus = SearchStatusCompleted
				return outcome, nil
			}
			outcome.ReviewReason = decision.ReviewReason
		}
	}
	ranked := strategy.Rank(song, append([]model.Video(nil), allVideos...), len(allVideos))
	decision := strategy.Decide(song, ranked, true)
	outcome.Candidates = retainCandidates(ranked, opts.TopK)
	if decision.SelectedIndex >= 0 && decision.SelectedIndex < len(ranked) {
		outcome.Selected = ranked[decision.SelectedIndex]
		outcome.HasSelection = outcome.Selected.Video != nil
		outcome.NeedsReview = !outcome.HasSelection
		outcome.ReviewReason = model.ReviewNone
		outcome.SearchStatus = SearchStatusCompleted
		return outcome, nil
	}
	outcome.ReviewReason = decision.ReviewReason
	if lastSearchErr != nil && len(outcome.Candidates) == 0 {
		outcome.Failure = &ItemFailure{Operation: "search", Item: song.Name, Reason: lastSearchErr.Error()}
		outcome.ReviewReason = model.ReviewSearchFailed
		outcome.SearchStatus = SearchStatusFailed
	} else if budgetLimited {
		outcome.ReviewReason = model.ReviewBudgetExhausted
		outcome.SearchStatus = SearchStatusBudgetExhausted
	} else if len(outcome.Candidates) == 0 {
		outcome.ReviewReason = model.ReviewNoCandidates
		outcome.SearchStatus = SearchStatusCompleted
	} else {
		outcome.SearchStatus = SearchStatusCompleted
	}
	outcome.NeedsReview = true
	return outcome, nil
}

func flattenQueries(phases []QueryPhase) []string {
	queries := make([]string, 0)
	for _, phase := range phases {
		for _, query := range phase.Queries {
			query = strings.TrimSpace(query)
			if query != "" && !containsString(queries, query) {
				queries = append(queries, query)
			}
		}
	}
	return queries
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func riskControlReason(err error) RiskControlReason {
	type riskControlled interface{ RiskControlReason() string }
	var risk riskControlled
	if errors.As(err, &risk) {
		return RiskControlReason(risk.RiskControlReason())
	}
	return ""
}

func isBatchFatal(err error) bool {
	type batchFatal interface {
		BatchFatal() bool
	}
	var fatal batchFatal
	return errors.As(err, &fatal) && fatal.BatchFatal()
}

func retainCandidates(ranked []model.MatchResult, limit int) []model.MatchResult {
	if limit < len(ranked) {
		ranked = ranked[:limit]
	}
	return append([]model.MatchResult(nil), ranked...)
}

func (e *Engine) SearchCandidates(ctx context.Context, song model.Song, query string, limit int) ([]model.MatchResult, error) {
	return e.SearchCandidatesWithOptions(ctx, song, query, CandidateSearchOptions{Limit: limit})
}

func (e *Engine) SearchCandidatesWithOptions(ctx context.Context, song model.Song, query string, options CandidateSearchOptions) ([]model.MatchResult, error) {
	if strings.TrimSpace(query) == "" {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "search", Message: "query is required"}
	}
	strategy, err := e.resolveMatchStrategy("search", options.Profile, options.Weights)
	if err != nil {
		return nil, err
	}
	if options.Limit < 1 {
		options.Limit = 10
	}
	if options.SearchIdentity == "" {
		options.SearchIdentity = SearchIdentityAnonymous
	}
	if options.SearchIdentity != SearchIdentityAnonymous && options.SearchIdentity != SearchIdentitySession {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "search", Message: fmt.Sprintf("unknown search identity %q", options.SearchIdentity)}
	}
	if options.SearchCachePolicy != SearchCacheDefault && options.SearchCachePolicy != SearchCacheBypass && options.SearchCachePolicy != SearchCacheRefresh {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: "search", Message: fmt.Sprintf("unknown search cache policy %q", options.SearchCachePolicy)}
	}
	response, err := e.searchVideos(ctx, SearchRequest{
		Keyword: query, Page: 1, PageSize: options.Limit, Identity: options.SearchIdentity, CachePolicy: options.SearchCachePolicy,
	})
	if err != nil {
		return nil, &OperationError{
			Category: ErrorNetwork, Operation: "search", Err: err,
			RiskReason: riskControlReason(err), SearchIdentity: options.SearchIdentity,
		}
	}
	return strategy.Rank(song, response.Videos, len(response.Videos)), nil
}

func (e *Engine) searchVideos(ctx context.Context, request SearchRequest) (SearchResponse, error) {
	if client, ok := e.match.(MatchClientWithSearchMetadata); ok {
		return client.SearchVideosWithMetadata(ctx, request)
	}
	if request.CacheOnly {
		return SearchResponse{}, searchCacheMissError{}
	}
	videos, err := e.match.SearchVideos(ctx, request)
	return SearchResponse{Videos: videos, RemoteRequest: true}, err
}

type searchCacheMissError struct{}

func (searchCacheMissError) Error() string         { return "search cache miss" }
func (searchCacheMissError) SearchCacheMiss() bool { return true }

func isSearchCacheMiss(err error) bool {
	type cacheMiss interface{ SearchCacheMiss() bool }
	var miss cacheMiss
	return errors.As(err, &miss) && miss.SearchCacheMiss()
}

func (e *Engine) ResetAnonymousIdentity(ctx context.Context) error {
	resetter, ok := e.match.(AnonymousIdentityResetter)
	if !ok {
		return &OperationError{Category: ErrorInternal, Operation: "reset anonymous identity", Message: "match client does not support anonymous identity reset"}
	}
	return classifyContext("reset anonymous identity", ErrorInternal, resetter.ResetAnonymousIdentity(ctx))
}

func (e *Engine) resolveMatchStrategy(operation string, profile MatchProfile, weights *MatchWeights) (MatchStrategy, error) {
	if profile == "" {
		profile = MatchProfileStandard
	}
	if profile != MatchProfileStandard && profile != MatchProfileClassical {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: operation, Message: fmt.Sprintf("unknown match profile %q", profile)}
	}
	if resolver, ok := e.strategy.(MatchStrategyResolver); ok {
		strategy, err := resolver.ResolveMatchStrategy(profile, weights)
		if err != nil {
			return nil, &OperationError{Category: ErrorInvalidInput, Operation: operation, Err: err}
		}
		return strategy, nil
	}
	if profile != MatchProfileStandard || weights != nil {
		return nil, &OperationError{Category: ErrorInvalidInput, Operation: operation, Message: "match strategy does not support profiles or custom weights"}
	}
	return e.strategy, nil
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
	var result AddResult
	var err error
	if reporting, ok := e.account.(AccountClientWithWriteReceipts); ok {
		completed := 0
		result, err = reporting.AddToFavoriteWithReceipts(ctx, favoriteID, videos, func(receipt WriteReceipt) {
			completed++
			updates.emit(ProgressEvent{
				Kind: EventVideo, Operation: "add_favorite", Message: receipt.BVID,
				Current: completed, Total: len(videos), WriteReceipt: &receipt,
			})
		})
	} else {
		result, err = e.account.AddToFavorite(ctx, favoriteID, videos)
		for index, bvid := range result.Succeeded {
			receipt := WriteReceipt{FavoriteID: favoriteID, BVID: bvid, Succeeded: true}
			updates.emit(ProgressEvent{Kind: EventVideo, Operation: "add_favorite", Message: bvid, Current: index + 1, Total: len(videos), WriteReceipt: &receipt})
		}
		for index, failure := range result.Failed {
			receipt := WriteReceipt{FavoriteID: favoriteID, BVID: failure.BVID, Reason: failure.Reason}
			updates.emit(ProgressEvent{Kind: EventVideo, Operation: "add_favorite", Message: failure.BVID, Current: len(result.Succeeded) + index + 1, Total: len(videos), WriteReceipt: &receipt})
		}
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
