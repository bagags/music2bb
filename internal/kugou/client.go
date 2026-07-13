// Package kugou extracts playlist songs through direct HTTP APIs, embedded
// page JSON, and an explicitly injected browser fallback in that order.
package kugou

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
	"github.com/gguage/music-to-bb/internal/netx"
)

const maxResponseBytes int64 = 16 << 20

const desktopUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

const (
	collectionEndpoint = "https://pubsongscdn.kugou.com/v2/get_other_list_file"
	h5SignatureSalt    = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"
	collectionPageSize = 100
)

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
	Method     string
	Parameters bool
	Paginated  bool
}

type Option func(*Client)

func WithAPIEndpoints(endpoints []APIEndpoint) Option {
	return func(client *Client) {
		client.endpoints = append([]APIEndpoint(nil), endpoints...)
	}
}

// WithCollectionEndpoint overrides the signed collection endpoint. It is
// primarily useful for deterministic integration tests.
func WithCollectionEndpoint(rawURL string) Option {
	return func(client *Client) { client.collectionEndpoint = rawURL }
}

// WithNow overrides the clock used in Kugou H5 signatures.
func WithNow(now func() time.Time) Option {
	return func(client *Client) {
		if now != nil {
			client.now = now
		}
	}
}

// ParseResult retains the playlist's declared size so callers can warn while
// continuing with a useful partial extraction.
type ParseResult struct {
	Songs         []model.Song
	ExpectedTotal int
}

type Client struct {
	http               HTTPDoer
	browser            Extractor
	endpoints          []APIEndpoint
	collectionEndpoint string
	now                func() time.Time
}

func New(httpClient *netx.Client, browser Extractor, options ...Option) *Client {
	if httpClient == nil {
		httpClient = netx.New(15*time.Second, 8, nil)
	}
	client := &Client{
		http:               httpClient,
		browser:            browser,
		endpoints:          defaultAPIEndpoints(),
		collectionEndpoint: collectionEndpoint,
		now:                time.Now,
	}
	for _, option := range options {
		option(client)
	}
	return client
}

func defaultAPIEndpoints() []APIEndpoint {
	return []APIEndpoint{
		{URL: "https://mobileservice.kugou.com/api/v3/special/song", Parameters: true, Paginated: true},
		{URL: "https://www.kugou.com/yy/special/song/sid={playlist_id}", Method: http.MethodPost},
		{URL: "https://mobileservice.kugou.com/api/v3/plist/speciallist", Parameters: true},
		{URL: "https://mobileservice.kugou.com/api/v3/plist/list", Parameters: true},
		{URL: "https://m.kugou.com/plist/list/{playlist_id}"},
		{URL: "https://wwwapi.kugou.com/playlist/detail/{playlist_id}"},
	}
}

func (c *Client) ParsePlaylist(ctx context.Context, rawURL string) ([]model.Song, error) {
	result, err := c.ParsePlaylistResult(ctx, rawURL)
	return result.Songs, err
}

// ParsePlaylistResult tries all direct sources before the browser fallback and
// returns partial songs without a fatal error when Kugou declares a larger
// total than could be retrieved.
func (c *Client) ParsePlaylistResult(ctx context.Context, rawURL string) (ParseResult, error) {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		if err == nil {
			err = errors.New("URL must use http or https and include a host")
		}
		return ParseResult{}, &Error{Kind: ErrorInvalidURL, Op: "parse URL", Err: err}
	}

	var failures []error
	best := ParseResult{}
	pageHTML, finalURL, fetchErr := c.fetch(ctx, parsed.String())
	if fetchErr == nil {
		identity := playlistIdentity(finalURL)
		if identity.ID == "" {
			identity = playlistIdentity(parsed.String())
		}
		if identity.ID != "" {
			var result ParseResult
			var apiErr error
			if identity.Collection {
				result, apiErr = c.fetchCollection(ctx, identity.ID)
				if len(result.Songs) == 0 {
					fallback, fallbackErr := c.fetchAPI(ctx, identity.ID)
					result = betterResult(result, fallback)
					apiErr = errors.Join(apiErr, fallbackErr)
				}
			} else {
				result, apiErr = c.fetchAPI(ctx, identity.ID)
			}
			result.Songs = CleanupSongs(result.Songs)
			best = betterResult(best, result)
			if apiErr != nil {
				failures = append(failures, apiErr)
			}
		}
		if songs := ExtractEmbeddedSongs(pageHTML); len(songs) > 0 {
			best = betterResult(best, ParseResult{Songs: CleanupSongs(songs)})
		}
	} else {
		if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
			return ParseResult{}, contextCause(ctx, fetchErr)
		}
		failures = append(failures, fetchErr)
	}

	if c.browser != nil && (len(best.Songs) == 0 || best.ExpectedTotal > len(best.Songs)) {
		songs, browserErr := c.browser.Extract(ctx, parsed.String())
		if len(songs) > 0 {
			best.Songs = mergeSongs(best.Songs, CleanupSongs(songs))
		}
		if browserErr != nil {
			if errors.Is(browserErr, context.Canceled) || errors.Is(browserErr, context.DeadlineExceeded) {
				return ParseResult{}, contextCause(ctx, browserErr)
			}
			failures = append(failures, browserErr)
		}
	}
	if len(best.Songs) > 0 {
		return best, nil
	}
	if len(failures) == 0 {
		failures = append(failures, errors.New("no direct, embedded, or browser extraction returned songs"))
	}
	return ParseResult{}, &Error{Kind: ErrorExtraction, Op: "parse playlist", Err: errors.Join(failures...)}
}

