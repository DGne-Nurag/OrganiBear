package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMovieHash(t *testing.T) {
	p := filepath.Join(t.TempDir(), "v.mkv")
	data := make([]byte, 128<<10)
	binary.LittleEndian.PutUint64(data, 1)
	binary.LittleEndian.PutUint64(data[len(data)-8:], 2)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	h, err := movieHash(p)
	if want := fmt.Sprintf("%016x", len(data)+1+2); err != nil || h != want {
		t.Errorf("Hash %s, erwartet %s (%v)", h, want, err)
	}
	// Kleine Dateien: Anfang und Ende überlappen, beide werden gezählt.
	if err := os.WriteFile(p, []byte{3, 0, 0, 0, 0, 0, 0, 0}, 0o600); err != nil {
		t.Fatal(err)
	}
	if h, _ := movieHash(p); h != fmt.Sprintf("%016x", 8+3+3) {
		t.Errorf("kleine Datei: %s", h)
	}
}

func TestHasSubtitle(t *testing.T) {
	dir := t.TempDir()
	touch(t, dir, "Film (2010).ger.forced.srt", "Film (2010).nfo", "Anderer Film.en.srt")
	video := filepath.Join(dir, "Film (2010).mkv")
	if !hasSubtitle(video, "de") {
		t.Error("deutscher Untertitel (ger) nicht erkannt")
	}
	if hasSubtitle(video, "en") {
		t.Error("englischer Untertitel eines anderen Films gezählt")
	}
}

// withAppKey setzt für einen Test den eingebauten API-Key und die Version.
func withAppKey(t *testing.T, key string) {
	oldKey, oldVersion := openSubtitlesKey, version
	openSubtitlesKey, version = key, "v0.1.0"
	t.Cleanup(func() { openSubtitlesKey, version = oldKey, oldVersion })
}

