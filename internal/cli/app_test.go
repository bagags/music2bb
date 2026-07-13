package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/service"
)

type fakeBackend struct {
	loginOpts service.LoginOptions
	matchOpts service.MatchOptions
	created   service.CreateFavoriteRequest
	loginErr  error
}

func (f *fakeBackend) Login(_ context.Context, opts service.LoginOptions, _ service.Observer) (service.Account, error) {
	f.loginOpts = opts
	return service.Account{ID: 1, Name: "tester"}, f.loginErr
}

func (f *fakeBackend) ParsePlaylist(context.Context, string, service.ParseOptions, service.Observer) ([]model.Song, error) {
	return []model.Song{{Name: "song", Artist: "artist"}}, nil
}

func (f *fakeBackend) Match(_ context.Context, songs []model.Song, opts service.MatchOptions, _ service.Observer) ([]service.MatchOutcome, error) {
	f.matchOpts = opts
	video := model.Video{BVID: "BV1", Title: "song", Uploader: "artist"}
	return []service.MatchOutcome{{Song: songs[0], HasSelection: true, Selected: model.MatchResult{Song: songs[0], Video: &video, Matched: true}}}, nil
}

func (f *fakeBackend) SearchCandidates(context.Context, model.Song, string, int) ([]model.MatchResult, error) {
	return nil, nil
}

func (f *fakeBackend) VideoDetail(context.Context, string) (model.Video, error) {
	return model.Video{}, nil
}

func (f *fakeBackend) ListFavorites(context.Context) ([]model.Favorite, error) {
	return []model.Favorite{{ID: 9, Title: "target"}}, nil
}

func (f *fakeBackend) CreateFavorite(_ context.Context, request service.CreateFavoriteRequest) (model.Favorite, error) {
	f.created = request
	return model.Favorite{ID: 10, Title: request.Title}, nil
}

func (f *fakeBackend) AddToFavorite(context.Context, int64, []service.MatchOutcome, service.Observer) (service.AddResult, error) {
	return service.AddResult{FavoriteID: 9, Succeeded: []string{"BV1"}}, nil
}

type fakeBrowser struct{ status BrowserStatus }

func (f fakeBrowser) Status(context.Context) (BrowserStatus, error)        { return f.status, nil }
func (f fakeBrowser) Install(context.Context, bool) (BrowserStatus, error) { return f.status, nil }
func (fakeBrowser) Clear(context.Context) error                            { return nil }

func testApp(backend Backend) (*App, *bytes.Buffer, *bytes.Buffer) {
	out := &bytes.Buffer{}
	errOut := &bytes.Buffer{}
	return &App{
		Backend: backend,
		Browser: fakeBrowser{status: BrowserStatus{Installed: true, Revision: "1", Verified: true, Path: "/tmp/chrome"}},
		IO:      IO{In: strings.NewReader(""), Out: out, Err: errOut},
		Version: "v1.2.3",
	}, out, errOut
}

func TestCompatibilityAliasAndInterspersedOptions(t *testing.T) {
	backend := &fakeBackend{}
	app, out, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"cli", "https://example.test/list", "--search-pages", "2", "--top-k=5", "--workers", "3", "--favorite", "target", "--yes", "--no-qr-login"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%s", exit, errOut.String())
	}
	if backend.matchOpts != (service.MatchOptions{SearchPages: 2, TopK: 5, Workers: 3}) {
		t.Fatalf("match options = %#v", backend.matchOpts)
	}
	if backend.loginOpts.AllowQR {
		t.Fatal("--no-qr-login did not disable QR")
	}
	if !strings.Contains(out.String(), "成功: 1") {
		t.Fatalf("output = %q", out.String())
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
	backend := &fakeBackend{loginErr: &service.OperationError{Category: service.ErrorAuthentication, Operation: "login", Err: errors.New("expired")}}
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
