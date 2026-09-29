package main

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Extras für Mediaserver: NFO-Dateien im Kodi-Format (lesen auch Jellyfin,
// Emby und Plex mit passendem Agent) sowie Poster, Hintergrund- und
// Vorschaubilder von TMDB. Alles wird nur angelegt, nie überschrieben.

type nfoID struct {
	Type    string `xml:"type,attr"`
	Default bool   `xml:"default,attr,omitempty"`
	ID      string `xml:",chardata"`
}

type nfoMovie struct {
	XMLName       xml.Name `xml:"movie"`
	Title         string   `xml:"title"`
	OriginalTitle string   `xml:"originaltitle,omitempty"`
	Year          int      `xml:"year,omitempty"`
	Premiered     string   `xml:"premiered,omitempty"`
	Plot          string   `xml:"plot,omitempty"`
	Runtime       int      `xml:"runtime,omitempty"`
	Genres        []string `xml:"genre"`
	IDs           []nfoID  `xml:"uniqueid"`
}

type nfoShow struct {
	XMLName       xml.Name `xml:"tvshow"`
	Title         string   `xml:"title"`
	OriginalTitle string   `xml:"originaltitle,omitempty"`
	Year          int      `xml:"year,omitempty"`
	Premiered     string   `xml:"premiered,omitempty"`
	Plot          string   `xml:"plot,omitempty"`
	Genres        []string `xml:"genre"`
	IDs           []nfoID  `xml:"uniqueid"`
}

type nfoEpisode struct {
	XMLName   xml.Name `xml:"episodedetails"`
	Title     string   `xml:"title"`
	ShowTitle string   `xml:"showtitle,omitempty"`
	Season    int      `xml:"season"`
	Episode   int      `xml:"episode"`
	Aired     string   `xml:"aired,omitempty"`
	Plot      string   `xml:"plot,omitempty"`
	IDs       []nfoID  `xml:"uniqueid"`
}

func genres(m Meta) []string {
	var out []string
	for _, g := range m.Genres {
		out = append(out, g.Name)
	}
	return out
}

func metaIDs(m Meta) []nfoID {
	ids := []nfoID{{Type: "tmdb", Default: true, ID: strconv.Itoa(m.ID)}}
	if m.ExternalIDs.IMDBID != "" {
		ids = append(ids, nfoID{Type: "imdb", ID: m.ExternalIDs.IMDBID})
	}
	if m.ExternalIDs.TVDBID > 0 {
		ids = append(ids, nfoID{Type: "tvdb", ID: strconv.Itoa(m.ExternalIDs.TVDBID)})
	}
	return ids
}

func movieNFO(info MediaInfo, m Meta) nfoMovie {
	return nfoMovie{
		Title: info.Title, OriginalTitle: info.OriginalTitle, Year: info.Year,
		Premiered: m.ReleaseDate, Plot: m.Overview, Runtime: m.Runtime,
		Genres: genres(m), IDs: metaIDs(m),
	}
}

func showNFO(info MediaInfo, m Meta) nfoShow {
	return nfoShow{
		Title: info.Title, OriginalTitle: info.OriginalTitle, Year: info.Year,
		Premiered: m.FirstAirDate, Plot: m.Overview, Genres: genres(m), IDs: metaIDs(m),
	}
}

func episodeNFO(info MediaInfo, ep Episode) nfoEpisode {
	n := nfoEpisode{
		Title: info.EpisodeTitle, ShowTitle: info.Title, Season: info.Season, Episode: info.Episode,
		Aired: ep.AirDate, Plot: ep.Overview,
	}
	if n.Title == "" {
		n.Title = ep.Name
	}
	if ep.ID > 0 {
		n.IDs = []nfoID{{Type: "tmdb", Default: true, ID: strconv.Itoa(ep.ID)}}
	}
	return n
}

// titleDir liefert den nach dem Titel benannten Ordner einer Vorlage
// (relativ zum Zielordner), also den Film- bzw. Serienordner. first wählt bei
// mehreren Kandidaten den obersten (Serie), sonst den untersten (Film).
// last meldet, ob die Datei direkt in diesem Ordner liegt.
func titleDir(tmpl string, info MediaInfo, first bool) (rel string, last, ok bool) {
	segs := strings.Split(tmpl, "/")
	dirs := segs[:len(segs)-1]
	idx := -1
	for i, s := range dirs {
		if strings.Contains(s, "{title") || strings.Contains(s, "{original_title") {
			idx = i
			if first {
				break
			}
		}
	}
	if idx < 0 {
		return "", false, false
	}
	rel = RenderTemplate(strings.Join(dirs[:idx+1], "/"), info)
	return rel, idx == len(dirs)-1, rel != ""
}

// extraWriter legt Dateien im Zielordner an und merkt sich, was neu ist.
type extraWriter struct {
	ctx     context.Context
	db      *TMDB
	dst     string
	created []string
	warns   []string
}

func (w *extraWriter) warn(what string, err error) {
	w.warns = append(w.warns, what+": "+err.Error())
}

// write legt eine neue Datei an. Existiert sie schon, bleibt sie unangetastet.
func (w *extraWriter) write(path string, data []byte) {
	if err := insideReal(w.dst, path); err != nil {
		w.warn(filepath.Base(path), err)
		return
	}
	// #nosec G302 G304 -- Pfad liegt im Zielordner (geprüft), Mediaserver müssen die Datei lesen können
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, os.ErrExist) {
		return
	}
	if err != nil {
		w.warn(filepath.Base(path), err)
		return
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path)
		w.warn(filepath.Base(path), err)
		return
	}
	w.created = append(w.created, path)
}

