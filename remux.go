package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
)

// Umpacken von MP4 nach MKV: Bild, Ton und Untertitel bleiben Bit für Bit
// gleich, nur der Container wechselt. Gelesen wird mit den MP4-Helfern aus
// probe.go, geschrieben nach der Matroska-Spezifikation (RFC 8794 für EBML,
// RFC 9559 für Matroska). Unterstützt sind die in MP4 üblichen Formate;
// alles andere lehnt canRemux ab, dann bleibt die Datei MP4.

var errRemuxUnsupported = errors.New("kann nicht umgepackt werden")

type rxSample struct {
	off  int64
	size uint32
	dts  int64 // Dekodierzeit in Einheiten der Spur
	pts  int64 // Anzeigezeit in Einheiten der Spur (nach Edit-Liste)
	dur  uint32
	key  bool
}

type rxTrack struct {
	id        uint32
	kind      uint64 // 1 Video, 2 Ton, 17 Untertitel (Matroska-TrackType)
	num       uint64 // Spurnummer in der MKV
	codecID   string
	private   []byte
	width     int
	height    int
	rate      float64
	channels  int
	lang      string // ISO 639-2, z. B. "deu"
	def       bool
	timescale uint32
	samples   []rxSample
	tx3g      bool // Untertitel mit Längenpräfix
}

// parseMP4ForRemux liest alle Spuren samt Sample-Tabellen.
func parseMP4ForRemux(f io.ReadSeeker) ([]*rxTrack, error) {
	fileSize, err := f.Seek(0, io.SeekEnd)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	var moov []byte
	for i := 0; i < 64 && moov == nil; i++ {
		typ, size, err := readBoxHeader(f)
		if err != nil {
			return nil, errNoMedia
		}
		if typ == "moov" {
			if size < 0 || size > 256<<20 || size > fileSize {
				return nil, errors.New("moov-Box zu groß")
			}
			if moov, err = readExactly(f, size); err != nil {
				return nil, err
			}
			break
		}
		if size < 0 {
			break
		}
		if _, err := f.Seek(size, io.SeekCurrent); err != nil {
			return nil, err
		}
	}
	if moov == nil {
		return nil, errNoMedia
	}
	if child(moov, "mvex") != nil {
		return nil, fmt.Errorf("%w: fragmentierte MP4", errRemuxUnsupported)
	}
	movieScale := mdhdTimescale(child(moov, "mvhd"))

	// Kapitel-Spuren (tref/chap) sind keine Untertitel.
	chapters := map[uint32]bool{}
	for _, t := range boxes(moov) {
		if t.typ == "trak" {
			if c := child(t.body, "tref", "chap"); c != nil {
				for i := 0; i+4 <= len(c); i += 4 {
					chapters[binary.BigEndian.Uint32(c[i:])] = true
				}
			}
		}
	}

	var tracks []*rxTrack
	for _, trak := range boxes(moov) {
		if trak.typ != "trak" {
			continue
		}
		t, err := parseRemuxTrack(trak.body, movieScale, chapters, fileSize)
		if err != nil {
			return nil, err
		}
		if t != nil && len(t.samples) > 0 {
			tracks = append(tracks, t)
		}
	}
	hasAV := false
	for _, t := range tracks {
		hasAV = hasAV || t.kind != 17
	}
	if !hasAV {
		return nil, fmt.Errorf("%w: keine Bild- oder Tonspur", errRemuxUnsupported)
	}
	return tracks, nil
}

