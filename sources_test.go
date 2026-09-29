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

// fakeFind ergänzt den TMDB-Testserver um /find für IMDb- und TheTVDB-IDs.
func fakeFind(t *testing.T) *TMDB {
	db := fakeTMDB(t)
	inner := db.BaseURL
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var out any
		switch r.URL.Path + "?" + r.URL.Query().Get("external_source") {
		case "/find/tt0133093?imdb_id":
			out = map[string]any{"movie_results": []map[string]any{{"id": 603, "title": "Matrix", "original_title": "The Matrix", "release_date": "1999-03-30"}}}
		case "/find/tt0903747?imdb_id":
			out = map[string]any{"tv_results": []map[string]any{{"id": 1396, "name": "Breaking Bad", "first_air_date": "2008-01-20"}}}
		case "/find/tt1480055?imdb_id": // eine einzelne Folge
			out = map[string]any{"tv_episode_results": []map[string]any{{"show_id": 1396, "season_number": 1, "episode_number": 3}}}
		case "/find/81189?tvdb_id":
			out = map[string]any{"tv_results": []map[string]any{{"id": 1396, "name": "Breaking Bad"}}}
		default:
			if strings.HasPrefix(r.URL.Path, "/find/") {
				out = map[string]any{"movie_results": []any{}}
				break
			}
			resp, err := http.Get(inner + r.URL.RequestURI())
			if err != nil {
				w.WriteHeader(http.StatusBadGateway)
				return
			}
			defer resp.Body.Close()
			w.WriteHeader(resp.StatusCode)
			var v any
			if json.NewDecoder(resp.Body).Decode(&v) == nil {
				json.NewEncoder(w).Encode(v)
			}
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	db.BaseURL = srv.URL
	return db
}

// fakeTVDB spielt TheTVDB v4 mit einer Serie, die TMDB nicht kennt, und
// Breaking Bad (das TMDB über die TheTVDB-ID 81189 findet).
func fakeTVDB(t *testing.T) *TVDB {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/login" {
			var b map[string]string
			json.NewDecoder(r.Body).Decode(&b)
			if b["apikey"] != "tvkey" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok"}})
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		q := strings.ToLower(r.URL.Query().Get("query"))
		var out any
		switch {
		case r.URL.Path == "/search" && strings.Contains(q, "hoshi"):
			out = map[string]any{"data": []map[string]any{{"tvdb_id": "424242", "name": "Hoshi no Kuma", "year": "2019",
				"translations": map[string]string{"deu": "Der Sternenbär"}, "thumbnail": "https://artworks.thetvdb.com/x.jpg"}}}
		case r.URL.Path == "/search" && strings.Contains(q, "breaking"):
			out = map[string]any{"data": []map[string]any{{"tvdb_id": "81189", "name": "Breaking Bad", "year": "2008"}}}
		case r.URL.Path == "/search":
			out = map[string]any{"data": []any{}}
		case r.URL.Path == "/series/424242/episodes/default/deu" && r.URL.Query().Get("page") == "0" && r.URL.Query().Get("season") == "":
			out = map[string]any{"data": map[string]any{"episodes": []map[string]any{
				{"seasonNumber": 0, "number": 1, "absoluteNumber": 0, "name": "Special"},
				{"seasonNumber": 1, "number": 12, "absoluteNumber": 12, "name": "Honig"}}},
				"links": map[string]any{"next": "https://api4.thetvdb.com/v4/series/424242/episodes/default/deu?page=1"}}
		case r.URL.Path == "/series/424242/episodes/default/deu" && r.URL.Query().Get("page") == "1":
			out = map[string]any{"data": map[string]any{"episodes": []map[string]any{
				{"seasonNumber": 2, "number": 1, "absoluteNumber": 13, "name": "Winterschlaf"}}}, "links": map[string]any{"next": nil}}
		case r.URL.Path == "/series/424242/episodes/default/deu" && r.URL.Query().Get("season") == "1":
			out = map[string]any{"data": map[string]any{"episodes": []map[string]any{{"seasonNumber": 1, "number": 12, "name": "Honig"}}}}
		case r.URL.Path == "/series/424242/episodes/dvd/deu":
			w.WriteHeader(http.StatusNotFound) // keine Übersetzung: Originalsprache
			return
		case r.URL.Path == "/series/424242/episodes/dvd":
			out = map[string]any{"data": map[string]any{"episodes": []map[string]any{{"seasonNumber": 1, "number": 12, "name": "Honey"}}}}
		default:
			w.WriteHeader(http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(out)
	}))
	t.Cleanup(srv.Close)
	tv := &TVDB{Key: "tvkey", Language: "deu", Order: "default", BaseURL: srv.URL, HTTP: srv.Client(), cache: map[string][]byte{}}
	return tv
}

