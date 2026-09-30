package main

import (
	"io"
	"os"
	"path/filepath"
)

// Wo die Einstellungen liegen. Früher lagen sie neben dem Programm. Wer eine
// neue Version in einen anderen Ordner lädt, stand dann ohne Einstellungen da.
// Jetzt liegen sie im Benutzerordner des Systems, egal wo das Programm liegt:
//
//	Windows: %AppData%\OrganiBear
//	macOS:   ~/Library/Application Support/OrganiBear
//	Linux:   ~/.config/OrganiBear
const (
	configName  = "organibear.json"
	journalName = "organibear-verlauf"
)

// defaultConfigPath liefert den festen Ort der Einstellungen. Gibt es dort noch
// keine, werden alte Einstellungen (neben dem Programm oder im Download-Ordner)
// samt Verlauf dorthin kopiert. Die alten Dateien bleiben unangetastet.
func defaultConfigPath() (path, migratedFrom string) {
	var legacy []string
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		legacy = append(legacy, filepath.Join(filepath.Dir(exe), configName))
	}
	home, _ := os.UserHomeDir()
	if home != "" {
		legacy = append(legacy, filepath.Join(home, "Downloads", configName))
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		// Kein Benutzerordner (sehr ungewöhnlich): wie früher neben dem Programm.
		if len(legacy) > 0 {
			return legacy[0], ""
		}
		return configName, ""
	}
	return resolveConfigPath(filepath.Join(dir, "OrganiBear", configName), legacy)
}

func resolveConfigPath(target string, legacy []string) (path, migratedFrom string) {
	if _, err := os.Stat(target); err == nil {
		return target, ""
	}
	for _, old := range legacy {
		if old == target {
			continue
		}
		if _, err := os.Stat(old); err != nil {
			continue
		}
		if err := migrateConfig(old, target); err != nil {
			// Kopieren ging nicht: lieber die alten Einstellungen weiter nutzen.
			return old, ""
		}
		return target, old
	}
	return target, ""
}

// migrateConfig kopiert Einstellungen und Verlauf an den neuen Ort.
func migrateConfig(oldCfg, newCfg string) error {
	if err := os.MkdirAll(filepath.Dir(newCfg), 0o700); err != nil {
		return err
	}
	if err := copyPrivate(oldCfg, newCfg, 0o600); err != nil {
		return err
	}
	oldJ := filepath.Join(filepath.Dir(oldCfg), journalName)
	newJ := filepath.Join(filepath.Dir(newCfg), journalName)
	entries, err := os.ReadDir(oldJ)
	if err != nil || os.MkdirAll(newJ, 0o700) != nil {
		return nil // kein oder unlesbarer Verlauf: die Einstellungen sind trotzdem da
	}
	for _, e := range entries {
		if e.Type().IsRegular() {
			_ = copyPrivate(filepath.Join(oldJ, e.Name()), filepath.Join(newJ, e.Name()), 0o600)
		}
	}
	return nil
}

func copyPrivate(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src) // #nosec G304 -- eigene Einstellungsdatei an bekanntem Ort
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm) // #nosec G304 -- Ziel im eigenen Einstellungsordner
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(dst)
		return err
	}
	return out.Close()
}
