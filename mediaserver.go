package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Nach dem Einsortieren lässt OrganiBear eingetragene Mediaserver ihre
// Bibliothek neu einlesen, damit die neuen Filme und Folgen sofort auftauchen.

var mediaHTTP = &http.Client{Timeout: 15 * time.Second}

// refreshServer bittet einen Mediaserver, seine Bibliothek neu einzulesen.
// kind ist "plex", "jellyfin" oder "kodi".
func refreshServer(ctx context.Context, kind string, s MediaServer) error {
	base, err := normalizeServerURL(s.URL)
	if err != nil {
		return err
	}
	if base == "" {
		return errors.New("keine Adresse eingetragen")
	}
	var req *http.Request
	switch kind {
	case "plex":
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, base+"/library/sections/all/refresh", nil)
		if err == nil {
			req.Header.Set("X-Plex-Token", s.Token)
		}
	case "jellyfin":
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/Library/Refresh", nil)
		if err == nil {
			req.Header.Set("Authorization", `MediaBrowser Token="`+strings.ReplaceAll(s.Token, `"`, "")+`"`)
		}
	case "kodi":
		body := []byte(`{"jsonrpc":"2.0","method":"VideoLibrary.Scan","id":1}`)
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, base+"/jsonrpc", bytes.NewReader(body))
		if err == nil {
			req.Header.Set("Content-Type", "application/json")
			if s.User != "" || s.Password != "" {
				req.SetBasicAuth(s.User, s.Password)
			}
		}
	default:
		return fmt.Errorf("unbekannter Mediaserver %q", kind)
	}
	if err != nil {
		return err
	}
	resp, err := mediaHTTP.Do(req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err
		}
		return fmt.Errorf("nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errors.New("Zugangsdaten abgelehnt")
	case resp.StatusCode < 200 || resp.StatusCode > 299:
		return fmt.Errorf("antwortet mit %s", resp.Status)
	}
	if kind == "kodi" {
		var r struct {
			Result string `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(data, &r) != nil || r.Result != "OK" {
			if r.Error != nil {
				return errors.New(r.Error.Message)
			}
			return errors.New("unerwartete Antwort, ist das wirklich Kodi?")
		}
	}
	return nil
}

var serverNames = map[string]string{"plex": "Plex", "jellyfin": "Jellyfin", "kodi": "Kodi"}

// RefreshLibraries stößt alle eingetragenen Mediaserver an und liefert pro
// Server eine kurze Meldung für den Bären.
func RefreshLibraries(ctx context.Context, x Extras) []string {
	var out []string
	for _, s := range []struct {
		kind string
		srv  MediaServer
	}{{"plex", x.Plex}, {"jellyfin", x.Jellyfin}, {"kodi", x.Kodi}} {
		if s.srv.URL == "" {
			continue
		}
		if err := refreshServer(ctx, s.kind, s.srv); err != nil {
			out = append(out, serverNames[s.kind]+": "+err.Error())
		} else {
			out = append(out, serverNames[s.kind]+" liest die Bibliothek neu ein")
		}
	}
	return out
}
