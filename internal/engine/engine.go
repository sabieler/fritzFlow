// Package engine wires configuration, the FRITZ!Box client and the flow
// tracker together and keeps everything running: reconnects with backoff,
// recovers from panics and only captures while somebody is watching.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sabieler/fritzFlow/internal/config"
	"github.com/sabieler/fritzFlow/internal/flow"
	"github.com/sabieler/fritzFlow/internal/fritz"
	"github.com/sabieler/fritzFlow/internal/pcap"
)

// States reported to the UI.
const (
	StateSetup      = "setup"
	StateIdle       = "idle"
	StateConnecting = "connecting"
	StateLive       = "live"
	StateError      = "error"
)

const (
	topConns       = 400
	idleTimeout    = 45 * time.Second
	viewerGrace    = 20 * time.Second
	hostsInterval  = 5 * time.Minute
	maxBackoff     = time.Minute
	authBackoffMin = time.Minute
)

// Status describes what the engine is doing.
type Status struct {
	State      string            `json:"state"`
	Message    string            `json:"message"`
	Since      int64             `json:"since"`
	Box        string            `json:"box"`
	Iface      string            `json:"iface"`
	Snaplen    int               `json:"snaplen"`
	Mode       string            `json:"mode"`
	StreamRate float64           `json:"streamRate"`
	HostsInfo  string            `json:"hostsInfo"`
	Demo       bool              `json:"demo"`
	ReadOnly   bool              `json:"readOnly"`
	Version    string            `json:"version"`
	Viewers    int32             `json:"viewers"`
	BoxName    string            `json:"boxName"`
	BoxModel   string            `json:"boxModel"`
	BoxOS      string            `json:"boxOS"`
	BoxStats   bool              `json:"boxStats"`
	Load       *fritz.BoxLoad    `json:"load,omitempty"`
	WAN        *fritz.WANMonitor `json:"wan,omitempty"`
	LoadInfo   string            `json:"loadInfo,omitempty"`
}

type message struct {
	Status Status `json:"status"`
	*flow.Snapshot
}

// Options configure the engine.
type Options struct {
	ConfigPath string
	ReadOnly   bool // demo mode: config is not persisted/changeable
	Demo       bool
	Version    string
	Logger     *log.Logger
}

// Engine is the application core.
type Engine struct {
	opt Options
	log *log.Logger

	mu        sync.Mutex
	cfg       config.Config
	client    *fritz.Client
	status    Status
	tracker   *flow.Tracker
	hostsKick chan struct{}

	viewers    atomic.Int32
	lastViewer atomic.Int64
	wake       chan struct{}
	restart    chan struct{}
	received   atomic.Uint64
	sid        atomic.Value // current session ID (string) while capturing
	lostAt     atomic.Int64 // when the box last ended our capture (unix nano)

	pubMu  sync.Mutex
	latest []byte
	notify chan struct{}
}

