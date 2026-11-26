package fakebox

import (
	"context"
	"math"
	"math/rand"
	"net/netip"
	"time"

	"github.com/sabieler/fritzFlow/internal/pktgen"
)

// All public addresses come from documentation ranges (RFC 5737 / RFC 3849).

type simDev struct {
	name      string
	mac       pktgen.MAC
	ip4, ip6  netip.Addr
	iface     string
	weight    float64
	anonymous bool // not in the host list; announces itself via mDNS
	dsts      []string
}

type simDst struct {
	key      string
	qname    string
	cname    string
	ip4, ip6 netip.Addr
	port     uint16
	proto    uint8
	down, up float64 // bytes per second at full speed
	burst    bool
	sni      bool
	life     [2]float64 // seconds
}

type simulation struct {
	devs     []*simDev
	dsts     map[string]*simDst
	router   pktgen.MAC
	routerV4 netip.Addr
	routerV6 netip.Addr
	printer  *simDev
}

func mac(last byte) pktgen.MAC { return pktgen.MAC{0x02, 0x42, 0xac, 0x11, 0x00, last} }

func newSimulation() *simulation {
	a := netip.MustParseAddr
	s := &simulation{
		router:   pktgen.MAC{0x2c, 0x91, 0xab, 0x00, 0x00, 0x01},
		routerV4: a("192.168.178.1"),
		routerV6: a("fe80::2e91:abff:fe00:1"),
		dsts:     map[string]*simDst{},
	}
	dev := func(name string, last byte, iface string, w float64, dsts ...string) *simDev {
		d := &simDev{name: name, mac: mac(last), iface: iface, weight: w, dsts: dsts,
			ip4: netip.AddrFrom4([4]byte{192, 168, 178, 20 + last}),
			ip6: netip.AddrFrom16([16]byte{0x20, 0x01, 0x0d, 0xb8, 0xab, 0xcd, 0, 0, 0x1c, 0x2d, 0x3e, 0x4f, 0, 0, 0, last})}
		s.devs = append(s.devs, d)
		return d
	}
	dev("Samsung-TV", 1, "Ethernet", 1.2, "netflix", "youtube", "disney")
	dev("iPhone-15", 2, "802.11", 2.0, "whatsapp", "instagram", "applepush", "spotify", "icloud", "google")
	dev("MacBook-Pro", 3, "802.11", 2.0, "github", "anthropic", "google", "icloud", "call", "youtube", "ntp")
	dev("PlayStation-5", 4, "Ethernet", 0.8, "psn", "discord", "youtube")
	dev("Echo-Dot", 5, "802.11", 0.8, "alexa", "spotify")
	dev("Desktop-PC", 6, "Ethernet", 1.5, "steam", "msupdate", "discord", "google", "github")
	dev("iPad-Wohnzimmer", 7, "802.11", 1.0, "youtube-quic", "disney", "applepush")
	s.printer = dev("HP-LaserJet", 8, "Ethernet", 0, "")
	sh := dev("shellyplug-s-7a1f", 9, "802.11", 0.3, "shelly")
	sh.anonymous = true

	dst := func(key, qname, cname, ip4, ip6 string, port uint16, proto uint8, down, up float64, burst, sni bool, l0, l1 float64) {
		d := &simDst{key: key, qname: qname, cname: cname, ip4: a(ip4), port: port, proto: proto,
			down: down, up: up, burst: burst, sni: sni, life: [2]float64{l0, l1}}
		if ip6 != "" {
			d.ip6 = a(ip6)
		}
		s.dsts[key] = d
	}
	const tcp, udp = 6, 17
	dst("youtube", "rr4---sn-h0jeen7y.googlevideo.com", "", "203.0.113.10", "2001:db8:4860::10", 443, tcp, 2_600_000, 30_000, true, true, 20, 90)
	dst("youtube-quic", "rr2---sn-4g5e6nsz.googlevideo.com", "", "203.0.113.11", "", 443, udp, 2_200_000, 40_000, true, false, 20, 80)
	dst("netflix", "ipv4-c012-fra002-ix.1.oca.nflxvideo.net", "", "203.0.113.20", "", 443, tcp, 2_000_000, 20_000, true, true, 40, 120)
	dst("disney", "vod-akc-eu-west-1.media.dssott.com", "", "192.0.2.10", "", 443, tcp, 1_700_000, 15_000, true, false, 30, 90)
	dst("spotify", "audio-ak-spotify-com.akamaized.net", "a1713.dscr.akamai.net", "203.0.113.30", "", 443, tcp, 60_000, 4_000, true, false, 20, 60)
	dst("whatsapp", "g.whatsapp.net", "", "203.0.113.40", "2001:db8:a0b::40", 443, tcp, 6_000, 3_000, false, true, 10, 60)
	dst("instagram", "scontent-fra5-1.cdninstagram.com", "", "203.0.113.41", "2001:db8:a0b::41", 443, tcp, 350_000, 10_000, true, true, 5, 30)
	dst("applepush", "17-courier.push.apple.com", "", "203.0.113.50", "", 5223, tcp, 300, 200, false, true, 30, 120)
	dst("icloud", "p123-content.icloud.com", "", "203.0.113.51", "", 443, tcp, 40_000, 700_000, false, true, 15, 60)
	dst("github", "api.github.com", "", "198.51.100.10", "", 443, tcp, 40_000, 8_000, false, true, 3, 20)
	dst("google", "www.google.com", "", "198.51.100.20", "2001:db8:4860::20", 443, tcp, 60_000, 6_000, false, true, 3, 15)
	dst("anthropic", "api.anthropic.com", "", "198.51.100.30", "", 443, tcp, 25_000, 12_000, false, true, 5, 40)
	dst("steam", "cache8-fra1.steamcontent.com", "", "198.51.100.40", "", 443, tcp, 9_000_000, 90_000, false, false, 15, 45)
	dst("psn", "gs2.ww.prod.dl.playstation.net", "", "198.51.100.41", "", 443, tcp, 5_000_000, 50_000, false, true, 15, 50)
	dst("msupdate", "tlu.dl.delivery.mp.microsoft.com", "", "198.51.100.50", "", 80, tcp, 1_500_000, 15_000, false, false, 10, 40)
	dst("discord", "gateway.discord.gg", "", "198.51.100.60", "", 443, tcp, 4_000, 2_000, false, true, 20, 90)
	dst("alexa", "avs-alexa-14-eu.amazon.com", "", "198.51.100.70", "", 443, tcp, 15_000, 8_000, false, true, 20, 90)
	dst("call", "zoomfrn145mmr.fra.zoom.us", "", "198.51.100.80", "", 8801, udp, 280_000, 260_000, false, false, 30, 90)
	dst("ntp", "time.apple.com", "", "198.51.100.90", "", 123, udp, 90, 90, false, false, 1, 2)
	dst("shelly", "shelly-11-eu.shelly.cloud", "", "192.0.2.50", "", 6022, tcp, 300, 300, false, true, 30, 120)
	return s
}

