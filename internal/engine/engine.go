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
	busyBackoffMax = 15 * time.Second
	// fallbackIface is the routing interface: it sees all routed traffic
	// with LAN addresses (before NAT) and is not blocked when 1-lan is.
	fallbackIface = "3-0"
	// guestIface is the guest network bridge (separate from 1-lan).
	guestIface = "1-guest"
	// guestIdleTimeout is long: a guest network is often silent for a while.
	guestIdleTimeout = 10 * time.Minute
)

var (
	// fallbackProbe is how often a blocked interface is checked during fallback.
	fallbackProbe = 30 * time.Second
	// exitCheck is how often exitWhenUnused looks for connected browsers.
	exitCheck = 5 * time.Second
	// guestRetry is the pause before restarting a failed guest capture.
	guestRetry = 30 * time.Second
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
	Fallback   string            `json:"fallback,omitempty"` // set while capturing fallbackIface instead
	GuestInfo  string            `json:"guestInfo,omitempty"` // why the guest network is not captured
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
	// ExitAfter, if not nil, overrides the configured exit_after_minutes:
	// Run ends once no browser has been connected for this long (0 = never).
	// Never applies in capture mode "always".
	ExitAfter *time.Duration
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
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	e.trk().Start(ctx)
	var wg sync.WaitGroup
	e.supervise(ctx, &wg, "exit", func(ctx context.Context) { e.exitWhenUnused(ctx, cancel) })
	e.supervise(ctx, &wg, "tick", e.tickLoop)
	e.supervise(ctx, &wg, "capture", e.captureLoop)
	e.supervise(ctx, &wg, "hosts", e.hostsLoop)
	e.supervise(ctx, &wg, "load", e.loadLoop)
	wg.Wait()
}

