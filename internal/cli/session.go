package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	music2bb "github.com/bagags/music2bb-go"
)

// conversionSession is the shared controller boundary used by both terminal
// frontends. It owns conversion policy while leaving presentation to the
// caller.
type conversionSession struct {
	backend      Backend
	browser      BrowserManager
	rawURL       string
	options      convertOptions
	policy       music2bb.BrowserPolicy
	loginMu      sync.Mutex
	account      music2bb.Account
	loggedIn     bool
	writeBlocked bool
	haltReason   music2bb.RiskControlReason
	state        *conversionState
	refreshOnce  sync.Once
	refreshErr   error
	telemetryMu  sync.Mutex
	telemetry    conversionTelemetry
}

type conversionTelemetry struct {
	anonymousRequests int
	sessionRequests   int
	cacheHits         int
	budgetCapacity    int
}

func newConversionSession(backend Backend, browser BrowserManager, rawURL string, options convertOptions, policy music2bb.BrowserPolicy) *conversionSession {
	session := &conversionSession{backend: backend, browser: browser, rawURL: rawURL, options: options, policy: policy}
	if provider, ok := backend.(interface{ PersistentStatePaths() (string, string) }); ok {
		configDir, _ := provider.PersistentStatePaths()
		session.state = newConversionState(configDir, rawURL, time.Now)
	}
	return session
}

func shouldUseTUI(io IO, disabled bool) bool {
	return io.Interactive && !disabled && !strings.EqualFold(os.Getenv("TERM"), "dumb")
}

func (s *conversionSession) login(ctx context.Context, observer music2bb.Observer) (music2bb.Account, error) {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	if s.loggedIn {
		return s.account, nil
	}
	account, err := s.backend.LoginWithOptions(ctx, music2bb.LoginOptions{UseStoredCookies: true, AllowQR: s.options.qrLogin}, observer)
	if err == nil {
		s.account, s.loggedIn = account, true
	}
	return account, err
}

func (s *conversionSession) parse(ctx context.Context, observer music2bb.Observer) ([]music2bb.Song, error) {
	incomplete := false
	tracking := music2bb.ObserverFunc(func(event music2bb.ProgressEvent) {
		if event.Kind == music2bb.EventWarning && event.Operation == "parse_playlist" && event.Total > 0 && event.Current < event.Total {
			incomplete = true
		}
		if observer != nil {
			observer.Observe(event)
		}
	})
	songs, err := s.backend.ParsePlaylistWithOptions(ctx, s.rawURL, music2bb.ParseOptions{BrowserPolicy: s.policy}, tracking)
	if (err == nil && !incomplete) || s.policy == music2bb.BrowserNever || s.browser == nil {
		return songs, err
	}
	status, statusErr := s.browser.Status(ctx)
	if statusErr != nil || status.Installed {
		return songs, err
	}
	message := fmt.Sprintf("Chromium 尚未就绪，正在自动下载并安装校验版（%s）后重试。", browserDownloadSize(status))
	if status.Bundled {
		message = "Chromium 尚未就绪，正在自动安装程序内置版本后重试。"
	}
	emitSessionWarning(observer, "parse_playlist", message)
	if _, installErr := s.browser.Install(ctx, true); installErr != nil {
		emitSessionWarning(observer, "parse_playlist", fmt.Sprintf("浏览器安装失败: %v", installErr))
		return songs, err
	}
	retrySongs, retryErr := s.backend.ParsePlaylistWithOptions(ctx, s.rawURL, music2bb.ParseOptions{BrowserPolicy: music2bb.BrowserAlways}, observer)
	if err != nil || retryErr == nil {
		return retrySongs, retryErr
	}
	emitSessionWarning(observer, "parse_playlist", fmt.Sprintf("Chromium 回退失败，将继续使用 HTTP 部分结果: %v", retryErr))
	return songs, err
}

