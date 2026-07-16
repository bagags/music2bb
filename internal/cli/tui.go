package cli

import (
	"context"
	"errors"
	"fmt"
	"image/color"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	music2bb "github.com/bagags/music2bb-go"
)

type tuiPhase int

const (
	phaseLogin tuiPhase = iota
	phaseParse
	phaseParseFailed
	phaseMatch
	phaseReview
	phaseFavorite
	phaseConfirm
	phaseWrite
	phaseResult
	phaseError
)

type tuiOverlay int

const (
	overlayNone tuiOverlay = iota
	overlaySearch
	overlayManualSongs
	overlayCreateFavorite
	overlayHelp
)

type tuiPhaseMsg struct {
	phase tuiPhase
	text  string
}

type tuiObserverMsg struct{ event music2bb.ProgressEvent }
type tuiAccountMsg struct {
	account music2bb.Account
	err     error
}
type tuiSongsMsg struct {
	songs []music2bb.Song
	err   error
}
type tuiMatchesMsg struct {
	outcomes []music2bb.MatchResult
	err      error
}
type tuiFavoritesMsg struct {
	favorites []music2bb.Favorite
	err       error
}
type tuiSearchMsg struct {
	requestID  uint64
	index      int
	candidates []music2bb.MatchResult
	err        error
}
type tuiFavoriteCreatedMsg struct {
	requestID uint64
	favorite  music2bb.Favorite
	err       error
}
type tuiWriteMsg struct {
	result music2bb.AddResult
	err    error
}
type tuiChannelClosedMsg struct{}
type tuiProgressTickMsg struct{ generation uint64 }

const (
	progressTickInterval = time.Second / 30
	progressQuickIn      = 0.45
	progressSlowOut      = 0.12
)

type tuiController struct {
	session *conversionSession
	ctx     context.Context
	cancel  context.CancelFunc
	events  chan tea.Msg
	started atomic.Bool

	mu     sync.Mutex
	closed bool
	wg     sync.WaitGroup
	once   sync.Once

	searchRequestID uint64
	searchCancel    context.CancelFunc
}

func newTUIController(parent context.Context, session *conversionSession) *tuiController {
	ctx, cancel := context.WithCancel(parent)
	return &tuiController{session: session, ctx: ctx, cancel: cancel, events: make(chan tea.Msg, 256)}
}

func (c *tuiController) command(work func()) tea.Cmd {
	return func() tea.Msg {
		c.mu.Lock()
		if c.closed {
			c.mu.Unlock()
			return nil
		}
		c.wg.Add(1)
		c.mu.Unlock()
		defer c.wg.Done()
		work()
		return nil
	}
}

func (c *tuiController) send(msg tea.Msg) bool {
	select {
	case c.events <- msg:
		return true
	case <-c.ctx.Done():
		return false
	}
}

func (c *tuiController) sendFinal(msg tea.Msg) {
	for {
		select {
		case c.events <- msg:
			return
		default:
			// A final result must not be lost behind stale progress. Dropping the
			// oldest buffered update also keeps shutdown non-blocking if the
			// renderer has already stopped consuming events.
			select {
			case <-c.events:
			default:
				continue
			}
		}
	}
}

func (c *tuiController) observer() music2bb.Observer {
	return music2bb.ObserverFunc(func(event music2bb.ProgressEvent) {
		c.send(tuiObserverMsg{event: event})
	})
}

func (c *tuiController) startCmd() tea.Cmd {
	return c.command(func() {
		c.started.Store(true)
		c.prepare(true, nil)
	})
}

func (c *tuiController) retryCmd() tea.Cmd {
	return c.command(func() { c.prepare(false, nil) })
}

func (c *tuiController) manualSongsCmd(songs []music2bb.Song) tea.Cmd {
	return c.command(func() { c.prepare(false, songs) })
}

func (c *tuiController) prepare(includeLogin bool, manualSongs []music2bb.Song) {
	observer := c.observer()
	if includeLogin {
		if !c.send(tuiPhaseMsg{phase: phaseLogin, text: "正在登录 Bilibili"}) {
			return
		}
		account, err := c.session.login(c.ctx, observer)
		if !c.send(tuiAccountMsg{account: account, err: err}) || err != nil {
			return
		}
	}
	var songs []music2bb.Song
	var err error
	if manualSongs == nil {
		if !c.send(tuiPhaseMsg{phase: phaseParse, text: "正在解析歌单"}) {
			return
		}
		songs, err = c.session.parse(c.ctx, observer)
		if !c.send(tuiSongsMsg{songs: songs, err: err}) || err != nil {
			return
		}
	} else {
		songs = append([]music2bb.Song(nil), manualSongs...)
		if !c.send(tuiSongsMsg{songs: songs}) {
			return
		}
	}
	if !c.send(tuiPhaseMsg{phase: phaseMatch, text: "正在匹配候选视频"}) {
		return
	}
	outcomes, matchErr := c.session.match(c.ctx, songs, observer)
	c.send(tuiMatchesMsg{outcomes: outcomes, err: matchErr})
}

func (c *tuiController) waitCmd() tea.Cmd {
	return func() tea.Msg {
		msg, ok := <-c.events
		if !ok {
			return tuiChannelClosedMsg{}
		}
		return msg
	}
}

func (c *tuiController) favoritesCmd() tea.Cmd {
	return c.command(func() {
		favorites, err := c.session.favorites(c.ctx)
		c.send(tuiFavoritesMsg{favorites: favorites, err: err})
	})
}

func (c *tuiController) searchCmd(requestID uint64, index int, song music2bb.Song, query string) tea.Cmd {
	ctx, cancel := context.WithCancel(c.ctx)
	c.mu.Lock()
	previousCancel := c.searchCancel
	c.searchRequestID = requestID
	c.searchCancel = cancel
	c.mu.Unlock()
	if previousCancel != nil {
		previousCancel()
	}
	return c.command(func() {
		defer c.finishSearch(requestID, cancel)
		candidates, err := c.session.search(ctx, song, query)
		c.send(tuiSearchMsg{requestID: requestID, index: index, candidates: candidates, err: err})
	})
}

func (c *tuiController) finishSearch(requestID uint64, cancel context.CancelFunc) {
	c.mu.Lock()
	if c.searchRequestID == requestID {
		c.searchRequestID = 0
		c.searchCancel = nil
	}
	c.mu.Unlock()
	cancel()
}