func parseRemuxTrack(trak []byte, movieScale uint32, chapters map[uint32]bool, fileSize int64) (*rxTrack, error) {
	tkhd := child(trak, "tkhd")
	mdia := child(trak, "mdia")
	hdlr := child(mdia, "hdlr")
	stbl := child(mdia, "minf", "stbl")
	stsd := child(stbl, "stsd")
	if len(hdlr) < 12 || len(stsd) < 8 || len(tkhd) < 16 {
		return nil, nil
	}
	t := &rxTrack{def: trackEnabled(tkhd), lang: mp4Lang3(child(mdia, "mdhd")), timescale: mdhdTimescale(child(mdia, "mdhd"))}
	if tkhd[0] == 1 {
		if len(tkhd) < 24 {
			return nil, nil
		}
		t.id = binary.BigEndian.Uint32(tkhd[20:24])
	} else {
		t.id = binary.BigEndian.Uint32(tkhd[12:16])
	}
	if t.timescale == 0 {
		return nil, nil
	}
	entries := boxes(stsd[8:])
	if len(entries) == 0 {
		return nil, nil
	}
	if len(entries) > 1 {
		return nil, fmt.Errorf("%w: mehrere Formate in einer Spur", errRemuxUnsupported)
	}
	e := entries[0]
	switch string(hdlr[8:12]) {
	case "vide":
		if err := t.videoEntry(e); err != nil {
			return nil, err
		}
	case "soun":
		if err := t.audioEntry(e); err != nil {
			return nil, err
		}
	case "sbtl", "text":
		if e.typ != "tx3g" || chapters[t.id] {
			return nil, nil // Kapitel und exotische Untertitel werden ausgelassen
		}
		t.kind, t.codecID, t.tx3g = 17, "S_TEXT/UTF8", true
	default:
		return nil, nil // Zeitcode, Metadaten, Hinweisspuren
	}
	if err := t.readSamples(stbl, child(trak, "edts", "elst"), movieScale, fileSize); err != nil {
		return nil, err
	}
	return t, nil
}

func (t *rxTrack) videoEntry(e box) error {
	if len(e.body) < 78 {
		return fmt.Errorf("%w: kaputte Videobeschreibung", errRemuxUnsupported)
	}
	t.kind = 1
	t.width = int(binary.BigEndian.Uint16(e.body[24:26]))
	t.height = int(binary.BigEndian.Uint16(e.body[26:28]))
	kids := e.body[78:]
	switch e.typ {
	case "avc1", "avc3":
		t.codecID, t.private = "V_MPEG4/ISO/AVC", child(kids, "avcC")
	case "hvc1", "hev1":
		t.codecID, t.private = "V_MPEGH/ISO/HEVC", child(kids, "hvcC")
	case "av01":
		t.codecID, t.private = "V_AV1", child(kids, "av1C")
	default:
		return fmt.Errorf("%w: Videoformat %q", errRemuxUnsupported, e.typ)
	}
	if t.private == nil {
		return fmt.Errorf("%w: Videoformat ohne Konfiguration", errRemuxUnsupported)
	}
	return nil
}

func (t *rxTrack) audioEntry(e box) error {
	if len(e.body) < 28 {
		return fmt.Errorf("%w: kaputte Tonbeschreibung", errRemuxUnsupported)
	}
	t.kind = 2
	t.channels = int(binary.BigEndian.Uint16(e.body[16:18]))
	t.rate = float64(binary.BigEndian.Uint32(e.body[24:28])) / 65536
	kidsAt := 28
	switch binary.BigEndian.Uint16(e.body[8:10]) { // QuickTime-Versionen haben mehr Felder
	case 1:
		kidsAt += 16
	case 2:
		kidsAt += 36
		if len(e.body) >= 44 {
			t.rate = math.Float64frombits(binary.BigEndian.Uint64(e.body[32:40]))
			t.channels = int(binary.BigEndian.Uint32(e.body[40:44]))
		}
	}
	if kidsAt > len(e.body) {
		kidsAt = len(e.body)
	}
	kids := e.body[kidsAt:]
	switch e.typ {
	case "mp4a":
		oti, asc := esdsConfig(child(kids, "esds"))
		switch oti {
		case 0x40, 0x66, 0x67, 0x68:
			if len(asc) == 0 {
				return fmt.Errorf("%w: AAC ohne Konfiguration", errRemuxUnsupported)
			}
			t.codecID, t.private = "A_AAC", asc
		case 0x69, 0x6B:
			t.codecID = "A_MPEG/L3"
		default:
			return fmt.Errorf("%w: Tonformat mp4a/%#x", errRemuxUnsupported, oti)
		}
	case "ac-3":
		t.codecID = "A_AC3"
	case "ec-3":
		t.codecID = "A_EAC3"
	default:
		return fmt.Errorf("%w: Tonformat %q", errRemuxUnsupported, e.typ)
	}
	return nil
}