func (s *conversionSession) match(ctx context.Context, songs []music2bb.Song, observer music2bb.Observer) ([]music2bb.MatchResult, error) {
	s.telemetryMu.Lock()
	s.telemetry = conversionTelemetry{}
	s.telemetryMu.Unlock()
	if err := s.prepareSearchState(ctx); err != nil {
		return nil, err
	}
	restored, err := s.state.restore(songs, s.options.fresh)
	if err != nil {
		return nil, persistentStateError("restore conversion", err)
	}
	outcomes := make([]music2bb.MatchResult, len(songs))
	pendingSongs := make([]music2bb.Song, 0, len(songs))
	pendingIndexes := make([]int, 0, len(songs))
	restoredCount := 0
	for index, song := range songs {
		song = songWithStableID(song)
		if outcome, ok := restored[stableSongID(song)]; ok {
			outcomes[index] = outcome
			restoredCount++
			if observer != nil {
				copy := outcome
				event := music2bb.ProgressEvent{Kind: music2bb.EventSong, Operation: "match", Current: restoredCount, Total: len(songs), Song: &copy.Song, Outcome: &copy}
				if copy.HasSelection {
					event.Match = &copy
				}
				observer.Observe(event)
			}
			continue
		}
		outcomes[index] = music2bb.MatchResult{
			Song: song, NeedsReview: true, ReviewReason: music2bb.ReviewNotSearched,
			SearchStatus: music2bb.SearchStatusNotSearched,
		}
		pendingSongs = append(pendingSongs, song)
		pendingIndexes = append(pendingIndexes, index)
	}
	s.telemetryMu.Lock()
	s.telemetry.budgetCapacity = len(pendingSongs) * normalizedSearchBudget(s.options.searchBudget)
	s.telemetryMu.Unlock()
	if len(pendingSongs) == 0 {
		s.emitVerboseSearchSummary(observer, outcomes)
		return outcomes, nil
	}
	var persistenceMu sync.Mutex
	var persistenceErr error
	trackingObserver := music2bb.ObserverFunc(func(event music2bb.ProgressEvent) {
		if event.Kind == music2bb.EventSong && event.Operation == "match" {
			event.Current += restoredCount
			event.Total = len(songs)
			if event.Outcome != nil {
				if saveErr := s.state.saveOutcome(*event.Outcome); saveErr != nil {
					persistenceMu.Lock()
					if persistenceErr == nil {
						persistenceErr = persistentStateError("save conversion checkpoint", saveErr)
					}
					persistenceMu.Unlock()
				}
			}
		}
		if observer != nil {
			observer.Observe(event)
		}
	})
	pendingOutcomes, matchErr := s.matchUnrestored(ctx, pendingSongs, trackingObserver)
	for position, index := range pendingIndexes {
		if position < len(pendingOutcomes) {
			outcomes[index] = pendingOutcomes[position]
		}
		if saveErr := s.state.saveOutcome(outcomes[index]); saveErr != nil && persistenceErr == nil {
			persistenceErr = persistentStateError("save conversion checkpoint", saveErr)
		}
	}
	persistenceMu.Lock()
	defer persistenceMu.Unlock()
	s.emitVerboseSearchSummary(observer, outcomes)
	return outcomes, joinSessionErrors(matchErr, persistenceErr)
}

