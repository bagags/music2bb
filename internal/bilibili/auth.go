package bilibili

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bagags/music2bb-go/internal/netx"
)

type navData struct {
	MID     flexibleInt64 `json:"mid"`
	Name    string        `json:"uname"`
	IsLogin bool          `json:"isLogin"`
	WBIImg  struct {
		ImgURL string `json:"img_url"`
		SubURL string `json:"sub_url"`
	} `json:"wbi_img"`
}

func (c *Client) Account(ctx context.Context) (Account, error) {
	var data navData
	if err := c.get(ctx, c.account, "account", c.endpoints.Nav, nil, &data); err != nil {
		return Account{}, err
	}
	loggedIn := data.IsLogin || data.Name != "" || data.MID != 0
	if data.MID > 0 {
		c.identityMu.Lock()
		c.sessionMID = int64(data.MID)
		c.identityMu.Unlock()
	}
	return Account{MID: int64(data.MID), Name: data.Name, LoggedIn: loggedIn}, nil
}

func (c *Client) IsLoggedIn(ctx context.Context) (bool, error) {
	account, err := c.Account(ctx)
	return account.LoggedIn, err
}

// Logout clears the persisted login and all authentication state held by this
// client. It does not make a remote request to revoke the Bilibili session.
func (c *Client) Logout(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	accountJar, err := newPersistentJar()
	if err != nil {
		return err
	}
	if err := c.cookieStore.Clear(); err != nil {
		return err
	}
	c.accountJar = accountJar
	c.account.HTTP.Jar = accountJar
	c.sessionSearch.HTTP.Jar = accountJar
	c.resetIdentityState(SearchIdentitySession)
	c.identityMu.Lock()
	c.sessionMID = 0
	c.identityMu.Unlock()
	return nil
}

type qrGenerateData struct {
	URL       string `json:"url"`
	QRCodeKey string `json:"qrcode_key"`
}

type qrPollData struct {
	URL     string `json:"url"`
	Cookie  string `json:"cookie"`
	Message string `json:"message"`
	Code    int64  `json:"code"`
}

func (c *Client) QRLogin(ctx context.Context, options LoginOptions) (Account, error) {
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Minute
	}
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	deadline := c.now().Add(timeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var generated qrGenerateData
	if err := c.get(ctx, c.account, "qr generate", c.endpoints.QRGenerate, nil, &generated); err != nil {
		return Account{}, err
	}
	if generated.URL == "" || generated.QRCodeKey == "" {
		return Account{}, &APIError{Operation: "qr generate", Message: "response omitted QR payload or key"}
	}
	notify(options.Observer, Event{Kind: EventQRPayload, QRPayload: generated.URL})

	scanned := false
	for {
		if err := ctx.Err(); err != nil {
			return Account{}, err
		}
		if !c.now().Before(deadline) {
			return Account{}, context.DeadlineExceeded
		}
		var polled qrPollData
		if err := c.get(ctx, c.account, "qr poll", c.endpoints.QRPoll, url.Values{"qrcode_key": {generated.QRCodeKey}}, &polled); err != nil {
			return Account{}, err
		}
		switch polled.Code {
		case 0:
			if polled.Cookie != "" {
				c.applyCookieString(polled.Cookie)
			}
			if err := c.SaveCookies(); err != nil {
				notify(options.Observer, Event{Kind: EventWarning, Message: "login succeeded but cookies could not be persisted: " + err.Error()})
			}
			return c.Account(ctx)
		case 86038:
			return Account{}, &APIError{Operation: "qr poll", Code: polled.Code, Message: firstNonEmpty(polled.Message, "QR code expired")}
		case 86090:
			if !scanned {
				notify(options.Observer, Event{Kind: EventQRScanned, Message: "QR code scanned; waiting for confirmation"})
				scanned = true
			}
		case 86101:
			// Waiting for the QR code to be scanned.
		default:
			return Account{}, &APIError{Operation: "qr poll", Code: polled.Code, Message: firstNonEmpty(polled.Message, "unexpected QR state")}
		}
		if err := c.sleep(ctx, pollInterval); err != nil {
			return Account{}, err
		}
	}
}