// New creates an engine.
func New(cfg config.Config, opt Options) *Engine {
	if opt.Logger == nil {
		opt.Logger = log.Default()
	}
	e := &Engine{
		opt:       opt,
		log:       opt.Logger,
		cfg:       cfg,
		wake:      make(chan struct{}, 1),
		restart:   make(chan struct{}, 1),
		hostsKick: make(chan struct{}, 1),
		notify:    make(chan struct{}),
		tracker:   flow.New(flow.Options{ReverseDNS: cfg.ReverseDNS}),
	}
	e.status = Status{State: StateIdle, Since: time.Now().Unix()}
	return e
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Config returns the current configuration.
func (e *Engine) Config() config.Config {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.cfg
}

// ReadOnly reports whether the configuration can be changed.
func (e *Engine) ReadOnly() bool { return e.opt.ReadOnly }

// ConfigPath returns where the configuration is stored.
func (e *Engine) ConfigPath() string { return e.opt.ConfigPath }

// Merge prepares a config coming from the UI: an empty password keeps the
// stored one; settings not exposed in the UI are carried over.
func (e *Engine) Merge(in config.Config) config.Config {
	cur := e.Config()
	in = in.WithPasswordFrom(cur)
	in.Listen = cur.Listen
	in.InsecureTLS = cur.InsecureTLS
	in.TR064URL = cur.TR064URL
	in.Normalize()
	return in
}

// UpdateConfig validates, persists and applies a new configuration.
func (e *Engine) UpdateConfig(in config.Config) error {
	if e.opt.ReadOnly {
		return errors.New("im Demo-Modus kann die Konfiguration nicht geändert werden")
	}
	in = e.Merge(in)
	if err := in.Validate(); err != nil {
		return err
	}
	if err := config.Save(e.opt.ConfigPath, in); err != nil {
		return fmt.Errorf("konnte Konfiguration nicht speichern: %w", err)
	}
	e.mu.Lock()
	e.cfg = in // reverse-DNS changes take effect after a program restart
	e.client = nil
	e.mu.Unlock()
	e.log.Printf("Konfiguration gespeichert (%s)", e.opt.ConfigPath)
	signal(e.restart)
	signal(e.wake)
	return nil
}

// Test checks a configuration by logging in (and reading the host list).
func (e *Engine) Test(ctx context.Context, in config.Config) (string, error) {
	in = e.Merge(in)
	if err := in.Validate(); err != nil {
		return "", err
	}
	if in.Password == "" {
		return "", errors.New("bitte Passwort eingeben")
	}
	cl, err := fritz.New(in.FritzBox, in.TR064URL, in.Username, in.Password, in.InsecureTLS)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	sid, err := cl.Login(ctx)
	if err != nil {
		return "", err
	}
	defer cl.Logout(context.Background(), sid)
	msg := "Anmeldung erfolgreich ✓"
	if hs, err := cl.Hosts(ctx); err == nil {
		active := 0
		for _, h := range hs {
			if h.Active {
				active++
			}
		}
		msg += fmt.Sprintf(" · %d Geräte bekannt, %d online", len(hs), active)
	} else {
		msg += " · Gerätenamen nicht abrufbar (TR-064)"
	}
	return msg, nil
}

func (e *Engine) clientFor(cfg config.Config) (*fritz.Client, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.client != nil {
		return e.client, nil
	}
	cl, err := fritz.New(cfg.FritzBox, cfg.TR064URL, cfg.Username, cfg.Password, cfg.InsecureTLS)
	if err != nil {
		return nil, err
	}
	e.client = cl
	return cl, nil
}

func (e *Engine) trk() *flow.Tracker {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.tracker
}

func (e *Engine) setStatus(state, msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.status.State != state || e.status.Message != msg {
		if e.status.State != state {
			e.status.Since = time.Now().Unix()
		}
		if msg != "" && (state == StateError || state != e.status.State) {
			e.log.Printf("[%s] %s", state, msg)
		}
		e.status.State, e.status.Message = state, msg
	}
}

func (e *Engine) setHostsInfo(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if s != e.status.HostsInfo && s != "" {
		e.log.Printf("Geräteliste: %s", s)
	}
	e.status.HostsInfo = s
}

func (e *Engine) state() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.status.State
}

// AddViewer registers a UI client; the returned func unregisters it.
func (e *Engine) AddViewer() func() {
	e.viewers.Add(1)
	signal(e.wake)
	var once sync.Once
	return func() {
		once.Do(func() {
			if e.viewers.Add(-1) <= 0 {
				e.lastViewer.Store(time.Now().UnixNano())
			}
		})
	}
}

// Touch registers activity of a polling client (keeps on-demand capture alive
// for the grace period).
func (e *Engine) Touch() {
	e.lastViewer.Store(time.Now().UnixNano())
	signal(e.wake)
}

