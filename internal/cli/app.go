package cli

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/service"
	qrcode "github.com/skip2/go-qrcode"
)

type Backend interface {
	Login(context.Context, service.LoginOptions, service.Observer) (service.Account, error)
	ParsePlaylist(context.Context, string, service.ParseOptions, service.Observer) ([]model.Song, error)
	Match(context.Context, []model.Song, service.MatchOptions, service.Observer) ([]service.MatchOutcome, error)
	SearchCandidates(context.Context, model.Song, string, int) ([]model.MatchResult, error)
	VideoDetail(context.Context, string) (model.Video, error)
	ListFavorites(context.Context) ([]model.Favorite, error)
	CreateFavorite(context.Context, service.CreateFavoriteRequest) (model.Favorite, error)
	AddToFavorite(context.Context, int64, []service.MatchOutcome, service.Observer) (service.AddResult, error)
}

type BrowserManager interface {
	Status(context.Context) (BrowserStatus, error)
	Install(context.Context, bool) (BrowserStatus, error)
	Clear(context.Context) error
}

type BrowserStatus struct {
	Installed bool
	Revision  string
	Path      string
	Verified  bool
}

type IO struct {
	In          io.Reader
	Out         io.Writer
	Err         io.Writer
	Interactive bool
}

type App struct {
	Backend Backend
	Browser BrowserManager
	IO      IO
	Version string
	reader  *bufio.Reader
}

func (a *App) Run(ctx context.Context, args []string) int {
	a.defaults()
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		a.printHelp()
		return ExitSuccess
	}
	command := args[0]
	commandArgs := args[1:]
	if command == "cli" {
		command = "convert"
	}
	switch command {
	case "convert":
		return a.runConvert(ctx, commandArgs)
	case "login":
		return a.runLogin(ctx, commandArgs)
	case "favorites":
		return a.runFavorites(ctx, commandArgs)
	case "browser":
		return a.runBrowser(ctx, commandArgs)
	case "version":
		fmt.Fprintln(a.IO.Out, a.Version)
		return ExitSuccess
	default:
		fmt.Fprintf(a.IO.Err, "未知命令: %s\n\n", command)
		a.printHelp()
		return ExitInvalidInput
	}
}

func (a *App) defaults() {
	if a.IO.In == nil {
		a.IO.In = strings.NewReader("")
	}
	if a.IO.Out == nil {
		a.IO.Out = io.Discard
	}
	if a.IO.Err == nil {
		a.IO.Err = io.Discard
	}
	if a.Version == "" {
		a.Version = "dev"
	}
	if a.reader == nil {
		a.reader = bufio.NewReader(a.IO.In)
	}
}

func (a *App) printHelp() {
	fmt.Fprintln(a.IO.Out, `酷狗歌单 → Bilibili 收藏夹

用法:
  kg2bb convert <kugou-url> [options]
  kg2bb cli <kugou-url> [options]
  kg2bb login [--no-qr-login]
  kg2bb favorites list
  kg2bb favorites create <name> [--intro TEXT] [--private]
  kg2bb browser install|status|clear
  kg2bb version`)
}

func (a *App) observer(verbose bool) service.Observer {
	return service.ObserverFunc(func(event service.ProgressEvent) {
		switch event.Kind {
		case service.EventQR:
			fmt.Fprintln(a.IO.Out, "请使用 Bilibili 客户端扫描二维码:")
			fmt.Fprint(a.IO.Out, renderQR(event.QRPayload))
		case service.EventWarning:
			fmt.Fprintln(a.IO.Err, event.Message)
		case service.EventSong:
			if event.Song != nil {
				if event.Match != nil && event.Match.Video != nil {
					fmt.Fprintf(a.IO.Out, "[%d/%d] ✓ %s → %s\n", event.Current, event.Total, event.Song.Name, event.Match.Video.Title)
				} else {
					fmt.Fprintf(a.IO.Out, "[%d/%d] ✗ %s\n", event.Current, event.Total, event.Song.Name)
				}
			}
		default:
			if verbose && event.Message != "" {
				fmt.Fprintln(a.IO.Err, event.Message)
			}
		}
	})
}

func renderQR(payload string) string {
	if payload == "" {
		return ""
	}
	code, err := qrcode.New(payload, qrcode.Medium)
	if err != nil {
		return payload + "\n"
	}
	bitmap := code.Bitmap()
	var output strings.Builder
	for row := 0; row < len(bitmap); row += 2 {
		for column := range bitmap[row] {
			top := bitmap[row][column]
			bottom := row+1 < len(bitmap) && bitmap[row+1][column]
			switch {
			case top && bottom:
				output.WriteRune('█')
			case top:
				output.WriteRune('▀')
			case bottom:
				output.WriteRune('▄')
			default:
				output.WriteRune(' ')
			}
		}
		output.WriteByte('\n')
	}
	return output.String()
}

func parseInt64(value string) (int64, bool) {
	number, err := strconv.ParseInt(value, 10, 64)
	return number, err == nil
}

func (a *App) ask(prompt string) (string, error) {
	fmt.Fprint(a.IO.Out, prompt)
	line, err := a.reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimSpace(line), nil
}

func newFlagSet(name string, output io.Writer) *flag.FlagSet {
	set := flag.NewFlagSet(name, flag.ContinueOnError)
	set.SetOutput(output)
	return set
}
