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
	"sync/atomic"
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

	quit     chan struct{} // wird geschlossen, wenn das Programm enden soll
	quitOnce sync.Once
	lastSeen atomic.Int64 // letzte Anfrage des Browsers (UnixNano), 0 = noch nie
	inFlight atomic.Int64 // laufende Anfragen, z. B. ein langes Einsortieren
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
		quit:       make(chan struct{}),
	}
}

// Quit bittet das Programm, sich zu beenden. Mehrfaches Aufrufen schadet nicht.
func (s *Server) Quit() { s.quitOnce.Do(func() { close(s.quit) }) }

// Done wird geschlossen, sobald jemand Quit aufgerufen hat.
func (s *Server) Done() <-chan struct{} { return s.quit }

// IdleFor meldet, wie lange der Browser schon nichts mehr von sich hören ließ.
// Solange noch nie ein Browser da war oder eine Anfrage läuft, ist das 0.
func (s *Server) IdleFor(now time.Time) time.Duration {
	last := s.lastSeen.Load()
	if last == 0 || s.inFlight.Load() > 0 {
		return 0
	}
	return now.Sub(time.Unix(0, last))
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config", s.getConfig)
	mux.HandleFunc("PUT /api/config", s.putConfig)
	mux.HandleFunc("POST /api/template-preview", s.templatePreview)
	mux.HandleFunc("POST /api/scan", s.scan)
	mux.HandleFunc("GET /api/items", s.getItems)
	mux.HandleFunc("POST /api/items/{id}", s.updateItem)
	mux.HandleFunc("GET /api/tv/{id}/seasons", s.tvSeasons)
	mux.HandleFunc("GET /api/tv/{id}/season/{season}", s.tvEpisodes)
	mux.HandleFunc("POST /api/apply", s.apply)
	mux.HandleFunc("GET /api/history", s.history)
	mux.HandleFunc("POST /api/undo", s.undo)
	mux.HandleFunc("GET /api/dirs", s.dirs)
	mux.HandleFunc("POST /api/ping", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) })
	mux.HandleFunc("POST /api/quit", s.quitHandler)
	mux.HandleFunc("POST /api/mediaserver/test", s.testMediaServer)
	mux.HandleFunc("POST /api/subtitles/test", s.testSubtitles)
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
		s.inFlight.Add(1)
		s.lastSeen.Store(time.Now().UnixNano())
		defer func() {
			s.lastSeen.Store(time.Now().UnixNano())
			s.inFlight.Add(-1)
		}()
		next.ServeHTTP(w, r)
	})
}

// quitHandler beendet das Programm über den Knopf im Webinterface. Ein laufendes
// Einsortieren wird vorher noch fertig (siehe main).
func (s *Server) quitHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
	s.Quit()
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
	writeJSON(w, map[string]any{"config": s.cfg, "path": s.cfgPath, "placeholders": Placeholders,
		"subtitles_available": openSubtitlesKey != ""})
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

