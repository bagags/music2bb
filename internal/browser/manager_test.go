package browser

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) { return f(request) }

func TestInstallRequiresExplicitApproval(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	archive := testArchive(t, "test/chrome", "binary")
	manager := testManager(t, server.Client(), server.URL, archive)
	_, err := manager.Install(context.Background(), InstallOptions{NonInteractive: true})
	if !IsKind(err, ErrorNonInteractive) {
		t.Fatalf("Install error = %v, want %s", err, ErrorNonInteractive)
	}
	if calls.Load() != 0 {
		t.Fatalf("download calls = %d, want 0", calls.Load())
	}

	_, err = manager.Install(context.Background(), InstallOptions{})
	if !IsKind(err, ErrorApprovalRequired) {
		t.Fatalf("Install error = %v, want %s", err, ErrorApprovalRequired)
	}
}

func TestInstallFailsClosedWithPlaceholderManifest(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
	}))
	defer server.Close()

	manifest := Manifest{Schema: 1, Artifacts: map[string]Artifact{
		"test/amd64": {Revision: 7, URL: server.URL, Executable: "test/chrome"},
	}}
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "test/amd64", Manifest: manifest, HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = manager.Install(context.Background(), InstallOptions{Approved: true})
	if !IsKind(err, ErrorUnverifiedArtifact) {
		t.Fatalf("Install error = %v, want %s", err, ErrorUnverifiedArtifact)
	}
	if calls.Load() != 0 {
		t.Fatalf("download calls = %d, want 0", calls.Load())
	}
}

func TestSchemaTwoManifestRequiresExactProvenance(t *testing.T) {
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "test/amd64",
		Manifest: Manifest{Schema: 2, Artifacts: map[string]Artifact{
			"test/amd64": {Revision: 7, SHA256: testHash("archive"), Executable: "test/chrome"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Status(context.Background()); !IsKind(err, ErrorUnverifiedArtifact) {
		t.Fatalf("Status error = %v, want %s", err, ErrorUnverifiedArtifact)
	}
}

func TestEmbeddedManifestPinsVerifiedArtifacts(t *testing.T) {
	manifest, err := loadEmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	expected := map[string]struct {
		version      string
		revision     int
		origin       string
		availability string
	}{
		"darwin/amd64":  {version: "152.0.7951.0", revision: 1661832, origin: "chromium-snapshot"},
		"darwin/arm64":  {version: "152.0.7951.0", revision: 1661829, origin: "chromium-snapshot"},
		"linux/amd64":   {version: "152.0.7951.0", revision: 1661846, origin: "chromium-snapshot"},
		"linux/arm64":   {version: "152.0.7951.0", revision: 1661846, origin: "music2bb-build", availability: "pending-build"},
		"windows/amd64": {version: "152.0.7950.0", revision: 1661781, origin: "chromium-snapshot"},
		"windows/arm64": {version: "152.0.7950.0", revision: 1661807, origin: "chromium-snapshot"},
	}
	if len(manifest.Artifacts) != len(expected) {
		t.Errorf("manifest artifact count = %d, want %d", len(manifest.Artifacts), len(expected))
	}
	for platform, want := range expected {
		artifact, ok := manifest.Artifacts[platform]
		if !ok {
			t.Errorf("manifest is missing %s", platform)
			continue
		}
		if artifact.Version != want.version {
			t.Errorf("%s version = %q, want %s", platform, artifact.Version, want.version)
		}
		if artifact.Revision != want.revision {
			t.Errorf("%s revision = %d, want %d", platform, artifact.Revision, want.revision)
		}
		if artifact.Origin != want.origin || artifact.Availability != want.availability {
			t.Errorf("%s origin/availability = %q/%q, want %q/%q", platform, artifact.Origin, artifact.Availability, want.origin, want.availability)
		}
		if err := validateArtifactProvenance(artifact); err != nil {
			t.Errorf("%s has invalid provenance: %v", platform, err)
		}
		if artifact.URL == "" || artifact.Executable == "" || (artifact.Availability == "" && artifact.ArchiveBytes <= 0) {
			t.Errorf("%s has incomplete artifact metadata: %#v", platform, artifact)
		}
	}
}

func TestInstallVerifiesArchiveAndExecutable(t *testing.T) {
	archive := testArchive(t, "test/chrome", "verified browser")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.Write(archive)
	}))
	defer server.Close()

	manager := testManager(t, server.Client(), server.URL, archive)
	status, err := manager.Install(context.Background(), InstallOptions{Approved: true})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Installed || !status.Verified || !status.Present {
		t.Fatalf("status = %+v, want installed, verified, and present", status)
	}
	contents, err := os.ReadFile(status.ExecutablePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(contents) != "verified browser" {
		t.Fatalf("executable contents = %q", contents)
	}
	path, err := manager.Executable(context.Background())
	if err != nil || path != status.ExecutablePath {
		t.Fatalf("Executable = %q, %v; want %q", path, err, status.ExecutablePath)
	}

	if err := os.WriteFile(status.ExecutablePath, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	status, err = manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Verified || status.Installed {
		t.Fatalf("tampered status = %+v, want unverified", status)
	}
	if _, err := manager.Executable(context.Background()); !IsKind(err, ErrorNotInstalled) {
		t.Fatalf("Executable error = %v, want %s", err, ErrorNotInstalled)
	}
}

func TestSystemBrowserPreventsDownloadAndNeedsNoApproval(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		http.Error(w, "must not download", http.StatusInternalServerError)
	}))
	defer server.Close()

	executable := filepath.Join(t.TempDir(), "chromium")
	if err := os.WriteFile(executable, []byte("system browser"), 0o755); err != nil {
		t.Fatal(err)
	}
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "linux/amd64", ExecutablePath: executable,
		Manifest: Manifest{Schema: 1, Artifacts: map[string]Artifact{
			"linux/amd64": {
				Revision: 7, URL: server.URL, SHA256: testHash("archive"), Executable: "test/chrome",
			},
		}},
		HTTPClient: server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Install(context.Background(), InstallOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if status.Source != SourceSystem || status.Bundled || !status.Installed || status.Verified || status.ExecutablePath != executable {
		t.Fatalf("status = %+v", status)
	}
	if calls.Load() != 0 {
		t.Fatalf("download calls = %d, want 0", calls.Load())
	}
}

