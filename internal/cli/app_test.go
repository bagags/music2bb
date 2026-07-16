package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	music2bb "github.com/bagags/music2bb-go"
)

type fakeBackend struct {
	loginOpts   music2bb.LoginOptions
	matchOpts   music2bb.MatchOptions
	searchOpts  music2bb.CandidateSearchOptions
	parseOpts   []music2bb.ParseOptions
	parse       func(context.Context, string, music2bb.ParseOptions, music2bb.Observer) ([]music2bb.Song, error)
	created     music2bb.CreateFavoriteRequest
	match       []music2bb.MatchResult
	addedTo     int64
	loginErr    error
	loginCalls  int
	logoutErr   error
	logoutCalls int
}

func (f *fakeBackend) LoginWithOptions(_ context.Context, opts music2bb.LoginOptions, _ music2bb.Observer) (music2bb.Account, error) {
	f.loginCalls++
	f.loginOpts = opts
	return music2bb.Account{ID: 1, Name: "tester"}, f.loginErr
}

func (f *fakeBackend) Logout(context.Context) error {
	f.logoutCalls++
	return f.logoutErr
}

func (f *fakeBackend) ParsePlaylistWithOptions(ctx context.Context, rawURL string, options music2bb.ParseOptions, observer music2bb.Observer) ([]music2bb.Song, error) {
	f.parseOpts = append(f.parseOpts, options)
	if f.parse != nil {
		return f.parse(ctx, rawURL, options, observer)
	}
	return []music2bb.Song{{Name: "song", Artist: "artist"}}, nil
}

func (f *fakeBackend) Match(_ context.Context, songs []music2bb.Song, opts music2bb.MatchOptions, _ music2bb.Observer) ([]music2bb.MatchResult, error) {
	f.matchOpts = opts
	if f.match != nil {
		return append([]music2bb.MatchResult(nil), f.match...), nil
	}
	video := music2bb.Video{BVID: "BV1", Title: "song", Uploader: "artist"}
	return []music2bb.MatchResult{{Song: songs[0], HasSelection: true, Video: &video, Matched: true}}, nil
}

func (f *fakeBackend) SearchCandidatesWithOptions(_ context.Context, _ music2bb.Song, _ string, options music2bb.CandidateSearchOptions) ([]music2bb.MatchResult, error) {
	f.searchOpts = options
	return nil, nil
}

func (f *fakeBackend) VideoDetail(context.Context, string) (music2bb.Video, error) {
	return music2bb.Video{}, nil
}

func (f *fakeBackend) ListFavorites(context.Context) ([]music2bb.Favorite, error) {
	return []music2bb.Favorite{{ID: 9, Title: "target"}}, nil
}

func (f *fakeBackend) CreateFavorite(_ context.Context, request music2bb.CreateFavoriteRequest) (music2bb.Favorite, error) {
	f.created = request
	return music2bb.Favorite{ID: 10, Title: request.Title}, nil
}

func (f *fakeBackend) AddToFavorite(_ context.Context, favoriteID int64, _ []music2bb.MatchResult, _ music2bb.Observer) (music2bb.AddResult, error) {
	f.addedTo = favoriteID
	return music2bb.AddResult{FavoriteID: favoriteID, Succeeded: []string{"BV1"}}, nil
}

type fakeBrowser struct{ status music2bb.BrowserStatus }

func (f fakeBrowser) Status(context.Context) (music2bb.BrowserStatus, error) { return f.status, nil }
func (f fakeBrowser) Install(context.Context, bool) (music2bb.BrowserStatus, error) {
	return f.status, nil
}
func (fakeBrowser) Clear(context.Context) error { return nil }

type recordingBrowser struct {
	status       music2bb.BrowserStatus
	statusCalls  int
	installCalls int
}

func (b *recordingBrowser) Status(context.Context) (music2bb.BrowserStatus, error) {
	b.statusCalls++
	return b.status, nil
}

