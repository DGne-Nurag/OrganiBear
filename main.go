// OrganiBear bringt Filme und Serien mit wilden Dateinamen in eine ordentliche,
// frei definierbare Ordnerstruktur. Ein einzelnes Programm mit Webinterface.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"syscall"
	"time"
)

//go:embed web
var webFiles embed.FS

var version = "dev"

const banner = `
   ʕ•ᴥ•ʔ  OrganiBear %s
   Der Bär, der deine Filme und Serien aufräumt.
`

func main() {
	defaultCfg := "organibear.json"
	if exe, err := os.Executable(); err == nil {
		defaultCfg = filepath.Join(filepath.Dir(exe), "organibear.json")
	}
	addr := flag.String("addr", "127.0.0.1:8765", "Adresse für das Webinterface")
	cfgPath := flag.String("config", defaultCfg, "Pfad zur Konfigurationsdatei")
	noBrowser := flag.Bool("no-browser", false, "Browser nicht automatisch öffnen")
	idle := flag.Duration("idle", 5*time.Minute, "beenden, wenn so lange kein Browser-Tab mehr offen ist (0 = nie)")
	flag.Parse()

	fmt.Printf(banner, version)

	cfg, err := LoadConfig(*cfgPath)
	if err != nil {
		log.Fatalf("Konfiguration kaputt: %v", err)
	}
	if _, err := os.Stat(*cfgPath); errors.Is(err, os.ErrNotExist) {
		if err := SaveConfig(*cfgPath, cfg); err != nil {
			log.Printf("Konnte Standard-Konfiguration nicht anlegen: %v", err)
		}
	}

	static, _ := fs.Sub(webFiles, "web")
	srv := NewServer(cfg, *cfgPath, static)

	if err := requireLoopback(*addr); err != nil {
		log.Fatal(err)
	}
	ln, err := listen(*addr)
	if err != nil {
		log.Fatal(err)
	}
	url := srv.LoginURL("http://" + ln.Addr().String())
	fmt.Printf("   Konfiguration: %s\n   Webinterface:  %s\n   Beenden über den Knopf im Webinterface oder mit Strg+C\n\n", *cfgPath, url)
	if !*noBrowser {
		openBrowser(url)
	}
	hs := &http.Server{
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
	}
	go func() {
		if err := hs.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	sig := make(chan os.Signal, 2)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	select {
	case <-sig:
	case <-srv.Done():
	case <-idleTimeout(srv, *idle):
		fmt.Println("   Kein Browser-Tab mehr offen.")
	}
	// Shutdown wartet, bis laufende Anfragen fertig sind, damit ein Einsortieren
	// nicht mittendrin abbricht. Ein zweites Strg+C beendet sofort.
	fmt.Println("   Der Bär räumt noch fertig auf …")
	go func() {
		<-sig
		os.Exit(1)
	}()
	if err := hs.Shutdown(context.Background()); err != nil {
		log.Print(err)
	}
	fmt.Println("   Gute Nacht! ʕ-ᴥ-ʔ")
}

// idleTimeout meldet sich, wenn der Browser länger als d nichts von sich hören
// ließ. Das Webinterface meldet sich alle 30 Sekunden, solange ein Tab offen ist.
func idleTimeout(srv *Server, d time.Duration) <-chan struct{} {
	ch := make(chan struct{})
	if d <= 0 {
		return ch
	}
	go func() {
		t := time.NewTicker(max(min(d/4, 15*time.Second), time.Millisecond))
		defer t.Stop()
		for now := range t.C {
			if srv.IdleFor(now) > d {
				close(ch)
				return
			}
		}
	}()
	return ch
}

// requireLoopback verhindert, dass die Oberfläche im Netzwerk erreichbar wird.
func requireLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "localhost" {
		return nil
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("-addr %s: OrganiBear lauscht aus Sicherheitsgründen nur auf 127.0.0.1, ::1 oder localhost", addr)
	}
	return nil
}

// listen probiert bei belegtem Port die nächsten zehn Ports durch.
func listen(addr string) (net.Listener, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	port, _ := strconv.Atoi(portStr)
	var lastErr error
	for i := 0; i < 10; i++ {
		ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port+i)))
		if err == nil {
			return ln, nil
		}
		lastErr = err
		if port == 0 {
			break
		}
	}
	return nil, lastErr
}

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) // #nosec G204 -- url ist die eigene lokale Adresse, keine Benutzereingabe
	case "darwin":
		cmd = exec.Command("open", url) // #nosec G204 -- url ist die eigene lokale Adresse, keine Benutzereingabe
	default:
		cmd = exec.Command("xdg-open", url) // #nosec G204 -- url ist die eigene lokale Adresse, keine Benutzereingabe
	}
	_ = cmd.Start()
}
