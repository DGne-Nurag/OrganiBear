package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Fehlende Untertitel von OpenSubtitles.com (REST-API v1). OpenSubtitles
// verlangt genau einen API-Key pro Anwendung (openSubtitlesKey); Nutzer dürfen
// keinen eigenen eintragen. Ohne Konto sind 5 Downloads pro IP und Tag erlaubt,
// mit kostenlosem Konto 20, mit VIP bis 1000.

var (
	errQuota           = errors.New("Tageslimit bei OpenSubtitles erreicht")
	errLoginRejected   = errors.New("OpenSubtitles lehnt Benutzer oder Passwort ab, bitte in den Einstellungen prüfen")
	errUnavailableSubs = errors.New("diese OrganiBear-Version enthält keinen OpenSubtitles-Zugang (nur in den offiziellen Releases)")
)

// OpenSubs ist ein kleiner Client für api.opensubtitles.com.
type OpenSubs struct {
	Key, User, Password string
	BaseURL             string
	HTTP                *http.Client

	insecureLinks bool // nur für Tests: Download-Links ohne https erlauben

	mu        sync.Mutex
	token     string    // nach dem Anmelden
	loginErr  error     // abgelehnte Anmeldung: OpenSubtitles will danach keine weiteren Versuche
	exhausted bool      // Tageslimit erreicht, für diesen Lauf nichts mehr laden
	last      time.Time // letzte Anfrage, OpenSubtitles erlaubt 5 pro Sekunde
	gap       time.Duration
}

func NewOpenSubs(s Subtitles) *OpenSubs {
	return &OpenSubs{
		Key: openSubtitlesKey, User: s.User, Password: s.Password,
		BaseURL: "https://api.opensubtitles.com/api/v1",
		HTTP:    &http.Client{Timeout: 20 * time.Second},
		gap:     250 * time.Millisecond,
	}
}

// userAgent ist der von OpenSubtitles verlangte Name samt Version, z. B.
// "OrganiBear v0.1.0".
func userAgent() string {
	return "OrganiBear v" + strings.TrimPrefix(version, "v")
}

// wait hält den Abstand zwischen zwei Anfragen ein (Limit: 5 pro Sekunde).
func (o *OpenSubs) wait(ctx context.Context) error {
	o.mu.Lock()
	next := o.last.Add(o.gap)
	now := time.Now()
	if next.Before(now) {
		next = now
	}
	o.last = next
	o.mu.Unlock()
	t := time.NewTimer(time.Until(next))
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (o *OpenSubs) do(ctx context.Context, method, path string, q url.Values, body, out any) error {
	u := o.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	if err := o.wait(ctx); err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return err
	}
	// OpenSubtitles verlangt einen User-Agent mit App-Name und Version.
	req.Header.Set("User-Agent", userAgent())
	req.Header.Set("Api-Key", o.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	o.mu.Lock()
	if o.token != "" {
		req.Header.Set("Authorization", "Bearer "+o.token)
	}
	o.mu.Unlock()
	resp, err := o.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("OpenSubtitles nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		if path == "/login" {
			return errLoginRejected
		}
		return errors.New("OpenSubtitles lehnt die Anfrage ab")
	case resp.StatusCode == http.StatusNotAcceptable:
		return errQuota
	case resp.StatusCode == http.StatusTooManyRequests:
		return errors.New("OpenSubtitles bittet um eine Pause, bitte später noch einmal")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("OpenSubtitles antwortet mit %s", resp.Status)
	}
	if out == nil {
		return nil
	}
	return json.Unmarshal(data, out)
}

// Login meldet mit dem Konto an, falls eins eingetragen ist.
func (o *OpenSubs) Login(ctx context.Context) error {
	if o.User == "" {
		return nil
	}
	o.mu.Lock()
	has, rejected := o.token != "", o.loginErr
	o.mu.Unlock()
	if has {
		return nil
	}
	if rejected != nil {
		return rejected
	}
	var r struct {
		Token   string `json:"token"`
		BaseURL string `json:"base_url"`
	}
	if err := o.do(ctx, http.MethodPost, "/login", nil, map[string]string{"username": o.User, "password": o.Password}, &r); err != nil {
		// Laut API-Doku ist die Anmeldung teuer: nach einer Ablehnung dieselben
		// Zugangsdaten nicht noch einmal schicken, für den Rest des Laufs gilt der Fehler.
		if errors.Is(err, errLoginRejected) {
			o.mu.Lock()
			o.loginErr = err
			o.mu.Unlock()
		}
		return err
	}
	o.mu.Lock()
	o.token = r.Token
	// VIP-Konten bekommen einen eigenen Server zugewiesen, aber nur bei OpenSubtitles.
	if r.BaseURL != "" && strings.HasSuffix(r.BaseURL, ".opensubtitles.com") && !strings.ContainsAny(r.BaseURL, "/:@") {
		o.BaseURL = "https://" + r.BaseURL + "/api/v1"
	}
	o.mu.Unlock()
	return nil
}

