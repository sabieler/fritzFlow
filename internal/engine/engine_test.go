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
