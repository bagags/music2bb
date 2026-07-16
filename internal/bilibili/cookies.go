package bilibili

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

type CookieRecord struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Domain string `json:"domain"`
	Path   string `json:"path"`
}

type persistentJar struct {
	jar     *cookiejar.Jar
	mu      sync.Mutex
	records map[string]CookieRecord
}

func newPersistentJar() (*persistentJar, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}
	return &persistentJar{jar: jar, records: make(map[string]CookieRecord)}, nil
}

func (j *persistentJar) Cookies(u *url.URL) []*http.Cookie { return j.jar.Cookies(u) }

func (j *persistentJar) SetCookies(u *url.URL, cookies []*http.Cookie) {
	j.jar.SetCookies(u, cookies)
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, cookie := range cookies {
		domain := cookie.Domain
		if domain == "" {
			domain = u.Hostname()
		}
		path := cookie.Path
		if path == "" {
			path = "/"
		}
		key := cookie.Name + "\x00" + domain + "\x00" + path
		if cookie.MaxAge < 0 {
			delete(j.records, key)
			continue
		}
		j.records[key] = CookieRecord{Name: cookie.Name, Value: cookie.Value, Domain: domain, Path: path}
	}
}

func (j *persistentJar) load(records []CookieRecord, fallback *url.URL) {
	for _, record := range records {
		if record.Name == "" {
			continue
		}
		domain := strings.TrimSpace(record.Domain)
		target := fallback
		if domain != "" && !strings.Contains(domain, ":") {
			host := strings.TrimPrefix(domain, ".")
			target = &url.URL{Scheme: "https", Host: host, Path: "/"}
		}
		path := record.Path
		if path == "" {
			path = "/"
		}
		j.SetCookies(target, []*http.Cookie{{Name: record.Name, Value: record.Value, Domain: domain, Path: path}})
	}
}

func (j *persistentJar) snapshot() []CookieRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	records := make([]CookieRecord, 0, len(j.records))
	for _, record := range j.records {
		records = append(records, record)
	}
	sort.Slice(records, func(i, k int) bool {
		if records[i].Domain != records[k].Domain {
			return records[i].Domain < records[k].Domain
		}
		if records[i].Path != records[k].Path {
			return records[i].Path < records[k].Path
		}
		return records[i].Name < records[k].Name
	})
	return records
}

func (c *Client) HasCookies() bool {
	return c != nil && c.cookieStore != nil && c.cookieStore.Exists()
}

func (c *Client) LoadCookies() (bool, error) {
	if c.cookieStore == nil {
		return false, ErrNoCookieFile
	}
	records, err := c.cookieStore.Load()
	if errors.Is(err, ErrNoCookieFile) || errors.Is(err, os.ErrNotExist) {
		return false, ErrNoCookieFile
	}
	if err != nil {
		return false, err
	}
	home, err := url.Parse(c.endpoints.Home)
	if err != nil {
		return false, err
	}
	c.accountJar.load(records, home)
	c.fingerprintMu.Lock()
	c.fingerprintReady = false
	c.fingerprintMu.Unlock()
	return true, nil
}

func (c *Client) SaveCookies() error {
	if c.cookieStore == nil {
		return nil
	}
	return c.cookieStore.Save(c.accountJar.snapshot())
}

type fileCookieStore struct {
	path string
}

func (s fileCookieStore) Exists() bool {
	if s.path == "" {
		return false
	}
	info, err := os.Stat(s.path)
	return err == nil && !info.IsDir()
}

func (s fileCookieStore) Load() ([]CookieRecord, error) {
	if s.path == "" {
		return nil, ErrNoCookieFile
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNoCookieFile
	}
	if err != nil {
		return nil, err
	}
	var records []CookieRecord
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, err
	}
	return records, nil
}

func (s fileCookieStore) Save(records []CookieRecord) error {
	if s.path == "" {
		return nil
	}
	data, err := json.MarshalIndent(records, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".cookies-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, s.path)
}

func (s fileCookieStore) Clear() error {
	if s.path == "" {
		return nil
	}
	if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}
