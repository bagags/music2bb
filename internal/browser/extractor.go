package browser

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
	"github.com/bagags/music2bb-go/internal/playlist"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const mobileUserAgent = "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Mobile Safari/537.36"

const navigationRetryDelay = 100 * time.Millisecond

// Extractor uses only the Manager's verified executable. In particular,
// launcher.Bin prevents Rod from silently selecting or downloading a browser.
type Extractor struct {
	Manager     *Manager
	LoadTimeout time.Duration
}

func NewExtractor(manager *Manager) *Extractor {
	return &Extractor{Manager: manager, LoadTimeout: 90 * time.Second}
}

// Available reports whether a selected system browser or checksum-verified
// managed browser is ready. It never installs or launches Chromium.
func (e *Extractor) Available(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if e == nil || e.Manager == nil {
		return false, nil
	}
	status, err := e.Manager.Status(ctx)
	if err != nil {
		return false, err
	}
	return status.Installed, nil
}

// EnsureAvailable performs the same read-only availability check. Downloading
// remains an explicit manager or CLI operation.
func (e *Extractor) EnsureAvailable(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if e == nil || e.Manager == nil {
		return false, nil
	}
	return e.Available(ctx)
}

// ExtractPlaylist extracts ordered provider-neutral track candidates using
// only the manager's verified Chromium executable.
func (e *Extractor) ExtractPlaylist(ctx context.Context, source playlist.Source) (playlist.RawResult, error) {
	return e.extractPlaylist(ctx, source.String())
}

// Extract preserves the legacy song-returning browser boundary while wiring
// migrates to ExtractPlaylist. New orchestration should use ExtractPlaylist so
// provider title capabilities can inspect the original source fields.
func (e *Extractor) Extract(ctx context.Context, rawURL string) ([]model.Song, error) {
	source, err := playlist.ParseSource(rawURL)
	if err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "parse URL", Err: err}
	}
	result, err := e.ExtractPlaylist(ctx, source)
	songs := playlist.DecodeTracks(result.Tracks, nil)
	if err == nil && len(songs) == 0 {
		err = &Error{Kind: ErrorExtraction, Op: "extract", Err: errors.New("dynamic page contained no songs")}
	}
	return songs, err
}

func (e *Extractor) extractPlaylist(ctx context.Context, rawURL string) (playlist.RawResult, error) {
	if e == nil || e.Manager == nil {
		return playlist.RawResult{}, &Error{Kind: ErrorNotInstalled, Op: "extract", Err: errors.New("browser manager is not configured")}
	}
	executable, err := e.Manager.Executable(ctx)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return playlist.RawResult{}, ctxErr
		}
		return playlist.RawResult{}, err
	}
	timeout := e.LoadTimeout
	if timeout <= 0 {
		timeout = 90 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	process := launcher.New().
		Context(ctx).
		Bin(executable).
		Headless(true).
		Leakless(true)
	controlURL, err := process.Launch()
	if err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorLaunch, "launch", err)
	}
	defer process.Cleanup()
	defer process.Kill()

	browser := rod.New().Context(ctx).ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorLaunch, "connect", err)
	}
	defer browser.Close()
	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "new page", err)
	}
	defer page.Close()
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: mobileUserAgent}); err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "set user agent", err)
	}
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: 390, Height: 844, DeviceScaleFactor: 1, Mobile: true,
	}); err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "set viewport", err)
	}
	if err := navigateWithRetry(ctx, func() error { return page.Navigate(rawURL) }); err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "navigate", err)
	}
	if err := page.WaitLoad(); err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "wait load", err)
	}
	// Best effort: pages with a permanent analytics connection never become
	// idle, so failure here must not suppress DOM extraction.
	_ = page.WaitIdle(2 * time.Second)
	_, _ = page.Eval(clickExpandJS)

	lastHeight := ""
	stale := 0
	for index := 0; index < 50; index++ {
		result, evalErr := page.Eval(scrollJS)
		if evalErr != nil {
			break
		}
		height := result.Value.Str()
		if height == lastHeight {
			stale++
			if stale >= 3 {
				break
			}
		} else {
			lastHeight = height
			stale = 0
		}
		if err := waitContext(ctx, 250*time.Millisecond); err != nil {
			return playlist.RawResult{}, err
		}
	}

	result, err := page.Eval(extractTracksJS)
	if err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "evaluate", err)
	}
	raw, err := decodeBrowserResult(result.Value.Str())
	if err != nil {
		return playlist.RawResult{}, browserOperationError(ctx, ErrorExtraction, "decode", err)
	}
	if len(raw.Tracks) == 0 {
		return playlist.RawResult{}, &Error{Kind: ErrorExtraction, Op: "extract", Err: errors.New("dynamic page contained no track candidates")}
	}
	return raw, nil
}

