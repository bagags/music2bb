package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	music2bb "github.com/bagags/music2bb-go"
)

func TestTUIPhaseTransitionsAndConfirmationGate(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()

	model = updateTUI(t, model, tuiAccountMsg{account: music2bb.Account{Name: "tester"}})
	model = updateTUI(t, model, tuiPhaseMsg{phase: phaseParse, text: "parse"})
	model = updateTUI(t, model, tuiSongsMsg{songs: sampleSongs()})
	model = updateTUI(t, model, tuiPhaseMsg{phase: phaseMatch, text: "match"})
	model = updateTUI(t, model, tuiMatchesMsg{outcomes: sampleOutcomes()})
	if model.phase != phaseReview || model.songCursor != 1 {
		t.Fatalf("review state = phase %v cursor %d", model.phase, model.songCursor)
	}

	model = pressTUI(t, model, "c")
	if model.phase != phaseReview || !strings.Contains(model.validation, "1 首") {
		t.Fatalf("unresolved review passed confirmation gate: %#v", model)
	}
	model = pressTUI(t, model, "x")
	model = pressTUI(t, model, "c")
	if model.phase != phaseFavorite {
		t.Fatalf("phase after resolving reviews = %v", model.phase)
	}
	model = updateTUI(t, model, tuiFavoritesMsg{favorites: []music2bb.Favorite{{ID: 9, Title: "target"}}})
	model = pressTUI(t, model, "enter")
	if model.phase != phaseConfirm || model.selectedFavorite.ID != 9 {
		t.Fatalf("favorite selection = %#v", model.selectedFavorite)
	}
	model = pressTUI(t, model, "c")
	if model.phase != phaseWrite {
		t.Fatalf("phase = %v, want write", model.phase)
	}
	model = updateTUI(t, model, tuiWriteMsg{result: music2bb.AddResult{FavoriteID: 9, Succeeded: []string{"BV-auto"}}})
	if model.phase != phaseResult || !strings.Contains(model.receipt, "成功: 1") || !strings.Contains(model.receipt, "跳过: 1") {
		t.Fatalf("result receipt = %q", model.receipt)
	}
}

func TestTUIReviewKeyBindingsAndOverride(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()
	model.songs = sampleSongs()
	model.outcomes = sampleOutcomes()
	model.processed = []bool{true, true}
	model.confirmed = make([]bool, 2)
	model.skipped = make([]bool, 2)
	model.phase = phaseReview
	model.songCursor = 0

	model = pressTUI(t, model, "right")
	if model.songCursor != 1 {
		t.Fatalf("right did not move song: %d", model.songCursor)
	}
	model = pressTUI(t, model, "down")
	if model.candCursor != 1 {
		t.Fatalf("down did not move candidate: %d", model.candCursor)
	}
	model = pressTUI(t, model, "enter")
	if !model.outcomes[1].HasSelection || model.outcomes[1].Video.BVID != "BV-alt" || !model.outcomes[1].ManualOverride {
		t.Fatalf("candidate override = %#v", model.outcomes[1])
	}
	model = pressTUI(t, model, "u")
	if model.outcomes[1].HasSelection || !model.outcomes[1].NeedsReview {
		t.Fatalf("undo did not restore unresolved state: %#v", model.outcomes[1])
	}
	model = pressTUI(t, model, "x")
	if !model.skipped[1] {
		t.Fatal("x did not skip song")
	}
	model = pressTUI(t, model, "left")
	model = pressTUI(t, model, "tab")
	if model.songCursor != 0 && model.unresolvedCount() > 0 {
		t.Fatalf("tab did not target unresolved song: %d", model.songCursor)
	}
	model = pressTUI(t, model, "?")
	if model.overlay != overlayHelp || !strings.Contains(model.render(), "快捷键") {
		t.Fatal("help overlay did not open")
	}
}

