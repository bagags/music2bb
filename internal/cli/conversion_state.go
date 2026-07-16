package cli

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	music2bb "github.com/bagags/music2bb-go"
)

const (
	conversionStateVersion = 1
	manualDecisionTTL      = 7 * 24 * time.Hour
)

type conversionCheckpoint struct {
	Version    int                       `json:"version"`
	PlaylistID string                    `json:"playlistID"`
	SourceURL  string                    `json:"sourceURL"`
	UpdatedAt  time.Time                 `json:"updatedAt"`
	Songs      map[string]checkpointSong `json:"songs"`
}

type checkpointSong struct {
	SourceID       string               `json:"sourceID"`
	Outcome        music2bb.MatchResult `json:"outcome"`
	ManualDecision bool                 `json:"manualDecision,omitempty"`
	UpdatedAt      time.Time            `json:"updatedAt"`
}

type manualDecision struct {
	Version   int             `json:"version"`
	SourceID  string          `json:"sourceID"`
	Kind      string          `json:"kind"`
	Video     *music2bb.Video `json:"video,omitempty"`
	UpdatedAt time.Time       `json:"updatedAt"`
}

type conversionState struct {
	mu             sync.Mutex
	now            func() time.Time
	rawURL         string
	playlistID     string
	checkpointPath string
	decisionsDir   string
	document       conversionCheckpoint
	loaded         bool
	loadErr        error
}

func newConversionState(configDir, rawURL string, now func() time.Time) *conversionState {
	if strings.TrimSpace(configDir) == "" {
		return nil
	}
	if now == nil {
		now = time.Now
	}
	playlistID := normalizedPlaylistID(rawURL)
	return &conversionState{
		now: now, rawURL: rawURL, playlistID: playlistID,
		checkpointPath: filepath.Join(configDir, "conversions", "v1", playlistID+".json"),
		decisionsDir:   filepath.Join(configDir, "decisions", "v1"),
	}
}

func (s *conversionState) restore(songs []music2bb.Song, fresh bool) (map[string]music2bb.MatchResult, error) {
	if s == nil {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadCheckpointLocked(); err != nil {
		return nil, err
	}
	restored := make(map[string]music2bb.MatchResult)
	for _, song := range songs {
		sourceID := stableSongID(song)
		decision, ok, err := s.loadDecisionLocked(sourceID)
		if err != nil {
			return nil, err
		}
		if ok {
			if !fresh && s.decisionFresh(decision) {
				restored[sourceID] = outcomeFromDecision(song, decision)
			}
			// A present decision file is authoritative even when expired. An
			// expired decision must be re-matched rather than revived from the
			// same-playlist checkpoint.
			continue
		}
		if fresh {
			continue
		}
		entry, ok := s.document.Songs[sourceID]
		if !ok || entry.ManualDecision || !checkpointOutcomeReusable(entry.Outcome) {
			continue
		}
		outcome := cloneMatchResults([]music2bb.MatchResult{entry.Outcome})[0]
		outcome.Song = songWithStableID(song)
		for index := range outcome.Candidates {
			outcome.Candidates[index].Song = outcome.Song
		}
		restored[sourceID] = outcome
	}
	if fresh {
		s.document = newCheckpointDocument(s.playlistID, s.rawURL)
	}
	return restored, nil
}

func (s *conversionState) saveOutcome(outcome music2bb.MatchResult) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadCheckpointLocked(); err != nil {
		return err
	}
	sourceID := stableSongID(outcome.Song)
	outcome = cloneMatchResults([]music2bb.MatchResult{outcome})[0]
	outcome.Song = songWithStableID(outcome.Song)
	now := s.now().UTC()
	previous := s.document.Songs[sourceID]
	s.document.Songs[sourceID] = checkpointSong{SourceID: sourceID, Outcome: outcome, ManualDecision: previous.ManualDecision, UpdatedAt: now}
	s.document.UpdatedAt = now
	return writeStateJSON(s.checkpointPath, s.document)
}