// esdsConfig liest Objekttyp und DecoderSpecificInfo (bei AAC die AudioSpecificConfig).
func esdsConfig(esds []byte) (oti byte, asc []byte) {
	if len(esds) < 4 {
		return 0, nil
	}
	b := esds[4:]
	next := func() (tag byte, body []byte, ok bool) {
		if len(b) < 2 {
			return 0, nil, false
		}
		tag = b[0]
		n, i := 0, 1
		for ; i < 5 && i < len(b); i++ {
			n = n<<7 | int(b[i]&0x7F)
			if b[i]&0x80 == 0 {
				i++
				break
			}
		}
		if i+n > len(b) {
			n = len(b) - i
		}
		body, b = b[i:i+n], b[i+n:]
		return tag, body, true
	}
	tag, es, ok := next()
	if !ok || tag != 0x03 || len(es) < 3 {
		return 0, nil
	}
	flags := es[2]
	es = es[3:]
	if flags&0x80 != 0 && len(es) >= 2 {
		es = es[2:]
	}
	if flags&0x40 != 0 && len(es) >= 1 {
		es = es[min(len(es), 1+int(es[0])):]
	}
	if flags&0x20 != 0 && len(es) >= 2 {
		es = es[2:]
	}
	b = es
	tag, dc, ok := next()
	if !ok || tag != 0x04 || len(dc) < 13 {
		return 0, nil
	}
	oti = dc[0]
	b = dc[13:]
	if tag, dsi, ok := next(); ok && tag == 0x05 {
		asc = dsi
	}
	return oti, asc
}

