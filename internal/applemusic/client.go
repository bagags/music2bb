package applemusic

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gguage/music-to-bb/internal/netx"
	"github.com/gguage/music-to-bb/internal/playlist"
)

const maxResponseBytes int64 = 16 << 20

const desktopUserAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.0.0 Safari/537.36"

var serializedScriptIDPattern = regexp.MustCompile(`(?i)(?:^|\s)id\s*=\s*["']serialized-server-data["'](?:\s|>)`)

type HTTPDoer interface {
	Do(*http.Request) (*http.Response, error)
}

// Client fetches Apple Music's unauthenticated server-rendered playlist page.
type Client struct {
	http HTTPDoer
}

func New(httpClient *netx.Client) *Client {
	if httpClient == nil {
		httpClient = netx.New(15*time.Second, 8, nil)
	}
	return &Client{http: httpClient}
}

// Name identifies this optimization in internal diagnostics.
func (c *Client) Name() string { return "apple-music-playlist" }

// ExtractPlaylist decodes the exact serialized server state embedded in an
// Apple Music share page. Chromium fallback remains coordinator-owned.
func (c *Client) ExtractPlaylist(ctx context.Context, source playlist.Source) (playlist.RawResult, error) {
	if err := ctx.Err(); err != nil {
		return playlist.RawResult{}, err
	}
	pageHTML, err := c.fetch(ctx, source.String())
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return playlist.RawResult{}, ctxErr
		}
		return playlist.RawResult{}, err
	}
	result, err := extractSerializedPlaylist(pageHTML, source.URL())
	if err != nil {
		return playlist.RawResult{}, &Error{Kind: ErrorExtraction, Op: "decode serialized server data", Err: err}
	}
	return result, nil
}

func (c *Client) fetch(ctx context.Context, rawURL string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, &Error{Kind: ErrorHTTP, Op: "create request", Err: err}
	}
	request.Header.Set("User-Agent", desktopUserAgent)
	request.Header.Set("Accept", "text/html,application/xhtml+xml")
	response, err := c.http.Do(request)
	if err != nil {
		return nil, &Error{Kind: ErrorHTTP, Op: "fetch playlist", Err: err}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, &Error{Kind: ErrorHTTP, Op: "fetch playlist", Err: fmt.Errorf("HTTP %s", response.Status)}
	}
	payload, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return nil, &Error{Kind: ErrorHTTP, Op: "read playlist", Err: err}
	}
	if int64(len(payload)) > maxResponseBytes {
		return nil, &Error{Kind: ErrorHTTP, Op: "read playlist", Err: fmt.Errorf("response exceeded %d bytes", maxResponseBytes)}
	}
	return payload, nil
}

type serializedRoot struct {
	Data []serializedEnvelope `json:"data"`
}

type serializedEnvelope struct {
	Data struct {
		Sections []serializedSection `json:"sections"`
	} `json:"data"`
}

type serializedSection struct {
	ID                         string            `json:"id"`
	ItemKind                   string            `json:"itemKind"`
	Items                      []serializedItem  `json:"items"`
	ContainerContentDescriptor contentDescriptor `json:"containerContentDescriptor"`
}

type serializedItem struct {
	Title             string            `json:"title"`
	ArtistName        string            `json:"artistName"`
	SubtitleLinks     []titleLink       `json:"subtitleLinks"`
	TertiaryLinks     []titleLink       `json:"tertiaryLinks"`
	Duration          json.RawMessage   `json:"duration"`
	TrackCount        *int              `json:"trackCount"`
	ContentDescriptor contentDescriptor `json:"contentDescriptor"`
}

type titleLink struct {
	Title string `json:"title"`
}

type contentDescriptor struct {
	Kind        string `json:"kind"`
	Identifiers struct {
		StoreAdamID string `json:"storeAdamID"`
	} `json:"identifiers"`
	URL string `json:"url"`
}