func (s *conversionSession) matchUnrestored(ctx context.Context, songs []music2bb.Song, observer music2bb.Observer) ([]music2bb.MatchResult, error) {
	if s.options.manual {
		outcomes := make([]music2bb.MatchResult, len(songs))
		for index, song := range songs {
			outcomes[index] = music2bb.MatchResult{Song: song, NeedsReview: true, ReviewReason: music2bb.ReviewNoCandidates}
		}
		return outcomes, nil
	}
	if err := s.ensureSearchAllowed(); err != nil {
		return nil, err
	}
	identity := music2bb.SearchIdentityAnonymous
	if s.options.searchIdentity == string(music2bb.SearchIdentitySession) {
		if _, err := s.login(ctx, observer); err != nil {
			return nil, err
		}
		identity = music2bb.SearchIdentitySession
	}
	baseOptions := music2bb.MatchOptions{
		SearchPages:       s.options.searchPages,
		TopK:              s.options.topK,
		Workers:           s.options.workers,
		Profile:           music2bb.MatchProfile(s.options.matchProfile),
		SearchIdentity:    identity,
		SearchBudget:      s.options.searchBudget,
		SearchCachePolicy: s.searchCachePolicy(),
	}
	outcomes, err := s.backend.Match(ctx, songs, baseOptions, observer)
	s.addRemoteRequests(identity, outcomes)
	if riskReasonOf(err) == "" || identity == music2bb.SearchIdentitySession || s.options.searchIdentity != "auto" {
		if riskReasonOf(err) != "" {
			s.blockWrites(riskReasonOf(err))
		}
		return outcomes, err
	}
	if _, loginErr := s.login(ctx, observer); loginErr != nil {
		s.blockWrites(riskReasonOf(err))
		return outcomes, loginErr
	}
	pending := 0
	for _, outcome := range outcomes {
		if outcome.SearchStatus == music2bb.SearchStatusNotSearched || outcome.SearchStatus == music2bb.SearchStatusRiskControl {
			pending++
		}
	}
	emitSessionWarning(observer, "search_identity", fmt.Sprintf("匿名搜索触发风控；已切换登录态继续 %d 首未完成歌曲。", pending))
	var firstErr error
	for index := range outcomes {
		if outcomes[index].SearchStatus != music2bb.SearchStatusNotSearched && outcomes[index].SearchStatus != music2bb.SearchStatusRiskControl {
			continue
		}
		remaining := s.options.searchBudget - outcomes[index].RemoteRequests
		if remaining <= 0 {
			outcomes[index].SearchStatus = music2bb.SearchStatusBudgetExhausted
			outcomes[index].ReviewReason = music2bb.ReviewBudgetExhausted
			outcomes[index].RiskReason = ""
			continue
		}
		fallbackOptions := baseOptions
		fallbackOptions.Workers = 1
		fallbackOptions.SearchIdentity = music2bb.SearchIdentitySession
		fallbackOptions.SearchBudget = remaining
		fallback, fallbackErr := s.backend.Match(ctx, []music2bb.Song{songs[index]}, fallbackOptions, observer)
		s.addRemoteRequests(music2bb.SearchIdentitySession, fallback)
		if len(fallback) == 1 {
			fallback[0].RemoteRequests += outcomes[index].RemoteRequests
			fallback[0].CacheHits += outcomes[index].CacheHits
			outcomes[index] = fallback[0]
		}
		if reason := riskReasonOf(fallbackErr); reason != "" {
			s.blockWrites(reason)
			for pending := index + 1; pending < len(outcomes); pending++ {
				if outcomes[pending].SearchStatus == music2bb.SearchStatusNotSearched || outcomes[pending].SearchStatus == music2bb.SearchStatusRiskControl {
					outcomes[pending].SearchStatus = music2bb.SearchStatusNotSearched
					outcomes[pending].ReviewReason = music2bb.ReviewNotSearched
					outcomes[pending].RiskReason = ""
				}
			}
			return outcomes, fallbackErr
		}
		if fallbackErr != nil && firstErr == nil {
			firstErr = fallbackErr
		}
	}
	return outcomes, firstErr
}

