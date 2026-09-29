package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// Aktionen, die eine Dateiregel auslösen kann.
const (
	ActionMove = "move"
	ActionCopy = "copy"
	ActionSkip = "skip"
)

// Arten von Dateien: Videos sind die Hauptdateien, Begleitdateien
// (Untertitel, NFO, Bilder …) wandern mit ihrem Video mit.
const (
	KindVideo     = "video"
	KindCompanion = "companion"
)

// FileRule legt fest, was mit Dateien bestimmter Endungen passiert.
type FileRule struct {
	Name       string   `json:"name"`
	Kind       string   `json:"kind"`
	Extensions []string `json:"extensions"`
	Action     string   `json:"action"`
}

// Config ist die globale Konfiguration, gespeichert als JSON neben dem Programm.
type Config struct {
	TMDBKey        string     `json:"tmdb_api_key"`
	Language       string     `json:"language"`
	SourceDir      string     `json:"source_dir"`
	TargetDir      string     `json:"target_dir"`
	MovieTemplate  string     `json:"movie_template"`
	SeriesTemplate string     `json:"series_template"`
	FileRules      []FileRule `json:"file_rules"`
	IgnorePatterns []string   `json:"ignore_patterns"`
	Extras         Extras     `json:"extras"`
	Subtitles      Subtitles  `json:"subtitles"`
	TheTVDB        TheTVDB    `json:"thetvdb"`
	// GuideSkipped: Die Anleitung zum TMDB-Key wurde mit „Später“ weggeklickt
	// und öffnet sich beim Start nicht mehr von selbst.
	GuideSkipped bool `json:"tmdb_guide_skipped,omitempty"`
	// InPlace: „Nur umbenennen“. Die Bibliothek entsteht im Quellordner selbst,
	// TargetDir bleibt für später gemerkt.
	InPlace bool `json:"in_place,omitempty"`
}

// Target ist der Ordner, in den einsortiert wird.
func (c Config) Target() string {
	if c.InPlace {
		return c.SourceDir
	}
	return c.TargetDir
}

// TheTVDB ist die zweite Quelle für Serien, wenn TMDB nichts findet. Den
// Projekt-Key bringt das Programm mit.
type TheTVDB struct {
	Enabled bool   `json:"enabled"`
	Order   string `json:"order,omitempty"` // "default" (wie ausgestrahlt) oder "dvd"
}

// Subtitles legt fest, ob und in welchen Sprachen fehlende Untertitel von
// OpenSubtitles.com geladen werden. Den API-Key bringt das Programm mit, das
// Konto trägt jeder selbst ein.
type Subtitles struct {
	Enabled   bool     `json:"enabled"`
	Languages []string `json:"languages"`          // z. B. ["de", "en"]
	User      string   `json:"user,omitempty"`     // optional: mehr Downloads pro Tag
	Password  string   `json:"password,omitempty"` // optional
}

// Extras legt fest, was OrganiBear für Mediaserver zusätzlich erledigt.
type Extras struct {
	NFO      bool        `json:"nfo"`     // NFO-Dateien im Kodi-Format schreiben
	Artwork  bool        `json:"artwork"` // Poster, Hintergrund- und Vorschaubilder laden
	Plex     MediaServer `json:"plex"`
	Jellyfin MediaServer `json:"jellyfin"`
	Kodi     MediaServer `json:"kodi"`
}

// MediaServer sind die Zugangsdaten eines Mediaservers. Ohne URL ist er aus.
type MediaServer struct {
	URL      string `json:"url,omitempty"`
	Token    string `json:"token,omitempty"`    // Plex-Token bzw. Jellyfin-API-Key
	User     string `json:"user,omitempty"`     // nur Kodi
	Password string `json:"password,omitempty"` // nur Kodi
}

var reLang = regexp.MustCompile(`^[a-z]{2}(-[a-z]{2})?$`)

// normalizeServerURL prüft eine Mediaserver-Adresse und entfernt den Schrägstrich am Ende.
func normalizeServerURL(raw string) (string, error) {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return "", nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", errors.New("die Adresse muss mit http:// oder https:// beginnen, z. B. http://192.168.1.10:32400")
	}
	return raw, nil
}