func notify(observer Observer, event Event) {
	if observer != nil {
		observer.ObserveBilibili(event)
	}
}

func (c *Client) applyCookieString(raw string) {
	u, err := url.Parse(c.endpoints.Home)
	if err != nil {
		return
	}
	cookies := make([]*http.Cookie, 0)
	domain := ""
	if u.Hostname() == "bilibili.com" || strings.HasSuffix(u.Hostname(), ".bilibili.com") {
		domain = ".bilibili.com"
	}
	for _, pair := range strings.Split(raw, ";") {
		name, value, ok := strings.Cut(strings.TrimSpace(pair), "=")
		if !ok || name == "" {
			continue
		}
		cookies = append(cookies, &http.Cookie{Name: name, Value: value, Domain: domain, Path: "/"})
	}
	c.accountJar.SetCookies(u, cookies)
	c.resetIdentityState(SearchIdentitySession)
}

func (c *Client) csrf() string {
	for _, endpoint := range []string{c.endpoints.Home, c.endpoints.Nav, c.endpoints.FavoriteCreate} {
		u, err := url.Parse(endpoint)
		if err != nil {
			continue
		}
		for _, cookie := range c.accountJar.Cookies(u) {
			if cookie.Name == "bili_jct" {
				return cookie.Value
			}
		}
	}
	return ""
}

func (c *Client) ensureFingerprint(ctx context.Context, identity SearchIdentity) error {
	c.fingerprintMu.Lock()
	defer c.fingerprintMu.Unlock()
	home, err := url.Parse(c.endpoints.Home)
	if err != nil {
		return err
	}
	if c.fingerprintReady[identity] {
		if identity == SearchIdentityAnonymous {
			c.sanitizeAnonymousJar(home)
		}
		return nil
	}
	jar := c.accountJar
	client := c.account
	if identity == SearchIdentityAnonymous {
		jar = c.searchJar
		client = c.search
		c.sanitizeAnonymousJar(home)
	}
	found := false
	for _, cookie := range jar.Cookies(home) {
		if isBilibiliDeviceID(cookie.Name) {
			found = true
		}
	}
	if !found {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints.Home, nil)
		if err != nil {
			return err
		}
		c.addHeaders(req)
		resp, err := client.Do(req)
		if err != nil {
			return err
		}
		resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			apiErr := &APIError{Operation: "fingerprint", StatusCode: resp.StatusCode}
			classifyRiskControl(apiErr)
			return apiErr
		}
		if identity == SearchIdentityAnonymous {
			c.sanitizeAnonymousJar(home)
			_ = c.SaveAnonymousCookies()
		} else {
			_ = c.SaveCookies()
		}
	}
	c.fingerprintReady[identity] = true
	return nil
}

func (c *Client) sanitizeAnonymousJar(home *url.URL) {
	snapshot := c.searchJar.snapshot()
	records := filterAnonymousCookies(snapshot)
	if len(records) == len(snapshot) {
		return
	}
	jar, err := newAnonymousJar()
	if err != nil {
		return
	}
	jar.load(records, home)
	c.searchJar = jar
	c.search.HTTP.Jar = jar
}

func (c *Client) searchClient(identity SearchIdentity) *netx.Client {
	if identity == SearchIdentitySession {
		return c.sessionSearch
	}
	return c.search
}

func (c *Client) resetIdentityState(identity SearchIdentity) {
	c.fingerprintMu.Lock()
	c.fingerprintReady[identity] = false
	c.fingerprintMu.Unlock()
	c.wbiMu.Lock()
	delete(c.wbi, identity)
	c.wbiMu.Unlock()
}

func isBilibiliDeviceID(name string) bool {
	return name == "buvid3" || name == "buvid4"
}