// fakeOpenSubs spielt die OpenSubtitles-API nach. quota < 0 heißt: Limit erreicht.
func fakeOpenSubs(t *testing.T, quota int, seen *[]string) *OpenSubs {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/file/") && (r.Header.Get("Api-Key") != "oskey" || r.Header.Get("User-Agent") != "OrganiBear v0.1.0") {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		*seen = append(*seen, r.Method+" "+r.URL.Path)
		switch r.URL.Path {
		case "/login":
			var c map[string]string
			_ = json.NewDecoder(r.Body).Decode(&c)
			if c["username"] != "baer" || c["password"] != "honig" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"token": "jwt", "base_url": "evil.example.com"})
		case "/subtitles":
			q := r.URL.Query()
			if q.Get("tmdb_id") != "603" || q.Get("languages") != "en" || len(q.Get("moviehash")) != 16 {
				t.Errorf("Suche: %v", q)
			}
			res := func(lang string, id int, hash bool, dl int) map[string]any {
				return map[string]any{"attributes": map[string]any{"language": lang, "moviehash_match": hash, "download_count": dl,
					"files": []map[string]any{{"file_id": id}}}}
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{res("en", 1, false, 9000), res("en", 2, true, 10), res("de", 3, true, 1)}})
		case "/download":
			if r.Header.Get("Authorization") != "Bearer jwt" {
				t.Error("Download ohne Anmeldung")
			}
			if quota < 0 {
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			var b struct {
				FileID int `json:"file_id"`
			}
			_ = json.NewDecoder(r.Body).Decode(&b)
			_ = json.NewEncoder(w).Encode(map[string]any{"link": fmt.Sprintf("%s/file/%d", srv.URL, b.FileID), "remaining": quota})
		case "/file/2":
			_, _ = w.Write([]byte("1\n00:00:01,000 --> 00:00:02,000\nWake up, Neo.\n"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	withAppKey(t, "oskey")
	o := NewOpenSubs(Subtitles{User: "baer", Password: "honig"})
	o.BaseURL, o.insecureLinks = srv.URL, true
	return o
}

func TestFetchSubtitles(t *testing.T) {
	dst := t.TempDir()
	touch(t, dst, "Filme/Matrix (1999)/Matrix (1999).mkv", "Filme/Matrix (1999)/Matrix (1999).de.srt")
	cfg := DefaultConfig()
	cfg.TargetDir = dst
	cfg.Subtitles = Subtitles{Enabled: true, Languages: []string{"de", "en"}}
	it := &Item{Info: MediaInfo{Title: "Matrix", Year: 1999, TMDBID: 603}, Target: filepath.Join(dst, "Filme", "Matrix (1999)", "Matrix (1999).mkv")}

	var seen []string
	subs := fakeOpenSubs(t, 5, &seen)
	created, warns := FetchSubtitles(context.Background(), cfg, subs, it)
	if len(warns) > 0 {
		t.Fatalf("Hinweise: %v", warns)
	}
	want := filepath.Join(dst, "Filme", "Matrix (1999)", "Matrix (1999).en.srt")
	if len(created) != 1 || created[0] != want {
		t.Fatalf("angelegt: %v", created)
	}
	if data, _ := os.ReadFile(want); !strings.Contains(string(data), "Wake up, Neo.") {
		t.Errorf("falscher Untertitel (Hash-Treffer sollte gewinnen): %q", data)
	}
	if strings.Contains(subs.BaseURL, "evil") {
		t.Error("fremde base_url aus der Anmeldung übernommen")
	}

	// Zweiter Lauf: alles da, keine Anfrage mehr.
	seen = nil
	if created, _ := FetchSubtitles(context.Background(), cfg, subs, it); created != nil || len(seen) > 0 {
		t.Errorf("zweiter Lauf: %v %v", created, seen)
	}
}

// Nach einer abgelehnten Anmeldung schickt der Bär dieselben Zugangsdaten
// nicht noch einmal, auch nicht für die nächste Datei.
func TestLoginRejectedOnce(t *testing.T) {
	var seen []string
	subs := fakeOpenSubs(t, 5, &seen)
	subs.Password = "falsch"
	for i := 0; i < 3; i++ {
		if err := subs.Login(context.Background()); !errors.Is(err, errLoginRejected) {
			t.Fatalf("Versuch %d: %v", i, err)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("Anmeldungen: %v", seen)
	}
}

func TestFetchSubtitlesQuota(t *testing.T) {
	dst := t.TempDir()
	touch(t, dst, "Matrix.mkv")
	cfg := DefaultConfig()
	cfg.TargetDir = dst
	cfg.Subtitles = Subtitles{Enabled: true, Languages: []string{"en"}}
	it := &Item{Info: MediaInfo{Title: "Matrix", TMDBID: 603}, Target: filepath.Join(dst, "Matrix.mkv")}
	var seen []string
	subs := fakeOpenSubs(t, -1, &seen)
	created, warns := FetchSubtitles(context.Background(), cfg, subs, it)
	if created != nil || len(warns) != 1 || !strings.Contains(warns[0], "Tageslimit") {
		t.Fatalf("Limit: %v %v", created, warns)
	}
	// Danach wird in diesem Lauf nichts mehr versucht.
	seen = nil
	FetchSubtitles(context.Background(), cfg, subs, it)
	for _, s := range seen {
		if s == "POST /download" {
			t.Error("nach erreichtem Limit weiter geladen")
		}
	}
}

func TestSubtitleConfig(t *testing.T) {
	c := DefaultConfig()
	c.Subtitles.Enabled = true
	c.Subtitles.Languages = nil
	if c.Validate() == nil {
		t.Error("ohne Sprache akzeptiert")
	}
	c.Subtitles.Languages = []string{" DE ", "pt-BR", ""}
	if err := c.Validate(); err != nil || strings.Join(c.Subtitles.Languages, ",") != "de,pt-br" {
		t.Errorf("%v %v", err, c.Subtitles.Languages)
	}
	c.Subtitles.Languages = []string{"deutsch"}
	if c.Validate() == nil {
		t.Error("ungültiges Sprachkürzel akzeptiert")
	}
}

func TestSubtitlesWithoutAppKey(t *testing.T) {
	withAppKey(t, "")
	dst := t.TempDir()
	touch(t, dst, "Matrix.mkv")
	cfg := DefaultConfig()
	cfg.TargetDir = dst
	cfg.Subtitles = Subtitles{Enabled: true, Languages: []string{"en"}}
	it := &Item{Info: MediaInfo{Title: "Matrix", TMDBID: 603}, Target: filepath.Join(dst, "Matrix.mkv")}
	if created, warns := FetchSubtitles(context.Background(), cfg, NewOpenSubs(cfg.Subtitles), it); created != nil || warns != nil {
		t.Errorf("ohne eingebauten Key: %v %v", created, warns)
	}
}

func TestUserAgent(t *testing.T) {
	withAppKey(t, "k")
	for v, want := range map[string]string{"v0.1.0": "OrganiBear v0.1.0", "0.2.0": "OrganiBear v0.2.0", "dev": "OrganiBear vdev"} {
		version = v
		if got := userAgent(); got != want {
			t.Errorf("%s: %q", v, got)
		}
	}
}