func (e *Engine) wanted(cfg config.Config) bool {
	if cfg.CaptureMode == config.ModeAlways || e.viewers.Load() > 0 {
		return true
	}
	return time.Since(time.Unix(0, e.lastViewer.Load())) < viewerGrace
}

// Subscribe returns the latest snapshot JSON and a channel closed on the next update.
func (e *Engine) Subscribe() ([]byte, <-chan struct{}) {
	e.pubMu.Lock()
	defer e.pubMu.Unlock()
	return e.latest, e.notify
}

func (e *Engine) publish(b []byte) {
	e.pubMu.Lock()
	e.latest = b
	close(e.notify)
	e.notify = make(chan struct{})
	e.pubMu.Unlock()
}

// Run blocks until ctx is cancelled.
func (e *Engine) Run(ctx context.Context) {
	e.trk().Start(ctx)
	var wg sync.WaitGroup
	e.supervise(ctx, &wg, "tick", e.tickLoop)
	e.supervise(ctx, &wg, "capture", e.captureLoop)
	e.supervise(ctx, &wg, "hosts", e.hostsLoop)
	e.supervise(ctx, &wg, "load", e.loadLoop)
	wg.Wait()
}

// supervise runs fn and restarts it after a panic.
func (e *Engine) supervise(ctx context.Context, wg *sync.WaitGroup, name string, fn func(context.Context)) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			finished := func() (ok bool) {
				defer func() {
					if r := recover(); r != nil {
						e.log.Printf("FEHLER in %s (wird neu gestartet): %v\n%s", name, r, debug.Stack())
						ok = false
					}
				}()
				fn(ctx)
				return true
			}()
			if finished || ctx.Err() != nil {
				return
			}
			if !sleep(ctx, time.Second) {
				return
			}
		}
	}()
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (e *Engine) tickLoop(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	prevRecv, prevT := e.received.Load(), time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			snap := e.trk().Snapshot(now, topConns)
			recv := e.received.Load()
			dt := now.Sub(prevT).Seconds()
			streamRate := 0.0
			if dt > 0 {
				streamRate = float64(recv-prevRecv) / dt
			}
			prevRecv, prevT = recv, now
			if e.viewers.Load() == 0 && time.Since(time.Unix(0, e.lastViewer.Load())) > viewerGrace {
				continue // nobody listens: skip JSON encoding
			}
			e.mu.Lock()
			st := e.status
			cfg := e.cfg
			e.mu.Unlock()
			st.StreamRate = streamRate
			st.Box, st.Iface, st.Snaplen, st.Mode = cfg.FritzBox, cfg.Interface, cfg.Snaplen, cfg.CaptureMode
			st.Demo, st.ReadOnly, st.Version = e.opt.Demo, e.opt.ReadOnly, e.opt.Version
			st.Viewers = e.viewers.Load()
			st.BoxStats = cfg.BoxStats
			if !cfg.BoxStats {
				st.Load, st.LoadInfo, st.WAN = nil, "", nil
			}
			b, err := json.Marshal(message{Status: st, Snapshot: snap})
			if err != nil {
				e.log.Printf("JSON-Fehler: %v", err)
				continue
			}
			e.publish(b)
		}
	}
}

func (e *Engine) waitFor(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-e.restart:
	case <-e.wake:
	case <-t.C:
	}
	return true
}