func (c *tuiController) cancelSearch(requestID uint64) {
	c.mu.Lock()
	if c.searchRequestID != requestID {
		c.mu.Unlock()
		return
	}
	cancel := c.searchCancel
	c.searchRequestID = 0
	c.searchCancel = nil
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (c *tuiController) createFavoriteCmd(requestID uint64, request music2bb.CreateFavoriteRequest) tea.Cmd {
	return c.command(func() {
		favorite, err := c.session.createFavorite(c.ctx, request)
		c.send(tuiFavoriteCreatedMsg{requestID: requestID, favorite: favorite, err: err})
	})
}

func (c *tuiController) writeCmd(favoriteID int64, outcomes []music2bb.MatchResult) tea.Cmd {
	return c.command(func() {
		result, err := c.session.write(c.ctx, favoriteID, outcomes, c.observer())
		c.sendFinal(tuiWriteMsg{result: result, err: err})
	})
}

func (c *tuiController) close() {
	c.once.Do(func() {
		c.mu.Lock()
		c.closed = true
		c.cancel()
		c.mu.Unlock()
		c.wg.Wait()
		close(c.events)
	})
}

type tuiModel struct {
	controller *tuiController
	options    convertOptions
	phase      tuiPhase
	phaseText  string
	validation string
	receipt    string
	exitCode   int

	width        int
	height       int
	dark         bool
	colorEnabled bool
	compactPane  int
	showHelp     bool

	account    music2bb.Account
	songs      []music2bb.Song
	outcomes   []music2bb.MatchResult
	processed  []bool
	confirmed  []bool
	skipped    []bool
	matchDone  int
	songCursor int
	candCursor int

	progressValue      float64
	progressTarget     float64
	progressVisible    bool
	progressExiting    bool
	progressTicking    bool
	progressGeneration uint64
	progressExpanded   bool

	favorites        []music2bb.Favorite
	favoriteCursor   int
	selectedFavorite music2bb.Favorite
	writeResult      music2bb.AddResult

	overlay       tuiOverlay
	input         textinput.Model
	manualInput   textarea.Model
	searchIndex   int
	createPrivate bool
	busy          bool
	qr            string

	nextOverlayRequest     uint64
	searchRequestID        uint64
	favoriteRequestID      uint64
	favoriteRequestVisible bool
	favoritePending        bool
}

func newTUIModel(controller *tuiController) tuiModel {
	input := textinput.New()
	input.Prompt = "搜索: "
	input.Placeholder = "歌名、歌手或 BV 号"
	input.SetWidth(56)
	manual := textarea.New()
	manual.Placeholder = "每行：歌名 - 歌手"
	manual.SetWidth(60)
	manual.SetHeight(10)
	return tuiModel{
		controller:    controller,
		options:       controller.session.options,
		phase:         phaseLogin,
		phaseText:     "准备全屏工作区",
		exitCode:      ExitSuccess,
		dark:          true,
		colorEnabled:  true,
		createPrivate: true,
		input:         input,
		manualInput:   manual,
	}
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(m.controller.startCmd(), m.controller.waitCmd(), tea.RequestBackgroundColor)
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case tea.BackgroundColorMsg:
		m.dark = msg.IsDark()
		return m, nil
	case tuiChannelClosedMsg:
		return m, nil
	case tuiProgressTickMsg:
		return m.updateProgress(msg)
	case tuiPhaseMsg:
		m.phase, m.phaseText = msg.phase, msg.text
		var cmd tea.Cmd
		if msg.phase == phaseMatch {
			cmd = m.beginProgress(false)
		}
		return m, tea.Batch(cmd, m.controller.waitCmd())
	case tuiObserverMsg:
		m.applyObserver(msg.event)
		return m, tea.Batch(m.animateProgress(), m.controller.waitCmd())
	case tuiAccountMsg:
		if msg.err != nil {
			m.phase = phaseError
			m.validation = "登录失败: " + msg.err.Error()
			m.exitCode = exitFor(msg.err)
		} else {
			m.account = msg.account
		}
		return m, m.controller.waitCmd()
	case tuiSongsMsg:
		if msg.err != nil {
			m.phase = phaseParseFailed
			m.validation = "解析失败: " + msg.err.Error()
			m.exitCode = exitFor(msg.err)
		} else {
			m.songs = append([]music2bb.Song(nil), msg.songs...)
			m.outcomes = make([]music2bb.MatchResult, len(m.songs))
			m.processed = make([]bool, len(m.songs))
			m.confirmed = make([]bool, len(m.songs))
			m.skipped = make([]bool, len(m.songs))
			m.matchDone = 0
			m.validation = ""
		}
		return m, m.controller.waitCmd()
	case tuiMatchesMsg:
		progressCmd := m.endProgress(true)
		m.outcomes = cloneMatchResults(msg.outcomes)
		m.ensureReviewState()
		for index := range m.processed {
			m.processed[index] = true
		}
		m.matchDone = len(m.songs)
		if msg.err != nil {
			if errors.Is(msg.err, context.Canceled) || exitFor(msg.err) == ExitCancelled {
				m.exitCode = ExitCancelled
				m.receipt = "转换已取消；未写入任何视频。"
				return m, tea.Quit
			}
			m.validation = "部分搜索失败；失败歌曲仍需选择或跳过。"
		}
		m.phase = phaseReview
		m.phaseText = "审核匹配结果"
		m.focusFirstUnresolved()
		return m, tea.Batch(progressCmd, m.controller.waitCmd())
	case tuiFavoritesMsg:
		cmd := m.applyFavorites(msg)
		return m, tea.Batch(cmd, m.controller.waitCmd())
	case tuiSearchMsg:
		if msg.requestID == 0 || msg.requestID != m.searchRequestID {
			return m, m.controller.waitCmd()
		}
		m.searchRequestID = 0
		progressCmd := m.endProgress(false)
		m.busy = false
		m.overlay = overlayNone
		m.input.Blur()
		if msg.err != nil {
			m.validation = "手动搜索失败: " + msg.err.Error()
		} else if msg.index >= 0 && msg.index < len(m.outcomes) {
			m.outcomes[msg.index].Candidates = cloneMatchResults(msg.candidates)
			m.outcomes[msg.index].NeedsReview = true
			m.outcomes[msg.index].ReviewReason = music2bb.ReviewArtistUnverified
			m.candCursor = 0
			m.validation = fmt.Sprintf("手动搜索返回 %d 个候选；按 Enter 接受。", len(msg.candidates))
		}
		return m, tea.Batch(progressCmd, m.controller.waitCmd())
	case tuiFavoriteCreatedMsg:
		if msg.requestID == 0 || msg.requestID != m.favoriteRequestID {
			return m, m.controller.waitCmd()
		}
		m.favoriteRequestID = 0
		m.favoritePending = false
		visible := m.favoriteRequestVisible && m.overlay == overlayCreateFavorite
		m.favoriteRequestVisible = false
		if visible {
			m.busy = false
			m.overlay = overlayNone
			m.input.Blur()
		}
		if msg.err != nil {
			m.validation = "创建收藏夹失败: " + msg.err.Error()
			return m, m.controller.waitCmd()
		}
		m.favorites = append(m.favorites, msg.favorite)
		if !visible {
			m.validation = fmt.Sprintf("已创建收藏夹「%s」。", msg.favorite.Title)
			return m, m.controller.waitCmd()
		}
		m.selectedFavorite = msg.favorite
		if m.options.yes {
			cmd := m.beginWrite()
			return m, tea.Batch(cmd, m.controller.waitCmd())
		}
		m.phase = phaseConfirm
		m.validation = ""
		return m, m.controller.waitCmd()
	case tuiWriteMsg:
		m.writeResult = msg.result
		m.phase = phaseResult
		m.exitCode = ExitSuccess
		if msg.err != nil {
			m.exitCode = exitFor(msg.err)
		}
		m.receipt = m.buildReceipt(msg.result, msg.err)
		return m, tea.Quit
	}

	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return m, nil
	}
	keystroke := key.Keystroke()
	if keystroke == "ctrl+c" {
		if m.phase == phaseWrite {
			m.controller.cancel()
			m.validation = "正在停止剩余写入；已完成项目会保留。"
			return m, nil
		}
		m.controller.cancel()
		m.exitCode = ExitCancelled
		m.receipt = "转换已取消；未开始写入。"
		return m, tea.Quit
	}
	if m.overlay != overlayNone {
		return m.updateOverlay(key)
	}
	if keystroke == "q" {
		if m.phase == phaseWrite {
			m.controller.cancel()
			m.validation = "正在停止剩余写入；已完成项目会保留。"
			return m, nil
		}
		m.controller.cancel()
		m.exitCode = ExitCancelled
		m.receipt = "转换已取消；未开始写入。"
		return m, tea.Quit
	}
	if keystroke == "?" {
		m.overlay = overlayHelp
		return m, nil
	}

	switch m.phase {
	case phaseParseFailed:
		switch keystroke {
		case "r":
			m.validation = ""
			return m, m.controller.retryCmd()
		case "m":
			m.overlay = overlayManualSongs
			m.manualInput.SetValue("")
			return m, m.manualInput.Focus()
		}
	case phaseError:
		if keystroke == "r" {
			m.validation = ""
			return m, m.controller.startCmd()
		}
	case phaseReview:
		return m.updateReview(keystroke)
	case phaseFavorite:
		return m.updateFavorite(keystroke)
	case phaseConfirm:
		if keystroke == "c" || keystroke == "enter" {
			return m, m.beginWrite()
		}
		if keystroke == "b" {
			m.phase = phaseFavorite
		}
	}
	return m, nil
}

