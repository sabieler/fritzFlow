// Package fakebox simulates the parts of a FRITZ!Box that FritzFlow uses:
// login_sid.lua (PBKDF2), the capture stream and the TR-064 host list.
// It powers the -demo mode and the integration tests.
package fakebox

import (
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"math"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/sabieler/fritzFlow/internal/fritz"
	"github.com/sabieler/fritzFlow/internal/pktgen"
)

// Box is a running simulator.
type Box struct {
	URL      string
	User     string
	Password string

	srv       *http.Server
	mu        sync.Mutex
	sids      map[string]bool
	challenge string
	nonce     string
	sim       *simulation
	ctx       context.Context
	cancel    context.CancelFunc
	orphans   map[string]bool // interfaces blocked by an orphaned capture → sticky
	stopAlls  int
	starts    map[string]int // capture starts per interface
}

// Starts reports how often a capture on iface was started.
func (b *Box) Starts(iface string) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.starts[iface]
}

// SimulateOrphanCapture blocks iface (e.g. "1-lan") like a capture whose
// client vanished: starts fail. If sticky, no stop request releases it (like
// FRITZ!OS until its TCP gives up); otherwise "Alle stoppen" does.
func (b *Box) SimulateOrphanCapture(iface string, sticky bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.orphans == nil {
		b.orphans = map[string]bool{}
	}
	b.orphans[iface] = sticky
}

// ReleaseOrphanCapture ends a simulated orphaned capture.
func (b *Box) ReleaseOrphanCapture(iface string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.orphans, iface)
}

// StopAlls reports how often "Alle stoppen" was requested.
func (b *Box) StopAlls() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.stopAlls
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Start launches the simulator on a random localhost port.
func Start(user, password string) (*Box, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &Box{
		URL:       "http://" + ln.Addr().String(),
		User:      user,
		Password:  password,
		sids:      map[string]bool{},
		challenge: fmt.Sprintf("2$%d$%s$%d$%s", 1000, randHex(16), 100, randHex(16)),
		nonce:     randHex(8),
		sim:       newSimulation(),
		ctx:       ctx,
		cancel:    cancel,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/login_sid.lua", b.login)
	mux.HandleFunc("/cgi-bin/capture_notimeout", b.capture)
	mux.HandleFunc("/upnp/control/hosts", b.hosts)
	mux.HandleFunc("/devicehostlist.lua", b.hostList)
	mux.HandleFunc("/data.lua", b.dataLua)
	mux.HandleFunc("/api/v0/generic/cpu", func(w http.ResponseWriter, r *http.Request) {
		if !b.validSID(strings.TrimPrefix(r.Header.Get("Authorization"), "AVM-SID ")) {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		// plausible 24 h curve (newest first, 6-minute steps): busy evenings,
		// quiet nights, a little jitter
		mk := func(base, amp float64) string {
			v := make([]string, 240)
			for i := range v {
				hoursAgo := float64(i) * 0.1
				day := 0.5 + 0.5*math.Sin((20-hoursAgo)/24*2*math.Pi-math.Pi/2)
				jitter := 0.15 * math.Sin(float64(i)*1.7) * math.Sin(float64(i)*0.37)
				v[i] = strconv.Itoa(int(base + amp*(day+jitter)))
			}
			return strings.Join(v, ",")
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{
			"StatCPU": mk(12, 30), "StatTemperature": mk(66, 8), "StatRAMStrictlyUsed": mk(31, 2),
			"StatRAMCacheUsed": mk(28, 2), "StatRAMPhysFree": mk(40, 2), "StatCurrentInterval": "1",
		})
	})
	mux.HandleFunc("/api/v0/generic/power", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"rate_sumact":"31","rate_systemact":"65","rate_wlanact":"100","rate_dspact":"100","rate_usbhostact":"0"}`)
	})
	mux.HandleFunc("/jason_boxinfo.xml", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/xml")
		fmt.Fprint(w, `<j:BoxInfo xmlns:j="http://jason.avm.de/updatecheck/"><j:Name>FRITZ!Box Demo</j:Name><j:Version>164.08.00</j:Version><j:Serial>DEMO</j:Serial><j:Lab></j:Lab></j:BoxInfo>`)
	})
	b.srv = &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = b.srv.Serve(ln) }()
	return b, nil
}

// Close stops the simulator.
func (b *Box) Close() {
	b.cancel()
	c, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = b.srv.Shutdown(c)
}

func (b *Box) validSID(sid string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sids[sid]
}

func (b *Box) login(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	sid := "0000000000000000"
	if s := r.Form.Get("sid"); s != "" && b.validSID(s) {
		if r.Form.Get("logout") != "" {
			b.mu.Lock()
			delete(b.sids, s)
			b.mu.Unlock()
		} else {
			sid = s
		}
	}
	if r.Method == http.MethodPost {
		want, _ := fritz.SolveChallenge(b.challenge, b.Password)
		if r.Form.Get("username") == b.User && r.Form.Get("response") == want {
			sid = randHex(8)
			b.mu.Lock()
			b.sids[sid] = true
			b.mu.Unlock()
		}
	}
	w.Header().Set("Content-Type", "text/xml")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="utf-8"?><SessionInfo><SID>%s</SID><Challenge>%s</Challenge>`+
		`<BlockTime>0</BlockTime><Rights></Rights><Users><User last="1">%s</User></Users></SessionInfo>`,
		sid, b.challenge, html.EscapeString(b.User))
}

func (b *Box) capture(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if !b.validSID(q.Get("sid")) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	if q.Get("capture") != "Start" {
		if q.Get("capture") == "Stop" && q.Get("iface") == "stopall" {
			b.mu.Lock()
			for k, sticky := range b.orphans {
				if !sticky {
					delete(b.orphans, k)
				}
			}
			b.stopAlls++
			b.mu.Unlock()
		}
		w.WriteHeader(http.StatusOK)
		return
	}
	b.mu.Lock()
	_, orphan := b.orphans[q.Get("ifaceorminor")]
	if b.starts == nil {
		b.starts = map[string]int{}
	}
	b.starts[q.Get("ifaceorminor")]++
	b.mu.Unlock()
	if orphan {
		// Like FRITZ!OS while another capture runs: 200 with an empty body.
		w.WriteHeader(http.StatusOK)
		return
	}
	// bitSize 18 bounds the value to 0..262143 already while parsing
	snap := 1600
	if v, err := strconv.ParseUint(q.Get("snaplen"), 10, 18); err == nil && v > 0 {
		snap = int(v)
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	fl, _ := w.(http.Flusher)
	pw, err := pktgen.NewWriter(w, snap)
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		select {
		case <-b.ctx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	b.sim.run(ctx, pw, func() {
		if fl != nil {
			fl.Flush()
		}
	})
}

func (b *Box) checkDigest(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Digest ") {
		return false
	}
	f := map[string]string{}
	for _, part := range strings.Split(h[7:], ",") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) == 2 {
			f[kv[0]] = strings.Trim(kv[1], `"`)
		}
	}
	md := func(s string) string { x := md5.Sum([]byte(s)); return hex.EncodeToString(x[:]) }
	ha1 := md(b.User + ":F!Box SOAP-Auth:" + b.Password)
	ha2 := md(r.Method + ":" + f["uri"])
	want := md(ha1 + ":" + b.nonce + ":" + f["nc"] + ":" + f["cnonce"] + ":" + f["qop"] + ":" + ha2)
	return f["username"] == b.User && f["nonce"] == b.nonce && f["response"] == want
}

