package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"sort"
	"strconv"
	"strings"

	music2bb "github.com/gguage/music-to-bb"
)

type convertOptions struct {
	searchPages  int
	topK         int
	workers      int
	favorite     string
	yes          bool
	browser      string
	configDir    string
	verbose      bool
	manual       bool
	manualReview bool
	qrLogin      bool
}

func (a *App) runConvert(ctx context.Context, args []string) int {
	set := newFlagSet("convert", a.IO.Err)
	options := convertOptions{searchPages: 3, topK: 3, workers: 4, browser: string(music2bb.BrowserAuto), qrLogin: true}
	set.IntVar(&options.searchPages, "search-pages", options.searchPages, "每首歌曲搜索页数")
	set.IntVar(&options.topK, "top-k", options.topK, "保留候选数量")
	set.IntVar(&options.workers, "workers", options.workers, "并发匹配数量")
	set.StringVar(&options.favorite, "favorite", "", "收藏夹 ID 或完整名称")
	set.BoolVar(&options.yes, "yes", false, "无需确认")
	set.StringVar(&options.browser, "browser", options.browser, "auto|never|always")
	set.StringVar(&options.configDir, "config-dir", "", "配置目录")
	set.BoolVar(&options.verbose, "verbose", false, "详细日志")
	set.BoolVar(&options.verbose, "v", false, "详细日志")
	set.BoolVar(&options.manualReview, "manual-review", false, "手动审核自动匹配")
	set.BoolVar(&options.manual, "manual", false, "完全手动匹配")
	set.BoolVar(&options.qrLogin, "qr-login", true, "允许扫码登录")
	noQR := false
	set.BoolVar(&noQR, "no-qr-login", false, "禁止扫码登录")
	valueFlags := map[string]bool{"--search-pages": true, "--top-k": true, "--workers": true, "--favorite": true, "--browser": true, "--config-dir": true}
	if err := set.Parse(interspersed(args, valueFlags)); err != nil {
		if err == flag.ErrHelp {
			return ExitSuccess
		}
		return ExitInvalidInput
	}
	if noQR {
		options.qrLogin = false
	}
	if set.NArg() != 1 || options.searchPages < 1 || options.topK < 1 || options.workers < 1 {
		fmt.Fprintln(a.IO.Err, "用法: music2bb convert <playlist-url> [options]")
		return ExitInvalidInput
	}
	policy := music2bb.BrowserPolicy(options.browser)
	if policy != music2bb.BrowserAuto && policy != music2bb.BrowserNever && policy != music2bb.BrowserAlways {
		fmt.Fprintln(a.IO.Err, "--browser 必须是 auto、never 或 always")
		return ExitInvalidInput
	}
	if a.Backend == nil {
		fmt.Fprintln(a.IO.Err, "后端未配置")
		return ExitInternal
	}

	observer := a.observer(options.verbose)
	account, err := a.Backend.LoginWithOptions(ctx, music2bb.LoginOptions{UseStoredCookies: true, AllowQR: options.qrLogin}, observer)
	if err != nil {
		fmt.Fprintf(a.IO.Err, "登录失败: %v\n", err)
		return exitFor(err)
	}
	if options.verbose {
		fmt.Fprintf(a.IO.Err, "已登录: %s\n", account.Name)
	}

	songs, err := a.Backend.ParsePlaylistWithOptions(ctx, set.Arg(0), music2bb.ParseOptions{BrowserPolicy: policy}, observer)
	if err != nil && policy != music2bb.BrowserNever && a.Browser != nil {
		status, statusErr := a.Browser.Status(ctx)
		if statusErr == nil && !status.Installed {
			approved := policy == music2bb.BrowserAlways
			if a.IO.Interactive && !approved {
				answer, _ := a.ask(fmt.Sprintf("直接解析失败。下载%s的校验版 Chromium 后重试? [y/N] ", browserDownloadSize(status)))
				approved = strings.EqualFold(answer, "y")
			}
			if approved {
				if _, installErr := a.Browser.Install(ctx, true); installErr == nil {
					songs, err = a.Backend.ParsePlaylistWithOptions(ctx, set.Arg(0), music2bb.ParseOptions{BrowserPolicy: music2bb.BrowserAlways}, observer)
				} else {
					fmt.Fprintf(a.IO.Err, "浏览器安装失败: %v\n", installErr)
				}
			}
		}
	}
	if err != nil && a.IO.Interactive {
		fmt.Fprintf(a.IO.Err, "自动解析失败: %v\n", err)
		songs = a.readManualSongs()
		err = nil
	}
	if err != nil {
		fmt.Fprintf(a.IO.Err, "解析失败: %v\n", err)
		return exitFor(err)
	}
	if len(songs) == 0 {
		fmt.Fprintln(a.IO.Err, "没有获取到歌曲")
		return ExitExtraction
	}
	fmt.Fprintf(a.IO.Out, "获取到 %d 首歌曲\n", len(songs))

	var outcomes []music2bb.MatchResult
	if options.manual {
		outcomes = a.manualMatchAll(ctx, songs)
	} else {
		outcomes, err = a.Backend.Match(ctx, songs, music2bb.MatchOptions{SearchPages: options.searchPages, TopK: options.topK, Workers: options.workers}, observer)
		if err != nil {
			fmt.Fprintf(a.IO.Err, "部分匹配请求失败: %v\n", err)
			if exitFor(err) == ExitCancelled {
				return ExitCancelled
			}
		}
		needsReview := hasRequiredReviews(outcomes)
		if options.manualReview || needsReview {
			if !a.IO.Interactive {
				if options.manualReview {
					fmt.Fprintln(a.IO.Err, "--manual-review 需要交互式终端")
					return ExitInvalidInput
				}
			} else {
				outcomes = a.reviewMatches(ctx, outcomes, options.manualReview)
			}
		}
	}

	matched := 0
	for _, outcome := range outcomes {
		if outcome.HasSelection {
			matched++
		}
	}
	if matched == 0 {
		fmt.Fprintln(a.IO.Err, "没有匹配到任何歌曲")
		return ExitNoMatches
	}
	fmt.Fprintf(a.IO.Out, "匹配成功: %d/%d\n", matched, len(outcomes))

	favorite, err := a.selectFavorite(ctx, options.favorite)
	if err != nil {
		if errors.Is(err, errFavoriteSelectionCancelled) {
			fmt.Fprintln(a.IO.Out, "已取消")
			return ExitSuccess
		}
		fmt.Fprintf(a.IO.Err, "选择收藏夹失败: %v\n", err)
		return exitFor(err)
	}
	if !options.yes {
		if !a.IO.Interactive {
			fmt.Fprintln(a.IO.Err, "非交互模式需要 --yes")
			return ExitInvalidInput
		}
		answer, askErr := a.ask(fmt.Sprintf("确认将 %d 个视频添加到「%s」? [y/N] ", matched, favorite.Title))
		if askErr != nil || !strings.EqualFold(answer, "y") {
			fmt.Fprintln(a.IO.Out, "已取消")
			return ExitSuccess
		}
	}
	result, err := a.Backend.AddToFavorite(ctx, favorite.ID, outcomes, observer)
	fmt.Fprintf(a.IO.Out, "成功: %d | 失败: %d\n", len(result.Succeeded), len(result.Failed))
	for _, failure := range result.Failed {
		fmt.Fprintf(a.IO.Err, "✗ %s: %s\n", failure.BVID, failure.Reason)
	}
	if err != nil {
		return exitFor(err)
	}
	return ExitSuccess
}