type browserResult struct {
	Tracks        []playlist.TrackCandidate `json:"tracks"`
	ExpectedTotal int                       `json:"expectedTotal"`
}

func decodeBrowserResult(payload string) (playlist.RawResult, error) {
	var decoded browserResult
	if err := json.Unmarshal([]byte(payload), &decoded); err != nil {
		return playlist.RawResult{}, err
	}
	tracks := make([]playlist.TrackCandidate, len(decoded.Tracks))
	for index, candidate := range decoded.Tracks {
		candidate.FilterNonSongText = true
		candidate.MaxTitleLength = 100
		tracks[index] = candidate.Clone()
	}
	return playlist.RawResult{Tracks: tracks, ExpectedTotal: decoded.ExpectedTotal}, nil
}

func browserOperationError(ctx context.Context, kind ErrorKind, operation string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return ctxErr
	}
	return &Error{Kind: kind, Op: operation, Err: err}
}

// Chromium can abort an initial CDP navigation during startup. Retry that
// exact transient once without weakening handling for other network failures.
func navigateWithRetry(ctx context.Context, navigate func() error) error {
	err := navigate()
	if !isTransientNavigationAbort(err) {
		return err
	}
	if err := waitContext(ctx, navigationRetryDelay); err != nil {
		return err
	}
	return navigate()
}

func isTransientNavigationAbort(err error) bool {
	var navigationErr *rod.NavigationError
	return errors.As(err, &navigationErr) && navigationErr.Reason == "net::ERR_ABORTED"
}

var _ playlist.BrowserExtractor = (*Extractor)(nil)

