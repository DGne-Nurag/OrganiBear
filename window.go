//go:build desktop

package main

import (
	"context"
	"fmt"
	"time"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/linux"
	"github.com/wailsapp/wails/v2/pkg/options/mac"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
	wailsrt "github.com/wailsapp/wails/v2/pkg/runtime"
)

// Das eigene Programmfenster (Wails v2, MIT-Lizenz). Es zeigt dieselbe
// Oberfläche wie im Browser, nutzt aber die Web-Engine des Systems (WebView2,
// WebKit, WebKitGTK). Nur so kommt beim Drag & Drop der echte Ordnerpfad an.
// Gebaut wird es mit -tags desktop,production; ohne die Tags gibt es nur den
// Browser-Modus (window_stub.go).

const windowAvailable = true

// runWindow öffnet das Fenster und kehrt erst zurück, wenn es geschlossen ist.
func runWindow(srv *Server) error {
	started := make(chan context.Context, 1)
	go func() {
		// Der Beenden-Knopf in der Oberfläche schließt auch das Fenster.
		<-srv.Done()
		wailsrt.Quit(<-started)
	}()
	return wails.Run(&options.App{
		Title:            "OrganiBear",
		Width:            1180,
		Height:           860,
		MinWidth:         360,
		MinHeight:        480,
		BackgroundColour: &options.RGBA{R: 255, G: 247, B: 234, A: 255},
		AssetServer:      &assetserver.Options{Handler: srv.WindowHandler()},
		DragAndDrop:      &options.DragAndDrop{EnableFileDrop: true},
		OnStartup:        func(c context.Context) { started <- c },
		OnShutdown: func(context.Context) {
			// Ein laufendes Einsortieren noch fertig werden lassen.
			srv.WaitIdle(10 * time.Minute)
		},
		Windows: &windows.Options{},
		Mac:     &mac.Options{About: &mac.AboutInfo{Title: "OrganiBear", Message: fmt.Sprintf("Version %s", version)}},
		Linux:   &linux.Options{ProgramName: "OrganiBear"},
	})
}
