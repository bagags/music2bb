package applemusic

import (
	"net/url"
	"testing"
)

func TestURLIdentifierMatchesOnlyMusicAppleComBoundary(t *testing.T) {
	tests := []struct {
		rawURL string
		want   bool
	}{
		{rawURL: "https://music.apple.com/us/playlist/example/pl.test", want: true},
		{rawURL: "https://MUSIC.APPLE.COM./us/playlist/example/pl.test", want: true},
		{rawURL: "https://apple.com/music", want: false},
		{rawURL: "https://www.music.apple.com/playlist", want: false},
		{rawURL: "https://music.apple.com.example.test/playlist", want: false},
		{rawURL: "https://notmusic.apple.com/playlist", want: false},
	}
	for _, test := range tests {
		t.Run(test.rawURL, func(t *testing.T) {
			parsed, err := url.Parse(test.rawURL)
			if err != nil {
				t.Fatal(err)
			}
			if got := (URLIdentifier{}).MatchesURL(parsed); got != test.want {
				t.Fatalf("MatchesURL(%q) = %v, want %v", test.rawURL, got, test.want)
			}
		})
	}
}