func (b *Box) hosts(w http.ResponseWriter, r *http.Request) {
	if !b.checkDigest(r) {
		w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Digest realm="F!Box SOAP-Auth", nonce="%s", algorithm=MD5, qop="auth"`, b.nonce))
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	action := r.Header.Get("SOAPAction")
	if i := strings.IndexByte(action, '#'); i >= 0 {
		action = strings.Trim(action[i+1:], `"`)
	}
	var inner string
	switch action {
	case "X_AVM-DE_GetHostListPath":
		sid := randHex(8)
		b.mu.Lock()
		b.sids[sid] = true
		b.mu.Unlock()
		inner = "<NewX_AVM-DE_HostListPath>/devicehostlist.lua?sid=" + sid + "</NewX_AVM-DE_HostListPath>"
	default:
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError><errorCode>401</errorCode><errorDescription>Invalid Action</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`)
		return
	}
	w.Header().Set("Content-Type", `text/xml; charset="utf-8"`)
	fmt.Fprintf(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body>`+
		`<u:%sResponse xmlns:u="urn:dslforum-org:service:Hosts:1">%s</u:%sResponse></s:Body></s:Envelope>`, action, inner, action)
}

func (b *Box) hostList(w http.ResponseWriter, r *http.Request) {
	if !b.validSID(r.URL.Query().Get("sid")) {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/xml")
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><List>`)
	write := func(i int, ip, mac, name, iface string, active int) {
		fmt.Fprintf(&sb, "<Item><Index>%d</Index><IPAddress>%s</IPAddress><MACAddress>%s</MACAddress><Active>%d</Active><HostName>%s</HostName><InterfaceType>%s</InterfaceType></Item>",
			i, ip, strings.ToUpper(mac), active, html.EscapeString(name), iface)
	}
	for i, d := range b.sim.devs {
		if d.anonymous {
			continue
		}
		write(i+1, d.ip4.String(), macStr(d.mac), d.name, d.iface, 1)
	}
	write(99, "192.168.178.77", "3c:22:fb:00:00:77", "Gaeste-Laptop", "802.11", 0)
	sb.WriteString(`</List>`)
	fmt.Fprint(w, sb.String())
}

// dataLua serves page=netDev like the FRITZ!OS web UI.
func (b *Box) dataLua(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !b.validSID(r.Form.Get("sid")) || r.Form.Get("page") != "netDev" {
		w.WriteHeader(http.StatusForbidden)
		return
	}
	type ip struct {
		IP string `json:"ip"`
	}
	type dev struct {
		Name string `json:"name"`
		MAC  string `json:"mac"`
		Type string `json:"type"`
		IPv4 ip     `json:"ipv4"`
	}
	var active []dev
	for _, d := range b.sim.devs {
		if d.anonymous {
			continue
		}
		t := "lan"
		if d.iface == "802.11" {
			t = "wlan"
		}
		active = append(active, dev{Name: d.name, MAC: strings.ToUpper(macStr(d.mac)), Type: t, IPv4: ip{d.ip4.String()}})
	}
	out := map[string]any{"data": map[string]any{
		"active":  active,
		"passive": []dev{{Name: "Gaeste-Laptop", MAC: "3C:22:FB:00:00:77", Type: "wlan", IPv4: ip{"192.168.178.77"}}},
		"fbox":    []dev{{Name: "fritz.demo", MAC: strings.ToUpper(macStr(b.sim.router)), Type: "unknown", IPv4: ip{b.sim.routerV4.String()}}},
	}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func macStr(m pktgen.MAC) string {
	return fmt.Sprintf("%02x:%02x:%02x:%02x:%02x:%02x", m[0], m[1], m[2], m[3], m[4], m[5])
}