type simFlow struct {
	dev     *simDev
	dst     *simDst
	sport   uint16
	v6      bool
	end     time.Time
	period  float64
	phase   float64
	accDown float64
	accUp   float64
}

type run struct {
	s     *simulation
	pw    *pktgen.Writer
	rnd   *rand.Rand
	now   time.Time
	flows []*simFlow
	err   error
}

func (r *run) emit(dst, src pktgen.MAC, sip, dip netip.Addr, proto uint8, l4 []byte, wireL4 int) {
	if r.err != nil {
		return
	}
	f := pktgen.Frame(dst, src, sip, dip, proto, l4, wireL4)
	orig := len(f) - len(l4) + max(wireL4, len(l4))
	r.err = r.pw.Write(r.now, f, orig)
}

// up: device -> internet; down: internet -> device.
func (r *run) up(f *simFlow, flags uint8, payload []byte, wire int) {
	sip, dip := f.dev.ip4, f.dst.ip4
	if f.v6 {
		sip, dip = f.dev.ip6, f.dst.ip6
	}
	var l4 []byte
	if f.dst.proto == 6 {
		l4 = pktgen.TCP(f.sport, f.dst.port, flags, payload)
	} else {
		l4 = pktgen.UDP(f.sport, f.dst.port, payload)
	}
	r.emit(r.s.router, f.dev.mac, sip, dip, f.dst.proto, l4, wire)
}

