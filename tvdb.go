package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TheTVDB (API v4) als zweite Quelle für Serien, wenn TMDB nichts findet.
// Den Projekt-Key bringt das Release mit (tvdbKey, wie bei OpenSubtitles),
// lizenziert als „Negotiated Contract“: kostenlos für kleine Projekte, aber
// wo Daten von TheTVDB angezeigt werden, muss ein Link auf TheTVDB.com stehen.

// tvdbKey wird beim Release-Build per -ldflags "-X main.tvdbKey=..." gesetzt.
var tvdbKey = ""

// TVDB ist ein kleiner Client für api4.thetvdb.com.
type TVDB struct {
	Key      string
	Language string // dreistellig, z. B. "deu"
	Order    string // Folgenreihenfolge: "default" (wie ausgestrahlt) oder "dvd"
	BaseURL  string
	HTTP     *http.Client

	mu    sync.Mutex
	token string
	cache map[string][]byte
}

// NewTVDB liefert nil, wenn TheTVDB aus ist oder kein Key eingebaut ist.
func NewTVDB(c TheTVDB, lang string) *TVDB {
	if !c.Enabled || tvdbKey == "" {
		return nil
	}
	order := "default"
	if c.Order == "dvd" {
		order = "dvd"
	}
	return &TVDB{
		Key: tvdbKey, Language: lang3(lang), Order: order,
		BaseURL: "https://api4.thetvdb.com/v4",
		HTTP:    &http.Client{Timeout: 15 * time.Second},
		cache:   map[string][]byte{},
	}
}

func (t *TVDB) Enabled() bool { return t != nil && t.Key != "" }

// lang3 macht aus "de-DE" das dreistellige Kürzel, das TheTVDB erwartet.
func lang3(lang string) string {
	l := strings.ToLower(lang)
	if i := strings.IndexAny(l, "-_"); i > 0 {
		l = l[:i]
	}
	for three, two := range iso6392 {
		// iso6392 enthält für manche Sprachen zwei Kürzel (ger/deu); TheTVDB nimmt die Terminologie-Form.
		if two == l && (len(three) == 3) && !bibliographic[three] {
			return three
		}
	}
	return "eng"
}

var bibliographic = map[string]bool{"ger": true, "fre": true, "chi": true, "dut": true, "cze": true, "gre": true, "rum": true}

func (t *TVDB) login(ctx context.Context) error {
	t.mu.Lock()
	has := t.token != ""
	t.mu.Unlock()
	if has {
		return nil
	}
	body := map[string]string{"apikey": t.Key}
	var r struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := t.call(ctx, http.MethodPost, "/login", body, &r); err != nil {
		if strings.Contains(err.Error(), "401") {
			return errors.New("TheTVDB lehnt den eingebauten Key ab")
		}
		return err
	}
	t.mu.Lock()
	t.token = r.Data.Token
	t.mu.Unlock()
	return nil
}

// get holt eine Antwort (mit Anmeldung und Zwischenspeicher).
func (t *TVDB) get(ctx context.Context, path string, q url.Values, out any) error {
	if err := t.login(ctx); err != nil {
		return err
	}
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	t.mu.Lock()
	body, ok := t.cache[path]
	t.mu.Unlock()
	if ok {
		return json.Unmarshal(body, out)
	}
	var raw json.RawMessage
	if err := t.call(ctx, http.MethodGet, path, nil, &raw); err != nil {
		return err
	}
	t.mu.Lock()
	t.cache[path] = raw
	t.mu.Unlock()
	return json.Unmarshal(raw, out)
}

func (t *TVDB) call(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(data)
	}
	// #nosec G704 -- Host ist fest (BaseURL), Pfade enthalten nur Zahlen-IDs und kodierte Suchbegriffe
	req, err := http.NewRequestWithContext(ctx, method, t.BaseURL+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	t.mu.Lock()
	if t.token != "" {
		req.Header.Set("Authorization", "Bearer "+t.token)
	}
	t.mu.Unlock()
	resp, err := t.HTTP.Do(req) // #nosec G704 -- siehe oben
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("TheTVDB nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return errNotFound
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("TheTVDB antwortet mit %s", resp.Status)
	}
	return json.Unmarshal(data, out)
}