// exitWhenUnused calls stop once no browser has been connected for the
// configured time (counted from program start if none ever connected), so
// the process does not keep running in the background after the tab is
// closed. The setting is re-read on every check and applies at once.
func (e *Engine) exitWhenUnused(ctx context.Context, stop context.CancelFunc) {
	start := time.Now()
	t := time.NewTicker(exitCheck)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		cfg := e.Config()
		after := cfg.ExitAfter()
		if e.opt.ExitAfter != nil {
			after = *e.opt.ExitAfter
		}
		if after <= 0 || e.viewers.Load() > 0 || cfg.CaptureMode == config.ModeAlways {
			continue
		}
		last := time.Unix(0, e.lastViewer.Load())
		if last.Before(start) {
			last = start
		}
		if time.Since(last) >= after {
			e.log.Printf("Seit %s ist kein Browser mehr verbunden – FritzFlow beendet sich", after)
			stop()
			return
		}
	}
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
			if st.Fallback != "" {
				st.Iface = fallbackIface
			}
			if cfg.CaptureGuest && st.GuestInfo == "" && st.State == StateLive {
				st.Iface += " + " + guestIface
			}
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
		case errors.Is(err, fritz.ErrBusy):
			wait = min(backoff, busyBackoffMax) // stops were already tried: retry soon
		case errors.Is(err, fritz.ErrNetChanged):
			wait, backoff = time.Second, 2*time.Second // new network is usually up already
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
	tr := e.trk()
	var guest sync.WaitGroup
	if cfg.CaptureGuest && cfg.Interface != guestIface {
		guest.Add(1)
		go func() {
			defer guest.Done()
			e.guestLoop(ctx, cl, sid, cfg, tr.Handle)
		}()
	}
	defer func() {
		cancel()
		guest.Wait() // the guest capture stops itself; it needs the session until then
		e.sid.Store("")
		c2, cancel2 := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel2()
		cl.StopCapture(c2, sid, cfg.Interface)
		cl.Logout(c2, sid)
	}()
	e.setStatus(StateConnecting, "Starte Mitschnitt auf "+cfg.Interface+" …")
	var live atomic.Bool
	handle := func(p pcap.Packet) {
		if !live.Load() {
			live.Store(true)
			e.setStatus(StateLive, "")
			signal(e.hostsKick)
		}
		tr.Handle(p)
	}
	capture := func() error {
		err := cl.Capture(ctx, sid, cfg.Interface, cfg.Snaplen, idleTimeout, &e.received, handle)
		switch {
		case errors.Is(err, fritz.ErrStreamEnded):
			// The box closed the stream itself: somebody stopped it on purpose.
			e.lostAt.Store(time.Now().UnixNano())
		case errors.Is(err, fritz.ErrBusy), ctx.Err() != nil:
		default:
			// Our side lost the stream (network switch, timeout): a capture
			// that blocks the interface afterwards is our own orphan.
			e.lostAt.Store(0)
		}
		return err
	}
	err = capture()
	if errors.Is(err, fritz.ErrBusy) && time.Since(time.Unix(0, e.lostAt.Load())) < 3*time.Minute {
		// The box ended our capture recently, so somebody else (another
		// FritzFlow, the FRITZ!Box UI) is capturing now: don't fight over it.
		return fmt.Errorf("auf %s läuft bereits ein anderer Mitschnitt (zweite FritzFlow-Instanz oder FRITZ!Box-Oberfläche?)", cfg.Interface)
	}
	// A capture left over from an earlier run or a vanished network
	// connection blocks the interface. Stop it and retry; if the box ignores
	// the per-interface stop (seen with orphaned captures on FRITZ!OS 8.x),
	// press "Alle stoppen".
	for step := 0; step < 2 && errors.Is(err, fritz.ErrBusy) && ctx.Err() == nil; step++ {
		var sctx context.Context
		var scancel context.CancelFunc
		if step == 0 {
			e.log.Printf("Hängender Mitschnitt auf %s – beende ihn und starte neu", cfg.Interface)
			sctx, scancel = context.WithTimeout(ctx, 8*time.Second)
			cl.StopCapture(sctx, sid, cfg.Interface)
		} else {
			e.log.Printf("Mitschnitt auf %s lässt sich nicht einzeln beenden – stoppe alle Mitschnitte der FRITZ!Box", cfg.Interface)
			// The box only answers once every capture has ended; don't wait for that.
			sctx, scancel = context.WithTimeout(ctx, 3*time.Second)
			cl.StopAllCaptures(sctx, sid)
		}
		scancel()
		if !sleep(ctx, time.Second) {
			break
		}
		err = capture()
	}
	if ctx.Err() != nil && parent.Err() == nil {
		return nil // stopped on purpose (restart / no viewers)
	}
	if errors.Is(err, fritz.ErrBusy) && cfg.Interface != fallbackIface {
		ferr := e.captureFallback(ctx, cl, sid, cfg, handle)
		switch {
		case ctx.Err() != nil && parent.Err() == nil:
			return nil
		case ferr == nil || !errors.Is(ferr, fritz.ErrBusy):
			return ferr // back on cfg.Interface, or the fallback stream broke (e.g. another network change)
		}
		// The fallback is blocked as well (an earlier fallback stream got
		// orphaned by a second network change).
		return fmt.Errorf("%w: auf %s und %s hängen noch alte Mitschnitte (mehrfacher LAN/WLAN-Wechsel?). "+
			"Die FRITZ!Box gibt sie erst nach einiger Zeit (im Test ca. 16 Minuten) von selbst frei – sofort hilft nur ein Neustart der Box – FritzFlow verbindet sich dann automatisch",
			fritz.ErrBusy, cfg.Interface, fallbackIface)
	}
	if errors.Is(err, fritz.ErrBusy) {
		// Typically our own capture whose connection vanished (adapter
		// switched off): the box keeps sending into the void until its TCP
		// gives up (about 16 minutes in a test with a 7590), and no stop request ends it earlier.
		return fmt.Errorf("%w: auf %s hängt noch ein alter Mitschnitt (z. B. nach LAN/WLAN-Wechsel). "+
			"Die FRITZ!Box gibt ihn erst nach einiger Zeit (im Test ca. 16 Minuten) von selbst frei – sofort hilft nur ein Neustart der Box – FritzFlow verbindet sich dann automatisch", fritz.ErrBusy, cfg.Interface)
	}
	return err
}