// readSamples baut aus stsz, stco/co64, stsc, stts, ctts, stss und elst die Sample-Liste.
func (t *rxTrack) readSamples(stbl, elst []byte, movieScale uint32, fileSize int64) error {
	full := func(b []byte) []byte { // Version/Flags überspringen
		if len(b) < 4 {
			return nil
		}
		return b[4:]
	}
	u32 := func(b []byte, i int) uint32 { return binary.BigEndian.Uint32(b[i:]) }

	stsz := full(child(stbl, "stsz"))
	if len(stsz) < 8 {
		return fmt.Errorf("%w: keine Sample-Größen", errRemuxUnsupported)
	}
	fixed, count := u32(stsz, 0), int(u32(stsz, 4))
	// Jedes Sample belegt mindestens ein Byte der Datei: schützt vor riesigen
	// Tabellen in kaputten Dateien.
	if int64(count) > fileSize || (fixed != 0 && int64(count)*int64(fixed) > fileSize) || (fixed == 0 && len(stsz) < 8+4*count) {
		return fmt.Errorf("%w: kaputte Sample-Tabelle", errRemuxUnsupported)
	}
	t.samples = make([]rxSample, count)
	for i := range t.samples {
		if fixed != 0 {
			t.samples[i].size = fixed
		} else {
			t.samples[i].size = u32(stsz, 8+4*i)
		}
	}

	// Chunk-Offsets
	var chunks []int64
	if co := full(child(stbl, "stco")); len(co) >= 4 {
		n := int(u32(co, 0))
		if len(co) < 4+4*n {
			return fmt.Errorf("%w: kaputte Chunk-Tabelle", errRemuxUnsupported)
		}
		for i := 0; i < n; i++ {
			chunks = append(chunks, int64(u32(co, 4+4*i)))
		}
	} else if co := full(child(stbl, "co64")); len(co) >= 4 {
		n := int(u32(co, 0))
		if len(co) < 4+8*n {
			return fmt.Errorf("%w: kaputte Chunk-Tabelle", errRemuxUnsupported)
		}
		for i := 0; i < n; i++ {
			chunks = append(chunks, int64(binary.BigEndian.Uint64(co[4+8*i:]))) // #nosec G115 -- Dateiposition
		}
	}
	stsc := full(child(stbl, "stsc"))
	if len(stsc) < 4 || len(chunks) == 0 {
		return fmt.Errorf("%w: keine Chunk-Zuordnung", errRemuxUnsupported)
	}
	nsc := int(u32(stsc, 0))
	if len(stsc) < 4+12*nsc || nsc == 0 {
		return fmt.Errorf("%w: kaputte Chunk-Zuordnung", errRemuxUnsupported)
	}
	s := 0
	for e := 0; e < nsc && s < count; e++ {
		first := int(u32(stsc, 4+12*e)) - 1
		per := int(u32(stsc, 8+12*e))
		last := len(chunks)
		if e+1 < nsc {
			last = int(u32(stsc, 4+12*(e+1))) - 1
		}
		for c := first; c < last && c < len(chunks) && s < count; c++ {
			off := chunks[c]
			for k := 0; k < per && s < count; k++ {
				t.samples[s].off = off
				off += int64(t.samples[s].size)
				s++
			}
		}
	}
	if s < count {
		return fmt.Errorf("%w: Samples ohne Chunk", errRemuxUnsupported)
	}

	// Zeiten
	stts := full(child(stbl, "stts"))
	if len(stts) < 4 {
		return fmt.Errorf("%w: keine Zeittabelle", errRemuxUnsupported)
	}
	var dts int64
	s = 0
	for e, n := 0, int(u32(stts, 0)); e < n && 8+8*e <= len(stts)-4; e++ {
		c, d := int(u32(stts, 4+8*e)), u32(stts, 8+8*e)
		for k := 0; k < c && s < count; k++ {
			t.samples[s].dts, t.samples[s].dur = dts, d
			dts += int64(d)
			s++
		}
	}
	for ; s < count; s++ { // unvollständige Tabelle: letzte Dauer weiterführen
		t.samples[s].dts = dts
		if s > 0 {
			t.samples[s].dur = t.samples[s-1].dur
		}
		dts += int64(t.samples[s].dur)
	}
	for i := range t.samples {
		t.samples[i].pts = t.samples[i].dts
	}
	if ctts := child(stbl, "ctts"); len(ctts) >= 8 {
		signed := ctts[0] == 1
		b := ctts[4:]
		s = 0
		for e, n := 0, int(u32(b, 0)); e < n && 12+8*e <= len(b); e++ {
			c, raw := int(u32(b, 4+8*e)), u32(b, 8+8*e)
			off := int64(raw)
			if signed {
				off = int64(int32(raw)) // #nosec G115 -- Version 1: vorzeichenbehaftet
			}
			for k := 0; k < c && s < count; k++ {
				t.samples[s].pts += off
				s++
			}
		}
	}

	// Schlüsselbilder: ohne stss ist jedes Sample eins.
	if stss := full(child(stbl, "stss")); len(stss) >= 4 && t.kind == 1 {
		n := int(u32(stss, 0))
		for i := 0; i < n && 4+4*i+4 <= len(stss); i++ {
			if k := int(u32(stss, 4+4*i)) - 1; k >= 0 && k < count {
				t.samples[k].key = true
			}
		}
	} else {
		for i := range t.samples {
			t.samples[i].key = true
		}
	}

	// Edit-Liste: Anfangsversatz (leere Einträge) und Startpunkt in den Mediendaten.
	if b := full(elst); len(b) >= 4 && movieScale > 0 {
		v1 := elst[0] == 1
		var shift int64
		for e, n := 0, int(u32(b, 0)); e < n; e++ {
			var segDur uint64
			var mediaTime int64
			if v1 {
				if len(b) < 4+20*(e+1) {
					break
				}
				segDur = binary.BigEndian.Uint64(b[4+20*e:])
				mediaTime = int64(binary.BigEndian.Uint64(b[12+20*e:])) // #nosec G115 -- vorzeichenbehaftet
			} else {
				if len(b) < 4+12*(e+1) {
					break
				}
				segDur = uint64(u32(b, 4+12*e))
				mediaTime = int64(int32(u32(b, 8+12*e))) // #nosec G115 -- vorzeichenbehaftet
			}
			if mediaTime == -1 { // leerer Eintrag: Spur beginnt später
				shift += int64(segDur) * int64(t.timescale) / int64(movieScale) // #nosec G115 -- Dauer
				continue
			}
			shift -= mediaTime
			break
		}
		for i := range t.samples {
			t.samples[i].pts += shift
			t.samples[i].dts += shift
		}
	}
	return nil
}