func TestTUIManualSearchAndPrivateFavoriteCreation(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()
	model.songs = sampleSongs()
	model.outcomes = sampleOutcomes()
	model.processed = []bool{true, true}
	model.confirmed = make([]bool, 2)
	model.skipped = make([]bool, 2)
	model.phase = phaseReview
	model.songCursor = 1

	model = pressTUI(t, model, "s")
	if model.overlay != overlaySearch {
		t.Fatal("search overlay did not open")
	}
	model.input.SetValue("manual query")
	model = pressTUI(t, model, "enter")
	if !model.busy {
		t.Fatal("manual search was not launched")
	}
	searchRequestID := model.searchRequestID
	manualVideo := music2bb.Video{BVID: "BV-manual", Title: "Manual", Uploader: "UP"}
	model = updateTUI(t, model, tuiSearchMsg{requestID: searchRequestID, index: 1, candidates: []music2bb.MatchResult{{Video: &manualVideo, Score: 50}}})
	if len(model.outcomes[1].Candidates) != 1 || model.overlay != overlayNone {
		t.Fatalf("manual search result = %#v", model.outcomes[1].Candidates)
	}

	model.phase = phaseFavorite
	model = pressTUI(t, model, "n")
	if !model.createPrivate || model.overlay != overlayCreateFavorite {
		t.Fatal("favorite creation was not private by default")
	}
	model.input.SetValue("private folder")
	model = pressTUI(t, model, "enter")
	if !model.busy {
		t.Fatal("favorite creation was not launched")
	}
	favoriteRequestID := model.favoriteRequestID
	model = updateTUI(t, model, tuiFavoriteCreatedMsg{requestID: favoriteRequestID, favorite: music2bb.Favorite{ID: 12, Title: "private folder"}})
	if model.phase != phaseConfirm || model.selectedFavorite.ID != 12 {
		t.Fatalf("created favorite did not advance: %#v", model.selectedFavorite)
	}
}

func TestTUIDismissedSearchDoesNotApplyStaleResults(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()
	model.songs = sampleSongs()
	model.outcomes = sampleOutcomes()
	model.processed = []bool{true, true}
	model.confirmed = make([]bool, 2)
	model.skipped = make([]bool, 2)
	model.phase = phaseReview
	model.songCursor = 1

	model = pressTUI(t, model, "s")
	model.input.SetValue("stale query")
	model = pressTUI(t, model, "enter")
	staleRequestID := model.searchRequestID
	model = pressTUI(t, model, "esc")
	model = pressTUI(t, model, "s")
	model.input.SetValue("fresh query")
	model = pressTUI(t, model, "enter")
	freshRequestID := model.searchRequestID
	staleVideo := music2bb.Video{BVID: "BV-stale", Title: "Stale"}
	model = updateTUI(t, model, tuiSearchMsg{requestID: staleRequestID, index: 1, candidates: []music2bb.MatchResult{{Video: &staleVideo}}})
	if len(model.outcomes[1].Candidates) != 2 || model.outcomes[1].Candidates[0].Video.BVID == "BV-stale" || model.overlay != overlaySearch || !model.busy {
		t.Fatalf("dismissed search applied stale candidates: %#v", model.outcomes[1].Candidates)
	}
	freshVideo := music2bb.Video{BVID: "BV-fresh", Title: "Fresh"}
	model = updateTUI(t, model, tuiSearchMsg{requestID: freshRequestID, index: 1, candidates: []music2bb.MatchResult{{Video: &freshVideo}}})
	if model.overlay != overlayNone || len(model.outcomes[1].Candidates) != 1 || model.outcomes[1].Candidates[0].Video.BVID != "BV-fresh" {
		t.Fatalf("fresh search result was not applied: %#v", model.outcomes[1].Candidates)
	}
}

func TestTUIDismissedFavoriteCreationCannotStartDuplicate(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()
	model.phase = phaseFavorite
	model = pressTUI(t, model, "n")
	model.input.SetValue("folder")
	model, first := updateTUIWithCmd(t, model, keyPress("enter"))
	if first == nil || !model.busy {
		t.Fatal("favorite creation did not start")
	}
	requestID := model.favoriteRequestID
	model = pressTUI(t, model, "esc")
	model = pressTUI(t, model, "n")
	model.input.SetValue("folder")
	model, duplicate := updateTUIWithCmd(t, model, keyPress("enter"))
	if duplicate != nil {
		t.Fatal("dismissed in-flight favorite creation allowed a duplicate request")
	}
	model = updateTUI(t, model, tuiFavoriteCreatedMsg{requestID: requestID, favorite: music2bb.Favorite{ID: 12, Title: "folder"}})
	if model.phase != phaseFavorite || model.overlay != overlayCreateFavorite || model.selectedFavorite.ID != 0 || len(model.favorites) != 1 {
		t.Fatalf("dismissed favorite result changed active flow: %#v", model)
	}
}

