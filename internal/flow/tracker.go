// Package flow turns a stream of captured frames into a live table of
// devices and connections ("who talks to whom, and where").
package flow

import (
	"context"
	"net"
	"net/netip"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sabieler/fritzFlow/internal/pcap"
)

// Host is a device known to the FRITZ!Box (from TR-064).
type Host struct {
	MAC    string
	IP     string
	Name   string
	Iface  string
	Active bool
	Router bool   // the FRITZ!Box itself
	Net    string // network segment label (LAN, WLAN 5 GHz, Gast …)
}

// Options tune the tracker.
type Options struct {
	ReverseDNS bool
	MaxConns   int
}

const (
	kindWAN uint8 = iota
	kindRouter
	kindLAN
)

var kindNames = [...]string{"wan", "router", "lan"}

const (
	srcNone uint8 = iota
	srcPTR
	srcDNS
	srcTLS
)

const (
	maxDevices   = 1024
	maxNames     = 100_000
	maxRouterMAC = 8
	nameMaxAge   = 12 * time.Hour
)

type connKey struct {
	proto uint8
	lport uint16
	rport uint16
	lip   netip.Addr
	rip   netip.Addr
}

type conn struct {
	id             uint64
	key            connKey
	dev            *device
	kind           uint8
	host           string
	hostSrc        uint8
	first, last    time.Time
	tx, rx         uint64
	ptx, prx       uint64
	txRate, rxRate float64
	finTx, finRx   bool
	rst            bool
	synOut         bool // we saw the local side open the connection
	synAck         bool // the remote side answered the handshake
	rstIn          bool // reset sent by the remote side
	unreach        bool // ICMP destination unreachable for this flow
}

// failure classifies connections that never got established.
func (c *conn) failure(now time.Time) string {
	switch {
	case c.unreach:
		return "unreachable"
	case c.key.proto == protoTCP && c.synOut && !c.synAck && c.rstIn:
		return "refused"
	case c.key.proto == protoTCP && c.synOut && !c.synAck && now.Sub(c.first) > 4*time.Second:
		return "timeout"
	}
	return ""
}

func (c *conn) closed() bool { return c.rst || (c.finTx && c.finRx) }

type device struct {
	key            string
	mac            string
	ips            map[netip.Addr]time.Time
	name           string
	iface          string
	net            string
	online         bool // reported active by the FRITZ!Box
	first, last    time.Time
	tx, rx         uint64
	ptx, prx       uint64
	txRate, rxRate float64
	conns          int
}

type dnsKey struct{ dev, name string }

type dnsFail struct {
	name, dev, reason string
	count             int
	first, last       time.Time
}

type dnsPendKey struct {
	client netip.Addr
	port   uint16
	id     uint16
}

type dnsPend struct {
	name, dev string
	at        time.Time
}

const (
	maxDNSFails   = 2000
	maxDNSPending = 20000
	dnsTimeout    = 5 * time.Second
	dnsFailKeep   = 30 * time.Minute
)

func rcodeText(rc uint8) string {
	switch rc {
	case 2:
		return "Serverfehler (SERVFAIL)"
	case 3:
		return "Name existiert nicht (NXDOMAIN)"
	case 5:
		return "Anfrage abgelehnt (REFUSED)"
	}
	return "DNS-Fehler (Code " + strconv.Itoa(int(rc)) + ")"
}

func (t *Tracker) recordDNSFail(name, dev, reason string, now time.Time) {
	if name == "" || strings.HasSuffix(name, ".arpa") {
		return
	}
	k := dnsKey{dev: dev, name: name}
	f := t.dnsFails[k]
	if f == nil {
		if len(t.dnsFails) >= maxDNSFails {
			return
		}
		f = &dnsFail{name: name, dev: dev, first: now}
		t.dnsFails[k] = f
	}
	f.reason, f.last = reason, now
	f.count++
}

