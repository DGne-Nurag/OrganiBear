package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Candidate ist ein Treffer aus der Online-Datenbank.
type Candidate struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title,omitempty"`
	Year          int    `json:"year,omitempty"`
	Overview      string `json:"overview,omitempty"`
	Poster        string `json:"poster,omitempty"`
	Source        string `json:"source,omitempty"` // leer: TMDB, "tvdb": TheTVDB
}

// TMDB ist ein kleiner Client für themoviedb.org (API v3).
type TMDB struct {
	Key      string
	Language string
	BaseURL  string
	ImageURL string // Basisadresse für Bilder in Originalgröße
	HTTP     *http.Client

	// TVDB ist die optionale zweite Quelle für Serien (nil, wenn aus).
	TVDB *TVDB

	mu    sync.Mutex
	cache map[string][]byte
}

func NewTMDB(key, lang string) *TMDB {
	return &TMDB{
		Key:      strings.TrimSpace(key),
		Language: lang,
		BaseURL:  "https://api.themoviedb.org/3",
		ImageURL: "https://image.tmdb.org/t/p/original",
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		cache:    map[string][]byte{},
	}
}

func (t *TMDB) Enabled() bool { return t != nil && t.Key != "" }

// Check prüft, ob TMDB den Key annimmt.
func (t *TMDB) Check(ctx context.Context) error {
	if !t.Enabled() {
		return errors.New("kein API-Key eingetragen")
	}
	var out struct {
		Success bool `json:"success"`
	}
	if err := t.get(ctx, "/authentication", nil, &out); err != nil {
		return err
	}
	if !out.Success {
		return errors.New("TMDB lehnt den API-Key ab")
	}
	return nil
}

func (t *TMDB) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	if t.Language != "" {
		q.Set("language", t.Language)
	}
	// Lange Tokens (v4 "Read Access Token") gehen als Bearer, kurze v3-Keys als Parameter.
	bearer := strings.HasPrefix(t.Key, "eyJ")
	if !bearer {
		q.Set("api_key", t.Key)
	}
	u := t.BaseURL + path + "?" + q.Encode()

	t.mu.Lock()
	body, ok := t.cache[u]
	t.mu.Unlock()
	if !ok {
		// #nosec G704 -- Host ist fest (BaseURL), Pfade enthalten nur Zahlen-IDs
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if bearer {
			req.Header.Set("Authorization", "Bearer "+t.Key)
		}
		resp, err := t.HTTP.Do(req) // #nosec G704 -- siehe oben
		if err != nil {
			// Die URL enthält den API-Key, also nur die eigentliche Ursache melden.
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			return fmt.Errorf("TMDB nicht erreichbar: %w", err)
		}
		defer resp.Body.Close()
		body, err = io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return err
		}
		switch {
		case resp.StatusCode == http.StatusUnauthorized:
			return errors.New("TMDB lehnt den API-Key ab")
		case resp.StatusCode == http.StatusNotFound:
			return errNotFound
		case resp.StatusCode != http.StatusOK:
			return fmt.Errorf("TMDB antwortet mit %s", resp.Status)
		}
		t.mu.Lock()
		if len(t.cache) > 500 {
			clear(t.cache)
		}
		t.cache[u] = body
		t.mu.Unlock()
	}
	return json.Unmarshal(body, out)
}

var errNotFound = errors.New("nicht gefunden")

type tmdbResult struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
}

func (r tmdbResult) candidate() Candidate {
	c := Candidate{ID: r.ID, Title: r.Title, OriginalTitle: r.OriginalTitle, Overview: r.Overview}
	date := r.ReleaseDate
	if c.Title == "" {
		c.Title, c.OriginalTitle, date = r.Name, r.OriginalName, r.FirstAirDate
	}
	if len(date) >= 4 {
		c.Year, _ = strconv.Atoi(date[:4])
	}
	if r.PosterPath != "" {
		c.Poster = "https://image.tmdb.org/t/p/w92" + r.PosterPath
	}
	return c
}

