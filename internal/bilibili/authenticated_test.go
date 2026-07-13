//go:build authenticated

package bilibili

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gguage/music-to-bb/internal/model"
)

// This destructive canary is doubly opt-in: it requires the authenticated
// build tag and MUSIC2BB_RUN_AUTH_CANARY=1. It never runs in the default suite.
func TestAuthenticatedFavoriteLifecycleCanary(t *testing.T) {
	if os.Getenv("MUSIC2BB_RUN_AUTH_CANARY") != "1" {
		t.Skip("set MUSIC2BB_RUN_AUTH_CANARY=1 to acknowledge remote writes")
	}
	cookieFile := os.Getenv("MUSIC2BB_TEST_COOKIE_FILE")
	bvid := os.Getenv("MUSIC2BB_TEST_BVID")
	if cookieFile == "" || bvid == "" {
		t.Skip("MUSIC2BB_TEST_COOKIE_FILE and MUSIC2BB_TEST_BVID are required")
	}
	client, err := New(Config{CookieFile: cookieFile, Timeout: 30 * time.Second, WriteInterval: 250 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer client.CloseIdleConnections()
	if ok, err := client.LoadCookies(); err != nil || !ok {
		t.Fatalf("load cookies: loaded=%v err=%v", ok, err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	account, err := client.Account(ctx)
	if err != nil || !account.LoggedIn {
		t.Fatalf("authenticated account required: account=%#v err=%v", account, err)
	}

	// Bilibili currently limits favorite titles to 20 characters. Keep the
	// canary uniquely identifiable without depending on the server truncating it.
	title := fmt.Sprintf("music2bb-%d", time.Now().Unix())
	favorite, err := client.CreateFavorite(ctx, CreateFavoriteRequest{Title: title, Intro: "music2bb authenticated integration canary", Private: true})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("created temporary private favorite; folder ID=%d", favorite.ID)

	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), time.Minute)
		defer cleanupCancel()
		resources, listErr := client.ListFavoriteResources(cleanupCtx, favorite.ID)
		if listErr != nil {
			t.Errorf("CANARY CLEANUP REQUIRED: list folder ID %d: %v", favorite.ID, listErr)
		} else if len(resources) > 0 {
			aids := make([]int64, 0, len(resources))
			for _, resource := range resources {
				aids = append(aids, resource.AID)
			}
			if err := client.RemoveFavoriteResources(cleanupCtx, favorite.ID, aids); err != nil {
				t.Errorf("CANARY CLEANUP REQUIRED: remove resources from folder ID %d: %v", favorite.ID, err)
			}
		}
		if err := client.DeleteFavorite(cleanupCtx, favorite.ID); err != nil {
			t.Errorf("CANARY CLEANUP REQUIRED: delete folder ID %d: %v", favorite.ID, err)
		}
	})

	video, err := client.VideoDetail(ctx, bvid)
	if err != nil {
		t.Fatal(err)
	}
	result, err := client.AddToFavorite(ctx, favorite.ID, []model.Video{video})
	if err != nil {
		t.Fatalf("add known video: result=%#v err=%v; folder ID=%d", result, err, favorite.ID)
	}
	resources, found, err := waitForFavoriteResource(ctx, client, favorite.ID, video)
	if err != nil {
		t.Fatalf("verify known video: %v; folder ID=%d", err, favorite.ID)
	}
	if !found {
		var ids []string
		for _, resource := range resources {
			ids = append(ids, resource.BVID)
		}
		t.Fatalf("known video %s not found after add (found %s); folder ID=%d", bvid, strings.Join(ids, ","), favorite.ID)
	}
}

func waitForFavoriteResource(ctx context.Context, client *Client, favoriteID int64, video model.Video) ([]FavoriteResource, bool, error) {
	const attempts = 20
	var resources []FavoriteResource
	for attempt := 0; attempt < attempts; attempt++ {
		var err error
		resources, err = client.ListFavoriteResources(ctx, favoriteID)
		if err != nil {
			return resources, false, err
		}
		for _, resource := range resources {
			if resource.BVID == video.BVID || (resource.BVID == "" && resource.AID == video.AID) {
				return resources, true, nil
			}
		}
		if attempt+1 < attempts {
			if err := client.sleep(ctx, 500*time.Millisecond); err != nil {
				return resources, false, err
			}
		}
	}
	return resources, false, nil
}
