package main

import (
	"image"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"
)

// Die Bilder für README und Tour entstehen mit e2e/screenshot.mjs. Dieser Test
// fängt kaputte Ergebnisse ab, z. B. einen Screenshot mit riesiger leerer Fläche.
func TestDocImages(t *testing.T) {
	size := func(p string) image.Point {
		f, err := os.Open(p) // #nosec G304 -- feste Pfade im Repo
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		c, _, err := image.DecodeConfig(f)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		return image.Pt(c.Width, c.Height)
	}
	// Die Vorschau mit 9 Beispielvideos ist etwa 1800 px hoch; 3000 px hieße leere Fläche.
	if s := size(filepath.Join("docs", "screenshot.png")); s.X != 1200 || s.Y < 1200 || s.Y > 2400 {
		t.Errorf("docs/screenshot.png ist %dx%d, erwartet 1200 breit und 1200 bis 2400 hoch", s.X, s.Y)
	}
	for i := 1; i <= 5; i++ {
		p := filepath.Join("web", "tour", string(rune('0'+i))+".jpg")
		if s := size(p); s != image.Pt(1100, 413) {
			t.Errorf("%s ist %dx%d, erwartet 1100x413", p, s.X, s.Y)
		}
	}
}
