package cli

import (
	"context"
	"fmt"
	"os"
	"strings"

	music2bb "github.com/bagags/music2bb-go"
)

// conversionSession is the shared controller boundary used by both terminal
// frontends. It owns conversion policy while leaving presentation to the
// caller.
type conversionSession struct {
	backend Backend
	browser BrowserManager
	rawURL  string
	options convertOptions
	policy  music2bb.BrowserPolicy
}

func newConversionSession(backend Backend, browser BrowserManager, rawURL string, options convertOptions, policy music2bb.BrowserPolicy) *conversionSession {
	return &conversionSession{backend: backend, browser: browser, rawURL: rawURL, options: options, policy: policy}
}

func shouldUseTUI(io IO, disabled bool) bool {
	return io.Interactive && !disabled && !strings.EqualFold(os.Getenv("TERM"), "dumb")
}

func (s *conversionSession) login(ctx context.Context, observer music2bb.Observer) (music2bb.Account, error) {
	return s.backend.LoginWithOptions(ctx, music2bb.LoginOptions{UseStoredCookies: true, AllowQR: s.options.qrLogin}, observer)
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
	if s.options.manual {
		outcomes := make([]music2bb.MatchResult, len(songs))
		for index, song := range songs {
			outcomes[index] = music2bb.MatchResult{Song: song, NeedsReview: true, ReviewReason: music2bb.ReviewNoCandidates}
		}
		return outcomes, nil
	}
	return s.backend.Match(ctx, songs, music2bb.MatchOptions{
		SearchPages: s.options.searchPages,
		TopK:        s.options.topK,
		Workers:     s.options.workers,
	}, observer)
}

func (s *conversionSession) search(ctx context.Context, song music2bb.Song, query string) ([]music2bb.MatchResult, error) {
	return s.backend.SearchCandidates(ctx, song, query, 10)
}

func (s *conversionSession) videoDetail(ctx context.Context, bvid string) (music2bb.Video, error) {
	return s.backend.VideoDetail(ctx, bvid)
}

func (s *conversionSession) favorites(ctx context.Context) ([]music2bb.Favorite, error) {
	return s.backend.ListFavorites(ctx)
}

func (s *conversionSession) createFavorite(ctx context.Context, request music2bb.CreateFavoriteRequest) (music2bb.Favorite, error) {
	return s.backend.CreateFavorite(ctx, request)
}

func (s *conversionSession) write(ctx context.Context, favoriteID int64, outcomes []music2bb.MatchResult, observer music2bb.Observer) (music2bb.AddResult, error) {
	return s.backend.AddToFavorite(ctx, favoriteID, outcomes, observer)
}

func emitSessionWarning(observer music2bb.Observer, operation, message string) {
	if observer != nil {
		observer.Observe(music2bb.ProgressEvent{Kind: music2bb.EventWarning, Operation: operation, Message: message})
	}
}
