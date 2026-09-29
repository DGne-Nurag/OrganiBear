package main

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestProbeFiles(t *testing.T) {
	cases := map[string]Media{
		"hevc-hdr10.mkv": {Width: 3840, Height: 1600, VCodec: "HEVC", HDR: "HDR10", Audio: []AudioTrack{
			{Lang: "de", Codec: "EAC3", Channels: 6}, {Lang: "en", Codec: "AAC", Channels: 2}}},
		"h264-scope.mp4": {Width: 1920, Height: 800, VCodec: "H.264", Audio: []AudioTrack{
			{Lang: "de", Codec: "AC3", Channels: 6, Default: true}, {Lang: "en", Codec: "AAC", Channels: 2}}},
		"av1.mkv": {Width: 1280, Height: 720, VCodec: "AV1", Audio: []AudioTrack{{Codec: "Opus", Channels: 2}}},
		"hevc-hlg.mp4": {Width: 1920, Height: 1080, VCodec: "HEVC", HDR: "HLG", Audio: []AudioTrack{
			{Lang: "en", Codec: "EAC3", Channels: 6, Default: true}}},
	}
	for name, want := range cases {
		got := ProbeFile(filepath.Join("testdata", "media", name))
		if got == nil || !reflect.DeepEqual(*got, want) {
			t.Errorf("%s:\n got %+v\nwant %+v", name, got, want)
		}
	}
	if m := ProbeFile(filepath.Join("testdata", "media", "dvd.avi")); m != nil {
		t.Errorf("AVI sollte auf den Dateinamen zurückfallen: %+v", m)
	}
}

func TestMediaStrings(t *testing.T) {
	m := ProbeFile(filepath.Join("testdata", "media", "hevc-hdr10.mkv"))
	if r, a, l := m.Resolution(), m.MainAudio().String(), m.Languages(); r != "2160p" || a != "EAC3 5.1" || l != "DE-EN" {
		t.Errorf("%s | %s | %s", r, a, l)
	}
	for wh, want := range map[[2]int]string{{1920, 800}: "1080p", {1280, 536}: "720p", {720, 576}: "576p", {640, 360}: "480p", {4096, 1716}: "2160p", {1440, 1080}: "1080p"} {
		if got := (&Media{Width: wh[0], Height: wh[1]}).Resolution(); got != want {
			t.Errorf("%v: %s, erwartet %s", wh, got, want)
		}
	}
	for in, want := range map[string]string{"ger": "de", "deu": "de", "en-US": "en", "und": "", "tlh": "tlh", "FR": "fr"} {
		if got := lang2(in); got != want {
			t.Errorf("lang2(%q) = %q", in, got)
		}
	}
}

// ebml baut ein EBML-Element (ID mit Markierung, Größe als 8-Byte-Zahl).
func ebml(id uint32, body ...[]byte) []byte {
	var b bytes.Buffer
	idb := binary.BigEndian.AppendUint32(nil, id)
	for len(idb) > 1 && idb[0] == 0 {
		idb = idb[1:]
	}
	b.Write(idb)
	data := bytes.Join(body, nil)
	size := binary.BigEndian.AppendUint64(nil, uint64(len(data)))
	size[0] = 0x01
	b.Write(size)
	b.Write(data)
	return b.Bytes()
}

func u(v uint64) []byte { return []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)} }

