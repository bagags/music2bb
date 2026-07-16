package bilibili

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"
)

const searchCacheSchemaVersion = 1

type fileSearchCache struct {
	dir string
	mu  sync.Mutex
}

type searchCacheDocument struct {
	Version int              `json:"version"`
	Entry   SearchCacheEntry `json:"entry"`
}

// NewFileSearchCache creates the versioned per-entry JSON search cache used
// by production wiring. Files are created lazily on the first successful
// search response.
func NewFileSearchCache(dir string) SearchCache {
	return &fileSearchCache{dir: filepath.Clean(dir)}
}

func (c *fileSearchCache) Get(ctx context.Context, key SearchCacheKey) (SearchCacheEntry, bool, error) {
	if err := ctx.Err(); err != nil {
		return SearchCacheEntry{}, false, err
	}
	path, err := c.path(key)
	if err != nil {
		return SearchCacheEntry{}, false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	payload, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return SearchCacheEntry{}, false, nil
	}
	if err != nil {
		return SearchCacheEntry{}, false, fmt.Errorf("read search cache: %w", err)
	}
	var document searchCacheDocument
	if err := json.Unmarshal(payload, &document); err != nil || document.Version != searchCacheSchemaVersion || !reflect.DeepEqual(document.Entry.Key, key) || document.Entry.StoredAt.IsZero() {
		if quarantineErr := quarantineCacheFile(path); quarantineErr != nil {
			return SearchCacheEntry{}, false, fmt.Errorf("quarantine corrupt search cache: %w", quarantineErr)
		}
		return SearchCacheEntry{}, false, nil
	}
	return cloneSearchCacheEntry(document.Entry), true, nil
}

func (c *fileSearchCache) Put(ctx context.Context, entry SearchCacheEntry) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	path, err := c.path(entry.Key)
	if err != nil {
		return err
	}
	payload, err := json.MarshalIndent(searchCacheDocument{Version: searchCacheSchemaVersion, Entry: cloneSearchCacheEntry(entry)}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode search cache: %w", err)
	}
	payload = append(payload, '\n')
	c.mu.Lock()
	defer c.mu.Unlock()
	return atomicWriteSearchState(path, payload)
}

func (c *fileSearchCache) path(key SearchCacheKey) (string, error) {
	if c == nil || c.dir == "" || c.dir == "." {
		return "", errors.New("search cache directory is required")
	}
	payload, err := json.Marshal(key)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return filepath.Join(c.dir, hex.EncodeToString(digest[:])+".json"), nil
}

func quarantineCacheFile(path string) error {
	suffix := time.Now().UTC().Format("20060102T150405.000000000Z")
	return os.Rename(path, path+".corrupt-"+suffix)
}

func atomicWriteSearchState(path string, payload []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create search cache directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".search-*.tmp")
	if err != nil {
		return fmt.Errorf("create search cache temporary file: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(payload); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func cloneSearchCacheEntry(entry SearchCacheEntry) SearchCacheEntry {
	entry.Videos = cloneVideos(entry.Videos)
	return entry
}
