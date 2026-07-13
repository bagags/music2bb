package bilibili

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/gguage/music-to-bb/internal/model"
)

type favoriteListData struct {
	List []struct {
		ID         flexibleInt64 `json:"id"`
		Title      string        `json:"title"`
		MediaCount int           `json:"media_count"`
	} `json:"list"`
}

func (c *Client) ListFavorites(ctx context.Context) ([]model.Favorite, error) {
	account, err := c.Account(ctx)
	if err != nil {
		return nil, err
	}
	params := url.Values{}
	if account.MID != 0 {
		params.Set("up_mid", strconv.FormatInt(account.MID, 10))
	}
	var data favoriteListData
	if err := c.get(ctx, c.account, "list favorites", c.endpoints.FavoriteList, params, &data); err != nil {
		return nil, err
	}
	favorites := make([]model.Favorite, 0, len(data.List))
	for _, favorite := range data.List {
		favorites = append(favorites, model.Favorite{
			ID: int64(favorite.ID), Title: favorite.Title,
			Count: favorite.MediaCount, MediaCount: favorite.MediaCount,
		})
	}
	return favorites, nil
}

func (c *Client) CreateFavorite(ctx context.Context, request CreateFavoriteRequest) (model.Favorite, error) {
	title := strings.TrimSpace(request.Title)
	if title == "" {
		return model.Favorite{}, &APIError{Operation: "create favorite", Message: "title is required"}
	}
	csrf := c.csrf()
	if csrf == "" {
		return model.Favorite{}, &APIError{Operation: "create favorite", Message: "missing bili_jct cookie"}
	}
	privacy := "0"
	if request.Private {
		privacy = "1"
	}
	var data struct {
		ID flexibleInt64 `json:"id"`
	}
	err := c.post(ctx, "create favorite", c.endpoints.FavoriteCreate, url.Values{
		"title": {title}, "intro": {request.Intro}, "privacy": {privacy}, "csrf": {csrf},
	}, &data)
	if err != nil {
		return model.Favorite{}, err
	}
	return model.Favorite{ID: int64(data.ID), Title: title}, nil
}

func (c *Client) AddToFavorite(ctx context.Context, favoriteID int64, videos []model.Video) (AddResult, error) {
	result := AddResult{FavoriteID: favoriteID, Succeeded: make([]string, 0, len(videos))}
	csrf := c.csrf()
	if csrf == "" {
		for index, video := range videos {
			result.Failed = append(result.Failed, AddFailure{Index: index, BVID: video.BVID, Reason: "missing bili_jct cookie"})
		}
		return result, result.Error()
	}
	for index, video := range videos {
		if err := ctx.Err(); err != nil {
			for pending := index; pending < len(videos); pending++ {
				result.Failed = append(result.Failed, AddFailure{Index: pending, BVID: videos[pending].BVID, Reason: err.Error(), Err: err})
			}
			return result, errors.Join(err, result.Error())
		}
		aid := video.AID
		if aid == 0 {
			detail, err := c.VideoDetail(ctx, video.BVID)
			if err != nil || detail.AID == 0 {
				reason := "unable to resolve aid"
				if err != nil {
					reason = err.Error()
				}
				result.Failed = append(result.Failed, AddFailure{Index: index, BVID: video.BVID, Reason: reason, Err: err})
				continue
			}
			aid = detail.AID
		}
		form := url.Values{
			"rid": {strconv.FormatInt(aid, 10)}, "type": {"2"},
			"add_media_ids": {strconv.FormatInt(favoriteID, 10)}, "del_media_ids": {""}, "csrf": {csrf},
		}
		if signed, err := c.SignWBI(ctx, form); err == nil {
			form = signed
		} else if ctx.Err() != nil {
			for pending := index; pending < len(videos); pending++ {
				result.Failed = append(result.Failed, AddFailure{Index: pending, BVID: videos[pending].BVID, Reason: ctx.Err().Error(), Err: ctx.Err()})
			}
			return result, errors.Join(ctx.Err(), result.Error())
		}
		if err := c.post(ctx, "add favorite resource", c.endpoints.FavoriteDeal, form, nil); err != nil {
			result.Failed = append(result.Failed, AddFailure{Index: index, BVID: video.BVID, Reason: err.Error(), Err: err})
			continue
		}
		result.Succeeded = append(result.Succeeded, video.BVID)
		if c.writeDelay > 0 && index+1 < len(videos) {
			if err := c.sleep(ctx, c.writeDelay); err != nil {
				for pending := index + 1; pending < len(videos); pending++ {
					result.Failed = append(result.Failed, AddFailure{Index: pending, BVID: videos[pending].BVID, Reason: err.Error(), Err: err})
				}
				return result, errors.Join(err, result.Error())
			}
		}
	}
	return result, result.Error()
}

type favoriteResourcesData struct {
	Medias []struct {
		ID    flexibleInt64 `json:"id"`
		BVID  string        `json:"bvid"`
		Title string        `json:"title"`
	} `json:"medias"`
	HasMore bool `json:"has_more"`
}

func (c *Client) ListFavoriteResources(ctx context.Context, favoriteID int64) ([]FavoriteResource, error) {
	const pageSize = 20
	resources := make([]FavoriteResource, 0)
	for page := 1; ; page++ {
		var data favoriteResourcesData
		params := url.Values{
			"media_id": {strconv.FormatInt(favoriteID, 10)}, "pn": {strconv.Itoa(page)}, "ps": {strconv.Itoa(pageSize)},
		}
		if err := c.get(ctx, c.account, "list favorite resources", c.endpoints.FavoriteResourceList, params, &data); err != nil {
			return resources, err
		}
		for _, media := range data.Medias {
			resources = append(resources, FavoriteResource{AID: int64(media.ID), BVID: media.BVID, Title: media.Title})
		}
		if !data.HasMore || len(data.Medias) == 0 {
			return resources, nil
		}
	}
}

func (c *Client) RemoveFavoriteResources(ctx context.Context, favoriteID int64, aids []int64) error {
	if len(aids) == 0 {
		return nil
	}
	csrf := c.csrf()
	if csrf == "" {
		return &APIError{Operation: "remove favorite resources", Message: "missing bili_jct cookie"}
	}
	resources := make([]string, len(aids))
	for index, aid := range aids {
		resources[index] = fmt.Sprintf("%d:2", aid)
	}
	return c.post(ctx, "remove favorite resources", c.endpoints.FavoriteResourceDel, url.Values{
		"media_id": {strconv.FormatInt(favoriteID, 10)}, "resources": {strings.Join(resources, ",")}, "csrf": {csrf},
	}, nil)
}

func (c *Client) DeleteFavorite(ctx context.Context, favoriteID int64) error {
	csrf := c.csrf()
	if csrf == "" {
		return &APIError{Operation: "delete favorite", Message: "missing bili_jct cookie"}
	}
	return c.post(ctx, "delete favorite", c.endpoints.FavoriteDelete, url.Values{
		"media_ids": {strconv.FormatInt(favoriteID, 10)}, "csrf": {csrf},
	}, nil)
}