func TestProbeMKVDolbyVisionAndDefaults(t *testing.T) {
	video := ebml(mkvTrackEntry, ebml(mkvTrackType, u(1)), ebml(mkvCodecID, []byte("V_MPEGH/ISO/HEVC")),
		ebml(mkvBlockAddMap, ebml(mkvBlockAddType, []byte("dvvC"))),
		ebml(mkvVideo, ebml(mkvPixelWidth, u(3840)), ebml(mkvPixelHeight, u(2160))))
	// Ohne Language-Element gilt Englisch, ohne Channels ein Kanal.
	audio := ebml(mkvTrackEntry, ebml(mkvTrackType, u(2)), ebml(mkvCodecID, []byte("A_TRUEHD")), ebml(mkvAudio, ebml(mkvChannels, u(8))))
	audio2 := ebml(mkvTrackEntry, ebml(mkvTrackType, u(2)), ebml(mkvCodecID, []byte("A_DTS")), ebml(mkvLanguage, []byte("ger")),
		ebml(mkvLanguageBCP47, []byte("de-CH")))
	file := bytes.Join([][]byte{
		ebml(0x1A45DFA3, ebml(0x4282, []byte("matroska"))),
		// Segment mit unbekannter Größe, davor ein Info-Element zum Überspringen.
		{0x18, 0x53, 0x80, 0x67, 0x01, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF},
		ebml(0x1549A966, []byte("irgendwas")),
		ebml(mkvTracks, video, audio, audio2),
	}, nil)
	m, err := probe(bytes.NewReader(file))
	if err != nil {
		t.Fatal(err)
	}
	want := Media{Width: 3840, Height: 2160, VCodec: "HEVC", HDR: "DV", Audio: []AudioTrack{
		{Lang: "en", Codec: "TrueHD", Channels: 8, Default: true}, {Lang: "de", Codec: "DTS", Channels: 1, Default: true}}}
	if !reflect.DeepEqual(*m, want) {
		t.Errorf("\n got %+v\nwant %+v", *m, want)
	}
	if m.MainAudio().String() != "TrueHD 7.1" {
		t.Errorf("Haupttonspur %q", m.MainAudio().String())
	}
}

func TestAC3Channels(t *testing.T) {
	// dac3: fscod=0 bsid=8 bsmod=0 acmod=7 lfeon=1 -> 5.1
	if n := ac3Channels([]byte{0x10, 0x3C, 0x00}, false); n != 6 {
		t.Errorf("dac3: %d", n)
	}
	// dec3: 5.1 plus abhängiger Unterstrom mit Lrs/Rrs -> 7.1
	// data_rate(13)=0 num_ind_sub(3)=0 | fscod bsid(16) res asvc bsmod acmod(7) lfe(1) res(3) deps(1) chan_loc(9)=0b010000000
	bits := "0000000000000000" + "00" + "10000" + "0" + "0" + "000" + "111" + "1" + "000" + "0001" + "010000000"
	b := make([]byte, (len(bits)+7)/8)
	for i, c := range bits {
		if c == '1' {
			b[i/8] |= 1 << (7 - uint(i%8))
		}
	}
	if n := ac3Channels(b, true); n != 8 {
		t.Errorf("dec3: %d", n)
	}
}

func TestProbeGarbage(t *testing.T) {
	// Kaputte oder abgeschnittene Kopfdaten dürfen nichts kaputt machen.
	for _, name := range []string{"hevc-hdr10.mkv", "h264-scope.mp4", "hevc-hlg.mp4", "av1.mkv"} {
		data, err := os.ReadFile(filepath.Join("testdata", "media", name))
		if err != nil {
			t.Fatal(err)
		}
		for cut := 0; cut < len(data); cut += 97 {
			_, _ = probe(bytes.NewReader(data[:cut]))
			flipped := append([]byte(nil), data...)
			flipped[cut] ^= 0xFF
			_, _ = probe(bytes.NewReader(flipped))
		}
	}
	huge := []byte{0x1A, 0x45, 0xDF, 0xA3, 0x80, 0x18, 0x53, 0x80, 0x67, 0xFF, 0x16, 0x54, 0xAE, 0x6B, 0x08, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF}
	if _, err := probe(bytes.NewReader(huge)); err == nil || !strings.Contains(err.Error(), "groß") {
		t.Errorf("riesige Spurliste: %v", err)
	}
}

func FuzzProbe(f *testing.F) {
	for _, name := range []string{"hevc-hdr10.mkv", "h264-scope.mp4"} {
		data, _ := os.ReadFile(filepath.Join("testdata", "media", name))
		f.Add(data)
	}
	f.Fuzz(func(t *testing.T, data []byte) { _, _ = probe(bytes.NewReader(data)) })
}