func (b *recordingBrowser) Install(context.Context, bool) (music2bb.BrowserStatus, error) {
	b.installCalls++
	b.status.Installed = true
	b.status.Verified = true
	return b.status, nil
}

func (*recordingBrowser) Clear(context.Context) error { return nil }

func testApp(backend Backend) (*App, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &App{
		Backend: backend,
		Browser: fakeBrowser{status: music2bb.BrowserStatus{Installed: true, Revision: 1, ApproxBytes: 267_483_258, Verified: true, ExecutablePath: "/tmp/chrome"}},
		IO:      IO{In: strings.NewReader(""), Out: out, Err: errOut},
		Version: "v1.2.3",
	}, out, errOut
}

func TestConvertInterspersedOptions(t *testing.T) {
	backend := &fakeBackend{}
	app, out, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--search-pages", "2", "--top-k=5", "--workers", "3", "--match-profile", "classical", "--favorite", "target", "--yes", "--no-qr-login"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.matchOpts != (music2bb.MatchOptions{SearchPages: 2, TopK: 5, Workers: 3, Profile: music2bb.MatchProfileClassical}) {
		t.Fatalf("match options = %#v", backend.matchOpts)
	}
	if backend.loginOpts.AllowQR {
		t.Fatal("--no-qr-login did not disable QR")
	}
	if !strings.Contains(out.String(), "成功: 1") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestConvertRejectsMatchProfileBeforeBackendWork(t *testing.T) {
	backend := &fakeBackend{}
	app, _, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--match-profile", "romantic"})
	if exit != ExitInvalidInput || backend.loginCalls != 0 {
		t.Fatalf("exit=%d loginCalls=%d stderr=%q", exit, backend.loginCalls, errOut.String())
	}
	if !strings.Contains(errOut.String(), "standard 或 classical") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestConversionSessionPropagatesProfileToManualSearch(t *testing.T) {
	backend := &fakeBackend{}
	session := newConversionSession(backend, nil, "https://example.test/list", convertOptions{matchProfile: string(music2bb.MatchProfileClassical)}, music2bb.BrowserAuto)
	if _, err := session.search(context.Background(), music2bb.Song{Name: "song"}, "query"); err != nil {
		t.Fatal(err)
	}
	if backend.searchOpts != (music2bb.CandidateSearchOptions{Limit: 10, Profile: music2bb.MatchProfileClassical}) {
		t.Fatalf("search options = %#v", backend.searchOpts)
	}
}

func TestConvertAutoInstallsBrowserWithoutPromptAndRetriesAlways(t *testing.T) {
	backend := &fakeBackend{}
	backend.parse = func(_ context.Context, _ string, options music2bb.ParseOptions, _ music2bb.Observer) ([]music2bb.Song, error) {
		if options.BrowserPolicy == music2bb.BrowserAlways {
			return []music2bb.Song{{Name: "song", Artist: "artist"}}, nil
		}
		return nil, &music2bb.Error{Category: music2bb.ErrorExtraction, Operation: "parse playlist", Err: errors.New("direct failed")}
	}
	browser := &recordingBrowser{status: music2bb.BrowserStatus{ApproxBytes: 267_483_258}}
	app, out, errOut := testApp(backend)
	app.Browser = browser
	app.IO.Interactive = true
	app.IO.In = strings.NewReader("n\n")
	if exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--no-tui", "--favorite", "9", "--yes"}); exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exit, errOut.String())
	}
	if browser.statusCalls != 1 || browser.installCalls != 1 {
		t.Fatalf("browser calls = status %d install %d", browser.statusCalls, browser.installCalls)
	}
	want := []music2bb.ParseOptions{{BrowserPolicy: music2bb.BrowserAuto}, {BrowserPolicy: music2bb.BrowserAlways}}
	if len(backend.parseOpts) != len(want) || backend.parseOpts[0] != want[0] || backend.parseOpts[1] != want[1] {
		t.Fatalf("parse options = %#v, want %#v", backend.parseOpts, want)
	}
	if strings.Contains(out.String(), "[y/N]") || !strings.Contains(errOut.String(), "正在自动下载并安装校验版") {
		t.Fatalf("stdout = %q, stderr = %q", out.String(), errOut.String())
	}
}