func (s *conversionSession) search(ctx context.Context, song music2bb.Song, query string, observer music2bb.Observer) ([]music2bb.MatchResult, error) {
	if err := s.ensureSearchAllowed(); err != nil {
		return nil, err
	}
	if err := s.prepareSearchState(ctx); err != nil {
		return nil, err
	}
	identity := music2bb.SearchIdentityAnonymous
	if s.options.searchIdentity == string(music2bb.SearchIdentitySession) {
		if _, err := s.login(ctx, observer); err != nil {
			return nil, err
		}
		identity = music2bb.SearchIdentitySession
	}
	options := music2bb.CandidateSearchOptions{
		Limit: 10, Profile: music2bb.MatchProfile(s.options.matchProfile), SearchIdentity: identity,
		SearchCachePolicy: s.searchCachePolicy(),
	}
	candidates, err := s.backend.SearchCandidatesWithOptions(ctx, song, query, options)
	if riskReasonOf(err) == "" || s.options.searchIdentity != "auto" {
		if reason := riskReasonOf(err); reason != "" {
			s.blockWrites(reason)
		}
		return candidates, err
	}
	if _, loginErr := s.login(ctx, observer); loginErr != nil {
		s.blockWrites(riskReasonOf(err))
		return nil, loginErr
	}
	options.SearchIdentity = music2bb.SearchIdentitySession
	candidates, err = s.backend.SearchCandidatesWithOptions(ctx, song, query, options)
	if reason := riskReasonOf(err); reason != "" {
		s.blockWrites(reason)
	}
	return candidates, err
}

func (s *conversionSession) prepareSearchState(ctx context.Context) error {
	if !s.options.refreshSearch {
		return nil
	}
	s.refreshOnce.Do(func() {
		resetter, ok := s.backend.(interface{ ResetAnonymousIdentity(context.Context) error })
		if !ok {
			s.refreshErr = &music2bb.Error{Category: music2bb.ErrorInternal, Operation: "refresh search", Message: "backend does not support anonymous identity reset"}
			return
		}
		s.refreshErr = resetter.ResetAnonymousIdentity(ctx)
	})
	return s.refreshErr
}

func (s *conversionSession) searchCachePolicy() music2bb.SearchCachePolicy {
	if s.options.refreshSearch {
		return music2bb.SearchCacheRefresh
	}
	return music2bb.SearchCacheDefault
}

func (s *conversionSession) recordDecision(outcome music2bb.MatchResult, skipped bool) error {
	if err := s.state.saveDecision(outcome, skipped); err != nil {
		return persistentStateError("save manual decision", err)
	}
	return nil
}

func (s *conversionSession) clearDecision(song music2bb.Song) error {
	if err := s.state.removeDecision(song); err != nil {
		return persistentStateError("clear manual decision", err)
	}
	return nil
}

func persistentStateError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return &music2bb.Error{Category: music2bb.ErrorInternal, Operation: operation, Err: err}
}

func joinSessionErrors(primary, persistence error) error {
	if primary == nil {
		return persistence
	}
	if persistence == nil {
		return primary
	}
	return errors.Join(primary, persistence)
}

func (s *conversionSession) videoDetail(ctx context.Context, bvid string) (music2bb.Video, error) {
	return s.backend.VideoDetail(ctx, bvid)
}

func (s *conversionSession) favorites(ctx context.Context) ([]music2bb.Favorite, error) {
	return s.backend.ListFavorites(ctx)
}

func (s *conversionSession) prepareWrite(ctx context.Context, observer music2bb.Observer, outcomes []music2bb.MatchResult) (music2bb.Account, error) {
	if err := s.ensureWritesAllowed(); err != nil {
		return music2bb.Account{}, err
	}
	if err := ensureOutcomesResolved(outcomes); err != nil {
		return music2bb.Account{}, err
	}
	return s.login(ctx, observer)
}

func (s *conversionSession) createFavorite(ctx context.Context, request music2bb.CreateFavoriteRequest) (music2bb.Favorite, error) {
	if err := s.ensureWritesAllowed(); err != nil {
		return music2bb.Favorite{}, err
	}
	return s.backend.CreateFavorite(ctx, request)
}

