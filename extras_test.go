package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtrasApplyUndo(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "downloads"), filepath.Join(tmp, "medien")
	touch(t, src, "The.Matrix.1999.1080p.mkv", "Breaking.Bad.S01E03.720p.mkv")
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey = src, dst, "testkey"
	cfg.Extras.NFO, cfg.Extras.Artwork = true, true
	db := fakeTMDB(t)
	ctx := context.Background()

	items, err := Scan(ctx, cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	// Ein Fanart, das schon da ist, darf nicht überschrieben werden.
	movieDir := filepath.Join(dst, "Filme", "Matrix (1999)")
	touch(t, movieDir, "fanart.jpg")

	ids := map[int]bool{}
	for _, it := range items {
		ids[it.ID] = true
	}
	extras := func(it *Item) ([]string, []string) { return WriteExtras(ctx, cfg, db, it) }
	j, jpath, err := Apply(cfg, items, ids, filepath.Join(tmp, "verlauf"), extras)
	if err != nil {
		t.Fatal(err)
	}
	if len(j.Warnings) > 0 {
		t.Errorf("Hinweise: %v", j.Warnings)
	}

	read := func(p string) string {
		t.Helper()
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("fehlt: %v", err)
		}
		return string(data)
	}
	nfo := read(filepath.Join(movieDir, "Matrix (1999).nfo"))
	for _, want := range []string{"<movie>", "<title>Matrix</title>", "<runtime>136</runtime>", "<genre>Science Fiction</genre>", `<uniqueid type="tmdb" default="true">603</uniqueid>`, `<uniqueid type="imdb">tt0133093</uniqueid>`} {
		if !strings.Contains(nfo, want) {
			t.Errorf("Film-NFO ohne %s:\n%s", want, nfo)
		}
	}
	if read(filepath.Join(movieDir, "poster.jpg")) != string(fakeJPEG) {
		t.Error("Poster falsch")
	}
	if read(filepath.Join(movieDir, "fanart.jpg")) != "fanart.jpg" {
		t.Error("vorhandenes Fanart wurde überschrieben")
	}

	show := filepath.Join(dst, "Serien", "Breaking Bad (2008)")
	if s := read(filepath.Join(show, "tvshow.nfo")); !strings.Contains(s, `<uniqueid type="tvdb">81189</uniqueid>`) || !strings.Contains(s, "<tvshow>") {
		t.Errorf("tvshow.nfo:\n%s", s)
	}
	read(filepath.Join(show, "poster.jpg"))
	read(filepath.Join(show, "season01-poster.png"))
	epBase := filepath.Join(show, "Staffel 01", "Breaking Bad - S01E03 - ...und der Leichensack")
	ep := read(epBase + ".nfo")
	for _, want := range []string{"<episodedetails>", "<season>1</season>", "<episode>3</episode>", "<aired>2008-02-10</aired>", "Walt &amp; Jesse &lt;räumen&gt; auf."} {
		if !strings.Contains(ep, want) {
			t.Errorf("Folgen-NFO ohne %s:\n%s", want, ep)
		}
	}
	read(epBase + "-thumb.jpg")

	// Undo räumt alles Angelegte wieder weg, das vorher vorhandene Fanart bleibt.
	if problems, err := Undo(jpath); err != nil || len(problems) > 0 {
		t.Fatalf("Undo: %v %v", err, problems)
	}
	if _, err := os.Stat(show); !os.IsNotExist(err) {
		t.Errorf("Serienordner nach Undo noch da: %v", err)
	}
	entries, _ := os.ReadDir(movieDir)
	if len(entries) != 1 || entries[0].Name() != "fanart.jpg" {
		t.Errorf("Filmordner nach Undo: %v", entries)
	}
}

func TestExtrasOffByDefault(t *testing.T) {
	tmp := t.TempDir()
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey = filepath.Join(tmp, "in"), filepath.Join(tmp, "out"), "testkey"
	it := &Item{Info: MediaInfo{Title: "Matrix", Year: 1999, TMDBID: 603}, Target: filepath.Join(tmp, "out", "Matrix.mkv")}
	if created, warns := WriteExtras(context.Background(), cfg, fakeTMDB(t), it); created != nil || warns != nil {
		t.Errorf("ohne Einstellung wurde etwas angelegt: %v %v", created, warns)
	}
}

