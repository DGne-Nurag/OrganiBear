package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Technische Daten direkt aus der Videodatei, ohne ffprobe: kleine Leser für
// Matroska (MKV/WebM, EBML) und MP4/MOV (ISO-BMFF-Boxen). Gelesen werden nur
// die Kopfdaten der Spuren, nie der Film selbst. Andere Formate (AVI usw.)
// liefern nil, dann gilt, was im Dateinamen steht.

// Media beschreibt, was in der Datei steckt.
type Media struct {
	Width  int          `json:"width"`
	Height int          `json:"height"`
	VCodec string       `json:"vcodec,omitempty"` // H.264, HEVC, AV1 …
	HDR    string       `json:"hdr,omitempty"`    // DV, HDR10, HLG oder leer
	Audio  []AudioTrack `json:"audio,omitempty"`
}

// AudioTrack ist eine Tonspur.
type AudioTrack struct {
	Lang     string `json:"lang,omitempty"` // zweistellig, wenn bekannt (de, en)
	Codec    string `json:"codec,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Default  bool   `json:"default,omitempty"`
}

var errNoMedia = errors.New("keine Spurinformationen gefunden")

// ProbeFile liest die Spurinformationen einer Videodatei. Bei unbekanntem
// Format oder kaputten Kopfdaten kommt nil zurück.
func ProbeFile(path string) *Media {
	f, err := os.Open(path) // #nosec G304 -- Pfad stammt aus dem Scan des Quellordners
	if err != nil {
		return nil
	}
	defer f.Close()
	m, err := probe(f)
	if err != nil || m.Width == 0 && m.VCodec == "" && len(m.Audio) == 0 {
		return nil
	}
	return m
}

func probe(r io.ReadSeeker) (*Media, error) {
	var head [12]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	if _, err := r.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	switch {
	case bytes.Equal(head[:4], []byte{0x1A, 0x45, 0xDF, 0xA3}):
		return probeMKV(r)
	case isBoxType(head[4:8]):
		return probeMP4(r)
	}
	return nil, errors.New("unbekanntes Format")
}

func isBoxType(t []byte) bool {
	switch string(t) {
	case "ftyp", "moov", "mdat", "free", "skip", "wide", "pnot":
		return true
	}
	return false
}

// Resolution ordnet die Bildgröße einer üblichen Bezeichnung zu. Die Breite
// zählt mit, damit Kinoformate wie 1920×800 als 1080p gelten.
func (m *Media) Resolution() string {
	if m == nil || m.Width == 0 || m.Height == 0 {
		return ""
	}
	w, h := m.Width, m.Height
	switch {
	case w >= 3200 || h >= 1800:
		return "2160p"
	case w >= 1800 || h >= 1000:
		return "1080p"
	case w >= 1200 || h >= 700:
		return "720p"
	case h >= 540:
		return "576p"
	}
	return "480p"
}

// MainAudio ist die Standard-Tonspur, sonst die erste.
func (m *Media) MainAudio() *AudioTrack {
	if m == nil || len(m.Audio) == 0 {
		return nil
	}
	for i := range m.Audio {
		if m.Audio[i].Default {
			return &m.Audio[i]
		}
	}
	return &m.Audio[0]
}

// String beschreibt eine Tonspur, z. B. "TrueHD 7.1".
func (a *AudioTrack) String() string {
	if a == nil {
		return ""
	}
	return strings.TrimSpace(a.Codec + " " + channelLayout(a.Channels))
}

func channelLayout(n int) string {
	switch {
	case n <= 0:
		return ""
	case n >= 6:
		return fmt.Sprintf("%d.1", n-1)
	}
	return fmt.Sprintf("%d.0", n)
}

// Languages listet die Sprachen der Tonspuren ohne Wiederholung, z. B. "DE-EN".
func (m *Media) Languages() string {
	if m == nil {
		return ""
	}
	var out []string
	seen := map[string]bool{}
	for _, a := range m.Audio {
		l := strings.ToUpper(a.Lang)
		if l != "" && !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	return strings.Join(out, "-")
}

// lang2 macht aus ISO-639-2 ("ger", "deu") oder BCP 47 ("de-DE") ein
// zweistelliges Kürzel. Unbekanntes bleibt, wie es ist, "und" wird leer.
func lang2(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.IndexAny(s, "-_"); i > 0 {
		s = s[:i]
	}
	if s == "und" || s == "zxx" || s == "mul" || s == "" {
		return ""
	}
	if len(s) == 2 {
		return s
	}
	if l, ok := iso6392[s]; ok {
		return l
	}
	return s
}

var iso6392 = map[string]string{
	"ger": "de", "deu": "de", "eng": "en", "fre": "fr", "fra": "fr", "spa": "es", "ita": "it",
	"jpn": "ja", "kor": "ko", "chi": "zh", "zho": "zh", "rus": "ru", "por": "pt", "dut": "nl",
	"nld": "nl", "pol": "pl", "tur": "tr", "swe": "sv", "dan": "da", "nor": "no", "fin": "fi",
	"cze": "cs", "ces": "cs", "hun": "hu", "gre": "el", "ell": "el", "ara": "ar", "heb": "he",
	"hin": "hi", "tha": "th", "ukr": "uk", "rum": "ro", "ron": "ro", "hrv": "hr", "srp": "sr",
}

// readExactly liest n Bytes. Der Speicher wächst mit den tatsächlich
// gelesenen Daten, eine gefälschte Größenangabe kostet also nichts.
func readExactly(r io.Reader, n int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, n))
	if err == nil && int64(len(data)) < n {
		err = io.ErrUnexpectedEOF
	}
	return data, err
}

// ---------- Matroska ----------

const (
	mkvSegment       = 0x18538067
	mkvTracks        = 0x1654AE6B
	mkvCluster       = 0x1F43B675
	mkvTrackEntry    = 0xAE
	mkvTrackType     = 0x83
	mkvCodecID       = 0x86
	mkvLanguage      = 0x22B59C
	mkvLanguageBCP47 = 0x22B59D
	mkvFlagDefault   = 0x88
	mkvVideo         = 0xE0
	mkvPixelWidth    = 0xB0
	mkvPixelHeight   = 0xBA
	mkvColour        = 0x55B0
	mkvTransfer      = 0x55BA
	mkvAudio         = 0xE1
	mkvChannels      = 0x9F
	mkvBlockAddMap   = 0x41E4
	mkvBlockAddType  = 0x41E7
)

const unknownSize = -1

// readVint liest eine EBML-Zahl variabler Länge. Für IDs bleibt die
// Längenmarkierung stehen, für Größen wird sie entfernt.
func readVint(r io.Reader, keepMarker bool) (int64, int, error) {
	var b [8]byte
	if _, err := io.ReadFull(r, b[:1]); err != nil {
		return 0, 0, err
	}
	n := 1
	for mask := byte(0x80); n <= 8 && b[0]&mask == 0; mask >>= 1 {
		n++
	}
	if n > 8 {
		return 0, 0, errors.New("ungültige EBML-Zahl")
	}
	if n > 1 {
		if _, err := io.ReadFull(r, b[1:n]); err != nil {
			return 0, 0, err
		}
	}
	v := int64(b[0])
	if !keepMarker {
		v &= int64(0xFF >> n)
	}
	allOnes := v == int64(0xFF>>n)
	for i := 1; i < n; i++ {
		v = v<<8 | int64(b[i])
		allOnes = allOnes && b[i] == 0xFF
	}
	if !keepMarker && allOnes {
		return unknownSize, n, nil
	}
	return v, n, nil
}

func probeMKV(r io.ReadSeeker) (*Media, error) {
	// EBML-Kopf überspringen, dann ins Segment hinein.
	for top := 0; top < 8; top++ {
		id, _, err := readVint(r, true)
		if err != nil {
			return nil, err
		}
		size, _, err := readVint(r, false)
		if err != nil {
			return nil, err
		}
		if id == mkvSegment {
			return mkvSegmentTracks(r)
		}
		if size == unknownSize {
			break
		}
		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	return nil, errNoMedia
}

// mkvSegmentTracks sucht im Segment nach den Spuren. Sie stehen vor dem
// ersten Cluster, alles andere wird übersprungen.
func mkvSegmentTracks(r io.ReadSeeker) (*Media, error) {
	for i := 0; i < 64; i++ {
		id, _, err := readVint(r, true)
		if err != nil {
			return nil, err
		}
		size, _, err := readVint(r, false)
		if err != nil {
			return nil, err
		}
		switch {
		case id == mkvTracks:
			if size < 0 || size > 4<<20 {
				return nil, errors.New("Spurliste zu groß")
			}
			data, err := readExactly(r, size)
			if err != nil {
				return nil, err
			}
			return mkvParseTracks(data)
		case id == mkvCluster || size == unknownSize:
			return nil, errNoMedia
		}
		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	return nil, errNoMedia
}

// ebmlChildren ruft fn für jedes Kindelement in data auf.
func ebmlChildren(data []byte, fn func(id int64, body []byte)) {
	r := bytes.NewReader(data)
	for r.Len() > 0 {
		id, _, err := readVint(r, true)
		if err != nil {
			return
		}
		size, _, err := readVint(r, false)
		if err != nil || size < 0 || size > int64(r.Len()) {
			return
		}
		body := data[len(data)-r.Len():][:size]
		fn(id, body)
		_, _ = r.Seek(size, io.SeekCurrent)
	}
}

func ebmlUint(b []byte) uint64 {
	var v uint64
	for _, c := range b {
		v = v<<8 | uint64(c)
	}
	return v
}

// ebmlSmall liest kleine Zahlen wie Bildbreite oder Kanalzahl. Unsinnig
// große Werte werden zu 0.
func ebmlSmall(b []byte) int {
	v := ebmlUint(b)
	if len(b) > 4 || v > 1<<20 {
		return 0
	}
	return int(v) // #nosec G115 -- oben auf 2^20 begrenzt
}

func mkvParseTracks(data []byte) (*Media, error) {
	m := &Media{}
	ebmlChildren(data, func(id int64, entry []byte) {
		if id != mkvTrackEntry {
			return
		}
		var (
			typ          uint64
			codec, lang  string
			bcp47        string
			isDefault    = true // Matroska-Standardwert
			w, h, ch     int
			transfer     uint64
			dolbyVision  bool
			haveLanguage bool
		)
		ebmlChildren(entry, func(id int64, b []byte) {
			switch id {
			case mkvTrackType:
				typ = ebmlUint(b)
			case mkvCodecID:
				codec = strings.TrimRight(string(b), "\x00")
			case mkvLanguage:
				lang, haveLanguage = strings.TrimRight(string(b), "\x00"), true
			case mkvLanguageBCP47:
				bcp47 = strings.TrimRight(string(b), "\x00")
			case mkvFlagDefault:
				isDefault = ebmlUint(b) != 0
			case mkvVideo:
				ebmlChildren(b, func(id int64, v []byte) {
					switch id {
					case mkvPixelWidth:
						w = ebmlSmall(v)
					case mkvPixelHeight:
						h = ebmlSmall(v)
					case mkvColour:
						ebmlChildren(v, func(id int64, c []byte) {
							if id == mkvTransfer {
								transfer = ebmlUint(c)
							}
						})
					}
				})
			case mkvAudio:
				ebmlChildren(b, func(id int64, a []byte) {
					if id == mkvChannels {
						ch = ebmlSmall(a)
					}
				})
			case mkvBlockAddMap:
				ebmlChildren(b, func(id int64, a []byte) {
					if id == mkvBlockAddType {
						switch string(a) { // vier Buchstaben als Zahl
						case "dvcC", "dvvC", "dvwC":
							dolbyVision = true
						}
					}
				})
			}
		})
		switch typ {
		case 1: // Video, die erste zählt
			if m.VCodec != "" || m.Width != 0 {
				return
			}
			m.Width, m.Height = w, h
			m.VCodec = mkvVideoCodec(codec)
			m.HDR = hdrName(transfer, dolbyVision)
		case 2: // Audio
			if bcp47 != "" {
				lang = bcp47
			} else if !haveLanguage {
				lang = "eng" // Matroska-Standardwert
			}
			ch := ch
			if ch == 0 {
				ch = 1 // Matroska-Standardwert
			}
			m.Audio = append(m.Audio, AudioTrack{Lang: lang2(lang), Codec: mkvAudioCodec(codec), Channels: ch, Default: isDefault})
		}
	})
	return m, nil
}

func mkvVideoCodec(id string) string {
	switch {
	case id == "V_MPEG4/ISO/AVC":
		return "H.264"
	case id == "V_MPEGH/ISO/HEVC":
		return "HEVC"
	case id == "V_AV1":
		return "AV1"
	case id == "V_VP9":
		return "VP9"
	case id == "V_VP8":
		return "VP8"
	case strings.HasPrefix(id, "V_MPEG4/ISO/"):
		return "MPEG-4"
	case id == "V_MPEG2" || id == "V_MPEG1":
		return "MPEG-" + id[len(id)-1:]
	}
	return ""
}

func mkvAudioCodec(id string) string {
	switch {
	case strings.HasPrefix(id, "A_AAC"):
		return "AAC"
	case id == "A_AC3":
		return "AC3"
	case id == "A_EAC3":
		return "EAC3"
	case id == "A_TRUEHD":
		return "TrueHD"
	case strings.HasPrefix(id, "A_DTS"):
		return "DTS"
	case id == "A_FLAC":
		return "FLAC"
	case id == "A_OPUS":
		return "Opus"
	case id == "A_VORBIS":
		return "Vorbis"
	case id == "A_MPEG/L3":
		return "MP3"
	case id == "A_MPEG/L2":
		return "MP2"
	case strings.HasPrefix(id, "A_PCM"):
		return "PCM"
	}
	return ""
}

// hdrName übersetzt die Transferfunktion (ITU-T H.273) in einen Namen.
func hdrName(transfer uint64, dolbyVision bool) string {
	switch {
	case dolbyVision:
		return "DV"
	case transfer == 16:
		return "HDR10"
	case transfer == 18:
		return "HLG"
	}
	return ""
}

// ---------- MP4 / MOV ----------

type box struct {
	typ  string
	body []byte
}

// readBoxHeader liest Typ und Inhaltsgröße einer Box (-1: bis zum Dateiende).
func readBoxHeader(r io.Reader) (string, int64, error) {
	var h [8]byte
	if _, err := io.ReadFull(r, h[:]); err != nil {
		return "", 0, err
	}
	size := int64(binary.BigEndian.Uint32(h[:4]))
	typ := string(h[4:8])
	switch size {
	case 0:
		return typ, -1, nil
	case 1:
		var l [8]byte
		if _, err := io.ReadFull(r, l[:]); err != nil {
			return "", 0, err
		}
		size = int64(binary.BigEndian.Uint64(l[:])) - 16 // #nosec G115 -- Größe wird unten geprüft
	default:
		size -= 8
	}
	if size < 0 {
		return "", 0, errors.New("ungültige Boxgröße")
	}
	return typ, size, nil
}

func probeMP4(r io.ReadSeeker) (*Media, error) {
	// Die moov-Box kann vor oder hinter den Filmdaten (mdat) liegen.
	for i := 0; i < 64; i++ {
		typ, size, err := readBoxHeader(r)
		if err != nil {
			return nil, err
		}
		if typ == "moov" {
			if size < 0 || size > 64<<20 {
				return nil, errors.New("moov-Box zu groß")
			}
			data, err := readExactly(r, size)
			if err != nil {
				return nil, err
			}
			return mp4ParseMoov(data), nil
		}
		if size < 0 {
			break
		}
		if _, err := r.Seek(size, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	return nil, errNoMedia
}

// boxes zerlegt data in aufeinanderfolgende Boxen.
func boxes(data []byte) []box {
	var out []box
	r := bytes.NewReader(data)
	for r.Len() >= 8 {
		typ, size, err := readBoxHeader(r)
		if err != nil {
			break
		}
		if size < 0 || size > int64(r.Len()) {
			size = int64(r.Len())
		}
		start := len(data) - r.Len()
		out = append(out, box{typ, data[start : start+int(size)]})
		_, _ = r.Seek(size, io.SeekCurrent)
	}
	return out
}

func child(data []byte, path ...string) []byte {
	for _, p := range path {
		var next []byte
		found := false
		for _, b := range boxes(data) {
			if b.typ == p {
				next, found = b.body, true
				break
			}
		}
		if !found {
			return nil
		}
		data = next
	}
	return data
}

func mp4ParseMoov(moov []byte) *Media {
	m := &Media{}
	for _, trak := range boxes(moov) {
		if trak.typ != "trak" {
			continue
		}
		mdia := child(trak.body, "mdia")
		hdlr := child(mdia, "hdlr")
		if len(hdlr) < 12 {
			continue
		}
		stsd := child(mdia, "minf", "stbl", "stsd")
		if len(stsd) < 8 {
			continue
		}
		entries := boxes(stsd[8:])
		if len(entries) == 0 {
			continue
		}
		e := entries[0]
		switch string(hdlr[8:12]) {
		case "vide":
			if m.VCodec != "" || m.Width != 0 || len(e.body) < 78 {
				continue
			}
			m.Width = int(binary.BigEndian.Uint16(e.body[24:26]))
			m.Height = int(binary.BigEndian.Uint16(e.body[26:28]))
			m.VCodec, m.HDR = mp4VideoCodec(e.typ, e.body[78:])
		case "soun":
			m.Audio = append(m.Audio, mp4Audio(e, mp4Language(child(mdia, "mdhd")), trackEnabled(child(trak.body, "tkhd"))))
		}
	}
	return m
}

func mp4VideoCodec(typ string, children []byte) (codec, hdr string) {
	dv := false
	switch typ {
	case "avc1", "avc3":
		codec = "H.264"
	case "hvc1", "hev1":
		codec = "HEVC"
	case "dvh1", "dvhe":
		codec, dv = "HEVC", true
	case "dva1", "dvav":
		codec, dv = "H.264", true
	case "av01":
		codec = "AV1"
	case "vp09":
		codec = "VP9"
	case "vp08":
		codec = "VP8"
	case "mp4v":
		codec = "MPEG-4"
	}
	var transfer uint64
	for _, b := range boxes(children) {
		switch b.typ {
		case "dvcC", "dvvC", "dvwC":
			dv = true
		case "colr":
			if len(b.body) >= 8 && (string(b.body[:4]) == "nclx" || string(b.body[:4]) == "nclc") {
				transfer = uint64(binary.BigEndian.Uint16(b.body[6:8]))
			}
		}
	}
	return codec, hdrName(transfer, dv)
}

func mp4Audio(e box, lang string, isDefault bool) AudioTrack {
	a := AudioTrack{Lang: lang, Default: isDefault}
	switch e.typ {
	case "mp4a":
		a.Codec = "AAC"
	case "ac-3":
		a.Codec = "AC3"
	case "ec-3":
		a.Codec = "EAC3"
	case "mlpa":
		a.Codec = "TrueHD"
	case "dtsc", "dtse":
		a.Codec = "DTS"
	case "dtsh":
		a.Codec = "DTS-HD"
	case "dtsl":
		a.Codec = "DTS-HD MA"
	case "Opus":
		a.Codec = "Opus"
	case "fLaC":
		a.Codec = "FLAC"
	case "alac":
		a.Codec = "ALAC"
	case ".mp3":
		a.Codec = "MP3"
	case "lpcm", "sowt", "twos", "ipcm":
		a.Codec = "PCM"
	}
	if len(e.body) < 28 {
		return a
	}
	// AudioSampleEntry: bei QuickTime-Version 1 und 2 ist der feste Teil länger.
	fixed := 28
	switch binary.BigEndian.Uint16(e.body[8:10]) {
	case 0:
		a.Channels = int(binary.BigEndian.Uint16(e.body[16:18]))
	case 1:
		a.Channels = int(binary.BigEndian.Uint16(e.body[16:18]))
		fixed += 16
	case 2:
		fixed += 36
		if len(e.body) >= 44 {
			a.Channels = int(binary.BigEndian.Uint32(e.body[40:44]))
		}
	}
	if len(e.body) > fixed {
		for _, b := range boxes(e.body[fixed:]) {
			switch b.typ {
			case "dac3":
				if n := ac3Channels(b.body, false); n > 0 {
					a.Channels = n
				}
			case "dec3":
				if n := ac3Channels(b.body, true); n > 0 {
					a.Channels = n
				}
			}
		}
	}
	return a
}

// ac3Channels liest die Kanalzahl aus dac3 bzw. dec3 (ETSI TS 102 366, Anhang F).
func ac3Channels(b []byte, eac3 bool) int {
	acmodChannels := [8]int{2, 1, 2, 3, 3, 4, 4, 5}
	bits := func(off, n int) int { // off in Bits ab Anfang von b
		v := 0
		for i := 0; i < n; i++ {
			byteI := (off + i) / 8
			if byteI >= len(b) {
				return -1
			}
			v = v<<1 | int(b[byteI]>>(7-uint((off+i)%8))&1) // #nosec G115 -- 0..7
		}
		return v
	}
	var acmod, lfe, extra int
	if !eac3 {
		// fscod(2) bsid(5) bsmod(3) acmod(3) lfeon(1)
		acmod, lfe = bits(10, 3), bits(13, 1)
	} else {
		// data_rate(13) num_ind_sub(3), dann je Unterstrom:
		// fscod(2) bsid(5) reserved(1) asvc(1) bsmod(3) acmod(3) lfeon(1) reserved(3) num_dep_sub(4)
		acmod, lfe = bits(16+12, 3), bits(16+15, 1)
		if deps := bits(16+19, 4); deps > 0 {
			// chan_loc(9): zusätzliche Kanäle der abhängigen Unterströme.
			locs := bits(16+23, 9)
			pairs := [9]int{2, 2, 2, 1, 1, 2, 2, 1, 1} // Lc/Rc, Lrs/Rrs, Cs, Ts, Lsd/Rsd, Lw/Rw, Lvh/Rvh, Cvh, LFE2
			for i := 0; i < 9 && locs >= 0; i++ {
				if locs&(1<<(8-i)) != 0 {
					extra += pairs[i]
				}
			}
		}
	}
	if acmod < 0 || lfe < 0 {
		return 0
	}
	return acmodChannels[acmod] + lfe + extra
}

// mp4Language liest die Sprache aus mdhd (drei gepackte Buchstaben).
func mp4Language(mdhd []byte) string {
	off := 20
	if len(mdhd) > 0 && mdhd[0] == 1 {
		off = 32
	}
	if len(mdhd) < off+2 {
		return ""
	}
	v := binary.BigEndian.Uint16(mdhd[off : off+2])
	if v == 0 || v == 0x7FFF {
		return ""
	}
	l := []byte{byte(v>>10&31) + 0x60, byte(v>>5&31) + 0x60, byte(v&31) + 0x60} // #nosec G115 -- 5 Bit
	return lang2(string(l))
}

// trackEnabled meldet, ob die Spur als abspielbar markiert ist (tkhd-Flag 1).
func trackEnabled(tkhd []byte) bool {
	return len(tkhd) >= 4 && tkhd[3]&1 != 0
}
