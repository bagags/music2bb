package wiring

import (
	"context"
	"testing"

	"github.com/gguage/music-to-bb/internal/config"
	"github.com/gguage/music-to-bb/internal/service"
)

func TestNewBuildsProductionGraphWithoutNetwork(t *testing.T) {
	root := t.TempDir()
	components, err := New(Options{State: config.Options{
		Dir:           root + "/config",
		CacheDir:      root + "/cache",
		SkipMigration: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer components.Close()
	if components.Engine == nil || components.Browser == nil {
		t.Fatalf("incomplete components: %#v", components)
	}
	if components.State.Dir != root+"/config" || components.State.CacheDir != root+"/cache" {
		t.Fatalf("unexpected state paths: %#v", components.State.Paths)
	}
}

func TestPlaylistAdapterMapsInvalidURL(t *testing.T) {
	root := t.TempDir()
	components, err := New(Options{State: config.Options{
		Dir: root + "/config", CacheDir: root + "/cache", SkipMigration: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer components.Close()
	_, err = components.Engine.ParsePlaylist(context.Background(), "not-a-url", service.ParseOptions{}, nil)
	if service.CategoryOf(err) != service.ErrorInvalidInput {
		t.Fatalf("category = %q, want %q (err=%v)", service.CategoryOf(err), service.ErrorInvalidInput, err)
	}
}
