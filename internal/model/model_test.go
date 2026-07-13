package model

import (
	"reflect"
	"testing"
)

func TestSongCleanName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "trim", in: "  Hello  ", want: "Hello"},
		{name: "from suffix", in: `Try Everything (From "Zootopia 2")`, want: "Try Everything Zootopia"},
		{name: "from suffix unicode digits", in: `Song (From "Album ２")`, want: "Song Album"},
		{name: "bracket suffixes", in: "Song（Live） [translation] 【PV】", want: "Song"},
		{name: "feature", in: "Song FEAT. Singer", want: "Song"},
		{name: "hyphen", in: "Song - Singer", want: "Song"},
		{name: "collapse unicode spaces", in: "Song\u3000  Name", want: "Song Name"},
		{name: "collapse next line control", in: "Song\u0085Name", want: "Song Name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (Song{Name: tt.in}).CleanName(); got != tt.want {
				t.Fatalf("CleanName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSongCleanArtist(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "first comma artist", in: "First, Second / Third", want: "First"},
		{name: "wide separator", in: "First、Second", want: "First"},
		{name: "keeps existing HOYO", in: "HOYO-MiX, Singer", want: "HOYO-MiX"},
		{name: "restores removed mihoyo", in: "Singer (miHoYo) / Other", want: "Singer miHoYo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := (Song{Artist: tt.in}).CleanArtist(); got != tt.want {
				t.Fatalf("CleanArtist() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSongSearchKeywords(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		song Song
		full string
		all  []string
	}{
		{
			name: "artist aliases retain primary query",
			song: Song{Name: "If I Can Stop One Heart From Breaking", Artist: "知更鸟"},
			full: "If I Can Stop One Heart From Breaking 知更鸟",
			all: []string{
				"If I Can Stop One Heart From Breaking 知更鸟",
				"If I Can Stop One Heart From Breaking Robin",
				"If I Can Stop One Heart From Breaking 知更鸟",
			},
		},
		{
			name: "name only",
			song: Song{Name: "Song (Live)"},
			full: "Song",
			all:  []string{"Song"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := tt.song.SearchKeywordFull(); got != tt.full {
				t.Errorf("SearchKeywordFull() = %q, want %q", got, tt.full)
			}
			if got := tt.song.AllSearchKeywords(); !reflect.DeepEqual(got, tt.all) {
				t.Errorf("AllSearchKeywords() = %#v, want %#v", got, tt.all)
			}
		})
	}
}

func TestVideoURL(t *testing.T) {
	t.Parallel()
	if got := (Video{BVID: "BV1test"}).URL(); got != "https://www.bilibili.com/video/BV1test" {
		t.Fatalf("URL() = %q", got)
	}
}
