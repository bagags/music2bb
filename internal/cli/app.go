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

	music2bb "github.com/bagags/music2bb-go"
	qrcode "github.com/skip2/go-qrcode"
)

type Backend interface {
	LoginWithOptions(context.Context, music2bb.LoginOptions, music2bb.Observer) (music2bb.Account, error)
	Logout(context.Context) error
	ParsePlaylistWithOptions(context.Context, string, music2bb.ParseOptions, music2bb.Observer) ([]music2bb.Song, error)
	Match(context.Context, []music2bb.Song, music2bb.MatchOptions, music2bb.Observer) ([]music2bb.MatchResult, error)
	SearchCandidatesWithOptions(context.Context, music2bb.Song, string, music2bb.CandidateSearchOptions) ([]music2bb.MatchResult, error)
	VideoDetail(context.Context, string) (music2bb.Video, error)
	ListFavorites(context.Context) ([]music2bb.Favorite, error)
	CreateFavorite(context.Context, music2bb.CreateFavoriteRequest) (music2bb.Favorite, error)
	AddToFavorite(context.Context, int64, []music2bb.MatchResult, music2bb.Observer) (music2bb.AddResult, error)
}

type BrowserManager interface {
	Status(context.Context) (music2bb.BrowserStatus, error)
	Install(context.Context, bool) (music2bb.BrowserStatus, error)
	Clear(context.Context) error
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
	switch command {
	case "convert":
		return a.runConvert(ctx, commandArgs)
	case "login":
		return a.runLogin(ctx, commandArgs)
	case "logout":
		return a.runLogout(ctx, commandArgs)
	case "favorites":
		return a.runFavorites(ctx, commandArgs)
	case "browser":
		return a.runBrowser(ctx, commandArgs)
	case "version":
		fmt.Fprintln(a.IO.Out, a.Version)
		return ExitSuccess
	case "license":
		a.printLicense()
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
	fmt.Fprintln(a.IO.Out, `在线歌单 → Bilibili 收藏夹

用法:
  music2bb convert <playlist-url> [options]
  music2bb login [--no-qr-login]
  music2bb logout
  music2bb favorites list
  music2bb favorites create <name> [--intro TEXT] [--public]
  music2bb browser install|status|clear
  music2bb version
  music2bb license`)
}

func (a *App) observer(verbose bool) music2bb.Observer {
	return music2bb.ObserverFunc(func(event music2bb.ProgressEvent) {
		switch event.Kind {
		case music2bb.EventQR:
			fmt.Fprintln(a.IO.Out, "请使用 Bilibili 客户端扫描二维码:")
			fmt.Fprint(a.IO.Out, renderQR(event.QRPayload))
		case music2bb.EventWarning:
			fmt.Fprintln(a.IO.Err, event.Message)
		case music2bb.EventSong:
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
	if errors.Is(err, io.EOF) && line == "" {
		return "", io.EOF
	}
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
