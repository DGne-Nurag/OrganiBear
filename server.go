package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Server hält Konfiguration und den aktuellen Plan.
type Server struct {
	mu         sync.Mutex
	cfg        Config
	cfgPath    string
	journalDir string
	db         *TMDB
	items      []*Item
	static     fs.FS
}

func NewServer(cfg Config, cfgPath string, static fs.FS) *Server {
	return &Server{
		cfg:        cfg,
		cfgPath:    cfgPath,
		journalDir: filepath.Join(filepath.Dir(cfgPath), "organibear-verlauf"),
		db:         NewTMDB(cfg.TMDBKey, cfg.Language),
		static:     static,
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("POST /api/template-preview", s.templatePreview)
	mux.HandleFunc("POST /api/scan", s.scan)
	mux.HandleFunc("GET /api/items", s.getItems)
	mux.HandleFunc("POST /api/items/{id}", s.updateItem)
	mux.HandleFunc("POST /api/apply", s.apply)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("POST /api/undo", s.undo)
	mux.HandleFunc("GET /api/dirs", s.dirs)
	mux.Handle("GET /", http.FileServerFS(s.static))
	return guard(mux)
}

// guard lässt nur Anfragen von localhost zu und verlangt für Änderungen einen
// eigenen Header. So kann keine fremde Webseite im Browser Dateien verschieben.
func guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "nur lokal erreichbar", http.StatusForbidden)
			return
		}
		if r.Method != http.MethodGet && r.Header.Get("X-OrganiBear") != "1" {
			http.Error(w, "fehlender Header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
}

func readJSON(r *http.Request, v any) error {
	return json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20)).Decode(v)
}

func (s *Server) getConfig(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{"config": s.cfg, "path": s.cfgPath, "placeholders": Placeholders})
}