func (s *conversionSession) write(ctx context.Context, favoriteID int64, outcomes []music2bb.MatchResult, observer music2bb.Observer) (music2bb.AddResult, error) {
	if err := s.ensureWritesAllowed(); err != nil {
		return music2bb.AddResult{}, err
	}
	if err := ensureOutcomesResolved(outcomes); err != nil {
		return music2bb.AddResult{}, err
	}
	already, err := s.state.successfulWrites(favoriteID)
	if err != nil {
		return music2bb.AddResult{}, persistentStateError("load write receipts", err)
	}
	result := music2bb.AddResult{FavoriteID: favoriteID}
	pending := make([]music2bb.MatchResult, 0, len(outcomes))
	seen := make(map[string]struct{}, len(outcomes))
	for _, outcome := range outcomes {
		if !outcome.HasSelection || outcome.Video == nil || strings.TrimSpace(outcome.Video.BVID) == "" {
			continue
		}
		bvid := outcome.Video.BVID
		if _, duplicate := seen[bvid]; duplicate {
			continue
		}
		seen[bvid] = struct{}{}
		if _, done := already[bvid]; done {
			result.Skipped = append(result.Skipped, bvid)
			continue
		}
		pending = append(pending, outcome)
	}
	if len(pending) == 0 {
		return result, nil
	}
	var persistenceMu sync.Mutex
	var persistenceErr error
	receiptSeen := make(map[string]struct{})
	tracking := music2bb.ObserverFunc(func(event music2bb.ProgressEvent) {
		if event.Kind == music2bb.EventVideo && event.Operation == "add_favorite" && event.Message != "" {
			item := event.WriteReceipt
			if item == nil {
				item = &music2bb.WriteReceipt{FavoriteID: favoriteID, BVID: event.Message, Succeeded: true}
			}
			persistenceMu.Lock()
			receiptSeen[writeReceiptKey(item.BVID, item.Succeeded)] = struct{}{}
			persistenceMu.Unlock()
			if saveErr := s.state.saveWriteReceipt(*item); saveErr != nil {
				persistenceMu.Lock()
				if persistenceErr == nil {
					persistenceErr = persistentStateError("save write receipt", saveErr)
				}
				persistenceMu.Unlock()
			}
		}
		if observer != nil {
			observer.Observe(event)
		}
	})
	written, writeErr := s.backend.AddToFavorite(ctx, favoriteID, pending, tracking)
	result.Succeeded = append(result.Succeeded, written.Succeeded...)
	result.Failed = append(result.Failed, written.Failed...)
	for _, bvid := range written.Succeeded {
		if _, ok := receiptSeen[writeReceiptKey(bvid, true)]; ok {
			continue
		}
		if saveErr := s.state.saveWriteReceipt(music2bb.WriteReceipt{FavoriteID: favoriteID, BVID: bvid, Succeeded: true}); saveErr != nil {
			persistenceMu.Lock()
			if persistenceErr == nil {
				persistenceErr = persistentStateError("save write receipt", saveErr)
			}
			persistenceMu.Unlock()
		}
	}
	for _, failure := range written.Failed {
		if _, ok := receiptSeen[writeReceiptKey(failure.BVID, false)]; ok {
			continue
		}
		if saveErr := s.state.saveWriteReceipt(music2bb.WriteReceipt{FavoriteID: favoriteID, BVID: failure.BVID, Reason: failure.Reason}); saveErr != nil {
			persistenceMu.Lock()
			if persistenceErr == nil {
				persistenceErr = persistentStateError("save write receipt", saveErr)
			}
			persistenceMu.Unlock()
		}
	}
	persistenceMu.Lock()
	defer persistenceMu.Unlock()
	return result, joinSessionErrors(writeErr, persistenceErr)
}

func writeReceiptKey(bvid string, succeeded bool) string {
	return fmt.Sprintf("%t\x00%s", succeeded, bvid)
}

func ensureOutcomesResolved(outcomes []music2bb.MatchResult) error {
	for _, outcome := range outcomes {
		selected := outcome.HasSelection && outcome.Video != nil
		skipped := !outcome.HasSelection && outcome.Video == nil && !outcome.NeedsReview &&
			outcome.SearchStatus == music2bb.SearchStatusCompleted && outcome.ReviewReason == music2bb.ReviewNone
		if !selected && !skipped {
			return &music2bb.Error{Category: music2bb.ErrorInvalidInput, Operation: "write", Message: "所有歌曲必须先选择或跳过"}
		}
	}
	return nil
}

