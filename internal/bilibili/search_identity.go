package bilibili

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
)

func (c *Client) searchIdentityKey(identity SearchIdentity) string {
	if identity == SearchIdentitySession {
		c.identityMu.Lock()
		mid := c.sessionMID
		c.identityMu.Unlock()
		if mid <= 0 {
			c.setSessionMIDFromCookies(c.accountJar.snapshot())
			c.identityMu.Lock()
			mid = c.sessionMID
			c.identityMu.Unlock()
		}
		if mid <= 0 {
			return ""
		}
		return "mid:" + strconv.FormatInt(mid, 10)
	}
	c.fingerprintMu.Lock()
	defer c.fingerprintMu.Unlock()
	records := make([]CookieRecord, 0)
	for _, record := range filterAnonymousCookies(c.searchJar.snapshot()) {
		if isBilibiliDeviceID(record.Name) || strings.EqualFold(record.Name, "buvid_fp") || strings.EqualFold(record.Name, "_uuid") {
			records = append(records, record)
		}
	}
	if len(records) == 0 {
		return ""
	}
	payload, _ := json.Marshal(records)
	digest := sha256.Sum256(payload)
	return "anonymous:" + hex.EncodeToString(digest[:])
}

func (c *Client) setSessionMIDFromCookies(records []CookieRecord) {
	c.identityMu.Lock()
	c.sessionMID = 0
	c.identityMu.Unlock()
	for _, record := range records {
		if !strings.EqualFold(strings.TrimSpace(record.Name), "DedeUserID") {
			continue
		}
		mid, err := strconv.ParseInt(strings.TrimSpace(record.Value), 10, 64)
		if err == nil && mid > 0 {
			c.identityMu.Lock()
			c.sessionMID = mid
			c.identityMu.Unlock()
		}
		return
	}
}

// ResetAnonymousIdentity clears only anonymous device state. Account cookies
// and their session cache partition are never modified.
func (c *Client) ResetAnonymousIdentity(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	jar, err := newAnonymousJar()
	if err != nil {
		return err
	}
	if c.anonymousCookieStore != nil {
		if err := c.anonymousCookieStore.Clear(); err != nil && !errors.Is(err, ErrNoCookieFile) {
			return err
		}
	}
	c.fingerprintMu.Lock()
	c.searchJar = jar
	c.search.HTTP.Jar = jar
	c.fingerprintReady[SearchIdentityAnonymous] = false
	c.fingerprintMu.Unlock()
	c.wbiMu.Lock()
	delete(c.wbi, SearchIdentityAnonymous)
	c.wbiMu.Unlock()
	c.cacheMu.Lock()
	for key := range c.cache {
		if key.Identity == SearchIdentityAnonymous {
			delete(c.cache, key)
		}
	}
	filtered := c.cacheOrder[:0]
	for _, key := range c.cacheOrder {
		if key.Identity != SearchIdentityAnonymous {
			filtered = append(filtered, key)
		}
	}
	c.cacheOrder = filtered
	c.cacheMu.Unlock()
	return nil
}
