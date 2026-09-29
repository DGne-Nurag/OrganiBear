package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var errExists = errors.New("Ziel existiert bereits")

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// moveFile verschiebt eine Datei, ohne je etwas zu überschreiben. Klappt das
// Umbenennen nicht (z. B. über Laufwerksgrenzen), wird kopiert und danach gelöscht.
func moveFile(src, dst string) error {
	if exists(dst) {
		return errExists
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Rename(src, dst); err == nil {
		return nil
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		os.Remove(dst) // Original bleibt, also keine halbe Doppelung zurücklassen
		return err
	}
	return nil
}

// copyFile kopiert eine Datei samt Änderungszeit. Existiert das Ziel, schlägt sie fehl.
func copyFile(src, dst string) (err error) {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm()|0o200)
	if errors.Is(err, os.ErrExist) {
		return errExists
	}
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			os.Remove(dst)
		}
	}()
	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	if err = out.Sync(); err != nil {
		return err
	}
	return os.Chtimes(dst, st.ModTime(), st.ModTime())
}

// runOp führt eine einzelne Dateioperation aus.
func runOp(op FileOp) error {
	switch op.Action {
	case ActionMove:
		return moveFile(op.Source, op.Target)
	case ActionCopy:
		return copyFile(op.Source, op.Target)
	}
	return fmt.Errorf("unbekannte Aktion %q", op.Action)
}

// within meldet, ob p innerhalb von root liegt (oder root selbst ist).
func within(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// pruneEmpty entfernt leere Ordner von dir aufwärts bis ausschließlich root.
func pruneEmpty(root, dir string) {
	for dir != root && within(root, dir) {
		if err := os.Remove(dir); err != nil { // schlägt fehl, wenn nicht leer
			return
		}
		dir = filepath.Dir(dir)
	}
}
