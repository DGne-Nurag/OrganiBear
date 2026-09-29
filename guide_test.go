package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestTMDBKeyCheck(t *testing.T) {
	tmdb := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/authentication" {
			http.NotFound(w, r)
			return
		}
		key := r.URL.Query().Get("api_key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if key != "gut" && key != "eyJgut" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"success":true}`))
	}))
	defer tmdb.Close()

	s := NewServer(DefaultConfig(), filepath.Join(t.TempDir(), "cfg.json"), fstest.MapFS{"index.html": {Data: []byte("hi")}})
	s.tmdbBase = tmdb.URL
	h := s.Handler()
	check := func(key string) (int, string) {
		body, _ := json.Marshal(map[string]string{"key": key})
		r := httptest.NewRequest("POST", "/api/tmdb/test", strings.NewReader(string(body)))
		r.Host = "127.0.0.1:8765"
		r.AddCookie(&http.Cookie{Name: cookieName, Value: s.token})
		r.Header.Set("X-OrganiBear", "1")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w.Code, w.Body.String()
	}

	for _, tc := range []struct {
		key  string
		code int
		msg  string
	}{
		{" gut ", http.StatusOK, "passt"},
		{"eyJgut", http.StatusOK, "passt"}, // Read Access Token als Bearer
		{"falsch", http.StatusBadGateway, "lehnt den API-Key ab"},
		{"", http.StatusBadGateway, "kein API-Key"},
	} {
		code, body := check(tc.key)
		if code != tc.code || !strings.Contains(body, tc.msg) {
			t.Errorf("Key %q: %d %s", tc.key, code, body)
		}
		if strings.Contains(body, "falsch") && tc.key == "falsch" {
			t.Errorf("Fehlermeldung enthält den Key: %s", body)
		}
	}
}
