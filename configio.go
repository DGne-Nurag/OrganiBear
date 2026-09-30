package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Einstellungen sichern und wieder einlesen. Seit sie im Benutzerordner liegen,
// findet man sie nicht mehr neben dem Programm. Mit Export und Import lassen sie
// sich trotzdem auf einen anderen Rechner oder in eine Sicherung mitnehmen.

const exportName = "OrganiBear-Einstellungen"

// exportConfig schreibt die gespeicherten Einstellungen in den gewählten Ordner.
// Eine vorhandene Datei wird nie überschrieben, sondern durchnummeriert.
func (s *Server) exportConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if st, err := os.Stat(req.Dir); err != nil || !st.IsDir() || !filepath.IsAbs(req.Dir) {
		writeErr(w, http.StatusBadRequest, errors.New("diesen Ordner gibt es nicht"))
		return
	}
	s.mu.Lock()
	data, err := json.MarshalIndent(s.cfg, "", "  ")
	s.mu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	for i := 1; i < 100; i++ {
		name := exportName + ".json"
		if i > 1 {
			name = fmt.Sprintf("%s (%d).json", exportName, i)
		}
		p := filepath.Join(req.Dir, name)
		// Die Datei enthält Schlüssel und Passwörter, also nur für den Benutzer lesbar.
		f, err := os.OpenFile(p, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) // #nosec G304 -- vom Benutzer gewählter Ordner, O_EXCL überschreibt nichts
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		if _, err := f.Write(append(data, '\n')); err != nil {
			_ = f.Close()
			_ = os.Remove(p)
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if err := f.Close(); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, map[string]string{"path": p})
		return
	}
	writeErr(w, http.StatusConflict, errors.New("in diesem Ordner liegen schon zu viele Sicherungen"))
}

// findExport sucht im Ordner die neueste exportierte Einstellungsdatei.
func findExport(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	type cand struct {
		path string
		mod  int64
	}
	var found []cand
	for _, e := range entries {
		n := e.Name()
		if !e.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(n), ".json") {
			continue
		}
		if !strings.HasPrefix(n, exportName) && n != configName {
			continue
		}
		if info, err := e.Info(); err == nil {
			found = append(found, cand{filepath.Join(dir, n), info.ModTime().UnixNano()})
		}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("in diesem Ordner liegt keine Datei %s.json", exportName)
	}
	sort.Slice(found, func(i, j int) bool { return found[i].mod > found[j].mod })
	return found[0].path, nil
}

// importConfig liest eine exportierte Datei ein und speichert sie als neue
// Einstellungen. Die bisherigen landen vorher als Sicherung im Einstellungsordner.
func (s *Server) importConfig(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Dir string `json:"dir"`
	}
	if err := readJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p, err := findExport(req.Dir)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := os.Stat(p)
	if err != nil || st.Size() > 1<<20 {
		writeErr(w, http.StatusBadRequest, errors.New("das ist keine Einstellungsdatei von OrganiBear"))
		return
	}
	if !looksLikeConfig(p) {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s ist keine Einstellungsdatei von OrganiBear", filepath.Base(p)))
		return
	}
	cfg, err := LoadConfig(p)
	if err == nil {
		for _, t := range []string{cfg.MovieTemplate, cfg.SeriesTemplate} {
			if err = CheckTemplate(t); err != nil {
				break
			}
		}
	}
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Errorf("%s passt nicht: %w", filepath.Base(p), err))
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	backup := filepath.Join(filepath.Dir(s.cfgPath), "organibear-vorher.json")
	if err := SaveConfig(backup, s.cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if err := s.setConfig(cfg); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, map[string]any{"config": s.cfg, "from": p, "backup": backup})
}

// looksLikeConfig schützt davor, fremde JSON-Dateien als leere Einstellungen
// einzulesen: ohne Namensvorlage ist es keine Datei von OrganiBear.
func looksLikeConfig(p string) bool {
	data, err := os.ReadFile(p) // #nosec G304 -- Datei aus dem vom Benutzer gewählten Ordner
	if err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(data, &m) != nil {
		return false
	}
	_, ok := m["movie_template"]
	return ok
}
