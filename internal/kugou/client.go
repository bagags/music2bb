// Package kugou implements Kugou-specific playlist extraction optimizations.
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
	"github.com/gguage/music-to-bb/internal/playlist"
)

const maxResponseBytes int64 = 16 << 20

const desktopUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

const (
	collectionEndpoint = "https://pubsongscdn.kugou.com/v2/get_other_list_file"
	h5SignatureSalt    = "NVPh5oo715z5DIWAeQlhMDsWXXQV4hwt"
	collectionPageSize = 100
)

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
	endpoints          []APIEndpoint
	collectionEndpoint string
	now                func() time.Time
}

func New(httpClient *netx.Client, options ...Option) *Client {
	if httpClient == nil {
		httpClient = netx.New(15*time.Second, 8, nil)
	}
	client := &Client{
		http:               httpClient,
		endpoints:          defaultAPIEndpoints(),
		collectionEndpoint: collectionEndpoint,
		now:                time.Now,
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
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

// ParsePlaylistResult is a compatibility helper that decodes the client's raw
// provider result using the same title and cleanup optimizations registered by
// production wiring.
func (c *Client) ParsePlaylistResult(ctx context.Context, rawURL string) (ParseResult, error) {
	source, err := playlist.ParseSource(rawURL)
	if err != nil {
		return ParseResult{}, &Error{Kind: ErrorInvalidURL, Op: "parse URL", Err: err}
	}
	raw, err := c.ExtractPlaylist(ctx, source)
	return ParseResult{Songs: decodeAndNormalize(raw.Tracks), ExpectedTotal: raw.ExpectedTotal}, err
}

// Name identifies this optimization in internal diagnostics.
func (c *Client) Name() string { return "kugou-playlist" }

// ExtractPlaylist tries Kugou's direct API and embedded-page strategies. It is
// intentionally browser-free; the neutral playlist coordinator owns generic
// browser fallback and cross-source merging.
func (c *Client) ExtractPlaylist(ctx context.Context, source playlist.Source) (playlist.RawResult, error) {
	parsed := source.URL()

	var failures []error
	best := playlist.RawResult{}
	pageHTML, finalURL, fetchErr := c.fetch(ctx, parsed.String())
	if fetchErr == nil {
		identity := playlistIdentity(finalURL)
		if identity.ID == "" {
			identity = playlistIdentity(parsed.String())
		}
		if identity.ID != "" {
			var result playlist.RawResult
			var apiErr error
			if identity.Collection {
				result, apiErr = c.fetchCollection(ctx, identity.ID)
				if resultSongCount(result) == 0 {
					fallback, fallbackErr := c.fetchAPI(ctx, identity.ID)
					result = betterRawResult(result, fallback)
					apiErr = errors.Join(apiErr, fallbackErr)
				}
			} else {
				result, apiErr = c.fetchAPI(ctx, identity.ID)
			}
			if apiErr != nil && (errors.Is(apiErr, context.Canceled) || errors.Is(apiErr, context.DeadlineExceeded)) {
				return playlist.RawResult{}, contextCause(ctx, apiErr)
			}
			best = betterRawResult(best, result)
			if apiErr != nil {
				failures = append(failures, apiErr)
			}
		}
		if tracks := ExtractEmbeddedTracks(pageHTML); len(tracks) > 0 {
			best = betterRawResult(best, playlist.RawResult{Tracks: tracks})
		}
	} else {
		if errors.Is(fetchErr, context.Canceled) || errors.Is(fetchErr, context.DeadlineExceeded) {
			return playlist.RawResult{}, contextCause(ctx, fetchErr)
		}
		failures = append(failures, fetchErr)
	}

	if err := ctx.Err(); err != nil {
		return playlist.RawResult{}, err
	}
	if resultSongCount(best) > 0 {
		return best, nil
	}
	if len(failures) == 0 {
		failures = append(failures, errors.New("no direct or embedded extraction returned songs"))
	}
	return playlist.RawResult{}, &Error{Kind: ErrorExtraction, Op: "extract playlist", Err: errors.Join(failures...)}
}

var _ playlist.PlaylistExtractor = (*Client)(nil)

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

func (c *Client) fetchAPI(ctx context.Context, playlistID string) (playlist.RawResult, error) {
	var failures []error
	best := playlist.RawResult{}
	for _, endpoint := range c.endpoints {
		var result playlist.RawResult
		var err error
		if endpoint.Paginated {
			result, err = c.fetchPaginatedAPI(ctx, endpoint, playlistID)
		} else {
			result, err = c.fetchAPIPage(ctx, endpoint, playlistID, 1, 200)
		}
		best = betterRawResult(best, result)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return best, contextCause(ctx, err)
			}
			failures = append(failures, err)
			continue
		}
		count := resultSongCount(result)
		if count > 0 && (result.ExpectedTotal == 0 || count >= result.ExpectedTotal) {
			return result, nil
		}
	}
	if resultSongCount(best) > 0 {
		return best, errors.Join(failures...)
	}
	return playlist.RawResult{}, errors.Join(failures...)
}

