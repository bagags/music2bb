// Package kugou extracts playlist songs through direct HTTP APIs, embedded
// page JSON, and an explicitly injected browser fallback in that order.
package kugou

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/netx"
)

const maxResponseBytes int64 = 16 << 20

const desktopUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

// Extractor is the narrow browser fallback contract. Implementations must not
// prompt or auto-install a browser inside Extract.
type Extractor interface {
	Extract(context.Context, string) ([]model.Song, error)
}

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

type APIEndpoint struct {
	URL        string
	Parameters bool
}

type Option func(*Client)

func WithAPIEndpoints(endpoints []APIEndpoint) Option {
	return func(client *Client) {
		client.endpoints = append([]APIEndpoint(nil), endpoints...)
	}
}

type Client struct {
	http      HTTPDoer
	browser   Extractor
	endpoints []APIEndpoint
}

func New(httpClient *netx.Client, browser Extractor, options ...Option) *Client {
	if httpClient == nil {
		httpClient = netx.New(15*time.Second, 8, nil)
	}
	client := &Client{http: httpClient, browser: browser, endpoints: defaultAPIEndpoints()}
	for _, option := range options {
		option(client)
	}
	return client
}

func defaultAPIEndpoints() []APIEndpoint {
	return []APIEndpoint{
		{URL: "https://mobileservice.kugou.com/api/v3/plist/speciallist", Parameters: true},
		{URL: "https://mobileservice.kugou.com/api/v3/plist/list", Parameters: true},
		{URL: "https://m.kugou.com/plist/list/{playlist_id}"},
		{URL: "https://wwwapi.kugou.com/playlist/detail/{playlist_id}"},
		{URL: "https://mobileservice.kugou.com/api/v3/special/song", Parameters: true},
	}
}

func (c *Client) ParsePlaylist(ctx context.Context, rawURL string) ([]model.Song, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		if err == nil {
			err = errors.New("URL must use http or https and include a host")
		}
		return nil, &Error{Kind: ErrorInvalidURL, Op: "parse URL", Err: err}
	}

	var failures []error
	pageHTML, finalURL, fetchErr := c.fetch(ctx, parsed.String())
	if fetchErr == nil {
		playlistID := PlaylistID(finalURL)
		if playlistID == "" {
			playlistID = PlaylistID(parsed.String())
		}
		if playlistID != "" {
			songs, apiErr := c.fetchAPI(ctx, playlistID)
			if len(songs) > 0 {
				return CleanupSongs(songs), nil
			}
			if apiErr != nil {
				failures = append(failures, apiErr)
			}
		}
		if songs := ExtractEmbeddedSongs(pageHTML); len(songs) > 0 {
			return songs, nil
		}
	} else {
		if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
			return nil, contextCause(ctx, fetchErr)
		}
		failures = append(failures, fetchErr)
	}

	if c.browser != nil {
		songs, browserErr := c.browser.Extract(ctx, parsed.String())
		if len(songs) > 0 {
			return CleanupSongs(songs), nil
		}
		if browserErr != nil {
			if errors.Is(browserErr, context.Canceled) || errors.Is(browserErr, context.DeadlineExceeded) {
				return nil, contextCause(ctx, browserErr)
			}
			failures = append(failures, browserErr)
		}
	}
	if len(failures) == 0 {
		failures = append(failures, errors.New("no direct, embedded, or browser extraction returned songs"))
	}
	return nil, &Error{Kind: ErrorExtraction, Op: "parse playlist", Err: errors.Join(failures...)}
}

func contextCause(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

// PlaylistID applies the Python reference's specialid/global_specialid rules.
func PlaylistID(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	if specialID := query.Get("specialid"); specialID != "" && specialID != "-2147483648" {
		return specialID
	}
	return query.Get("global_specialid")
}

func (c *Client) fetch(ctx context.Context, rawURL string) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", rawURL, err
	}
	setRequestHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", rawURL, &Error{Kind: ErrorHTTP, Op: "fetch playlist", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", responseURL(resp, rawURL), &Error{Kind: ErrorHTTP, Op: "fetch playlist", Err: fmt.Errorf("HTTP %s", resp.Status)}
	}
	payload, err := readLimited(resp.Body)
	if err != nil {
		return "", responseURL(resp, rawURL), &Error{Kind: ErrorHTTP, Op: "read playlist", Err: err}
	}
	return string(payload), responseURL(resp, rawURL), nil
}

func (c *Client) fetchAPI(ctx context.Context, playlistID string) ([]model.Song, error) {
	var failures []error
	for _, endpoint := range c.endpoints {
		rawURL := strings.ReplaceAll(endpoint.URL, "{playlist_id}", url.PathEscape(playlistID))
		parsed, err := url.Parse(rawURL)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		if endpoint.Parameters {
			query := parsed.Query()
			query.Set("specialid", playlistID)
			query.Set("pagesize", "200")
			query.Set("page", "1")
			parsed.RawQuery = query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			failures = append(failures, err)
			continue
		}
		setRequestHeaders(req)
		resp, err := c.http.Do(req)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, contextCause(ctx, err)
			}
			failures = append(failures, err)
			continue
		}
		payload, readErr := readAPIResponse(resp)
		if readErr != nil {
			failures = append(failures, readErr)
			continue
		}
		if songs := decodeSongResponse(payload); len(songs) > 0 {
			return songs, nil
		}
		failures = append(failures, fmt.Errorf("%s returned no songs", endpoint.URL))
	}
	return nil, errors.Join(failures...)
}

func readAPIResponse(resp *http.Response) ([]byte, error) {
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("HTTP %s", resp.Status)
	}
	return readLimited(resp.Body)
}

func readLimited(reader io.Reader) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, maxResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(payload)) > maxResponseBytes {
		return nil, fmt.Errorf("response exceeded %d bytes", maxResponseBytes)
	}
	return payload, nil
}

func responseURL(resp *http.Response, fallback string) string {
	if resp != nil && resp.Request != nil && resp.Request.URL != nil {
		return resp.Request.URL.String()
	}
	return fallback
}

func setRequestHeaders(req *http.Request) {
	req.Header.Set("User-Agent", desktopUserAgent)
	req.Header.Set("Referer", "https://m.kugou.com/")
}
