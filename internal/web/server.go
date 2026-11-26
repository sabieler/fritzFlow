// Package web serves the embedded UI and the JSON/SSE API.
package web

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"github.com/sabieler/fritzFlow/internal/browser"
	"github.com/sabieler/fritzFlow/internal/config"
	"github.com/sabieler/fritzFlow/internal/engine"
)

//go:embed static
var static embed.FS

type server struct {
	e           *engine.Engine
	allowRemote bool
}

// Handler returns the HTTP handler. Unless allowRemote is set, only requests
// addressed to localhost are accepted (protects against DNS rebinding).
func Handler(e *engine.Engine, allowRemote bool) http.Handler {
	s := &server{e: e, allowRemote: allowRemote}
	sub, _ := fs.Sub(static, "static")
	files := http.FileServer(http.FS(sub))
	mux := http.NewServeMux()
	// Neutral paths: ad/tracker blockers often block URLs containing "events".
	mux.HandleFunc("/live", s.events)
	mux.HandleFunc("/state", s.state)
	mux.HandleFunc("/api/config", s.config)
	mux.HandleFunc("/api/test", s.test)
	mux.HandleFunc("/api/browsers", s.browsers)
	mux.HandleFunc("/api/browser-test", s.browserTest)
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		files.ServeHTTP(w, r)
	}))
	return s.guard(mux)
}

func isLocalHost(hostport string) bool {
	h := hostport
	if hh, _, err := net.SplitHostPort(hostport); err == nil {
		h = hh
	}
	h = strings.Trim(h, "[]")
	if strings.EqualFold(h, "localhost") {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (s *server) guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				http.Error(w, "interner Fehler", http.StatusInternalServerError)
			}
		}()
		if !s.allowRemote && !isLocalHost(r.Host) {
			http.Error(w, "forbidden host", http.StatusForbidden)
			return
		}
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; connect-src 'self'")
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "cross-site request", http.StatusForbidden)
				return
			}
			if o := r.Header.Get("Origin"); o != "" {
				if u, err := url.Parse(o); err != nil || u.Host != r.Host {
					http.Error(w, "bad origin", http.StatusForbidden)
					return
				}
			}
			if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
				http.Error(w, "json required", http.StatusUnsupportedMediaType)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type configView struct {
	FritzBox    string `json:"fritzbox"`
	Username    string `json:"username"`
	Password    string `json:"password,omitempty"`
	PasswordSet bool   `json:"passwordSet"`
	Interface   string `json:"interface"`
	Snaplen     int    `json:"snaplen"`
	CaptureMode string `json:"captureMode"`
	ReverseDNS  bool   `json:"reverseDns"`
	ReadOnly    bool   `json:"readOnly"`
	Path        string `json:"path"`
	PasswordEnv bool   `json:"passwordEnv"`
	Browser     string `json:"browser"`
	OpenBrowser bool   `json:"openBrowser"`
}

func (s *server) config(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		c := s.e.Config()
		writeJSON(w, http.StatusOK, configView{
			FritzBox: c.FritzBox, Username: c.Username, PasswordSet: c.Password != "",
			Interface: c.Interface, Snaplen: c.Snaplen, CaptureMode: c.CaptureMode,
			ReverseDNS: c.ReverseDNS, ReadOnly: s.e.ReadOnly(), Path: s.e.ConfigPath(),
			PasswordEnv: c.PasswordFromEnv(), Browser: c.Browser, OpenBrowser: c.OpenBrowser,
		})
	case http.MethodPost:
		in, ok := readConfig(w, r)
		if !ok {
			return
		}
		if err := s.e.UpdateConfig(in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Gespeichert"})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func readConfig(w http.ResponseWriter, r *http.Request) (config.Config, bool) {
	var v configView
	if err := json.NewDecoder(io.LimitReader(r.Body, 64*1024)).Decode(&v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "ungültige Anfrage"})
		return config.Config{}, false
	}
	return config.Config{
		FritzBox: v.FritzBox, Username: v.Username, Password: v.Password,
		Interface: v.Interface, Snaplen: v.Snaplen, CaptureMode: v.CaptureMode, ReverseDNS: v.ReverseDNS,
		Browser: v.Browser, OpenBrowser: v.OpenBrowser,
	}, true
}

func (s *server) test(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	in, ok := readConfig(w, r)
	if !ok {
		return
	}
	msg, err := s.e.Test(r.Context(), in)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": msg})
}

// state returns the latest snapshot (polling fallback when SSE is blocked).
func (s *server) state(w http.ResponseWriter, r *http.Request) {
	s.e.Touch()
	data, _ := s.e.Subscribe()
	w.Header().Set("Cache-Control", "no-store")
	if data == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(data)
}

func (s *server) browsers(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"os": runtime.GOOS, "browsers": browser.Detect()})
}

func (s *server) browserTest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var v struct {
		Browser string `json:"browser"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&v); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "message": "ungültige Anfrage"})
		return
	}
	if err := browser.Open(v.Browser, "http://"+r.Host+"/"); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "message": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": "Browser wurde gestartet"})
}

func (s *server) events(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")
	done := s.e.AddViewer()
	defer done()
	_, _ = io.WriteString(w, "retry: 2000\n\n")
	fl.Flush()
	var sent []byte
	ping := time.NewTicker(15 * time.Second)
	defer ping.Stop()
	for {
		data, ch := s.e.Subscribe()
		if data != nil && (sent == nil || &data[0] != &sent[0]) {
			if _, err := w.Write([]byte("data: ")); err != nil {
				return
			}
			if _, err := w.Write(data); err != nil {
				return
			}
			if _, err := w.Write([]byte("\n\n")); err != nil {
				return
			}
			fl.Flush()
			sent = data
		}
		select {
		case <-r.Context().Done():
			return
		case <-ch:
		case <-ping.C:
			if _, err := io.WriteString(w, ": ping\n\n"); err != nil {
				return
			}
			fl.Flush()
		}
	}
}
