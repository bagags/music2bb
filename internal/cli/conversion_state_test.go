package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	music2bb "github.com/bagags/music2bb-go"
)

func TestConversionStateRestoresBySourceIDAcrossPlaylistChanges(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	state := newConversionState(root, "https://music.example/list?id=1&utm_source=test", func() time.Time { return now })
	songA := music2bb.Song{Name: "A", SourceID: "source:a"}
	songB := music2bb.Song{Name: "B", SourceID: "source:b"}
	videoA := music2bb.Video{BVID: "BVA", Title: "A"}
	videoB := music2bb.Video{BVID: "BVB", Title: "B"}
	for _, outcome := range []music2bb.MatchResult{
		{Song: songA, Video: &videoA, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted},
		{Song: songB, Video: &videoB, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted},
	} {
		if err := state.saveOutcome(outcome); err != nil {
			t.Fatal(err)
		}
	}

	reloaded := newConversionState(root, "https://music.example/list?utm_source=other&id=1", func() time.Time { return now })
	songC := music2bb.Song{Name: "C", SourceID: "source:c"}
	restored, err := reloaded.restore([]music2bb.Song{songB, songC, songA}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 2 || restored["source:a"].Video.BVID != "BVA" || restored["source:b"].Video.BVID != "BVB" {
		t.Fatalf("restored = %#v", restored)
	}
	if _, ok := restored["source:c"]; ok {
		t.Fatal("new playlist song unexpectedly restored")
	}
}

func TestManualDecisionsReuseAcrossPlaylistsAndHardExpire(t *testing.T) {
	root := t.TempDir()
	now := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	songA := music2bb.Song{Name: "A", SourceID: "source:a"}
	songB := music2bb.Song{Name: "B", SourceID: "source:b"}
	video := music2bb.Video{BVID: "BV-selected", Title: "Manual"}
	first := newConversionState(root, "https://music.example/one", clock)
	if err := first.saveDecision(music2bb.MatchResult{Song: songA, Video: &video, HasSelection: true}, false); err != nil {
		t.Fatal(err)
	}
	if err := first.saveDecision(music2bb.MatchResult{Song: songB}, true); err != nil {
		t.Fatal(err)
	}

	second := newConversionState(root, "https://music.example/two", clock)
	restored, err := second.restore([]music2bb.Song{songB, songA}, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := restored["source:a"]; !got.ManualOverride || got.Video == nil || got.Video.BVID != "BV-selected" {
		t.Fatalf("selected decision = %#v", got)
	}
	if got := restored["source:b"]; got.HasSelection || got.NeedsReview {
		t.Fatalf("skip decision = %#v", got)
	}

	now = now.Add(manualDecisionTTL)
	expired := newConversionState(root, "https://music.example/one", clock)
	restored, err = expired.restore([]music2bb.Song{songA, songB}, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 0 {
		t.Fatalf("expired decisions restored from decision or checkpoint: %#v", restored)
	}
}

func TestFreshIgnoresCheckpointAndDecisionWithoutAffectingSearchSemantics(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1000, 0)
	song := music2bb.Song{Name: "Song", SourceID: "source:song"}
	video := music2bb.Video{BVID: "BV1"}
	state := newConversionState(root, "https://music.example/list", func() time.Time { return now })
	if err := state.saveOutcome(music2bb.MatchResult{Song: song, Video: &video, HasSelection: true, SearchStatus: music2bb.SearchStatusCompleted}); err != nil {
		t.Fatal(err)
	}
	if err := state.saveDecision(music2bb.MatchResult{Song: song, Video: &video, HasSelection: true}, false); err != nil {
		t.Fatal(err)
	}

	fresh := newConversionState(root, "https://music.example/list", func() time.Time { return now })
	restored, err := fresh.restore([]music2bb.Song{song}, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(restored) != 0 {
		t.Fatalf("fresh restored = %#v", restored)
	}
	if info, err := os.Stat(state.checkpointPath); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("checkpoint mode = %v", info.Mode().Perm())
	}
	if info, err := os.Stat(state.decisionPath(stableSongID(song))); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0o600 {
		t.Fatalf("decision mode = %v", info.Mode().Perm())
	}
}

func TestCorruptCheckpointAndDecisionAreReportedAndPreserved(t *testing.T) {
	root := t.TempDir()
	song := music2bb.Song{Name: "Song", SourceID: "source:song"}

	checkpoint := newConversionState(root, "https://music.example/checkpoint", time.Now)
	if err := os.MkdirAll(filepath.Dir(checkpoint.checkpointPath), 0o700); err != nil {
		t.Fatal(err)
	}
	corrupt := []byte("{broken checkpoint")
	if err := os.WriteFile(checkpoint.checkpointPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := checkpoint.restore([]music2bb.Song{song}, false); err == nil || !strings.Contains(err.Error(), "original file preserved") {
		t.Fatalf("checkpoint error = %v", err)
	}
	if got, _ := os.ReadFile(checkpoint.checkpointPath); !reflect.DeepEqual(got, corrupt) {
		t.Fatalf("checkpoint was changed: %q", got)
	}

	decision := newConversionState(root, "https://music.example/decision", time.Now)
	decisionPath := decision.decisionPath(stableSongID(song))
	if err := os.MkdirAll(filepath.Dir(decisionPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decisionPath, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := decision.restore([]music2bb.Song{song}, false); err == nil || !strings.Contains(err.Error(), "original file preserved") {
		t.Fatalf("decision error = %v", err)
	}
	if got, _ := os.ReadFile(decisionPath); !reflect.DeepEqual(got, corrupt) {
		t.Fatalf("decision was changed: %q", got)
	}
}
