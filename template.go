package main

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// MediaInfo sind die endgültigen Daten, mit denen eine Vorlage gefüllt wird.
type MediaInfo struct {
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title,omitempty"`
	Year          int    `json:"year,omitempty"`
	Series        bool   `json:"series"`
	Season        int    `json:"season,omitempty"`
	Episode       int    `json:"episode,omitempty"`
	EpisodeEnd    int    `json:"episode_end,omitempty"`
	EpisodeTitle  string `json:"episode_title,omitempty"`
	Resolution    string `json:"resolution,omitempty"`
	VCodec        string `json:"vcodec,omitempty"`    // aus der Datei, z. B. HEVC
	HDR           string `json:"hdr,omitempty"`       // DV, HDR10, HLG
	Audio         string `json:"audio,omitempty"`     // Haupttonspur, z. B. "TrueHD 7.1"
	Languages     string `json:"languages,omitempty"` // Tonspur-Sprachen, z. B. "DE-EN"
	Part          int    `json:"part,omitempty"`      // Teil eines mehrteiligen Films
	TMDBID        int    `json:"tmdb_id,omitempty"`
}

// Placeholders listet alle Platzhalter für die Hilfe im Webinterface.
var Placeholders = []string{"title", "original_title", "year", "season", "episode", "episode_title", "resolution", "vcodec", "hdr", "audio", "languages", "part", "tmdb_id", "first_letter"}

var rePlaceholder = regexp.MustCompile(`\{([a-z_]+)(?::(\d+))?\}`)

// CheckTemplate meldet unbekannte Platzhalter.
func CheckTemplate(tmpl string) error {
	for _, m := range rePlaceholder.FindAllStringSubmatch(tmpl, -1) {
		known := false
		for _, p := range Placeholders {
			if p == m[1] {
				known = true
			}
		}
		if !known {
			return fmt.Errorf("unbekannter Platzhalter {%s}", m[1])
		}
	}
	return nil
}

// partName ist die Teilbezeichnung, die alle Mediaserver verstehen.
func partName(n int) string {
	if n <= 0 {
		return ""
	}
	return fmt.Sprintf("part%d", n)
}

func pad(n, width int) string {
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%0*d", width, n)
}

// RenderTemplate füllt eine Vorlage und liefert einen relativen Pfad ohne Endung.
func RenderTemplate(tmpl string, info MediaInfo) string {
	out := rePlaceholder.ReplaceAllStringFunc(tmpl, func(tok string) string {
		m := rePlaceholder.FindStringSubmatch(tok)
		width := 1
		if m[2] != "" {
			width, _ = strconv.Atoi(m[2])
		}
		switch m[1] {
		case "title":
			return sanitize(info.Title)
		case "original_title":
			if info.OriginalTitle == "" {
				return sanitize(info.Title)
			}
			return sanitize(info.OriginalTitle)
		case "year":
			return pad(info.Year, width)
		case "season":
			return pad(info.Season, width)
		case "episode":
			s := pad(info.Episode, width)
			if info.EpisodeEnd > info.Episode {
				s += "-E" + pad(info.EpisodeEnd, width)
			}
			return s
		case "episode_title":
			return sanitize(info.EpisodeTitle)
		case "resolution":
			return sanitize(info.Resolution)
		case "vcodec":
			return sanitize(info.VCodec)
		case "hdr":
			return sanitize(info.HDR)
		case "audio":
			return sanitize(info.Audio)
		case "languages":
			return sanitize(info.Languages)
		case "part":
			return partName(info.Part)
		case "tmdb_id":
			return pad(info.TMDBID, width)
		case "first_letter":
			for _, r := range sortTitle(info.Title) {
				if unicode.IsLetter(r) {
					return string(unicode.ToUpper(r))
				}
				if unicode.IsDigit(r) {
					return "0-9"
				}
			}
			return "#"
		}
		return tok
	})

	var parts []string
	for _, seg := range strings.Split(out, "/") {
		if seg = cleanSegment(seg); seg != "" {
			parts = append(parts, seg)
		}
	}
	// Mehrteilige Filme: Steht {part} nicht in der Vorlage, kommt " - part1"
	// an den Dateinamen. So erkennen Plex, Jellyfin und Kodi die Teile.
	if info.Part > 0 && len(parts) > 0 && !strings.Contains(tmpl, "{part") {
		parts[len(parts)-1] += " - " + partName(info.Part)
	}
	return filepath.Join(parts...)
}

// sortTitle entfernt führende Artikel für {first_letter}.
func sortTitle(t string) string {
	lower := strings.ToLower(t)
	for _, a := range []string{"the ", "der ", "die ", "das ", "a ", "an ", "ein ", "eine "} {
		if strings.HasPrefix(lower, a) {
			return t[len(a):]
		}
	}
	return t
}

// sanitize macht einen Wert dateinamentauglich (auch für Windows).
func sanitize(s string) string {
	s = strings.NewReplacer(
		": ", " - ", ":", "-", "/", "-", "\\", "-", "|", "-",
		"?", "", "*", "", "<", "", ">", "", "\"", "'",
	).Replace(s)
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

var (
	reEmptyBrackets = regexp.MustCompile(`\(\s*\)|\[\s*\]|\{\s*\}`)
	reDoubleDash    = regexp.MustCompile(`\s-(\s+-)+\s`)
	reOpenSpace     = regexp.MustCompile(`([(\[{])\s+`)
	reCloseSpace    = regexp.MustCompile(`\s+([)\]}])`)
)

// cleanSegment räumt Reste leerer Platzhalter auf, z. B. "Titel ()" oder "S01E02 - ".
func cleanSegment(s string) string {
	s = reEmptyBrackets.ReplaceAllString(s, "")
	s = reSpaces.ReplaceAllString(s, " ")
	s = reOpenSpace.ReplaceAllString(s, "$1")
	s = reCloseSpace.ReplaceAllString(s, "$1")
	s = reDoubleDash.ReplaceAllString(s, " - ")
	s = strings.Trim(s, " -_.")
	return s
}