func (m *tuiModel) applyObserver(event music2bb.ProgressEvent) {
	if event.Message != "" {
		m.phaseText = event.Message
	}
	if event.Kind == music2bb.EventWarning {
		m.validation = event.Message
	}
	if event.Kind == music2bb.EventQR {
		m.qr = renderQR(event.QRPayload)
	}
	if event.Kind == music2bb.EventSong && event.Operation == "match" {
		m.matchDone = event.Current
		if len(m.songs) > 0 {
			m.progressTarget = max(0, min(1, float64(event.Current)/float64(len(m.songs))))
		}
		if event.Song != nil {
			for index, song := range m.songs {
				if !m.processed[index] && song.Name == event.Song.Name && song.Artist == event.Song.Artist {
					m.processed[index] = true
					break
				}
			}
		}
	}
	if event.Kind == music2bb.EventVideo && event.Operation == "add_favorite" && event.Message != "" {
		m.writeResult.Succeeded = append(m.writeResult.Succeeded, event.Message)
	}
}

func (m *tuiModel) beginProgress(indeterminate bool) tea.Cmd {
	m.progressGeneration++
	m.progressTicking = false
	m.progressVisible = true
	m.progressExiting = false
	m.progressValue = 0
	m.progressTarget = 0
	m.progressExpanded = indeterminate
	if indeterminate {
		m.progressTarget = 0.82
	} else if len(m.songs) > 0 {
		m.progressTarget = max(0, min(1, float64(m.matchDone)/float64(len(m.songs))))
	}
	return m.animateProgress()
}

func (m *tuiModel) endProgress(completed bool) tea.Cmd {
	if !m.progressVisible {
		return nil
	}
	if completed {
		m.progressValue = 1
	} else if m.progressValue < 0.2 {
		m.progressValue = 0.2
	}
	m.progressTarget = 0
	m.progressExiting = true
	return m.animateProgress()
}

func (m *tuiModel) animateProgress() tea.Cmd {
	if !m.progressVisible || m.progressTicking {
		return nil
	}
	activeSearch := m.searchRequestID != 0
	activeMatch := m.phase == phaseMatch
	if !m.progressExiting && !activeSearch && (!activeMatch || math.Abs(m.progressTarget-m.progressValue) < 0.005) {
		return nil
	}
	m.progressTicking = true
	generation := m.progressGeneration
	return tea.Tick(progressTickInterval, func(time.Time) tea.Msg {
		return tuiProgressTickMsg{generation: generation}
	})
}

func (m tuiModel) updateProgress(msg tuiProgressTickMsg) (tea.Model, tea.Cmd) {
	if msg.generation != m.progressGeneration {
		return m, nil
	}
	m.progressTicking = false
	if !m.progressVisible {
		return m, nil
	}
	rate := progressQuickIn
	if m.progressExiting {
		rate = progressSlowOut
	}
	m.progressValue += (m.progressTarget - m.progressValue) * rate

	if m.progressExiting {
		if m.progressValue <= 0.01 {
			m.progressValue = 0
			m.progressVisible = false
			m.progressExiting = false
			m.progressGeneration++
			return m, nil
		}
		return m, m.animateProgress()
	}
	if m.searchRequestID != 0 {
		if math.Abs(m.progressTarget-m.progressValue) < 0.01 {
			m.progressValue = m.progressTarget
			m.progressExpanded = !m.progressExpanded
			if m.progressExpanded {
				m.progressTarget = 0.82
			} else {
				m.progressTarget = 0.18
			}
		}
		return m, m.animateProgress()
	}
	if math.Abs(m.progressTarget-m.progressValue) < 0.005 {
		m.progressValue = m.progressTarget
		return m, nil
	}
	return m, m.animateProgress()
}

