package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/gguage/music-to-bb/internal/browser"
	"github.com/gguage/music-to-bb/internal/cli"
	"github.com/gguage/music-to-bb/internal/config"
	"github.com/gguage/music-to-bb/internal/wiring"
	"golang.org/x/term"
)

var (
	version = "dev"
	commit  = "unknown"
	date    = "unknown"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	components, err := wiring.New(wiring.Options{State: config.Options{Dir: cli.ExtractConfigDir(args)}})
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		return cli.ExitInternal
	}
	defer components.Close()

	application := &cli.App{
		Backend: components.Engine,
		Browser: browserCLIAdapter{manager: components.Browser},
		IO: cli.IO{
			In:          os.Stdin,
			Out:         os.Stdout,
			Err:         os.Stderr,
			Interactive: term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())),
		},
		Version: versionString(),
	}
	return application.Run(ctx, args)
}

func versionString() string {
	if version == "dev" {
		return "kg2bb dev"
	}
	return fmt.Sprintf("kg2bb %s (commit %s, built %s)", version, commit, date)
}

type browserCLIAdapter struct {
	manager *browser.Manager
}

func (a browserCLIAdapter) Status(ctx context.Context) (cli.BrowserStatus, error) {
	status, err := a.manager.Status(ctx)
	return convertBrowserStatus(status), err
}

func (a browserCLIAdapter) Install(ctx context.Context, approved bool) (cli.BrowserStatus, error) {
	status, err := a.manager.Install(ctx, browser.InstallOptions{Approved: approved, NonInteractive: !approved})
	return convertBrowserStatus(status), err
}

func (a browserCLIAdapter) Clear(ctx context.Context) error {
	return a.manager.Clear(ctx)
}

func convertBrowserStatus(status browser.Status) cli.BrowserStatus {
	return cli.BrowserStatus{
		Installed: status.Installed,
		Revision:  fmt.Sprint(status.Revision),
		Path:      status.ExecutablePath,
		Verified:  status.Verified,
	}
}
