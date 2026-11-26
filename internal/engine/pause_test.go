package engine

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"path/filepath"
	"testing"
	"time"

	"github.com/sabieler/fritzFlow/internal/config"
	"github.com/sabieler/fritzFlow/internal/fakebox"
	"github.com/sabieler/fritzFlow/internal/fritz"
)

func waitStatus(t *testing.T, e *Engine, what string, ok func(Status) bool) Status {
	t.Helper()
	deadline := time.After(15 * time.Second)
	for {
		data, ch := e.Subscribe()
		if data != nil {
			var m struct{ Status Status }
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatal(err)
			}
			if ok(m.Status) {
				return m.Status
			}
		}
		select {
		case <-ch:
		case <-deadline:
			t.Fatalf("timeout waiting for %s; last: %.300s", what, data)
		}
	}
}

// The capture steps aside while the line is busy and comes back once it is
// quiet; "resume now" keeps it running despite the load.
func TestAutoPause(t *testing.T) {
	oldPoll, oldQuiet := onlinePoll, autoResumeQuiet
	onlinePoll, autoResumeQuiet = 100*time.Millisecond, 300*time.Millisecond
	defer func() { onlinePoll, autoResumeQuiet = oldPoll, oldQuiet }()

	box, err := fakebox.Start("fritzFlow", "geheim")
	if err != nil {
		t.Fatal(err)
	}
	defer box.Close()
	box.SetWANRate(0.2e6, 0.05e6)

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
	defer e.AddViewer()()

	live := func(s Status) bool { return s.State == StateLive && s.Paused == "" }
	waitStatus(t, e, "live", live)

	box.SetWANRate(12e6, 0.1e6) // ~96 Mbit/s
	st := waitStatus(t, e, "auto pause", func(s Status) bool { return s.State == StatePaused && s.Paused == "auto" })
	if !st.AutoPause || st.Message == "" {
		t.Fatalf("pause status incomplete: %+v", st)
	}
	starts := box.Starts("1-lan")
	time.Sleep(time.Second) // still busy: must stay paused
	if box.Starts("1-lan") != starts {
		t.Fatal("capture restarted while the line was busy")
	}

	box.SetWANRate(0.1e6, 0.02e6)
	waitStatus(t, e, "resume", live)
	if box.Starts("1-lan") <= starts {
		t.Fatal("capture was not restarted after the pause")
	}

	// manual pause and resume
	e.SetPaused(true)
	waitStatus(t, e, "manual pause", func(s Status) bool { return s.State == StatePaused && s.Paused == "manual" })
	e.SetPaused(false)
	waitStatus(t, e, "manual resume", live)

	// "resume now" during an auto pause holds the capture despite load
	box.SetWANRate(12e6, 0)
	waitStatus(t, e, "auto pause 2", func(s Status) bool { return s.Paused == "auto" })
	e.SetPaused(false)
	waitStatus(t, e, "held live", live)
	time.Sleep(time.Second)
	if p, _ := e.pauseState(); p != "" {
		t.Fatalf("auto pause came back despite resume now: %s", p)
	}
}

func TestLineBusyQuiet(t *testing.T) {
	mb := func(v ...float64) []float64 {
		for i := range v {
			v[i] *= 1e6 / 8
		}
		return v
	}
	w := &fritz.WANMonitor{DownHist: mb(1, 1, 50, 60), UpHist: mb(1, 1, 1, 1)}
	if r, busy := lineBusy(w); !busy || r != 50e6/8 {
		t.Fatalf("busy = %v, %v", r, busy)
	}
	w.DownHist = mb(1, 1, 60, 10)
	if _, busy := lineBusy(w); busy {
		t.Fatal("one high sample must not pause")
	}
	w.UpHist = mb(1, 1, 35, 40) // a big upload counts as well
	if _, busy := lineBusy(w); !busy {
		t.Fatal("busy upload not detected")
	}
	q := &fritz.WANMonitor{DownHist: mb(200, 10, 10, 10, 10), UpHist: mb(1, 1, 1, 1, 1)}
	if !lineQuiet(q, 20*time.Second) {
		t.Fatal("last 20 s were quiet")
	}
	if lineQuiet(q, 25*time.Second) {
		t.Fatal("window with 200 Mbit/s is not quiet")
	}
	if lineQuiet(q, time.Minute) {
		t.Fatal("too little history must not count as quiet")
	}
}