func (m *tuiModel) ensureReviewState() {
	if len(m.confirmed) != len(m.outcomes) {
		m.confirmed = make([]bool, len(m.outcomes))
	}
	if len(m.skipped) != len(m.outcomes) {
		m.skipped = make([]bool, len(m.outcomes))
	}
	if len(m.processed) != len(m.outcomes) {
		m.processed = make([]bool, len(m.outcomes))
	}
}

func (m *tuiModel) updateReview(key string) (tea.Model, tea.Cmd) {
	if len(m.outcomes) == 0 {
		return *m, nil
	}
	switch key {
	case "left", "h":
		m.moveSong(-1)
	case "right", "l":
		m.moveSong(1)
	case "up", "k":
		m.moveCandidate(-1)
	case "down", "j":
		m.moveCandidate(1)
	case "tab":
		m.nextUnresolved()
		if m.width < 80 {
			m.compactPane = 1 - m.compactPane
		}
	case "enter":
		m.acceptCandidate()
	case "s":
		m.overlay = overlaySearch
		m.searchIndex = m.songCursor
		query := m.outcomes[m.songCursor].Song.SearchKeywordFull()
		m.input.Prompt = "搜索: "
		m.input.SetValue(query)
		m.input.CursorEnd()
		return *m, m.input.Focus()
	case "x":
		m.skipCurrent()
	case "u":
		m.clearCurrent()
	case "c":
		if unresolved := m.unresolvedCount(); unresolved > 0 {
			m.validation = fmt.Sprintf("仍有 %d 首歌曲需要选择或跳过。", unresolved)
			m.focusFirstUnresolved()
			return *m, nil
		}
		if selected, skipped := m.selectionCounts(); selected == 0 {
			m.phase = phaseResult
			m.exitCode = ExitNoMatches
			m.receipt = fmt.Sprintf("未写入任何视频；已跳过 %d 首歌曲。", skipped)
			return *m, tea.Quit
		}
		m.phase = phaseFavorite
		m.phaseText = "选择目标收藏夹"
		m.validation = "正在加载收藏夹…"
		return *m, m.controller.favoritesCmd()
	}
	return *m, nil
}

func (m *tuiModel) updateFavorite(key string) (tea.Model, tea.Cmd) {
	switch key {
	case "up", "k":
		if len(m.favorites) > 0 {
			m.favoriteCursor = (m.favoriteCursor - 1 + len(m.favorites)) % len(m.favorites)
		}
	case "down", "j":
		if len(m.favorites) > 0 {
			m.favoriteCursor = (m.favoriteCursor + 1) % len(m.favorites)
		}
	case "enter":
		if len(m.favorites) == 0 {
			m.validation = "没有可用收藏夹；按 n 新建。"
			return *m, nil
		}
		m.selectedFavorite = m.favorites[m.favoriteCursor]
		if m.options.yes {
			return *m, m.beginWrite()
		}
		m.phase = phaseConfirm
		m.validation = ""
	case "n":
		m.overlay = overlayCreateFavorite
		m.busy = false
		m.createPrivate = true
		m.input.Prompt = "名称: "
		m.input.Placeholder = "新收藏夹"
		m.input.SetValue("")
		return *m, m.input.Focus()
	case "r":
		m.validation = "正在重新加载收藏夹…"
		return *m, m.controller.favoritesCmd()
	}
	return *m, nil
}

