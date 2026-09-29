// OrganiBear bringt Filme und Serien mit wilden Dateinamen in eine ordentliche,
// frei definierbare Ordnerstruktur. Ein einzelnes Programm mit Webinterface.
package main

import (
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
	"path/filepath"
	"runtime"
	"strconv"
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

	ln, err := listen(*addr)
	if err != nil {
		log.Fatal(err)
	}
	url := "http://" + ln.Addr().String()
	fmt.Printf("   Konfiguration: %s\n   Webinterface:  %s\n   Beenden mit Strg+C\n\n", *cfgPath, url)
	if !*noBrowser {
		openBrowser(url)
	}
	log.Fatal(http.Serve(ln, srv.Handler()))
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
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