var sampleMovie = MediaInfo{Title: "Der Herr der Ringe: Die Gefährten", OriginalTitle: "The Lord of the Rings: The Fellowship of the Ring", Year: 2001, Resolution: "2160p", VCodec: "HEVC", HDR: "HDR10", Audio: "TrueHD 7.1", Languages: "DE-EN", TMDBID: 120}
var sampleEpisode = MediaInfo{Title: "Breaking Bad", Year: 2008, Series: true, Season: 1, Episode: 3, EpisodeTitle: "...und der Leichensack", Resolution: "1080p", VCodec: "H.264", Audio: "EAC3 5.1", Languages: "DE-EN", TMDBID: 1396}

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
		// Staffel und Folge aus der TMDB-Auswahl, der Folgentitel kommt von TMDB.
		Pick *struct {
			Season  int `json:"season"`
			Episode int `json:"episode"`
		} `json:"pick"`
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
	case req.Pick != nil:
		if !it.Info.Series || req.Pick.Season < 0 || req.Pick.Episode < 1 {
			writeErr(w, http.StatusBadRequest, errors.New("ungültige Staffel oder Folge"))
			return
		}
		it.Info.Season, it.Info.Episode, it.Info.EpisodeEnd, it.Info.EpisodeTitle = req.Pick.Season, req.Pick.Episode, 0, ""
		it.Message = ""
		if it.Info.TMDBID > 0 && s.db.Enabled() {
			if name, err := s.db.EpisodeTitle(ctx, it.Info.TMDBID, req.Pick.Season, req.Pick.Episode); err == nil {
				it.Info.EpisodeTitle = name
			}
		}
	case req.Info != nil:
		it.Info = *req.Info
		applyMedia(&it.Info, it.Media)
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
	cfg, db := s.cfg, s.db
	subs := NewOpenSubs(cfg.Subtitles)
	extras := func(it *Item) ([]string, []string) {
		created, warns := WriteExtras(r.Context(), cfg, db, it)
		c2, w2 := FetchSubtitles(r.Context(), cfg, subs, it)
		return append(created, c2...), append(warns, w2...)
	}
	j, path, err := Apply(cfg, s.items, ids, s.journalDir, extras)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var refreshed []string
	if len(j.Ops) > 0 {
		refreshed = RefreshLibraries(r.Context(), cfg.Extras)
	}
	// Erledigtes verschwindet aus der Liste, der Rest wird neu geprüft.
	rest := s.items[:0]
	done := 0
	for _, it := range s.items {
		if it.Status == StatusDone {
			done++
			continue
		}
		rest = append(rest, it)
	}
	s.items = rest
	PlanTargets(s.cfg, s.items)
	writeJSON(w, map[string]any{"items": s.items, "online": s.db.Enabled(), "ops": len(j.Ops), "done": done, "journal": filepath.Base(path),
		"warnings": j.Warnings, "refreshed": refreshed})
}

// testSubtitles prüft die Verbindung und ggf. das Konto bei OpenSubtitles (auch ungespeichert).
func (s *Server) testSubtitles(w http.ResponseWriter, r *http.Request) {
	var sub Subtitles
	if err := readJSON(r, &sub); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sub.User = strings.TrimSpace(sub.User)
	if openSubtitlesKey == "" {
		writeErr(w, http.StatusBadRequest, errUnavailableSubs)
		return
	}
	if err := NewOpenSubs(sub).Check(r.Context()); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	msg := "OpenSubtitles ist erreichbar. Ohne Konto sind 5 Downloads pro Tag möglich."
	if sub.User != "" {
		msg = "Anmeldung passt."
	}
	writeJSON(w, map[string]string{"message": msg})
}

// testMediaServer probiert die Zugangsdaten aus dem Formular aus (auch
// ungespeicherte), indem es den Server die Bibliothek neu einlesen lässt.
func (s *Server) testMediaServer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind   string      `json:"kind"`
		Server MediaServer `json:"server"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if _, ok := serverNames[req.Kind]; !ok {
		writeErr(w, http.StatusBadRequest, errors.New("unbekannter Mediaserver"))
		return
	}
	if err := refreshServer(r.Context(), req.Kind, req.Server); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Errorf("%s: %w", serverNames[req.Kind], err))
		return
	}
	writeJSON(w, map[string]string{"message": serverNames[req.Kind] + " ist erreichbar und liest die Bibliothek neu ein."})
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

// tvSeasons liefert die Staffeln einer Serie für die Auswahl im Anpassen-Feld.
func (s *Server) tvSeasons(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("ungültige Serien-ID"))
		return
	}
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if !db.Enabled() {
		writeErr(w, http.StatusBadRequest, errors.New("dafür braucht der Bär einen TMDB-Key"))
		return
	}
	seasons, err := db.Seasons(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, seasons)
}

// tvEpisodes liefert die Folgen einer Staffel samt Titel.
func (s *Server) tvEpisodes(w http.ResponseWriter, r *http.Request) {
	id, err1 := strconv.Atoi(r.PathValue("id"))
	season, err2 := strconv.Atoi(r.PathValue("season"))
	if err1 != nil || err2 != nil || id <= 0 || season < 0 {
		writeErr(w, http.StatusBadRequest, errors.New("ungültige Staffel"))
		return
	}
	s.mu.Lock()
	db := s.db
	s.mu.Unlock()
	if !db.Enabled() {
		writeErr(w, http.StatusBadRequest, errors.New("dafür braucht der Bär einen TMDB-Key"))
		return
	}
	eps, err := db.SeasonEpisodes(r.Context(), id, season)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, eps)
}
