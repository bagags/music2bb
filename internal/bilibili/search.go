package bilibili

import (
	"context"
	"encoding/json"
	"html"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/bagags/music2bb-go/internal/model"
)

type searchKey struct {
	Query       string
	Page        int
	PageSize    int
	SearchType  string
	Order       string
	Identity    SearchIdentity
	IdentityKey string
}

type searchFlight struct {
	done   chan struct{}
	result SearchResult
	err    error
}

const (
	positiveSearchCacheTTL = 7 * 24 * time.Hour
	negativeSearchCacheTTL = time.Hour
)

var htmlTagPattern = regexp.MustCompile(`<[^>]*>`)

type searchData struct {
	Result  []json.RawMessage `json:"result"`
	Voucher string            `json:"v_voucher"`
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
	result, err := c.SearchWithResult(ctx, query, options)
	return result.Videos, err
}

// SearchWithResult returns cache metadata in addition to the caller-owned
// unscored video snapshot.
func (c *Client) SearchWithResult(ctx context.Context, query string, options SearchOptions) (SearchResult, error) {
	if err := ctx.Err(); err != nil {
		return SearchResult{}, err
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return SearchResult{}, &APIError{Operation: "search", Message: "empty query"}
	}
	if options.Page <= 0 {
		options.Page = 1
	}
	if options.PageSize <= 0 {
		options.PageSize = 20
	}
	if options.SearchType == "" {
		options.SearchType = "video"
	}
	if options.Order == "" {
		options.Order = "totalrank"
	}
	if options.Identity == "" {
		options.Identity = SearchIdentityAnonymous
	}
	if options.Identity != SearchIdentityAnonymous && options.Identity != SearchIdentitySession {
		return SearchResult{}, &APIError{Operation: "search", Message: "unknown search identity " + string(options.Identity)}
	}
	if options.CachePolicy != SearchCacheDefault && options.CachePolicy != SearchCacheBypass && options.CachePolicy != SearchCacheRefresh {
		return SearchResult{}, &APIError{Operation: "search", Message: "unknown search cache policy " + string(options.CachePolicy)}
	}
	key := searchKey{
		Query: query, Page: options.Page, PageSize: options.PageSize,
		SearchType: options.SearchType, Order: options.Order, Identity: options.Identity,
		IdentityKey: c.searchIdentityKey(options.Identity),
	}
	if options.Identity == SearchIdentityAnonymous && key.IdentityKey == "" && !options.CacheOnly {
		if err := c.ensureFingerprint(ctx, options.Identity); err != nil {
			return SearchResult{}, err
		}
		key.IdentityKey = c.searchIdentityKey(options.Identity)
	}
	if options.CachePolicy == SearchCacheDefault {
		if videos, ok := c.cached(key); ok {
			return SearchResult{Videos: videos, CacheHit: true}, nil
		}
		if key.IdentityKey != "" && c.searchCache != nil {
			entry, ok, err := c.searchCache.Get(ctx, key.cacheKey())
			if err != nil {
				return SearchResult{}, &APIError{Operation: "search cache", Err: err}
			}
			if ok && searchCacheEntryFresh(entry, c.now()) {
				c.cacheMu.Lock()
				c.putCachedLocked(key, entry)
				c.cacheMu.Unlock()
				return SearchResult{Videos: cloneVideos(entry.Videos), CacheHit: true}, nil
			}
		}
	}
	if options.CacheOnly {
		return SearchResult{}, SearchCacheMissError{}
	}
	if options.CachePolicy == SearchCacheBypass || options.CachePolicy == SearchCacheRefresh {
		videos, err := c.searchUncached(ctx, query, options)
		result := SearchResult{Videos: cloneVideos(videos), RemoteRequest: true}
		if err == nil && options.CachePolicy == SearchCacheRefresh {
			c.storeSearchResult(ctx, key, videos)
		}
		return result, err
	}

	c.cacheMu.Lock()
	if flight := c.inflight[key]; flight != nil {
		c.cacheMu.Unlock()
		select {
		case <-ctx.Done():
			return SearchResult{}, ctx.Err()
		case <-flight.done:
			if flight.err != nil {
				return SearchResult{}, flight.err
			}
			return SearchResult{Videos: cloneVideos(flight.result.Videos), CacheHit: true}, nil
		}
	}
	flight := &searchFlight{done: make(chan struct{})}
	c.inflight[key] = flight
	c.cacheMu.Unlock()

	videos, err := c.searchUncached(ctx, query, options)
	result := SearchResult{Videos: cloneVideos(videos), RemoteRequest: true}
	if err == nil {
		c.storeSearchResult(ctx, key, videos)
	}
	c.cacheMu.Lock()
	flight.result = SearchResult{
		Videos: cloneVideos(result.Videos), RemoteRequest: result.RemoteRequest,
	}
	flight.err = err
	delete(c.inflight, key)
	close(flight.done)
	c.cacheMu.Unlock()
	return result, err
}