func TestConvertNeverDoesNotInspectOrInstallBrowser(t *testing.T) {
	backend := &fakeBackend{parse: func(context.Context, string, music2bb.ParseOptions, music2bb.Observer) ([]music2bb.Song, error) {
		return nil, &music2bb.Error{Category: music2bb.ErrorExtraction, Operation: "parse playlist", Err: errors.New("failed")}
	}}
	browser := &recordingBrowser{}
	app, _, _ := testApp(backend)
	app.Browser = browser
	if exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--browser", "never", "--favorite", "9", "--yes"}); exit != ExitExtraction {
		t.Fatalf("exit = %d, want %d", exit, ExitExtraction)
	}
	if browser.statusCalls != 0 || browser.installCalls != 0 {
		t.Fatalf("browser calls = status %d install %d", browser.statusCalls, browser.installCalls)
	}
}

func TestConvertIncompleteHTTPResultAlsoInstallsAndRetries(t *testing.T) {
	backend := &fakeBackend{}
	backend.parse = func(_ context.Context, _ string, options music2bb.ParseOptions, observer music2bb.Observer) ([]music2bb.Song, error) {
		if options.BrowserPolicy == music2bb.BrowserAlways {
			return []music2bb.Song{{Name: "one"}, {Name: "two"}}, nil
		}
		observer.Observe(music2bb.ProgressEvent{
			Kind: music2bb.EventWarning, Operation: "parse_playlist", Current: 1, Total: 2,
			Message: "HTTP result is incomplete",
		})
		return []music2bb.Song{{Name: "one"}}, nil
	}
	browser := &recordingBrowser{status: music2bb.BrowserStatus{ApproxBytes: 267_483_258}}
	app, _, errOut := testApp(backend)
	app.Browser = browser
	if exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--favorite", "9", "--yes"}); exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exit, errOut.String())
	}
	if browser.installCalls != 1 || len(backend.parseOpts) != 2 || backend.parseOpts[1].BrowserPolicy != music2bb.BrowserAlways {
		t.Fatalf("install calls = %d, parse options = %#v", browser.installCalls, backend.parseOpts)
	}
}

func TestHelpUsesGenericPlaylistURL(t *testing.T) {
	app, out, _ := testApp(&fakeBackend{})
	if exit := app.Run(context.Background(), nil); exit != ExitSuccess {
		t.Fatalf("exit = %d, want %d", exit, ExitSuccess)
	}
	if !strings.Contains(out.String(), "在线歌单 → Bilibili 收藏夹") ||
		!strings.Contains(out.String(), "music2bb convert <playlist-url> [options]") {
		t.Fatalf("help = %q", out.String())
	}
	if strings.Contains(out.String(), "<kugou-url>") {
		t.Fatalf("help retains provider-specific usage: %q", out.String())
	}
	if !strings.Contains(out.String(), "music2bb license") {
		t.Fatalf("help omits license command: %q", out.String())
	}
}

