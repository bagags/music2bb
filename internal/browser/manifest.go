package browser

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Artifact describes one exact Chromium archive, its upstream source, and its
// executable inside the extracted archive.
type Artifact struct {
	Origin            string `json:"origin"`
	Availability      string `json:"availability,omitempty"`
	Version           string `json:"chromium_version"`
	Revision          int    `json:"revision"`
	Commit            string `json:"chromium_commit"`
	SourceURL         string `json:"source_url"`
	LicenseURL        string `json:"license_url"`
	RevisionURL       string `json:"revision_url"`
	URL               string `json:"archive_url"`
	ArchiveGeneration string `json:"archive_generation"`
	PublishedAt       string `json:"published_at"`
	SHA256            string `json:"sha256"`
	Executable        string `json:"executable"`
	ArchiveBytes      int64  `json:"archive_bytes"`
	BuildRecipeURL    string `json:"build_recipe_url,omitempty"`
	BuilderRunURL     string `json:"builder_run_url,omitempty"`
	AttestationURL    string `json:"attestation_url,omitempty"`
}

// CreditsProvenance records how the distributable third-party notices were
// generated and audited against the newer snapshot commits.
type CreditsProvenance struct {
	File                   string              `json:"file"`
	SHA256                 string              `json:"sha256"`
	BaseVersion            string              `json:"base_chromium_version"`
	BaseRevision           int                 `json:"base_main_revision"`
	BaseCommit             string              `json:"base_chromium_commit"`
	BaseMainCommit         string              `json:"base_main_commit"`
	BaseSourceURL          string              `json:"base_source_url"`
	BaseArchiveURL         string              `json:"base_archive_url"`
	BaseArchiveGeneration  string              `json:"base_archive_generation"`
	BaseArchivePublishedAt string              `json:"base_archive_published_at"`
	BaseArchiveSHA256      string              `json:"base_archive_sha256"`
	BaseArchiveBytes       int64               `json:"base_archive_bytes"`
	Generator              string              `json:"generator"`
	AuditedThroughVersion  string              `json:"audited_through_version"`
	AuditedThroughRevision int                 `json:"audited_through_revision"`
	AuditedThroughCommit   string              `json:"audited_through_commit"`
	SupplementalProjects   []CreditsSupplement `json:"supplemental_projects"`
}

// CreditsSupplement records a shipped dependency added after the credits base
// was generated.
type CreditsSupplement struct {
	Name          string `json:"name"`
	Version       string `json:"version"`
	ReadmeURL     string `json:"readme_url"`
	LicenseURL    string `json:"license_url"`
	LicenseSHA256 string `json:"license_sha256"`
}

// Manifest is embedded in the executable so download verification does not
// depend on mutable remote metadata.
type Manifest struct {
	Schema     int                 `json:"schema"`
	Project    string              `json:"project,omitempty"`
	ResolvedAt string              `json:"resolved_at,omitempty"`
	Credits    CreditsProvenance   `json:"credits"`
	Artifacts  map[string]Artifact `json:"artifacts"`
}

//go:embed manifest.json
var embeddedManifest []byte

func loadEmbeddedManifest() (Manifest, error) {
	var manifest Manifest
	if err := json.Unmarshal(embeddedManifest, &manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode embedded manifest: %w", err)
	}
	if manifest.Schema != 3 {
		return Manifest{}, fmt.Errorf("unsupported manifest schema %d", manifest.Schema)
	}
	if manifest.Project != "Chromium" {
		return Manifest{}, fmt.Errorf("browser manifest has incomplete upstream metadata")
	}
	if _, err := time.Parse(time.RFC3339, manifest.ResolvedAt); err != nil {
		return Manifest{}, fmt.Errorf("browser manifest has invalid resolution time: %w", err)
	}
	for platform, artifact := range manifest.Artifacts {
		if err := validateArtifactProvenance(artifact); err != nil {
			return Manifest{}, fmt.Errorf("browser manifest %s: %w", platform, err)
		}
	}
	if err := validateCreditsProvenance(manifest.Credits); err != nil {
		return Manifest{}, fmt.Errorf("browser manifest credits: %w", err)
	}
	return manifest, nil
}

func currentPlatform() string { return runtime.GOOS + "/" + runtime.GOARCH }

func validChecksum(value string) bool {
	return validHex(value, 64)
}

func validCommit(value string) bool {
	return validHex(value, 40)
}

func validHex(value string, length int) bool {
	if len(value) != length {
		return false
	}
	for _, char := range strings.ToLower(value) {
		if !strings.ContainsRune("0123456789abcdef", char) {
			return false
		}
	}
	return true
}

func validateArtifactProvenance(artifact Artifact) error {
	if artifact.Revision <= 0 || artifact.Executable == "" {
		return fmt.Errorf("incomplete archive metadata")
	}
	versionParts := strings.Split(artifact.Version, ".")
	if len(versionParts) != 4 {
		return fmt.Errorf("invalid Chromium version %q", artifact.Version)
	}
	for _, part := range versionParts {
		if part == "" {
			return fmt.Errorf("invalid Chromium version %q", artifact.Version)
		}
		if _, err := strconv.ParseUint(part, 10, 32); err != nil {
			return fmt.Errorf("invalid Chromium version %q", artifact.Version)
		}
	}
	if !validCommit(artifact.Commit) {
		return fmt.Errorf("invalid Chromium commit %q", artifact.Commit)
	}
	if artifact.SourceURL != "https://chromium.googlesource.com/chromium/src/+/"+artifact.Commit || artifact.LicenseURL != artifact.SourceURL+"/LICENSE" {
		return fmt.Errorf("provenance URLs do not match revision and commit")
	}
	switch artifact.Origin {
	case "chromium-snapshot":
		return validateSnapshotArtifact(artifact)
	case "music2bb-build":
		return validateProjectArtifact(artifact)
	default:
		return fmt.Errorf("unsupported artifact origin %q", artifact.Origin)
	}
}

