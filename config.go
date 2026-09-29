package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
