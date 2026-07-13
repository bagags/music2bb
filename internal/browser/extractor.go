package browser

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/proto"
)

const mobileUserAgent = "Mozilla/5.0 (Linux; Android 12; Pixel 6) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/111.0.0.0 Mobile Safari/537.36"

// Extractor uses only the Manager's verified executable. In particular,
// launcher.Bin prevents Rod from silently selecting or downloading a browser.
type Extractor struct {
	Manager     *Manager
	LoadTimeout time.Duration
}

func NewExtractor(manager *Manager) *Extractor {
	return &Extractor{Manager: manager, LoadTimeout: 90 * time.Second}
}

func (e *Extractor) Extract(ctx context.Context, rawURL string) ([]model.Song, error) {
	if e == nil || e.Manager == nil {
		return nil, &Error{Kind: ErrorNotInstalled, Op: "extract", Err: errors.New("browser manager is not configured")}
	}
	executable, err := e.Manager.Executable(ctx)
	if err != nil {
		return nil, err
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
		return nil, &Error{Kind: ErrorLaunch, Op: "launch", Err: err}
	}
	defer process.Cleanup()
	defer process.Kill()

	browser := rod.New().Context(ctx).ControlURL(controlURL)
	if err := browser.Connect(); err != nil {
		return nil, &Error{Kind: ErrorLaunch, Op: "connect", Err: err}
	}
	defer browser.Close()
	page, err := browser.Page(proto.TargetCreateTarget{})
	if err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "new page", Err: err}
	}
	defer page.Close()
	if err := page.SetUserAgent(&proto.NetworkSetUserAgentOverride{UserAgent: mobileUserAgent}); err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "set user agent", Err: err}
	}
	if err := page.SetViewport(&proto.EmulationSetDeviceMetricsOverride{
		Width: 390, Height: 844, DeviceScaleFactor: 1, Mobile: true,
	}); err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "set viewport", Err: err}
	}
	if err := page.Navigate(rawURL); err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "navigate", Err: err}
	}
	if err := page.WaitLoad(); err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "wait load", Err: err}
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
			return nil, err
		}
	}

	result, err := page.Eval(extractSongsJS)
	if err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "evaluate", Err: err}
	}
	songs, err := decodeBrowserSongs(result.Value.Str())
	if err != nil {
		return nil, &Error{Kind: ErrorExtraction, Op: "decode", Err: err}
	}
	if len(songs) == 0 {
		return nil, &Error{Kind: ErrorExtraction, Op: "extract", Err: errors.New("dynamic page contained no songs")}
	}
	return songs, nil
}

type browserSong struct {
	Name   string `json:"name"`
	Artist string `json:"artist"`
}

func decodeBrowserSongs(payload string) ([]model.Song, error) {
	var raw []browserSong
	if err := json.Unmarshal([]byte(payload), &raw); err != nil {
		return nil, err
	}
	songs := make([]model.Song, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for _, item := range raw {
		name := strings.TrimSpace(item.Name)
		artist := strings.TrimSpace(item.Artist)
		if name == "" || isNonSongText(name) {
			continue
		}
		key := name + "|" + artist
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		songs = append(songs, model.Song{Name: name, Artist: artist})
	}
	return songs, nil
}

func isNonSongText(text string) bool {
	for _, skipped := range []string{
		"全部", "播放", "VIP", "收藏", "歌单", "分享", "下载", "评论", "首歌曲",
		"正在加载", "加载中", "Loading", "暂无", "没有更多", "已到底",
	} {
		if strings.Contains(text, skipped) {
			return true
		}
	}
	return false
}

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

const extractSongsJS = `() => {
  const results = [];
  const seen = new Set();
  const add = (name, artist) => {
    name = String(name || '').trim();
    artist = String(artist || '').trim();
    if (!name || name.length >= 100) return;
    const key = name + '|' + artist;
    if (!seen.has(key)) {
      seen.add(key);
      results.push({name, artist});
    }
  };
  const walk = (value, depth = 0, visited = new Set()) => {
    if (!value || typeof value !== 'object' || depth > 8 || visited.has(value)) return;
    visited.add(value);
    if (Array.isArray(value)) {
      for (const item of value) {
        if (item && typeof item === 'object') {
          let name = item.songname || item.name || item.title || item.songName || item.filename || item.FileName;
          let artist = item.singername || item.author || item.artist || item.singerName;
          if (!artist && Array.isArray(item.singerinfo)) {
            artist = item.singerinfo.map((singer) => singer && singer.name).filter(Boolean).join('、');
          }
          if ((item.filename || item.FileName || item.singerinfo) && String(name || '').includes(' - ')) {
            const parts = String(name).split(' - ');
            if (!artist) artist = parts.shift().trim(); else parts.shift();
            name = parts.join(' - ').trim();
          }
          add(name, artist);
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
    let name = firstText(['[class*="song-name"]', '[class*="songName"]', '.songname', '.song_name', '[class*="title"]', '.name', '[class*="name"]', 'h3', 'h4']);
    let artist = firstText(['[class*="singer"]', '[class*="artist"]', '.singername', '.singer_name', '[class*="author"]', '.artist', '[class*="singerName"]']);
    if (!name) {
      const text = (item.innerText || '').trim();
      if (text.includes(' - ')) {
        const parts = text.split(' - ');
        name = parts.shift().trim();
        artist = parts.join(' - ').trim();
      }
    }
    add(name, artist);
  }
  return JSON.stringify(results);
}`
