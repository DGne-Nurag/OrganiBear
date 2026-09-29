package main

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
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
	token      string // Zugangsschlüssel pro Programmstart
}

func NewServer(cfg Config, cfgPath string, static fs.FS) *Server {
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		panic(err)
	}
	return &Server{
		token:      hex.EncodeToString(tok),
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
	return s.guard(mux)
}

const cookieName = "organibear"

// LoginURL ist die Adresse, die der Browser beim Start öffnet. Der Schlüssel
// darin wird gegen ein Cookie getauscht.
func (s *Server) LoginURL(base string) string { return base + "/?t=" + s.token }

// guard schützt die Oberfläche:
//   - nur Host localhost/127.0.0.1/::1 (gegen DNS-Rebinding),
//   - Zugang nur mit dem Schlüssel aus dem Startlink (gegen andere Programme
//     und Benutzer auf demselben Rechner),
//   - Änderungen nur mit eigenem Header (gegen Formulare fremder Webseiten),
//   - keine Einbettung in fremde Seiten (gegen Clickjacking).
func (s *Server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' https://image.tmdb.org data:; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'none'")

		host := r.Host
		if hh, _, err := net.SplitHostPort(host); err == nil {
			host = hh
		}
		if host != "localhost" && host != "127.0.0.1" && host != "::1" {
			http.Error(w, "nur lokal erreichbar", http.StatusForbidden)
			return
		}

		if t := r.URL.Query().Get("t"); t != "" && r.URL.Path == "/" {
			if subtle.ConstantTimeCompare([]byte(t), []byte(s.token)) != 1 {
				denied(w)
				return
			}
			// #nosec G124 -- nur http://127.0.0.1, ein Secure-Cookie würde dort nicht überall gesetzt
			http.SetCookie(w, &http.Cookie{Name: cookieName, Value: s.token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			http.Redirect(w, r, "/", http.StatusSeeOther)
			return
		}
		c, err := r.Cookie(cookieName)
		if err != nil || subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.token)) != 1 {
			denied(w)
			return
		}

		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-OrganiBear") != "1" {
			http.Error(w, "fehlender Header", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func denied(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusForbidden)
	fmt.Fprint(w, `<!doctype html><html lang="de"><meta charset="utf-8"><title>OrganiBear</title>
<body style="font-family:system-ui,sans-serif;background:#fff7ea;color:#3b2616;display:grid;place-items:center;min-height:100vh;margin:0;text-align:center">
<main><p style="font-size:64px;margin:0" aria-hidden="true">ʕ•ᴥ•ʔ</p><h1>Bitte über den Startlink öffnen</h1>
<p>Aus Sicherheitsgründen lässt dich der Bär nur mit dem Link herein, den OrganiBear beim Start öffnet<br>bzw. im Programmfenster anzeigt. Nach einem Neustart gibt es einen neuen Link.</p></main>`)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v) // Client weg: nichts mehr zu tun
}

func writeErr(w http.ResponseWriter, code int, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
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
		if err := SaveConfig(s.cfgPath, cfg); err != nil {
			log.Printf("Ordner konnten nicht gespeichert werden: %v", err)
		}
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
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
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
