package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	music2bb "github.com/gguage/music-to-bb"
	"github.com/gguage/music-to-bb/internal/cli"
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

	engine, err := music2bb.New(music2bb.Config{ConfigDir: cli.ExtractConfigDir(args)})
	if err != nil {
		fmt.Fprintf(os.Stderr, "初始化失败: %v\n", err)
		return cli.ExitInternal
	}
	defer engine.Close()

	application := &cli.App{
		Backend: engine,
		Browser: engine.Browser(),
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
		return "music2bb dev"
	}
	return fmt.Sprintf("music2bb %s (commit %s, built %s)", version, commit, date)
}