func extractSerializedPlaylist(pageHTML []byte, sourceURL *url.URL) (playlist.RawResult, error) {
	payload, err := serializedServerData(pageHTML)
	if err != nil {
		return playlist.RawResult{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	var root serializedRoot
	if err := decoder.Decode(&root); err != nil {
		return playlist.RawResult{}, fmt.Errorf("decode JSON: %w", err)
	}
	playlistID := playlistIdentifier(sourceURL)
	if playlistID == "" {
		return playlist.RawResult{}, errors.New("playlist URL does not contain an Apple Music playlist ID")
	}

	sections := make([]serializedSection, 0)
	for _, envelope := range root.Data {
		sections = append(sections, envelope.Data.Sections...)
	}
	var trackSection *serializedSection
	for index := range sections {
		section := &sections[index]
		if section.ItemKind == "trackLockup" && descriptorMatches(section.ContainerContentDescriptor, playlistID) {
			trackSection = section
			break
		}
	}
	if trackSection == nil {
		return playlist.RawResult{}, fmt.Errorf("matching trackLockup section %q was not found", playlistID)
	}

	expectedTotal := -1
	for _, section := range sections {
		for _, item := range section.Items {
			if item.TrackCount != nil && descriptorMatches(item.ContentDescriptor, playlistID) {
				expectedTotal = *item.TrackCount
				break
			}
		}
		if expectedTotal >= 0 {
			break
		}
	}
	if expectedTotal < 0 {
		expectedTotal = len(trackSection.Items)
	}

	tracks := make([]playlist.TrackCandidate, 0, len(trackSection.Items))
	for _, item := range trackSection.Items {
		artist := strings.TrimSpace(item.ArtistName)
		if artist == "" {
			artist = joinedLinkTitles(item.SubtitleLinks)
		}
		album := ""
		if len(item.TertiaryLinks) > 0 {
			album = strings.TrimSpace(item.TertiaryLinks[0].Title)
		}
		tracks = append(tracks, playlist.TrackCandidate{
			Fields: map[string]string{
				"title":  strings.TrimSpace(item.Title),
				"artist": artist,
			},
			Album:    album,
			Duration: formatDurationMillis(item.Duration),
		})
	}
	return playlist.RawResult{Tracks: tracks, ExpectedTotal: expectedTotal}, nil
}

func serializedServerData(pageHTML []byte) ([]byte, error) {
	lower := bytes.ToLower(pageHTML)
	for offset := 0; ; {
		relative := bytes.Index(lower[offset:], []byte("<script"))
		if relative < 0 {
			break
		}
		start := offset + relative
		headerEndRelative := bytes.IndexByte(lower[start:], '>')
		if headerEndRelative < 0 {
			break
		}
		headerEnd := start + headerEndRelative
		header := lower[start : headerEnd+1]
		if hasSerializedServerDataID(header) {
			closeRelative := bytes.Index(lower[headerEnd+1:], []byte("</script>"))
			if closeRelative < 0 {
				return nil, errors.New("serialized-server-data script is not closed")
			}
			payload := bytes.TrimSpace(pageHTML[headerEnd+1 : headerEnd+1+closeRelative])
			if len(payload) == 0 {
				return nil, errors.New("serialized-server-data script is empty")
			}
			return payload, nil
		}
		offset = headerEnd + 1
	}
	return nil, errors.New("serialized-server-data script was not found")
}

func hasSerializedServerDataID(header []byte) bool {
	return serializedScriptIDPattern.Match(header)
}

func playlistIdentifier(value *url.URL) string {
	if value == nil {
		return ""
	}
	segments := strings.Split(strings.Trim(value.Path, "/"), "/")
	for index := len(segments) - 1; index >= 0; index-- {
		if strings.HasPrefix(segments[index], "pl.") {
			return segments[index]
		}
	}
	return ""
}

func descriptorMatches(descriptor contentDescriptor, playlistID string) bool {
	return descriptor.Kind == "playlist" && descriptor.Identifiers.StoreAdamID == playlistID
}

func joinedLinkTitles(links []titleLink) string {
	titles := make([]string, 0, len(links))
	for _, link := range links {
		if title := strings.TrimSpace(link.Title); title != "" {
			titles = append(titles, title)
		}
	}
	return strings.Join(titles, " & ")
}

func formatDurationMillis(raw json.RawMessage) string {
	value := strings.TrimSpace(string(raw))
	if value == "" || value == "null" {
		return ""
	}
	if unquoted, err := strconv.Unquote(value); err == nil {
		value = unquoted
	}
	milliseconds, err := strconv.ParseInt(value, 10, 64)
	if err != nil || milliseconds < 0 {
		return ""
	}
	seconds := milliseconds / 1000
	return fmt.Sprintf("%d:%02d", seconds/60, seconds%60)
}

var _ playlist.PlaylistExtractor = (*Client)(nil)