func TestTUICtrlCCancelsFromEveryOverlay(t *testing.T) {
	tests := []struct {
		name    string
		overlay tuiOverlay
	}{
		{name: "search", overlay: overlaySearch},
		{name: "manual songs", overlay: overlayManualSongs},
		{name: "create favorite", overlay: overlayCreateFavorite},
		{name: "help", overlay: overlayHelp},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, cleanup := testTUIModel(t)
			defer cleanup()
			model.overlay = tt.overlay
			model, cmd := updateTUIWithCmd(t, model, keyPress("ctrl+c"))
			if cmd == nil || model.exitCode != ExitCancelled {
				t.Fatalf("Ctrl-C did not cancel: exit=%d cmd=%v", model.exitCode, cmd)
			}
			select {
			case <-model.controller.ctx.Done():
			default:
				t.Fatal("Ctrl-C did not cancel controller context")
			}
		})
	}
}

func TestTUIParseFailureManualEntryAndPartialWriteReceipt(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()
	model = updateTUI(t, model, tuiSongsMsg{err: errors.New("bad page")})
	if model.phase != phaseParseFailed {
		t.Fatalf("phase = %v", model.phase)
	}
	model = pressTUI(t, model, "m")
	model.manualInput.SetValue("中文歌 - 歌手\nLatin Song - Artist")
	model = pressTUI(t, model, "ctrl+s")
	if model.overlay != overlayNone {
		t.Fatal("manual song entry did not submit")
	}
	if songs := parseManualSongText("中文歌 - 歌手\nLatin Song - Artist"); len(songs) != 2 || songs[0].Artist != "歌手" {
		t.Fatalf("manual songs = %#v", songs)
	}

	model.songs = sampleSongs()
	model.outcomes = sampleOutcomes()
	model.processed = []bool{true, true}
	model.skipped = []bool{false, true}
	model.confirmed = make([]bool, 2)
	model.selectedFavorite = music2bb.Favorite{ID: 9, Title: "target"}
	model.phase = phaseWrite
	partial := music2bb.AddResult{FavoriteID: 9, Succeeded: []string{"BV-auto"}, Failed: []music2bb.AddFailure{{BVID: "BV2", Reason: "denied"}}}
	model = updateTUI(t, model, tuiWriteMsg{result: partial, err: &music2bb.Error{Category: music2bb.ErrorPartialWrite, Operation: "write", Message: "partial"}})
	if model.exitCode != ExitPartialWrite || !strings.Contains(model.receipt, "成功: 1 | 失败: 1 | 跳过: 1") {
		t.Fatalf("partial receipt = %q exit=%d", model.receipt, model.exitCode)
	}
}

func TestTUIResponsiveRenderSnapshots(t *testing.T) {
	model, cleanup := testTUIModel(t)
	defer cleanup()
	model.phase = phaseReview
	model.phaseText = "审核匹配结果"
	model.songs = sampleSongs()
	model.outcomes = sampleOutcomes()
	model.processed = []bool{true, true}
	model.confirmed = make([]bool, 2)
	model.skipped = make([]bool, 2)
	model.songCursor = 1
	model.colorEnabled = false

	tests := []struct {
		width, height int
		contains      []string
		notContains   string
	}{
		{120, 32, []string{"中文歌", "Latin Song", "BV-review"}, ""},
		{80, 20, []string{"中文歌", "Latin Song", "BV-review"}, ""},
		{70, 18, []string{"中文歌", "Latin Song"}, "BV-review"},
		{39, 11, []string{"终端窗口太小", "40×12"}, "BV-review"},
	}
	for _, tt := range tests {
		t.Run(fmt.Sprintf("%dx%d", tt.width, tt.height), func(t *testing.T) {
			model.width, model.height = tt.width, tt.height
			got := model.render()
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Fatalf("render missing %q:\n%s", want, got)
				}
			}
			if tt.notContains != "" && strings.Contains(got, tt.notContains) {
				t.Fatalf("single-pane render unexpectedly contains %q:\n%s", tt.notContains, got)
			}
			if strings.Contains(got, "\x1b[") {
				t.Fatalf("color-free render contains ANSI: %q", got)
			}
			for _, line := range strings.Split(got, "\n") {
				if lipgloss.Width(line) > tt.width {
					t.Fatalf("line width %d exceeds %d: %q", lipgloss.Width(line), tt.width, line)
				}
			}
		})
	}

	model.width, model.height = 70, 18
	model.compactPane = 1
	if got := model.render(); !strings.Contains(got, "BV-review") {
		t.Fatalf("compact detail pane was not inspectable:\n%s", got)
	}
	model.colorEnabled = true
	model.width, model.height = 80, 20
	model.dark = false
	light := model.render()
	model.dark = true
	dark := model.render()
	if light == dark {
		t.Fatal("light and dark adaptive renders are identical")
	}
}

