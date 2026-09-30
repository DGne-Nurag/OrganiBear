package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestConfigExportImport(t *testing.T) {
	cfgDir, stick := t.TempDir(), t.TempDir()
	cfg := DefaultConfig()
	cfg.TMDBKey = "abc"
	cfg.MovieTemplate = "{title} ({year})/{title}"
	s := NewServer(cfg, filepath.Join(cfgDir, "cfg.json"), fstest.MapFS{"index.html": {Data: []byte("hi")}})
	h := s.Handler()
	post := func(path, dir string) (int, map[string]any) {
		body, _ := json.Marshal(map[string]string{"dir": dir})
		r := httptest.NewRequest("POST", path, strings.NewReader(string(body)))
		r.Host = "127.0.0.1:8765"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: s.token})
		r.Header.Set("X-OrganiBear", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		var res map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		return w.Code, res
	}

	code, res := post("/api/config/export", stick)
	if code != http.StatusOK || res["path"] != filepath.Join(stick, "OrganiBear-Einstellungen.json") {
		t.Fatalf("Export: %d %v", code, res)
	}
	// Ein zweiter Export überschreibt nichts.
	if _, res := post("/api/config/export", stick); res["path"] != filepath.Join(stick, "OrganiBear-Einstellungen (2).json") {
		t.Errorf("zweiter Export: %v", res)
	}
	if code, _ := post("/api/config/export", filepath.Join(stick, "gibtsnicht")); code != http.StatusBadRequest {
		t.Errorf("Export in fehlenden Ordner: %d", code)
	}

	// Jemand ändert die Einstellungen, dann wird die Sicherung eingelesen.
	s.cfg.TMDBKey = "neu"
	s.cfg.MovieTemplate = "{title}"
	code, res = post("/api/config/import", stick)
	if code != http.StatusOK {
		t.Fatalf("Import: %d %v", code, res)
	}
	if s.cfg.TMDBKey != "abc" || s.cfg.MovieTemplate != cfg.MovieTemplate {
		t.Errorf("Import hat die Sicherung nicht übernommen: %+v", s.cfg)
	}
	saved, err := LoadConfig(filepath.Join(cfgDir, "cfg.json"))
	if err != nil || saved.TMDBKey != "abc" {
		t.Errorf("Import nicht gespeichert: %v %q", err, saved.TMDBKey)
	}
	backup, err := LoadConfig(filepath.Join(cfgDir, "organibear-vorher.json"))
	if err != nil || backup.TMDBKey != "neu" {
		t.Errorf("Sicherung der alten Einstellungen fehlt: %v %q", err, backup.TMDBKey)
	}

	// Ordner ohne Sicherung und fremde JSON-Dateien werden abgelehnt.
	empty := t.TempDir()
	if code, _ := post("/api/config/import", empty); code != http.StatusBadRequest {
		t.Errorf("Import aus leerem Ordner: %d", code)
	}
	if err := os.WriteFile(filepath.Join(empty, "OrganiBear-Einstellungen.json"), []byte(`{"hallo":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code, _ := post("/api/config/import", empty); code != http.StatusBadRequest || s.cfg.TMDBKey != "abc" {
		t.Errorf("fremde Datei eingelesen: %d %q", code, s.cfg.TMDBKey)
	}
}