func TestTitleDir(t *testing.T) {
	info := MediaInfo{Title: "Dark", Year: 2017, Series: true, Season: 1, Episode: 2}
	for _, c := range []struct {
		tmpl  string
		first bool
		rel   string
		last  bool
		ok    bool
	}{
		{"Serien/{title} ({year})/Staffel {season:02}/{title} - S{season:02}E{episode:02}", true, filepath.Join("Serien", "Dark (2017)"), false, true},
		{"Filme/{first_letter}/{title} ({year})/{title}", false, filepath.Join("Filme", "D", "Dark (2017)"), true, true},
		{"Filme/{title} ({year})", false, "", false, false},
	} {
		rel, last, ok := titleDir(c.tmpl, info, c.first)
		if rel != c.rel || last != c.last || ok != c.ok {
			t.Errorf("%s: %q %v %v", c.tmpl, rel, last, ok)
		}
	}
}

func TestJournalRejectsCreateWithSource(t *testing.T) {
	j := Journal{SourceDir: "/in", TargetDir: "/out", Ops: []FileOp{{Source: "/in/a", Target: "/out/a.nfo", Action: ActionCreate}}}
	if j.validate() == nil {
		t.Error("angelegte Datei mit Quelle wurde akzeptiert")
	}
	j.Ops[0].Source = ""
	if err := j.validate(); err != nil {
		t.Error(err)
	}
	j.Ops[0].Target = "/etc/passwd"
	if j.validate() == nil {
		t.Error("angelegte Datei außerhalb des Ziels wurde akzeptiert")
	}
}

func TestRefreshServers(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/library/sections/all/refresh" && r.Header.Get("X-Plex-Token") == "plextoken":
			got = append(got, "plex")
		case r.URL.Path == "/Library/Refresh" && r.Method == http.MethodPost && r.Header.Get("Authorization") == `MediaBrowser Token="jfkey"`:
			got = append(got, "jellyfin")
			w.WriteHeader(http.StatusNoContent)
		case r.URL.Path == "/jsonrpc" && r.Header.Get("Authorization") == "Basic "+base64.StdEncoding.EncodeToString([]byte("kodi:geheim")):
			var body struct{ Method string }
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Method != "VideoLibrary.Scan" {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			got = append(got, "kodi")
			_, _ = w.Write([]byte(`{"id":1,"jsonrpc":"2.0","result":"OK"}`))
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()

	x := Extras{
		Plex:     MediaServer{URL: srv.URL + "/", Token: "plextoken"},
		Jellyfin: MediaServer{URL: srv.URL, Token: "jfkey"},
		Kodi:     MediaServer{URL: srv.URL, User: "kodi", Password: "geheim"},
	}
	msgs := RefreshLibraries(context.Background(), x)
	if strings.Join(got, ",") != "plex,jellyfin,kodi" {
		t.Errorf("angestoßen: %v, Meldungen: %v", got, msgs)
	}
	x.Plex.Token = "falsch"
	if err := refreshServer(context.Background(), "plex", x.Plex); err == nil || !strings.Contains(err.Error(), "abgelehnt") {
		t.Errorf("falscher Token: %v", err)
	}
	if err := refreshServer(context.Background(), "plex", MediaServer{URL: "file:///etc/passwd"}); err == nil {
		t.Error("file-URL wurde akzeptiert")
	}
	if msgs := RefreshLibraries(context.Background(), Extras{}); len(msgs) != 0 {
		t.Errorf("ohne Server: %v", msgs)
	}
}

func TestExtrasMultiPart(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "in"), filepath.Join(tmp, "out")
	touch(t, src, "The.Matrix.1999.CD1.mkv", "The.Matrix.1999.CD2.mkv")
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey = src, dst, "testkey"
	cfg.Extras.NFO = true
	db := fakeTMDB(t)
	ctx := context.Background()
	items, err := Scan(ctx, cfg, db)
	if err != nil || len(items) != 2 {
		t.Fatalf("%v %d", err, len(items))
	}
	extras := func(it *Item) ([]string, []string) { return WriteExtras(ctx, cfg, db, it) }
	j, _, err := Apply(cfg, items, map[int]bool{items[0].ID: true, items[1].ID: true}, filepath.Join(tmp, "verlauf"), extras)
	if err != nil || len(j.Warnings) > 0 {
		t.Fatalf("%v %v", err, j.Warnings)
	}
	dir := filepath.Join(dst, "Filme", "Matrix (1999)")
	entries, _ := os.ReadDir(dir)
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	// Eine gemeinsame NFO ohne Teilangabe, dazu beide Teile.
	if strings.Join(names, "|") != "Matrix (1999) - part1.mkv|Matrix (1999) - part2.mkv|Matrix (1999).nfo" {
		t.Errorf("Inhalt: %v", names)
	}
}
