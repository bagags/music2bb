package cli

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
	matchFunc   func([]music2bb.Song, music2bb.MatchOptions) ([]music2bb.MatchResult, error)
	matchCalls  int
	addedTo     int64
	addedBVIDs  [][]string
	addCalls    int
	addFunc     func(context.Context, int64, []music2bb.MatchResult, music2bb.Observer) (music2bb.AddResult, error)
	loginErr    error
	loginCalls  int
	loginEvent  *music2bb.ProgressEvent
	loginSeen   bool
	logoutErr   error
	logoutCalls int
	searchFunc  func(music2bb.CandidateSearchOptions) ([]music2bb.MatchResult, error)
	searchCalls int
}

type persistentFakeBackend struct {
	*fakeBackend
	configDir string
	cacheDir  string
	resets    int
}

func (f *persistentFakeBackend) PersistentStatePaths() (string, string) {
	return f.configDir, f.cacheDir
}

func (f *persistentFakeBackend) ResetAnonymousIdentity(context.Context) error {
	f.resets++
	err := os.Remove(filepath.Join(f.configDir, "cookies", "bilibili-anonymous.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (f *fakeBackend) LoginWithOptions(_ context.Context, opts music2bb.LoginOptions, observer music2bb.Observer) (music2bb.Account, error) {
	f.loginCalls++
	f.loginOpts = opts
	f.loginSeen = observer != nil
	if observer != nil && f.loginEvent != nil {
		observer.Observe(*f.loginEvent)
	}
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
	f.matchCalls++
	f.matchOpts = opts
	if f.matchFunc != nil {
		return f.matchFunc(songs, opts)
	}
	if f.match != nil {
		return append([]music2bb.MatchResult(nil), f.match...), nil
	}
	video := music2bb.Video{BVID: "BV1", Title: "song", Uploader: "artist"}
	results := make([]music2bb.MatchResult, len(songs))
	for index, song := range songs {
		copy := video
		results[index] = music2bb.MatchResult{Song: song, HasSelection: true, Video: &copy, Matched: true}
	}
	return results, nil
}

func (f *fakeBackend) SearchCandidatesWithOptions(_ context.Context, _ music2bb.Song, _ string, options music2bb.CandidateSearchOptions) ([]music2bb.MatchResult, error) {
	f.searchCalls++
	f.searchOpts = options
	if f.searchFunc != nil {
		return f.searchFunc(options)
	}
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

func (f *fakeBackend) AddToFavorite(ctx context.Context, favoriteID int64, outcomes []music2bb.MatchResult, observer music2bb.Observer) (music2bb.AddResult, error) {
	f.addCalls++
	f.addedTo = favoriteID
	bvids := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.HasSelection && outcome.Video != nil {
			bvids = append(bvids, outcome.Video.BVID)
		}
	}
	f.addedBVIDs = append(f.addedBVIDs, bvids)
	if f.addFunc != nil {
		return f.addFunc(ctx, favoriteID, outcomes, observer)
	}
	result := music2bb.AddResult{FavoriteID: favoriteID, Succeeded: append([]string(nil), bvids...)}
	for index, bvid := range result.Succeeded {
		if observer != nil {
			observer.Observe(music2bb.ProgressEvent{Kind: music2bb.EventVideo, Operation: "add_favorite", Message: bvid, Current: index + 1, Total: len(bvids)})
		}
	}
	return result, nil
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
	if backend.matchOpts != (music2bb.MatchOptions{
		SearchPages: 2, TopK: 5, Workers: 3, Profile: music2bb.MatchProfileClassical,
		SearchIdentity: music2bb.SearchIdentityAnonymous, SearchBudget: 4,
	}) {
		t.Fatalf("match options = %#v", backend.matchOpts)
	}
	if backend.loginOpts.AllowQR {
		t.Fatal("--no-qr-login did not disable QR")
	}
	if !strings.Contains(out.String(), "成功: 1") {
		t.Fatalf("output = %q", out.String())
	}
}

func TestURLShorthandStartsConversion(t *testing.T) {
	const playlistURL = "https://test.ulr.com"
	backend := &fakeBackend{}
	backend.parse = func(_ context.Context, rawURL string, _ music2bb.ParseOptions, _ music2bb.Observer) ([]music2bb.Song, error) {
		if rawURL != playlistURL {
			t.Fatalf("playlist URL = %q, want %q", rawURL, playlistURL)
		}
		return []music2bb.Song{{Name: "song", Artist: "artist"}}, nil
	}
	app, _, errOut := testApp(backend)
	if exit := app.Run(context.Background(), []string{playlistURL, "--favorite", "target", "--yes"}); exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr = %q", exit, errOut.String())
	}
	if backend.matchCalls != 1 {
		t.Fatalf("match calls = %d, want 1", backend.matchCalls)
	}
}

func TestFreshAndRefreshSearchHaveOrthogonalResumeSemantics(t *testing.T) {
	root := t.TempDir()
	base := &fakeBackend{}
	base.matchFunc = func(songs []music2bb.Song, _ music2bb.MatchOptions) ([]music2bb.MatchResult, error) {
		outcomes := make([]music2bb.MatchResult, len(songs))
		for index, song := range songs {
			video := music2bb.Video{BVID: "BV-" + song.Name, Title: song.Name}
			outcomes[index] = music2bb.MatchResult{
				Song: song, Video: &video, HasSelection: true, Matched: true,
				SearchStatus: music2bb.SearchStatusCompleted,
			}
		}
		return outcomes, nil
	}
	backend := &persistentFakeBackend{fakeBackend: base, configDir: root, cacheDir: root + "/cache"}
	songs := []music2bb.Song{{Name: "song", SourceID: "source:song"}}
	options := convertOptions{searchPages: 3, topK: 5, workers: 2, matchProfile: "standard", searchIdentity: "auto", searchBudget: 4}

	first := newConversionSession(backend, nil, "https://example.test/list", options, music2bb.BrowserAuto)
	if _, err := first.match(context.Background(), songs, nil); err != nil {
		t.Fatal(err)
	}
	if base.matchCalls != 1 {
		t.Fatalf("initial match calls = %d", base.matchCalls)
	}

	resumed := newConversionSession(backend, nil, "https://example.test/list", options, music2bb.BrowserAuto)
	if _, err := resumed.match(context.Background(), songs, nil); err != nil {
		t.Fatal(err)
	}
	if base.matchCalls != 1 {
		t.Fatalf("checkpoint resume made a remote match call: %d", base.matchCalls)
	}

	refreshOptions := options
	refreshOptions.refreshSearch = true
	refresh := newConversionSession(backend, nil, "https://example.test/list", refreshOptions, music2bb.BrowserAuto)
	if _, err := refresh.match(context.Background(), songs, nil); err != nil {
		t.Fatal(err)
	}
	if backend.resets != 1 || base.matchCalls != 1 {
		t.Fatalf("refresh-only resets/matches = %d/%d", backend.resets, base.matchCalls)
	}

	freshOptions := options
	freshOptions.fresh = true
	fresh := newConversionSession(backend, nil, "https://example.test/list", freshOptions, music2bb.BrowserAuto)
	if _, err := fresh.match(context.Background(), songs, nil); err != nil {
		t.Fatal(err)
	}
	if base.matchCalls != 2 || base.matchOpts.SearchCachePolicy != music2bb.SearchCacheDefault {
		t.Fatalf("fresh match calls/options = %d/%#v", base.matchCalls, base.matchOpts)
	}

	bothOptions := options
	bothOptions.fresh, bothOptions.refreshSearch = true, true
	both := newConversionSession(backend, nil, "https://example.test/list", bothOptions, music2bb.BrowserAuto)
	if _, err := both.match(context.Background(), songs, nil); err != nil {
		t.Fatal(err)
	}
	if backend.resets != 2 || base.matchCalls != 3 || base.matchOpts.SearchCachePolicy != music2bb.SearchCacheRefresh {
		t.Fatalf("combined resets/matches/options = %d/%d/%#v", backend.resets, base.matchCalls, base.matchOpts)
	}
}

func TestSessionResumeMatchesOnlyIncompleteSongsAndRealignsOrder(t *testing.T) {
	root := t.TempDir()
	rawURL := "https://example.test/list"
	songA := music2bb.Song{Name: "A", SourceID: "source:a"}
	songB := music2bb.Song{Name: "B", SourceID: "source:b"}
	songC := music2bb.Song{Name: "C", SourceID: "source:c"}
	videoA := music2bb.Video{BVID: "BVA", Title: "A"}
	state := newConversionState(root, rawURL, time.Now)
	if err := state.saveOutcome(music2bb.MatchResult{Song: songA, Video: &videoA, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := state.saveOutcome(music2bb.MatchResult{Song: songB, NeedsReview: true, SearchStatus: music2bb.SearchStatusRiskControl}); err != nil {
		t.Fatal(err)
	}

	base := &fakeBackend{}
	base.matchFunc = func(songs []music2bb.Song, _ music2bb.MatchOptions) ([]music2bb.MatchResult, error) {
		if got := []string{songs[0].SourceID, songs[1].SourceID}; !reflect.DeepEqual(got, []string{"source:b", "source:c"}) {
			t.Fatalf("pending songs = %#v", got)
		}
		outcomes := make([]music2bb.MatchResult, len(songs))
		for index, song := range songs {
			video := music2bb.Video{BVID: "BV-" + song.Name}
			outcomes[index] = music2bb.MatchResult{Song: song, Video: &video, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted}
		}
		return outcomes, nil
	}
	backend := &persistentFakeBackend{fakeBackend: base, configDir: root, cacheDir: root + "/cache"}
	options := convertOptions{searchPages: 3, topK: 5, workers: 2, matchProfile: "standard", searchIdentity: "auto", searchBudget: 4}
	session := newConversionSession(backend, nil, rawURL, options, music2bb.BrowserAuto)
	outcomes, err := session.match(context.Background(), []music2bb.Song{songB, songA, songC}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(outcomes) != 3 || outcomes[0].Video.BVID != "BV-B" || outcomes[1].Video.BVID != "BVA" || outcomes[2].Video.BVID != "BV-C" {
		t.Fatalf("realigned outcomes = %#v", outcomes)
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
	if _, err := session.search(context.Background(), music2bb.Song{Name: "song"}, "query", nil); err != nil {
		t.Fatal(err)
	}
	if backend.searchOpts != (music2bb.CandidateSearchOptions{Limit: 10, Profile: music2bb.MatchProfileClassical, SearchIdentity: music2bb.SearchIdentityAnonymous}) {
		t.Fatalf("search options = %#v", backend.searchOpts)
	}
}

func TestManualSearchLoginForwardsObserver(t *testing.T) {
	for _, identity := range []string{string(music2bb.SearchIdentitySession), "auto"} {
		t.Run(identity, func(t *testing.T) {
			qr := music2bb.ProgressEvent{Kind: music2bb.EventQR, Operation: "login", QRPayload: "fixture-qr"}
			backend := &fakeBackend{loginEvent: &qr}
			if identity == "auto" {
				backend.searchFunc = func(options music2bb.CandidateSearchOptions) ([]music2bb.MatchResult, error) {
					if options.SearchIdentity == music2bb.SearchIdentityAnonymous {
						return nil, &music2bb.Error{
							Category: music2bb.ErrorNetwork, Operation: "search", Message: "risk control",
							RiskReason: music2bb.RiskControlVoucher, SearchIdentity: music2bb.SearchIdentityAnonymous,
						}
					}
					return nil, nil
				}
			}
			session := newConversionSession(backend, nil, "url", convertOptions{
				matchProfile: string(music2bb.MatchProfileStandard), searchIdentity: identity,
			}, music2bb.BrowserAuto)
			var events []music2bb.ProgressEvent
			observer := music2bb.ObserverFunc(func(event music2bb.ProgressEvent) { events = append(events, event) })
			if _, err := session.search(context.Background(), music2bb.Song{Name: "song"}, "query", observer); err != nil {
				t.Fatal(err)
			}
			if !backend.loginSeen || len(events) != 1 || events[0].Kind != music2bb.EventQR || events[0].QRPayload != "fixture-qr" {
				t.Fatalf("login observer/events = %t/%#v", backend.loginSeen, events)
			}
		})
	}
}

func TestConvertDelaysLoginUntilAnonymousSearchNeedsFallback(t *testing.T) {
	backend := &fakeBackend{}
	backend.parse = func(context.Context, string, music2bb.ParseOptions, music2bb.Observer) ([]music2bb.Song, error) {
		if backend.loginCalls != 0 {
			t.Fatal("login happened before playlist parsing")
		}
		return []music2bb.Song{{Name: "done"}, {Name: "retry"}}, nil
	}
	backend.matchFunc = func(songs []music2bb.Song, opts music2bb.MatchOptions) ([]music2bb.MatchResult, error) {
		if opts.SearchIdentity == music2bb.SearchIdentityAnonymous {
			if backend.loginCalls != 0 {
				t.Fatal("login happened before anonymous matching")
			}
			video := music2bb.Video{BVID: "BVdone", Title: "done"}
			return []music2bb.MatchResult{
				{Song: songs[0], Video: &video, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted, RemoteRequests: 1},
				{Song: songs[1], NeedsReview: true, ReviewReason: music2bb.ReviewRiskControl, SearchStatus: music2bb.SearchStatusRiskControl, RemoteRequests: 1, RiskReason: music2bb.RiskControlVoucher},
			}, &music2bb.BatchError{Category: music2bb.ErrorNetwork, HaltReason: music2bb.RiskControlVoucher, SearchIdentity: music2bb.SearchIdentityAnonymous}
		}
		video := music2bb.Video{BVID: "BVretry", Title: "retry"}
		return []music2bb.MatchResult{{Song: songs[0], Video: &video, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted, RemoteRequests: 1}}, nil
	}
	app, _, errOut := testApp(backend)
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--no-tui", "--favorite", "target", "--yes"})
	if exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr=%q", exit, errOut.String())
	}
	if backend.matchCalls != 2 || backend.loginCalls != 1 || backend.addCalls != 1 {
		t.Fatalf("calls = match %d login %d add %d", backend.matchCalls, backend.loginCalls, backend.addCalls)
	}
}

func TestSessionBlocksWritesAfterSessionRiskControl(t *testing.T) {
	backend := &fakeBackend{}
	backend.matchFunc = func(songs []music2bb.Song, opts music2bb.MatchOptions) ([]music2bb.MatchResult, error) {
		outcome := music2bb.MatchResult{
			Song: songs[0], NeedsReview: true, ReviewReason: music2bb.ReviewRiskControl,
			SearchStatus: music2bb.SearchStatusRiskControl, RemoteRequests: 1, RiskReason: music2bb.RiskControlCode412,
		}
		return []music2bb.MatchResult{outcome}, &music2bb.BatchError{
			Category: music2bb.ErrorNetwork, HaltReason: music2bb.RiskControlCode412, SearchIdentity: opts.SearchIdentity,
		}
	}
	session := newConversionSession(backend, nil, "url", convertOptions{
		matchProfile: string(music2bb.MatchProfileStandard), searchIdentity: "auto", searchPages: 3, topK: 5, workers: 2, searchBudget: 4,
	}, music2bb.BrowserAuto)
	outcomes, err := session.match(context.Background(), []music2bb.Song{{Name: "song"}}, nil)
	if riskReasonOf(err) != music2bb.RiskControlCode412 || len(outcomes) != 1 {
		t.Fatalf("match = %#v, %v", outcomes, err)
	}
	if _, writeErr := session.write(context.Background(), 9, outcomes, nil); writeErr == nil {
		t.Fatal("write succeeded after session risk control")
	}
	if backend.addCalls != 0 {
		t.Fatalf("favorite write calls = %d, want zero", backend.addCalls)
	}
	if _, searchErr := session.search(context.Background(), music2bb.Song{Name: "song"}, "query", nil); riskReasonOf(searchErr) != music2bb.RiskControlCode412 {
		t.Fatalf("manual search halt error = %T %v", searchErr, searchErr)
	}
	if backend.searchCalls != 0 {
		t.Fatalf("manual search made %d remote calls after halt", backend.searchCalls)
	}
	matchCalls := backend.matchCalls
	if _, retryErr := session.match(context.Background(), []music2bb.Song{{Name: "retry"}}, nil); riskReasonOf(retryErr) != music2bb.RiskControlCode412 {
		t.Fatalf("batch search halt error = %T %v", retryErr, retryErr)
	}
	if backend.matchCalls != matchCalls {
		t.Fatalf("batch search made %d additional remote calls after halt", backend.matchCalls-matchCalls)
	}
}

func TestSessionWriteReceiptsSkipSucceededBVIDsOnRetry(t *testing.T) {
	root := t.TempDir()
	base := &fakeBackend{}
	firstCall := true
	base.addFunc = func(_ context.Context, favoriteID int64, outcomes []music2bb.MatchResult, observer music2bb.Observer) (music2bb.AddResult, error) {
		result := music2bb.AddResult{FavoriteID: favoriteID}
		for _, outcome := range outcomes {
			if outcome.Video.BVID == "BV-one" && firstCall {
				result.Succeeded = append(result.Succeeded, outcome.Video.BVID)
				observer.Observe(music2bb.ProgressEvent{Kind: music2bb.EventVideo, Operation: "add_favorite", Message: outcome.Video.BVID})
			} else if firstCall {
				result.Failed = append(result.Failed, music2bb.AddFailure{BVID: outcome.Video.BVID, Reason: "temporary"})
			} else {
				result.Succeeded = append(result.Succeeded, outcome.Video.BVID)
				observer.Observe(music2bb.ProgressEvent{Kind: music2bb.EventVideo, Operation: "add_favorite", Message: outcome.Video.BVID})
			}
		}
		if firstCall {
			firstCall = false
			return result, &music2bb.Error{Category: music2bb.ErrorPartialWrite, Operation: "write", Message: "partial"}
		}
		return result, nil
	}
	backend := &persistentFakeBackend{fakeBackend: base, configDir: root, cacheDir: filepath.Join(root, "cache")}
	session := newConversionSession(backend, nil, "https://example.test/list", convertOptions{}, music2bb.BrowserAuto)
	one := music2bb.Video{BVID: "BV-one"}
	two := music2bb.Video{BVID: "BV-two"}
	outcomes := []music2bb.MatchResult{
		{Song: music2bb.Song{Name: "one"}, Video: &one, HasSelection: true},
		{Song: music2bb.Song{Name: "two"}, Video: &two, HasSelection: true},
	}
	if result, err := session.write(context.Background(), 9, outcomes, nil); err == nil || len(result.Succeeded) != 1 {
		t.Fatalf("first write = %#v, %v", result, err)
	}
	result, err := session.write(context.Background(), 9, outcomes, nil)
	if err != nil || !reflect.DeepEqual(result.Succeeded, []string{"BV-two"}) || !reflect.DeepEqual(result.Skipped, []string{"BV-one"}) {
		t.Fatalf("retry write = %#v, %v", result, err)
	}
	if got := base.addedBVIDs; len(got) != 2 || !reflect.DeepEqual(got[0], []string{"BV-one", "BV-two"}) || !reflect.DeepEqual(got[1], []string{"BV-two"}) {
		t.Fatalf("submitted BVIDs = %#v", got)
	}

	firstCall = false
	if _, err := session.write(context.Background(), 10, outcomes, nil); err != nil {
		t.Fatal(err)
	}
	if got := base.addedBVIDs[len(base.addedBVIDs)-1]; !reflect.DeepEqual(got, []string{"BV-one", "BV-two"}) {
		t.Fatalf("different favorite submitted BVIDs = %#v", got)
	}
}

func TestSessionRejectsWriteUntilEverySongIsResolved(t *testing.T) {
	backend := &fakeBackend{}
	session := newConversionSession(backend, nil, "url", convertOptions{}, music2bb.BrowserAuto)
	video := music2bb.Video{BVID: "BV-one"}
	outcomes := []music2bb.MatchResult{
		{Video: &video, HasSelection: true},
		{NeedsReview: true, ReviewReason: music2bb.ReviewNotSearched},
	}
	if _, err := session.prepareWrite(context.Background(), nil, outcomes); err == nil {
		t.Fatal("prepareWrite accepted an unresolved song")
	}
	if _, err := session.write(context.Background(), 9, outcomes, nil); err == nil {
		t.Fatal("write accepted an unresolved song")
	}
	if backend.loginCalls != 0 || backend.addCalls != 0 {
		t.Fatalf("calls = login %d add %d", backend.loginCalls, backend.addCalls)
	}
}

func TestVerboseSearchSummaryAndIdentitySwitchWarning(t *testing.T) {
	root := t.TempDir()
	base := &fakeBackend{}
	base.matchFunc = func(songs []music2bb.Song, options music2bb.MatchOptions) ([]music2bb.MatchResult, error) {
		if options.SearchIdentity == music2bb.SearchIdentityAnonymous {
			return []music2bb.MatchResult{{
				Song: songs[0], NeedsReview: true, ReviewReason: music2bb.ReviewRiskControl,
				SearchStatus: music2bb.SearchStatusRiskControl, SearchIdentity: music2bb.SearchIdentityAnonymous,
				RemoteRequests: 1, CacheHits: 2, RiskReason: music2bb.RiskControlVoucher,
			}}, &music2bb.BatchError{Category: music2bb.ErrorNetwork, HaltReason: music2bb.RiskControlVoucher, SearchIdentity: music2bb.SearchIdentityAnonymous}
		}
		video := music2bb.Video{BVID: "BV-one"}
		return []music2bb.MatchResult{{
			Song: songs[0], Video: &video, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted,
			SearchIdentity: music2bb.SearchIdentitySession, RemoteRequests: 2, CacheHits: 1,
		}}, nil
	}
	backend := &persistentFakeBackend{fakeBackend: base, configDir: root, cacheDir: filepath.Join(root, "cache")}
	session := newConversionSession(backend, nil, "url", convertOptions{
		searchPages: 3, topK: 5, workers: 2, matchProfile: "standard", searchIdentity: "auto", searchBudget: 4, verbose: true,
	}, music2bb.BrowserAuto)
	var messages []string
	observer := music2bb.ObserverFunc(func(event music2bb.ProgressEvent) { messages = append(messages, event.Message) })
	if _, err := session.match(context.Background(), []music2bb.Song{{Name: "one"}}, observer); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(messages, "\n")
	for _, want := range []string{"已切换登录态继续 1 首", "缓存命中 3", "匿名远程请求 1", "登录远程请求 2", "完成歌曲 1/1", "预算消耗 3/4"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("messages missing %q: %q", want, joined)
		}
	}
}

func TestCacheStatusAndSelectiveClearPreserveAccountCookies(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, "config")
	cacheDir := filepath.Join(root, "cache")
	paths := []string{
		filepath.Join(cacheDir, "search", "v1", "page.json"),
		filepath.Join(configDir, "conversions", "v1", "checkpoint.json"),
		filepath.Join(configDir, "decisions", "v1", "decision.json"),
		filepath.Join(configDir, "cookies", "bilibili-anonymous.json"),
		filepath.Join(configDir, "cookies", "bilibili.json"),
	}
	for _, path := range paths {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	backend := &persistentFakeBackend{fakeBackend: &fakeBackend{}, configDir: configDir, cacheDir: cacheDir}
	app, out, errOut := testApp(backend)
	if exit := app.Run(context.Background(), []string{"cache", "status"}); exit != ExitSuccess {
		t.Fatalf("status exit=%d stderr=%q", exit, errOut.String())
	}
	for _, want := range []string{"search\tfiles=1", "checkpoints\tfiles=1", "decisions\tfiles=1", "anonymous-identity\tfiles=1"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("status missing %q: %q", want, out.String())
		}
	}
	out.Reset()
	if exit := app.Run(context.Background(), []string{"cache", "clear", "--search", "--checkpoints", "--decisions", "--anonymous-identity"}); exit != ExitSuccess {
		t.Fatalf("clear exit=%d stderr=%q", exit, errOut.String())
	}
	for _, path := range paths[:4] {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("cleared path still exists: %s (%v)", path, err)
		}
	}
	if payload, err := os.ReadFile(paths[4]); err != nil || string(payload) != "state" {
		t.Fatalf("account cookie changed: %q, %v", payload, err)
	}
	if backend.resets != 1 {
		t.Fatalf("anonymous resets = %d", backend.resets)
	}
	for _, path := range paths[:4] {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("state"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if exit := app.Run(context.Background(), []string{"cache", "clear", "--all"}); exit != ExitSuccess {
		t.Fatalf("clear --all exit=%d stderr=%q", exit, errOut.String())
	}
	for _, path := range paths[:4] {
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("--all path still exists: %s (%v)", path, err)
		}
	}
	if payload, err := os.ReadFile(paths[4]); err != nil || string(payload) != "state" {
		t.Fatalf("--all changed account cookie: %q, %v", payload, err)
	}
	if backend.resets != 2 {
		t.Fatalf("anonymous resets after --all = %d", backend.resets)
	}
}

func TestOrdinarySearchFailureDoesNotTriggerLoginFallback(t *testing.T) {
	backend := &fakeBackend{}
	backend.matchFunc = func(songs []music2bb.Song, _ music2bb.MatchOptions) ([]music2bb.MatchResult, error) {
		return []music2bb.MatchResult{{Song: songs[0], NeedsReview: true, ReviewReason: music2bb.ReviewSearchFailed, SearchStatus: music2bb.SearchStatusFailed}},
			&music2bb.BatchError{Category: music2bb.ErrorNetwork, Failures: []music2bb.ItemFailure{{Index: 0, Reason: "timeout"}}}
	}
	session := newConversionSession(backend, nil, "url", convertOptions{matchProfile: "standard", searchIdentity: "auto", searchPages: 3, topK: 5, workers: 2, searchBudget: 4}, music2bb.BrowserAuto)
	_, _ = session.match(context.Background(), []music2bb.Song{{Name: "song"}}, nil)
	if backend.loginCalls != 0 {
		t.Fatalf("ordinary failure triggered %d login call(s)", backend.loginCalls)
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