func TestLookupIMDBID(t *testing.T) {
	ctx := context.Background()
	db := fakeFind(t)

	it := &Item{Parsed: ParseName("The.Matrix.1999.tt0133093.1080p.mkv")}
	Lookup(ctx, db, it, it.Parsed.Title, it.Parsed.Year)
	if !it.Matched || it.Info.TMDBID != 603 || it.Info.IMDBID != "tt0133093" || it.Info.Source != "tmdb" {
		t.Fatalf("IMDb-ID aus dem Namen: %+v", it.Info)
	}

	// Eine Folgen-ID liefert Serie, Staffel und Folge.
	it = &Item{Parsed: ParseName("irgendwas.mkv")}
	Lookup(ctx, db, it, "tt1480055", 0)
	if !it.Info.Series || it.Info.TMDBID != 1396 || it.Info.Season != 1 || it.Info.Episode != 3 || it.Info.EpisodeTitle == "" {
		t.Fatalf("Folgen-ID im Suchfeld: %+v", it.Info)
	}
	if it.Info.IMDBID != "tt1480055" || it.Info.TVDBID != 81189 {
		t.Fatalf("IDs der Folge: imdb %q tvdb %d", it.Info.IMDBID, it.Info.TVDBID)
	}

	// Eine unbekannte ID sucht nicht nach dem Text „tt…“.
	it = &Item{Parsed: ParseName("irgendwas.mkv")}
	Lookup(ctx, db, it, "tt9999999", 0)
	if it.Matched || len(it.Candidates) != 0 {
		t.Fatalf("unbekannte ID: %+v", it)
	}

	// Eine TMDB-ID im Ordnernamen geht direkt.
	it = &Item{Parsed: ParsePath("Matrix (1999) [tmdbid=603]/film.mkv")}
	Lookup(ctx, db, it, it.Parsed.Title, it.Parsed.Year)
	if it.Info.TMDBID != 603 || it.Info.Title != "Matrix" {
		t.Fatalf("TMDB-ID aus dem Ordner: %+v", it.Info)
	}
}