func (e *Engine) captureLoop(ctx context.Context) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		cfg := e.Config()
		if !cfg.Configured() {
			e.setStatus(StateSetup, "Bitte FRITZ!Box-Zugangsdaten eingeben")
			if !e.waitFor(ctx, time.Hour) {
				return
			}
			continue
		}
		if !e.wanted(cfg) {
			e.setStatus(StateIdle, "Pausiert – Mitschnitt läuft nur bei geöffneter Ansicht")
			if !e.waitFor(ctx, 2*time.Second) {
				return
			}
			continue
		}
		start := time.Now()
		err := e.captureOnce(ctx, cfg)
		if ctx.Err() != nil {
			return
		}
		if err == nil || errors.Is(err, context.Canceled) {
			backoff = 2 * time.Second
			continue
		}
		if time.Since(start) > 2*time.Minute {
			backoff = 2 * time.Second // was running fine for a while
		}
		wait := backoff
		var blocked *fritz.BlockedError
		switch {
		case errors.As(err, &blocked):
			wait = max(blocked.Wait+time.Second, backoff)
		case errors.Is(err, fritz.ErrAuth):
			wait = max(authBackoffMin, backoff) // never hammer the login
		}
		e.setStatus(StateError, fmt.Sprintf("%v – neuer Versuch in %d s", err, int(wait.Seconds())))
		backoff = min(backoff*2, maxBackoff)
		t := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			t.Stop()
			return
		case <-e.restart:
			backoff = 2 * time.Second
		case <-t.C:
		}
		t.Stop()
	}
}

func (e *Engine) captureOnce(parent context.Context, cfg config.Config) error {
	cl, err := e.clientFor(cfg)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	// Stop capturing when the config changes or nobody watches any more.
	go func() {
		t := time.NewTicker(time.Second)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-e.restart:
				cancel()
				return
			case <-t.C:
				if !e.wanted(cfg) {
					cancel()
					return
				}
			}
		}
	}()

	e.setStatus(StateConnecting, "Anmeldung an der FRITZ!Box …")
	sid, err := cl.Login(ctx)
	if err != nil {
		return err
	}
	e.sid.Store(sid)
	go e.learnBoxAddresses(ctx, cfg)
	go e.learnBoxInfo(ctx, cl)
	defer func() {
		e.sid.Store("")
		c2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()
		cl.StopCapture(c2, sid, cfg.Interface)
		cl.Logout(c2, sid)
	}()
	e.setStatus(StateConnecting, "Starte Mitschnitt auf "+cfg.Interface+" …")
	tr := e.trk()
	var live atomic.Bool
	handle := func(p pcap.Packet) {
		if !live.Load() {
			live.Store(true)
			e.setStatus(StateLive, "")
			signal(e.hostsKick)
		}
		tr.Handle(p)
	}
	err = cl.Capture(ctx, sid, cfg.Interface, cfg.Snaplen, idleTimeout, &e.received, handle)
	if errors.Is(err, fritz.ErrStreamEnded) {
		e.lostAt.Store(time.Now().UnixNano())
	}
	if errors.Is(err, fritz.ErrBusy) && time.Since(time.Unix(0, e.lostAt.Load())) < 3*time.Minute {
		// Our own capture was ended recently, so somebody else (another
		// FritzFlow, the FRITZ!Box UI) is capturing now: don't fight over it.
		return fmt.Errorf("auf %s läuft bereits ein anderer Mitschnitt (zweite FritzFlow-Instanz oder FRITZ!Box-Oberfläche?)", cfg.Interface)
	}
	if errors.Is(err, fritz.ErrBusy) && ctx.Err() == nil {
		// A capture left over from an earlier run blocks the interface:
		// stop it and try once more right away.
		e.log.Printf("Hängender Mitschnitt auf %s – beende ihn und starte neu", cfg.Interface)
		sctx, scancel := context.WithTimeout(ctx, 8*time.Second)
		cl.StopCapture(sctx, sid, cfg.Interface)
		scancel()
		if sleep(ctx, time.Second) {
			err = cl.Capture(ctx, sid, cfg.Interface, cfg.Snaplen, idleTimeout, &e.received, handle)
		}
	}
	if ctx.Err() != nil && parent.Err() == nil {
		return nil // stopped on purpose (restart / no viewers)
	}
	return err
}