func (c *Client) searchUncached(ctx context.Context, query string, options SearchOptions) ([]model.Video, error) {
	if err := c.ensureFingerprint(ctx, options.Identity); err != nil {
		return nil, err
	}
	var data searchData
	if options.Identity == SearchIdentityAnonymous && options.SearchType == "video" && options.Order == "totalrank" {
		params := url.Values{
			"keyword":      {query},
			"page":         {strconv.Itoa(options.Page)},
			"page_size":    {strconv.Itoa(options.PageSize)},
			"platform":     {"h5"},
			"web_location": {"1430654"},
		}
		if err := c.get(ctx, c.search, "search", c.endpoints.AnonymousSearch, params, &data); err != nil {
			return nil, err
		}
	} else {
		params := url.Values{
			"keyword":      {query},
			"page":         {strconv.Itoa(options.Page)},
			"page_size":    {strconv.Itoa(options.PageSize)},
			"search_type":  {options.SearchType},
			"order":        {options.Order},
			"web_location": {"1430654"},
		}
		signed, err := c.SignWBIWithIdentity(ctx, params, options.Identity)
		if err != nil {
			return nil, err
		}
		if err := c.get(ctx, c.searchClient(options.Identity), "search", c.endpoints.Search, signed, &data); err != nil {
			return nil, err
		}
	}
	if options.Identity == SearchIdentityAnonymous {
		_ = c.SaveAnonymousCookies()
	}
	if data.Voucher != "" {
		return nil, &APIError{Operation: "search", Message: "risk-control challenge required", RiskControl: true, RiskReason: RiskControlVoucher}
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
	entry, ok := c.cache[key]
	if !ok || !searchCacheEntryFresh(entry, c.now()) {
		if ok {
			c.removeCachedLocked(key)
		}
		return nil, false
	}
	return cloneVideos(entry.Videos), true
}

func (c *Client) putCachedLocked(key searchKey, entry SearchCacheEntry) {
	if _, exists := c.cache[key]; !exists {
		c.cacheOrder = append(c.cacheOrder, key)
	}
	c.cache[key] = cloneSearchCacheEntry(entry)
	for len(c.cacheOrder) > c.cacheSize {
		oldest := c.cacheOrder[0]
		c.cacheOrder = c.cacheOrder[1:]
		delete(c.cache, oldest)
	}
}

func (c *Client) removeCachedLocked(key searchKey) {
	delete(c.cache, key)
	for index, candidate := range c.cacheOrder {
		if candidate == key {
			c.cacheOrder = append(c.cacheOrder[:index], c.cacheOrder[index+1:]...)
			break
		}
	}
}

func (c *Client) storeSearchResult(ctx context.Context, initialKey searchKey, videos []model.Video) {
	key := initialKey
	key.IdentityKey = c.searchIdentityKey(key.Identity)
	if key.IdentityKey == "" {
		return
	}
	entry := SearchCacheEntry{Key: key.cacheKey(), Videos: cloneVideos(videos), StoredAt: c.now()}
	c.cacheMu.Lock()
	c.putCachedLocked(key, entry)
	c.cacheMu.Unlock()
	if c.searchCache != nil {
		_ = c.searchCache.Put(ctx, entry)
	}
}

func (key searchKey) cacheKey() SearchCacheKey {
	return SearchCacheKey{
		Query: key.Query, Page: key.Page, PageSize: key.PageSize,
		SearchType: key.SearchType, Order: key.Order, Identity: key.Identity, IdentityKey: key.IdentityKey,
	}
}

func searchCacheEntryFresh(entry SearchCacheEntry, now time.Time) bool {
	if entry.StoredAt.IsZero() {
		return false
	}
	ttl := positiveSearchCacheTTL
	if len(entry.Videos) == 0 {
		ttl = negativeSearchCacheTTL
	}
	age := now.Sub(entry.StoredAt)
	return age < ttl
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