func mdhdTimescale(b []byte) uint32 {
	if len(b) >= 24 && b[0] == 1 {
		return binary.BigEndian.Uint32(b[20:24])
	}
	if len(b) >= 16 {
		return binary.BigEndian.Uint32(b[12:16])
	}
	return 0
}

// mp4Lang3 liefert die Sprache aus mdhd als ISO 639-2 („und“, wenn unbekannt).
func mp4Lang3(mdhd []byte) string {
	off := 20
	if len(mdhd) > 0 && mdhd[0] == 1 {
		off = 32
	}
	if len(mdhd) < off+2 {
		return "und"
	}
	v := binary.BigEndian.Uint16(mdhd[off : off+2])
	l := []byte{byte(v>>10&31) + 0x60, byte(v>>5&31) + 0x60, byte(v&31) + 0x60} // #nosec G115 -- 5 Bit
	for _, c := range l {
		if c < 'a' || c > 'z' {
			return "und"
		}
	}
	return string(l)
}

// canRemux prüft ohne zu schreiben, ob sich eine Datei umpacken lässt.
func canRemux(path string) error {
	f, err := os.Open(path) // #nosec G304 -- Datei aus dem gewählten Quellordner
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = parseMP4ForRemux(f)
	return err
}

// remuxFile packt src (MP4) nach dst (MKV) um. dst darf nicht existieren; bei
// einem Fehler bleibt keine halbe Datei liegen.
func remuxFile(src, dst string) (err error) {
	in, err := os.Open(src) // #nosec G304 -- Datei aus dem gewählten Quellordner
	if err != nil {
		return err
	}
	defer in.Close()
	tracks, err := parseMP4ForRemux(in)
	if err != nil {
		return err
	}
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644) // #nosec G302 G304 -- Bibliothek, lesbar für Mediaserver
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return errExists
		}
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
	return writeMKV(out, in, tracks)
}

// --- Matroska schreiben ---

const (
	idEBML               = 0x1A45DFA3
	idEBMLVersion        = 0x4286
	idEBMLReadVersion    = 0x42F7
	idEBMLMaxIDLength    = 0x42F2
	idEBMLMaxSizeLength  = 0x42F3
	idDocType            = 0x4282
	idDocTypeVersion     = 0x4287
	idDocTypeReadVersion = 0x4285
	idSegment            = 0x18538067
	idSeekHead           = 0x114D9B74
	idSeek               = 0x4DBB
	idSeekID             = 0x53AB
	idSeekPosition       = 0x53AC
	idVoid               = 0xEC
	idInfo               = 0x1549A966
	idTimestampScale     = 0x2AD7B1
	idDuration           = 0x4489
	idMuxingApp          = 0x4D80
	idWritingApp         = 0x5741
	idTracks             = 0x1654AE6B
	idTrackEntry         = 0xAE
	idTrackNumber        = 0xD7
	idTrackUID           = 0x73C5
	idTrackType          = 0x83
	idFlagDefault        = 0x88
	idFlagLacing         = 0x9C
	idDefaultDuration    = 0x23E383
	idLanguage           = 0x22B59C
	idCodecID            = 0x86
	idCodecPrivate       = 0x63A2
	idVideo              = 0xE0
	idPixelWidth         = 0xB0
	idPixelHeight        = 0xBA
	idAudio              = 0xE1
	idSamplingFrequency  = 0xB5
	idChannels           = 0x9F
	idCluster            = 0x1F43B675
	idTimestamp          = 0xE7
	idSimpleBlock        = 0xA3
	idBlockGroup         = 0xA0
	idBlock              = 0xA1
	idBlockDuration      = 0x9B
	idCues               = 0x1C53BB6B
	idCuePoint           = 0xBB
	idCueTime            = 0xB3
	idCueTrackPositions  = 0xB7
	idCueTrack           = 0xF7
	idCueClusterPosition = 0xF1
)

