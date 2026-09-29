package main

import (
	"context"
	"encoding/json"
	"fmt"
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
		if strings.HasPrefix(r.URL.Path, "/img/") {
			w.Write(fakeJPEG) // Bilder brauchen keinen Key
			return
		}
		if r.URL.Query().Get("api_key") != "testkey" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := strings.ToLower(r.URL.Query().Get("query"))
		var out any
		switch {
		case r.URL.Path == "/search/movie" && strings.Contains(q, "matrix"):
			out = map[string]any{"results": []map[string]any{{"id": 603, "title": "Matrix", "original_title": "The Matrix", "release_date": "1999-03-30"}}}
		case r.URL.Path == "/search/tv" && strings.Contains(q, "one piece"):
			out = map[string]any{"results": []map[string]any{{"id": 37854, "name": "One Piece", "first_air_date": "1999-10-20"}}}
		case r.URL.Path == "/tv/37854":
			// Specials zählen nicht, Staffel 1 hat 61 Folgen, Staffel 2 hat 16.
			out = map[string]any{"id": 37854, "name": "One Piece", "seasons": []map[string]any{
				{"season_number": 0, "name": "Specials", "episode_count": 40},
				{"season_number": 2, "name": "Staffel 2", "episode_count": 16},
				{"season_number": 1, "name": "Staffel 1", "episode_count": 61}}}
		case r.URL.Path == "/tv/37854/season/2":
			out = map[string]any{"episodes": []map[string]any{{"episode_number": 1, "name": "Abenteuer in Grand Line"}, {"episode_number": 2, "name": "Laboon"}}}
		case r.URL.Path == "/tv/37854/season/2/episode/2":
			out = map[string]any{"name": "Laboon"}
		case r.URL.Path == "/search/tv" && strings.Contains(q, "breaking"):
			out = map[string]any{"results": []map[string]any{{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20"}}}
		case r.URL.Path == "/tv/1396/season/1/episode/3":
			out = map[string]any{"id": 62087, "name": "...und der Leichensack", "overview": "Walt & Jesse <räumen> auf.", "air_date": "2008-02-10", "still_path": "/still.jpg"}
		case r.URL.Path == "/movie/603":
			out = map[string]any{"id": 603, "title": "Matrix", "overview": "Neo erwacht.", "release_date": "1999-03-30", "runtime": 136,
				"genres": []map[string]any{{"name": "Action"}, {"name": "Science Fiction"}}, "poster_path": "/poster.jpg", "backdrop_path": "/backdrop.jpg",
				"external_ids": map[string]any{"imdb_id": "tt0133093"}}
		case r.URL.Path == "/tv/1396":
			out = map[string]any{"id": 1396, "name": "Breaking Bad", "overview": "Ein Lehrer.", "first_air_date": "2008-01-20", "poster_path": "/showposter.jpg",
				"external_ids": map[string]any{"imdb_id": "tt0903747", "tvdb_id": 81189}}
		case r.URL.Path == "/tv/1396/season/1":
			out = map[string]any{"poster_path": "/season1.png"}
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
	db.ImageURL = srv.URL + "/img"
	return db
}

// fakeJPEG ist der Anfang einer JPEG-Datei, genug für die Formatprüfung.
var fakeJPEG = []byte("\xff\xd8\xff\xe0\x00\x10JFIF\x00")

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
	j, jpath, err := Apply(cfg, items, map[int]bool{matrix.ID: true, bb.ID: true, unknown.ID: true}, journalDir, nil)
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
	j, _, err := Apply(cfg, items, map[int]bool{dups[0].ID: true, dups[1].ID: true}, filepath.Join(tmp, "verlauf"), nil)
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

func TestDuplicateSuggestsBetter(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "in"), filepath.Join(tmp, "out")
	copyFile := func(from, to string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join("testdata", "media", from))
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(src, filepath.FromSlash(to))
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Der Name behauptet 1080p, die Datei ist aber 720p AV1. Die andere ist 2160p HDR10 HEVC.
	copyFile("av1.mkv", "Inception.2010.1080p.mkv")
	copyFile("hevc-hdr10.mkv", "neu/Inception (2010).mkv")
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir = src, dst
	cfg.MovieTemplate = "{title} ({year})"
	items, err := Scan(context.Background(), cfg, NewTMDB("", "de-DE"))
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	small, big := items[0], items[1]
	if small.Info.Resolution != "720p" || big.Info.Resolution != "2160p" || big.Info.VCodec != "HEVC" || big.Info.Audio != "EAC3 5.1" {
		t.Errorf("Infos aus der Datei: %+v / %+v", small.Info, big.Info)
	}
	if big.Status != StatusDuplicate || big.Better != "2160p HDR10 statt 720p SDR" || small.Better != "" {
		t.Errorf("Vorschlag: %q %q (%s)", big.Better, small.Better, big.Status)
	}
	if !strings.Contains(small.Message, "„neu/Inception (2010).mkv“") {
		t.Errorf("Meldung der schlechteren: %q", small.Message)
	}

	// Mit den neuen Platzhaltern landen beide auf verschiedenen Zielen.
	cfg.MovieTemplate = "{title} ({year}) [{resolution} {hdr} {vcodec} {audio} {languages}]"
	PlanTargets(cfg, items)
	if small.RelTarget != "Inception (2010) [720p AV1 Opus 2.0].mkv" || big.RelTarget != "Inception (2010) [2160p HDR10 HEVC EAC3 5.1 DE-EN].mkv" {
		t.Errorf("Ziele: %q / %q", small.RelTarget, big.RelTarget)
	}
}

func TestBetterReason(t *testing.T) {
	a := &Item{Info: MediaInfo{Resolution: "720p"}, Size: 3 << 30}
	b := &Item{Size: 1 << 30}
	if got := betterReason(a, b); got != "720p statt unbekannter Auflösung" {
		t.Error(got)
	}
	b.Info.Resolution = "720p"
	if best, why := suggestBest([]*Item{b, a}); best != a || why != "größere Datei (3,0 GB statt 1,0 GB)" {
		t.Errorf("%v %q", best == a, why)
	}
	b.Size = a.Size - 1
	if best, _ := suggestBest([]*Item{b, a}); best != nil {
		t.Error("fast gleich große Dateien sollten keinen Vorschlag bekommen")
	}
}

func TestMultiPartMovie(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "in"), filepath.Join(tmp, "out")
	touch(t, src, "Titanic.1997.CD1.avi", "Titanic.1997.CD2.avi", "Titanic.1997.CD1.srt", "Titanic.1997.CD2.srt")
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir = src, dst
	items, err := Scan(context.Background(), cfg, NewTMDB("", "de-DE"))
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	for i, it := range items {
		n := fmt.Sprint(i + 1)
		want := filepath.FromSlash("Filme/Titanic (1997)/Titanic (1997) - part" + n + ".avi")
		if it.Status != StatusOffline || it.RelTarget != want {
			t.Errorf("Teil %s: %q %q (%s)", n, it.Status, it.RelTarget, it.Message)
		}
		if len(it.Companions) != 1 || !strings.HasSuffix(it.Companions[0].Target, "Titanic (1997) - part"+n+".srt") {
			t.Errorf("Untertitel zu Teil %s: %+v", n, it.Companions)
		}
	}
}

func TestAbsoluteEpisodes(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "in"), filepath.Join(tmp, "out")
	touch(t, src, "One.Piece.E0063.1080p.mkv", "One.Piece.E0200.1080p.mkv")
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey = src, dst, "testkey"
	items, err := Scan(context.Background(), cfg, fakeTMDB(t))
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	ok, far := items[0], items[1]
	// 63 = 61 Folgen aus Staffel 1, dann Staffel 2 Folge 2.
	if ok.Info.Season != 2 || ok.Info.Episode != 2 || ok.Info.EpisodeTitle != "Laboon" || ok.Status != StatusReady || !strings.Contains(ok.Message, "fortlaufend") {
		t.Errorf("E0063: %+v %s %q", ok.Info, ok.Status, ok.Message)
	}
	if want := filepath.FromSlash("Serien/One Piece (1999)/Staffel 02/One Piece - S02E02 - Laboon.mkv"); ok.RelTarget != want {
		t.Errorf("Ziel %q", ok.RelTarget)
	}
	// 200 gibt es laut TMDB (noch) nicht: bitte selbst wählen.
	if far.Status != StatusUnmatched || far.Info.Season != 0 || !strings.Contains(far.Message, "Staffel und Folge wählen") {
		t.Errorf("E0200: %+v %s %q", far.Info, far.Status, far.Message)
	}
}

func TestAbsoluteEpisodeMath(t *testing.T) {
	seasons := []Season{{Number: 1, Episodes: 10}, {Number: 0, Episodes: 5}, {Number: 2, Episodes: 0}, {Number: 3, Episodes: 4}}
	for abs, want := range map[int][2]int{1: {1, 1}, 10: {1, 10}, 11: {3, 1}, 14: {3, 4}, 15: {0, 0}, 0: {0, 0}} {
		s, e, _ := absoluteEpisode(seasons, abs)
		if s != want[0] || e != want[1] {
			t.Errorf("%d: S%d E%d", abs, s, e)
		}
	}
}

func TestInPlace(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "filme")
	touch(t, src,
		"The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.mkv",
		"The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.nfo",
		"The.Matrix.1999.1080p.BluRay.x264-GRP/Subs/English.srt",
		"Breaking.Bad.S01E03.720p-GRP/Breaking.Bad.S01E03.720p.mkv",
		"Breaking.Bad.S01E03.720p-GRP/Breaking.Bad.S01E03.720p.de.srt",
	)
	cfg := DefaultConfig()
	// Das gemerkte Ziel wird bei „Nur umbenennen“ nicht benutzt.
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey, cfg.InPlace = src, filepath.Join(tmp, "anderswo"), "testkey", true
	for i := range cfg.FileRules {
		if cfg.FileRules[i].Name == "Untertitel" {
			cfg.FileRules[i].Action = ActionCopy
		}
	}
	db := fakeTMDB(t)
	items, err := Scan(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[int]bool{}
	for _, it := range items {
		if it.Status != StatusReady {
			t.Fatalf("%s: %s %s", it.RelSource, it.Status, it.Message)
		}
		if it.Action != ActionMove || it.Companions[0].Action != ActionMove {
			t.Errorf("%s: beim Umbenennen wird nichts kopiert: %s / %s", it.RelSource, it.Action, it.Companions[0].Action)
		}
		ids[it.ID] = true
	}
	_, jpath, err := Apply(cfg, items, ids, filepath.Join(tmp, "verlauf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(src, "Filme/Matrix (1999)/Matrix (1999).mkv"), true)
	mustExist(t, filepath.Join(src, "Filme/Matrix (1999)/Matrix (1999).English.srt"), true)
	mustExist(t, filepath.Join(src, "Serien/Breaking Bad (2008)/Staffel 01/Breaking Bad - S01E03 - ...und der Leichensack.de.srt"), true)
	mustExist(t, filepath.Join(tmp, "anderswo"), false)
	// Leere Release-Ordner verschwinden, Ordner mit Übrigem (NFO) bleiben.
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p-GRP"), false)
	mustExist(t, filepath.Join(src, "The.Matrix.1999.1080p.BluRay.x264-GRP/Subs"), false)
	mustExist(t, filepath.Join(src, "The.Matrix.1999.1080p.BluRay.x264-GRP/The.Matrix.1999.1080p.BluRay.x264-GRP.nfo"), true)

	// Nochmal schnüffeln: alles liegt schon richtig.
	again, err := Scan(context.Background(), cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range again {
		if it.Status != StatusSame {
			t.Errorf("zweiter Lauf %s: %s %s", it.RelSource, it.Status, it.Message)
		}
	}

	problems, err := Undo(jpath)
	if err != nil || len(problems) > 0 {
		t.Fatalf("Undo: %v %v", err, problems)
	}
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p-GRP/Breaking.Bad.S01E03.720p.de.srt"), true)
	mustExist(t, filepath.Join(src, "The.Matrix.1999.1080p.BluRay.x264-GRP/Subs/English.srt"), true)
	mustExist(t, filepath.Join(src, "Filme"), false)
	mustExist(t, filepath.Join(src, "Serien"), false)
}