// Check prüft die Verbindung und ggf. das Konto.
func (o *OpenSubs) Check(ctx context.Context) error {
	if o.User != "" {
		return o.Login(ctx)
	}
	return o.do(ctx, http.MethodGet, "/infos/formats", nil, nil, nil)
}

type subResult struct {
	Attributes struct {
		Language          string `json:"language"`
		MovieHashMatch    bool   `json:"moviehash_match"`
		MachineTranslated bool   `json:"machine_translated"`
		AITranslated      bool   `json:"ai_translated"`
		DownloadCount     int    `json:"download_count"`
		Files             []struct {
			FileID int `json:"file_id"`
		} `json:"files"`
	} `json:"attributes"`
}

// best wählt je Sprache den besten Treffer: passender Datei-Hash vor
// menschlicher Übersetzung vor Beliebtheit. Mehrteilige (CD1/CD2) fallen weg.
func best(results []subResult, lang string) (int, bool) {
	score := func(r subResult) int {
		s := r.Attributes.DownloadCount
		if r.Attributes.MovieHashMatch {
			s += 1 << 40
		}
		if !r.Attributes.MachineTranslated && !r.Attributes.AITranslated {
			s += 1 << 30
		}
		return s
	}
	id, top := 0, -1
	for _, r := range results {
		if !strings.EqualFold(r.Attributes.Language, lang) || len(r.Attributes.Files) != 1 {
			continue
		}
		if s := score(r); s > top {
			id, top = r.Attributes.Files[0].FileID, s
		}
	}
	return id, top >= 0
}

// Search sucht Untertitel zu einem Eintrag, per Datei-Hash und TMDB-ID.
func (o *OpenSubs) Search(ctx context.Context, it *Item, hash string, langs []string) ([]subResult, error) {
	q := url.Values{"languages": {strings.Join(langs, ",")}}
	if hash != "" {
		q.Set("moviehash", hash)
	}
	switch {
	case it.Info.TMDBID > 0 && it.Info.Series:
		q.Set("parent_tmdb_id", strconv.Itoa(it.Info.TMDBID))
		q.Set("season_number", strconv.Itoa(it.Info.Season))
		q.Set("episode_number", strconv.Itoa(it.Info.Episode))
	case it.Info.TMDBID > 0:
		q.Set("tmdb_id", strconv.Itoa(it.Info.TMDBID))
	case hash == "":
		return nil, nil
	}
	var r struct {
		Data []subResult `json:"data"`
	}
	err := o.do(ctx, http.MethodGet, "/subtitles", q, nil, &r)
	return r.Data, err
}

