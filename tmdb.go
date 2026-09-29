package main

import (
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

// Candidate ist ein Treffer aus der Online-Datenbank.
type Candidate struct {
	ID            int    `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title,omitempty"`
	Year          int    `json:"year,omitempty"`
	Overview      string `json:"overview,omitempty"`
	Poster        string `json:"poster,omitempty"`
}

// TMDB ist ein kleiner Client für themoviedb.org (API v3).
type TMDB struct {
	Key      string
	Language string
	BaseURL  string
	HTTP     *http.Client

	mu    sync.Mutex
	cache map[string][]byte
}

func NewTMDB(key, lang string) *TMDB {
	return &TMDB{
		Key:      strings.TrimSpace(key),
		Language: lang,
		BaseURL:  "https://api.themoviedb.org/3",
		HTTP:     &http.Client{Timeout: 15 * time.Second},
		cache:    map[string][]byte{},
	}
}

func (t *TMDB) Enabled() bool { return t != nil && t.Key != "" }

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
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Accept", "application/json")
		if bearer {
			req.Header.Set("Authorization", "Bearer "+t.Key)
		}
		resp, err := t.HTTP.Do(req)
		if err != nil {
			return fmt.Errorf("TMDB nicht erreichbar: %w", err)
		}
		defer resp.Body.Close()
		body, err = io.ReadAll(io.LimitReader(resp.Body, 4<<20))
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
	var r struct {
		Name string `json:"name"`
	}
	err := t.get(ctx, fmt.Sprintf("/tv/%d/season/%d/episode/%d", tvID, season, episode), nil, &r)
	return r.Name, err
}
