package browser

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const maxArchiveBytes int64 = 2 << 30

// ManagerOptions allows deterministic cache, platform, manifest, and network
// injection. Production callers normally use NewManager.
type ManagerOptions struct {
	CacheDir   string
	Platform   string
	Manifest   Manifest
	HTTPClient *http.Client
}

// Manager owns the verified, pinned browser cache. It never downloads during
// Status or Executable; Install is the sole network-mutating operation.
type Manager struct {
	cacheDir string
	platform string
	manifest Manifest
	http     *http.Client
	mu       sync.Mutex
}

// Status describes both physical presence and cryptographic verification.
type Status struct {
	CacheDir         string
	Platform         string
	Revision         int
	ApproxBytes      int64
	ExecutablePath   string
	ExpectedSHA256   string
	ArchiveSHA256    string
	ExecutableSHA256 string
	Present          bool
	Installed        bool
	Verified         bool
}

// InstallOptions separates a user's explicit approval from whether a prompt
// was possible. Manager never reads from a terminal itself.
type InstallOptions struct {
	Approved       bool
	NonInteractive bool
}

type installRecord struct {
	Platform         string `json:"platform"`
	Revision         int    `json:"revision"`
	ArchiveSHA256    string `json:"archive_sha256"`
	Executable       string `json:"executable"`
	ExecutableSHA256 string `json:"executable_sha256"`
}

// DefaultCacheDir returns an OS-appropriate cache path outside the release
// binary and configuration directory.
func DefaultCacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "kg2bb", "browser"), nil
}

func NewManager(cacheDir string) (*Manager, error) {
	manifest, err := loadEmbeddedManifest()
	if err != nil {
		return nil, err
	}
	return NewManagerWithOptions(ManagerOptions{CacheDir: cacheDir, Manifest: manifest})
}

func NewManagerWithOptions(opts ManagerOptions) (*Manager, error) {
	cacheDir := strings.TrimSpace(opts.CacheDir)
	if cacheDir == "" {
		var err error
		cacheDir, err = DefaultCacheDir()
		if err != nil {
			return nil, fmt.Errorf("browser cache directory: %w", err)
		}
	}
	cacheDir, err := filepath.Abs(cacheDir)
	if err != nil {
		return nil, fmt.Errorf("browser cache directory: %w", err)
	}
	platform := opts.Platform
	if platform == "" {
		platform = currentPlatform()
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Minute}
	}
	if opts.Manifest.Artifacts == nil {
		return nil, errors.New("browser manifest has no artifacts")
	}
	return &Manager{cacheDir: filepath.Clean(cacheDir), platform: platform, manifest: opts.Manifest, http: client}, nil
}

func (m *Manager) artifact() (Artifact, error) {
	artifact, ok := m.manifest.Artifacts[m.platform]
	if !ok || artifact.Revision == 0 || artifact.Executable == "" {
		return Artifact{}, &Error{Kind: ErrorUnsupportedPlatform, Op: "manifest", Err: fmt.Errorf("no artifact for %s", m.platform)}
	}
	return artifact, nil
}

func (m *Manager) installDir(artifact Artifact) string {
	return filepath.Join(m.cacheDir, fmt.Sprintf("chromium-%d", artifact.Revision))
}

func (m *Manager) executablePath(artifact Artifact) string {
	return filepath.Join(m.installDir(artifact), filepath.FromSlash(artifact.Executable))
}

func (m *Manager) recordPath() string { return filepath.Join(m.cacheDir, "installed.json") }

func (m *Manager) Status(ctx context.Context) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	artifact, err := m.artifact()
	if err != nil {
		return Status{CacheDir: m.cacheDir, Platform: m.platform}, err
	}
	status := Status{
		CacheDir:       m.cacheDir,
		Platform:       m.platform,
		Revision:       artifact.Revision,
		ApproxBytes:    artifact.ApproxBytes,
		ExecutablePath: m.executablePath(artifact),
		ExpectedSHA256: strings.ToLower(artifact.SHA256),
	}
	if _, err := os.Stat(status.ExecutablePath); err == nil {
		status.Present = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return status, fmt.Errorf("stat browser executable: %w", err)
	}

	recordBytes, err := os.ReadFile(m.recordPath())
	if errors.Is(err, os.ErrNotExist) {
		return status, nil
	}
	if err != nil {
		return status, fmt.Errorf("read browser verification record: %w", err)
	}
	var record installRecord
	if err := json.Unmarshal(recordBytes, &record); err != nil {
		return status, nil
	}
	status.ArchiveSHA256 = strings.ToLower(record.ArchiveSHA256)
	status.ExecutableSHA256 = strings.ToLower(record.ExecutableSHA256)
	if !status.Present || !validChecksum(artifact.SHA256) ||
		record.Platform != m.platform || record.Revision != artifact.Revision ||
		!strings.EqualFold(record.ArchiveSHA256, artifact.SHA256) ||
		filepath.Clean(record.Executable) != filepath.Clean(artifact.Executable) ||
		!validChecksum(record.ExecutableSHA256) {
		return status, nil
	}
	actualExecutableHash, err := hashFile(ctx, status.ExecutablePath)
	if err != nil {
		return status, fmt.Errorf("verify browser executable: %w", err)
	}
	status.ExecutableSHA256 = actualExecutableHash
	status.Verified = strings.EqualFold(actualExecutableHash, record.ExecutableSHA256)
	status.Installed = status.Verified
	return status, nil
}

