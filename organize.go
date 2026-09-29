package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Status eines Eintrags im Plan.
const (
	StatusReady     = "ready"     // bereit, Treffer aus der Datenbank
	StatusOffline   = "offline"   // bereit, nur aus dem Dateinamen (kein API-Key)
	StatusUnmatched = "unmatched" // kein eindeutiger Treffer, bitte prüfen
	StatusConflict  = "conflict"  // Ziel existiert oder ist doppelt
	StatusSame      = "same"      // liegt schon richtig
	StatusDone      = "done"
	StatusError     = "error"
)

// FileOp ist eine einzelne Datei-Aktion (verschieben oder kopieren).
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
			Lookup(ctx, db, it, it.Parsed.Title, it.Parsed.Year)
		}(it)
	}
	wg.Wait()

	PlanTargets(cfg, items)
	return items, nil
}

func infoFromParsed(p Parsed) MediaInfo {
	return MediaInfo{
		Title: p.Title, Year: p.Year, Series: p.Series, Season: p.Season,
		Episode: p.Episode, EpisodeEnd: p.EpisodeEnd, EpisodeTitle: p.EpisodeTitle,
		Resolution: p.Resolution,
	}
}

// Lookup sucht Treffer in der Datenbank und übernimmt den besten.
func Lookup(ctx context.Context, db *TMDB, it *Item, query string, year int) {
	it.Info = infoFromParsed(it.Parsed)
	it.Candidates, it.Matched, it.Message = nil, false, ""
	if !db.Enabled() || strings.TrimSpace(query) == "" {
		return
	}
	cands, err := db.Search(ctx, it.Parsed.Series, query, year)
	if err != nil {
		it.Message = err.Error()
		return
	}
	it.Candidates = cands
	if len(cands) > 0 {
		ApplyCandidate(ctx, db, it, cands[0])
	}
}

// ApplyCandidate übernimmt einen Datenbanktreffer in die Infos des Eintrags.
func ApplyCandidate(ctx context.Context, db *TMDB, it *Item, c Candidate) {
	it.Info.Title, it.Info.OriginalTitle, it.Info.Year, it.Info.TMDBID = c.Title, c.OriginalTitle, c.Year, c.ID
	it.Matched = true
	it.Message = ""
	if it.Info.Series && it.Info.Episode > 0 && db.Enabled() {
		if name, err := db.EpisodeTitle(ctx, c.ID, it.Info.Season, it.Info.Episode); err == nil && name != "" {
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
	seen := map[string]*Item{}
	for _, it := range items {
		if it.Status == StatusDone {
			continue
		}
		it.Status = ""
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
		case it.Info.Series && it.Info.Episode == 0:
			it.Status, it.Message = StatusUnmatched, "Keine Folgennummer erkannt"
		case !within(dst, it.Target):
			it.Status, it.Message = StatusError, "Ziel liegt außerhalb des Zielordners"
		case it.Target == it.Source:
			it.Status, it.Message = StatusSame, "Liegt schon richtig"
		case exists(it.Target):
			it.Status, it.Message = StatusConflict, "Ziel existiert bereits"
		}
		if it.Status == "" {
			for _, c := range it.Companions {
				if c.Target != c.Source && exists(c.Target) {
					it.Status, it.Message = StatusConflict, "Begleitdatei existiert bereits: "+filepath.Base(c.Target)
				}
			}
		}
		key := strings.ToLower(it.Target)
		if other, ok := seen[key]; ok && it.Status != StatusSame {
			it.Status, it.Message = StatusConflict, fmt.Sprintf("Gleiches Ziel wie Nr. %d", other.ID)
			if other.Status != StatusDone {
				other.Status, other.Message = StatusConflict, fmt.Sprintf("Gleiches Ziel wie Nr. %d", it.ID)
			}
		}
		seen[key] = it
		if it.Status != "" {
			continue
		}
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
}

func hasKey(cfg Config) bool { return strings.TrimSpace(cfg.TMDBKey) != "" }

// Journal protokolliert einen Sortierlauf, damit er rückgängig gemacht werden kann.
type Journal struct {
	Created   time.Time  `json:"created"`
	SourceDir string     `json:"source_dir"`
	TargetDir string     `json:"target_dir"`
	Ops       []FileOp   `json:"ops"`
	Undone    *time.Time `json:"undone,omitempty"`
}

// Apply führt die ausgewählten Einträge aus und schreibt ein Journal.
func Apply(cfg Config, items []*Item, ids map[int]bool, journalDir string) (*Journal, string, error) {
	dst, _ := filepath.Abs(cfg.TargetDir)
	src, _ := filepath.Abs(cfg.SourceDir)
	j := &Journal{Created: time.Now(), SourceDir: src, TargetDir: dst}
	for _, it := range items {
		if !ids[it.ID] {
			continue
		}
		switch it.Status {
		case StatusReady, StatusOffline, StatusUnmatched:
		default:
			continue
		}
		if it.Info.Title == "" {
			continue
		}
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
	data, err := os.ReadFile(path)
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
		case ActionCopy:
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
		if op.Action != ActionMove && op.Action != ActionCopy {
			return bad
		}
		if !within(j.TargetDir, op.Target) || op.Target == j.TargetDir || !within(j.SourceDir, op.Source) || op.Source == j.SourceDir {
			return bad
		}
	}
	return nil
}