func validateSnapshotArtifact(artifact Artifact) error {
	if artifact.Availability != "" || artifact.ArchiveBytes <= 0 || !validChecksum(artifact.SHA256) {
		return fmt.Errorf("incomplete snapshot archive metadata")
	}
	if _, err := strconv.ParseUint(artifact.ArchiveGeneration, 10, 64); err != nil {
		return fmt.Errorf("invalid archive generation %q", artifact.ArchiveGeneration)
	}
	if _, err := time.Parse(time.RFC3339, artifact.PublishedAt); err != nil {
		return fmt.Errorf("invalid archive publication time: %w", err)
	}
	revision := strconv.Itoa(artifact.Revision)
	archiveURL, err := url.Parse(artifact.URL)
	if err != nil || archiveURL.Query().Get("generation") != artifact.ArchiveGeneration {
		return fmt.Errorf("archive URL does not pin object generation")
	}
	if !strings.HasPrefix(artifact.RevisionURL, "https://storage.googleapis.com/chromium-browser-snapshots/") ||
		!strings.HasSuffix(artifact.RevisionURL, "/"+revision+"/REVISIONS") ||
		!strings.HasPrefix(artifact.URL, strings.TrimSuffix(artifact.RevisionURL, "REVISIONS")) {
		return fmt.Errorf("snapshot URLs do not match revision")
	}
	return nil
}

func validateProjectArtifact(artifact Artifact) error {
	tag := fmt.Sprintf("chromium-%s-r%d", artifact.Version, artifact.Revision)
	wantPrefix := "https://github.com/bagags/music2bb-go/releases/download/" + tag + "/"
	if artifact.ArchiveGeneration != tag || !strings.HasPrefix(artifact.URL, wantPrefix) || artifact.BuildRecipeURL == "" {
		return fmt.Errorf("project build URLs do not match immutable browser release %s", tag)
	}
	if artifact.Availability == "pending-build" {
		if artifact.SHA256 != "" || artifact.ArchiveBytes != 0 || artifact.PublishedAt != "" || artifact.BuilderRunURL != "" || artifact.AttestationURL != "" {
			return fmt.Errorf("pending project build contains publication metadata")
		}
		return nil
	}
	if artifact.Availability != "" || !validChecksum(artifact.SHA256) || artifact.ArchiveBytes <= 0 || artifact.BuilderRunURL == "" || artifact.AttestationURL == "" {
		return fmt.Errorf("incomplete project build publication metadata")
	}
	if _, err := time.Parse(time.RFC3339, artifact.PublishedAt); err != nil {
		return fmt.Errorf("invalid archive publication time: %w", err)
	}
	return nil
}

func validateCreditsProvenance(credits CreditsProvenance) error {
	if credits.File != "CHROMIUM_CREDITS.html" || !validChecksum(credits.SHA256) ||
		credits.BaseRevision <= 0 || credits.BaseArchiveBytes <= 0 ||
		credits.AuditedThroughRevision < credits.BaseRevision || len(credits.SupplementalProjects) == 0 {
		return fmt.Errorf("incomplete credits metadata")
	}
	if !validCommit(credits.BaseCommit) || !validCommit(credits.BaseMainCommit) ||
		!validCommit(credits.AuditedThroughCommit) || !validChecksum(credits.BaseArchiveSHA256) {
		return fmt.Errorf("invalid credits commit or checksum")
	}
	if _, err := strconv.ParseUint(credits.BaseArchiveGeneration, 10, 64); err != nil {
		return fmt.Errorf("invalid credits archive generation %q", credits.BaseArchiveGeneration)
	}
	if _, err := time.Parse(time.RFC3339, credits.BaseArchivePublishedAt); err != nil {
		return fmt.Errorf("invalid credits archive publication time: %w", err)
	}
	archiveURL, err := url.Parse(credits.BaseArchiveURL)
	if err != nil || archiveURL.Query().Get("generation") != credits.BaseArchiveGeneration {
		return fmt.Errorf("credits archive URL does not pin object generation")
	}
	if credits.BaseSourceURL != "https://chromium.googlesource.com/chromium/src/+/"+credits.BaseCommit ||
		credits.Generator != "tools/licenses/licenses.py via chrome://credits" {
		return fmt.Errorf("credits source metadata does not match its commit")
	}
	auditedSource := "https://chromium.googlesource.com/chromium/src/+/" + credits.AuditedThroughCommit + "/"
	for _, project := range credits.SupplementalProjects {
		if project.Name == "" || project.Version == "" || !validChecksum(project.LicenseSHA256) ||
			!strings.HasPrefix(project.ReadmeURL, auditedSource) ||
			!strings.HasPrefix(project.LicenseURL, auditedSource) {
			return fmt.Errorf("invalid supplemental credits metadata for %q", project.Name)
		}
	}
	return nil
}