func (m tuiModel) updateOverlay(key tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	stroke := key.Keystroke()
	if stroke == "esc" {
		var progressCmd tea.Cmd
		if m.overlay == overlaySearch && m.searchRequestID != 0 {
			m.controller.cancelSearch(m.searchRequestID)
			m.searchRequestID = 0
			progressCmd = m.endProgress(false)
		}
		if m.overlay == overlayCreateFavorite && m.favoritePending {
			m.favoriteRequestVisible = false
		}
		m.overlay = overlayNone
		m.input.Blur()
		m.manualInput.Blur()
		m.busy = false
		return m, progressCmd
	}
	switch m.overlay {
	case overlayHelp:
		if stroke == "?" || stroke == "enter" {
			m.overlay = overlayNone
		}
		return m, nil
	case overlaySearch:
		if stroke == "enter" && !m.busy {
			query := strings.TrimSpace(m.input.Value())
			if query == "" {
				m.validation = "搜索词不能为空。"
				return m, nil
			}
			m.busy = true
			m.validation = "正在手动搜索…"
			m.nextOverlayRequest++
			m.searchRequestID = m.nextOverlayRequest
			return m, tea.Batch(
				m.beginProgress(true),
				m.controller.searchCmd(m.searchRequestID, m.searchIndex, m.outcomes[m.searchIndex].Song, query),
			)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd
	case overlayCreateFavorite:
		if stroke == "ctrl+p" {
			m.createPrivate = !m.createPrivate
			return m, nil
		}
		if stroke == "enter" && m.favoritePending {
			m.validation = "收藏夹仍在创建中，请等待结果。"
			return m, nil
		}
		if stroke == "enter" && !m.busy {
			title := strings.TrimSpace(m.input.Value())
			if title == "" {
				m.validation = "收藏夹名称不能为空。"
				return m, nil
			}
			m.busy = true
			m.validation = "正在创建收藏夹…"
			request := music2bb.CreateFavoriteRequest{Title: title, Private: m.createPrivate}
			m.nextOverlayRequest++
			m.favoriteRequestID = m.nextOverlayRequest
			m.favoriteRequestVisible = true
			m.favoritePending = true
			return m, m.controller.createFavoriteCmd(m.favoriteRequestID, request)
		}
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(key)
		return m, cmd
	case overlayManualSongs:
		if stroke == "ctrl+s" {
			songs := parseManualSongText(m.manualInput.Value())
			if len(songs) == 0 {
				m.validation = "请至少输入一首歌曲。"
				return m, nil
			}
			m.overlay = overlayNone
			m.manualInput.Blur()
			m.validation = ""
			return m, m.controller.manualSongsCmd(songs)
		}
		var cmd tea.Cmd
		m.manualInput, cmd = m.manualInput.Update(key)
		return m, cmd
	}
	return m, nil
}

func (m *tuiModel) applyFavorites(msg tuiFavoritesMsg) tea.Cmd {
	if msg.err != nil {
		m.phase = phaseFavorite
		m.validation = "获取收藏夹失败: " + msg.err.Error() + "（按 r 重试）"
		return nil
	}
	m.favorites = append([]music2bb.Favorite(nil), msg.favorites...)
	sort.Slice(m.favorites, func(i, j int) bool { return m.favorites[i].ID < m.favorites[j].ID })
	m.favoriteCursor = 0
	m.validation = ""
	if m.options.favorite != "" {
		favorite, ok := findFavorite(m.favorites, m.options.favorite)
		if !ok {
			m.validation = fmt.Sprintf("未找到收藏夹「%s」；请选择或新建。", m.options.favorite)
			m.phase = phaseFavorite
			return nil
		}
		m.selectedFavorite = favorite
		if m.options.yes {
			return m.beginWrite()
		}
		m.phase = phaseConfirm
		return nil
	}
	m.phase = phaseFavorite
	return nil
}

func (m *tuiModel) beginWrite() tea.Cmd {
	m.phase = phaseWrite
	m.phaseText = "正在写入收藏夹"
	m.validation = "按 q 可停止剩余写入；已成功项目会保留。"
	return m.controller.writeCmd(m.selectedFavorite.ID, cloneMatchResults(m.outcomes))
}

func (m *tuiModel) moveSong(delta int) {
	if len(m.outcomes) == 0 {
		return
	}
	m.songCursor = (m.songCursor + delta + len(m.outcomes)) % len(m.outcomes)
	m.candCursor = m.recommendedCandidate(m.songCursor)
}

func (m *tuiModel) moveCandidate(delta int) {
	if m.songCursor < 0 || m.songCursor >= len(m.outcomes) {
		return
	}
	candidates := m.outcomes[m.songCursor].Candidates
	if len(candidates) == 0 {
		return
	}
	m.candCursor = (m.candCursor + delta + len(candidates)) % len(candidates)
}

func (m *tuiModel) acceptCandidate() {
	if m.songCursor < 0 || m.songCursor >= len(m.outcomes) {
		return
	}
	outcome := &m.outcomes[m.songCursor]
	if m.candCursor < 0 || m.candCursor >= len(outcome.Candidates) || outcome.Candidates[m.candCursor].Video == nil {
		m.validation = "当前歌曲没有可接受的候选；请搜索或跳过。"
		return
	}
	candidates := cloneMatchResults(outcome.Candidates)
	selected := candidates[m.candCursor]
	selected.Song = outcome.Song
	selected.HasSelection = true
	selected.Matched = true
	selected.ManualOverride = true
	selected.NeedsReview = false
	selected.ReviewReason = music2bb.ReviewNone
	selected.Candidates = candidates
	*outcome = selected
	m.confirmed[m.songCursor] = true
	m.skipped[m.songCursor] = false
	m.validation = "已接受当前候选。"
}

func (m *tuiModel) skipCurrent() {
	if m.songCursor < 0 || m.songCursor >= len(m.outcomes) {
		return
	}
	m.skipped[m.songCursor] = true
	m.confirmed[m.songCursor] = false
	m.outcomes[m.songCursor].HasSelection = false
	m.outcomes[m.songCursor].Video = nil
	m.outcomes[m.songCursor].NeedsReview = false
	m.validation = "已跳过当前歌曲。"
	m.nextUnresolved()
}

func (m *tuiModel) clearCurrent() {
	if m.songCursor < 0 || m.songCursor >= len(m.outcomes) {
		return
	}
	m.skipped[m.songCursor] = false
	m.confirmed[m.songCursor] = false
	m.outcomes[m.songCursor].HasSelection = false
	m.outcomes[m.songCursor].Video = nil
	m.outcomes[m.songCursor].NeedsReview = true
	if m.outcomes[m.songCursor].ReviewReason == music2bb.ReviewNone {
		m.outcomes[m.songCursor].ReviewReason = music2bb.ReviewArtistUnverified
	}
	m.validation = "已清除选择。"
}

func (m *tuiModel) resolved(index int) bool {
	if index < 0 || index >= len(m.outcomes) {
		return true
	}
	if m.skipped[index] {
		return true
	}
	outcome := m.outcomes[index]
	if !outcome.HasSelection || outcome.Video == nil || outcome.NeedsReview {
		return false
	}
	return !m.options.manualReview || m.confirmed[index]
}

func (m *tuiModel) unresolvedCount() int {
	count := 0
	for index := range m.outcomes {
		if !m.resolved(index) {
			count++
		}
	}
	return count
}

func (m *tuiModel) focusFirstUnresolved() {
	for index := range m.outcomes {
		if !m.resolved(index) {
			m.songCursor = index
			m.candCursor = m.recommendedCandidate(index)
			return
		}
	}
	m.songCursor = 0
	m.candCursor = m.recommendedCandidate(0)
}

func (m *tuiModel) nextUnresolved() {
	if len(m.outcomes) == 0 {
		return
	}
	for offset := 1; offset <= len(m.outcomes); offset++ {
		index := (m.songCursor + offset) % len(m.outcomes)
		if !m.resolved(index) {
			m.songCursor = index
			m.candCursor = m.recommendedCandidate(index)
			return
		}
	}
}

func (m *tuiModel) recommendedCandidate(index int) int {
	if index < 0 || index >= len(m.outcomes) {
		return 0
	}
	outcome := m.outcomes[index]
	if outcome.Video != nil {
		for candidateIndex, candidate := range outcome.Candidates {
			if candidate.Video != nil && candidate.Video.BVID == outcome.Video.BVID {
				return candidateIndex
			}
		}
	}
	return 0
}

func (m tuiModel) View() tea.View {
	content := m.render()
	view := tea.NewView(content)
	view.AltScreen = true
	view.WindowTitle = "music2bb conversion workspace"
	return view
}

func (m tuiModel) render() string {
	width, height := m.width, m.height
	if width <= 0 {
		width = 80
	}
	if height <= 0 {
		height = 24
	}
	header := m.renderHeader(width)
	progressHeight := 0
	if m.progressVisible {
		progressHeight = 1
	}
	bodyHeight := height - 3 - progressHeight
	if bodyHeight < 1 {
		bodyHeight = 1
	}
	var body string
	if width < 40 || height < 12 {
		body = lipgloss.Place(width, bodyHeight, lipgloss.Center, lipgloss.Center,
			"终端窗口太小\n请调整到至少 40×12\n仍可按 q 取消")
	} else {
		body = m.renderWorkspace(width, bodyHeight)
	}
	status := m.renderBottomLine(m.validation, width)
	guide := m.renderBottomLine(m.renderFooter(), width)
	content := header + "\n" + body + "\n" + status + "\n" + guide
	if m.progressVisible {
		content = m.renderProgress(width) + "\n" + content
	}
	return content
}

func (m tuiModel) renderProgress(width int) string {
	width = maxInt(1, width)
	filled := int(math.Round(float64(width) * max(0, min(1, m.progressValue))))
	full, empty := strings.Repeat("━", filled), strings.Repeat("─", width-filled)
	if !m.colorEnabled {
		return full + empty
	}
	fullStyle := lipgloss.NewStyle().Foreground(m.pickColor("#276FBF", "#69A7E7"))
	emptyStyle := lipgloss.NewStyle().Foreground(m.pickColor("#B8C5D1", "#43566A"))
	return fullStyle.Render(full) + emptyStyle.Render(empty)
}

func (m tuiModel) renderBottomLine(text string, width int) string {
	text = strings.ReplaceAll(strings.ReplaceAll(text, "\r", " "), "\n", " ")
	return fixedSize(ansi.TruncateWc(text, width, "…"), width, 1)
}

func (m tuiModel) renderHeader(width int) string {
	automatic, review, skipped, failed := m.counts()
	progress := ""
	if len(m.songs) > 0 {
		progress = fmt.Sprintf(" %d/%d", m.matchDone, len(m.songs))
	}
	text := fmt.Sprintf(" music2bb · %s%s  自动 %d  待审 %d  跳过 %d  失败 %d", m.phaseName(), progress, automatic, review, skipped, failed)
	style := lipgloss.NewStyle().Padding(0, 1)
	if m.colorEnabled {
		style = style.Bold(true).Foreground(m.pickColor("#17324D", "#D7E9FF")).Background(m.pickColor("#DCEEFF", "#20354D"))
	}
	return style.Width(maxInt(1, width)).Render(ansi.TruncateWc(text, maxInt(1, width-2), "…"))
}

func (m tuiModel) renderWorkspace(width, height int) string {
	if m.overlay != overlayNone {
		return m.renderOverlay(width, height)
	}
	if width >= 80 {
		leftWidth := width / 3
		rightWidth := width - leftWidth
		return lipgloss.JoinHorizontal(lipgloss.Top,
			m.renderPane(leftWidth, height, m.songCursor >= 0, m.renderSongs(leftWidth-4, height-2)),
			m.renderPane(rightWidth, height, true, m.renderDetails(rightWidth-4, height-2)),
		)
	}
	content := m.renderSongs(width-4, height-2)
	if m.compactPane == 1 {
		content = m.renderDetails(width-4, height-2)
	}
	return m.renderPane(width, height, true, content)
}

func (m tuiModel) paneStyle(width, height int, active bool) lipgloss.Style {
	borderColor := m.pickColor("#8292A2", "#6F8296")
	if active {
		borderColor = m.pickColor("#276FBF", "#69A7E7")
	}
	style := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1).
		Width(maxInt(1, width)).Height(maxInt(1, height))
	if m.colorEnabled {
		style = style.BorderForeground(borderColor)
	}
	return style
}