func DefaultConfig() Config {
	return Config{
		Language:       "de-DE",
		MovieTemplate:  "Filme/{title} ({year})/{title} ({year})",
		SeriesTemplate: "Serien/{title} ({year})/Staffel {season:02}/{title} - S{season:02}E{episode:02} - {episode_title}",
		FileRules: []FileRule{
			{Name: "Videos", Kind: KindVideo, Extensions: []string{"mkv", "mp4", "avi", "m4v", "mov", "wmv", "ts", "m2ts", "mpg", "mpeg", "webm"}, Action: ActionMove},
			{Name: "Untertitel", Kind: KindCompanion, Extensions: []string{"srt", "sub", "idx", "ass", "ssa", "vtt", "sup"}, Action: ActionMove},
			{Name: "Infodateien", Kind: KindCompanion, Extensions: []string{"nfo"}, Action: ActionSkip},
			{Name: "Bilder", Kind: KindCompanion, Extensions: []string{"jpg", "jpeg", "png"}, Action: ActionSkip},
		},
		IgnorePatterns: []string{"*sample*", "*trailer*"},
		Subtitles:      Subtitles{Languages: []string{"de", "en"}},
	}
}

// Validate prüft die Konfiguration und normalisiert Endungen.
func (c *Config) Validate() error {
	if strings.TrimSpace(c.MovieTemplate) == "" || strings.TrimSpace(c.SeriesTemplate) == "" {
		return errors.New("die Vorlagen für Filme und Serien dürfen nicht leer sein")
	}
	seen := map[string]string{}
	for i := range c.FileRules {
		r := &c.FileRules[i]
		switch r.Kind {
		case KindVideo, KindCompanion:
		default:
			return fmt.Errorf("Regel %q: unbekannte Art %q", r.Name, r.Kind)
		}
		switch r.Action {
		case ActionMove, ActionCopy, ActionSkip:
		default:
			return fmt.Errorf("Regel %q: unbekannte Aktion %q", r.Name, r.Action)
		}
		var exts []string
		for _, e := range r.Extensions {
			e = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))
			if e == "" {
				continue
			}
			if other, ok := seen[e]; ok {
				return fmt.Errorf("Endung .%s steht in den Regeln %q und %q", e, other, r.Name)
			}
			seen[e] = r.Name
			exts = append(exts, e)
		}
		r.Extensions = exts
	}
	for _, s := range []struct {
		name string
		srv  *MediaServer
	}{{"Plex", &c.Extras.Plex}, {"Jellyfin", &c.Extras.Jellyfin}, {"Kodi", &c.Extras.Kodi}} {
		u, err := normalizeServerURL(s.srv.URL)
		if err != nil {
			return fmt.Errorf("%s: %w", s.name, err)
		}
		s.srv.URL = u
		s.srv.Token = strings.TrimSpace(s.srv.Token)
	}
	var langs []string
	for _, l := range c.Subtitles.Languages {
		l = strings.ToLower(strings.TrimSpace(l))
		if l == "" {
			continue
		}
		if !reLang.MatchString(l) {
			return fmt.Errorf("Untertitel: %q ist kein Sprachkürzel (z. B. de, en, pt-br)", l)
		}
		langs = append(langs, l)
	}
	c.Subtitles.Languages = langs
	c.Subtitles.User = strings.TrimSpace(c.Subtitles.User)
	if c.Subtitles.Enabled && len(langs) == 0 {
		return errors.New("Untertitel: bitte mindestens eine Sprache angeben")
	}
	switch c.TheTVDB.Order {
	case "", "default", "dvd":
	default:
		return errors.New("TheTVDB: unbekannte Folgenreihenfolge")
	}
	return nil
}

// RuleFor liefert die Regel für eine Datei oder nil, wenn keine passt.
func (c *Config) RuleFor(path string) *FileRule {
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(path), "."))
	if ext == "" {
		return nil
	}
	for i := range c.FileRules {
		for _, e := range c.FileRules[i].Extensions {
			if e == ext {
				return &c.FileRules[i]
			}
		}
	}
	return nil
}

// Ignored meldet, ob ein Dateiname auf ein Ignorier-Muster passt.
func (c *Config) Ignored(name string) bool {
	lower := strings.ToLower(name)
	for _, p := range c.IgnorePatterns {
		if ok, _ := filepath.Match(strings.ToLower(p), lower); ok {
			return true
		}
	}
	return false
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()
	data, err := os.ReadFile(path) // #nosec G304 -- Konfigurationspfad vom Benutzer per -config gewählt
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("%s: %w", path, err)
	}
	return cfg, cfg.Validate()
}

func SaveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	// Frische Temp-Datei mit 0600, damit der API-Key nicht lesbar für andere wird.
	f, err := os.CreateTemp(filepath.Dir(path), ".organibear-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err := f.Chmod(0o600); err != nil && runtime.GOOS != "windows" {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(append(data, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
