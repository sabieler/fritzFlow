// Package pktgen builds synthetic network frames and pcap streams.
// It is used by the demo FRITZ!Box simulator and by tests.
package pktgen

import (
	"bufio"
	"encoding/binary"
	"io"
	"net/netip"
	"strings"
	"time"
)

// TCP flags.
const (
	FIN = 0x01
	SYN = 0x02
	RST = 0x04
	PSH = 0x08
	ACK = 0x10
)

// MAC is an Ethernet address.
type MAC [6]byte

// Writer writes a little-endian classic pcap stream (link type Ethernet).
type Writer struct {
	w    *bufio.Writer
	snap int
	hdr  [16]byte
}

// NewWriter writes the global header.
func NewWriter(w io.Writer, snaplen int) (*Writer, error) {
	bw := bufio.NewWriterSize(w, 64*1024)
	var h [24]byte
	binary.LittleEndian.PutUint32(h[0:], 0xa1b2c3d4)
	binary.LittleEndian.PutUint16(h[4:], 2)
	binary.LittleEndian.PutUint16(h[6:], 4)
	binary.LittleEndian.PutUint32(h[16:], uint32(snaplen))
	binary.LittleEndian.PutUint32(h[20:], 1)
	if _, err := bw.Write(h[:]); err != nil {
		return nil, err
	}
	return &Writer{w: bw, snap: snaplen}, nil
}

// Write emits one frame truncated to the snaplen; origLen is the claimed wire length.
func (pw *Writer) Write(ts time.Time, frame []byte, origLen int) error {
	incl := min(len(frame), pw.snap)
	origLen = max(origLen, len(frame))
	h := pw.hdr[:]
	binary.LittleEndian.PutUint32(h[0:], uint32(ts.Unix()))
	binary.LittleEndian.PutUint32(h[4:], uint32(ts.Nanosecond()/1000))
	binary.LittleEndian.PutUint32(h[8:], uint32(incl))
	binary.LittleEndian.PutUint32(h[12:], uint32(origLen))
	if _, err := pw.w.Write(h); err != nil {
		return err
	}
	_, err := pw.w.Write(frame[:incl])
	return err
}

// Flush flushes buffered data.
func (pw *Writer) Flush() error { return pw.w.Flush() }

// Frame builds an Ethernet+IP frame. l4 is the transport header (+payload);
// wireL4 is the claimed transport length (>= len(l4)) used for IP length fields.
func Frame(dst, src MAC, sip, dip netip.Addr, proto uint8, l4 []byte, wireL4 int) []byte {
	wireL4 = max(wireL4, len(l4))
	var b []byte
	if sip.Is4() {
		b = make([]byte, 14+20, 14+20+len(l4))
		copy(b[0:6], dst[:])
		copy(b[6:12], src[:])
		binary.BigEndian.PutUint16(b[12:], 0x0800)
		ip := b[14:]
		ip[0] = 0x45
		binary.BigEndian.PutUint16(ip[2:], uint16(min(20+wireL4, 65535)))
		ip[8] = 64
		ip[9] = proto
		s, d := sip.As4(), dip.As4()
		copy(ip[12:16], s[:])
		copy(ip[16:20], d[:])
	} else {
		b = make([]byte, 14+40, 14+40+len(l4))
		copy(b[0:6], dst[:])
		copy(b[6:12], src[:])
		binary.BigEndian.PutUint16(b[12:], 0x86DD)
		ip := b[14:]
		ip[0] = 0x60
		binary.BigEndian.PutUint16(ip[4:], uint16(min(wireL4, 65535)))
		ip[6] = proto
		ip[7] = 64
		s, d := sip.As16(), dip.As16()
		copy(ip[8:24], s[:])
		copy(ip[24:40], d[:])
	}
	return append(b, l4...)
}

// TCP builds a TCP header followed by payload.
func TCP(sport, dport uint16, flags uint8, payload []byte) []byte {
	b := make([]byte, 20, 20+len(payload))
	binary.BigEndian.PutUint16(b[0:], sport)
	binary.BigEndian.PutUint16(b[2:], dport)
	b[12] = 5 << 4
	b[13] = flags
	binary.BigEndian.PutUint16(b[14:], 65535)
	return append(b, payload...)
}