func TestTUIControllerOrdersEventsAndCloses(t *testing.T) {
	session := newConversionSession(&fakeBackend{}, nil, "https://example.test", convertOptions{}, music2bb.BrowserAuto)
	controller := newTUIController(context.Background(), session)
	controller.send(tuiPhaseMsg{phase: phaseLogin, text: "one"})
	controller.send(tuiPhaseMsg{phase: phaseParse, text: "two"})
	first := controller.waitCmd()().(tuiPhaseMsg)
	second := controller.waitCmd()().(tuiPhaseMsg)
	if first.text != "one" || second.text != "two" {
		t.Fatalf("event order = %q, %q", first.text, second.text)
	}
	controller.close()
	if _, ok := controller.waitCmd()().(tuiChannelClosedMsg); !ok {
		t.Fatal("closed controller did not terminate waiter")
	}
}

type cancellationBackend struct {
	*fakeBackend
	started chan struct{}
	done    chan struct{}
}

func (b *cancellationBackend) AddToFavorite(ctx context.Context, favoriteID int64, _ []music2bb.MatchResult, _ music2bb.Observer) (music2bb.AddResult, error) {
	close(b.started)
	<-ctx.Done()
	close(b.done)
	return music2bb.AddResult{FavoriteID: favoriteID, Succeeded: []string{"BV-already"}}, ctx.Err()
}

func TestTUIControllerCancellationWaitsForWrite(t *testing.T) {
	backend := &cancellationBackend{fakeBackend: &fakeBackend{}, started: make(chan struct{}), done: make(chan struct{})}
	session := newConversionSession(backend, nil, "https://example.test", convertOptions{}, music2bb.BrowserAuto)
	controller := newTUIController(context.Background(), session)
	cmd := controller.writeCmd(9, sampleOutcomes())
	go cmd()
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("write did not start")
	}
	controller.cancel()
	select {
	case <-backend.done:
	case <-time.After(time.Second):
		t.Fatal("write did not stop on cancellation")
	}
	msg := controller.waitCmd()()
	result, ok := msg.(tuiWriteMsg)
	if !ok || len(result.result.Succeeded) != 1 || !errors.Is(result.err, context.Canceled) {
		t.Fatalf("write result = %#v", msg)
	}
	controller.close()
}

func TestPlainFallbackHasNoANSIOrCarriageReturns(t *testing.T) {
	backend := &fakeBackend{}
	app, out, errOut := testApp(backend)
	app.IO.Interactive = true
	exit := app.Run(context.Background(), []string{"convert", "https://example.test/list", "--no-tui", "--favorite", "target", "--yes"})
	if exit != ExitSuccess {
		t.Fatalf("exit=%d stderr=%q", exit, errOut.String())
	}
	combined := out.String() + errOut.String()
	if strings.Contains(combined, "\x1b[") || strings.Contains(combined, "\r") {
		t.Fatalf("plain output contains terminal control sequences: %q", combined)
	}
	if count := strings.Count(combined, "[1/1]"); count != 1 {
		t.Fatalf("plain output duplicated per-song status (%d): %q", count, combined)
	}
}

func TestTUIAutomaticLaunchConditions(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	if !shouldUseTUI(IO{Interactive: true}, false) {
		t.Fatal("interactive terminal did not select TUI")
	}
	if shouldUseTUI(IO{Interactive: false}, false) || shouldUseTUI(IO{Interactive: true}, true) {
		t.Fatal("non-terminal or --no-tui selected TUI")
	}
	t.Setenv("TERM", "dumb")
	if shouldUseTUI(IO{Interactive: true}, false) {
		t.Fatal("TERM=dumb selected TUI")
	}
}