func TestConvertUsageUsesGenericPlaylistURL(t *testing.T) {
	app, _, errOut := testApp(&fakeBackend{})
	if exit := app.Run(context.Background(), []string{"convert"}); exit != ExitInvalidInput {
		t.Fatalf("exit = %d, want %d", exit, ExitInvalidInput)
	}
	if errOut.String() != "用法: music2bb convert <playlist-url> [options]\n" {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestCLISubcommandIsUnknown(t *testing.T) {
	app, _, errOut := testApp(&fakeBackend{})
	exit := app.Run(context.Background(), []string{"cli", "https://example.test/list"})
	if exit != ExitInvalidInput {
		t.Fatalf("exit = %d, want %d", exit, ExitInvalidInput)
	}
	if !strings.Contains(errOut.String(), "未知命令: cli") {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestExplicitQRLoginAlias(t *testing.T) {
	backend := &fakeBackend{}
	app, _, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"convert", "--qr-login", "https://example.test/list", "--favorite", "9", "--yes"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if !backend.loginOpts.AllowQR {
		t.Fatal("--qr-login was not accepted")
	}
}

func TestLogoutClearsLocalCookies(t *testing.T) {
	backend := &fakeBackend{}
	app, out, errOut := testApp(backend)
	if exit := app.Run(context.Background(), []string{"logout"}); exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.logoutCalls != 1 {
		t.Fatalf("logout calls = %d, want 1", backend.logoutCalls)
	}
	if !strings.Contains(out.String(), "本地 Cookie 已清除") {
		t.Fatalf("stdout = %q", out.String())
	}
}

func TestLogoutRejectsArguments(t *testing.T) {
	backend := &fakeBackend{}
	app, _, errOut := testApp(backend)
	if exit := app.Run(context.Background(), []string{"logout", "extra"}); exit != ExitInvalidInput {
		t.Fatalf("exit = %d, want %d", exit, ExitInvalidInput)
	}
	if backend.logoutCalls != 0 || !strings.Contains(errOut.String(), "用法: music2bb logout") {
		t.Fatalf("logout calls = %d, stderr = %q", backend.logoutCalls, errOut.String())
	}
}

func TestFavoritesCreateDefaultsToPrivate(t *testing.T) {
	backend := &fakeBackend{}
	app, _, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"favorites", "create", "new folder", "--intro", "hello"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.created.Title != "new folder" || backend.created.Intro != "hello" || !backend.created.Private {
		t.Fatalf("request = %#v", backend.created)
	}
}

func TestFavoritesCreatePublicFlagAllowsFlagsAfterName(t *testing.T) {
	backend := &fakeBackend{}
	app, _, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"favorites", "create", "new folder", "--intro", "hello", "--public"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.created.Title != "new folder" || backend.created.Intro != "hello" || backend.created.Private {
		t.Fatalf("request = %#v", backend.created)
	}
}

func TestStableExitCategories(t *testing.T) {
	backend := &fakeBackend{loginErr: &music2bb.Error{Category: music2bb.ErrorAuthentication, Operation: "login", Err: errors.New("expired")}}
	app, _, _ := testApp(backend)
	if exit := app.Run(context.Background(), []string{"convert", "url", "--favorite", "9", "--yes"}); exit != ExitAuthentication {
		t.Fatalf("exit = %d, want %d", exit, ExitAuthentication)
	}
	if exit := app.Run(context.Background(), []string{"convert", "url", "--workers", "0"}); exit != ExitInvalidInput {
		t.Fatalf("invalid exit = %d, want %d", exit, ExitInvalidInput)
	}
	for _, category := range []music2bb.ErrorCategory{music2bb.ErrorExtraction, music2bb.ErrorBrowser, music2bb.ErrorNetwork} {
		if exit := exitFor(&music2bb.Error{Category: category, Err: errors.New("failed")}); exit != ExitExtraction {
			t.Fatalf("category %q exit = %d, want %d", category, exit, ExitExtraction)
		}
	}
}

func TestVersionAndBrowserStatus(t *testing.T) {
	app, out, _ := testApp(&fakeBackend{})
	if exit := app.Run(context.Background(), []string{"version"}); exit != 0 || out.String() != "v1.2.3\n" {
		t.Fatalf("version exit=%d output=%q", exit, out.String())
	}
	out.Reset()
	if exit := app.Run(context.Background(), []string{"browser", "status"}); exit != 0 || !strings.Contains(out.String(), "verified=true") {
		t.Fatalf("browser exit=%d output=%q", exit, out.String())
	}
}

func TestLicenseIncludesRequiredNotices(t *testing.T) {
	app, out, errOut := testApp(&fakeBackend{})
	if exit := app.Run(context.Background(), []string{"license"}); exit != ExitSuccess {
		t.Fatalf("exit = %d, want %d", exit, ExitSuccess)
	}
	for _, required := range []string{
		"Copyright (C) 2026 bagags and music2bb contributors.",
		"This project is an independent continuation of earlier work by Chaoyi Liu,",
		"GNU General Public License",
		"GPL-3.0-only",
		"WITHOUT ANY",
		"WARRANTY;",
		"https://github.com/bagags/music2bb-go/blob/main/LICENSE.md",
		"Source: https://github.com/bagags/music2bb-go",
		"CHROMIUM_CREDITS.html",
		"CHROMIUM_PROVENANCE.json",
		"https://chromium.googlesource.com/chromium/src/",
	} {
		if !strings.Contains(out.String(), required) {
			t.Errorf("output omits %q: %q", required, out.String())
		}
	}
	if errOut.Len() != 0 {
		t.Fatalf("stderr = %q", errOut.String())
	}
}

func TestBrowserStatusReportsBundledBeforeFirstExtraction(t *testing.T) {
	app, out, _ := testApp(&fakeBackend{})
	app.Browser = fakeBrowser{status: music2bb.BrowserStatus{
		Bundled: true, Version: "152.0.7951.0", Revision: 1661829,
		ChromiumCommit: "544d3cf2b1f8195b3133e90511d95e8c0325569b",
	}}
	want := "bundled\tversion=152.0.7951.0\trevision=1661829\tcommit=544d3cf2b1f8195b3133e90511d95e8c0325569b\tinstalled=false\n"
	if exit := app.Run(context.Background(), []string{"browser", "status"}); exit != ExitSuccess || out.String() != want {
		t.Fatalf("exit = %d, output = %q", exit, out.String())
	}
}

func TestBrowserInstallReportsPlatformArchiveSize(t *testing.T) {
	app, out, errOut := testApp(&fakeBackend{})
	if exit := app.Run(context.Background(), []string{"browser", "install"}); exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exit, errOut.String())
	}
	if !strings.Contains(out.String(), "约 268 MB") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestConvertRetriesInvalidFavoriteAndCreatesInline(t *testing.T) {
	backend := &fakeBackend{}
	app, out, errOut := testApp(backend)
	app.IO.Interactive = true
	app.IO.In = strings.NewReader("invalid\n0\nnew folder\ninline intro\n\n")
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--no-tui", "--favorite", "missing", "--yes"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%q, stdout=%q", exit, errOut.String(), out.String())
	}
	if backend.addedTo != 10 {
		t.Fatalf("added favorite ID = %d, want 10", backend.addedTo)
	}
	if backend.created != (music2bb.CreateFavoriteRequest{Title: "new folder", Intro: "inline intro", Private: true}) {
		t.Fatalf("create request = %#v", backend.created)
	}
	if !strings.Contains(errOut.String(), "请重新选择") || !strings.Contains(out.String(), "0. 新建收藏夹") {
		t.Fatalf("stderr=%q stdout=%q", errOut.String(), out.String())
	}
}

func TestConvertAutomaticallyReviewsUnsafeMatch(t *testing.T) {
	video := music2bb.Video{BVID: "BV-review", Title: "same name", Uploader: "someone"}
	backend := &fakeBackend{match: []music2bb.MatchResult{{
		Song: music2bb.Song{Name: "same name"}, NeedsReview: true,
		Candidates: []music2bb.MatchResult{{Video: &video, Score: 42, TitleScore: 88, ArtistScore: 77, KeywordScore: 88}},
	}}}
	app, out, errOut := testApp(backend)
	app.IO.Interactive = true
	app.IO.In = strings.NewReader("1\n")
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--no-tui", "--favorite", "target", "--yes"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%q, stdout=%q", exit, errOut.String(), out.String())
	}
	if backend.addedTo != 9 || !strings.Contains(out.String(), "输入候选序号") || !strings.Contains(out.String(), "歌手 77.0") {
		t.Fatalf("addedTo=%d output=%q", backend.addedTo, out.String())
	}
}