func devKeyOf(m mac, hasMAC bool, ip netip.Addr) string {
	if hasMAC {
		return m.String()
	}
	return ip.String()
}

type nameEntry struct {
	name string
	src  uint8
	at   time.Time
}

// Tracker aggregates packets. All methods are safe for concurrent use.
type Tracker struct {
	opt Options

	mu         sync.Mutex
	conns      map[connKey]*conn
	devs       map[string]*device
	routerMACs map[mac]struct{}
	routerIPs  map[netip.Addr]struct{}
	names      map[netip.Addr]nameEntry
	mdns       map[netip.Addr]string
	hostsByMAC map[string]Host
	hostsByIP  map[netip.Addr]Host
	nextID     uint64

	packets, bytes uint64
	ppackets       uint64
	wanRx, wanTx   uint64
	pwanRx, pwanTx uint64
	dropped        uint64
	lastTick       time.Time
	lastSweep      time.Time
	rxRate, txRate float64
	pps            float64

	dnsFails   map[dnsKey]*dnsFail
	dnsPending map[dnsPendKey]dnsPend

	panics  atomic.Uint64
	ptrQ    chan netip.Addr
	ptrSeen map[netip.Addr]struct{}
}

// New creates a tracker.
func New(opt Options) *Tracker {
	if opt.MaxConns <= 0 {
		opt.MaxConns = 50_000
	}
	now := time.Now()
	return &Tracker{
		opt:        opt,
		conns:      make(map[connKey]*conn),
		devs:       make(map[string]*device),
		routerMACs: make(map[mac]struct{}),
		routerIPs:  make(map[netip.Addr]struct{}),
		names:      make(map[netip.Addr]nameEntry),
		mdns:       make(map[netip.Addr]string),
		hostsByMAC: make(map[string]Host),
		hostsByIP:  make(map[netip.Addr]Host),
		lastTick:   now,
		lastSweep:  now,
		ptrQ:       make(chan netip.Addr, 512),
		ptrSeen:    make(map[netip.Addr]struct{}),
		dnsFails:   make(map[dnsKey]*dnsFail),
		dnsPending: make(map[dnsPendKey]dnsPend),
	}
}

// Start runs background reverse-DNS workers until ctx is done.
func (t *Tracker) Start(ctx context.Context) {
	if !t.opt.ReverseDNS {
		return
	}
	for i := 0; i < 3; i++ {
		go t.ptrWorker(ctx)
	}
}

// Panics returns how many packets caused a recovered panic (should be 0).
func (t *Tracker) Panics() uint64 { return t.panics.Load() }

