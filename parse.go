package main

import (
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Parsed ist das, was der Bär aus einem unordentlichen Dateinamen herausliest.
type Parsed struct {
	Title        string `json:"title"`
	Year         int    `json:"year,omitempty"`
	Series       bool   `json:"series"`
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeEnd   int    `json:"episode_end,omitempty"`
	EpisodeTitle string `json:"episode_title,omitempty"`
	Resolution   string `json:"resolution,omitempty"`
	Part         int    `json:"part,omitempty"`     // Teil eines mehrteiligen Films (CD1, part2 …)
	Absolute     int    `json:"absolute,omitempty"` // fortlaufende Folgennummer ohne Staffel (Anime)
	IMDBID       string `json:"imdb_id,omitempty"`  // z. B. tt0133093, wenn im Namen
	TMDBID       int    `json:"tmdb_id,omitempty"`  // aus "{tmdb-603}"
}

var (
	reSxxExx     = regexp.MustCompile(`(?i)\bs(\d{1,2}) ?e(\d{1,3})(?: ?-? ?e(\d{1,3}))?\b`)
	reNxNN       = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	reSeasonEp   = regexp.MustCompile(`(?i)\b(?:season|staffel)[ -]*(\d{1,2})[ -]*(?:episode|folge|ep|e)[ -]*(\d{1,3})\b`)
	reYear       = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	reResolution = regexp.MustCompile(`(?i)\b(2160p|1080p|1080i|720p|576p|480p|4k|uhd)\b`)
	reJunk       = regexp.MustCompile(`(?i)\b(2160p|1080p|1080i|720p|576p|480p|4k|uhd|blu ?ray|bdrip|brrip|dvdrip|dvd9|dvd5|web ?-?dl|webrip|hdtv|hdrip|x264|x265|h ?264|h ?265|hevc|avc|xvid|divx|aac|ac3|eac3|dts|dd5|ddp5?|atmos|truehd|remux|proper|repack|extended|unrated|uncut|directors cut|german|deutsch|multi|dubbed|subbed|hdr|hdr10|10bit|complete)\b`)
	reLeadGroup  = regexp.MustCompile(`^\s*\[[^\]]*\]\s*`)
	reBrackets   = regexp.MustCompile(`[\[\(\{][^\]\)\}]*[\]\)\}]`)
	reSpaces     = regexp.MustCompile(`\s+`)
	reSeasonDir  = regexp.MustCompile(`(?i)^(?:season|staffel|s)[ ._-]*(\d{1,2})$`)
	reEpOnly     = regexp.MustCompile(`(?i)(?:^|\b(?:e|ep|episode|folge) ?)(\d{1,3})\b`)
	// Teile eines Films. "cd", "disc" und "pt" sind eindeutig, "part"
	// und "teil" nur hinter der Jahreszahl ("Harry Potter … Part 1 (2010)" ist ein Titel).
	rePartSure = regexp.MustCompile(`(?i)\b(?:cd|dis[ck]|pt)[ -]?(\d{1,2})\b`)
	// Fortlaufende Folgennummern ohne Staffel: "One.Piece.E1071", "Folge 12",
	// "[Gruppe] One Piece - 1071 [1080p]".
	reAbsEp    = regexp.MustCompile(`(?i)(?:^|\s)(?:e|ep|folge)[ -]?(\d{1,4})\b`)
	reAbsAnime = regexp.MustCompile(`\s-\s(\d{2,4})(?:v\d)?(?:\s|$)`)
	// IDs im Namen, auch in der Plex/Jellyfin-Schreibweise "{imdb-tt0133093}" oder "[tmdbid=603]".
	reIMDB      = regexp.MustCompile(`(?i)[\[{(]?\s*(?:imdb(?:id)?\s*[-=:]\s*)?\b(tt\d{7,9})\b\s*[\]})]?`)
	reTMDBID    = regexp.MustCompile(`(?i)[\[{(]\s*tmdb(?:id)?\s*[-=:]\s*(\d{1,9})\s*[\]})]`)
	rePartMaybe = regexp.MustCompile(`(?i)\b(?:part|teil)[ -]?(\d{1,2})\b`)
)

// cutPart sucht eine Teilangabe, entfernt sie aus s und liefert die Nummer.
// after ist die Position, ab der auch "part"/"teil" zählen (-1: nie).
func cutPart(s string, after int) (string, int) {
	m := rePartSure.FindStringSubmatchIndex(s)
	if m == nil && after >= 0 {
		if mm := rePartMaybe.FindStringSubmatchIndex(s); mm != nil && mm[0] >= after {
			m = mm
		}
	}
	if m == nil {
		return s, 0
	}
	n, _ := strconv.Atoi(s[m[2]:m[3]])
	if n == 0 {
		return s, 0
	}
	return strings.TrimSpace(reSpaces.ReplaceAllString(s[:m[0]]+" "+s[m[1]:], " ")), n
}

// normalize ersetzt Punkte und Unterstriche durch Leerzeichen und entfernt
// führende Release-Gruppen wie "[Gruppe]".
func normalize(name string) string {
	for reLeadGroup.MatchString(name) {
		name = reLeadGroup.ReplaceAllString(name, "")
	}
	name = strings.NewReplacer(".", " ", "_", " ").Replace(name)
	return strings.TrimSpace(reSpaces.ReplaceAllString(name, " "))
}

// cleanTitle entfernt Klammerreste, Trenner und Leerzeichen an den Rändern.
func cleanTitle(s string) string {
	// Klammern mit Release-Kram oder Zahlen fliegen raus, "(US)" o. ä. bleibt als Text.
	s = reBrackets.ReplaceAllStringFunc(s, func(b string) string {
		inner := strings.TrimSpace(b[1 : len(b)-1])
		if inner == "" || reJunk.MatchString(inner) || strings.Trim(inner, "0123456789 ") == "" {
			return " "
		}
		return " " + inner + " "
	})
	s = strings.NewReplacer("(", " ", ")", " ", "[", " ", "]", " ", "{", " ", "}", " ").Replace(s)
	s = reSpaces.ReplaceAllString(s, " ")
	return strings.Trim(s, " -–:,")
}

// cutJunk schneidet alles ab dem ersten Qualitäts-/Release-Merkmal ab.
func cutJunk(s string) string {
	if loc := reJunk.FindStringIndex(s); loc != nil {
		s = s[:loc[0]]
	}
	// Release-Gruppe am Ende, z. B. "-GRUPPE"
	if i := strings.LastIndex(s, "-"); i > 0 && !strings.Contains(s[i:], " ") {
		s = s[:i]
	}
	return s
}

// ParseName liest Titel, Jahr, Staffel und Folge aus einem Datei- oder Ordnernamen.
func ParseName(name string) Parsed {
	var p Parsed
	// IDs vor dem Normalisieren suchen, damit Klammern und Punkte noch stimmen.
	if m := reIMDB.FindStringSubmatchIndex(name); m != nil {
		p.IMDBID = strings.ToLower(name[m[2]:m[3]])
		name = name[:m[0]] + " " + name[m[1]:]
	}
	if m := reTMDBID.FindStringSubmatchIndex(name); m != nil {
		p.TMDBID, _ = strconv.Atoi(name[m[2]:m[3]])
		name = name[:m[0]] + " " + name[m[1]:]
	}
	s := normalize(name)
	if m := reResolution.FindStringSubmatch(s); m != nil {
		p.Resolution = strings.ToLower(m[1])
	}

	// Serienmuster
	var loc []int
	if m := reSxxExx.FindStringSubmatchIndex(s); m != nil {
		loc = m
		p.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		p.Episode, _ = strconv.Atoi(s[m[4]:m[5]])
		if m[6] >= 0 {
			p.EpisodeEnd, _ = strconv.Atoi(s[m[6]:m[7]])
		}
	} else if m := reSeasonEp.FindStringSubmatchIndex(s); m != nil {
		loc = m
		p.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		p.Episode, _ = strconv.Atoi(s[m[4]:m[5]])
	} else if m := reNxNN.FindStringSubmatchIndex(s); m != nil {
		loc = m
		p.Season, _ = strconv.Atoi(s[m[2]:m[3]])
		p.Episode, _ = strconv.Atoi(s[m[4]:m[5]])
	}
	if loc == nil {
		// Fortlaufende Nummer, aber keine Jahreszahl als Nummer ("Film - 2019").
		for _, re := range []*regexp.Regexp{reAbsEp, reAbsAnime} {
			if m := re.FindStringSubmatchIndex(s); m != nil && !reYear.MatchString(s[m[2]:m[3]]) {
				n, _ := strconv.Atoi(s[m[2]:m[3]])
				if n > 0 {
					loc, p.Episode, p.Absolute = m, n, n
					break
				}
			}
		}
	}
	if loc != nil {
		p.Series = true
		head, tail := s[:loc[0]], s[loc[1]:]
		if y := lastYear(head); y != nil {
			p.Year, _ = strconv.Atoi(head[y[2]:y[3]])
			head = head[:y[0]]
		}
		p.Title = cleanTitle(cutJunk(head))
		p.EpisodeTitle = cleanTitle(cutJunk(tail))
		return p
	}

	// Film: Teilangabe (CD1, part2 …) heraustrennen, dann steht der Titel vor
	// dem (letzten) Jahr.
	after := -1
	if y := lastYear(s); y != nil {
		after = y[1]
	}
	s, p.Part = cutPart(s, after)
	if y := lastYear(s); y != nil {
		p.Year, _ = strconv.Atoi(s[y[2]:y[3]])
		p.Title = cleanTitle(s[:y[0]])
		return p
	}
	p.Title = cleanTitle(cutJunk(s))
	return p
}

// lastYear sucht die letzte Jahreszahl, die nicht am Anfang steht
// (damit "1917" oder "2012" als Titel erhalten bleiben).
func lastYear(s string) []int {
	all := reYear.FindAllStringSubmatchIndex(s, -1)
	for i := len(all) - 1; i >= 0; i-- {
		if strings.TrimSpace(strings.Trim(s[:all[i][0]], "([{ ")) != "" {
			return all[i]
		}
	}
	return nil
}

// ParsePath parst eine Datei und zieht bei Bedarf die Ordnernamen hinzu,
// z. B. "Breaking Bad/Staffel 1/S01E02.mkv" oder "Inception (2010)/film.mkv".
func ParsePath(path string) Parsed {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	p := ParseName(base)

	dir := filepath.Dir(path)
	dirName := filepath.Base(dir)
	dirSeason := 0
	if m := reSeasonDir.FindStringSubmatch(normalize(dirName)); m != nil {
		dirSeason, _ = strconv.Atoi(m[1])
		dir = filepath.Dir(dir)
		dirName = filepath.Base(dir)
	}
	if dirName == "." || dirName == string(filepath.Separator) {
		return p
	}
	d := ParseName(dirName)

	if p.Series {
		if p.Absolute > 0 && dirSeason > 0 {
			// "Staffel 2/E05.mkv": die Nummer zählt innerhalb der Staffel.
			p.Season, p.Absolute = dirSeason, 0
		}
		if p.Title == "" {
			p.Title = d.Title
			if p.Year == 0 {
				p.Year = d.Year
			}
		}
		return withIDs(p, p, d)
	}
	if dirSeason > 0 {
		// Datei im Staffelordner ohne SxxExx: nur die Folgennummer im Namen?
		if m := reEpOnly.FindStringSubmatch(normalize(base)); m != nil {
			ep, _ := strconv.Atoi(m[1])
			return withIDs(Parsed{Title: d.Title, Year: d.Year, Series: true, Season: dirSeason, Episode: ep, Resolution: p.Resolution}, p, d)
		}
	}
	if p.Title == "" || (p.Year == 0 && d.Year != 0 && !d.Series) {
		res := p.Resolution
		if res == "" {
			res = d.Resolution
		}
		p = withIDs(Parsed{Title: d.Title, Year: d.Year, Resolution: res, Part: p.Part}, p, d)
	}
	return withIDs(p, p, d)
}

// withIDs übernimmt IMDb- und TMDB-ID aus dem Dateinamen, sonst aus dem
// Ordner (bei Serien der Serienordner, dann steht die ID für die ganze Serie).
func withIDs(out, file, dir Parsed) Parsed {
	out.IMDBID, out.TMDBID = file.IMDBID, file.TMDBID
	if out.IMDBID == "" && out.TMDBID == 0 {
		out.IMDBID, out.TMDBID = dir.IMDBID, dir.TMDBID
	}
	return out
}