func TestConvertHelpDocumentsTUIFallback(t *testing.T) {
	app, _, errOut := testApp(&fakeBackend{})
	if exit := app.Run(context.Background(), []string{"convert", "--help"}); exit != ExitSuccess {
		t.Fatalf("exit = %d", exit)
	}
	if output := errOut.String(); !strings.Contains(output, "--no-tui") || !strings.Contains(output, "全屏审核工作区") {
		t.Fatalf("help output = %q", output)
	}
}

func testTUIModel(t *testing.T) (tuiModel, func()) {
	t.Helper()
	session := newConversionSession(&fakeBackend{}, nil, "https://example.test", convertOptions{}, music2bb.BrowserAuto)
	controller := newTUIController(context.Background(), session)
	model := newTUIModel(controller)
	model.width, model.height = 120, 32
	return model, controller.close
}

func updateTUI(t *testing.T, model tuiModel, msg tea.Msg) tuiModel {
	t.Helper()
	updated, _ := model.Update(msg)
	result, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("updated model type = %T", updated)
	}
	return result
}

func updateTUIWithCmd(t *testing.T, model tuiModel, msg tea.Msg) (tuiModel, tea.Cmd) {
	t.Helper()
	updated, cmd := model.Update(msg)
	result, ok := updated.(tuiModel)
	if !ok {
		t.Fatalf("updated model type = %T", updated)
	}
	return result, cmd
}

func keyPress(stroke string) tea.KeyPressMsg {
	var key tea.Key
	switch stroke {
	case "esc":
		key.Code = tea.KeyEscape
	case "enter":
		key.Code = tea.KeyEnter
	case "ctrl+c":
		key.Code = 'c'
		key.Mod = tea.ModCtrl
	default:
		key.Code = []rune(stroke)[0]
		key.Text = stroke
	}
	return tea.KeyPressMsg(key)
}

func pressTUI(t *testing.T, model tuiModel, stroke string) tuiModel {
	t.Helper()
	if stroke == "esc" || stroke == "ctrl+c" {
		return updateTUI(t, model, keyPress(stroke))
	}
	var key tea.Key
	switch stroke {
	case "left":
		key.Code = tea.KeyLeft
	case "right":
		key.Code = tea.KeyRight
	case "up":
		key.Code = tea.KeyUp
	case "down":
		key.Code = tea.KeyDown
	case "enter":
		key.Code = tea.KeyEnter
	case "tab":
		key.Code = tea.KeyTab
	case "ctrl+s":
		key.Code = 's'
		key.Mod = tea.ModCtrl
	default:
		key.Code = []rune(stroke)[0]
		key.Text = stroke
	}
	return updateTUI(t, model, tea.KeyPressMsg(key))
}

func sampleSongs() []music2bb.Song {
	return []music2bb.Song{
		{Name: "中文歌", Artist: "歌手", Album: "专辑", Duration: "03:20"},
		{Name: "Latin Song", Artist: "Artist", Album: "Album", Duration: "04:10"},
	}
}

func sampleOutcomes() []music2bb.MatchResult {
	autoVideo := music2bb.Video{BVID: "BV-auto", Title: "中文歌 Official", Uploader: "歌手", Duration: "03:20"}
	reviewVideo := music2bb.Video{BVID: "BV-review", Title: "Latin Song live recording with a deliberately long candidate title", Uploader: "Other", Duration: "04:11"}
	altVideo := music2bb.Video{BVID: "BV-alt", Title: "Latin Song Alternative", Uploader: "Artist", Duration: "04:10"}
	return []music2bb.MatchResult{
		{Song: sampleSongs()[0], Video: &autoVideo, HasSelection: true, Matched: true, Score: 55, KeywordScore: 100, Candidates: []music2bb.MatchResult{{Video: &autoVideo, Score: 55, KeywordScore: 100}}},
		{Song: sampleSongs()[1], NeedsReview: true, ReviewReason: music2bb.ReviewAmbiguous, Candidates: []music2bb.MatchResult{
			{Video: &reviewVideo, Score: 40, KeywordScore: 100, QualityScore: 15, OfficialScore: 10, PopularityScore: 8},
			{Video: &altVideo, Score: 37, KeywordScore: 100, QualityScore: 5, OfficialScore: 5, PopularityScore: 4},
		}},
	}
}
