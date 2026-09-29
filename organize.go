package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Status eines Eintrags im Plan.
const (
	StatusReady     = "ready"     // bereit, Treffer aus der Datenbank
	StatusOffline   = "offline"   // bereit, nur aus dem Dateinamen (kein API-Key)
	StatusUnmatched = "unmatched" // kein eindeutiger Treffer, bitte prüfen
	StatusConflict  = "conflict"  // Ziel existiert bereits
	StatusDuplicate = "duplicate" // mehrere Dateien wollen dasselbe Ziel, eine davon wählen
	StatusSame      = "same"      // liegt schon richtig
	StatusDone      = "done"
	StatusError     = "error"
)

// ActionCreate steht im Verlauf für eine neu angelegte Datei (NFO, Bild).
const ActionCreate = "create"

// FileOp ist eine einzelne Datei-Aktion (verschieben, kopieren oder neu angelegt).
type FileOp struct {
	Source string `json:"source"`
	Target string `json:"target"`
	Action string `json:"action"`
}

// Item ist eine Videodatei samt Begleitdateien im Plan.
type Item struct {
	ID         int         `json:"id"`
	Source     string      `json:"source"`
	RelSource  string      `json:"rel_source"`
	Parsed     Parsed      `json:"parsed"`
	Media      *Media      `json:"media,omitempty"` // aus der Datei gelesen, nil wenn unbekannt
	Size       int64       `json:"size"`
	Better     string      `json:"better,omitempty"` // bei Duplikaten: warum diese Version besser ist
	Info       MediaInfo   `json:"info"`
	Candidates []Candidate `json:"candidates"`
	Matched    bool        `json:"matched"`
	Action     string      `json:"action"`
	Target     string      `json:"target"`
	RelTarget  string      `json:"rel_target"`
	Companions []FileOp    `json:"companions"`
	Status     string      `json:"status"`
	Message    string      `json:"message,omitempty"`
}

var subDirNames = map[string]bool{"subs": true, "sub": true, "subtitles": true, "untertitel": true}

type scanned struct {
	path string
	rule *FileRule
}

