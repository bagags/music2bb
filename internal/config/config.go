// Package config resolves persistent state paths, imports legacy Python state,
// and loads matcher lists without process-wide mutable globals.
package config

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

const (
	appName             = "kg2bb"
	migrationMarkerName = ".migration-v1"
)

//go:embed defaults/*.txt
var defaultLists embed.FS

// Options controls path resolution and first-run migration. Empty path fields
// use platform defaults. WorkingDir and ExecutableDir are injectable so tests
// and portable launchers do not depend on process-global directory state.
type Options struct {
	Dir           string
	CacheDir      string
	WorkingDir    string
	ExecutableDir string
	SkipMigration bool
}

// Paths contains all state locations used by the Go implementation.
type Paths struct {
	Dir             string
	CacheDir        string
	CookieFile      string
	BlockFile       string
	QualityFile     string
	UploaderFile    string
	MigrationMarker string
}

// MigrationResult reports what happened during the one-time legacy import.
type MigrationResult struct {
	AlreadyComplete bool
	Copied          []string
}

// Config is an immutable-by-convention snapshot. Load returns fresh keyword
// slices so callers cannot mutate embedded defaults or another engine.
type Config struct {
	Paths
	BlockKeywords     []string
	QualityKeywords   []string
	WeightedUploaders []string
	Migration         MigrationResult
}

// Resolve computes config and cache paths without creating them.
func Resolve(options Options) (Paths, error) {
	dir := strings.TrimSpace(options.Dir)
	cacheDir := strings.TrimSpace(options.CacheDir)
	if dir == "" || cacheDir == "" {
		defaultDir, defaultCache, err := defaultBaseDirs()
		if err != nil {
			return Paths{}, err
		}
		if dir == "" {
			dir = defaultDir
		}
		if cacheDir == "" {
			cacheDir = defaultCache
		}
	}
	dir, err := filepath.Abs(dir)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config directory: %w", err)
	}
	cacheDir, err = filepath.Abs(cacheDir)
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cache directory: %w", err)
	}
	return Paths{
		Dir:             dir,
		CacheDir:        cacheDir,
		CookieFile:      filepath.Join(dir, "cookies", "bilibili.json"),
		BlockFile:       filepath.Join(dir, "b.txt"),
		QualityFile:     filepath.Join(dir, "w.txt"),
		UploaderFile:    filepath.Join(dir, "w-up.txt"),
		MigrationMarker: filepath.Join(dir, migrationMarkerName),
	}, nil
}

// Load prepares the state directory, performs first-run migration, and loads
// external keyword lists when present. An existing external file overrides the
// embedded list even when it intentionally contains no keywords.
func Load(options Options) (Config, error) {
	paths, err := Resolve(options)
	if err != nil {
		return Config{}, err
	}
	if err := os.MkdirAll(paths.Dir, 0o700); err != nil {
		return Config{}, fmt.Errorf("create config directory: %w", err)
	}

	migration := MigrationResult{}
	if !options.SkipMigration {
		legacyDirs, err := resolveLegacyDirs(options)
		if err != nil {
			return Config{}, err
		}
		migration, err = Migrate(paths, legacyDirs...)
		if err != nil {
			return Config{}, err
		}
	}

	blocks, err := loadList(paths.BlockFile, "defaults/b.txt")
	if err != nil {
		return Config{}, fmt.Errorf("load block keywords: %w", err)
	}
	quality, err := loadList(paths.QualityFile, "defaults/w.txt")
	if err != nil {
		return Config{}, fmt.Errorf("load quality keywords: %w", err)
	}
	uploaders, err := loadList(paths.UploaderFile, "defaults/w-up.txt")
	if err != nil {
		return Config{}, fmt.Errorf("load uploader keywords: %w", err)
	}
	return Config{
		Paths:             paths,
		BlockKeywords:     blocks,
		QualityKeywords:   quality,
		WeightedUploaders: uploaders,
		Migration:         migration,
	}, nil
}

