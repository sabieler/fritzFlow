package flow

import (
	"math/rand"
	"net/netip"
	"testing"
	"time"

	"github.com/sabieler/fritzFlow/internal/pcap"
	"github.com/sabieler/fritzFlow/internal/pktgen"
)

var (
	router = pktgen.MAC{0x2c, 0x91, 0xab, 0, 0, 1}
	phone  = pktgen.MAC{0x02, 0, 0, 0, 0, 2}
	gw     = netip.MustParseAddr("192.168.178.1")
	ip     = netip.MustParseAddr("192.168.178.22")
	remote = netip.MustParseAddr("203.0.113.10")
)

func pkt(frame []byte, orig int) pcap.Packet {
	return pcap.Packet{TS: time.Now(), Link: pcap.LinkEthernet, Data: frame, OrigLen: orig}
}

func TestParseDNSWithCNAME(t *testing.T) {
	msg := pktgen.DNSResponse(7, "audio.example.com", "edge.cdn.net", remote)
	var got []string
	parseDNS(msg, func(q, owner string, a netip.Addr, _ uint32) { got = append(got, q+"|"+owner+"|"+a.String()) })
	if len(got) != 1 || got[0] != "audio.example.com|edge.cdn.net|203.0.113.10" {
		t.Fatalf("got %v", got)
	}
	// truncated message: no panic, no result
	for i := 0; i < len(msg); i++ {
		parseDNS(msg[:i], func(string, string, netip.Addr, uint32) {})
	}
}

func TestParseSNIAndHTTP(t *testing.T) {
	if h := parseSNI(pktgen.ClientHello("WWW.Example.COM")); h != "www.example.com" {
		t.Fatalf("sni %q", h)
	}
	hello := pktgen.ClientHello("a.example.com")
	for i := 0; i < len(hello); i++ {
		_ = parseSNI(hello[:i])
	}
	if h := parseHTTPHost([]byte("GET / HTTP/1.1\r\nUser-Agent: x\r\nhost: dl.example.org:8080\r\n\r\n")); h != "dl.example.org" {
		t.Fatalf("http host %q", h)
	}
	if h := parseHTTPHost([]byte("GET / HTTP/1.1\r\nHost: evil<script>\r\n\r\n")); h != "" {
		t.Fatalf("invalid host accepted: %q", h)
	}
}

func TestTrackerEndToEnd(t *testing.T) {
	tr := New(Options{})
	// DNS answer from the box teaches the name of the remote IP.
	dns := pktgen.UDP(53, 40000, pktgen.DNSResponse(1, "video.example.com", "", remote))
	tr.Handle(pkt(pktgen.Frame(phone, router, gw, ip, 17, dns, len(dns)), 0))
	// TCP handshake + data
	tr.Handle(pkt(pktgen.Frame(router, phone, ip, remote, 6, pktgen.TCP(50000, 443, pktgen.SYN, nil), 20), 0))
	for i := 0; i < 10; i++ {
		tr.Handle(pkt(pktgen.Frame(phone, router, remote, ip, 6, pktgen.TCP(443, 50000, pktgen.ACK, nil), 1480), 1514))
	}
	tr.SetHosts([]Host{{MAC: "02:00:00:00:00:02", IP: "192.168.178.22", Name: "iPhone", Iface: "802.11", Active: true}})
	s := tr.Snapshot(time.Now().Add(time.Second), 100)

	var wan *ConnView
	for i := range s.Conns {
		if s.Conns[i].Kind == "wan" {
			wan = &s.Conns[i]
		}
	}
	if wan == nil {
		t.Fatalf("no wan conn: %+v", s.Conns)
	}
	if wan.Host != "video.example.com" || wan.Svc != "HTTPS" || wan.RPort != 443 || wan.LIP != ip.String() {
		t.Fatalf("bad conn %+v", *wan)
	}
	if wan.Rx != 15140 {
		t.Fatalf("rx bytes = %d, want 15140 (orig len)", wan.Rx)
	}
	if len(s.Devices) != 1 || s.Devices[0].Name != "iPhone" || s.Devices[0].ID != "02:00:00:00:00:02" {
		t.Fatalf("devices %+v", s.Devices)
	}
	if s.Totals.Rx != 15140 || s.Totals.ConnsWAN != 1 {
		t.Fatalf("totals %+v", s.Totals)
	}
	// FIN in both directions closes the connection; it expires afterwards.
	tr.Handle(pkt(pktgen.Frame(router, phone, ip, remote, 6, pktgen.TCP(50000, 443, pktgen.FIN|pktgen.ACK, nil), 20), 0))
	tr.Handle(pkt(pktgen.Frame(phone, router, remote, ip, 6, pktgen.TCP(443, 50000, pktgen.FIN|pktgen.ACK, nil), 20), 0))
	s = tr.Snapshot(time.Now().Add(2*time.Second), 100)
	for _, c := range s.Conns {
		if c.Kind == "wan" && !c.Closed {
			t.Fatal("connection should be closed")
		}
	}
	s = tr.Snapshot(time.Now().Add(30*time.Second), 100)
	for _, c := range s.Conns {
		if c.Kind == "wan" {
			t.Fatal("closed connection should have expired")
		}
	}
}

func TestTrackerSurvivesGarbage(t *testing.T) {
	tr := New(Options{MaxConns: 1000})
	rnd := rand.New(rand.NewSource(42))
	base := pktgen.Frame(router, phone, ip, remote, 6, pktgen.TCP(1, 443, pktgen.SYN, pktgen.ClientHello("x.example")), 0)
	for i := 0; i < 50_000; i++ {
		b := append([]byte(nil), base...)
		for j := 0; j < 1+rnd.Intn(8); j++ {
			b[rnd.Intn(len(b))] = byte(rnd.Intn(256))
		}
		b = b[:rnd.Intn(len(b)+1)]
		link := []uint32{1, 101, 113, 276, 0, 228, 229}[rnd.Intn(7)]
		tr.Handle(pcap.Packet{Link: link, Data: b, OrigLen: len(b)})
	}
	_ = tr.Snapshot(time.Now(), 100)
	if tr.Panics() != 0 {
		t.Fatalf("%d panics recovered – decoder has a bounds bug", tr.Panics())
	}
	if len(tr.conns) > 1000 {
		t.Fatalf("conn table exceeded limit: %d", len(tr.conns))
	}
}

func FuzzDecode(f *testing.F) {
	f.Add(pktgen.Frame(router, phone, ip, remote, 6, pktgen.TCP(1, 443, 0, pktgen.ClientHello("a.b")), 0))
	f.Add(pktgen.Frame(phone, router, gw, ip, 17, pktgen.UDP(53, 1, pktgen.DNSResponse(1, "a.b", "c.d", remote)), 0))
	f.Fuzz(func(t *testing.T, b []byte) {
		var p packet
		if decodePacket(pcap.LinkEthernet, b, &p) && p.proto == 17 {
			parseDNS(p.payload, func(string, string, netip.Addr, uint32) {})
		}
		_ = parseSNI(b)
		_ = parseHTTPHost(b)
	})
}
