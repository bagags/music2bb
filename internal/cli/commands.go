package cli

import (
	"context"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/gguage/music-to-bb/internal/service"
)

func (a *App) runLogin(ctx context.Context, args []string) int {
	set := newFlagSet("login", a.IO.Err)
	allowQR := true
	noQR := false
	var configDir string
	set.BoolVar(&allowQR, "qr-login", true, "允许扫码登录")
	set.BoolVar(&noQR, "no-qr-login", false, "禁止扫码登录")
	set.StringVar(&configDir, "config-dir", "", "配置目录")
	if err := set.Parse(interspersed(args, map[string]bool{"--config-dir": true})); err != nil {
		if err == flag.ErrHelp {
			return ExitSuccess
		}
		return ExitInvalidInput
	}
	if set.NArg() != 0 {
		return ExitInvalidInput
	}
	if noQR {
		allowQR = false
	}
	account, err := a.Backend.Login(ctx, service.LoginOptions{UseStoredCookies: true, AllowQR: allowQR, Timeout: 3 * time.Minute}, a.observer(false))
	if err != nil {
		fmt.Fprintf(a.IO.Err, "登录失败: %v\n", err)
		return exitFor(err)
	}
	fmt.Fprintf(a.IO.Out, "登录成功: %s\n", account.Name)
	return ExitSuccess
}

func (a *App) runFavorites(ctx context.Context, args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(a.IO.Err, "用法: kg2bb favorites list|create")
		return ExitInvalidInput
	}
	if _, err := a.Backend.Login(ctx, service.LoginOptions{UseStoredCookies: true, AllowQR: a.IO.Interactive}, a.observer(false)); err != nil {
		fmt.Fprintf(a.IO.Err, "登录失败: %v\n", err)
		return exitFor(err)
	}
	switch args[0] {
	case "list":
		set := newFlagSet("favorites list", a.IO.Err)
		var configDir string
		set.StringVar(&configDir, "config-dir", "", "配置目录")
		if err := set.Parse(interspersed(args[1:], map[string]bool{"--config-dir": true})); err != nil {
			if err == flag.ErrHelp {
				return ExitSuccess
			}
			return ExitInvalidInput
		}
		favorites, err := a.Backend.ListFavorites(ctx)
		if err != nil {
			fmt.Fprintf(a.IO.Err, "获取收藏夹失败: %v\n", err)
			return exitFor(err)
		}
		for _, favorite := range favorites {
			fmt.Fprintf(a.IO.Out, "%d\t%s\t%d\n", favorite.ID, favorite.Title, favorite.MediaCount)
		}
		return ExitSuccess
	case "create":
		set := newFlagSet("favorites create", a.IO.Err)
		var intro, configDir string
		var private bool
		set.StringVar(&intro, "intro", "", "收藏夹简介")
		set.BoolVar(&private, "private", false, "仅自己可见")
		set.StringVar(&configDir, "config-dir", "", "配置目录")
		values := map[string]bool{"--intro": true, "--config-dir": true}
		if err := set.Parse(interspersed(args[1:], values)); err != nil {
			if err == flag.ErrHelp {
				return ExitSuccess
			}
			return ExitInvalidInput
		}
		if set.NArg() != 1 || strings.TrimSpace(set.Arg(0)) == "" {
			fmt.Fprintln(a.IO.Err, "用法: kg2bb favorites create <name> [--intro TEXT] [--private]")
			return ExitInvalidInput
		}
		favorite, err := a.Backend.CreateFavorite(ctx, service.CreateFavoriteRequest{Title: strings.TrimSpace(set.Arg(0)), Intro: intro, Private: private})
		if err != nil {
			fmt.Fprintf(a.IO.Err, "创建收藏夹失败: %v\n", err)
			return exitFor(err)
		}
		fmt.Fprintf(a.IO.Out, "%d\t%s\n", favorite.ID, favorite.Title)
		return ExitSuccess
	default:
		fmt.Fprintf(a.IO.Err, "未知 favorites 子命令: %s\n", args[0])
		return ExitInvalidInput
	}
}

func (a *App) runBrowser(ctx context.Context, args []string) int {
	filtered := make([]string, 0, len(args))
	for index := 0; index < len(args); index++ {
		if args[index] == "--config-dir" && index+1 < len(args) {
			index++
			continue
		}
		if strings.HasPrefix(args[index], "--config-dir=") {
			continue
		}
		filtered = append(filtered, args[index])
	}
	if len(filtered) != 1 || a.Browser == nil {
		fmt.Fprintln(a.IO.Err, "用法: kg2bb browser install|status|clear")
		return ExitInvalidInput
	}
	switch filtered[0] {
	case "status":
		status, err := a.Browser.Status(ctx)
		if err != nil {
			fmt.Fprintf(a.IO.Err, "浏览器状态读取失败: %v\n", err)
			return ExitInternal
		}
		if !status.Installed {
			fmt.Fprintln(a.IO.Out, "not installed")
			return ExitSuccess
		}
		fmt.Fprintf(a.IO.Out, "installed\trevision=%s\tverified=%t\tpath=%s\n", status.Revision, status.Verified, status.Path)
		return ExitSuccess
	case "install":
		allow := true // The explicit install command is non-interactive approval.
		fmt.Fprintln(a.IO.Out, "Chromium 下载约 150 MB，完成后会校验 SHA-256。")
		if a.IO.Interactive {
			answer, _ := a.ask("将下载经过校验的 Chromium（约 150 MB），继续? [y/N] ")
			allow = strings.EqualFold(answer, "y")
		}
		if !allow {
			fmt.Fprintln(a.IO.Err, "浏览器安装需要交互式确认")
			return ExitInvalidInput
		}
		status, err := a.Browser.Install(ctx, true)
		if err != nil {
			fmt.Fprintf(a.IO.Err, "浏览器安装失败: %v\n", err)
			return ExitExtraction
		}
		fmt.Fprintf(a.IO.Out, "installed\trevision=%s\tverified=%t\tpath=%s\n", status.Revision, status.Verified, status.Path)
		return ExitSuccess
	case "clear":
		if err := a.Browser.Clear(ctx); err != nil {
			fmt.Fprintf(a.IO.Err, "清理浏览器失败: %v\n", err)
			return ExitInternal
		}
		fmt.Fprintln(a.IO.Out, "cleared")
		return ExitSuccess
	default:
		return ExitInvalidInput
	}
}