func (a *App) readManualSongs() []music2bb.Song {
	fmt.Fprintln(a.IO.Out, "请输入歌曲（每行格式：歌名 - 歌手，空行结束）:")
	songs := make([]music2bb.Song, 0)
	for {
		line, err := a.reader.ReadString('\n')
		if err != nil && line == "" {
			break
		}
		line = strings.TrimSpace(line)
		if line == "" {
			break
		}
		parts := strings.SplitN(line, " - ", 2)
		song := music2bb.Song{Name: strings.TrimSpace(parts[0])}
		if len(parts) == 2 {
			song.Artist = strings.TrimSpace(parts[1])
		}
		if song.Name != "" {
			songs = append(songs, song)
		}
	}
	return songs
}

func (a *App) manualMatchAll(ctx context.Context, songs []music2bb.Song) []music2bb.MatchResult {
	outcomes := make([]music2bb.MatchResult, len(songs))
	for index, song := range songs {
		outcomes[index] = a.manualMatch(ctx, song)
	}
	return outcomes
}

func (a *App) manualMatch(ctx context.Context, song music2bb.Song) music2bb.MatchResult {
	outcome := music2bb.MatchResult{Song: song}
	if !a.IO.Interactive {
		return outcome
	}
	query, _ := a.ask(fmt.Sprintf("手动匹配 %s - %s，搜索关键词 [%s]: ", song.Name, song.Artist, song.SearchKeywordFull()))
	if query == "" {
		query = song.SearchKeywordFull()
	}
	candidates, err := a.Backend.SearchCandidates(ctx, song, query, 10)
	if err != nil {
		fmt.Fprintf(a.IO.Err, "搜索失败: %v\n", err)
		return outcome
	}
	for index, candidate := range candidates {
		if candidate.Video != nil {
			fmt.Fprintf(a.IO.Out, "%d. %s - %s (%.1f)\n", index+1, candidate.Video.Title, candidate.Video.Uploader, candidate.Score)
		}
	}
	choice, _ := a.ask("选择序号、输入 BV 号，或 0 跳过: ")
	if strings.HasPrefix(choice, "BV") {
		video, detailErr := a.Backend.VideoDetail(ctx, choice)
		if detailErr == nil {
			outcome.Video = &video
			outcome.Score = 999
			outcome.Matched = true
			outcome.HasSelection = true
			outcome.ManualOverride = true
		}
		return outcome
	}
	selected, parseErr := strconv.Atoi(choice)
	if parseErr == nil && selected > 0 && selected <= len(candidates) {
		outcome = candidates[selected-1]
		outcome.Song = song
		outcome.Matched = true
		outcome.HasSelection = true
		outcome.ManualOverride = true
		outcome.Candidates = candidates
	}
	return outcome
}

