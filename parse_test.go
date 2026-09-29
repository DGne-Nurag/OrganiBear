package main

import (
	"path/filepath"
	"testing"
)

func TestParsePath(t *testing.T) {
	tests := []struct {
		in   string
		want Parsed
	}{
		{"The.Matrix.1999.1080p.BluRay.x264-GROUP.mkv", Parsed{Title: "The Matrix", Year: 1999, Resolution: "1080p"}},
		{"Inception (2010) [720p].mp4", Parsed{Title: "Inception", Year: 2010, Resolution: "720p"}},
		{"Der_Schuh_des_Manitu_2001_German_DL.avi", Parsed{Title: "Der Schuh des Manitu", Year: 2001}},
		{"1917.2019.2160p.UHD.mkv", Parsed{Title: "1917", Year: 2019, Resolution: "2160p"}},
		{"1917.mkv", Parsed{Title: "1917"}},
		{"Blade Runner 2049 (2017).mkv", Parsed{Title: "Blade Runner 2049", Year: 2017}},
		{"Amelie.German.720p.WEB-DL.mkv", Parsed{Title: "Amelie", Resolution: "720p"}},
		{"Breaking.Bad.S01E03.720p.HDTV.x264.mkv", Parsed{Title: "Breaking Bad", Series: true, Season: 1, Episode: 3, Resolution: "720p"}},
		{"breaking bad - 1x03 - ...And the Bag's in the River.avi", Parsed{Title: "breaking bad", Series: true, Season: 1, Episode: 3, EpisodeTitle: "And the Bag's in the River"}},
		{"Doctor.Who.2005.S02E01E02.mkv", Parsed{Title: "Doctor Who", Year: 2005, Series: true, Season: 2, Episode: 1, EpisodeEnd: 2}},
		{"[SubGroup] Dark - Staffel 1 Folge 4.mkv", Parsed{Title: "Dark", Series: true, Season: 1, Episode: 4}},
		{"The Office (US) S03E10 Christmas Party.mkv", Parsed{Title: "The Office US", Series: true, Season: 3, Episode: 10, EpisodeTitle: "Christmas Party"}},
		{"Stranger Things/Season 2/S02E05.mkv", Parsed{Title: "Stranger Things", Series: true, Season: 2, Episode: 5}},
		{"Stranger Things/Staffel 2/05 - Dig Dug.mkv", Parsed{Title: "Stranger Things", Series: true, Season: 2, Episode: 5}},
		{"Inception (2010)/inception-hd.mkv", Parsed{Title: "Inception", Year: 2010}},
		{"Filme/Arrival.2016.1080p/arrival.mkv", Parsed{Title: "Arrival", Year: 2016, Resolution: "1080p"}},
	}
	for _, tt := range tests {
		got := ParsePath(filepath.FromSlash(tt.in))
		if got != tt.want {
			t.Errorf("ParsePath(%q)\n got  %+v\n want %+v", tt.in, got, tt.want)
		}
	}
}

func TestRenderTemplate(t *testing.T) {
	cfg := DefaultConfig()
	tests := []struct {
		tmpl string
		info MediaInfo
		want string
	}{
		{cfg.MovieTemplate, MediaInfo{Title: "Star Wars: Eine neue Hoffnung", Year: 1977}, "Filme/Star Wars - Eine neue Hoffnung (1977)/Star Wars - Eine neue Hoffnung (1977)"},
		{cfg.MovieTemplate, MediaInfo{Title: "Amelie"}, "Filme/Amelie/Amelie"},
		{cfg.SeriesTemplate, MediaInfo{Title: "Dark", Year: 2017, Series: true, Season: 1, Episode: 4, EpisodeTitle: "Doppelleben"}, "Serien/Dark (2017)/Staffel 01/Dark - S01E04 - Doppelleben"},
		{cfg.SeriesTemplate, MediaInfo{Title: "Dark", Series: true, Season: 1, Episode: 4}, "Serien/Dark/Staffel 01/Dark - S01E04"},
		{cfg.SeriesTemplate, MediaInfo{Title: "Doctor Who", Year: 2005, Series: true, Season: 2, Episode: 1, EpisodeEnd: 2, EpisodeTitle: "A/B?"}, "Serien/Doctor Who (2005)/Staffel 02/Doctor Who - S02E01-E02 - A-B"},
		{"{first_letter}/{title} [{resolution}]", MediaInfo{Title: "The Matrix", Resolution: "1080p"}, "M/The Matrix [1080p]"},
		{"{title}/../../etc/{title}", MediaInfo{Title: ".."}, "etc"},
	}
	for _, tt := range tests {
		if got := RenderTemplate(tt.tmpl, tt.info); got != filepath.FromSlash(tt.want) {
			t.Errorf("RenderTemplate(%q)\n got  %q\n want %q", tt.tmpl, got, tt.want)
		}
	}
	if err := CheckTemplate("{title} {nope}"); err == nil {
		t.Error("unbekannter Platzhalter wurde nicht gemeldet")
	}
}