// Migrate imports missing legacy files from the supplied directories. Earlier
// directories take priority. Sources are only read and destination files are
// written atomically.
func Migrate(paths Paths, legacyDirs ...string) (MigrationResult, error) {
	if _, err := os.Stat(paths.MigrationMarker); err == nil {
		return MigrationResult{AlreadyComplete: true}, nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return MigrationResult{}, fmt.Errorf("inspect migration marker: %w", err)
	}

	type candidate struct {
		name       string
		legacyPath func(string) string
		target     string
		mode       fs.FileMode
	}
	candidates := []candidate{
		{
			name:       "bilibili.json",
			legacyPath: func(dir string) string { return filepath.Join(dir, ".cookies", "bilibili.json") },
			target:     paths.CookieFile,
			mode:       0o600,
		},
		{name: "b.txt", legacyPath: func(dir string) string { return filepath.Join(dir, "b.txt") }, target: paths.BlockFile, mode: 0o644},
		{name: "w.txt", legacyPath: func(dir string) string { return filepath.Join(dir, "w.txt") }, target: paths.QualityFile, mode: 0o644},
		{name: "w-up.txt", legacyPath: func(dir string) string { return filepath.Join(dir, "w-up.txt") }, target: paths.UploaderFile, mode: 0o644},
	}

	result := MigrationResult{}
	for _, item := range candidates {
		exists, err := pathExists(item.target)
		if err != nil {
			return MigrationResult{}, fmt.Errorf("inspect migration destination %s: %w", item.target, err)
		}
		if exists {
			continue
		}
		for _, legacyDir := range uniqueNonEmptyPaths(legacyDirs) {
			source := item.legacyPath(legacyDir)
			data, err := os.ReadFile(source)
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			if err != nil {
				return MigrationResult{}, fmt.Errorf("read legacy %s: %w", source, err)
			}
			if err := atomicWriteFile(item.target, data, item.mode); err != nil {
				return MigrationResult{}, fmt.Errorf("migrate %s: %w", item.name, err)
			}
			result.Copied = append(result.Copied, item.name)
			break
		}
	}
	if err := atomicWriteFile(paths.MigrationMarker, []byte("1\n"), 0o600); err != nil {
		return MigrationResult{}, fmt.Errorf("record migration completion: %w", err)
	}
	return result, nil
}

func defaultBaseDirs() (string, string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", "", fmt.Errorf("resolve home directory: %w", err)
	}
	switch runtime.GOOS {
	case "darwin":
		return filepath.Join(home, "Library", "Application Support", appName), filepath.Join(home, "Library", "Caches", appName), nil
	case "windows":
		appData := os.Getenv("APPDATA")
		if appData == "" {
			appData = filepath.Join(home, "AppData", "Roaming")
		}
		localAppData := os.Getenv("LOCALAPPDATA")
		if localAppData == "" {
			localAppData = filepath.Join(home, "AppData", "Local")
		}
		return filepath.Join(appData, appName), filepath.Join(localAppData, appName), nil
	default:
		configBase := os.Getenv("XDG_CONFIG_HOME")
		if configBase == "" {
			configBase = filepath.Join(home, ".config")
		}
		cacheBase := os.Getenv("XDG_CACHE_HOME")
		if cacheBase == "" {
			cacheBase = filepath.Join(home, ".cache")
		}
		return filepath.Join(configBase, appName), filepath.Join(cacheBase, appName), nil
	}
}

func resolveLegacyDirs(options Options) ([]string, error) {
	workingDir := options.WorkingDir
	if workingDir == "" {
		var err error
		workingDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("resolve working directory for migration: %w", err)
		}
	}
	executableDir := options.ExecutableDir
	if executableDir == "" {
		executable, err := os.Executable()
		if err != nil {
			return nil, fmt.Errorf("resolve executable directory for migration: %w", err)
		}
		executableDir = filepath.Dir(executable)
	}
	return uniqueNonEmptyPaths([]string{workingDir, executableDir}), nil
}

func loadList(externalPath, embeddedPath string) ([]string, error) {
	data, err := os.ReadFile(externalPath)
	if errors.Is(err, fs.ErrNotExist) {
		data, err = defaultLists.ReadFile(embeddedPath)
	}
	if err != nil {
		return nil, err
	}
	return parseKeywords(data), nil
}

func parseKeywords(data []byte) []string {
	lines := strings.Split(string(data), "\n")
	keywords := make([]string, 0, len(lines))
	for _, line := range lines {
		keyword := strings.TrimSpace(line)
		if keyword == "" || strings.HasPrefix(keyword, "#") {
			continue
		}
		keywords = append(keywords, keyword)
	}
	return keywords
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func uniqueNonEmptyPaths(paths []string) []string {
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		if strings.TrimSpace(path) == "" {
			continue
		}
		clean := filepath.Clean(path)
		if !slices.Contains(result, clean) {
			result = append(result, clean)
		}
	}
	return result
}

func atomicWriteFile(path string, data []byte, mode fs.FileMode) (err error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".kg2bb-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer func() {
		_ = temporary.Close()
		if err != nil {
			_ = os.Remove(temporaryName)
		}
	}()
	if err = temporary.Chmod(mode); err != nil {
		return err
	}
	if _, err = temporary.Write(data); err != nil {
		return err
	}
	if err = temporary.Sync(); err != nil {
		return err
	}
	if err = temporary.Close(); err != nil {
		return err
	}
	if err = os.Rename(temporaryName, path); err != nil {
		return err
	}
	return os.Chmod(path, mode)
}