// captureFallback captures the routing interface while cfg.Interface is
// blocked by an orphaned capture, and returns nil as soon as cfg.Interface
// can be captured again. It returns an error if the fallback fails, too.
func (e *Engine) captureFallback(ctx context.Context, cl *fritz.Client, sid string, cfg config.Config, handle func(pcap.Packet)) error {
	e.log.Printf("%s ist noch durch einen alten Mitschnitt belegt – eingeschränkte Ansicht über %s, bis %s wieder frei ist",
		cfg.Interface, fallbackIface, cfg.Interface)
	e.setFallback(fmt.Sprintf("Eingeschränkte Ansicht: Auf %s hängt noch ein alter Mitschnitt (z. B. nach LAN/WLAN-Wechsel), "+
		"den die FRITZ!Box erst nach einigen Minuten freigibt. Bis dahin zeigt FritzFlow nur Verkehr über den Router "+
		"(kein Verkehr zwischen Geräten im Heimnetz) und wechselt danach automatisch zurück.", cfg.Interface))
	defer e.setFallback("")
	fctx, fcancel := context.WithCancel(ctx)
	defer fcancel()
	var freed atomic.Bool
	go func() {
		t := time.NewTicker(fallbackProbe)
		defer t.Stop()
		for {
			select {
			case <-fctx.Done():
				return
			case <-t.C:
			}
			if cl.CaptureAvailable(fctx, sid, cfg.Interface) {
				freed.Store(true)
				fcancel()
				return
			}
		}
	}()
	err := cl.Capture(fctx, sid, fallbackIface, cfg.Snaplen, idleTimeout, &e.received, handle)
	sctx, scancel := context.WithTimeout(context.Background(), 8*time.Second)
	cl.StopCapture(sctx, sid, fallbackIface)
	scancel()
	if freed.Load() {
		e.log.Printf("%s ist wieder frei – wechsle zurück", cfg.Interface)
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

// guestLoop captures the guest network next to the main interface while ctx
// lives. Problems are shown as a hint but never stop the main capture.
func (e *Engine) guestLoop(ctx context.Context, cl *fritz.Client, sid string, cfg config.Config, handle func(pcap.Packet)) {
	defer e.setGuestInfo("")
	defer func() {
		c, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		cl.StopCapture(c, sid, guestIface)
	}()
	stopped := false
	for ctx.Err() == nil {
		var got atomic.Bool
		err := cl.Capture(ctx, sid, guestIface, cfg.Snaplen, guestIdleTimeout, &e.received, func(p pcap.Packet) {
			if !got.Swap(true) {
				e.setGuestInfo("")
			}
			handle(p)
		})
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, fritz.ErrBusy) && !stopped {
			stopped = true // a leftover guest capture: stop it once and retry at once
			sctx, scancel := context.WithTimeout(ctx, 8*time.Second)
			cl.StopCapture(sctx, sid, guestIface)
			scancel()
			continue
		}
		if !got.Load() && !errors.Is(err, fritz.ErrIdle) { // a quiet guest network is fine
			e.setGuestInfo("Gastnetz wird nicht mitgeschnitten: " + err.Error())
		}
		if !sleep(ctx, guestRetry) {
			return
		}
	}
}

func (e *Engine) setGuestInfo(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if msg != "" && msg != e.status.GuestInfo {
		e.log.Print(msg)
	}
	e.status.GuestInfo = msg
}

func (e *Engine) setFallback(msg string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.status.Fallback = msg
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
