package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeTMDB beantwortet die paar Endpunkte, die OrganiBear braucht.
func fakeTMDB(t *testing.T) *TMDB {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("api_key") != "testkey" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := strings.ToLower(r.URL.Query().Get("query"))
		var out any
		switch {
		case r.URL.Path == "/search/movie" && strings.Contains(q, "matrix"):
			out = map[string]any{"results": []map[string]any{{"id": 603, "title": "Matrix", "original_title": "The Matrix", "release_date": "1999-03-30"}}}
		case r.URL.Path == "/search/tv" && strings.Contains(q, "breaking"):
			out = map[string]any{"results": []map[string]any{{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20"}}}
		case r.URL.Path == "/tv/1396/season/1/episode/3":
			out = map[string]any{"name": "...und der Leichensack"}
		case strings.HasPrefix(r.URL.Path, "/search/"):
			out = map[string]any{"results": []any{}}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	db := NewTMDB("testkey", "de-DE")
	db.BaseURL = srv.URL
	return db
}

func touch(t *testing.T, root string, files ...string) {
	for _, f := range files {
		p := filepath.Join(root, filepath.FromSlash(f))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(f), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func itemFor(items []*Item, rel string) *Item {
	for _, it := range items {
		if filepath.ToSlash(it.RelSource) == rel {
			return it
		}
	}
	return nil
}

func TestScanApplyUndo(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "downloads"), filepath.Join(tmp, "medien")
	touch(t, src,
		"The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.mkv",
		"The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.nfo",
		"The.Matrix.1999.1080p.BluRay.x264-GRP/Subs/English.srt",
		"The.Matrix.1999.1080p.BluRay.x264-GRP/sample-matrix.mkv",
		"Breaking.Bad.S01E03.720p.mkv",
		"Breaking.Bad.S01E03.720p.de.srt",
		"Unbekannter.Film.2003.mkv",
		"notizen.txt",
	)
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey = src, dst, "testkey"
	// Untertitel kopieren statt verschieben
	for i := range cfg.FileRules {
		if cfg.FileRules[i].Name == "Untertitel" {
			cfg.FileRules[i].Action = ActionCopy
		}
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}

	items, err := Scan(context.Background(), cfg, fakeTMDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 3 {
		t.Fatalf("erwartet 3 Videos (Sample ignoriert), bekommen %d", len(items))
	}

	matrix := itemFor(items, "The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.mkv")
	if matrix == nil || matrix.Status != StatusReady {
		t.Fatalf("Matrix nicht bereit: %+v", matrix)
	}
	if want := filepath.FromSlash("Filme/Matrix (1999)/Matrix (1999).mkv"); matrix.RelTarget != want {
		t.Errorf("Matrix-Ziel %q, erwartet %q", matrix.RelTarget, want)
	}
	if len(matrix.Companions) != 1 || !strings.HasSuffix(matrix.Companions[0].Target, "Matrix (1999).English.srt") {
		t.Errorf("Matrix-Begleitdateien falsch (NFO ist ignoriert, Subs/English.srt gehört dazu): %+v", matrix.Companions)
	}

	bb := itemFor(items, "Breaking.Bad.S01E03.720p.mkv")
	if want := filepath.FromSlash("Serien/Breaking Bad (2008)/Staffel 01/Breaking Bad - S01E03 - ...und der Leichensack.mkv"); bb.RelTarget != want {
		t.Errorf("Serien-Ziel %q, erwartet %q", bb.RelTarget, want)
	}
	if len(bb.Companions) != 1 || !strings.HasSuffix(bb.Companions[0].Target, "Leichensack.de.srt") {
		t.Errorf("Serien-Untertitel falsch: %+v", bb.Companions)
	}

	unknown := itemFor(items, "Unbekannter.Film.2003.mkv")
	if unknown.Status != StatusUnmatched {
		t.Errorf("unbekannter Film sollte geprüft werden müssen, ist %q", unknown.Status)
	}

	// Ein bereits existierendes Ziel wird nie überschrieben.
	touch(t, dst, "Filme/Unbekannter Film (2003)/Unbekannter Film (2003).mkv")
	PlanTargets(cfg, items)
	if unknown.Status != StatusConflict {
		t.Errorf("Konflikt nicht erkannt, Status %q", unknown.Status)
	}

	journalDir := filepath.Join(tmp, "verlauf")
	j, jpath, err := Apply(cfg, items, map[int]bool{matrix.ID: true, bb.ID: true, unknown.ID: true}, journalDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Ops) != 4 {
		t.Errorf("erwartet 4 Operationen, bekommen %d: %+v", len(j.Ops), j.Ops)
	}
	if unknown.Status != StatusConflict {
		t.Errorf("Konflikt-Eintrag wurde ausgeführt")
	}
	mustExist(t, filepath.Join(dst, "Filme/Matrix (1999)/Matrix (1999).mkv"), true)
	mustExist(t, filepath.Join(dst, "Filme/Matrix (1999)/Matrix (1999).English.srt"), true)
	mustExist(t, matrix.Source, false)                                        // verschoben
	mustExist(t, matrix.Companions[0].Source, true)                           // kopiert
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p.de.srt"), true) // kopiert
	mustExist(t, filepath.Join(src, "The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.nfo"), true)

	problems, err := Undo(jpath)
	if err != nil || len(problems) > 0 {
		t.Fatalf("Undo: %v %v", err, problems)
	}
	mustExist(t, matrix.Source, true)
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p.mkv"), true)
	mustExist(t, filepath.Join(dst, "Filme/Matrix (1999)"), false)
	mustExist(t, filepath.Join(dst, "Serien"), false)
	mustExist(t, filepath.Join(dst, "Filme/Unbekannter Film (2003)/Unbekannter Film (2003).mkv"), true)
	if _, err := Undo(jpath); err == nil {
		t.Error("zweites Undo sollte abgelehnt werden")
	}
}

func TestOfflineAndDuplicates(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "in"), filepath.Join(tmp, "out")
	touch(t, src, "Amelie.2001.mkv", "Amelie (2001).avi", "a/Amelie.2001.720p.mkv")
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir = src, dst
	items, err := Scan(context.Background(), cfg, NewTMDB("", "de-DE"))
	if err != nil {
		t.Fatal(err)
	}
	var dups []*Item
	for _, it := range items {
		switch it.Status {
		case StatusOffline:
		case StatusDuplicate:
			dups = append(dups, it)
		default:
			t.Errorf("%s: unerwarteter Status %q", it.RelSource, it.Status)
		}
	}
	// Die beiden .mkv landen auf demselben Ziel, die .avi nicht.
	if len(dups) != 2 {
		t.Fatalf("erwartet 2 doppelte, bekommen %d", len(dups))
	}
	if !strings.Contains(dups[0].Message, dups[1].RelSource) {
		t.Errorf("Meldung nennt die andere Datei nicht: %q", dups[0].Message)
	}

	// Sind beide gewählt, wird nur die erste einsortiert, die zweite bleibt liegen.
	j, _, err := Apply(cfg, items, map[int]bool{dups[0].ID: true, dups[1].ID: true}, filepath.Join(tmp, "verlauf"))
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Ops) != 1 || dups[0].Status != StatusDone || !exists(dups[1].Source) {
		t.Errorf("doppelte falsch behandelt: ops=%d, erste=%q", len(j.Ops), dups[0].Status)
	}
	PlanTargets(cfg, items)
	if dups[1].Status != StatusConflict {
		t.Errorf("zweite sollte jetzt Konflikt sein, ist %q", dups[1].Status)
	}
}

func mustExist(t *testing.T, p string, want bool) {
	t.Helper()
	if exists(filepath.FromSlash(p)) != want {
		t.Errorf("%s: existiert=%v, erwartet %v", p, !want, want)
	}
}
