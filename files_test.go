package main

import (
	"context"
	"path/filepath"
	"testing"
)

func TestFilesRoot(t *testing.T) {
	root, err := FilesRoot([]string{"/a/b/c/x.mkv", "/a/b/d/y.mkv", "/a/b/z.mkv"})
	if err != nil || root != filepath.FromSlash("/a/b") {
		t.Errorf("root = %q, %v", root, err)
	}
	if root, _ := FilesRoot([]string{"/a/b/x.mkv"}); root != filepath.FromSlash("/a/b") {
		t.Errorf("eine Datei: %q", root)
	}
	if _, err := FilesRoot(nil); err == nil {
		t.Error("leere Liste akzeptiert")
	}
}

// Nur die gewählten Videos kommen in die Liste. Ihre Untertitel wandern mit,
// die der nicht gewählten Nachbarn bleiben liegen.
func TestScanFiles(t *testing.T) {
	tmp := t.TempDir()
	src := filepath.Join(tmp, "Downloads")
	touch(t, src,
		"Breaking.Bad.S01E03.720p.mkv",
		"Breaking.Bad.S01E03.720p.de.srt",
		"Amelie.2001.mkv",
		"Amelie.2001.de.srt",
		"The.Matrix.1999.1080p-GRP/The.Matrix.1999.1080p-GRP.mkv",
		"The.Matrix.1999.1080p-GRP/Subs/English.srt",
		"Anderes/Inception.2010.mkv",
	)
	cfg := DefaultConfig()
	cfg.TargetDir, cfg.TMDBKey = filepath.Join(tmp, "Bibliothek"), "testkey"
	cfg.Files = []string{
		filepath.Join(src, "Breaking.Bad.S01E03.720p.mkv"),
		filepath.Join(src, "The.Matrix.1999.1080p-GRP/The.Matrix.1999.1080p-GRP.mkv"),
	}
	root, err := FilesRoot(cfg.Files)
	if err != nil || root != src {
		t.Fatalf("root %q %v", root, err)
	}
	cfg.SourceDir = root
	items, err := Scan(context.Background(), cfg, fakeTMDB(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 {
		t.Fatalf("%d Einträge, erwartet 2", len(items))
	}
	bb := itemFor(items, "Breaking.Bad.S01E03.720p.mkv")
	if bb == nil || len(bb.Companions) != 1 || filepath.Base(bb.Companions[0].Source) != "Breaking.Bad.S01E03.720p.de.srt" {
		t.Fatalf("Breaking Bad: %+v", bb)
	}
	m := itemFor(items, filepath.Join("The.Matrix.1999.1080p-GRP", "The.Matrix.1999.1080p-GRP.mkv"))
	if m == nil || len(m.Companions) != 1 {
		t.Fatalf("Matrix: Untertitel aus Subs fehlt: %+v", m)
	}

	// „Nur umbenennen“: Die Bibliothek entsteht im gemeinsamen Ordner.
	cfg.InPlace = true
	PlanTargets(cfg, items)
	ids := map[int]bool{bb.ID: true, m.ID: true}
	_, jpath, err := Apply(cfg, items, ids, filepath.Join(tmp, "verlauf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	mustExist(t, filepath.Join(src, "Serien/Breaking Bad (2008)/Staffel 01/Breaking Bad - S01E03 - ...und der Leichensack.mkv"), true)
	mustExist(t, filepath.Join(src, "Filme/Matrix (1999)/Matrix (1999).English.srt"), true)
	mustExist(t, filepath.Join(src, "Amelie.2001.de.srt"), true)
	mustExist(t, filepath.Join(src, "Anderes/Inception.2010.mkv"), true)
	mustExist(t, filepath.Join(tmp, "Bibliothek"), false)

	if problems, err := Undo(jpath); err != nil || len(problems) > 0 {
		t.Fatalf("Undo: %v %v", err, problems)
	}
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p.mkv"), true)
	mustExist(t, filepath.Join(src, "The.Matrix.1999.1080p-GRP/Subs/English.srt"), true)
}
