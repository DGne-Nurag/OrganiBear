package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestGuard(t *testing.T) {
	dir := t.TempDir()
	s := NewServer(DefaultConfig(), filepath.Join(dir, "cfg.json"), fstest.MapFS{"index.html": {Data: []byte("hi")}})
	h := s.Handler()
	do := func(method, target, host string, cookie bool, header bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, target, strings.NewReader("{}"))
		r.Host = host
		if cookie {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: s.token})
		}
		if header {
			r.Header.Set("X-OrganiBear", "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	if w := do("GET", "/api/config", "127.0.0.1:8765", false, false); w.Code != http.StatusForbidden {
		t.Errorf("ohne Schlüssel: %d", w.Code)
	}
	if w := do("GET", "/?t=falsch", "127.0.0.1:8765", false, false); w.Code != http.StatusForbidden {
		t.Errorf("falscher Schlüssel: %d", w.Code)
	}
	w := do("GET", "/?t="+s.token, "127.0.0.1:8765", false, false)
	if w.Code != http.StatusSeeOther || !strings.Contains(w.Header().Get("Set-Cookie"), "HttpOnly") {
		t.Errorf("Login: %d %q", w.Code, w.Header().Get("Set-Cookie"))
	}
	if w := do("GET", "/api/config", "evil.example:8765", true, false); w.Code != http.StatusForbidden {
		t.Errorf("fremder Host: %d", w.Code)
	}
	if w := do("POST", "/api/undo", "127.0.0.1:8765", true, false); w.Code != http.StatusForbidden {
		t.Errorf("POST ohne Header: %d", w.Code)
	}
	w = do("GET", "/api/config", "localhost:8765", true, false)
	if w.Code != http.StatusOK {
		t.Errorf("gültige Anfrage: %d", w.Code)
	}
	if w.Header().Get("X-Frame-Options") != "DENY" || !strings.Contains(w.Header().Get("Content-Security-Policy"), "frame-ancestors 'none'") {
		t.Error("Sicherheits-Header fehlen")
	}
}

func TestRequireLoopback(t *testing.T) {
	for addr, ok := range map[string]bool{"127.0.0.1:1": true, "[::1]:1": true, "localhost:1": true, "0.0.0.0:1": false, "192.168.1.5:1": false, ":1": false} {
		if got := requireLoopback(addr) == nil; got != ok {
			t.Errorf("%s: erlaubt=%v, erwartet %v", addr, got, ok)
		}
	}
}

func TestMoveNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	src, dst := filepath.Join(dir, "a.mkv"), filepath.Join(dir, "b.mkv")
	os.WriteFile(src, []byte("neu"), 0o644)
	os.WriteFile(dst, []byte("alt"), 0o644)
	if err := moveFile(src, dst); err != errExists {
		t.Fatalf("erwartet errExists, bekommen %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "alt" {
		t.Error("Ziel wurde überschrieben")
	}
}

func TestSymlinkedSourceAndTarget(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Symlinks brauchen unter Windows Sonderrechte")
	}
	dir := t.TempDir()
	secret := filepath.Join(dir, "geheim.txt")
	os.WriteFile(secret, []byte("x"), 0o600)
	link := filepath.Join(dir, "film.mkv")
	os.Symlink(secret, link)
	if err := copyFile(link, filepath.Join(dir, "kopie.mkv")); err == nil {
		t.Error("Symlink als Quelle wurde kopiert")
	}

	root := filepath.Join(dir, "medien")
	outside := filepath.Join(dir, "woanders")
	os.MkdirAll(root, 0o755)
	os.MkdirAll(outside, 0o755)
	os.Symlink(outside, filepath.Join(root, "Filme"))
	if err := insideReal(root, filepath.Join(root, "Filme", "X", "x.mkv")); err == nil {
		t.Error("Ziel über Symlink außerhalb wurde akzeptiert")
	}
	if err := insideReal(root, filepath.Join(root, "Serien", "x.mkv")); err != nil {
		t.Errorf("normales Ziel abgelehnt: %v", err)
	}
	pruneEmpty(root, filepath.Join(root, "Filme"))
	if _, err := os.Lstat(filepath.Join(root, "Filme")); err != nil {
		t.Error("pruneEmpty hat einen Symlink gelöscht")
	}
}

func TestUndoRejectsTamperedJournal(t *testing.T) {
	dir := t.TempDir()
	victim := filepath.Join(dir, "wichtig.txt")
	os.WriteFile(victim, []byte("x"), 0o644)
	j := &Journal{
		Created:   time.Now(),
		SourceDir: filepath.Join(dir, "in"),
		TargetDir: filepath.Join(dir, "out"),
		Ops:       []FileOp{{Source: filepath.Join(dir, "in", "a.mkv"), Target: victim, Action: ActionCopy}},
	}
	path := filepath.Join(dir, "j.json")
	if err := writeJournal(path, j); err != nil {
		t.Fatal(err)
	}
	if _, err := Undo(path); err == nil {
		t.Error("manipuliertes Journal wurde akzeptiert")
	}
	if !exists(victim) {
		t.Error("Datei außerhalb des Zielordners wurde gelöscht")
	}
}