func (s *Server) putConfig(w http.ResponseWriter, r *http.Request) {
	var cfg Config
	if err := readJSON(r, &cfg); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := cfg.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	for _, t := range []string{cfg.MovieTemplate, cfg.SeriesTemplate} {
		if err := CheckTemplate(t); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := SaveConfig(s.cfgPath, cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if cfg.TMDBKey != s.cfg.TMDBKey || cfg.Language != s.cfg.Language {
		s.db = NewTMDB(cfg.TMDBKey, cfg.Language)
	}
	s.cfg = cfg
	PlanTargets(s.cfg, s.items)
	writeJSON(w, map[string]any{"config": s.cfg})
}

var sampleMovie = MediaInfo{Title: "Der Herr der Ringe: Die Gefährten", OriginalTitle: "The Lord of the Rings: The Fellowship of the Ring", Year: 2001, Resolution: "1080p", TMDBID: 120}
var sampleEpisode = MediaInfo{Title: "Breaking Bad", Year: 2008, Series: true, Season: 1, Episode: 3, EpisodeTitle: "...und der Leichensack", Resolution: "720p", TMDBID: 1396}

func (s *Server) templatePreview(w http.ResponseWriter, r *http.Request) {
	var req struct {
		MovieTemplate  string `json:"movie_template"`
		SeriesTemplate string `json:"series_template"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res := map[string]string{}
	for key, v := range map[string]struct {
		tmpl string
		info MediaInfo
	}{"movie": {req.MovieTemplate, sampleMovie}, "series": {req.SeriesTemplate, sampleEpisode}} {
		if err := CheckTemplate(v.tmpl); err != nil {
			res[key+"_error"] = err.Error()
		}
		res[key] = filepath.ToSlash(RenderTemplate(v.tmpl, v.info)) + ".mkv"
	}
	writeJSON(w, res)
}

func (s *Server) scan(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SourceDir string `json:"source_dir"`
		TargetDir string `json:"target_dir"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	cfg := s.cfg
	if req.SourceDir != "" {
		cfg.SourceDir = req.SourceDir
	}
	if req.TargetDir != "" {
		cfg.TargetDir = req.TargetDir
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	items, err := Scan(ctx, cfg, s.db)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// Ordner merken
	if cfg.SourceDir != s.cfg.SourceDir || cfg.TargetDir != s.cfg.TargetDir {
		s.cfg = cfg
		SaveConfig(s.cfgPath, cfg)
	}
	s.items = items
	writeJSON(w, map[string]any{"items": s.items, "online": s.db.Enabled()})
}

func (s *Server) getItems(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, map[string]any{"items": s.items, "online": s.db.Enabled()})
}

func (s *Server) updateItem(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(r.PathValue("id"))
	var req struct {
		CandidateID int        `json:"candidate_id"`
		Query       string     `json:"query"`
		Year        int        `json:"year"`
		Series      *bool      `json:"series"`
		Info        *MediaInfo `json:"info"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var it *Item
	for _, x := range s.items {
		if x.ID == id {
			it = x
		}
	}
	if it == nil || it.Status == StatusDone {
		writeErr(w, http.StatusNotFound, errors.New("Eintrag nicht gefunden"))
		return
	}
	ctx := r.Context()
	switch {
	case req.Info != nil:
		it.Info = *req.Info
		it.Matched = true
		it.Message = ""
	case req.Query != "":
		if req.Series != nil {
			it.Parsed.Series = *req.Series
		}
		Lookup(ctx, s.db, it, req.Query, req.Year)
	case req.CandidateID != 0:
		for _, c := range it.Candidates {
			if c.ID == req.CandidateID {
				ApplyCandidate(ctx, s.db, it, c)
			}
		}
	}
	PlanTargets(s.cfg, s.items)
	writeJSON(w, map[string]any{"items": s.items, "online": s.db.Enabled()})
}

func (s *Server) apply(w http.ResponseWriter, r *http.Request) {
	var req struct {
		IDs []int `json:"ids"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ids := map[int]bool{}
	for _, id := range req.IDs {
		ids[id] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	PlanTargets(s.cfg, s.items) // Ziele frisch prüfen, falls sich auf der Platte etwas getan hat
	j, path, err := Apply(s.cfg, s.items, ids, s.journalDir)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	PlanTargets(s.cfg, s.items)
	writeJSON(w, map[string]any{"items": s.items, "online": s.db.Enabled(), "ops": len(j.Ops), "journal": filepath.Base(path)})
}

type historyEntry struct {
	File    string     `json:"file"`
	Created time.Time  `json:"created"`
	Ops     int        `json:"ops"`
	Undone  *time.Time `json:"undone,omitempty"`
	Sample  []FileOp   `json:"sample"`
}

func (s *Server) history(w http.ResponseWriter, r *http.Request) {
	entries, _ := os.ReadDir(s.journalDir)
	out := []historyEntry{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		j, err := ReadJournal(filepath.Join(s.journalDir, e.Name()))
		if err != nil {
			continue
		}
		h := historyEntry{File: e.Name(), Created: j.Created, Ops: len(j.Ops), Undone: j.Undone}
		for i, op := range j.Ops {
			if i == 5 {
				break
			}
			h.Sample = append(h.Sample, op)
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	writeJSON(w, out)
}

func (s *Server) undo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		File string `json:"file"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	name := filepath.Base(req.File)
	if name != req.File || !strings.HasSuffix(name, ".json") {
		writeErr(w, http.StatusBadRequest, errors.New("ungültiger Verlaufseintrag"))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	problems, err := Undo(filepath.Join(s.journalDir, name))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.items = nil // der alte Plan passt nicht mehr
	writeJSON(w, map[string]any{"problems": problems})
}

func (s *Server) dirs(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Query().Get("path")
	if p == "" {
		p, _ = os.UserHomeDir()
	}
	p, _ = filepath.Abs(p)
	entries, err := os.ReadDir(p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	dirs := []string{}
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			dirs = append(dirs, e.Name())
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return strings.ToLower(dirs[i]) < strings.ToLower(dirs[j]) })
	parent := filepath.Dir(p)
	if parent == p {
		parent = ""
	}
	writeJSON(w, map[string]any{"path": p, "parent": parent, "dirs": dirs, "sep": string(filepath.Separator)})
}