func contextCause(ctx context.Context, fallback error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return fallback
}

// PlaylistID applies the Python reference's specialid/global_specialid rules.
func PlaylistID(rawURL string) string {
	return playlistIdentity(rawURL).ID
}

type playlistID struct {
	ID         string
	Collection bool
}

func playlistIdentity(rawURL string) playlistID {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return playlistID{}
	}
	query := parsed.Query()
	if specialID := query.Get("specialid"); specialID != "" && specialID != "-2147483648" {
		return playlistID{ID: specialID}
	}
	globalID := query.Get("global_specialid")
	return playlistID{ID: globalID, Collection: globalID != ""}
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

func (c *Client) fetchAPI(ctx context.Context, playlistID string) (ParseResult, error) {
	var failures []error
	best := ParseResult{}
	for _, endpoint := range c.endpoints {
		var result ParseResult
		var err error
		if endpoint.Paginated {
			result, err = c.fetchPaginatedAPI(ctx, endpoint, playlistID)
		} else {
			result, err = c.fetchAPIPage(ctx, endpoint, playlistID, 1, 200)
		}
		best = betterResult(best, result)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return best, contextCause(ctx, err)
			}
			failures = append(failures, err)
			continue
		}
		if len(result.Songs) > 0 && (result.ExpectedTotal == 0 || len(result.Songs) >= result.ExpectedTotal) {
			return result, nil
		}
	}
	if len(best.Songs) > 0 {
		return best, errors.Join(failures...)
	}
	return ParseResult{}, errors.Join(failures...)
}

func (c *Client) fetchPaginatedAPI(ctx context.Context, endpoint APIEndpoint, playlistID string) (ParseResult, error) {
	result := ParseResult{}
	for page := 1; ; page++ {
		current, err := c.fetchAPIPage(ctx, endpoint, playlistID, page, 200)
		if err != nil {
			return result, err
		}
		if current.ExpectedTotal > result.ExpectedTotal {
			result.ExpectedTotal = current.ExpectedTotal
		}
		before := len(result.Songs)
		result.Songs = mergeSongs(result.Songs, current.Songs)
		if len(current.Songs) == 0 || len(result.Songs) == before || result.ExpectedTotal == 0 || len(result.Songs) >= result.ExpectedTotal {
			return result, nil
		}
	}
}

func (c *Client) fetchAPIPage(ctx context.Context, endpoint APIEndpoint, playlistID string, page, pageSize int) (ParseResult, error) {
	rawURL := strings.ReplaceAll(endpoint.URL, "{playlist_id}", url.PathEscape(playlistID))
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ParseResult{}, err
	}
	if endpoint.Parameters {
		query := parsed.Query()
		query.Set("specialid", playlistID)
		query.Set("pagesize", strconv.Itoa(pageSize))
		query.Set("page", strconv.Itoa(page))
		parsed.RawQuery = query.Encode()
	}
	method := endpoint.Method
	if method == "" {
		method = http.MethodGet
	}
	var body io.Reader
	if method == http.MethodPost {
		// Kugou's current public endpoint rejects a POST without an explicit
		// zero-length body (HTTP 411), even though it takes no form fields.
		body = strings.NewReader("")
	}
	req, err := http.NewRequestWithContext(ctx, method, parsed.String(), body)
	if err != nil {
		return ParseResult{}, err
	}
	setRequestHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return ParseResult{}, err
	}
	payload, readErr := readAPIResponse(resp)
	if readErr != nil {
		return ParseResult{}, readErr
	}
	result := decodeSongPage(payload)
	if len(result.Songs) > 0 {
		return result, nil
	}
	return result, fmt.Errorf("%s returned no songs", endpoint.URL)
}

