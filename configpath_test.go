package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveConfigPath(t *testing.T) {
	t.Run("fester Ort vorhanden", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "user", configName)
		old := filepath.Join(dir, "exe", configName)
		writeTestFile(t, target, `{"language":"de-DE"}`)
		writeTestFile(t, old, `{"language":"en-US"}`)
		path, from := resolveConfigPath(target, []string{old})
		if path != target || from != "" {
			t.Fatalf("got %q from %q", path, from)
		}
	})

	t.Run("alte Einstellungen neben dem Programm werden übernommen", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "user", "OrganiBear", configName)
		old := filepath.Join(dir, "exe", configName)
		writeTestFile(t, old, `{"tmdb_api_key":"abc"}`)
		writeTestFile(t, filepath.Join(dir, "exe", journalName, "1.json"), `[]`)
		path, from := resolveConfigPath(target, []string{filepath.Join(dir, "nix", configName), old})
		if path != target || from != old {
			t.Fatalf("got %q from %q", path, from)
		}
		if b, _ := os.ReadFile(target); string(b) != `{"tmdb_api_key":"abc"}` {
			t.Fatalf("Einstellungen nicht kopiert: %q", b)
		}
		if _, err := os.Stat(filepath.Join(dir, "user", "OrganiBear", journalName, "1.json")); err != nil {
			t.Fatalf("Verlauf nicht kopiert: %v", err)
		}
		if _, err := os.Stat(old); err != nil {
			t.Fatalf("alte Datei darf bleiben: %v", err)
		}
		// Zweiter Start: nichts mehr zu übernehmen.
		if _, from := resolveConfigPath(target, []string{old}); from != "" {
			t.Fatalf("zweimal übernommen von %q", from)
		}
	})

	t.Run("nichts vorhanden", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "user", configName)
		path, from := resolveConfigPath(target, []string{filepath.Join(dir, "exe", configName)})
		if path != target || from != "" {
			t.Fatalf("got %q from %q", path, from)
		}
	})
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