// learnBoxAddresses resolves the box host name so that traffic to the box's
// own (public) IPv6 address is not shown as internet traffic.
func (e *Engine) learnBoxAddresses(ctx context.Context, cfg config.Config) {
	u, err := url.Parse(cfg.FritzBox)
	if err != nil {
		return
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	addrs, err := net.DefaultResolver.LookupNetIP(lctx, "ip", u.Hostname())
	if err != nil {
		return
	}
	e.trk().AddRouterIPs(addrs)
}

// learnBoxInfo reads model and firmware version of the box.
func (e *Engine) learnBoxInfo(ctx context.Context, cl *fritz.Client) {
	ictx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	info, err := cl.Info(ictx)
	if err != nil {
		return
	}
	e.mu.Lock()
	changed := e.status.BoxModel != info.Model || e.status.BoxOS != info.Firmware
	e.status.BoxModel, e.status.BoxOS = info.Model, info.Firmware
	e.mu.Unlock()
	if changed && info.Model != "" {
		e.log.Printf("%s, FRITZ!OS %s", info.Model, info.Firmware)
	}
}

func (e *Engine) setBoxName(name string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status.BoxName = name
}

// loadLoop polls the box utilisation every 30 s while capturing.
func (e *Engine) loadLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	var last time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := e.Config()
		sid, _ := e.sid.Load().(string)
		if !cfg.BoxStats || sid == "" || e.state() != StateLive {
			continue
		}
		cl, err := e.clientFor(cfg)
		if err != nil {
			continue
		}
		wctx, wcancel := context.WithTimeout(ctx, 4*time.Second)
		if w, err := cl.Online(wctx, sid); err == nil {
			e.mu.Lock()
			e.status.WAN = w
			e.mu.Unlock()
		}
		wcancel()
		if time.Since(last) < 30*time.Second {
			continue
		}
		last = time.Now()
		lctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		l, err := cl.Load(lctx, sid)
		cancel()
		e.mu.Lock()
		if err != nil {
			e.status.LoadInfo = "Auslastung nicht verfügbar: " + err.Error()
			if errors.Is(err, fritz.ErrNoLoadAPI) {
				last = time.Now().Add(time.Hour) // don't ask again soon
			}
		} else {
			e.status.Load, e.status.LoadInfo = l, ""
		}
		e.mu.Unlock()
	}
}

func (e *Engine) hostsLoop(ctx context.Context) {
	var last time.Time
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		kicked := false
		select {
		case <-ctx.Done():
			return
		case <-e.hostsKick:
			kicked = true
		case <-t.C:
		}
		if e.state() != StateLive || (!kicked && time.Since(last) < hostsInterval) {
			continue
		}
		if kicked && time.Since(last) < 30*time.Second {
			continue
		}
		last = time.Now()
		cfg := e.Config()
		cl, err := e.clientFor(cfg)
		if err != nil {
			continue
		}
		hctx, cancel := context.WithTimeout(ctx, 60*time.Second)
		var hs []fritz.Host
		err = errors.New("keine Sitzung")
		// Heimnetz list from the web UI: includes the box itself (its name)
		// and works even when TR-064 is disabled.
		if sid, _ := e.sid.Load().(string); sid != "" {
			hs, err = cl.NetDevices(hctx, sid)
		}
		if err != nil { // fall back to the official TR-064 API
			if hs2, err2 := cl.Hosts(hctx); err2 == nil {
				hs, err = hs2, nil
			} else {
				err = fmt.Errorf("%v; %v", err, err2)
			}
		}
		cancel()
		if err != nil {
			e.setHostsInfo("Gerätenamen nicht verfügbar: " + err.Error())
			last = time.Now().Add(-hostsInterval + time.Minute) // retry in a minute
			continue
		}
		e.setHostsInfo("")
		fh := make([]flow.Host, 0, len(hs))
		for _, h := range hs {
			if h.Router && h.Name != "" {
				e.setBoxName(h.Name)
			}
			fh = append(fh, flow.Host{MAC: h.MAC, IP: h.IP, Name: h.Name, Iface: h.Iface, Active: h.Active, Router: h.Router, Net: h.Net})
		}
		e.trk().SetHosts(fh)
	}
}