func (m tuiModel) renderPane(width, height int, active bool, content string) string {
	style := m.paneStyle(width, height, active)
	content = fixedSize(content, width-style.GetHorizontalFrameSize(), height-style.GetVerticalFrameSize())
	return style.Render(content)
}

func (m tuiModel) renderSongs(width, height int) string {
	if len(m.songs) == 0 {
		if m.qr != "" {
			return "登录二维码\n\n" + m.qr
		}
		return m.phaseText + "\n\n等待歌曲列表…"
	}
	blocks := make([]string, len(m.songs))
	selectedStart := 0
	selectedHeight := 1
	lineOffset := 0
	for index := range m.songs {
		block := m.renderSong(index, width)
		blocks[index] = block
		blockHeight := lipgloss.Height(block)
		if index == m.songCursor {
			selectedStart = lineOffset
			selectedHeight = blockHeight
		}
		lineOffset += blockHeight
	}
	vp := viewport.New(viewport.WithWidth(maxInt(1, width)), viewport.WithHeight(maxInt(1, height)))
	vp.SetContent(strings.Join(blocks, "\n"))
	selectedPadding := maxInt(0, (vp.Height()-selectedHeight)/2)
	vp.SetYOffset(maxInt(0, selectedStart-selectedPadding))
	return vp.View()
}

func (m tuiModel) renderSong(index, width int) string {
	song := m.songs[index]
	selected := index == m.songCursor
	marker := "  "
	if selected {
		marker = "› "
	}
	prefix := fmt.Sprintf("%s%3d %s  ", marker, index+1, m.songStatus(index))
	text := song.Name
	if selected && song.Artist != "" {
		text += " — " + song.Artist
	}

	prefixWidth := lipgloss.Width(prefix)
	wrapped := strings.Split(ansi.WrapWc(text, maxInt(1, width-prefixWidth), ""), "\n")
	continuation := strings.Repeat(" ", prefixWidth)
	for lineIndex := range wrapped {
		if lineIndex == 0 {
			wrapped[lineIndex] = prefix + wrapped[lineIndex]
		} else {
			wrapped[lineIndex] = continuation + wrapped[lineIndex]
		}
	}
	block := strings.Join(wrapped, "\n")
	if selected && m.colorEnabled {
		block = lipgloss.NewStyle().Bold(true).Foreground(m.pickColor("#185F9D", "#8CC8FF")).Render(block)
	}
	return block
}