func (w *extraWriter) nfo(path string, v any) {
	data, err := xml.MarshalIndent(v, "", "  ")
	if err != nil {
		w.warn(filepath.Base(path), err)
		return
	}
	w.write(path, append([]byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?>`+"\n"), append(data, '\n')...))
}

// image lädt ein TMDB-Bild nach base + Endung, sofern es dort noch keins gibt.
func (w *extraWriter) image(base, tmdbPath string) {
	if tmdbPath == "" {
		return
	}
	ext := strings.ToLower(filepath.Ext(tmdbPath))
	if ext != ".png" {
		ext = ".jpg"
	}
	path := base + ext
	if exists(path) {
		return
	}
	data, err := w.db.Image(w.ctx, tmdbPath)
	if err != nil {
		w.warn(filepath.Base(path), err)
		return
	}
	w.write(path, data)
}

// WriteExtras legt nach dem Einsortieren NFO-Dateien und Bilder für einen
// Eintrag an. Es liefert die neu angelegten Dateien (für den Verlauf) und
// Hinweise zu allem, was nicht geklappt hat. Ohne TMDB-Treffer passiert nichts.
func WriteExtras(ctx context.Context, cfg Config, db *TMDB, it *Item) (created, warns []string) {
	x := cfg.Extras
	if (!x.NFO && !x.Artwork) || it.Info.TMDBID == 0 || !db.Enabled() {
		return nil, nil
	}
	dst, _ := filepath.Abs(cfg.TargetDir)
	w := &extraWriter{ctx: ctx, db: db, dst: dst}
	meta, err := db.Meta(ctx, it.Info.Series, it.Info.TMDBID)
	if err != nil {
		w.warn(it.Info.Title, err)
		return nil, w.warns
	}
	base := strings.TrimSuffix(it.Target, filepath.Ext(it.Target))

	if !it.Info.Series {
		// Mehrteilige Filme teilen sich NFO und Bilder: Name ohne Teilangabe.
		if it.Info.Part > 0 {
			whole := it.Info
			whole.Part = 0
			base = filepath.Join(dst, RenderTemplate(cfg.MovieTemplate, whole))
		}
		if x.NFO {
			w.nfo(base+".nfo", movieNFO(it.Info, meta))
		}
		if x.Artwork {
			// Eigener Filmordner: poster.jpg/fanart.jpg verstehen alle Mediaserver.
			// Liegen mehrere Filme in einem Ordner, bekommen die Bilder den Filmnamen.
			poster, fanart := base+"-poster", base+"-fanart"
			if rel, last, ok := titleDir(cfg.MovieTemplate, it.Info, false); ok && last && filepath.Join(dst, rel) == filepath.Dir(it.Target) {
				dir := filepath.Dir(it.Target)
				poster, fanart = filepath.Join(dir, "poster"), filepath.Join(dir, "fanart")
			}
			w.image(poster, meta.PosterPath)
			w.image(fanart, meta.BackdropPath)
		}
		return w.created, w.warns
	}

	// Serie: Serien-Dateien in den Serienordner, Folgen-Dateien neben die Folge.
	if rel, _, ok := titleDir(cfg.SeriesTemplate, it.Info, true); ok {
		show := filepath.Join(dst, rel)
		if x.NFO {
			w.nfo(filepath.Join(show, "tvshow.nfo"), showNFO(it.Info, meta))
		}
		if x.Artwork {
			w.image(filepath.Join(show, "poster"), meta.PosterPath)
			w.image(filepath.Join(show, "fanart"), meta.BackdropPath)
			name := fmt.Sprintf("season%02d-poster", it.Info.Season)
			if it.Info.Season == 0 {
				name = "season-specials-poster"
			}
			if p, err := db.SeasonPoster(ctx, it.Info.TMDBID, it.Info.Season); err == nil {
				w.image(filepath.Join(show, name), p)
			}
		}
	}
	ep, err := db.Episode(ctx, it.Info.TMDBID, it.Info.Season, it.Info.Episode)
	if err != nil && !errors.Is(err, errNotFound) {
		w.warn(filepath.Base(it.Target), err)
	}
	if x.NFO {
		w.nfo(base+".nfo", episodeNFO(it.Info, ep))
	}
	if x.Artwork {
		w.image(base+"-thumb", ep.StillPath)
	}
	return w.created, w.warns
}