func TestLookupTVDBFallback(t *testing.T) {
	ctx := context.Background()
	db := fakeFind(t)
	db.TVDB = fakeTVDB(t)

	// TMDB kennt die Serie nicht, TheTVDB schon: fortlaufende Folge 13 ist S02E01.
	it := &Item{Parsed: ParseName("Hoshi no Kuma - 13 [1080p].mkv")}
	Lookup(ctx, db, it, it.Parsed.Title, it.Parsed.Year)
	if !it.Matched || it.Info.Source != "tvdb" || it.Info.TVDBID != 424242 || it.Info.TMDBID != 0 {
		t.Fatalf("TheTVDB-Treffer: %+v (%s)", it.Info, it.Message)
	}
	if it.Info.Title != "Der Sternenbär" || it.Info.Season != 2 || it.Info.Episode != 1 || it.Info.EpisodeTitle != "Winterschlaf" {
		t.Fatalf("fortlaufende Folge über TheTVDB: %+v", it.Info)
	}
	if !strings.Contains(it.Message, "TheTVDB") {
		t.Fatalf("Hinweis fehlt: %q", it.Message)
	}

	// Normale Zählung: Folgentitel aus TheTVDB.
	it = &Item{Parsed: ParseName("Hoshi no Kuma S01E12.mkv")}
	Lookup(ctx, db, it, it.Parsed.Title, 0)
	if it.Info.EpisodeTitle != "Honig" {
		t.Fatalf("Folgentitel: %+v", it.Info)
	}

	// DVD-Reihenfolge ohne deutsche Übersetzung fällt auf die Originalsprache zurück.
	db.TVDB.Order = "dvd"
	if name, err := db.TVDB.EpisodeTitle(ctx, 424242, 1, 12); err != nil || name != "Honey" {
		t.Fatalf("DVD-Reihenfolge: %q %v", name, err)
	}
	db.TVDB.Order = "default"

	// Ein TheTVDB-Treffer, den TMDB über die TheTVDB-ID kennt, bekommt die TMDB-ID.
	it = &Item{Parsed: ParseName("Breaking Bad S01E03.mkv"), Candidates: nil}
	it.Info = infoFromParsed(it.Parsed)
	ApplyCandidate(ctx, db, it, Candidate{ID: 81189, Source: "tvdb", Title: "Breaking Bad", Year: 2008})
	if it.Info.TMDBID != 1396 || it.Info.TVDBID != 81189 || it.Info.Source != "tvdb" {
		t.Fatalf("TMDB-ID über TheTVDB-ID: %+v", it.Info)
	}

	// Filme fragen TheTVDB nie.
	it = &Item{Parsed: ParseName("Hoshi no Kuma (2019).mkv")}
	Lookup(ctx, db, it, it.Parsed.Title, it.Parsed.Year)
	if it.Matched {
		t.Fatalf("Film bei TheTVDB gesucht: %+v", it.Info)
	}
}

func TestTVDBWrongKey(t *testing.T) {
	tv := fakeTVDB(t)
	tv.Key = "falsch"
	if _, err := tv.Search(context.Background(), "hoshi", 0); err == nil || !strings.Contains(err.Error(), "Key") {
		t.Fatalf("falscher Key: %v", err)
	}
}

func TestNFOIDs(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, body string) string {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	film := write("Film/film.mkv", "x")
	write("Film/film.nfo", `<movie><uniqueid type="imdb">tt0133093</uniqueid><uniqueid type="tmdb" default="true">603</uniqueid></movie>`)
	if imdb, tmdb := nfoIDs(film, false); imdb != "tt0133093" || tmdb != 603 {
		t.Fatalf("Film-NFO: %q %d", imdb, tmdb)
	}
	// Folge: die NFO der Folge zählt nicht, tvshow.nfo eine Ebene höher schon.
	ep := write("Serie/Season 1/S01E01.mkv", "x")
	write("Serie/Season 1/S01E01.nfo", `<episodedetails><uniqueid type="imdb">tt1111111</uniqueid></episodedetails>`)
	write("Serie/tvshow.nfo", "https://www.themoviedb.org/tv/1396-breaking-bad\n")
	if imdb, tmdb := nfoIDs(ep, true); imdb != "" || tmdb != 1396 {
		t.Fatalf("tvshow.nfo: %q %d", imdb, tmdb)
	}
	// Ohne NFO: nichts.
	if imdb, tmdb := nfoIDs(write("Leer/x.mkv", "x"), false); imdb != "" || tmdb != 0 {
		t.Fatalf("ohne NFO: %q %d", imdb, tmdb)
	}
}

func TestScanReadsNFO(t *testing.T) {
	root := t.TempDir()
	touch(t, root, "Unbekannt/video.mkv")
	os.WriteFile(filepath.Join(root, "Unbekannt/video.nfo"), []byte("<movie><id>tt0133093</id></movie>"), 0o644)
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir = root, filepath.Join(root, "ziel")
	items, err := Scan(context.Background(), cfg, fakeFind(t))
	if err != nil {
		t.Fatal(err)
	}
	it := itemFor(items, "Unbekannt/video.mkv")
	if it == nil || it.Info.TMDBID != 603 {
		t.Fatalf("Scan mit NFO: %+v", it)
	}
}
