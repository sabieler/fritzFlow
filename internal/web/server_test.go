package web

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sabieler/fritzFlow/internal/config"
	"github.com/sabieler/fritzFlow/internal/engine"
)

func newHandler(t *testing.T, allowRemote bool) http.Handler {
	e := engine.New(config.Default(), engine.Options{
		ConfigPath: filepath.Join(t.TempDir(), "c.json"), ReadOnly: true, Logger: log.New(io.Discard, "", 0),
	})
	return Handler(e, allowRemote)
}

func post(h http.Handler, remote, host, path, body string, hdr map[string]string) int {
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	r.RemoteAddr, r.Host = remote, host
	r.Header.Set("Content-Type", "application/json")
	for k, v := range hdr {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w.Code
}

func TestSettingsOnlyFromThisComputer(t *testing.T) {
	h := newHandler(t, true) // UI shared in the LAN
	for _, p := range []string{"/api/config", "/api/test", "/api/browser-test"} {
		if c := post(h, "192.168.178.50:51234", "192.168.178.10:8484", p, `{"browser":"rm -rf /"}`, nil); c != http.StatusForbidden {
			t.Errorf("%s from LAN: got %d, want 403", p, c)
		}
	}
	// from the computer itself the request reaches the handler (read-only config -> 400)
	if c := post(h, "127.0.0.1:50000", "localhost:8484", "/api/config", `{}`, nil); c != http.StatusBadRequest {
		t.Errorf("local config save: got %d, want 400 (read-only)", c)
	}
}

func TestCrossSiteAndRebindingBlocked(t *testing.T) {
	h := newHandler(t, false)
	if c := post(h, "127.0.0.1:1", "localhost:8484", "/api/config", `{}`, map[string]string{"Origin": "https://evil.example"}); c != http.StatusForbidden {
		t.Errorf("foreign origin: got %d", c)
	}
	if c := post(h, "127.0.0.1:1", "evil.example:8484", "/api/config", `{}`, nil); c != http.StatusForbidden {
		t.Errorf("DNS rebinding host: got %d", c)
	}
}