func ebmlID(b *bytes.Buffer, id uint32) {
	var raw [4]byte
	binary.BigEndian.PutUint32(raw[:], id)
	n := 4
	for n > 1 && raw[4-n] == 0 {
		n--
	}
	b.Write(raw[4-n:])
}

func ebmlSize(b *bytes.Buffer, n uint64) {
	l := 1
	for l < 8 && n >= 1<<(7*uint(l))-1 {
		l++
	}
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], n|1<<(7*uint(l)))
	b.Write(raw[8-l:])
}

func ebmlElem(b *bytes.Buffer, id uint32, body []byte) {
	ebmlID(b, id)
	ebmlSize(b, uint64(len(body)))
	b.Write(body)
}

func ebmlMaster(b *bytes.Buffer, id uint32, fill func(*bytes.Buffer)) {
	var inner bytes.Buffer
	fill(&inner)
	ebmlElem(b, id, inner.Bytes())
}

func ebmlUintElem(b *bytes.Buffer, id uint32, v uint64) {
	var raw [8]byte
	binary.BigEndian.PutUint64(raw[:], v)
	n := 8
	for n > 1 && raw[8-n] == 0 {
		n--
	}
	ebmlElem(b, id, raw[8-n:])
}

func ebmlFloatElem(b *bytes.Buffer, id uint32, v float64) {
	body := make([]byte, 8)
	binary.BigEndian.PutUint64(body, math.Float64bits(v))
	ebmlElem(b, id, body)
}

type rxBlock struct {
	track *rxTrack
	s     *rxSample
	dts   int64 // ms
	pts   int64 // ms
}

func toMS(v int64, scale uint32) int64 {
	return int64(math.Round(float64(v) * 1000 / float64(scale)))
}

