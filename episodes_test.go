package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestPickSeasonEpisode(t *testing.T) {
	dir := t.TempDir()
	cfg := DefaultConfig()
	cfg.TargetDir, cfg.TMDBKey = filepath.Join(dir, "out"), "testkey"
	s := NewServer(cfg, filepath.Join(dir, "cfg.json"), fstest.MapFS{"index.html": {Data: []byte("hi")}})
	s.db = fakeTMDB(t)
	s.items = []*Item{{ID: 1, Source: filepath.Join(dir, "in", "One.Piece.E9999.mkv"), Matched: true,
		Info: MediaInfo{Title: "One Piece", Year: 1999, Series: true, Episode: 9999, Absolute: 9999, TMDBID: 37854}}}
	PlanTargets(s.cfg, s.items)
	h := s.Handler()
	do := func(method, path, body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		r.Host = "127.0.0.1:8765"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: s.token})
		r.Header.Set("X-OrganiBear", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}

	w := do("GET", "/api/tv/37854/seasons", "")
	var seasons []Season
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &seasons) != nil || len(seasons) != 3 || seasons[1].Name != "Staffel 2" || seasons[1].Episodes != 16 {
		t.Fatalf("Staffeln: %d %s", w.Code, w.Body)
	}
	w = do("GET", "/api/tv/37854/season/2", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"name":"Laboon"`) {
		t.Fatalf("Folgen: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/tv/abc/seasons", ""); w.Code != http.StatusBadRequest {
		t.Errorf("ungültige ID: %d", w.Code)
	}

	if s.items[0].Status != StatusUnmatched {
		t.Fatalf("vorher: %s", s.items[0].Status)
	}
	w = do("POST", "/api/items/1", `{"pick":{"season":2,"episode":2}}`)
	it := s.items[0]
	if w.Code != http.StatusOK || it.Info.Season != 2 || it.Info.Episode != 2 || it.Info.EpisodeTitle != "Laboon" || it.Info.TMDBID != 37854 || it.Status != StatusReady {
		t.Errorf("Auswahl: %d %+v %s", w.Code, it.Info, it.Status)
	}
	if w := do("POST", "/api/items/1", `{"pick":{"season":1,"episode":0}}`); w.Code != http.StatusBadRequest {
		t.Errorf("Folge 0: %d", w.Code)
	}
}