func (c *Client) fetchPaginatedAPI(ctx context.Context, endpoint APIEndpoint, playlistID string) (playlist.RawResult, error) {
	result := playlist.RawResult{}
	for page := 1; ; page++ {
		current, err := c.fetchAPIPage(ctx, endpoint, playlistID, page, 200)
		if err != nil {
			return result, err
		}
		if current.ExpectedTotal > result.ExpectedTotal {
			result.ExpectedTotal = current.ExpectedTotal
		}
		before := len(result.Tracks)
		result.Tracks = mergeTracks(result.Tracks, current.Tracks)
		count := resultSongCount(result)
		if len(current.Tracks) == 0 || len(result.Tracks) == before || result.ExpectedTotal == 0 || count >= result.ExpectedTotal {
			return result, nil
		}
	}
}

func (c *Client) fetchAPIPage(ctx context.Context, endpoint APIEndpoint, playlistID string, page, pageSize int) (playlist.RawResult, error) {
	rawURL := strings.ReplaceAll(endpoint.URL, "{playlist_id}", url.PathEscape(playlistID))
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return playlist.RawResult{}, err
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
		return playlist.RawResult{}, err
	}
	setRequestHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return playlist.RawResult{}, err
	}
	payload, readErr := readAPIResponse(resp)
	if readErr != nil {
		return playlist.RawResult{}, readErr
	}
	result := decodeSongPage(payload)
	if len(result.Tracks) > 0 {
		return result, nil
	}
	return result, fmt.Errorf("%s returned no songs", endpoint.URL)
}

func (c *Client) fetchCollection(ctx context.Context, collectionID string) (playlist.RawResult, error) {
	result := playlist.RawResult{}
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
		before := len(result.Tracks)
		result.Tracks = mergeTracks(result.Tracks, current.Tracks)
		count := resultSongCount(result)
		if len(current.Tracks) == 0 {
			if len(result.Tracks) == 0 {
				return result, errors.New("collection endpoint returned no songs")
			}
			return result, nil
		}
		if len(result.Tracks) == before || result.ExpectedTotal == 0 || count >= result.ExpectedTotal {
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

func betterRawResult(left, right playlist.RawResult) playlist.RawResult {
	expected := max(left.ExpectedTotal, right.ExpectedTotal)
	if resultSongCount(right) > resultSongCount(left) {
		right.ExpectedTotal = expected
		return right
	}
	left.ExpectedTotal = expected
	return left
}

func resultSongCount(result playlist.RawResult) int {
	return len(decodeAndNormalize(result.Tracks))
}

func decodeAndNormalize(tracks []playlist.TrackCandidate) []model.Song {
	return CleanupSongs(playlist.DecodeTracks(tracks, []playlist.TitleExtractor{NewTitleExtractor()}))
}

func mergeTracks(existing, additional []playlist.TrackCandidate) []playlist.TrackCandidate {
	merged := make([]playlist.TrackCandidate, 0, len(existing)+len(additional))
	seen := make(map[string]struct{}, len(existing)+len(additional))
	seenNames := make(map[string]struct{}, len(existing)+len(additional))
	for _, candidate := range existing {
		song, ok := decodeCandidate(candidate)
		if !ok {
			continue
		}
		merged = append(merged, candidate.Clone())
		seen[songIdentity(song)] = struct{}{}
		seenNames[songNameIdentity(song)] = struct{}{}
	}
	for _, candidate := range additional {
		song, ok := decodeCandidate(candidate)
		if !ok {
			continue
		}
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
		merged = append(merged, candidate.Clone())
	}
	return merged
}

func decodeCandidate(candidate playlist.TrackCandidate) (model.Song, bool) {
	title, ok := NewTitleExtractor().ExtractTitle(candidate.Clone())
	title.Name = strings.TrimSpace(title.Name)
	title.Artist = strings.TrimSpace(title.Artist)
	if !ok || title.Name == "" {
		return model.Song{}, false
	}
	return model.Song{
		Name: title.Name, Artist: title.Artist,
		Album: strings.TrimSpace(candidate.Album), Duration: strings.TrimSpace(candidate.Duration), Hash: strings.TrimSpace(candidate.Hash),
	}, true
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