func writeMKV(out *os.File, in io.ReaderAt, tracks []*rxTrack) error {
	var head bytes.Buffer
	ebmlMaster(&head, idEBML, func(b *bytes.Buffer) {
		ebmlUintElem(b, idEBMLVersion, 1)
		ebmlUintElem(b, idEBMLReadVersion, 1)
		ebmlUintElem(b, idEBMLMaxIDLength, 4)
		ebmlUintElem(b, idEBMLMaxSizeLength, 8)
		ebmlElem(b, idDocType, []byte("matroska"))
		ebmlUintElem(b, idDocTypeVersion, 4)
		ebmlUintElem(b, idDocTypeReadVersion, 2)
	})
	ebmlID(&head, idSegment)
	sizeAt := int64(head.Len())
	head.Write([]byte{0x01, 0, 0, 0, 0, 0, 0, 0}) // Größe wird am Ende eingetragen
	segStart := int64(head.Len())

	// Alle Blöcke in Dekodier-Reihenfolge, spurübergreifend verzahnt.
	var blocks []rxBlock
	var durMS int64
	var num uint64
	for _, t := range tracks {
		num++
		t.num = num
		for j := range t.samples {
			s := &t.samples[j]
			if t.tx3g && s.size <= 2 {
				continue // leere Untertitel markieren nur Pausen
			}
			bl := rxBlock{track: t, s: s, dts: toMS(s.dts, t.timescale), pts: toMS(s.pts, t.timescale)}
			blocks = append(blocks, bl)
			if end := toMS(s.pts+int64(s.dur), t.timescale); end > durMS {
				durMS = end
			}
		}
	}
	sort.SliceStable(blocks, func(a, b int) bool {
		if blocks[a].dts != blocks[b].dts {
			return blocks[a].dts < blocks[b].dts
		}
		return blocks[a].track.num < blocks[b].track.num
	})
	// Anzeigezeiten dürfen nicht negativ sein: alles gemeinsam verschieben,
	// damit Bild, Ton und Untertitel synchron bleiben. Die Anlaufproben des
	// Tons bleiben drin, der Decoder braucht sie für den ersten echten Block.
	var minPTS int64
	for _, bl := range blocks {
		minPTS = min(minPTS, bl.pts)
	}

	// SeekHead mit festen 8-Byte-Positionen, damit er vorab geschrieben werden kann.
	seekEntry := func(b *bytes.Buffer, id uint32, pos uint64) {
		ebmlMaster(b, idSeek, func(s *bytes.Buffer) {
			var idb bytes.Buffer
			ebmlID(&idb, id)
			ebmlElem(s, idSeekID, idb.Bytes())
			p := make([]byte, 8)
			binary.BigEndian.PutUint64(p, pos)
			ebmlElem(s, idSeekPosition, p)
		})
	}
	var info, trks bytes.Buffer
	ebmlMaster(&info, idInfo, func(b *bytes.Buffer) {
		ebmlUintElem(b, idTimestampScale, 1_000_000)
		ebmlFloatElem(b, idDuration, float64(durMS-minPTS))
		ebmlElem(b, idMuxingApp, []byte("OrganiBear"))
		ebmlElem(b, idWritingApp, []byte("OrganiBear "+version))
	})
	ebmlMaster(&trks, idTracks, func(b *bytes.Buffer) {
		for _, t := range tracks {
			ebmlMaster(b, idTrackEntry, func(e *bytes.Buffer) {
				ebmlUintElem(e, idTrackNumber, t.num)
				ebmlUintElem(e, idTrackUID, t.num)
				ebmlUintElem(e, idTrackType, t.kind)
				ebmlUintElem(e, idFlagLacing, 0)
				if !t.def {
					ebmlUintElem(e, idFlagDefault, 0)
				}
				ebmlElem(e, idLanguage, []byte(t.lang))
				ebmlElem(e, idCodecID, []byte(t.codecID))
				if len(t.private) > 0 {
					ebmlElem(e, idCodecPrivate, t.private)
				}
				switch t.kind {
				case 1:
					if d := t.samples[len(t.samples)/2].dur; d > 0 { // Bildrate für Player
						ebmlUintElem(e, idDefaultDuration, uint64(d)*1_000_000_000/uint64(t.timescale))
					}
					ebmlMaster(e, idVideo, func(v *bytes.Buffer) {
						ebmlUintElem(v, idPixelWidth, uint64(t.width))   // #nosec G115 -- aus uint16
						ebmlUintElem(v, idPixelHeight, uint64(t.height)) // #nosec G115 -- aus uint16
					})
				case 2:
					ebmlMaster(e, idAudio, func(a *bytes.Buffer) {
						ebmlFloatElem(a, idSamplingFrequency, t.rate)
						ebmlUintElem(a, idChannels, uint64(max(t.channels, 1))) // #nosec G115 -- klein
					})
				}
			})
		}
	})
	// Die Positionen im SeekHead sind immer 8 Byte lang, seine Größe hängt also
	// nicht von den Werten ab: erst mit Platzhaltern bauen, am Ende überschreiben.
	var seek bytes.Buffer
	var infoPos, tracksPos uint64
	build := func(cuesPos uint64) {
		seek.Reset()
		ebmlMaster(&seek, idSeekHead, func(b *bytes.Buffer) {
			seekEntry(b, idInfo, infoPos)
			seekEntry(b, idTracks, tracksPos)
			seekEntry(b, idCues, cuesPos)
		})
	}
	build(0)
	seekLen := seek.Len()
	infoPos = uint64(seekLen)                // #nosec G115 -- Puffergröße, nicht negativ
	tracksPos = infoPos + uint64(info.Len()) // #nosec G115 -- Puffergröße, nicht negativ
	build(0)
	head.Write(seek.Bytes())
	head.Write(info.Bytes())
	head.Write(trks.Bytes())
	if _, err := out.Write(head.Bytes()); err != nil {
		return err
	}
	pos := int64(head.Len())

	// Cluster: neu bei jedem Schlüsselbild des Videos (höchstens alle 5 s),
	// sonst spätestens nach 5 s oder wenn der relative Zeitstempel knapp wird.
	type cue struct{ t, pos, track uint64 }
	var cues []cue
	hasVideo := false
	for _, t := range tracks {
		hasVideo = hasVideo || t.kind == 1
	}
	var cl bytes.Buffer
	var clTime int64
	clOpen := false
	var clSize int
	flush := func() error {
		if !clOpen {
			return nil
		}
		var c bytes.Buffer
		ebmlElem(&c, idCluster, cl.Bytes())
		if _, err := out.Write(c.Bytes()); err != nil {
			return err
		}
		pos += int64(c.Len())
		cl.Reset()
		clOpen, clSize = false, 0
		return nil
	}
	buf := make([]byte, 0, 1<<20)
	for _, bl := range blocks {
		pts := bl.pts - minPTS
		isVideoKey := bl.track.kind == 1 && bl.s.key
		rel := pts - clTime
		needNew := !clOpen || rel > 30000 || rel < -30000 || clSize > 32<<20 ||
			(hasVideo && isVideoKey && pts-clTime >= 1000) ||
			(!hasVideo && pts-clTime >= 5000)
		if needNew {
			if err := flush(); err != nil {
				return err
			}
			clTime, clOpen = pts, true
			ebmlUintElem(&cl, idTimestamp, uint64(pts)) // #nosec G115 -- nicht negativ
			if !hasVideo || isVideoKey {
				cues = append(cues, cue{uint64(pts), uint64(pos - segStart), bl.track.num}) // #nosec G115 -- nicht negativ
			}
			rel = 0
		}
		if cap(buf) < int(bl.s.size) {
			buf = make([]byte, bl.s.size)
		}
		data := buf[:bl.s.size]
		if _, err := in.ReadAt(data, bl.s.off); err != nil {
			return fmt.Errorf("Lesen bei %d: %w", bl.s.off, err)
		}
		if bl.track.tx3g { // 16-Bit-Länge, dann UTF-8, danach Stilangaben
			n := int(binary.BigEndian.Uint16(data))
			data = data[2:min(2+n, len(data))]
		}
		var blk bytes.Buffer
		ebmlSize(&blk, bl.track.num)
		var tc [2]byte
		binary.BigEndian.PutUint16(tc[:], uint16(int16(rel))) // #nosec G115 -- oben auf ±30000 begrenzt
		blk.Write(tc[:])
		if bl.track.kind == 17 {
			blk.WriteByte(0)
			blk.Write(data)
			ebmlMaster(&cl, idBlockGroup, func(g *bytes.Buffer) {
				ebmlElem(g, idBlock, blk.Bytes())
				ebmlUintElem(g, idBlockDuration, uint64(max(toMS(int64(bl.s.dur), bl.track.timescale), 1))) // #nosec G115 -- positiv
			})
		} else {
			var flags byte
			if bl.s.key {
				flags |= 0x80
			}
			blk.WriteByte(flags)
			blk.Write(data)
			ebmlElem(&cl, idSimpleBlock, blk.Bytes())
		}
		clSize += blk.Len()
	}
	if err := flush(); err != nil {
		return err
	}

	// Cues ans Ende, dann SeekHead und Segmentgröße nachtragen.
	cuesPos := uint64(pos - segStart) // #nosec G115 -- nicht negativ
	var cb bytes.Buffer
	ebmlMaster(&cb, idCues, func(b *bytes.Buffer) {
		for _, c := range cues {
			ebmlMaster(b, idCuePoint, func(p *bytes.Buffer) {
				ebmlUintElem(p, idCueTime, c.t)
				ebmlMaster(p, idCueTrackPositions, func(tp *bytes.Buffer) {
					ebmlUintElem(tp, idCueTrack, c.track)
					ebmlUintElem(tp, idCueClusterPosition, c.pos)
				})
			})
		}
	})
	if _, err := out.Write(cb.Bytes()); err != nil {
		return err
	}
	pos += int64(cb.Len())
	build(cuesPos)
	if seek.Len() != seekLen {
		return errors.New("SeekHead hat seine Größe geändert")
	}
	if _, err := out.WriteAt(seek.Bytes(), segStart); err != nil {
		return err
	}
	size := make([]byte, 8)
	binary.BigEndian.PutUint64(size, uint64(pos-segStart)|0x01<<56) // #nosec G115 -- nicht negativ
	if _, err := out.WriteAt(size, sizeAt); err != nil {
		return err
	}
	return nil
}
