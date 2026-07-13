package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	music2bb "github.com/gguage/music-to-bb"
)

type fakeBackend struct {
	loginOpts music2bb.LoginOptions
	matchOpts music2bb.MatchOptions
	created   music2bb.CreateFavoriteRequest
	match     []music2bb.MatchResult
	addedTo   int64
	loginErr  error
}

func (f *fakeBackend) LoginWithOptions(_ context.Context, opts music2bb.LoginOptions, _ music2bb.Observer) (music2bb.Account, error) {
	f.loginOpts = opts
	return music2bb.Account{ID: 1, Name: "tester"}, f.loginErr
}

func (f *fakeBackend) ParsePlaylistWithOptions(context.Context, string, music2bb.ParseOptions, music2bb.Observer) ([]music2bb.Song, error) {
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

func (f *fakeBackend) SearchCandidates(context.Context, music2bb.Song, string, int) ([]music2bb.MatchResult, error) {
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
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--search-pages", "2", "--top-k=5", "--workers", "3", "--favorite", "target", "--yes", "--no-qr-login"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.matchOpts != (music2bb.MatchOptions{SearchPages: 2, TopK: 5, Workers: 3}) {
		t.Fatalf("match options = %#v", backend.matchOpts)
	}
	if backend.loginOpts.AllowQR {
		t.Fatal("--no-qr-login did not disable QR")
	}
	if !strings.Contains(out.String(), "成功: 1") {
		t.Fatalf("output = %q", out.String())
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

func TestFavoritesCreateAllowsFlagsAfterName(t *testing.T) {
	backend := &fakeBackend{}
	app, _, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"favorites", "create", "new folder", "--intro", "hello", "--private"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.created.Title != "new folder" || backend.created.Intro != "hello" || !backend.created.Private {
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
	app.IO.In = strings.NewReader("invalid\n0\nnew folder\ninline intro\ny\n")
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--favorite", "missing", "--yes"})
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
		Candidates: []music2bb.MatchResult{{Video: &video, Score: 42}},
	}}}
	app, out, errOut := testApp(backend)
	app.IO.Interactive = true
	app.IO.In = strings.NewReader("1\n")
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--favorite", "target", "--yes"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%q, stdout=%q", exit, errOut.String(), out.String())
	}
	if backend.addedTo != 9 || !strings.Contains(out.String(), "输入候选序号") {
		t.Fatalf("addedTo=%d output=%q", backend.addedTo, out.String())
	}
}