func (m tuiModel) renderDetails(width, height int) string {
	switch m.phase {
	case phaseLogin, phaseParse, phaseMatch:
		text := m.phaseText
		if m.phase == phaseLogin && m.account.Name != "" {
			text += "\n\n账号: " + m.account.Name
		}
		if m.qr != "" {
			text += "\n\n请使用 Bilibili 客户端扫描：\n" + m.qr
		}
		return text
	case phaseParseFailed:
		return "自动解析未完成\n\n" + m.validation + "\n\n[r] 重试  [m] 手动输入  [q] 取消"
	case phaseError:
		return m.validation + "\n\n[r] 重试  [q] 取消"
	case phaseFavorite:
		return m.renderFavorites()
	case phaseConfirm:
		return m.renderConfirmation()
	case phaseWrite:
		return m.renderWrite()
	case phaseResult:
		return m.receipt
	}
	if m.songCursor < 0 || m.songCursor >= len(m.outcomes) {
		return "等待匹配结果…"
	}
	outcome := m.outcomes[m.songCursor]
	var b strings.Builder
	fmt.Fprintf(&b, "%s — %s\n", outcome.Song.Name, fallback(outcome.Song.Artist, "未知歌手"))
	fmt.Fprintf(&b, "专辑: %s   时长: %s\n", fallback(outcome.Song.Album, "—"), fallback(outcome.Song.Duration, "—"))
	fmt.Fprintf(&b, "状态: %s   审核原因: %s\n\n", m.songStatus(m.songCursor), reviewReasonText(outcome.ReviewReason))
	if len(outcome.Candidates) == 0 {
		b.WriteString("没有候选。按 s 手动搜索，或按 x 跳过。")
		return b.String()
	}
	for index, candidate := range outcome.Candidates {
		marker := "  "
		if index == m.candCursor {
			marker = "▶ "
		}
		if candidate.Video == nil {
			continue
		}
		title := ansi.WrapWc(candidate.Video.Title, maxInt(20, width-8), "")
		fmt.Fprintf(&b, "%s%d. %s\n", marker, index+1, title)
		fmt.Fprintf(&b, "    总分 %.1f  标题 %.1f  歌手 %.1f  质量 %.1f  官方 %.1f  热度 %.1f  UP %.1f\n",
			candidate.Score, candidate.TitleScore, candidate.ArtistScore, candidate.QualityScore, candidate.OfficialScore, candidate.PopularityScore, candidate.UploaderScore)
		fmt.Fprintf(&b, "    UP: %s  时长: %s  %s\n", fallback(candidate.Video.Uploader, "—"), fallback(candidate.Video.Duration, "—"), candidate.Video.BVID)
		fmt.Fprintf(&b, "    %s\n\n", candidate.Video.URL())
	}
	vp := viewport.New(viewport.WithWidth(maxInt(1, width)), viewport.WithHeight(maxInt(1, height)))
	vp.SetContent(strings.TrimRight(b.String(), "\n"))
	return vp.View()
}

func (m tuiModel) renderFavorites() string {
	var b strings.Builder
	b.WriteString("选择收藏夹\n\n")
	if len(m.favorites) == 0 {
		b.WriteString("暂无收藏夹。按 n 新建一个私密收藏夹。")
		return b.String()
	}
	for index, favorite := range m.favorites {
		marker := "  "
		if index == m.favoriteCursor {
			marker = "▶ "
		}
		fmt.Fprintf(&b, "%s%s  (%d)\n", marker, favorite.Title, favorite.MediaCount)
	}
	return b.String()
}

func (m tuiModel) renderConfirmation() string {
	selected, skipped := m.selectionCounts()
	failed := m.failedCount()
	return fmt.Sprintf("确认写入\n\n目标: %s\n已选: %d\n跳过: %d\n搜索失败: %d\n\n[c/Enter] 开始写入  [b] 返回", m.selectedFavorite.Title, selected, skipped, failed)
}

func (m tuiModel) renderWrite() string {
	selected, skipped := m.selectionCounts()
	return fmt.Sprintf("正在写入「%s」\n\n计划添加: %d\n跳过: %d\n已成功: %d\n已失败: %d\n\n%s",
		m.selectedFavorite.Title, selected, skipped, len(m.writeResult.Succeeded), len(m.writeResult.Failed), m.validation)
}

func (m tuiModel) renderOverlay(width, height int) string {
	var title, content, footer string
	switch m.overlay {
	case overlaySearch:
		title = "手动搜索"
		content = m.input.View()
		footer = "Enter 搜索 · Esc 关闭"
		if m.busy {
			footer = "正在搜索…"
		}
	case overlayManualSongs:
		title = "手动输入歌曲"
		content = m.manualInput.View()
		footer = "Ctrl-S 提交 · Esc 关闭"
	case overlayCreateFavorite:
		title = "新建收藏夹"
		visibility := "私密"
		if !m.createPrivate {
			visibility = "公开"
		}
		content = m.input.View() + "\n\n可见性: " + visibility
		footer = "Enter 创建 · Ctrl-P 切换可见性 · Esc 关闭"
	case overlayHelp:
		title = "快捷键"
		content = strings.Join([]string{
			"←/→ 或 h/l  上一首/下一首",
			"↑/↓ 或 k/j  浏览候选",
			"Enter 接受候选   Tab 下一待审（窄屏同时切换窗格）",
			"s 手动搜索   x 跳过   u 清除选择   c 继续",
			"收藏夹: ↑/↓ 选择，n 新建；确认页 c 写入",
			"q 或 Ctrl-C 取消；写入时会保留已成功项目",
		}, "\n")
		footer = "? / Enter / Esc 关闭"
	}
	boxWidth := minInt(maxInt(34, width-8), 70)
	boxHeight := minInt(height, 18)
	box := lipgloss.NewStyle().Border(lipgloss.DoubleBorder()).Padding(1, 2).
		Width(boxWidth).Height(boxHeight)
	if m.colorEnabled {
		box = box.BorderForeground(m.pickColor("#276FBF", "#7DB8F2"))
	}
	body := fixedSize(title+"\n\n"+content+"\n\n"+footer,
		boxWidth-box.GetHorizontalFrameSize(), boxHeight-box.GetVerticalFrameSize())
	return lipgloss.Place(width, height, lipgloss.Center, lipgloss.Center, box.Render(body))
}

func fixedSize(content string, width, height int) string {
	width = maxInt(1, width)
	height = maxInt(1, height)
	return lipgloss.NewStyle().Width(width).Height(height).MaxWidth(width).MaxHeight(height).Render(content)
}

func (m tuiModel) renderFooter() string {
	switch m.phase {
	case phaseReview:
		return " ←/→ 歌曲  ↑/↓ 候选  Enter 接受  Tab 下一待审  s 搜索  x 跳过  u 清除  c 继续  ? 帮助  q 取消"
	case phaseFavorite:
		return " ↑/↓ 选择  Enter 确认  n 新建  r 刷新  ? 帮助  q 取消"
	case phaseConfirm:
		return " c/Enter 写入  b 返回  ? 帮助  q 取消"
	case phaseWrite:
		return " q 停止剩余写入"
	case phaseParseFailed:
		return " r 重试  m 手动输入  q 取消"
	default:
		return " ? 帮助  q 取消"
	}
}

