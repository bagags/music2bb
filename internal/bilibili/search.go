package bilibili

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
)

type searchKey struct {
	Query      string
	Page       int
	PageSize   int
	SearchType int
	Order      string
}

type searchFlight struct {
	done chan struct{}
	err  error
}

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

type searchData struct {
	Result []json.RawMessage `json:"result"`
}

type searchBlock struct {
	ResultType string          `json:"result_type"`
	Data       json.RawMessage `json:"data"`
}

type searchVideo struct {
	BVID        string        `json:"bvid"`
	AID         flexibleInt64 `json:"aid"`
	Title       string        `json:"title"`
	Author      string        `json:"author"`
	Duration    string        `json:"duration"`
	Play        flexibleInt64 `json:"play"`
	Favorites   flexibleInt64 `json:"favorites"`
	VideoReview flexibleInt64 `json:"video_review"`
	Description string        `json:"description"`
	Tag         string        `json:"tag"`
	IsVerify    bool          `json:"is_verify"`
	Owner       struct {
		VerifyType int `json:"verify_type"`
	} `json:"owner"`
}

func (c *Client) Search(ctx context.Context, query string, options SearchOptions) ([]model.Video, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, &APIError{Operation: "search", Message: "empty query"}
	}
	if options.Page <= 0 {
		options.Page = 1
	}
	if options.PageSize <= 0 {
		options.PageSize = 20
	}
	if options.SearchType <= 0 {
		options.SearchType = 1
	}
	if options.Order == "" {
		options.Order = "totalrank"
	}
	key := searchKey{Query: query, Page: options.Page, PageSize: options.PageSize, SearchType: options.SearchType, Order: options.Order}
	if videos, ok := c.cached(key); ok {
		return videos, nil
	}

	c.cacheMu.Lock()
	if flight := c.inflight[key]; flight != nil {
		c.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-flight.done:
			if flight.err != nil {
				return nil, flight.err
			}
			videos, _ := c.cached(key)
			return videos, nil
		}
	}
	flight := &searchFlight{done: make(chan struct{})}
	c.inflight[key] = flight
	c.cacheMu.Unlock()

	videos, err := c.searchUncached(ctx, query, options)
	c.cacheMu.Lock()
	if err == nil {
		c.putCachedLocked(key, videos)
	}
	flight.err = err
	delete(c.inflight, key)
	close(flight.done)
	c.cacheMu.Unlock()
	return cloneVideos(videos), err
}

func (c *Client) searchUncached(ctx context.Context, query string, options SearchOptions) ([]model.Video, error) {
	if err := c.ensureFingerprint(ctx); err != nil {
		return nil, err
	}
	params := url.Values{
		"keyword":     {query},
		"page":        {strconv.Itoa(options.Page)},
		"page_size":   {strconv.Itoa(options.PageSize)},
		"search_type": {strconv.Itoa(options.SearchType)},
		"order":       {options.Order},
	}
	var data searchData
	if err := c.get(ctx, c.search, "search", c.endpoints.Search, params, &data); err != nil {
		return nil, err
	}
	items := make([]searchVideo, 0)
	for _, raw := range data.Result {
		var block searchBlock
		if err := json.Unmarshal(raw, &block); err == nil && block.ResultType != "" {
			if block.ResultType == "video" {
				_ = json.Unmarshal(block.Data, &items)
				break
			}
			continue
		}
		var direct searchVideo
		if err := json.Unmarshal(raw, &direct); err == nil && direct.BVID != "" {
			items = append(items, direct)
		}
	}
	videos := make([]model.Video, 0, len(items))
	for _, item := range items {
		if item.BVID == "" {
			continue
		}
		title := html.UnescapeString(htmlTagPattern.ReplaceAllString(item.Title, ""))
		text := strings.ToLower(title + " " + item.Author)
		videos = append(videos, model.Video{
			BVID: item.BVID, AID: int64(item.AID), Title: title, Uploader: item.Author,
			Duration: item.Duration, PlayCount: int64(item.Play), FavoriteCount: int64(item.Favorites),
			DanmakuCount: int64(item.VideoReview), Description: item.Description,
			Tags: splitTags(item.Tag), IsVerified: item.IsVerify || item.Owner.VerifyType > 0,
			IsOfficial: strings.Contains(text, "官方") || strings.Contains(text, "official"),
		})
	}
	return videos, nil
}

func splitTags(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func (c *Client) cached(key searchKey) ([]model.Video, bool) {
	c.cacheMu.Lock()
	defer c.cacheMu.Unlock()
	videos, ok := c.cache[key]
	return cloneVideos(videos), ok
}

func (c *Client) putCachedLocked(key searchKey, videos []model.Video) {
	if _, exists := c.cache[key]; !exists {
		c.cacheOrder = append(c.cacheOrder, key)
	}
	c.cache[key] = cloneVideos(videos)
	for len(c.cacheOrder) > c.cacheSize {
		oldest := c.cacheOrder[0]
		c.cacheOrder = c.cacheOrder[1:]
		delete(c.cache, oldest)
	}
}

func cloneVideos(videos []model.Video) []model.Video {
	if videos == nil {
		return nil
	}
	clone := make([]model.Video, len(videos))
	copy(clone, videos)
	for index := range clone {
		clone[index].Tags = append([]string(nil), clone[index].Tags...)
	}
	return clone
}

type videoDetailData struct {
	BVID     string        `json:"bvid"`
	AID      flexibleInt64 `json:"aid"`
	Title    string        `json:"title"`
	Duration flexibleInt64 `json:"duration"`
	Desc     string        `json:"desc"`
	Owner    struct {
		Name string `json:"name"`
	} `json:"owner"`
	Stat struct {
		View     flexibleInt64 `json:"view"`
		Favorite flexibleInt64 `json:"favorite"`
		Danmaku  flexibleInt64 `json:"danmaku"`
	} `json:"stat"`
	IsCooperation bool `json:"is_cooperation"`
	IsSteinGate   bool `json:"is_stein_gate"`
}

func (c *Client) VideoDetail(ctx context.Context, bvid string) (model.Video, error) {
	var data videoDetailData
	if err := c.get(ctx, c.account, "video detail", c.endpoints.VideoDetail, url.Values{"bvid": {bvid}}, &data); err != nil {
		return model.Video{}, err
	}
	if data.BVID == "" {
		data.BVID = bvid
	}
	return model.Video{
		BVID: data.BVID, AID: int64(data.AID), Title: data.Title, Uploader: data.Owner.Name,
		Duration: formatDuration(int64(data.Duration)), Description: data.Desc,
		PlayCount: int64(data.Stat.View), FavoriteCount: int64(data.Stat.Favorite), DanmakuCount: int64(data.Stat.Danmaku),
		IsOfficial: data.IsCooperation || data.IsSteinGate,
	}, nil
}