func (c *Client) fetchCollection(ctx context.Context, collectionID string) (ParseResult, error) {
	result := ParseResult{}
	for page := 1; ; page++ {
		params := collectionParams(collectionID, page, collectionPageSize, c.now())
		parsed, err := url.Parse(c.collectionEndpoint)
		if err != nil {
			return result, err
		}
		parsed.RawQuery = params.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
		if err != nil {
			return result, err
		}
		setRequestHeaders(req)
		resp, err := c.http.Do(req)
		if err != nil {
			return result, err
		}
		payload, err := readAPIResponse(resp)
		if err != nil {
			return result, err
		}
		current := decodeSongPage(payload)
		if current.ExpectedTotal > result.ExpectedTotal {
			result.ExpectedTotal = current.ExpectedTotal
		}
		before := len(result.Songs)
		result.Songs = mergeSongs(result.Songs, current.Songs)
		if len(current.Songs) == 0 {
			if len(result.Songs) == 0 {
				return result, errors.New("collection endpoint returned no songs")
			}
			return result, nil
		}
		if len(result.Songs) == before || result.ExpectedTotal == 0 || len(result.Songs) >= result.ExpectedTotal {
			return result, nil
		}
	}
}

func collectionParams(collectionID string, page, pageSize int, now time.Time) url.Values {
	timestamp := strconv.FormatInt(now.UnixMilli(), 10)
	params := url.Values{
		"appid":                {"1058"},
		"type":                 {"0"},
		"module":               {"playlist"},
		"page":                 {strconv.Itoa(page)},
		"pagesize":             {strconv.Itoa(pageSize)},
		"global_collection_id": {collectionID},
		"mid":                  {timestamp},
		"uid":                  {"0"},
		"token":                {""},
		"dfid":                 {"-"},
		"srcappid":             {"2919"},
		"clientver":            {"20000"},
		"clienttime":           {timestamp},
		"uuid":                 {timestamp},
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var signed strings.Builder
	signed.WriteString(h5SignatureSalt)
	for _, key := range keys {
		signed.WriteString(key)
		signed.WriteByte('=')
		signed.WriteString(params.Get(key))
	}
	signed.WriteString(h5SignatureSalt)
	params.Set("signature", fmt.Sprintf("%x", md5.Sum([]byte(signed.String()))))
	return params
}

func betterResult(left, right ParseResult) ParseResult {
	expected := max(left.ExpectedTotal, right.ExpectedTotal)
	if len(right.Songs) > len(left.Songs) {
		right.ExpectedTotal = expected
		return right
	}
	left.ExpectedTotal = expected
	return left
}

func mergeSongs(existing, additional []model.Song) []model.Song {
	merged := append([]model.Song(nil), existing...)
	seen := make(map[string]struct{}, len(existing)+len(additional))
	seenNames := make(map[string]struct{}, len(existing)+len(additional))
	for _, song := range existing {
		seen[songIdentity(song)] = struct{}{}
		seenNames[songNameIdentity(song)] = struct{}{}
	}
	for _, song := range additional {
		key := songIdentity(song)
		if _, ok := seen[key]; ok {
			continue
		}
		nameKey := songNameIdentity(song)
		if _, ok := seenNames[nameKey]; ok {
			continue
		}
		seen[key] = struct{}{}
		seenNames[nameKey] = struct{}{}
		merged = append(merged, song)
	}
	return merged
}

func songNameIdentity(song model.Song) string {
	return strings.TrimSpace(song.Name) + "\x00" + strings.TrimSpace(song.Artist)
}

func songIdentity(song model.Song) string {
	if hash := strings.TrimSpace(song.Hash); hash != "" {
		return "hash:" + strings.ToUpper(hash)
	}
	return "song:" + strings.TrimSpace(song.Name) + "\x00" + strings.TrimSpace(song.Artist)
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