func (m tuiModel) phaseName() string {
	switch m.phase {
	case phaseLogin:
		return "登录"
	case phaseParse:
		return "解析"
	case phaseParseFailed:
		return "解析失败"
	case phaseMatch:
		return "匹配"
	case phaseReview:
		return "审核"
	case phaseFavorite:
		return "收藏夹"
	case phaseConfirm:
		return "确认"
	case phaseWrite:
		return "写入"
	case phaseResult:
		return "结果"
	default:
		return "错误"
	}
}

func (m tuiModel) songStatus(index int) string {
	if index < 0 || index >= len(m.songs) {
		return "待处理"
	}
	if index < len(m.skipped) && m.skipped[index] {
		return "[跳过]"
	}
	if index >= len(m.processed) || !m.processed[index] {
		return "[匹配中]"
	}
	if index >= len(m.outcomes) {
		return "[待处理]"
	}
	outcome := m.outcomes[index]
	if outcome.Failure != nil && len(outcome.Candidates) == 0 {
		return "[失败]"
	}
	if m.resolved(index) {
		if outcome.ManualOverride || (index < len(m.confirmed) && m.confirmed[index]) {
			return "[已选]"
		}
		return "[自动]"
	}
	if outcome.HasSelection && m.options.manualReview {
		return "[待确认]"
	}
	return "[待审]"
}

func (m tuiModel) counts() (automatic, review, skipped, failed int) {
	for index, outcome := range m.outcomes {
		if index < len(m.skipped) && m.skipped[index] {
			skipped++
			continue
		}
		if outcome.Failure != nil {
			failed++
		}
		if index < len(m.processed) && m.processed[index] && outcome.HasSelection && !outcome.ManualOverride && !outcome.NeedsReview {
			automatic++
		}
		if index < len(m.processed) && m.processed[index] && !m.resolved(index) {
			review++
		}
	}
	return
}

func (m tuiModel) selectionCounts() (selected, skipped int) {
	for index, outcome := range m.outcomes {
		if index < len(m.skipped) && m.skipped[index] {
			skipped++
		} else if outcome.HasSelection && outcome.Video != nil {
			selected++
		}
	}
	return
}

func (m tuiModel) failedCount() int {
	count := 0
	for _, outcome := range m.outcomes {
		if outcome.Failure != nil {
			count++
		}
	}
	return count
}

func (m tuiModel) buildReceipt(result music2bb.AddResult, err error) string {
	_, skipped := m.selectionCounts()
	destination := m.selectedFavorite.Title
	if destination == "" {
		destination = strconv.FormatInt(result.FavoriteID, 10)
	}
	receipt := fmt.Sprintf("写入回执 · %s\n成功: %d | 失败: %d | 跳过: %d", destination, len(result.Succeeded), len(result.Failed), skipped)
	if err != nil {
		receipt += "\n状态: " + err.Error()
	}
	for _, failure := range result.Failed {
		receipt += fmt.Sprintf("\n- %s: %s", failure.BVID, failure.Reason)
	}
	return receipt
}

func (m tuiModel) pickColor(light, dark string) color.Color {
	if m.dark {
		return lipgloss.Color(dark)
	}
	return lipgloss.Color(light)
}

func (a *App) runTUI(ctx context.Context, session *conversionSession) (int, bool) {
	controller := newTUIController(ctx, session)
	model := newTUIModel(controller)
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(a.IO.In), tea.WithOutput(a.IO.Out))
	final, err := program.Run()
	controller.close()
	initialized := controller.started.Load()
	if err != nil {
		if !initialized {
			return ExitInternal, false
		}
		if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
			return ExitCancelled, true
		}
		fmt.Fprintf(a.IO.Err, "全屏界面运行失败: %v\n", err)
		return ExitInternal, true
	}
	result, ok := final.(tuiModel)
	if !ok {
		fmt.Fprintln(a.IO.Err, "全屏界面返回了无效状态")
		return ExitInternal, true
	}
	if result.receipt != "" {
		fmt.Fprintln(a.IO.Out, result.receipt)
	}
	return result.exitCode, true
}

func parseManualSongText(value string) []music2bb.Song {
	lines := strings.Split(strings.ReplaceAll(value, "\r\n", "\n"), "\n")
	songs := make([]music2bb.Song, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, " - ", 2)
		song := music2bb.Song{Name: strings.TrimSpace(parts[0])}
		if len(parts) == 2 {
			song.Artist = strings.TrimSpace(parts[1])
		}
		if song.Name != "" {
			songs = append(songs, song)
		}
	}
	return songs
}

func findFavorite(favorites []music2bb.Favorite, selector string) (music2bb.Favorite, bool) {
	if id, ok := parseInt64(selector); ok {
		for _, favorite := range favorites {
			if favorite.ID == id {
				return favorite, true
			}
		}
		return music2bb.Favorite{}, false
	}
	for _, favorite := range favorites {
		if favorite.Title == selector {
			return favorite, true
		}
	}
	return music2bb.Favorite{}, false
}

func cloneMatchResults(source []music2bb.MatchResult) []music2bb.MatchResult {
	result := make([]music2bb.MatchResult, len(source))
	for index, match := range source {
		result[index] = match
		if match.Video != nil {
			video := *match.Video
			video.Tags = append([]string(nil), match.Video.Tags...)
			result[index].Video = &video
		}
		if len(match.Candidates) > 0 {
			result[index].Candidates = cloneMatchResults(match.Candidates)
		}
		if match.Failure != nil {
			failure := *match.Failure
			result[index].Failure = &failure
		}
	}
	return result
}

func reviewReasonText(reason music2bb.ReviewReason) string {
	switch reason {
	case music2bb.ReviewNoCandidates:
		return "没有候选"
	case music2bb.ReviewSearchFailed:
		return "搜索失败"
	case music2bb.ReviewWeakTitle:
		return "标题匹配较弱"
	case music2bb.ReviewArtistUnverified:
		return "歌手未验证"
	case music2bb.ReviewAmbiguous:
		return "候选分数接近"
	default:
		return "无需审核"
	}
}

func fallback(value, replacement string) string {
	if strings.TrimSpace(value) == "" {
		return replacement
	}
	return value
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