func (t *Tracker) ptrWorker(ctx context.Context) {
	defer func() { _ = recover() }()
	for {
		select {
		case <-ctx.Done():
			return
		case ip := <-t.ptrQ:
			lctx, cancel := context.WithTimeout(ctx, 2*time.Second)
			names, err := net.DefaultResolver.LookupAddr(lctx, ip.String())
			cancel()
			if err == nil && len(names) > 0 {
				if n := cleanHost([]byte(names[0])); n != "" {
					t.mu.Lock()
					if e, ok := t.names[ip]; !ok || e.src <= srcPTR {
						t.names[ip] = nameEntry{name: n, src: srcPTR, at: time.Now()}
					}
					t.mu.Unlock()
				}
			}
			select { // gentle pacing
			case <-ctx.Done():
				return
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

func isPublic(a netip.Addr) bool {
	return a.IsGlobalUnicast() && !a.IsPrivate()
}

func isLocalIP(a netip.Addr) bool {
	return a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLoopback()
}

func isBroadcast(a netip.Addr) bool {
	if !a.Is4() {
		return false
	}
	b := a.As4()
	return b[3] == 255 || a == netip.AddrFrom4([4]byte{255, 255, 255, 255})
}

// Handle processes one captured packet. It never panics.
func (t *Tracker) Handle(pk pcap.Packet) {
	defer func() {
		if r := recover(); r != nil {
			t.panics.Add(1)
		}
	}()
	var p packet
	ok := decodePacket(pk.Link, pk.Data, &p)
	n := uint64(max(pk.OrigLen, len(pk.Data)))
	now := time.Now()

	t.mu.Lock()
	defer t.mu.Unlock()
	t.packets++
	t.bytes += n
	if !ok || !p.src.IsValid() || !p.dst.IsValid() {
		return
	}
	p.src, p.dst = p.src.Unmap(), p.dst.Unmap()

	// Learn the router's MAC: public IPv4 addresses are only reachable through it.
	if p.hasMAC && p.src.Is4() && p.dst.Is4() {
		if isPublic(p.src) {
			t.learnRouter(p.srcMAC)
		}
		if isPublic(p.dst) {
			t.learnRouter(p.dstMAC)
		}
	}

	// Name learning from DNS / mDNS responses (also for multicast).
	if p.proto == protoUDP && len(p.payload) > 12 {
		msg := p.payload
		switch {
		case p.dport == 53 && msg[2]&0x80 == 0: // query
			if name, _, ok := readName(msg, 12); ok && len(t.dnsPending) < maxDNSPending {
				t.dnsPending[dnsPendKey{p.src, p.sport, be16(msg)}] = dnsPend{name: name, dev: devKeyOf(p.srcMAC, p.hasMAC, p.src), at: now}
			}
		case p.sport == 53 && msg[2]&0x80 != 0: // response
			pk := dnsPendKey{p.dst, p.dport, be16(msg)}
			pend, had := t.dnsPending[pk]
			delete(t.dnsPending, pk)
			if rc := msg[3] & 0x0f; rc != 0 {
				name, _, _ := readName(msg, 12)
				dev := devKeyOf(p.dstMAC, p.hasMAC, p.dst)
				if had {
					name, dev = pend.name, pend.dev
				}
				t.recordDNSFail(name, dev, rcodeText(rc), now)
			}
		}
		switch {
		case p.sport == 53:
			parseDNS(p.payload, func(qname, owner string, ip netip.Addr, _ uint32) {
				name := qname
				if name == "" {
					name = owner
				}
				if name == "fritz.box" || name == "myfritz.box" {
					t.addRouterIP(ip.Unmap())
				} else if name != "" && !strings.HasSuffix(name, ".arpa") {
					t.setName(ip.Unmap(), name, srcDNS, now)
				}
			})
		case p.sport == 5353:
			parseDNS(p.payload, func(_, owner string, ip netip.Addr, _ uint32) {
				if strings.HasSuffix(owner, ".local") && len(t.mdns) < maxDevices*4 {
					t.mdns[ip.Unmap()] = strings.TrimSuffix(owner, ".local")
				}
			})
		}
	}

	if p.dst.IsMulticast() || isBroadcast(p.dst) || (p.hasMAC && p.dstMAC[0]&1 == 1) {
		return
	}
	if p.proto == protoICMP || p.proto == protoICMPv6 {
		if k, _, ok := icmpUnreachable(p.proto, p.payload); ok {
			if c := t.conns[k]; c != nil {
				c.unreach = true
			} else if c := t.conns[connKey{proto: k.proto, lip: k.rip, lport: k.rport, rip: k.lip, rport: k.lport}]; c != nil {
				c.unreach = true
			}
			return // error messages are not connections of their own
		}
	}

	var out bool
	var kind uint8
	srcR, dstR := false, false
	if p.hasMAC {
		_, srcR = t.routerMACs[p.srcMAC]
		_, dstR = t.routerMACs[p.dstMAC]
	}
	switch {
	case p.hasMAC && len(t.routerMACs) > 0 && (srcR || dstR):
		if srcR && dstR {
			return
		}
		out = dstR
		remote := p.dst
		if srcR {
			remote = p.src
			if isLocalIP(p.src) && len(t.routerIPs) < 64 {
				t.routerIPs[p.src] = struct{}{}
			}
		}
		kind = kindWAN
		if _, own := t.routerIPs[remote]; own || !isPublic(remote) {
			kind = kindRouter
		}
	default:
		sl, dl := isLocalIP(p.src), isLocalIP(p.dst)
		switch {
		case sl && dl:
			out, kind = true, kindLAN
			if _, ok := t.routerIPs[p.dst]; ok {
				kind = kindRouter
			} else if _, ok := t.routerIPs[p.src]; ok {
				out, kind = false, kindRouter
			}
		case sl:
			out, kind = true, kindWAN
		case dl:
			out, kind = false, kindWAN
		default:
			return
		}
	}

	k := connKey{proto: p.proto}
	var lmac mac
	if out {
		k.lip, k.lport, k.rip, k.rport = p.src, p.sport, p.dst, p.dport
		lmac = p.srcMAC
	} else {
		k.lip, k.lport, k.rip, k.rport = p.dst, p.dport, p.src, p.sport
		lmac = p.dstMAC
	}
	c := t.conns[k]
	if c == nil && kind == kindLAN {
		rk := connKey{proto: k.proto, lip: k.rip, lport: k.rport, rip: k.lip, rport: k.lport}
		if rc := t.conns[rk]; rc != nil {
			c, k, out = rc, rk, !out
		}
	}
	if c == nil {
		if len(t.conns) >= t.opt.MaxConns {
			t.dropped++
			return
		}
		dev := t.device(lmac, p.hasMAC, k.lip, now)
		if dev == nil {
			t.dropped++
			return
		}
		t.nextID++
		c = &conn{id: t.nextID, key: k, dev: dev, kind: kind, first: now}
		t.resolve(c, now)
		t.conns[k] = c
	}
	c.last = now
	c.dev.last = now
	if out {
		c.tx += n
		c.dev.tx += n
	} else {
		c.rx += n
		c.dev.rx += n
	}
	if c.kind == kindWAN {
		if out {
			t.wanTx += n
		} else {
			t.wanRx += n
		}
	}

	if p.proto == protoTCP && p.hasPorts {
		f := p.tcpFlags
		if f&tcpSYN != 0 && f&tcpACK == 0 && c.closed() {
			c.finTx, c.finRx, c.rst = false, false, false // port reuse
			c.synAck, c.rstIn, c.unreach = false, false, false
			c.first = now
		}
		if f&tcpSYN != 0 && f&tcpACK == 0 && out {
			c.synOut = true
		}
		if !out && f&tcpRST == 0 {
			c.synAck = true // any reply from the remote side (not just SYN-ACK)
		}
		if f&tcpRST != 0 {
			c.rst = true
			if !out {
				c.rstIn = true
			}
		}
		if f&tcpFIN != 0 {
			if out {
				c.finTx = true
			} else {
				c.finRx = true
			}
		}
		if out && len(p.payload) > 5 && c.hostSrc < srcTLS {
			h := parseSNI(p.payload)
			if h == "" {
				h = parseHTTPHost(p.payload)
			}
			if h != "" {
				c.host, c.hostSrc = h, srcTLS
				t.setName(k.rip, h, srcTLS, now)
			}
		}
	}
}

func (t *Tracker) learnRouter(m mac) {
	if _, ok := t.routerMACs[m]; !ok && len(t.routerMACs) < maxRouterMAC && m[0]&1 == 0 {
		t.routerMACs[m] = struct{}{}
		// a device entry that was created for the router MAC is bogus
		delete(t.devs, m.String())
	}
}

func (t *Tracker) addRouterIP(ip netip.Addr) {
	if ip.IsValid() && len(t.routerIPs) < 64 {
		t.routerIPs[ip] = struct{}{}
	}
}

// AddRouterIPs marks addresses that belong to the FRITZ!Box itself.
func (t *Tracker) AddRouterIPs(ips []netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, ip := range ips {
		t.addRouterIP(ip.Unmap())
	}
}

func (t *Tracker) setName(ip netip.Addr, name string, src uint8, now time.Time) {
	if e, ok := t.names[ip]; ok && e.src > src && now.Sub(e.at) < nameMaxAge {
		return
	}
	if len(t.names) >= maxNames {
		i := 0
		for k := range t.names { // random eviction of half the cache
			delete(t.names, k)
			if i++; i > maxNames/2 {
				break
			}
		}
	}
	t.names[ip] = nameEntry{name: name, src: src, at: now}
}

// resolve fills c.host from what we know about the remote address.
func (t *Tracker) resolve(c *conn, now time.Time) {
	switch c.kind {
	case kindRouter:
		c.host, c.hostSrc = "FRITZ!Box", srcTLS
		return
	case kindLAN:
		if h, ok := t.hostsByIP[c.key.rip]; ok && h.Name != "" {
			c.host, c.hostSrc = h.Name, srcTLS
		} else if n := t.mdns[c.key.rip]; n != "" {
			c.host, c.hostSrc = n, srcDNS
		}
		return
	}
	if e, ok := t.names[c.key.rip]; ok && e.src > c.hostSrc {
		c.host, c.hostSrc = e.name, e.src
		return
	}
	if c.hostSrc == srcNone && t.opt.ReverseDNS {
		if _, seen := t.ptrSeen[c.key.rip]; !seen {
			if len(t.ptrSeen) > maxNames {
				t.ptrSeen = make(map[netip.Addr]struct{})
			}
			t.ptrSeen[c.key.rip] = struct{}{}
			select {
			case t.ptrQ <- c.key.rip:
			default:
				delete(t.ptrSeen, c.key.rip) // queue full: retry later
			}
		}
	}
}

func (t *Tracker) device(m mac, hasMAC bool, ip netip.Addr, now time.Time) *device {
	key := ip.String()
	if hasMAC {
		key = m.String()
	}
	d := t.devs[key]
	if d == nil {
		if len(t.devs) >= maxDevices {
			return nil
		}
		d = &device{key: key, ips: make(map[netip.Addr]time.Time), first: now}
		if hasMAC {
			d.mac = key
		}
		t.devs[key] = d
		t.applyHost(d)
	}
	if _, ok := d.ips[ip]; !ok {
		if len(d.ips) >= 8 {
			var oldest netip.Addr
			var ot time.Time
			for a, at := range d.ips {
				if !oldest.IsValid() || at.Before(ot) {
					oldest, ot = a, at
				}
			}
			delete(d.ips, oldest)
		}
		d.ips[ip] = now
		if d.name == "" {
			t.applyHost(d)
		}
	} else if now.Sub(d.ips[ip]) > time.Second {
		d.ips[ip] = now
	}
	return d
}

func (t *Tracker) applyHost(d *device) {
	if h, ok := t.hostsByMAC[d.mac]; ok && d.mac != "" {
		d.name, d.iface, d.online, d.net = h.Name, h.Iface, h.Active, h.Net
		return
	}
	for ip := range d.ips {
		if h, ok := t.hostsByIP[ip]; ok {
			d.name, d.iface, d.online, d.net = h.Name, h.Iface, h.Active, h.Net
			return
		}
	}
}

// SetHosts installs the device list from the FRITZ!Box.
func (t *Tracker) SetHosts(hs []Host) {
	now := time.Now()
	t.mu.Lock()
	defer t.mu.Unlock()
	t.hostsByMAC = make(map[string]Host, len(hs))
	t.hostsByIP = make(map[netip.Addr]Host, len(hs))
	hs = append([]Host(nil), hs...)
	for i := range hs {
		hs[i].MAC = strings.ToLower(strings.TrimSpace(hs[i].MAC))
	}
	for _, h := range hs {
		if h.Router {
			if m, err := net.ParseMAC(h.MAC); err == nil && len(m) == 6 {
				t.learnRouter(mac(m))
			}
			if ip, err := netip.ParseAddr(h.IP); err == nil {
				t.addRouterIP(ip.Unmap())
			}
			continue
		}
		if h.MAC != "" {
			t.hostsByMAC[h.MAC] = h
		}
		if ip, err := netip.ParseAddr(h.IP); err == nil {
			t.hostsByIP[ip.Unmap()] = h
		}
	}
	for _, d := range t.devs {
		d.online = false
		t.applyHost(d)
	}
	for _, h := range hs {
		if !h.Active || h.Router || len(t.devs) >= maxDevices {
			continue
		}
		if _, err := net.ParseMAC(h.MAC); err != nil {
			continue
		}
		if _, ok := t.devs[h.MAC]; ok {
			continue
		}
		d := &device{key: h.MAC, mac: h.MAC, ips: make(map[netip.Addr]time.Time), first: now}
		if ip, err := netip.ParseAddr(h.IP); err == nil {
			d.ips[ip.Unmap()] = time.Time{}
		}
		t.applyHost(d)
		t.devs[h.MAC] = d
	}
	for k, c := range t.conns {
		if _, own := t.routerIPs[k.rip]; own && c.kind != kindRouter {
			c.kind = kindRouter
			c.host, c.hostSrc = "", srcNone
		}
		if c.kind != kindWAN && c.hostSrc < srcTLS {
			t.resolve(c, now)
		}
	}
}

// ---- snapshots -------------------------------------------------------------

// Totals summarizes the whole network.
type Totals struct {
	RxRate   float64 `json:"rxRate"` // internet download, bytes/s
	TxRate   float64 `json:"txRate"` // internet upload, bytes/s
	Rx       uint64  `json:"rx"`
	Tx       uint64  `json:"tx"`
	Conns    int     `json:"conns"`
	ConnsWAN int     `json:"connsWan"`
	TCP      int     `json:"tcp"`
	UDP      int     `json:"udp"`
	Active   int     `json:"active"` // devices with traffic in the last minute
	Online   int     `json:"online"`
	PPS      float64 `json:"pps"`
	Packets  uint64  `json:"packets"`
	Dropped  uint64  `json:"dropped"`
	Failed   int     `json:"failed"`
	DNSFail  int     `json:"dnsFail"`
}

// DeviceView is a device as shown in the UI.
type DeviceView struct {
	ID     string   `json:"id"`
	Name   string   `json:"name"`
	IP     string   `json:"ip"`
	IPs    []string `json:"ips,omitempty"`
	MAC    string   `json:"mac"`
	Iface  string   `json:"iface"`
	Net    string   `json:"net"`
	Rx     uint64   `json:"rx"`
	Tx     uint64   `json:"tx"`
	RxRate float64  `json:"rxRate"`
	TxRate float64  `json:"txRate"`
	Conns  int      `json:"conns"`
	Online bool     `json:"online"`
	Idle   float64  `json:"idle"` // seconds since last packet, -1 if never
}

// ConnView is a connection as shown in the UI.
type ConnView struct {
	ID     uint64  `json:"id"`
	Dev    string  `json:"dev"`
	Proto  string  `json:"proto"`
	LIP    string  `json:"lip"`
	LPort  uint16  `json:"lport"`
	RIP    string  `json:"rip"`
	RPort  uint16  `json:"rport"`
	Host   string  `json:"host"`
	Svc    string  `json:"svc"`
	Kind   string  `json:"kind"`
	Rx     uint64  `json:"rx"`
	Tx     uint64  `json:"tx"`
	RxRate float64 `json:"rxRate"`
	TxRate float64 `json:"txRate"`
	Age    float64 `json:"age"`
	Idle   float64 `json:"idle"`
	Closed bool    `json:"closed"`
	Fail   string  `json:"fail,omitempty"` // timeout | refused | unreachable
}

// DNSFailView is a name that could not be resolved.
type DNSFailView struct {
	Name   string  `json:"name"`
	Dev    string  `json:"dev"`
	Reason string  `json:"reason"`
	Count  int     `json:"count"`
	Ago    float64 `json:"ago"`
}

// Snapshot is the complete state pushed to the UI.
type Snapshot struct {
	Time    int64         `json:"t"`
	Totals  Totals        `json:"totals"`
	Devices []DeviceView  `json:"devices"`
	Conns   []ConnView    `json:"conns"`
	DNS     []DNSFailView `json:"dnsFails"`
}

func timeoutFor(c *conn, now time.Time) time.Duration {
	switch {
	case c.failure(now) != "":
		return 2 * time.Minute
	case c.closed():
		return 10 * time.Second
	case c.key.proto == protoTCP:
		return 5 * time.Minute
	case c.key.proto == protoUDP:
		return 90 * time.Second
	}
	return 30 * time.Second
}

// Snapshot computes rates, expires stale entries and returns the top conns.
func (t *Tracker) Snapshot(now time.Time, topN int) *Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	dt := now.Sub(t.lastTick).Seconds()
	if dt < 0.05 {
		dt = 0.05
	}
	t.lastTick = now
	rate := func(cur uint64, prev *uint64) float64 {
		r := float64(cur-*prev) / dt
		*prev = cur
		return r
	}
	t.rxRate = rate(t.wanRx, &t.pwanRx)
	t.txRate = rate(t.wanTx, &t.pwanTx)
	t.pps = rate(t.packets, &t.ppackets)

	s := &Snapshot{Time: now.UnixMilli()}
	for _, d := range t.devs {
		d.conns = 0
	}
	all := make([]*conn, 0, len(t.conns))
	for k, c := range t.conns {
		if now.Sub(c.last) > timeoutFor(c, now) {
			delete(t.conns, k)
			continue
		}
		c.rxRate = rate(c.rx, &c.prx)
		c.txRate = rate(c.tx, &c.ptx)
		if c.hostSrc < srcDNS {
			t.resolve(c, now)
		}
		c.dev.conns++
		s.Totals.Conns++
		if c.failure(now) != "" {
			s.Totals.Failed++
		}
		if c.kind == kindWAN {
			s.Totals.ConnsWAN++
		}
		switch c.key.proto {
		case protoTCP:
			s.Totals.TCP++
		case protoUDP:
			s.Totals.UDP++
		}
		all = append(all, c)
	}

	devs := make([]*device, 0, len(t.devs))
	for k, d := range t.devs {
		d.rxRate = rate(d.rx, &d.prx)
		d.txRate = rate(d.tx, &d.ptx)
		seen := !d.last.IsZero()
		if d.conns == 0 && !d.online && (!seen || now.Sub(d.last) > 2*time.Hour) {
			delete(t.devs, k)
			continue
		}
		if seen && now.Sub(d.last) < time.Minute {
			s.Totals.Active++
		}
		if d.online || (seen && now.Sub(d.last) < 5*time.Minute) {
			s.Totals.Online++
		}
		devs = append(devs, d)
	}

	if now.Sub(t.lastSweep) > time.Minute {
		t.lastSweep = now
		for ip, e := range t.names {
			if now.Sub(e.at) > nameMaxAge {
				delete(t.names, ip)
			}
		}
	}

	s.Totals.RxRate, s.Totals.TxRate = t.rxRate, t.txRate
	s.Totals.Rx, s.Totals.Tx = t.wanRx, t.wanTx
	s.Totals.PPS, s.Totals.Packets, s.Totals.Dropped = t.pps, t.packets, t.dropped

	sort.Slice(devs, func(i, j int) bool {
		a, b := devs[i], devs[j]
		ra, rb := a.rxRate+a.txRate, b.rxRate+b.txRate
		if ra != rb {
			return ra > rb
		}
		if a.rx+a.tx != b.rx+b.tx {
			return a.rx+a.tx > b.rx+b.tx
		}
		return a.key < b.key
	})
	s.Devices = make([]DeviceView, 0, len(devs))
	for _, d := range devs {
		s.Devices = append(s.Devices, t.deviceView(d, now))
	}

	sort.Slice(all, func(i, j int) bool {
		fa, fb := all[i].failure(now) != "", all[j].failure(now) != ""
		if fa != fb {
			return fa
		}
		ra, rb := all[i].rxRate+all[i].txRate, all[j].rxRate+all[j].txRate
		if ra != rb {
			return ra > rb
		}
		return all[i].last.After(all[j].last)
	})
	if topN > 0 && len(all) > topN {
		all = all[:topN]
	}
	for k, pd := range t.dnsPending {
		if now.Sub(pd.at) > dnsTimeout {
			delete(t.dnsPending, k)
			t.recordDNSFail(pd.name, pd.dev, "Keine Antwort vom DNS-Server", pd.at)
		}
	}
	for k, f := range t.dnsFails {
		if now.Sub(f.last) > dnsFailKeep {
			delete(t.dnsFails, k)
			continue
		}
		s.DNS = append(s.DNS, DNSFailView{Name: f.name, Dev: f.dev, Reason: f.reason, Count: f.count, Ago: now.Sub(f.last).Seconds()})
	}
	sort.Slice(s.DNS, func(i, j int) bool { return s.DNS[i].Ago < s.DNS[j].Ago })
	s.Totals.DNSFail = len(s.DNS)
	if len(s.DNS) > 100 {
		s.DNS = s.DNS[:100]
	}
	s.Conns = make([]ConnView, 0, len(all))
	for _, c := range all {
		s.Conns = append(s.Conns, ConnView{
			ID: c.id, Dev: c.dev.key, Proto: protoName(c.key.proto),
			LIP: c.key.lip.String(), LPort: c.key.lport,
			RIP: c.key.rip.String(), RPort: c.key.rport,
			Host: c.host, Svc: service(c.key.proto, c.key.lport, c.key.rport),
			Kind: kindNames[c.kind], Rx: c.rx, Tx: c.tx, RxRate: c.rxRate, TxRate: c.txRate,
			Age: now.Sub(c.first).Seconds(), Idle: now.Sub(c.last).Seconds(), Closed: c.closed(),
			Fail: c.failure(now),
		})
	}
	return s
}

func (t *Tracker) deviceView(d *device, now time.Time) DeviceView {
	v := DeviceView{
		ID: d.key, Name: d.name, MAC: d.mac, Iface: d.iface, Net: d.net,
		Rx: d.rx, Tx: d.tx, RxRate: d.rxRate, TxRate: d.txRate,
		Conns: d.conns, Online: d.online || (!d.last.IsZero() && now.Sub(d.last) < 5*time.Minute), Idle: -1,
	}
	if !d.last.IsZero() {
		v.Idle = now.Sub(d.last).Seconds()
	}
	var best4, best6 netip.Addr
	var t4, t6 time.Time
	for ip, at := range d.ips {
		v.IPs = append(v.IPs, ip.String())
		if ip.Is4() && (!best4.IsValid() || at.After(t4)) {
			best4, t4 = ip, at
		}
		if ip.Is6() && !ip.IsLinkLocalUnicast() && (!best6.IsValid() || at.After(t6)) {
			best6, t6 = ip, at
		}
	}
	sort.Strings(v.IPs)
	switch {
	case best4.IsValid():
		v.IP = best4.String()
	case best6.IsValid():
		v.IP = best6.String()
	case len(v.IPs) > 0:
		v.IP = v.IPs[0]
	}
	if v.Name == "" {
		for ip := range d.ips {
			if n := t.mdns[ip]; n != "" {
				v.Name = n
				break
			}
		}
	}
	return v
}