func (s *conversionSession) addRemoteRequests(identity music2bb.SearchIdentity, outcomes []music2bb.MatchResult) {
	total, cacheHits := 0, 0
	for _, outcome := range outcomes {
		total += outcome.RemoteRequests
		cacheHits += outcome.CacheHits
	}
	s.telemetryMu.Lock()
	s.telemetry.cacheHits += cacheHits
	if identity == music2bb.SearchIdentitySession {
		s.telemetry.sessionRequests += total
	} else {
		s.telemetry.anonymousRequests += total
	}
	s.telemetryMu.Unlock()
}

func (s *conversionSession) emitVerboseSearchSummary(observer music2bb.Observer, outcomes []music2bb.MatchResult) {
	if !s.options.verbose || observer == nil {
		return
	}
	completed := 0
	for _, outcome := range outcomes {
		if outcome.SearchStatus == music2bb.SearchStatusCompleted {
			completed++
		}
	}
	s.telemetryMu.Lock()
	telemetry := s.telemetry
	s.telemetryMu.Unlock()
	requests := telemetry.anonymousRequests + telemetry.sessionRequests
	message := fmt.Sprintf("搜索汇总: 缓存命中 %d，匿名远程请求 %d，登录远程请求 %d，完成歌曲 %d/%d，预算消耗 %d/%d",
		telemetry.cacheHits, telemetry.anonymousRequests, telemetry.sessionRequests, completed, len(outcomes), requests, telemetry.budgetCapacity)
	s.loginMu.Lock()
	if s.writeBlocked {
		message += fmt.Sprintf("，停止原因 %s", s.haltReason)
	}
	s.loginMu.Unlock()
	observer.Observe(music2bb.ProgressEvent{Kind: music2bb.EventProgress, Operation: "search_summary", Message: message})
}

func normalizedSearchBudget(value int) int {
	if value < 1 {
		return 4
	}
	return value
}

func riskReasonOf(err error) music2bb.RiskControlReason {
	if err == nil {
		return ""
	}
	var batch *music2bb.BatchError
	if errors.As(err, &batch) && batch.HaltReason != "" {
		return batch.HaltReason
	}
	var operation *music2bb.Error
	if errors.As(err, &operation) && operation.RiskReason != "" {
		return operation.RiskReason
	}
	type riskControlled interface{ RiskControlReason() string }
	var risk riskControlled
	if errors.As(err, &risk) {
		return music2bb.RiskControlReason(risk.RiskControlReason())
	}
	return ""
}

func (s *conversionSession) blockWrites(reason music2bb.RiskControlReason) {
	s.loginMu.Lock()
	s.writeBlocked, s.haltReason = true, reason
	s.loginMu.Unlock()
}

func (s *conversionSession) ensureWritesAllowed() error {
	s.loginMu.Lock()
	blocked, reason := s.writeBlocked, s.haltReason
	s.loginMu.Unlock()
	if blocked {
		return &music2bb.Error{Category: music2bb.ErrorNetwork, Operation: "write", Message: fmt.Sprintf("搜索因风控停止（%s）；本次运行禁止写入收藏夹", reason)}
	}
	return nil
}

func (s *conversionSession) ensureSearchAllowed() error {
	s.loginMu.Lock()
	blocked, reason := s.writeBlocked, s.haltReason
	s.loginMu.Unlock()
	if blocked {
		return &music2bb.Error{
			Category:   music2bb.ErrorNetwork,
			Operation:  "search",
			Message:    fmt.Sprintf("搜索因风控停止（%s）；只能离线审核已获取的候选", reason),
			RiskReason: reason,
		}
	}
	return nil
}

func emitSessionWarning(observer music2bb.Observer, operation, message string) {
	if observer != nil {
		observer.Observe(music2bb.ProgressEvent{Kind: music2bb.EventWarning, Operation: operation, Message: message})
	}
}