func TestBrowserExecutablePrecedence(t *testing.T) {
	explicit := testExecutable(t, "explicit")
	environment := testExecutable(t, "environment")
	discovered := testExecutable(t, "discovered")
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "linux/amd64", ExecutablePath: explicit,
		Manifest: Manifest{Schema: 1, Artifacts: map[string]Artifact{"linux/amd64": {Revision: 1, Executable: "chrome"}}},
		Getenv: func(name string) string {
			if name == browserExecutableEnvironment {
				return environment
			}
			return ""
		},
		LookPath: func(string) (string, error) { return discovered, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ExecutablePath != explicit {
		t.Fatalf("selected executable = %q, want explicit %q", status.ExecutablePath, explicit)
	}
}

func TestBrowserExecutableEnvironmentPrecedesDiscovery(t *testing.T) {
	environment := testExecutable(t, "environment")
	discovered := testExecutable(t, "discovered")
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "linux/amd64",
		Manifest: Manifest{Schema: 1, Artifacts: map[string]Artifact{"linux/amd64": {Revision: 1, Executable: "chrome"}}},
		Getenv: func(name string) string {
			if name == browserExecutableEnvironment {
				return environment
			}
			return ""
		},
		LookPath: func(string) (string, error) { return discovered, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ExecutablePath != environment {
		t.Fatalf("selected executable = %q, want environment %q", status.ExecutablePath, environment)
	}
}

func TestAutomaticDiscoveryUsesChromiumOrGoogleChromeOnly(t *testing.T) {
	discovered := testExecutable(t, "google-chrome")
	var names []string
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "linux/amd64",
		Manifest: Manifest{Schema: 1, Artifacts: map[string]Artifact{"linux/amd64": {Revision: 1, Executable: "chrome"}}},
		Getenv:   func(string) string { return "" },
		LookPath: func(name string) (string, error) {
			names = append(names, name)
			if name == "google-chrome" {
				return discovered, nil
			}
			return "", os.ErrNotExist
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.ExecutablePath != discovered || status.Source != SourceSystem {
		t.Fatalf("status = %+v", status)
	}
	for _, name := range names {
		if strings.Contains(strings.ToLower(name), "edge") {
			t.Fatalf("discovery queried Edge candidate %q", name)
		}
	}
}

func TestInvalidExplicitBrowserFailsImmediately(t *testing.T) {
	_, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "linux/amd64", ExecutablePath: filepath.Join(t.TempDir(), "missing"),
		Manifest: Manifest{Schema: 1, Artifacts: map[string]Artifact{"linux/amd64": {Revision: 1, Executable: "chrome"}}},
	})
	if !IsKind(err, ErrorInvalidExecutable) {
		t.Fatalf("NewManagerWithOptions error = %v, want %s", err, ErrorInvalidExecutable)
	}
}

func TestManagedInstallReusesVerifiedCache(t *testing.T) {
	archive := testArchive(t, "test/chrome", "cached browser")
	var calls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		calls.Add(1)
		return &http.Response{
			StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header),
			Body: io.NopCloser(bytes.NewReader(archive)), ContentLength: int64(len(archive)), Request: request,
		}, nil
	})}
	manager := testManager(t, client, "https://example.test/chromium.zip", archive)
	for range 2 {
		status, err := manager.Install(context.Background(), InstallOptions{Approved: true})
		if err != nil {
			t.Fatal(err)
		}
		if status.Source != SourceManaged || !status.Verified {
			t.Fatalf("status = %+v", status)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("download calls = %d, want 1", calls.Load())
	}
}