type tvdbSearchResult struct {
	TVDBID       string            `json:"tvdb_id"`
	Name         string            `json:"name"`
	Year         string            `json:"year"`
	Overview     string            `json:"overview"`
	Thumbnail    string            `json:"thumbnail"`
	Translations map[string]string `json:"translations"`
	Overviews    map[string]string `json:"overviews"`
}

// Search sucht eine Serie. Die Treffer tragen Source "tvdb".
func (t *TVDB) Search(ctx context.Context, query string, year int) ([]Candidate, error) {
	q := url.Values{"query": {query}, "type": {"series"}, "limit": {"8"}}
	if year > 0 {
		q.Set("year", strconv.Itoa(year))
	}
	var r struct {
		Data []tvdbSearchResult `json:"data"`
	}
	if err := t.get(ctx, "/search", q, &r); err != nil {
		return nil, err
	}
	if len(r.Data) == 0 && year > 0 {
		return t.Search(ctx, query, 0)
	}
	var out []Candidate
	for _, s := range r.Data {
		id, err := strconv.Atoi(s.TVDBID)
		if err != nil || id <= 0 {
			continue
		}
		c := Candidate{ID: id, Source: "tvdb", Title: s.Name, OriginalTitle: s.Name, Overview: s.Overview}
		if tr := s.Translations[t.Language]; tr != "" {
			c.Title = tr
		}
		if ov := s.Overviews[t.Language]; ov != "" {
			c.Overview = ov
		}
		c.Year, _ = strconv.Atoi(s.Year)
		if strings.HasPrefix(s.Thumbnail, "https://") {
			c.Poster = s.Thumbnail
		}
		out = append(out, c)
		if len(out) == 8 {
			break
		}
	}
	return out, nil
}

type tvdbEpisode struct {
	Season   int    `json:"seasonNumber"`
	Number   int    `json:"number"`
	Absolute int    `json:"absoluteNumber"`
	Name     string `json:"name"`
}

// episodes lädt eine Seite der Folgenliste in der eingestellten Reihenfolge
// und Sprache. Fehlt die Übersetzung, kommt die Originalsprache.
func (t *TVDB) episodes(ctx context.Context, seriesID int, q url.Values) ([]tvdbEpisode, bool, error) {
	var r struct {
		Data struct {
			Episodes []tvdbEpisode `json:"episodes"`
		} `json:"data"`
		Links struct {
			Next *string `json:"next"`
		} `json:"links"`
	}
	path := fmt.Sprintf("/series/%d/episodes/%s/%s", seriesID, t.Order, t.Language)
	err := t.get(ctx, path, q, &r)
	if errors.Is(err, errNotFound) {
		path = fmt.Sprintf("/series/%d/episodes/%s", seriesID, t.Order)
		err = t.get(ctx, path, q, &r)
	}
	return r.Data.Episodes, r.Links.Next != nil && *r.Links.Next != "", err
}

// EpisodeTitle holt den Folgentitel in der eingestellten Reihenfolge.
func (t *TVDB) EpisodeTitle(ctx context.Context, seriesID, season, episode int) (string, error) {
	eps, _, err := t.episodes(ctx, seriesID, url.Values{"page": {"0"}, "season": {strconv.Itoa(season)}, "episodeNumber": {strconv.Itoa(episode)}})
	if err != nil {
		return "", err
	}
	for _, e := range eps {
		if e.Season == season && e.Number == episode {
			return e.Name, nil
		}
	}
	return "", errNotFound
}

// Absolute sucht zu einer fortlaufenden Folgennummer Staffel, Folge und Titel.
func (t *TVDB) Absolute(ctx context.Context, seriesID, abs int) (tvdbEpisode, error) {
	for page := 0; page < 20; page++ {
		eps, more, err := t.episodes(ctx, seriesID, url.Values{"page": {strconv.Itoa(page)}})
		if err != nil {
			return tvdbEpisode{}, err
		}
		for _, e := range eps {
			if e.Absolute == abs && e.Season > 0 {
				return e, nil
			}
		}
		if !more {
			break
		}
	}
	return tvdbEpisode{}, errNotFound
}