// Scan durchsucht den Quellordner und baut einen Plan. Es wird nichts verändert.
func Scan(ctx context.Context, cfg Config, db *TMDB) ([]*Item, error) {
	src, err := filepath.Abs(cfg.SourceDir)
	if err != nil || cfg.SourceDir == "" {
		return nil, errors.New("bitte einen Quellordner angeben")
	}
	if st, err := os.Stat(src); err != nil || !st.IsDir() {
		return nil, fmt.Errorf("Quellordner %s nicht gefunden", src)
	}
	if cfg.TargetDir == "" {
		return nil, errors.New("bitte einen Zielordner angeben")
	}
	dst, _ := filepath.Abs(cfg.TargetDir)

	videos := map[string][]scanned{}     // Ordner -> Videos
	companions := map[string][]scanned{} // Ordner -> Begleitdateien
	err = filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // unlesbare Ordner überspringen
		}
		if d.IsDir() {
			if p != src && (strings.HasPrefix(d.Name(), ".") || p == dst) {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() || cfg.Ignored(d.Name()) {
			return nil
		}
		rule := cfg.RuleFor(p)
		if rule == nil || rule.Action == ActionSkip {
			return nil
		}
		dir := filepath.Dir(p)
		if rule.Kind == KindVideo {
			videos[dir] = append(videos[dir], scanned{p, rule})
		} else {
			companions[dir] = append(companions[dir], scanned{p, rule})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	var items []*Item
	used := map[string]bool{}
	dirs := make([]string, 0, len(videos))
	for d := range videos {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		vs := videos[dir]
		sort.Slice(vs, func(i, j int) bool { return vs[i].path < vs[j].path })
		for _, v := range vs {
			rel, _ := filepath.Rel(src, v.path)
			it := &Item{ID: len(items) + 1, Source: v.path, RelSource: rel, Action: v.rule.Action}
			it.Parsed = ParsePath(rel)
			base := strings.ToLower(strings.TrimSuffix(filepath.Base(v.path), filepath.Ext(v.path)))
			var cands []scanned
			cands = append(cands, companions[dir]...)
			if len(vs) == 1 {
				for sub := range subDirNames {
					for d, cs := range companions {
						if filepath.Dir(d) == dir && strings.ToLower(filepath.Base(d)) == sub {
							cands = append(cands, cs...)
						}
					}
				}
			}
			for _, c := range cands {
				name := strings.ToLower(filepath.Base(c.path))
				if used[c.path] || !(strings.HasPrefix(name, base) || len(vs) == 1) {
					continue
				}
				used[c.path] = true
				it.Companions = append(it.Companions, FileOp{Source: c.path, Action: c.rule.Action})
			}
			items = append(items, it)
		}
	}

	// Metadaten parallel abrufen, aber freundlich zur API.
	sem := make(chan struct{}, 4)
	var wg sync.WaitGroup
	for _, it := range items {
		wg.Add(1)
		sem <- struct{}{}
		go func(it *Item) {
			defer func() { <-sem; wg.Done() }()
			it.Media = ProbeFile(it.Source)
			if it.Parsed.IMDBID == "" && it.Parsed.TMDBID == 0 {
				it.Parsed.IMDBID, it.Parsed.TMDBID = nfoIDs(it.Source, it.Parsed.Series)
			}
			if st, err := os.Stat(it.Source); err == nil {
				it.Size = st.Size()
			}
			Lookup(ctx, db, it, it.Parsed.Title, it.Parsed.Year)
		}(it)
	}
	wg.Wait()

	PlanTargets(cfg, items)
	return items, nil
}

var (
	reNFOTMDB = regexp.MustCompile(`(?is)<uniqueid[^>]*type="tmdb"[^>]*>\s*(\d{1,9})\s*</uniqueid>|themoviedb\.org/(?:movie|tv)/(\d{1,9})`)
	reNFOIMDB = regexp.MustCompile(`\b(tt\d{7,9})\b`)
)

// nfoIDs sucht IDs in einer NFO, die schon neben der Datei liegt (z. B. von
// Kodi oder Jellyfin): erst „<name>.nfo“, dann movie.nfo bzw. tvshow.nfo im
// Ordner der Serie. Bei Folgen zählt nur tvshow.nfo, denn die IMDb-ID einer
// Folgen-NFO beschreibt die Folge, nicht die Serie.
func nfoIDs(file string, series bool) (imdb string, tmdb int) {
	dir := filepath.Dir(file)
	var paths []string
	if series {
		paths = []string{filepath.Join(dir, "tvshow.nfo"), filepath.Join(filepath.Dir(dir), "tvshow.nfo")}
	} else {
		paths = []string{strings.TrimSuffix(file, filepath.Ext(file)) + ".nfo", filepath.Join(dir, "movie.nfo")}
	}
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || !st.Mode().IsRegular() || st.Size() > 1<<20 {
			continue
		}
		data, err := os.ReadFile(p) // #nosec G304 -- NFO neben einer Datei, die der Nutzer einsortieren will
		if err != nil {
			continue
		}
		if m := reNFOTMDB.FindSubmatch(data); m != nil {
			id := m[1]
			if len(id) == 0 {
				id = m[2]
			}
			tmdb, _ = strconv.Atoi(string(id))
		}
		if m := reNFOIMDB.FindSubmatch(data); m != nil {
			imdb = string(m[1])
		}
		if imdb != "" || tmdb > 0 {
			return imdb, tmdb
		}
	}
	return "", 0
}

func infoFromParsed(p Parsed) MediaInfo {
	return MediaInfo{
		Title: p.Title, Year: p.Year, Series: p.Series, Season: p.Season,
		Episode: p.Episode, EpisodeEnd: p.EpisodeEnd, EpisodeTitle: p.EpisodeTitle,
		Resolution: p.Resolution, Part: p.Part, Absolute: p.Absolute, IMDBID: p.IMDBID,
	}
}

// applyMedia übernimmt, was in der Datei steht. Es hat Vorrang vor dem Dateinamen.
func applyMedia(i *MediaInfo, m *Media) {
	if m == nil {
		return
	}
	if r := m.Resolution(); r != "" {
		i.Resolution = r
	}
	i.VCodec, i.HDR, i.Audio, i.Languages = m.VCodec, m.HDR, m.MainAudio().String(), m.Languages()
}

// Lookup sucht Treffer in der Datenbank und übernimmt den besten. Steht eine
// IMDb- oder TMDB-ID im Namen (oder in einer NFO), geht das vor der Titelsuche.
// Findet TMDB zu einer Serie nichts, fragt der Bär TheTVDB.
func Lookup(ctx context.Context, db *TMDB, it *Item, query string, year int) {
	it.Info = infoFromParsed(it.Parsed)
	applyMedia(&it.Info, it.Media)
	it.Candidates, it.Matched, it.Message = nil, false, ""
	query = strings.TrimSpace(query)
	if query == "" {
		return
	}
	// IDs aus dem Namen gelten nur für die automatische Suche, eine
	// eingetippte IMDb-ID im Suchfeld immer.
	imdb, tmdbID := "", 0
	if reIMDBOnly.MatchString(query) {
		imdb = strings.ToLower(query)
	} else if query == it.Parsed.Title {
		imdb, tmdbID = it.Parsed.IMDBID, it.Parsed.TMDBID
	}
	var cands []Candidate
	if db.Enabled() {
		switch {
		case tmdbID > 0:
			if c, err := db.Details(ctx, it.Info.Series, tmdbID); err == nil {
				cands = []Candidate{c}
			}
		case imdb != "":
			if f, err := db.Find(ctx, "imdb_id", imdb); err == nil {
				it.Info.Series, it.Info.IMDBID = f.Series, imdb
				if f.Season > 0 || f.Episode > 0 {
					it.Info.Season, it.Info.Episode, it.Info.EpisodeEnd, it.Info.Absolute = f.Season, f.Episode, 0, 0
				}
				cands = []Candidate{f.Candidate}
			}
		}
		if cands == nil && query != imdb {
			var err error
			if cands, err = db.Search(ctx, it.Info.Series, query, year); err != nil {
				it.Message = err.Error()
			}
		}
	}
	if len(cands) == 0 && it.Info.Series && db != nil && db.TVDB.Enabled() && imdb != query {
		tv, err := db.TVDB.Search(ctx, query, year)
		if err != nil && it.Message == "" {
			it.Message = err.Error()
		}
		cands = tv
	}
	it.Candidates = cands
	if len(cands) > 0 {
		ApplyCandidate(ctx, db, it, cands[0])
	}
}

var reIMDBOnly = regexp.MustCompile(`(?i)^tt\d{7,9}$`)

// ApplyCandidate übernimmt einen Datenbanktreffer in die Infos des Eintrags.
func ApplyCandidate(ctx context.Context, db *TMDB, it *Item, c Candidate) {
	it.Info.Title, it.Info.OriginalTitle, it.Info.Year = c.Title, c.OriginalTitle, c.Year
	it.Matched = true
	it.Message = ""
	if c.Source == "tvdb" {
		applyTVDB(ctx, db, it, c)
		return
	}
	it.Info.TMDBID, it.Info.TVDBID, it.Info.Source = c.ID, 0, "tmdb"
	if db.Enabled() {
		// IMDb- und TheTVDB-ID für Platzhalter und NFO (zwischengespeichert, einmal pro Titel).
		if m, err := db.Meta(ctx, it.Info.Series, c.ID); err == nil {
			if m.ExternalIDs.IMDBID != "" && !(it.Info.Series && it.Info.IMDBID != "") {
				it.Info.IMDBID = m.ExternalIDs.IMDBID
			}
			it.Info.TVDBID = m.ExternalIDs.TVDBID
		}
	}
	if it.Info.Series && it.Info.Absolute > 0 && it.Info.Season == 0 && db.Enabled() {
		if seasons, err := db.Seasons(ctx, c.ID); err == nil {
			if s, e, ok := absoluteEpisode(seasons, it.Info.Absolute); ok {
				it.Info.Season, it.Info.Episode = s, e
				it.Message = fmt.Sprintf("Folge %d ist fortlaufend gezählt, laut TMDB ist das Staffel %d, Folge %d. Bitte kurz prüfen.", it.Info.Absolute, s, e)
			}
		}
	}
	if it.Info.Series && it.Info.Episode > 0 && db.Enabled() {
		if name, err := db.EpisodeTitle(ctx, c.ID, it.Info.Season, it.Info.Episode); err == nil && name != "" {
			it.Info.EpisodeTitle = name
		}
	}
}

// applyTVDB übernimmt einen Treffer von TheTVDB. Kennt TMDB die Serie unter
// ihrer TheTVDB-ID, bekommt der Eintrag auch die TMDB-ID (für NFO und Bilder).
func applyTVDB(ctx context.Context, db *TMDB, it *Item, c Candidate) {
	it.Info.TVDBID, it.Info.TMDBID, it.Info.Source = c.ID, 0, "tvdb"
	if db.Enabled() {
		if f, err := db.Find(ctx, "tvdb_id", strconv.Itoa(c.ID)); err == nil && f.Series {
			it.Info.TMDBID = f.ID
		}
	}
	if db == nil {
		return
	}
	tv := db.TVDB
	if !tv.Enabled() || !it.Info.Series {
		return
	}
	if it.Info.Absolute > 0 && it.Info.Season == 0 {
		if e, err := tv.Absolute(ctx, c.ID, it.Info.Absolute); err == nil {
			it.Info.Season, it.Info.Episode, it.Info.EpisodeTitle = e.Season, e.Number, e.Name
			it.Message = fmt.Sprintf("Folge %d ist fortlaufend gezählt, laut TheTVDB ist das Staffel %d, Folge %d. Bitte kurz prüfen.", it.Info.Absolute, e.Season, e.Number)
		}
		return
	}
	if it.Info.Episode > 0 {
		if name, err := tv.EpisodeTitle(ctx, c.ID, it.Info.Season, it.Info.Episode); err == nil && name != "" {
			it.Info.EpisodeTitle = name
		}
	}
}

// companionSuffix liefert den Teil des Begleitdatei-Namens, der an den neuen
// Videonamen angehängt wird, z. B. ".de.forced.srt".
func companionSuffix(video, companion string) string {
	vb := strings.TrimSuffix(filepath.Base(video), filepath.Ext(video))
	cn := filepath.Base(companion)
	if len(cn) > len(vb) && strings.EqualFold(cn[:len(vb)], vb) {
		return cn[len(vb):]
	}
	return "." + cn
}

// PlanTargets berechnet Zielpfade und Status für alle offenen Einträge.
func PlanTargets(cfg Config, items []*Item) {
	dst, _ := filepath.Abs(cfg.TargetDir)
	groups := map[string][]*Item{}
	for _, it := range items {
		if it.Status == StatusDone {
			continue
		}
		it.Status, it.Better = "", ""
		tmpl := cfg.MovieTemplate
		if it.Info.Series {
			tmpl = cfg.SeriesTemplate
		}
		rel := RenderTemplate(tmpl, it.Info)
		it.Target = filepath.Join(dst, rel+strings.ToLower(filepath.Ext(it.Source)))
		it.RelTarget, _ = filepath.Rel(dst, it.Target)
		newBase := strings.TrimSuffix(it.Target, filepath.Ext(it.Target))
		for i := range it.Companions {
			it.Companions[i].Target = newBase + companionSuffix(it.Source, it.Companions[i].Source)
		}

		switch {
		case it.Info.Title == "" || rel == "":
			it.Status, it.Message = StatusUnmatched, "Kein Titel erkannt"
		case it.Info.Series && it.Info.Absolute > 0 && it.Info.Season == 0:
			it.Status, it.Message = StatusUnmatched, fmt.Sprintf("Folge %d ist fortlaufend gezählt. Bitte unter „Anpassen“ Staffel und Folge wählen.", it.Info.Absolute)
		case it.Info.Series && it.Info.Episode == 0:
			it.Status, it.Message = StatusUnmatched, "Keine Folgennummer erkannt"
		case !within(dst, it.Target):
			it.Status, it.Message = StatusError, "Ziel liegt außerhalb des Zielordners"
		case it.Target == it.Source:
			it.Status, it.Message = StatusSame, "Liegt schon richtig"
		case exists(it.Target):
			it.Status, it.Message = StatusConflict, "Ziel existiert bereits. Über „Anpassen“ kannst du einen anderen Namen vergeben."
		}
		if it.Status == "" {
			for _, c := range it.Companions {
				if c.Target != c.Source && exists(c.Target) {
					it.Status, it.Message = StatusConflict, "Begleitdatei existiert bereits: "+filepath.Base(c.Target)
				}
			}
		}
		if it.Status == "" {
			switch {
			case it.Matched:
				it.Status = StatusReady
			case hasKey(cfg):
				it.Status = StatusUnmatched
				if it.Message == "" {
					it.Message = "Nichts in der Datenbank gefunden"
				}
			default:
				it.Status = StatusOffline
			}
		}
		if selectableStatus(it.Status) {
			key := strings.ToLower(it.Target)
			groups[key] = append(groups[key], it)
		}
	}

	// Mehrere Dateien mit demselben Ziel: als "doppelt" markieren, die
	// jeweils anderen beim Namen nennen und die bessere Version vorschlagen.
	for _, g := range groups {
		if len(g) < 2 {
			continue
		}
		best, why := suggestBest(g)
		for _, it := range g {
			var others []string
			for _, o := range g {
				if o != it {
					others = append(others, "„"+o.RelSource+"“")
				}
			}
			it.Status = StatusDuplicate
			switch {
			case best == nil:
				it.Message = "Gleiches Ziel wie " + strings.Join(others, ", ") + ". Wähle eine davon aus."
			case it == best:
				it.Better = why
				it.Message = "Gleiches Ziel wie " + strings.Join(others, ", ") + ". Vorschlag: diese Version, " + why + "."
			default:
				it.Message = "Gleiches Ziel wie " + strings.Join(others, ", ") + ". Die bessere Version ist „" + best.RelSource + "“ (" + why + ")."
			}
		}
	}
}

// selectableStatus meldet, ob ein Eintrag mit diesem Status einsortiert werden darf.
func selectableStatus(s string) bool {
	switch s {
	case StatusReady, StatusOffline, StatusUnmatched, StatusDuplicate:
		return true
	}
	return false
}

func hasKey(cfg Config) bool { return strings.TrimSpace(cfg.TMDBKey) != "" }

// Journal protokolliert einen Sortierlauf, damit er rückgängig gemacht werden kann.
type Journal struct {
	Created   time.Time  `json:"created"`
	SourceDir string     `json:"source_dir"`
	TargetDir string     `json:"target_dir"`
	Ops       []FileOp   `json:"ops"`
	Warnings  []string   `json:"warnings,omitempty"`
	Undone    *time.Time `json:"undone,omitempty"`
}

// ExtrasFunc legt nach dem Einsortieren eines Eintrags Zusatzdateien an und
// liefert die neuen Dateien sowie Hinweise zu Fehlschlägen.
type ExtrasFunc func(it *Item) (created, warns []string)

// Apply führt die ausgewählten Einträge aus und schreibt ein Journal.
// extras darf nil sein.
func Apply(cfg Config, items []*Item, ids map[int]bool, journalDir string, extras ExtrasFunc) (*Journal, string, error) {
	dst, _ := filepath.Abs(cfg.TargetDir)
	src, _ := filepath.Abs(cfg.SourceDir)
	j := &Journal{Created: time.Now(), SourceDir: src, TargetDir: dst}
	taken := map[string]bool{} // von doppelten Einträgen nur den ersten gewählten ausführen
	for _, it := range items {
		if !ids[it.ID] || !selectableStatus(it.Status) || it.Info.Title == "" {
			continue
		}
		key := strings.ToLower(it.Target)
		if taken[key] {
			continue
		}
		taken[key] = true
		ops := append([]FileOp{{Source: it.Source, Target: it.Target, Action: it.Action}}, it.Companions...)
		it.Status, it.Message = StatusDone, ""
		for _, op := range ops {
			if op.Source == op.Target {
				continue
			}
			err := insideReal(dst, op.Target)
			if err == nil {
				err = runOp(op)
			}
			if err != nil {
				it.Status, it.Message = StatusError, filepath.Base(op.Source)+": "+err.Error()
				break
			}
			j.Ops = append(j.Ops, op)
		}
		if it.Status == StatusDone && extras != nil {
			created, warns := extras(it)
			for _, p := range created {
				j.Ops = append(j.Ops, FileOp{Target: p, Action: ActionCreate})
			}
			j.Warnings = append(j.Warnings, warns...)
		}
	}
	if len(j.Ops) == 0 {
		return j, "", nil
	}
	if err := os.MkdirAll(journalDir, 0o700); err != nil {
		return j, "", err
	}
	path := filepath.Join(journalDir, j.Created.Format("2006-01-02_15-04-05")+".json")
	for n := 2; exists(path); n++ {
		path = filepath.Join(journalDir, fmt.Sprintf("%s_%d.json", j.Created.Format("2006-01-02_15-04-05"), n))
	}
	return j, path, writeJournal(path, j)
}

func writeJournal(path string, j *Journal) error {
	data, err := json.MarshalIndent(j, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}

func ReadJournal(path string) (*Journal, error) {
	data, err := os.ReadFile(path) // #nosec G304 -- Pfad kommt aus dem eigenen Verlaufsordner, Name wird im Server geprüft
	if err != nil {
		return nil, err
	}
	var j Journal
	return &j, json.Unmarshal(data, &j)
}

// Undo macht einen Sortierlauf rückgängig: Verschobenes wandert zurück,
// Kopien werden entfernt. Nichts wird überschrieben.
func Undo(path string) ([]string, error) {
	j, err := ReadJournal(path)
	if err != nil {
		return nil, err
	}
	if j.Undone != nil {
		return nil, errors.New("dieser Lauf wurde schon rückgängig gemacht")
	}
	if err := j.validate(); err != nil {
		return nil, err
	}
	var problems []string
	for i := len(j.Ops) - 1; i >= 0; i-- {
		op := j.Ops[i]
		var err error
		switch op.Action {
		case ActionMove:
			err = moveFile(op.Target, op.Source)
		case ActionCopy, ActionCreate:
			if err = requireRegular(op.Target); err == nil {
				err = os.Remove(op.Target)
			}
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			problems = append(problems, filepath.Base(op.Target)+": "+err.Error())
			continue
		}
		pruneEmpty(j.TargetDir, filepath.Dir(op.Target))
	}
	now := time.Now()
	j.Undone = &now
	return problems, writeJournal(path, j)
}

// validate stellt sicher, dass ein (evtl. manipuliertes) Journal nur Dateien
// innerhalb von Quell- und Zielordner anfasst.
func (j *Journal) validate() error {
	bad := errors.New("Verlaufseintrag ist ungültig")
	for _, root := range []string{j.SourceDir, j.TargetDir} {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return bad
		}
	}
	for _, op := range j.Ops {
		if !within(j.TargetDir, op.Target) || op.Target == j.TargetDir {
			return bad
		}
		switch op.Action {
		case ActionMove, ActionCopy:
			if !within(j.SourceDir, op.Source) || op.Source == j.SourceDir {
				return bad
			}
		case ActionCreate:
			if op.Source != "" {
				return bad
			}
		default:
			return bad
		}
	}
	return nil
}