// UDP builds a UDP header followed by payload.
func UDP(sport, dport uint16, payload []byte) []byte {
	b := make([]byte, 8, 8+len(payload))
	binary.BigEndian.PutUint16(b[0:], sport)
	binary.BigEndian.PutUint16(b[2:], dport)
	binary.BigEndian.PutUint16(b[4:], uint16(8+len(payload)))
	return append(b, payload...)
}

func appendName(b []byte, name string) []byte {
	for _, l := range strings.Split(strings.TrimSuffix(name, "."), ".") {
		if l == "" {
			continue
		}
		b = append(b, byte(len(l)))
		b = append(b, l...)
	}
	return append(b, 0)
}

// DNSResponse builds a DNS answer for qname. If cname is set, the answer is
// a CNAME chain qname -> cname -> ip (like CDNs do).
func DNSResponse(id uint16, qname, cname string, ip netip.Addr) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[0:], id)
	binary.BigEndian.PutUint16(b[2:], 0x8180)
	binary.BigEndian.PutUint16(b[4:], 1)
	an := 1
	if cname != "" {
		an = 2
	}
	binary.BigEndian.PutUint16(b[6:], uint16(an))
	b = appendName(b, qname)
	qtype := uint16(1)
	if ip.Is6() {
		qtype = 28
	}
	b = binary.BigEndian.AppendUint16(b, qtype)
	b = binary.BigEndian.AppendUint16(b, 1)
	owner := []byte{0xC0, 12} // pointer to question name
	if cname != "" {
		b = append(b, owner...)
		b = binary.BigEndian.AppendUint16(b, 5)
		b = binary.BigEndian.AppendUint16(b, 1)
		b = binary.BigEndian.AppendUint32(b, 300)
		target := appendName(nil, cname)
		off := len(b) + 2
		b = binary.BigEndian.AppendUint16(b, uint16(len(target)))
		b = append(b, target...)
		owner = []byte{0xC0 | byte(off>>8), byte(off)}
	}
	b = append(b, owner...)
	b = binary.BigEndian.AppendUint16(b, qtype)
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint32(b, 300)
	if ip.Is4() {
		a := ip.As4()
		b = binary.BigEndian.AppendUint16(b, 4)
		b = append(b, a[:]...)
	} else {
		a := ip.As16()
		b = binary.BigEndian.AppendUint16(b, 16)
		b = append(b, a[:]...)
	}
	return b
}

// MDNSAnnounce builds an unsolicited mDNS response "name.local A ip".
func MDNSAnnounce(name string, ip netip.Addr) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint16(b[2:], 0x8400)
	binary.BigEndian.PutUint16(b[6:], 1)
	b = appendName(b, name+".local")
	b = binary.BigEndian.AppendUint16(b, 1)
	b = binary.BigEndian.AppendUint16(b, 0x8001)
	b = binary.BigEndian.AppendUint32(b, 120)
	a := ip.As4()
	b = binary.BigEndian.AppendUint16(b, 4)
	return append(b, a[:]...)
}

// ClientHello builds a minimal TLS ClientHello record carrying an SNI.
func ClientHello(sni string) []byte {
	ext := []byte{}
	name := []byte(sni)
	// server_name extension
	sn := binary.BigEndian.AppendUint16(nil, uint16(len(name)+3))
	sn = append(sn, 0)
	sn = binary.BigEndian.AppendUint16(sn, uint16(len(name)))
	sn = append(sn, name...)
	ext = binary.BigEndian.AppendUint16(ext, 0)
	ext = binary.BigEndian.AppendUint16(ext, uint16(len(sn)))
	ext = append(ext, sn...)

	hs := []byte{3, 3}
	hs = append(hs, make([]byte, 32)...) // random
	hs = append(hs, 0)                   // session id
	hs = append(hs, 0, 2, 0x13, 0x01)    // one cipher suite
	hs = append(hs, 1, 0)                // compression
	hs = binary.BigEndian.AppendUint16(hs, uint16(len(ext)))
	hs = append(hs, ext...)

	msg := []byte{1, byte(len(hs) >> 16), byte(len(hs) >> 8), byte(len(hs))}
	msg = append(msg, hs...)
	rec := []byte{0x16, 3, 1}
	rec = binary.BigEndian.AppendUint16(rec, uint16(len(msg)))
	return append(rec, msg...)
}