func (s *conversionState) saveDecision(outcome music2bb.MatchResult, skipped bool) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadCheckpointLocked(); err != nil {
		return err
	}
	sourceID := stableSongID(outcome.Song)
	if _, _, err := s.loadDecisionLocked(sourceID); err != nil {
		return err
	}
	decision := manualDecision{Version: conversionStateVersion, SourceID: sourceID, Kind: "skip", UpdatedAt: s.now().UTC()}
	if !skipped {
		if !outcome.HasSelection || outcome.Video == nil {
			return errors.New("manual selection has no video")
		}
		video := *outcome.Video
		video.Tags = append([]string(nil), outcome.Video.Tags...)
		decision.Kind, decision.Video = "select", &video
	}
	if err := writeStateJSON(s.decisionPath(sourceID), decision); err != nil {
		return err
	}
	checkpointOutcome := cloneMatchResults([]music2bb.MatchResult{outcome})[0]
	checkpointOutcome.Song = songWithStableID(outcome.Song)
	checkpointOutcome.ManualOverride = !skipped
	checkpointOutcome.NeedsReview = false
	checkpointOutcome.ReviewReason = music2bb.ReviewNone
	checkpointOutcome.SearchStatus = music2bb.SearchStatusCompleted
	if skipped {
		checkpointOutcome.Video = nil
		checkpointOutcome.HasSelection = false
		checkpointOutcome.Matched = false
	}
	now := s.now().UTC()
	s.document.Songs[sourceID] = checkpointSong{SourceID: sourceID, Outcome: checkpointOutcome, ManualDecision: true, UpdatedAt: now}
	s.document.UpdatedAt = now
	return writeStateJSON(s.checkpointPath, s.document)
}

func (s *conversionState) removeDecision(song music2bb.Song) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.loadCheckpointLocked(); err != nil {
		return err
	}
	sourceID := stableSongID(song)
	if _, _, err := s.loadDecisionLocked(sourceID); err != nil {
		return err
	}
	if err := os.Remove(s.decisionPath(sourceID)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("remove manual decision: %w", err)
	}
	if _, ok := s.document.Songs[sourceID]; ok {
		delete(s.document.Songs, sourceID)
		s.document.UpdatedAt = s.now().UTC()
		if err := writeStateJSON(s.checkpointPath, s.document); err != nil {
			return err
		}
	}
	return nil
}

func (s *conversionState) loadCheckpointLocked() error {
	if s.loaded {
		return s.loadErr
	}
	s.loaded = true
	payload, err := os.ReadFile(s.checkpointPath)
	if errors.Is(err, fs.ErrNotExist) {
		s.document = newCheckpointDocument(s.playlistID, s.rawURL)
		return nil
	}
	if err != nil {
		s.loadErr = fmt.Errorf("read conversion checkpoint: %w", err)
		return s.loadErr
	}
	var document conversionCheckpoint
	if err := decodeStateJSON(payload, &document); err != nil || document.Version != conversionStateVersion || document.PlaylistID != s.playlistID || document.Songs == nil {
		if err == nil {
			err = errors.New("schema or playlist identity mismatch")
		}
		s.loadErr = fmt.Errorf("conversion checkpoint %s is corrupt; original file preserved: %w", s.checkpointPath, err)
		return s.loadErr
	}
	s.document = document
	return nil
}

func (s *conversionState) loadDecisionLocked(sourceID string) (manualDecision, bool, error) {
	path := s.decisionPath(sourceID)
	payload, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return manualDecision{}, false, nil
	}
	if err != nil {
		return manualDecision{}, false, fmt.Errorf("read manual decision: %w", err)
	}
	var decision manualDecision
	if err := decodeStateJSON(payload, &decision); err != nil || decision.Version != conversionStateVersion || decision.SourceID != sourceID || (decision.Kind != "select" && decision.Kind != "skip") || (decision.Kind == "select" && decision.Video == nil) || decision.UpdatedAt.IsZero() {
		if err == nil {
			err = errors.New("schema or source identity mismatch")
		}
		return manualDecision{}, false, fmt.Errorf("manual decision %s is corrupt; original file preserved: %w", path, err)
	}
	return decision, true, nil
}

