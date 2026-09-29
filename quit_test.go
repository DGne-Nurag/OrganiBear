package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

func TestQuitAndIdle(t *testing.T) {
	s := NewServer(DefaultConfig(), filepath.Join(t.TempDir(), "cfg.json"), fstest.MapFS{"index.html": {Data: []byte("hi")}})
	h := s.Handler()
	post := func(path string, auth bool) int {
		r := httptest.NewRequest("POST", path, strings.NewReader("{}"))
		r.Host = "127.0.0.1:8765"
		if auth {
			r.AddCookie(&http.Cookie{Name: cookieName, Value: s.token})
			r.Header.Set("X-OrganiBear", "1")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code
	}

	if d := s.IdleFor(time.Now().Add(time.Hour)); d != 0 {
		t.Errorf("ohne Browser darf es kein Auto-Ende geben, IdleFor = %v", d)
	}
	if code := post("/api/ping", true); code != http.StatusNoContent {
		t.Fatalf("ping: %d", code)
	}
	if d := s.IdleFor(time.Now().Add(10 * time.Minute)); d < 9*time.Minute {
		t.Errorf("nach dem Ping: IdleFor = %v", d)
	}
	s.inFlight.Add(1) // z. B. ein langes Einsortieren
	if d := s.IdleFor(time.Now().Add(time.Hour)); d != 0 {
		t.Errorf("während einer laufenden Anfrage: IdleFor = %v", d)
	}
	s.inFlight.Add(-1)

	if code := post("/api/quit", false); code != http.StatusForbidden {
		t.Errorf("Beenden ohne Schlüssel: %d", code)
	}
	select {
	case <-s.Done():
		t.Fatal("Beenden ohne Schlüssel hat trotzdem beendet")
	default:
	}
	if code := post("/api/quit", true); code != http.StatusNoContent {
		t.Fatalf("quit: %d", code)
	}
	select {
	case <-s.Done():
	case <-time.After(time.Second):
		t.Fatal("Beenden kam nicht an")
	}
	post("/api/quit", true) // zweites Beenden darf nicht abstürzen
}

func TestIdleTimeout(t *testing.T) {
	s := NewServer(DefaultConfig(), filepath.Join(t.TempDir(), "cfg.json"), fstest.MapFS{})
	s.lastSeen.Store(time.Now().Add(-time.Hour).UnixNano())
	select {
	case <-idleTimeout(s, 40*time.Millisecond):
	case <-time.After(2 * time.Second):
		t.Fatal("Auto-Ende hat nicht ausgelöst")
	}
	select {
	case <-idleTimeout(s, 0):
		t.Fatal("-idle 0 muss das Auto-Ende abschalten")
	case <-time.After(100 * time.Millisecond):
	}
}