// Search sucht einen Film oder eine Serie. Findet die Suche mit Jahr nichts,
// wird ohne Jahr erneut gesucht.
func (t *TMDB) Search(ctx context.Context, series bool, query string, year int) ([]Candidate, error) {
	path, yearParam := "/search/movie", "year"
	if series {
		path, yearParam = "/search/tv", "first_air_date_year"
	}
	for _, y := range []int{year, 0} {
		q := url.Values{"query": {query}}
		if y > 0 {
			q.Set(yearParam, strconv.Itoa(y))
		}
		var res struct {
			Results []tmdbResult `json:"results"`
		}
		if err := t.get(ctx, path, q, &res); err != nil {
			return nil, err
		}
		if len(res.Results) > 0 || y == 0 {
			var out []Candidate
			for i, r := range res.Results {
				if i == 8 {
					break
				}
				out = append(out, r.candidate())
			}
			return out, nil
		}
	}
	return nil, nil
}

// Found ist ein Treffer über eine fremde ID (IMDb oder TheTVDB).
type Found struct {
	Candidate
	Series          bool
	Season, Episode int // gesetzt, wenn die ID zu einer einzelnen Folge gehört
}

// Find löst eine IMDb-ID ("tt0133093") oder TheTVDB-ID über TMDB auf.
// source ist "imdb_id" oder "tvdb_id".
func (t *TMDB) Find(ctx context.Context, source, id string) (Found, error) {
	var r struct {
		Movies   []tmdbResult `json:"movie_results"`
		TV       []tmdbResult `json:"tv_results"`
		Episodes []struct {
			ShowID  int `json:"show_id"`
			Season  int `json:"season_number"`
			Episode int `json:"episode_number"`
		} `json:"tv_episode_results"`
	}
	if err := t.get(ctx, "/find/"+url.PathEscape(id), url.Values{"external_source": {source}}, &r); err != nil {
		return Found{}, err
	}
	switch {
	case len(r.Movies) > 0:
		return Found{Candidate: r.Movies[0].candidate()}, nil
	case len(r.TV) > 0:
		return Found{Candidate: r.TV[0].candidate(), Series: true}, nil
	case len(r.Episodes) > 0 && r.Episodes[0].ShowID > 0:
		e := r.Episodes[0]
		c, err := t.Details(ctx, true, e.ShowID)
		return Found{Candidate: c, Series: true, Season: e.Season, Episode: e.Episode}, err
	}
	return Found{}, errNotFound
}

// Details lädt einen Film oder eine Serie direkt per ID.
func (t *TMDB) Details(ctx context.Context, series bool, id int) (Candidate, error) {
	path := "/movie/" + strconv.Itoa(id)
	if series {
		path = "/tv/" + strconv.Itoa(id)
	}
	var r tmdbResult
	if err := t.get(ctx, path, nil, &r); err != nil {
		return Candidate{}, err
	}
	return r.candidate(), nil
}

// EpisodeTitle holt den Namen einer Folge.
func (t *TMDB) EpisodeTitle(ctx context.Context, tvID, season, episode int) (string, error) {
	ep, err := t.Episode(ctx, tvID, season, episode)
	return ep.Name, err
}

// Meta sind die ausführlichen Daten eines Films oder einer Serie für NFO-Dateien.
type Meta struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	Name          string `json:"name"`
	OriginalTitle string `json:"original_title"`
	OriginalName  string `json:"original_name"`
	Overview      string `json:"overview"`
	ReleaseDate   string `json:"release_date"`
	FirstAirDate  string `json:"first_air_date"`
	Runtime       int    `json:"runtime"`
	Genres        []struct {
		Name string `json:"name"`
	} `json:"genres"`
	PosterPath   string `json:"poster_path"`
	BackdropPath string `json:"backdrop_path"`
	ExternalIDs  struct {
		IMDBID string `json:"imdb_id"`
		TVDBID int    `json:"tvdb_id"`
	} `json:"external_ids"`
}

