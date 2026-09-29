//go:build desktop && linux

package main

import "os"

// WebKitGTK stürzt unter Wayland mit manchen Grafiktreibern (v. a. NVIDIA) beim
// Start ab: "Gdk-Message: Error 71 (Protokollfehler) dispatching to Wayland
// display". Ohne den DMA-BUF-Renderer läuft das Fenster überall. Wer es anders
// will, setzt die Variable selbst, dann bleibt sie unangetastet.
func init() {
	if _, ok := os.LookupEnv("WEBKIT_DISABLE_DMABUF_RENDERER"); !ok {
		_ = os.Setenv("WEBKIT_DISABLE_DMABUF_RENDERER", "1")
	}
}