func (s *conversionState) decisionFresh(decision manualDecision) bool {
	return s.now().Sub(decision.UpdatedAt) < manualDecisionTTL
}

func (s *conversionState) decisionPath(sourceID string) string {
	digest := sha256.Sum256([]byte(sourceID))
	return filepath.Join(s.decisionsDir, hex.EncodeToString(digest[:])+".json")
}

func newCheckpointDocument(playlistID, rawURL string) conversionCheckpoint {
	return conversionCheckpoint{
		Version: conversionStateVersion, PlaylistID: playlistID, SourceURL: rawURL,
		Songs: make(map[string]checkpointSong),
	}
}

func checkpointOutcomeReusable(outcome music2bb.MatchResult) bool {
	return outcome.SearchStatus == music2bb.SearchStatusCompleted
}

func outcomeFromDecision(song music2bb.Song, decision manualDecision) music2bb.MatchResult {
	outcome := music2bb.MatchResult{
		Song: songWithStableID(song), NeedsReview: false, ReviewReason: music2bb.ReviewNone,
		SearchStatus: music2bb.SearchStatusCompleted,
	}
	if decision.Kind == "skip" {
		return outcome
	}
	video := *decision.Video
	video.Tags = append([]string(nil), decision.Video.Tags...)
	outcome.Video = &video
	outcome.Score = 999
	outcome.Matched = true
	outcome.HasSelection = true
	outcome.ManualOverride = true
	candidate := outcome
	candidate.Candidates = nil
	outcome.Candidates = []music2bb.MatchResult{candidate}
	return outcome
}

func stableSongID(song music2bb.Song) string { return song.StableSourceID() }

func songWithStableID(song music2bb.Song) music2bb.Song {
	song.SourceID = stableSongID(song)
	return song
}

func normalizedPlaylistID(rawURL string) string {
	canonical := strings.TrimSpace(rawURL)
	if parsed, err := url.Parse(canonical); err == nil && parsed.Host != "" {
		if providerID := providerPlaylistIdentity(parsed); providerID != "" {
			canonical = providerID
		} else {
			parsed.Scheme = strings.ToLower(parsed.Scheme)
			parsed.Host = strings.ToLower(parsed.Host)
			parsed.Fragment = ""
			parsed.Path = path.Clean(parsed.Path)
			query := parsed.Query()
			for key := range query {
				lower := strings.ToLower(key)
				if strings.HasPrefix(lower, "utm_") || lower == "spm_id_from" || lower == "share_source" {
					query.Del(key)
				}
			}
			parsed.RawQuery = query.Encode()
			canonical = parsed.String()
		}
	}
	digest := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(digest[:])
}

func providerPlaylistIdentity(parsed *url.URL) string {
	hostname := strings.ToLower(parsed.Hostname())
	if hostname == "music.apple.com" || strings.HasSuffix(hostname, ".music.apple.com") {
		for _, segment := range strings.Split(parsed.Path, "/") {
			if strings.HasPrefix(segment, "pl.") {
				return "applemusic:" + segment
			}
		}
	}
	if hostname == "kugou.com" || strings.HasSuffix(hostname, ".kugou.com") {
		query := parsed.Query()
		playlistID := query.Get("specialid")
		if playlistID == "" || playlistID == "-2147483648" {
			playlistID = query.Get("global_specialid")
		}
		if playlistID != "" {
			return "kugou:" + playlistID
		}
	}
	return ""
}

func decodeStateJSON(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}

func writeStateJSON(destination string, value any) error {
	payload, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Errorf("encode persistent conversion state: %w", err)
	}
	payload = append(payload, '\n')
	dir := filepath.Dir(destination)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create persistent state directory: %w", err)
	}
	temporary, err := os.CreateTemp(dir, ".state-*.tmp")
	if err != nil {
		return fmt.Errorf("create persistent state temporary file: %w", err)
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
	if err := os.Rename(temporaryPath, destination); err != nil {
		return fmt.Errorf("replace persistent state: %w", err)
	}
	return nil
}
