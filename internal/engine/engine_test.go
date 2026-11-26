package engine

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sabieler/fritzFlow/internal/config"
	"github.com/sabieler/fritzFlow/internal/fakebox"
)

// End-to-end: login (PBKDF2), capture stream, TR-064 digest auth + host list,
// flow tracking and JSON snapshots – against the simulated FRITZ!Box.
func TestEngineAgainstFakeBox(t *testing.T) {
	box, err := fakebox.Start("fritzFlow", "geheim")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()

	cfg := config.Default()
	cfg.FritzBox, cfg.TR064URL = box.URL, box.URL
	cfg.Username, cfg.Password = "fritzFlow", "geheim"
	cfg.ReverseDNS = false
	cfg.Normalize()
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0)})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	leave := e.AddViewer()
	defer leave()

	deadline := time.After(15 * time.Second)
	for {
		data, ch := e.Subscribe()
		if data != nil {
			var m struct {
				Status  Status `json:"status"`
				Devices []struct {
					Name string `json:"name"`
				} `json:"devices"`
				Conns []struct {
					Host string `json:"host"`
					Kind string `json:"kind"`
				} `json:"conns"`
			}
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			named, hosts := 0, 0
			for _, d := range m.Devices {
				if d.Name == "iPhone-15" || d.Name == "Samsung-TV" {
					named++
				}
			}
			for _, c := range m.Conns {
				if c.Kind == "wan" && strings.Contains(c.Host, ".") {
					hosts++
				}
			}
			if m.Status.State == StateLive && named == 2 && hosts >= 5 {
				return // success
			}
		}
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("no complete snapshot in time; last: %.400s", data)
		}
	}
}

func TestWrongPasswordReportsError(t *testing.T) {
	box, err := fakebox.Start("fritzFlow", "richtig")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	cfg := config.Default()
	cfg.FritzBox, cfg.TR064URL = box.URL, box.URL
	cfg.Username, cfg.Password = "fritzFlow", "falsch"
	cfg.Normalize()
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0)})
	if _, err := e.Test(context.Background(), cfg); err == nil || !strings.Contains(err.Error(), "Passwort") {
		t.Fatalf("expected auth error, got %v", err)
	}
	cfg.Password = "richtig"
	msg, err := e.Test(context.Background(), cfg)
	if err != nil || !strings.Contains(msg, "erfolgreich") {
		t.Fatalf("expected success, got %q %v", msg, err)
	}
}

// An orphaned capture (client vanished after a LAN/WLAN switch) ignores the
// per-interface stop; the engine must fall back to "Alle stoppen".
func TestRecoversFromOrphanedCapture(t *testing.T) {
	box, err := fakebox.Start("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	box.SimulateOrphanCapture("1-lan", false)
	cfg := config.Default()
	cfg.FritzBox, cfg.TR064URL, cfg.Username, cfg.Password = box.URL, box.URL, "u", "p"
	cfg.ReverseDNS = false
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)
	defer e.AddViewer()()
	for i := 0; i < 100 && e.state() != StateLive; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if s := e.state(); s != StateLive {
		t.Fatalf("expected live after stopping the orphan, got %s (%s)", s, e.status.Message)
	}
	if n := box.StopAlls(); n != 1 {
		t.Fatalf("expected exactly one \"Alle stoppen\", got %d", n)
	}
}

// After the box itself ended our stream, a busy interface means somebody
// else captures: the engine must not stop all captures then.
func TestBusyAfterBoxEndedStreamDoesNotStopAll(t *testing.T) {
	box, err := fakebox.Start("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	box.SimulateOrphanCapture("1-lan", false)
	cfg := config.Default()
	cfg.FritzBox, cfg.TR064URL, cfg.Username, cfg.Password = box.URL, box.URL, "u", "p"
	cfg.ReverseDNS = false
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0)})
	e.lostAt.Store(time.Now().UnixNano())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)
	defer e.AddViewer()()
	for i := 0; i < 50 && e.state() != StateError; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if s := e.state(); s != StateError {
		t.Fatalf("expected error state, got %s", s)
	}
	if n := box.StopAlls(); n != 0 {
		t.Fatalf("must not stop other captures, got %d stop-alls", n)
	}
}

// An orphan that no stop request ends: capture the routing interface
// meanwhile and switch back once the box releases the interface.
func TestFallbackWhileInterfaceBlocked(t *testing.T) {
	fallbackProbe = 200 * time.Millisecond
	box, err := fakebox.Start("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	box.SimulateOrphanCapture("1-lan", true)
	cfg := config.Default()
	cfg.FritzBox, cfg.TR064URL, cfg.Username, cfg.Password = box.URL, box.URL, "u", "p"
	cfg.ReverseDNS = false
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)
	defer e.AddViewer()()
	status := func() Status { e.mu.Lock(); defer e.mu.Unlock(); return e.status }
	waitFor := func(what string, ok func(Status) bool) {
		t.Helper()
		for i := 0; i < 150; i++ {
			if ok(status()) {
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatalf("timeout waiting for %s; status %+v", what, status())
	}
	waitFor("fallback live", func(s Status) bool { return s.State == StateLive && s.Fallback != "" })
	box.ReleaseOrphanCapture("1-lan")
	waitFor("back on 1-lan", func(s Status) bool { return s.State == StateLive && s.Fallback == "" })
}

// Run ends by itself once no browser has been connected for ExitAfter.
func TestExitsWhenBrowserClosed(t *testing.T) {
	exitCheck = 50 * time.Millisecond
	cfg := config.Default()
	after := 500 * time.Millisecond
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0),
		ExitAfter: &after})
	done := make(chan struct{})
	go func() { e.Run(context.Background()); close(done) }()
	leave := e.AddViewer()
	select {
	case <-done:
		t.Fatal("must keep running while a browser is connected")
	case <-time.After(1200 * time.Millisecond):
	}
	leave()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("did not exit after the browser was closed")
	}
}

func TestOnDemandStopsWithoutViewers(t *testing.T) {
	box, err := fakebox.Start("u", "p")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	cfg := config.Default()
	cfg.FritzBox, cfg.TR064URL, cfg.Username, cfg.Password = box.URL, box.URL, "u", "p"
	cfg.ReverseDNS = false
	e := New(cfg, Options{ConfigPath: filepath.Join(t.TempDir(), "c.json"), Logger: log.New(io.Discard, "", 0)})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go e.Run(ctx)
	time.Sleep(1500 * time.Millisecond)
	if s := e.state(); s != StateIdle {
		t.Fatalf("without viewers state should be idle, got %s", s)
	}
	leave := e.AddViewer()
	for i := 0; i < 50 && e.state() != StateLive; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if s := e.state(); s != StateLive {
		t.Fatalf("with viewer state should be live, got %s", s)
	}
	leave()
	e.lastViewer.Store(time.Now().Add(-time.Hour).UnixNano()) // skip grace period
	for i := 0; i < 50 && e.state() != StateIdle; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if s := e.state(); s != StateIdle {
		t.Fatalf("after viewer left state should be idle again, got %s", s)
	}
}