func (a *App) reviewMatches(ctx context.Context, outcomes []music2bb.MatchResult, reviewAll bool) []music2bb.MatchResult {
	for index := range outcomes {
		if !reviewAll && !outcomes[index].NeedsReview {
			continue
		}
		for candidateIndex, candidate := range outcomes[index].Candidates {
			if candidate.Video != nil {
				fmt.Fprintf(a.IO.Out, "  %d. %s - %s (%.1f)\n", candidateIndex+1, candidate.Video.Title, candidate.Video.Uploader, candidate.Score)
			}
		}
		prompt := fmt.Sprintf("[%d/%d] %s，输入候选序号，或手动搜索? [y/N] ", index+1, len(outcomes), outcomes[index].Song.Name)
		if !outcomes[index].HasSelection {
			prompt = fmt.Sprintf("[%d/%d] %s 未匹配，输入候选序号，或手动搜索? [Y/n] ", index+1, len(outcomes), outcomes[index].Song.Name)
		}
		answer, _ := a.ask(prompt)
		if selected, err := strconv.Atoi(answer); err == nil && selected > 0 && selected <= len(outcomes[index].Candidates) {
			candidate := outcomes[index].Candidates[selected-1]
			candidate.Song = outcomes[index].Song
			candidate.HasSelection = candidate.Video != nil
			candidate.Matched = candidate.HasSelection
			candidate.ManualOverride = candidate.HasSelection
			candidate.NeedsReview = false
			candidate.Candidates = outcomes[index].Candidates
			outcomes[index] = candidate
			continue
		}
		accept := strings.EqualFold(answer, "y") || (!outcomes[index].HasSelection && answer == "")
		if accept {
			manual := a.manualMatch(ctx, outcomes[index].Song)
			if manual.HasSelection {
				manual.NeedsReview = false
				outcomes[index] = manual
			}
		}
	}
	return outcomes
}