func (r *run) down(f *simFlow, flags uint8, wire int) {
	sip, dip := f.dst.ip4, f.dev.ip4
	if f.v6 {
		sip, dip = f.dst.ip6, f.dev.ip6
	}
	var l4 []byte
	if f.dst.proto == 6 {
		l4 = pktgen.TCP(f.dst.port, f.sport, flags, nil)
	} else {
		l4 = pktgen.UDP(f.dst.port, f.sport, nil)
	}
	r.emit(f.dev.mac, r.s.router, sip, dip, f.dst.proto, l4, wire)
}

func (r *run) dns(d *simDev, dst *simDst, v6 bool) {
	ip := dst.ip4
	if v6 {
		ip = dst.ip6
	}
	sport := uint16(30000 + r.rnd.Intn(30000))
	q := pktgen.UDP(sport, 53, make([]byte, 32))
	r.emit(r.s.router, d.mac, d.ip4, r.s.routerV4, 17, q, len(q))
	resp := pktgen.UDP(53, sport, pktgen.DNSResponse(uint16(r.rnd.Intn(65535)), dst.qname, dst.cname, ip))
	r.emit(d.mac, r.s.router, r.s.routerV4, d.ip4, 17, resp, len(resp))
}

func (r *run) spawn() {
	total := 0.0
	for _, d := range r.s.devs {
		total += d.weight
	}
	x := r.rnd.Float64() * total
	var dev *simDev
	for _, d := range r.s.devs {
		if x -= d.weight; x <= 0 && d.weight > 0 {
			dev = d
			break
		}
	}
	if dev == nil || len(dev.dsts) == 0 {
		return
	}
	dst := r.s.dsts[dev.dsts[r.rnd.Intn(len(dev.dsts))]]
	if dst == nil {
		return
	}
	same := 0
	for _, f := range r.flows {
		if f.dev == dev && f.dst == dst {
			same++
		}
	}
	if same >= 1 && (dst.down > 500_000 || same >= 3) {
		return
	}
	f := &simFlow{
		dev: dev, dst: dst, sport: uint16(49152 + r.rnd.Intn(16000)),
		v6:     dst.ip6.IsValid() && r.rnd.Intn(2) == 0,
		end:    r.now.Add(time.Duration((dst.life[0] + r.rnd.Float64()*(dst.life[1]-dst.life[0])) * float64(time.Second))),
		period: 3 + r.rnd.Float64()*5, phase: r.rnd.Float64(),
	}
	r.dns(dev, dst, f.v6)
	if dst.proto == 6 {
		r.up(f, pktgen.SYN, nil, 40)
		r.down(f, pktgen.SYN|pktgen.ACK, 40)
		r.up(f, pktgen.ACK, nil, 20)
		if dst.sni {
			hello := pktgen.ClientHello(dst.qname)
			r.up(f, pktgen.PSH|pktgen.ACK, hello, 20+len(hello)+200)
		} else if dst.port == 80 {
			req := []byte("GET /filestreamingservice/files/abc HTTP/1.1\r\nHost: " + dst.qname + "\r\nUser-Agent: Microsoft-Delivery-Optimization/10.0\r\n\r\n")
			r.up(f, pktgen.PSH|pktgen.ACK, req, 20+len(req))
		}
	}
	r.flows = append(r.flows, f)
}