func TestClearWithSystemBrowserLeavesExecutable(t *testing.T) {
	root := t.TempDir()
	executable := testExecutable(t, "chromium")
	cacheDir := filepath.Join(root, "managed")
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: cacheDir, Platform: "linux/amd64", ExecutablePath: executable,
		Manifest: Manifest{Schema: 1, Artifacts: map[string]Artifact{"linux/amd64": {Revision: 1, Executable: "chrome"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "managed"), []byte("cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(executable); err != nil {
		t.Fatalf("system executable removed: %v", err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache stat error = %v, want not exist", err)
	}
}

func TestInstallRejectsChecksumMismatchWithoutExtraction(t *testing.T) {
	archive := testArchive(t, "test/chrome", "unexpected")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(archive)
	}))
	defer server.Close()

	manager := testManager(t, server.Client(), server.URL, testArchive(t, "test/chrome", "expected"))
	_, err := manager.Install(context.Background(), InstallOptions{Approved: true})
	if !IsKind(err, ErrorChecksumMismatch) {
		t.Fatalf("Install error = %v, want %s", err, ErrorChecksumMismatch)
	}
	status, statusErr := manager.Status(context.Background())
	if statusErr != nil {
		t.Fatal(statusErr)
	}
	if status.Present || status.Installed || status.Verified {
		t.Fatalf("status = %+v, want no installed artifact", status)
	}
}

func TestClearRemovesOnlyBrowserCache(t *testing.T) {
	root := t.TempDir()
	cacheDir := filepath.Join(root, "music2bb", "browser")
	outside := filepath.Join(root, "keep")
	if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{Schema: 1, Artifacts: map[string]Artifact{
		"test/amd64": {Revision: 7, SHA256: testHash("x"), Executable: "test/chrome"},
	}}
	manager, err := NewManagerWithOptions(ManagerOptions{CacheDir: cacheDir, Platform: "test/amd64", Manifest: manifest})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cacheDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cacheDir, "junk"), []byte("junk"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := manager.Clear(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(cacheDir); !os.IsNotExist(err) {
		t.Fatalf("cache stat error = %v, want not exist", err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("outside file = %q, %v", data, err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	archive := testArchive(t, "../escape", "bad")
	archivePath := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(archivePath, archive, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(context.Background(), archivePath, t.TempDir()); err == nil {
		t.Fatal("expected unsafe archive path error")
	}
}

func TestExtractZipAllowsSafeRelativeSymlink(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	targetHeader := &zip.FileHeader{Name: "app/Versions/1/file", Method: zip.Store}
	targetHeader.SetMode(0o755)
	target, err := writer.CreateHeader(targetHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(target, "binary"); err != nil {
		t.Fatal(err)
	}
	linkHeader := &zip.FileHeader{Name: "app/Current", Method: zip.Store}
	linkHeader.SetMode(os.ModeSymlink | 0o777)
	link, err := writer.CreateHeader(linkHeader)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(link, "Versions/1"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(archivePath, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	destination := t.TempDir()
	if err := extractZip(context.Background(), archivePath, destination); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(filepath.Join(destination, "app", "Current", "file"))
	if err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(resolved); err != nil || string(data) != "binary" {
		t.Fatalf("resolved symlink data = %q, %v", data, err)
	}
}

func TestExtractZipRejectsEscapingSymlink(t *testing.T) {
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: "app/link", Method: zip.Store}
	header.SetMode(os.ModeSymlink | 0o777)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, "../../escape"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(t.TempDir(), "archive.zip")
	if err := os.WriteFile(archivePath, buffer.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := extractZip(context.Background(), archivePath, t.TempDir()); err == nil {
		t.Fatal("expected unsafe symlink error")
	}
}

func testManager(t *testing.T, client *http.Client, url string, archive []byte) *Manager {
	t.Helper()
	sum := sha256.Sum256(archive)
	manifest := Manifest{Schema: 1, Artifacts: map[string]Artifact{
		"test/amd64": {
			Revision: 7, URL: url, SHA256: hex.EncodeToString(sum[:]), Executable: "test/chrome",
		},
	}}
	manager, err := NewManagerWithOptions(ManagerOptions{
		CacheDir: t.TempDir(), Platform: "test/amd64", Manifest: manifest, HTTPClient: client,
	})
	if err != nil {
		t.Fatal(err)
	}
	return manager
}

func testArchive(t *testing.T, name, contents string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	writer := zip.NewWriter(&buffer)
	header := &zip.FileHeader{Name: name, Method: zip.Deflate}
	header.SetMode(0o755)
	entry, err := writer.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(entry, contents); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func testHash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func testExecutable(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(name), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}
