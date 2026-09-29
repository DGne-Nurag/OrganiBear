package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
)

var errExists = errors.New("Ziel existiert bereits")

func exists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// moveFile verschiebt eine Datei, ohne je etwas zu überschreiben.
//
// Zuerst wird ein Hardlink angelegt: das schlägt atomar fehl, wenn das Ziel
// existiert, anders als os.Rename, das still überschreiben würde. Kann das
// Dateisystem keine Hardlinks (z. B. FAT/exFAT), wird umbenannt. Nur über
// Laufwerksgrenzen hinweg wird kopiert und danach gelöscht.
func moveFile(src, dst string) error {
	if err := requireRegular(src); err != nil {
		return err
	}
	if exists(dst) {
		if caseOnly(src, dst) {
			return os.Rename(src, dst)
		}
		return errExists
	}
	// #nosec G301 -- Bibliotheksordner müssen für Mediaserver lesbar sein
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	err := os.Link(src, dst)
	switch {
	case err == nil:
		if err := os.Remove(src); err != nil {
			_ = os.Remove(dst)
			return err
		}
		return nil
	case errors.Is(err, fs.ErrExist):
		return errExists
	case !isCrossDevice(err):
		if exists(dst) {
			return errExists
		}
		if err = os.Rename(src, dst); !isCrossDevice(err) {
			return err
		}
	}
	if err := copyFile(src, dst); err != nil {
		return err
	}
	if err := os.Remove(src); err != nil {
		_ = os.Remove(dst) // Original bleibt, also keine halbe Doppelung zurücklassen
		return err
	}
	return nil
}

// caseOnly meldet, ob dst nur in der Groß-/Kleinschreibung von src abweicht und
// auf derselben Datei landet (Windows, macOS). Dann ist Umbenennen erlaubt.
func caseOnly(src, dst string) bool {
	if src == dst || !strings.EqualFold(src, dst) {
		return false
	}
	a, err1 := os.Stat(src)
	b, err2 := os.Stat(dst)
	return err1 == nil && err2 == nil && os.SameFile(a, b)
}

// isCrossDevice meldet, ob ein Fehler "anderes Laufwerk" bedeutet.
func isCrossDevice(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, syscall.EXDEV) {
		return true
	}
	var errno syscall.Errno
	// ERROR_NOT_SAME_DEVICE unter Windows
	return runtime.GOOS == "windows" && errors.As(err, &errno) && errno == 17
}

// requireRegular lehnt alles ab, was keine normale Datei ist (z. B. Symlinks,
// die nach dem Scan untergeschoben wurden).
func requireRegular(p string) error {
	st, err := os.Lstat(p)
	if err != nil {
		return err
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("%s ist keine normale Datei", filepath.Base(p))
	}
	return nil
}

// insideReal prüft nach dem Auflösen von Symlinks, ob der Ordner von p
// wirklich innerhalb von root liegt. Legt fehlende Ordner vorher an.
func insideReal(root, p string) error {
	dir := filepath.Dir(p)
	// #nosec G301 -- Bibliotheksordner müssen für Mediaserver lesbar sein
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return err
	}
	if !within(realRoot, realDir) {
		return errors.New("Ziel zeigt über einen Symlink aus dem Zielordner heraus")
	}
	return nil
}

// copyFile kopiert eine Datei samt Änderungszeit. Existiert das Ziel, schlägt sie fehl.
func copyFile(src, dst string) (err error) {
	// #nosec G301 -- Bibliotheksordner müssen für Mediaserver lesbar sein
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := requireRegular(src); err != nil {
		return err
	}
	in, err := os.Open(src) // #nosec G304 -- Quelldatei aus dem vom Benutzer gewählten Ordner, vorher als normale Datei geprüft
	if err != nil {
		return err
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, // #nosec G304 -- Ziel innerhalb des Zielordners, O_EXCL verhindert Überschreiben
		os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm()|0o200)
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
			_ = os.Remove(dst)
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
		if st, err := os.Lstat(dir); err != nil || !st.IsDir() {
			return // Symlinks und Fremdes nie anfassen
		}
		if err := os.Remove(dir); err != nil { // schlägt fehl, wenn nicht leer
			return
		}
		dir = filepath.Dir(dir)
	}
}