// Download lädt eine Untertiteldatei (höchstens 5 MB).
func (o *OpenSubs) Download(ctx context.Context, fileID int) ([]byte, error) {
	o.mu.Lock()
	done := o.exhausted
	o.mu.Unlock()
	if done {
		return nil, errQuota
	}
	var r struct {
		Link      string `json:"link"`
		Remaining *int   `json:"remaining"`
	}
	err := o.do(ctx, http.MethodPost, "/download", nil, map[string]any{"file_id": fileID, "sub_format": "srt"}, &r)
	if errors.Is(err, errQuota) {
		o.mu.Lock()
		o.exhausted = true
		o.mu.Unlock()
	}
	if err != nil {
		return nil, err
	}
	if r.Remaining != nil && *r.Remaining <= 0 {
		o.mu.Lock()
		o.exhausted = true // dieser war der letzte für heute
		o.mu.Unlock()
	}
	u, err := url.Parse(r.Link)
	if err != nil || u.Host == "" || (u.Scheme != "https" && !(o.insecureLinks && u.Scheme == "http")) {
		return nil, errors.New("OpenSubtitles liefert keinen gültigen Download-Link")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.Link, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", userAgent())
	resp, err := o.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("Download fehlgeschlagen: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Download: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 5<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 5<<20 {
		return nil, errors.New("Untertitel ist zu groß")
	}
	return data, nil
}

// movieHash ist der OpenSubtitles-Hash: Dateigröße plus die Summe aller
// 64-Bit-Wörter (Little Endian) der ersten und letzten 64 KiB.
func movieHash(path string) (string, error) {
	f, err := os.Open(path) // #nosec G304 -- eben einsortierte Datei im Zielordner
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	const chunk = 64 << 10
	size := st.Size()
	h := uint64(size) // #nosec G115 -- Dateigrößen sind nie negativ
	buf := make([]byte, chunk)
	for _, off := range []int64{0, max(0, size-chunk)} {
		n, err := f.ReadAt(buf, off)
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
		for i := 0; i+8 <= n; i += 8 {
			h += binary.LittleEndian.Uint64(buf[i:])
		}
	}
	return fmt.Sprintf("%016x", h), nil
}

// langAliases sind übliche Schreibweisen einer Sprache in Dateinamen.
var langAliases = map[string][]string{
	"de": {"ger", "deu", "german", "deutsch"},
	"en": {"eng", "english"},
	"fr": {"fre", "fra", "french"},
	"es": {"spa", "spanish"},
	"it": {"ita", "italian"},
	"nl": {"dut", "nld", "dutch"},
	"pl": {"pol", "polish"},
	"pt": {"por", "portuguese"},
	"ru": {"rus", "russian"},
	"tr": {"tur", "turkish"},
	"sv": {"swe", "swedish"},
	"da": {"dan", "danish"},
	"no": {"nor", "norwegian"},
	"fi": {"fin", "finnish"},
	"ja": {"jpn", "japanese"},
}

var subExts = map[string]bool{".srt": true, ".sub": true, ".ass": true, ".ssa": true, ".vtt": true, ".sup": true, ".idx": true}

// hasSubtitle meldet, ob neben dem Video schon ein Untertitel in lang liegt,
// z. B. "Film (2010).de.srt", "Film (2010).ger.forced.srt".
func hasSubtitle(videoTarget, lang string) bool {
	base := strings.TrimSuffix(filepath.Base(videoTarget), filepath.Ext(videoTarget))
	entries, err := os.ReadDir(filepath.Dir(videoTarget))
	if err != nil {
		return false
	}
	names := append([]string{lang}, langAliases[strings.SplitN(lang, "-", 2)[0]]...)
	for _, e := range entries {
		n := e.Name()
		if !subExts[strings.ToLower(filepath.Ext(n))] || len(n) <= len(base) || !strings.EqualFold(n[:len(base)], base) {
			continue
		}
		for _, part := range strings.Split(strings.ToLower(n[len(base):]), ".") {
			for _, alias := range names {
				if part == alias {
					return true
				}
			}
		}
	}
	return false
}

// FetchSubtitles lädt nach dem Einsortieren fehlende Untertitel als
// "<Video>.<sprache>.srt". Vorhandene Dateien bleiben unangetastet.
func FetchSubtitles(ctx context.Context, cfg Config, subs *OpenSubs, it *Item) (created, warns []string) {
	s := cfg.Subtitles
	if !s.Enabled || subs == nil || subs.Key == "" {
		return nil, nil
	}
	var missing []string
	for _, l := range s.Languages {
		if !hasSubtitle(it.Target, l) {
			missing = append(missing, l)
		}
	}
	if len(missing) == 0 {
		return nil, nil
	}
	name := filepath.Base(it.Target)
	if err := subs.Login(ctx); err != nil {
		return nil, []string{"Untertitel " + name + ": " + err.Error()}
	}
	hash, _ := movieHash(it.Target)
	results, err := subs.Search(ctx, it, hash, missing)
	if err != nil {
		return nil, []string{"Untertitel " + name + ": " + err.Error()}
	}
	dst, _ := filepath.Abs(cfg.Target())
	w := &extraWriter{ctx: ctx, dst: dst}
	base := strings.TrimSuffix(it.Target, filepath.Ext(it.Target))
	for _, l := range missing {
		id, ok := best(results, l)
		if !ok {
			continue // nichts gefunden, das ist kein Fehler
		}
		data, err := subs.Download(ctx, id)
		if err != nil {
			w.warn("Untertitel "+l+" für "+name, err)
			if errors.Is(err, errQuota) {
				break
			}
			continue
		}
		w.write(base+"."+l+".srt", data)
	}
	return w.created, w.warns
}