// Meta lädt die ausführlichen Daten samt IMDb- und TheTVDB-ID.
func (t *TMDB) Meta(ctx context.Context, series bool, id int) (Meta, error) {
	path := "/movie/" + strconv.Itoa(id)
	if series {
		path = "/tv/" + strconv.Itoa(id)
	}
	var m Meta
	err := t.get(ctx, path, url.Values{"append_to_response": {"external_ids"}}, &m)
	return m, err
}

// Episode sind die Daten einer einzelnen Folge.
type Episode struct {
	ID        int    `json:"id"`
	Name      string `json:"name"`
	Overview  string `json:"overview"`
	AirDate   string `json:"air_date"`
	StillPath string `json:"still_path"`
}

// Episode lädt eine einzelne Folge.
func (t *TMDB) Episode(ctx context.Context, tvID, season, episode int) (Episode, error) {
	var ep Episode
	err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d/episode/%d", tvID, season, episode), nil, &ep)
	return ep, err
}

// Season ist eine Staffel einer Serie.
type Season struct {
	Number   int    `json:"number"`
	Name     string `json:"name"`
	Episodes int    `json:"episodes"`
}

// Seasons listet die Staffeln einer Serie (Specials als Staffel 0).
func (t *TMDB) Seasons(ctx context.Context, tvID int) ([]Season, error) {
	var r struct {
		Seasons []struct {
			Number   int    `json:"season_number"`
			Name     string `json:"name"`
			Episodes int    `json:"episode_count"`
		} `json:"seasons"`
	}
	if err := t.get(ctx, fmt.Sprintf("/tv/%d", tvID), nil, &r); err != nil {
		return nil, err
	}
	out := make([]Season, 0, len(r.Seasons))
	for _, s := range r.Seasons {
		out = append(out, Season(s))
	}
	return out, nil
}

// EpisodeName ist eine Folge in der Staffelliste.
type EpisodeName struct {
	Number int    `json:"number"`
	Name   string `json:"name"`
}

// SeasonEpisodes listet die Folgen einer Staffel.
func (t *TMDB) SeasonEpisodes(ctx context.Context, tvID, season int) ([]EpisodeName, error) {
	var r struct {
		Episodes []struct {
			Number int    `json:"episode_number"`
			Name   string `json:"name"`
		} `json:"episodes"`
	}
	if err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tvID, season), nil, &r); err != nil {
		return nil, err
	}
	out := make([]EpisodeName, 0, len(r.Episodes))
	for _, e := range r.Episodes {
		out = append(out, EpisodeName(e))
	}
	return out, nil
}

// absoluteEpisode rechnet eine fortlaufende Folgennummer (Anime) über die
// Folgenzahl der Staffeln in Staffel und Folge um. Specials zählen nicht mit.
func absoluteEpisode(seasons []Season, abs int) (season, episode int, ok bool) {
	if abs <= 0 {
		return 0, 0, false
	}
	sorted := append([]Season(nil), seasons...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Number < sorted[j].Number })
	n := abs
	for _, s := range sorted {
		if s.Number < 1 || s.Episodes <= 0 {
			continue
		}
		if n <= s.Episodes {
			return s.Number, n, true
		}
		n -= s.Episodes
	}
	return 0, 0, false
}

// SeasonPoster liefert den Bildpfad des Staffelposters.
func (t *TMDB) SeasonPoster(ctx context.Context, tvID, season int) (string, error) {
	var r struct {
		PosterPath string `json:"poster_path"`
	}
	err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tvID, season), nil, &r)
	return r.PosterPath, err
}

// Image lädt ein Bild von TMDB (höchstens 20 MB, nur JPEG oder PNG).
func (t *TMDB) Image(ctx context.Context, path string) ([]byte, error) {
	if !strings.HasPrefix(path, "/") || strings.Contains(path, "..") {
		return nil, errors.New("ungültiger Bildpfad")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.ImageURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := t.HTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return nil, fmt.Errorf("Bild nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Bild: TMDB antwortet mit %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 20<<20+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 20<<20 {
		return nil, errors.New("Bild ist zu groß")
	}
	if ct := http.DetectContentType(data); ct != "image/jpeg" && ct != "image/png" {
		return nil, fmt.Errorf("Bild hat unerwartetes Format %s", ct)
	}
	return data, nil
}
