package main

import (
	"fmt"
	"sort"
	"strings"
)

// Welche von mehreren Dateien mit demselben Ziel ist die bessere? Verglichen
// wird der Reihe nach: Auflösung, HDR, Video-Codec, Ton, zuletzt die Dateigröße.

var (
	resRank   = map[string]int{"480p": 1, "576p": 2, "720p": 3, "1080i": 4, "1080p": 5, "2160p": 6, "4k": 6, "uhd": 6}
	hdrRank   = map[string]int{"HLG": 1, "HDR10": 2, "DV": 3}
	codecRank = map[string]int{"MPEG-2": 1, "MPEG-4": 2, "VP8": 2, "H.264": 3, "VP9": 4, "HEVC": 5, "AV1": 6}
	audioRank = map[string]int{"MP2": 1, "MP3": 1, "AAC": 2, "Vorbis": 2, "Opus": 3, "AC3": 3, "EAC3": 4, "DTS": 4,
		"DTS-HD": 5, "FLAC": 6, "ALAC": 6, "PCM": 6, "DTS-HD MA": 7, "TrueHD": 7}
)

// quality fasst die vergleichbaren Merkmale einer Datei zusammen.
type quality struct {
	res, hdr, codec, channels, audio int
	size                             int64
}

func qualityOf(it *Item) quality {
	q := quality{res: resRank[strings.ToLower(it.Info.Resolution)], size: it.Size}
	if m := it.Media; m != nil {
		q.hdr, q.codec = hdrRank[m.HDR], codecRank[m.VCodec]
		for _, a := range m.Audio { // die beste Tonspur zählt
			if a.Channels > q.channels || a.Channels == q.channels && audioRank[a.Codec] > q.audio {
				q.channels, q.audio = a.Channels, audioRank[a.Codec]
			}
		}
	}
	return q
}

// cmp liefert >0, wenn q besser ist als o, ohne die Dateigröße.
func (q quality) cmp(o quality) int {
	for _, d := range [][2]int{{q.res, o.res}, {q.hdr, o.hdr}, {q.codec, o.codec}, {q.channels, o.channels}, {q.audio, o.audio}} {
		if d[0] != d[1] {
			return d[0] - d[1]
		}
	}
	return 0
}

// qualityParts beschreibt eine Datei in derselben Reihenfolge wie der Vergleich.
func qualityParts(it *Item) [4]string {
	p := [4]string{it.Info.Resolution}
	if m := it.Media; m != nil {
		p[1], p[2], p[3] = m.HDR, m.VCodec, bestAudio(m)
		if p[1] == "" && m.VCodec != "" {
			p[1] = "SDR"
		}
	}
	return p
}

func bestAudio(m *Media) string {
	var best *AudioTrack
	for i := range m.Audio {
		a := &m.Audio[i]
		if best == nil || a.Channels > best.Channels || a.Channels == best.Channels && audioRank[a.Codec] > audioRank[best.Codec] {
			best = a
		}
	}
	return best.String()
}

// betterReason erklärt kurz, warum a besser ist als b, z. B.
// "2160p HEVC statt 1080p H.264".
func betterReason(a, b *Item) string {
	pa, pb := qualityParts(a), qualityParts(b)
	unknown := [4]string{"unbekannter Auflösung", "", "unbekanntem Codec", "unbekanntem Ton"}
	var wa, wb []string
	for i := range pa {
		if pa[i] == pb[i] || pa[i] == "" || len(wa) == 2 { // die zwei wichtigsten Unterschiede reichen
			continue
		}
		other := pb[i]
		if other == "" {
			if other = unknown[i]; other == "" {
				continue
			}
		}
		wa, wb = append(wa, pa[i]), append(wb, other)
	}
	if len(wa) > 0 {
		return strings.Join(wa, " ") + " statt " + strings.Join(wb, " ")
	}
	return "größere Datei (" + humanSize(a.Size) + " statt " + humanSize(b.Size) + ")"
}

func humanSize(n int64) string {
	switch {
	case n >= 1<<30:
		return strings.Replace(fmt.Sprintf("%.1f GB", float64(n)/(1<<30)), ".", ",", 1)
	case n >= 1<<20:
		return fmt.Sprintf("%d MB", n>>20)
	}
	return fmt.Sprintf("%d KB", (n+1023)>>10)
}

// suggestBest markiert in einer Gruppe gleicher Ziele die beste Datei und
// liefert sie samt Begründung. Sind alle gleichwertig, gibt es keinen Vorschlag.
func suggestBest(g []*Item) (*Item, string) {
	sorted := append([]*Item(nil), g...)
	qs := map[*Item]quality{}
	for _, it := range sorted {
		qs[it] = qualityOf(it)
	}
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := qs[sorted[i]], qs[sorted[j]]
		if c := a.cmp(b); c != 0 {
			return c > 0
		}
		return a.size > b.size
	})
	best, next := sorted[0], sorted[1]
	c := qs[best].cmp(qs[next])
	// Die Größe allein entscheidet nur bei deutlichem Unterschied (über 10 %).
	if c == 0 && qs[best].size*10 <= qs[next].size*11 {
		return nil, ""
	}
	return best, betterReason(best, next)
}
