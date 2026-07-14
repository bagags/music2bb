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
	"sync/atomic"
	"testing"
)

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

func TestEmbeddedManifestPinsVerifiedArtifacts(t *testing.T) {
	manifest, err := loadEmbeddedManifest()
	if err != nil {
		t.Fatal(err)
	}
	expectedPlatforms := []string{
		"darwin/amd64",
		"darwin/arm64",
		"linux/amd64",
		"windows/amd64",
		"windows/arm64",
	}
	if len(manifest.Artifacts) != len(expectedPlatforms) {
		t.Errorf("manifest artifact count = %d, want %d", len(manifest.Artifacts), len(expectedPlatforms))
	}
	for _, platform := range expectedPlatforms {
		artifact, ok := manifest.Artifacts[platform]
		if !ok {
			t.Errorf("manifest is missing %s", platform)
			continue
		}
		if artifact.Revision != PinnedRevision {
			t.Errorf("%s revision = %d, want %d", platform, artifact.Revision, PinnedRevision)
		}
		if !validChecksum(artifact.SHA256) {
			t.Errorf("%s has invalid checksum %q", platform, artifact.SHA256)
		}
		if artifact.URL == "" || artifact.Executable == "" || artifact.ApproxBytes <= 0 {
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