func (r *run) step(dt float64) {
	t := float64(r.now.UnixNano()) / 1e9
	keep := r.flows[:0]
	for _, f := range r.flows {
		if r.now.After(f.end) {
			if f.dst.proto == 6 {
				r.up(f, pktgen.FIN|pktgen.ACK, nil, 20)
				r.down(f, pktgen.FIN|pktgen.ACK, 20)
			}
			continue
		}
		factor := 0.75 + 0.5*r.rnd.Float64()
		if f.dst.burst {
			if math.Mod(t/f.period+f.phase, 1) < 0.45 {
				factor = 1.7 + 0.3*r.rnd.Float64()
			} else {
				factor = 0.03
			}
		}
		const mss = 1448.0
		f.accDown += f.dst.down * factor * dt
		f.accUp += f.dst.up * factor * dt
		nd := int(f.accDown / mss)
		if nd == 0 && f.accDown > 120 && f.dst.down < 20_000 {
			nd = 1 // small flows: occasional small packets
		}
		frameD := mss
		if nd == 1 && f.accDown < mss {
			frameD = f.accDown
		}
		f.accDown -= float64(nd) * frameD
		if f.accDown < 0 {
			f.accDown = 0
		}
		nd = min(nd, 600)
		for i := 0; i < nd; i++ {
			r.down(f, pktgen.ACK|pktgen.PSH, 20+int(frameD))
		}
		if f.dst.proto == 6 { // ACKs back
			for i := 0; i < nd/2; i++ {
				r.up(f, pktgen.ACK, nil, 20)
			}
		}
		nu := int(f.accUp / mss)
		frameU := mss
		if nu == 0 && f.accUp > 120 {
			nu, frameU = 1, f.accUp
		}
		f.accUp -= float64(nu) * frameU
		if f.accUp < 0 {
			f.accUp = 0
		}
		nu = min(nu, 600)
		for i := 0; i < nu; i++ {
			r.up(f, pktgen.ACK|pktgen.PSH, nil, 20+int(frameU))
		}
		keep = append(keep, f)
	}
	r.flows = keep
}

func (r *run) extras(tick int) {
	if tick%200 == 1 { // mDNS announcement every ~5 s
		for _, d := range r.s.devs {
			if d.anonymous {
				l4 := pktgen.UDP(5353, 5353, pktgen.MDNSAnnounce(d.name, d.ip4))
				r.emit(pktgen.MAC{0x01, 0x00, 0x5e, 0x00, 0x00, 0xfb}, d.mac, d.ip4, netip.MustParseAddr("224.0.0.251"), 17, l4, len(l4))
			}
		}
	}
	// a print job now and then: LAN-internal traffic
	if tick%1200 > 1100 {
		pc := r.s.devs[5]
		l4 := pktgen.TCP(51515, 9100, pktgen.ACK|pktgen.PSH, nil)
		for i := 0; i < 12; i++ {
			r.emit(r.s.printer.mac, pc.mac, pc.ip4, r.s.printer.ip4, 6, l4, 1480)
		}
		back := pktgen.TCP(9100, 51515, pktgen.ACK, nil)
		r.emit(pc.mac, r.s.printer.mac, r.s.printer.ip4, pc.ip4, 6, back, 20)
	}
}

// run streams simulated traffic until ctx is done or writing fails.
func (s *simulation) run(ctx context.Context, pw *pktgen.Writer, flush func()) {
	r := &run{s: s, pw: pw, rnd: rand.New(rand.NewSource(time.Now().UnixNano())), now: time.Now()}
	const interval = 25 * time.Millisecond
	t := time.NewTicker(interval)
	defer t.Stop()
	for i := 0; i < 14; i++ {
		r.spawn()
	}
	last := time.Now()
	for tick := 0; ; tick++ {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			r.now = now
			dt := now.Sub(last).Seconds()
			last = now
			target := 22 + int(8*math.Sin(float64(now.Unix())/40))
			if len(r.flows) < target && r.rnd.Float64() < 0.25 {
				r.spawn()
			}
			r.step(dt)
			r.extras(tick)
			if r.err == nil {
				r.err = pw.Flush()
			}
			if r.err != nil {
				return
			}
			flush()
		}
	}
}