// Executable returns only a browser installed through a checksum-verified
// Install. It never falls back to a system browser or Rod's auto-downloader.
func (m *Manager) Executable(ctx context.Context) (string, error) {
	status, err := m.Status(ctx)
	if err != nil {
		return "", err
	}
	if !status.Verified {
		return "", &Error{Kind: ErrorNotInstalled, Op: "executable", Err: fmt.Errorf("run browser install first")}
	}
	return status.ExecutablePath, nil
}

// Install downloads, hashes, and only then extracts the pinned archive.
func (m *Manager) Install(ctx context.Context, opts InstallOptions) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	if !opts.Approved {
		kind := ErrorApprovalRequired
		if opts.NonInteractive {
			kind = ErrorNonInteractive
		}
		return Status{}, &Error{Kind: kind, Op: "install", Err: errors.New("browser download requires explicit approval")}
	}
	artifact, err := m.artifact()
	if err != nil {
		return Status{}, err
	}
	if artifact.Revision != PinnedRevision && m.manifest.Schema == 1 {
		// Tests may inject another revision using a non-production platform. The
		// embedded production platforms are always pinned to PinnedRevision.
		if m.platform == currentPlatform() {
			return Status{}, &Error{Kind: ErrorUnverifiedArtifact, Op: "install", Err: fmt.Errorf("revision %d is not pinned revision %d", artifact.Revision, PinnedRevision)}
		}
	}
	if !validChecksum(artifact.SHA256) {
		return Status{}, &Error{Kind: ErrorUnverifiedArtifact, Op: "install", Err: errors.New("embedded archive checksum is absent or a placeholder")}
	}
	if artifact.URL == "" {
		return Status{}, &Error{Kind: ErrorUnverifiedArtifact, Op: "install", Err: errors.New("artifact URL is absent")}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if status, statusErr := m.Status(ctx); statusErr == nil && status.Verified {
		return status, nil
	}
	if err := os.MkdirAll(m.cacheDir, 0o700); err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "install", Err: err}
	}

	archive, actualHash, err := m.download(ctx, artifact)
	if err != nil {
		return Status{}, err
	}
	defer os.Remove(archive)
	if !strings.EqualFold(actualHash, artifact.SHA256) {
		return Status{}, &Error{Kind: ErrorChecksumMismatch, Op: "verify", Err: fmt.Errorf("got %s, want %s", actualHash, strings.ToLower(artifact.SHA256))}
	}

	tempDir, err := os.MkdirTemp(m.cacheDir, ".extract-*")
	if err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "extract", Err: err}
	}
	defer os.RemoveAll(tempDir)
	if err := extractZip(ctx, archive, tempDir); err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "extract", Err: err}
	}
	tempExecutable := filepath.Join(tempDir, filepath.FromSlash(artifact.Executable))
	if _, err := os.Stat(tempExecutable); err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "extract", Err: fmt.Errorf("archive lacks %s: %w", artifact.Executable, err)}
	}
	executableHash, err := hashFile(ctx, tempExecutable)
	if err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "verify executable", Err: err}
	}

	installDir := m.installDir(artifact)
	if err := os.RemoveAll(installDir); err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "replace", Err: err}
	}
	if err := os.Rename(tempDir, installDir); err != nil {
		return Status{}, &Error{Kind: ErrorInstall, Op: "replace", Err: err}
	}
	record := installRecord{
		Platform: m.platform, Revision: artifact.Revision,
		ArchiveSHA256: strings.ToLower(actualHash), Executable: artifact.Executable,
		ExecutableSHA256: executableHash,
	}
	if err := writeRecordAtomic(m.recordPath(), record); err != nil {
		_ = os.RemoveAll(installDir)
		return Status{}, &Error{Kind: ErrorInstall, Op: "record", Err: err}
	}
	status, err := m.Status(ctx)
	if err != nil {
		return Status{}, err
	}
	if !status.Verified {
		return status, &Error{Kind: ErrorInstall, Op: "verify installed browser", Err: errors.New("verification record did not validate")}
	}
	return status, nil
}

