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

// Das Programmfenster schickt seine Anfragen ohne Cookie und mit eigener
// Adresse (wails://wails, wails.localhost). WindowHandler lässt sie durch,
// der normale Handler weiterhin nicht.
func TestWindowHandler(t *testing.T) {
	s := NewServer(DefaultConfig(), filepath.Join(t.TempDir(), "cfg.json"), fstest.MapFS{"index.html": {Data: []byte("hi")}})
	for _, host := range []string{"wails", "wails.localhost"} {
		r := httptest.NewRequest("GET", "/api/config", nil)
		r.Host = host
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s ohne Fenster: %d", host, w.Code)
		}
		w = httptest.NewRecorder()
		s.WindowHandler().ServeHTTP(w, r)
		if w.Code != http.StatusOK {
			t.Errorf("%s im Fenster: %d", host, w.Code)
		}
		// Änderungen brauchen auch im Fenster den eigenen Header.
		r = httptest.NewRequest("POST", "/api/ping", strings.NewReader("{}"))
		r.Host = host
		w = httptest.NewRecorder()
		s.WindowHandler().ServeHTTP(w, r)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s POST ohne Header: %d", host, w.Code)
		}
	}
}

// Beim Drag & Drop kann eine Datei statt eines Ordners ankommen.
func TestDirsWithFile(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "film.mkv")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(DefaultConfig(), filepath.Join(t.TempDir(), "cfg.json"), fstest.MapFS{})
	r := httptest.NewRequest("GET", "/api/dirs?path="+file, nil)
	w := httptest.NewRecorder()
	s.WindowHandler().ServeHTTP(w, r)
	var got struct{ Path string }
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil || got.Path != dir {
		t.Fatalf("Datei statt Ordner: %d %q %v", w.Code, got.Path, err)
	}
}
