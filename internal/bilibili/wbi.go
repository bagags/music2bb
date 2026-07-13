package bilibili

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var wbiMixinTable = [...]int{
	46, 47, 18, 2, 53, 8, 23, 32, 15, 50, 10, 31, 58, 3, 45, 35,
	27, 43, 5, 49, 33, 9, 42, 19, 29, 28, 14, 39, 12, 38, 41, 13,
	37, 48, 7, 16, 24, 55, 40, 61, 26, 17, 0, 1, 60, 51, 30, 4,
	22, 25, 54, 21, 56, 59, 6, 63, 57, 62, 11, 36, 20, 34, 44, 52,
}

var wbiUnsafe = regexp.MustCompile(`[^A-Za-z0-9_!'()*\.\-]`)

func mixinKey(original string) string {
	var builder strings.Builder
	for _, index := range wbiMixinTable {
		if index < len(original) {
			builder.WriteByte(original[index])
		}
	}
	key := builder.String()
	if len(key) > 32 {
		key = key[:32]
	}
	return key
}

func signWBI(params url.Values, imgKey, subKey string, timestamp int64) url.Values {
	result := make(url.Values, len(params)+2)
	for key, values := range params {
		for _, value := range values {
			result.Add(key, wbiUnsafe.ReplaceAllString(value, ""))
		}
	}
	result.Set("wts", strconv.FormatInt(timestamp, 10))
	keys := make([]string, 0, len(result))
	for key := range result {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make(url.Values, len(keys))
	for _, key := range keys {
		ordered[key] = result[key]
	}
	digest := md5.Sum([]byte(ordered.Encode() + mixinKey(imgKey+subKey)))
	result.Set("w_rid", hex.EncodeToString(digest[:]))
	return result
}

func (c *Client) SignWBI(ctx context.Context, params url.Values) (url.Values, error) {
	img, sub, err := c.wbiKeys(ctx)
	if err != nil {
		return nil, err
	}
	return signWBI(params, img, sub, c.now().Unix()), nil
}

func (c *Client) wbiKeys(ctx context.Context) (string, string, error) {
	c.wbiMu.Lock()
	defer c.wbiMu.Unlock()
	if c.wbiImgKey != "" && c.now().Sub(c.wbiAt) < 10*time.Minute {
		return c.wbiImgKey, c.wbiSubKey, nil
	}
	var data navData
	if err := c.get(ctx, c.account, "wbi keys", c.endpoints.Nav, nil, &data); err != nil {
		return "", "", err
	}
	img := strings.TrimSuffix(path.Base(data.WBIImg.ImgURL), path.Ext(data.WBIImg.ImgURL))
	sub := strings.TrimSuffix(path.Base(data.WBIImg.SubURL), path.Ext(data.WBIImg.SubURL))
	if img == "" || sub == "" || img == "." || sub == "." {
		return "", "", &APIError{Operation: "wbi keys", Message: "NAV response omitted WBI keys"}
	}
	c.wbiImgKey, c.wbiSubKey, c.wbiAt = img, sub, c.now()
	return img, sub, nil
}