func (m *Manager) download(ctx context.Context, artifact Artifact) (string, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, artifact.URL, nil)
	if err != nil {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: err}
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: fmt.Errorf("HTTP %s", resp.Status)}
	}
	if resp.ContentLength > maxArchiveBytes {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: fmt.Errorf("archive is larger than %d bytes", maxArchiveBytes)}
	}
	temp, err := os.CreateTemp(m.cacheDir, ".chromium-*.zip.partial")
	if err != nil {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: err}
	}
	path := temp.Name()
	keep := false
	defer func() {
		_ = temp.Close()
		if !keep {
			_ = os.Remove(path)
		}
	}()
	hash := sha256.New()
	written, err := io.Copy(io.MultiWriter(temp, hash), io.LimitReader(resp.Body, maxArchiveBytes+1))
	if err != nil {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: err}
	}
	if written > maxArchiveBytes {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: fmt.Errorf("archive exceeded %d bytes", maxArchiveBytes)}
	}
	if err := temp.Sync(); err != nil {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: err}
	}
	if err := temp.Close(); err != nil {
		return "", "", &Error{Kind: ErrorDownload, Op: "download", Err: err}
	}
	keep = true
	return path, hex.EncodeToString(hash.Sum(nil)), nil
}

// Clear removes only this manager's browser cache.
func (m *Manager) Clear(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if unsafeCachePath(m.cacheDir) {
		return &Error{Kind: ErrorClear, Op: "clear", Err: fmt.Errorf("refusing unsafe cache path %q", m.cacheDir)}
	}
	if err := os.RemoveAll(m.cacheDir); err != nil {
		return &Error{Kind: ErrorClear, Op: "clear", Err: err}
	}
	return nil
}

func unsafeCachePath(path string) bool {
	clean := filepath.Clean(strings.TrimSpace(path))
	if clean == "" || clean == "." || clean == string(filepath.Separator) {
		return true
	}
	volume := filepath.VolumeName(clean)
	return clean == volume+string(filepath.Separator)
}

func writeRecordAtomic(path string, record installRecord) error {
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".installed-*.json")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)
	if err := temp.Chmod(0o600); err != nil {
		temp.Close()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempPath, path)
}

func hashFile(ctx context.Context, path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	for {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			if _, err := hash.Write(buffer[:read]); err != nil {
				return "", err
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return "", readErr
		}
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func extractZip(ctx context.Context, archivePath, destination string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	destination = filepath.Clean(destination)
	var total uint64
	for _, entry := range reader.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		total += entry.UncompressedSize64
		if total > uint64(maxArchiveBytes) {
			return fmt.Errorf("expanded archive exceeded %d bytes", maxArchiveBytes)
		}
		target := filepath.Join(destination, filepath.FromSlash(entry.Name))
		if target != destination && !strings.HasPrefix(target, destination+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", entry.Name)
		}
		mode := entry.Mode()
		if entry.FileInfo().IsDir() {
			if err := os.MkdirAll(target, mode.Perm()|0o700); err != nil {
				return err
			}
			continue
		}
		if mode&os.ModeSymlink != 0 {
			link, err := readZipEntry(entry, 4096)
			if err != nil {
				return fmt.Errorf("read archive symlink %q: %w", entry.Name, err)
			}
			linkTarget := filepath.FromSlash(string(link))
			if filepath.IsAbs(linkTarget) {
				return fmt.Errorf("unsafe absolute symlink %q", entry.Name)
			}
			resolved := filepath.Clean(filepath.Join(filepath.Dir(target), linkTarget))
			if resolved != destination && !strings.HasPrefix(resolved, destination+string(filepath.Separator)) {
				return fmt.Errorf("unsafe archive symlink %q -> %q", entry.Name, string(link))
			}
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(linkTarget, target); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		source, err := entry.Open()
		if err != nil {
			return err
		}
		permissions := mode.Perm()
		if permissions == 0 {
			permissions = 0o644
		}
		output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions)
		if err != nil {
			source.Close()
			return err
		}
		_, copyErr := io.Copy(output, source)
		closeErr := output.Close()
		sourceErr := source.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if sourceErr != nil {
			return sourceErr
		}
	}
	return nil
}

func readZipEntry(entry *zip.File, limit int64) ([]byte, error) {
	source, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer source.Close()
	data, err := io.ReadAll(io.LimitReader(source, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("entry exceeds %d bytes", limit)
	}
	return data, nil
}