func hasRequiredReviews(outcomes []music2bb.MatchResult) bool {
	for _, outcome := range outcomes {
		if outcome.NeedsReview {
			return true
		}
	}
	return false
}

var errFavoriteSelectionCancelled = errors.New("favorite selection cancelled")

func (a *App) selectFavorite(ctx context.Context, selector string) (music2bb.Favorite, error) {
	for {
		favorites, err := a.Backend.ListFavorites(ctx)
		if err != nil {
			if !a.IO.Interactive {
				return music2bb.Favorite{}, err
			}
			fmt.Fprintf(a.IO.Err, "获取收藏夹失败: %v\n", err)
			answer, askErr := a.ask("重试获取收藏夹? [Y/n] ")
			if askErr != nil || strings.EqualFold(answer, "n") {
				return music2bb.Favorite{}, err
			}
			continue
		}
		if selector != "" {
			if id, ok := parseInt64(selector); ok {
				for _, favorite := range favorites {
					if favorite.ID == id {
						return favorite, nil
					}
				}
			} else {
				for _, favorite := range favorites {
					if favorite.Title == selector {
						return favorite, nil
					}
				}
			}
			if !a.IO.Interactive {
				return music2bb.Favorite{}, &music2bb.Error{Category: music2bb.ErrorInvalidInput, Operation: "select favorite", Message: "未找到指定收藏夹"}
			}
			fmt.Fprintf(a.IO.Err, "未找到收藏夹「%s」，请重新选择\n", selector)
			selector = ""
		}
		if !a.IO.Interactive {
			return music2bb.Favorite{}, &music2bb.Error{Category: music2bb.ErrorInvalidInput, Operation: "select favorite", Message: "非交互模式需要 --favorite"}
		}
		sort.Slice(favorites, func(i, j int) bool { return favorites[i].ID < favorites[j].ID })
		fmt.Fprintln(a.IO.Out, "0. 新建收藏夹")
		for index, favorite := range favorites {
			fmt.Fprintf(a.IO.Out, "%d. %s (%d)\n", index+1, favorite.Title, favorite.MediaCount)
		}
		choice, askErr := a.ask("选择收藏夹序号（q 取消）: ")
		if askErr != nil || strings.EqualFold(choice, "q") {
			return music2bb.Favorite{}, errFavoriteSelectionCancelled
		}
		selected, parseErr := strconv.Atoi(choice)
		if parseErr != nil || selected < 0 || selected > len(favorites) {
			fmt.Fprintln(a.IO.Err, "无效收藏夹序号，请重新选择")
			continue
		}
		if selected == 0 {
			favorite, created, createErr := a.createFavoriteInline(ctx)
			if createErr != nil {
				fmt.Fprintf(a.IO.Err, "创建收藏夹失败: %v\n", createErr)
				continue
			}
			if created {
				return favorite, nil
			}
			continue
		}
		return favorites[selected-1], nil
	}
}

func (a *App) createFavoriteInline(ctx context.Context) (music2bb.Favorite, bool, error) {
	title, err := a.ask("收藏夹名称（留空返回）: ")
	if err != nil || title == "" {
		return music2bb.Favorite{}, false, nil
	}
	intro, err := a.ask("简介（可留空）: ")
	if err != nil {
		return music2bb.Favorite{}, false, err
	}
	publicAnswer, err := a.ask("设为公开可见? [y/N] ")
	if err != nil {
		return music2bb.Favorite{}, false, err
	}
	favorite, err := a.Backend.CreateFavorite(ctx, music2bb.CreateFavoriteRequest{
		Title: strings.TrimSpace(title), Intro: intro, Private: !strings.EqualFold(publicAnswer, "y"),
	})
	if err != nil {
		return music2bb.Favorite{}, false, err
	}
	fmt.Fprintf(a.IO.Out, "已创建收藏夹「%s」\n", favorite.Title)
	return favorite, true, nil
}
