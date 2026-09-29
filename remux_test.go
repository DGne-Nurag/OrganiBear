package main

import (
	"bytes"
	"context"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Die Beispieldateien in testdata/remux sind mit ffmpeg erzeugt (Testbild und Sinuston).
func TestRemux(t *testing.T) {
	for _, c := range []struct {
		file, vcodec string
		audio        []string
		subs         bool
	}{
		{"h264aac.mp4", "H.264", []string{"AAC"}, false},
		{"hevcac3.mp4", "HEVC", []string{"AC3"}, false},
		{"subs.mp4", "H.264", []string{"EAC3"}, true},
		{"av1mp3.mp4", "AV1", []string{"MP3"}, false},
	} {
		t.Run(c.file, func(t *testing.T) {
			src := filepath.Join("testdata", "remux", c.file)
			if err := canRemux(src); err != nil {
				t.Fatal(err)
			}
			dst := filepath.Join(t.TempDir(), "out.mkv")
			if err := remuxFile(src, dst); err != nil {
				t.Fatal(err)
			}
			m := ProbeFile(dst)
			if m == nil {
				t.Fatal("MKV nicht lesbar")
			}
			if m.VCodec != c.vcodec || m.Width != 160 || m.Height != 90 {
				t.Errorf("Video: %+v", m)
			}
			if len(m.Audio) != len(c.audio) {
				t.Fatalf("Tonspuren: %+v", m.Audio)
			}
			for i, a := range c.audio {
				if !strings.HasPrefix(m.Audio[i].Codec, a) {
					t.Errorf("Ton %d: %+v, erwartet %s", i, m.Audio[i], a)
				}
			}
			if c.file == "h264aac.mp4" && m.Audio[0].Lang != "de" {
				t.Errorf("Sprache: %q", m.Audio[0].Lang)
			}
			// Mit ffmpeg zusätzlich vollständig dekodieren, wenn vorhanden.
			ff, err := exec.LookPath("ffmpeg")
			if err != nil {
				ff = os.Getenv("OB_FFMPEG")
			}
			if ff == "" {
				return
			}
			// Jedes dekodierte Bild und jeder Tonblock muss mit dem Original übereinstimmen.
			frames := func(p string) []string {
				out, err := exec.Command(ff, "-v", "error", "-i", p, "-map", "0:v", "-map", "0:a", "-f", "framemd5", "-").CombinedOutput() // #nosec G204 -- Test
				if err != nil {
					t.Fatalf("ffmpeg %s: %v\n%s", p, err, out)
				}
				var lines []string
				for _, l := range strings.Split(string(out), "\n") {
					if f := strings.Split(l, ","); len(f) == 6 && !strings.HasPrefix(l, "#") {
						lines = append(lines, strings.TrimSpace(f[0])+" "+strings.TrimSpace(f[5]))
					}
				}
				return lines
			}
			// Video muss exakt gleich sein. Beim Ton blendet MP4 über die
			// Edit-Liste die Anlaufproben des Encoders aus (wenige Millisekunden).
			// MKV spielt sie mit ab, daher darf der Anfang abweichen.
			a, b := frames(src), frames(dst)
			for _, st := range []string{"0", "1"} {
				x, y := stream(a, st), stream(b, st)
				if st == "1" && len(x) > 2 {
					// Anfang in der MKV suchen, dann gleich lang vergleichen.
					x = x[1:]
					k := 0
					for k < 3 && k < len(y) && y[k] != x[0] {
						k++
					}
					y = y[min(k, len(y)):]
					if len(y) > len(x) {
						y = y[:len(x)]
					}
				}
				if len(x) == 0 || strings.Join(x, " ") != strings.Join(y, " ") {
					t.Fatalf("Spur %s weicht ab (%d/%d Blöcke)", st, len(x), len(y))
				}
			}
			if c.subs {
				srt, err := exec.Command(ff, "-v", "error", "-i", dst, "-map", "0:s", "-f", "srt", "-").CombinedOutput() // #nosec G204 -- Test
				if err != nil || !regexp.MustCompile(`00:00:00,5[0-3]\d --> 00:00:01,5[0-3]\d\nHallo Bär`).Match(srt) || !strings.Contains(string(srt), "Tschüss") {
					t.Errorf("Untertitel: %v\n%s", err, srt)
				}
			}
			info, _ := exec.Command(ff, "-hide_banner", "-i", dst).CombinedOutput() // #nosec G204 -- Test
			if c.subs && !strings.Contains(string(info), "Subtitle: subrip") {
				t.Errorf("keine Untertitel:\n%s", info)
			}
			if !strings.Contains(string(info), "Duration: 00:00:03") && !strings.Contains(string(info), "Duration: 00:00:02.9") {
				t.Errorf("Dauer falsch:\n%s", info)
			}
		})
	}
}

func TestRemuxRejects(t *testing.T) {
	dir := t.TempDir()
	junk := filepath.Join(dir, "x.mp4")
	if err := os.WriteFile(junk, []byte("kein Video"), 0o600); err != nil {
		t.Fatal(err)
	}
	if canRemux(junk) == nil {
		t.Error("Unsinn wurde akzeptiert")
	}
	dst := filepath.Join(dir, "x.mkv")
	if remuxFile(junk, dst) == nil {
		t.Error("Unsinn umgepackt")
	}
	if _, err := os.Stat(dst); !os.IsNotExist(err) {
		t.Error("halbe Datei liegen geblieben")
	}
	// Ziel existiert schon: nichts überschreiben.
	if err := os.WriteFile(dst, []byte("alt"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := remuxFile(filepath.Join("testdata", "remux", "h264aac.mp4"), dst); err != errExists {
		t.Errorf("err = %v", err)
	}
	if b, _ := os.ReadFile(dst); string(b) != "alt" { // #nosec G304 -- Test
		t.Error("vorhandene Datei überschrieben")
	}
}

func stream(lines []string, st string) []string {
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, st+" ") {
			out = append(out, l)
		}
	}
	return out
}

// Einsortieren mit Umpacken: MKV im Ziel, Original im Papierkorb, kaputte MP4
// bleiben MP4, und „Rückgängig“ stellt alles wieder her.
func TestRemuxApplyUndo(t *testing.T) {
	tmp := t.TempDir()
	src, dst := filepath.Join(tmp, "quelle"), filepath.Join(tmp, "ziel")
	touch(t, src, "Breaking.Bad.S01E03.720p.de.srt", "The.Matrix.1999.mp4")
	orig, err := os.ReadFile(filepath.Join("testdata", "remux", "subs.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "Breaking.Bad.S01E03.720p.mp4"), orig, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.SourceDir, cfg.TargetDir, cfg.TMDBKey, cfg.RemuxMP4 = src, dst, "testkey", true
	items, err := Scan(context.Background(), cfg, fakeTMDB(t))
	if err != nil {
		t.Fatal(err)
	}
	bb, matrix := itemFor(items, "Breaking.Bad.S01E03.720p.mp4"), itemFor(items, "The.Matrix.1999.mp4")
	if !bb.Remux || filepath.Ext(bb.Target) != ".mkv" {
		t.Fatalf("Breaking Bad: %+v", bb)
	}
	if matrix.Remux || filepath.Ext(matrix.Target) != ".mp4" || matrix.RemuxNote == "" {
		t.Fatalf("leere Datei darf nicht umgepackt werden: %+v", matrix)
	}
	ids := map[int]bool{bb.ID: true, matrix.ID: true}
	_, jpath, err := Apply(cfg, items, ids, filepath.Join(tmp, "verlauf"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ep := filepath.Join(dst, "Serien/Breaking Bad (2008)/Staffel 01/Breaking Bad - S01E03 - ...und der Leichensack")
	mustExist(t, ep+".mkv", true)
	mustExist(t, ep+".de.srt", true)
	mustExist(t, filepath.Join(dst, "Filme/Matrix (1999)/Matrix (1999).mp4"), true)
	mustExist(t, filepath.Join(dst, TrashDir, "Breaking.Bad.S01E03.720p.mp4"), true)
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p.mp4"), false)
	if m := ProbeFile(ep + ".mkv"); m == nil || m.VCodec != "H.264" {
		t.Errorf("MKV: %+v", m)
	}

	// Im Papierkorb wird nicht geschnüffelt, auch nicht bei „Nur umbenennen“.
	in := cfg
	in.SourceDir, in.InPlace = dst, true
	again, err := Scan(context.Background(), in, fakeTMDB(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range again {
		if strings.Contains(it.Source, TrashDir) {
			t.Errorf("Papierkorb gescannt: %s", it.RelSource)
		}
	}

	problems, err := Undo(jpath)
	if err != nil || len(problems) > 0 {
		t.Fatalf("Undo: %v %v", err, problems)
	}
	back, err := os.ReadFile(filepath.Join(src, "Breaking.Bad.S01E03.720p.mp4")) // #nosec G304 -- Test
	if err != nil || !bytes.Equal(back, orig) {
		t.Fatalf("Original nicht zurück: %v", err)
	}
	mustExist(t, ep+".mkv", false)
	mustExist(t, filepath.Join(dst, TrashDir), false)
	mustExist(t, filepath.Join(src, "Breaking.Bad.S01E03.720p.de.srt"), true)
}

// Kaputte Dateien dürfen nie zum Absturz führen.
func TestRemuxCorrupt(t *testing.T) {
	orig, err := os.ReadFile(filepath.Join("testdata", "remux", "subs.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rnd := rand.New(rand.NewPCG(1, 2)) // #nosec G404 -- Testdaten
	for i := 0; i < 300; i++ {
		data := append([]byte(nil), orig...)
		if i%3 == 0 {
			data = data[:rnd.IntN(len(data))]
		}
		for k := 0; k < 1+rnd.IntN(20); k++ {
			data[rnd.IntN(len(data))] = byte(rnd.IntN(256)) // #nosec G115 -- < 256
		}
		src, dst := filepath.Join(dir, "x.mp4"), filepath.Join(dir, "x.mkv")
		if err := os.WriteFile(src, data, 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Remove(dst)
		_ = canRemux(src)
		_ = remuxFile(src, dst)
	}
}