func waitContext(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

const clickExpandJS = `() => {
  const candidates = Array.from(document.querySelectorAll(
    'button, a, .show-all, .showMore, [class*="expand"], [class*="unfold"], [class*="open-all"]'
  ));
  const wanted = /展开|查看全部|^全部$/;
  const button = candidates.find((node) => {
    const text = (node.innerText || '').trim();
    const style = window.getComputedStyle(node);
    return style.display !== 'none' && style.visibility !== 'hidden' &&
      (wanted.test(text) || /show-all|showMore|expand|unfold|open-all/.test(node.className || ''));
  });
  if (button) button.click();
  return Boolean(button);
}`

const scrollJS = `() => {
  window.scrollTo(0, document.body.scrollHeight);
  const buttons = document.querySelectorAll(
    'button[class*="more"], a[class*="more"], [class*="load-more"], [class*="loadMore"], [class*="next-page"], [data-more]'
  );
  for (const button of buttons) {
    const style = window.getComputedStyle(button);
    if (style.display !== 'none' && style.visibility !== 'hidden') button.click();
  }
  return String(document.body.scrollHeight);
}`

const extractTracksJS = `() => {
  const results = [];
  const owns = (value, key) => Object.prototype.hasOwnProperty.call(value, key);
  const scalarText = (value) => {
    if (value === null) return '';
    switch (typeof value) {
      case 'string':
      case 'number':
      case 'boolean':
      case 'bigint':
        return String(value);
      default:
        return '';
    }
  };
  const scalarFields = (item) => {
    const fields = Object.create(null);
    for (const key of Object.keys(item)) {
      const value = item[key];
      if (value === null || ['string', 'number', 'boolean', 'bigint'].includes(typeof value)) {
        fields[key] = scalarText(value);
      }
    }
    return fields;
  };
  const firstPresent = (item, keys) => {
    for (const key of keys) {
      if (owns(item, key)) return {found: true, value: item[key]};
    }
    return {found: false, value: null};
  };
  const nestedArtistNames = (item) => {
    if (!owns(item, 'singerinfo') || item.singerinfo === null) return null;
    if (!Array.isArray(item.singerinfo)) return [];
    return item.singerinfo.map((singer) => {
      if (!singer || typeof singer !== 'object' || !owns(singer, 'name')) return '';
      return scalarText(singer.name);
    });
  };
  const formatDuration = (value) => {
    let seconds;
    if (typeof value === 'number' && Number.isFinite(value)) {
      seconds = Math.trunc(value);
    } else if (typeof value === 'string' && /^[+-]?\d+$/.test(value)) {
      seconds = Number.parseInt(value, 10);
    } else {
      return '';
    }
    if (!Number.isFinite(seconds)) return '';
    if (seconds >= 24 * 60 * 60) seconds = Math.trunc(seconds / 1000);
    let minutes = Math.trunc(seconds / 60);
    let remainder = seconds % 60;
    if (remainder < 0) {
      minutes--;
      remainder += 60;
    }
    return String(minutes) + ':' + String(remainder).padStart(2, '0');
  };
  const addDataCandidate = (item) => {
    const fields = scalarFields(item);
    const titleKeys = ['songname', 'name', 'title', 'songName', 'filename', 'FileName'];
    if (!titleKeys.some((key) => owns(fields, key) && fields[key].trim())) return;

    const albumValue = firstPresent(item, ['album_name', 'albumname', 'album']);
    let album = albumValue.found ? scalarText(albumValue.value).trim() : '';
    if (!album && item.albuminfo && typeof item.albuminfo === 'object') {
      album = scalarText(item.albuminfo.name).trim();
    }
    const durationValue = firstPresent(item, ['duration', 'timelength', 'timelen']);
    const hashValue = firstPresent(item, ['hash', '320hash', 'filehash']);
    results.push({
      fields,
      artistNames: nestedArtistNames(item),
      visibleText: '',
      album,
      duration: durationValue.found ? formatDuration(durationValue.value) : '',
      hash: hashValue.found ? scalarText(hashValue.value) : ''
    });
  };
  const walk = (value, depth = 0, visited = new Set()) => {
    if (!value || typeof value !== 'object' || depth > 8 || visited.has(value)) return;
    visited.add(value);
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item && typeof item === 'object') {
          addDataCandidate(item);
          walk(item, depth + 1, visited);
        }
      }
      return;
    }
    for (const key of ['info', 'songs', 'list', 'songlist', 'songList', 'data', 'playlist', 'tracks']) {
      if (value[key]) walk(value[key], depth + 1, visited);
    }
  };
  for (const globalName of ['songData', 'playlistData', 'listData', 'songsData', 'data', '__INITIAL_STATE__', '__NUXT__', '__NEXT_DATA__']) {
    try {
      let value = window[globalName];
      if (typeof value === 'string') value = JSON.parse(value);
      walk(value);
    } catch (_) {}
  }
  const itemSelectors = [
    '.song-item', '.list_content li', '[class*="songItem"]', '[class*="song-item"]',
    '.music-item', '.track-item', '[class*="trackItem"]', '[class*="musicItem"]',
    'li[data-index]', 'li[data-songid]', '[class*="songRow"]', '[class*="song_row"]',
    '.list-item', '[class*="listItem"]', '[class*="ListItem"]'
  ];
  for (const item of document.querySelectorAll(itemSelectors.join(','))) {
    const firstText = (selectors) => {
      for (const selector of selectors) {
        const node = item.querySelector(selector);
        if (node && node.innerText && node.innerText.trim()) return node.innerText.trim();
      }
      return '';
    };
    const name = firstText(['[class*="song-name"]', '[class*="songName"]', '.songname', '.song_name', '[class*="title"]', '.name', '[class*="name"]', 'h3', 'h4']);
    const artist = firstText(['[class*="singer"]', '[class*="artist"]', '.singername', '.singer_name', '[class*="author"]', '.artist', '[class*="singerName"]']);
    const visibleText = (item.innerText || '').trim();
    if (!name && !visibleText) continue;
    const highConfidence = item.matches([
      '.song-item', '.list_content li', '[class*="songItem"]', '[class*="song-item"]',
      '.music-item', '.track-item', '[class*="trackItem"]', '[class*="musicItem"]',
      'li[data-songid]', '[class*="songRow"]', '[class*="song_row"]'
    ].join(','));
    if (!highConfidence) {
      const songEvidence = item.hasAttribute('data-songid') || Boolean(item.querySelector('a[href*="/song/"]'));
      if (!(name && artist) && !songEvidence) continue;
    }
    const fields = Object.create(null);
    if (name) fields.name = name;
    if (artist) fields.artist = artist;
    results.push({fields, artistNames: null, visibleText, album: '', duration: '', hash: ''});
  }
  return JSON.stringify({tracks: results, expectedTotal: 0});
}`
