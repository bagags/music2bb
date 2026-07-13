package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadUsesEmbeddedDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg, err := Load(Options{Dir: dir, CacheDir: filepath.Join(dir, "cache"), SkipMigration: true})
	if err != nil {
		t.Fatal(err)
	}
	wantBlocks := []string{
		"翻唱", "还原", "扒带", "扒谱", "乐谱", "五线谱", "简谱", "伴奏", "cover", "吉他", "自弹自唱",
		"弹唱", "教程", "教学", "如何", "怎么", "卡拉OK", "KTV", "自制", "开车", "合唱", "填词", "录屏",
		"手元", "剪辑", "翻填", "feat", "排行榜", "Top", "top", "收录", "合集", "联动",
	}
	wantQuality := []string{
		"官方", "official", "MV", "mv", "无损", "flac", "Hi-Res", "HiRes", "4K", "1080p", "remaster", "重制",
		"修复", "原版", "原唱", "录影棚", "试听", "PV", "pv", "OP", "op", "EP", "ep", "中文字幕", "中字",
	}
	wantUploaders := []string{"HOYO-MiX", "原神", "崩坏星穹铁道", "绝区零", "JLRS-jayfm", "JLRS-LeoFM"}
	if !reflect.DeepEqual(cfg.BlockKeywords, wantBlocks) {
		t.Errorf("embedded block keywords changed:\n got: %#v\nwant: %#v", cfg.BlockKeywords, wantBlocks)
	}
	if !reflect.DeepEqual(cfg.QualityKeywords, wantQuality) {
		t.Errorf("embedded quality keywords changed:\n got: %#v\nwant: %#v", cfg.QualityKeywords, wantQuality)
	}
	if !reflect.DeepEqual(cfg.WeightedUploaders, wantUploaders) {
		t.Errorf("embedded uploaders changed:\n got: %#v\nwant: %#v", cfg.WeightedUploaders, wantUploaders)
	}
	for _, path := range []string{cfg.BlockFile, cfg.QualityFile, cfg.UploaderFile} {
		if _, err := os.Stat(path); err != nil {
			t.Errorf("editable default %s was not materialized: %v", path, err)
		}
	}
}

func TestExternalListsOverrideEmbeddedDefaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeTestFile(t, filepath.Join(dir, "b.txt"), "# intentionally empty\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "w.txt"), "custom\n# ignored\n", 0o644)
	writeTestFile(t, filepath.Join(dir, "w-up.txt"), "Uploader\n", 0o644)

	cfg, err := Load(Options{Dir: dir, CacheDir: filepath.Join(dir, "cache"), SkipMigration: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BlockKeywords) != 0 {
		t.Fatalf("empty external list did not override defaults: %#v", cfg.BlockKeywords)
	}
	if !reflect.DeepEqual(cfg.QualityKeywords, []string{"custom"}) {
		t.Errorf("quality keywords = %#v", cfg.QualityKeywords)
	}
	if !reflect.DeepEqual(cfg.WeightedUploaders, []string{"Uploader"}) {
		t.Errorf("weighted uploaders = %#v", cfg.WeightedUploaders)
	}
}

func TestMigrateCopiesLegacyStateWithoutOverwriting(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	working := filepath.Join(root, "working")
	executable := filepath.Join(root, "executable")
	destination := filepath.Join(root, "config")
	writeTestFile(t, filepath.Join(working, ".cookies", "bilibili.json"), `[{"name":"bili_jct","value":"secret"}]`, 0o644)
	writeTestFile(t, filepath.Join(working, "b.txt"), "working-block\n", 0o644)
	writeTestFile(t, filepath.Join(executable, "b.txt"), "executable-block\n", 0o644)
	writeTestFile(t, filepath.Join(executable, "w.txt"), "legacy-quality\n", 0o644)
	writeTestFile(t, filepath.Join(executable, "w-up.txt"), "legacy-uploader\n", 0o644)
	writeTestFile(t, filepath.Join(destination, "w.txt"), "user-quality\n", 0o644)

	cfg, err := Load(Options{
		Dir:           destination,
		CacheDir:      filepath.Join(root, "cache"),
		WorkingDir:    working,
		ExecutableDir: executable,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(cfg.Migration.Copied, []string{"bilibili.json", "b.txt", "w-up.txt"}) {
		t.Errorf("copied = %#v", cfg.Migration.Copied)
	}
	assertFileContent(t, cfg.BlockFile, "working-block\n")
	assertFileContent(t, cfg.QualityFile, "user-quality\n")
	assertFileContent(t, cfg.UploaderFile, "legacy-uploader\n")
	assertFileContent(t, filepath.Join(working, "b.txt"), "working-block\n")
	if info, err := os.Stat(cfg.CookieFile); err != nil {
		t.Fatal(err)
	} else if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("cookie mode = %04o, want 0600", got)
	}
	if _, err := os.Stat(cfg.MigrationMarker); err != nil {
		t.Errorf("migration marker missing: %v", err)
	}
}

func TestMigrationMarkerPreventsSubsequentImport(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	legacy := filepath.Join(root, "legacy")
	destination := filepath.Join(root, "config")
	writeTestFile(t, filepath.Join(legacy, "b.txt"), "first\n", 0o644)
	options := Options{Dir: destination, CacheDir: filepath.Join(root, "cache"), WorkingDir: legacy, ExecutableDir: legacy}
	first, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(first.BlockFile); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, filepath.Join(legacy, "b.txt"), "second\n", 0o644)
	second, err := Load(options)
	if err != nil {
		t.Fatal(err)
	}
	if !second.Migration.AlreadyComplete {
		t.Fatal("second load did not observe migration marker")
	}
	if _, err := os.Stat(second.BlockFile); err != nil {
		t.Fatalf("editable defaults were not restored, stat error = %v", err)
	}
	if len(second.BlockKeywords) != 33 {
		t.Errorf("missing external list should use embedded defaults, got %d entries", len(second.BlockKeywords))
	}
}

func TestLoadMigratesOldAppConfigAndRepairsHeaderOnlyArtifact(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	legacy := filepath.Join(root, "kg2bb")
	destination := filepath.Join(root, "music2bb")
	writeTestFile(t, filepath.Join(legacy, "b.txt"), "# 视频关键词屏蔽列表\n# 匹配以下关键词的视频将被屏蔽\n# 一行一个，#开头的行会被忽略\n", 0o644)
	writeTestFile(t, filepath.Join(legacy, "w.txt"), "legacy-quality\n", 0o644)

	cfg, err := Load(Options{
		Dir: destination, CacheDir: filepath.Join(root, "cache"), LegacyDir: legacy,
		WorkingDir: filepath.Join(root, "working"), ExecutableDir: filepath.Join(root, "executable"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.BlockKeywords) != 33 || cfg.BlockKeywords[0] != "翻唱" {
		t.Fatalf("header-only artifact was not repaired: %#v", cfg.BlockKeywords)
	}
	if !reflect.DeepEqual(cfg.QualityKeywords, []string{"legacy-quality"}) {
		t.Fatalf("legacy override was not preserved: %#v", cfg.QualityKeywords)
	}
	if !reflect.DeepEqual(cfg.Migration.Copied, []string{"b.txt", "w.txt"}) {
		t.Fatalf("copied = %#v", cfg.Migration.Copied)
	}
}

func TestParseKeywordsHandlesBOMWhitespaceAndDuplicates(t *testing.T) {
	t.Parallel()
	got := parseKeywords([]byte("\uFEFF alpha \r\n# comment\nalpha\n beta\n"))
	if !reflect.DeepEqual(got, []string{"alpha", "beta"}) {
		t.Fatalf("keywords = %#v", got)
	}
}

func TestResolveExplicitPaths(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	paths, err := Resolve(Options{Dir: filepath.Join(root, "state"), CacheDir: filepath.Join(root, "cache")})
	if err != nil {
		t.Fatal(err)
	}
	if paths.CookieFile != filepath.Join(root, "state", "cookies", "bilibili.json") {
		t.Errorf("cookie path = %q", paths.CookieFile)
	}
	if paths.MigrationMarker != filepath.Join(root, "state", migrationMarkerName) {
		t.Errorf("marker path = %q", paths.MigrationMarker)
	}
}

func writeTestFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(data); got != want {
		t.Errorf("%s = %q, want %q", path, got, want)
	}
}
