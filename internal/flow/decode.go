package flow

import (
	"encoding/binary"
	"net/netip"

	"github.com/sabieler/fritzFlow/internal/pcap"
)

type mac [6]byte

func (m mac) String() string {
	const hexd = "0123456789abcdef"
	b := make([]byte, 0, 17)
	for i, v := range m {
		if i > 0 {
			b = append(b, ':')
		}
		b = append(b, hexd[v>>4], hexd[v&0xf])
	}
	return string(b)
}

// packet holds the decoded headers of one frame. payload aliases the frame.
type packet struct {
	srcMAC, dstMAC mac
	hasMAC         bool
	src, dst       netip.Addr
	proto          uint8
	sport, dport   uint16
	tcpFlags       uint8
	hasPorts       bool
	payload        []byte
}

const (
	protoICMP   = 1
	protoTCP    = 6
	protoUDP    = 17
	protoICMPv6 = 58

	tcpFIN = 0x01
	tcpSYN = 0x02
	tcpRST = 0x04
	tcpACK = 0x10
)

func be16(b []byte) uint16 { return binary.BigEndian.Uint16(b) }

// decodePacket parses link, network and transport headers.
// It only returns true when at least source and destination IP are known.
// All accesses are bounds checked; truncated frames are fine.
func decodePacket(link uint32, b []byte, p *packet) bool {
	*p = packet{}
	var etype uint16
	switch link {
	case pcap.LinkEthernet:
		if len(b) < 14 {
			return false
		}
		copy(p.dstMAC[:], b[0:6])
		copy(p.srcMAC[:], b[6:12])
		p.hasMAC = true
		etype = be16(b[12:])
		b = b[14:]
		for i := 0; i < 2 && (etype == 0x8100 || etype == 0x88a8); i++ {
			if len(b) < 4 {
				return false
			}
			etype = be16(b[2:])
			b = b[4:]
		}
	case pcap.LinkLinuxSLL:
		if len(b) < 16 {
			return false
		}
		etype = be16(b[14:])
		b = b[16:]
	case pcap.LinkLinuxSLL2:
		if len(b) < 20 {
			return false
		}
		etype = be16(b[0:])
		b = b[20:]
	case pcap.LinkNull:
		if len(b) < 5 {
			return false
		}
		b = b[4:]
		etype = ipVersionType(b)
	case pcap.LinkRaw, pcap.LinkRawDLT12, pcap.LinkRawDLT14, pcap.LinkIPv4, pcap.LinkIPv6:
		etype = ipVersionType(b)
	default:
		return false
	}
	switch etype {
	case 0x0800:
		return decodeIPv4(b, p)
	case 0x86DD:
		return decodeIPv6(b, p)
	}
	return false
}

func ipVersionType(b []byte) uint16 {
	if len(b) == 0 {
		return 0
	}
	switch b[0] >> 4 {
	case 4:
		return 0x0800
	case 6:
		return 0x86DD
	}
	return 0
}

func decodeIPv4(b []byte, p *packet) bool {
	if len(b) < 20 || b[0]>>4 != 4 {
		return false
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || len(b) < ihl {
		return false
	}
	if total := int(be16(b[2:])); total >= ihl && total < len(b) {
		b = b[:total] // strip Ethernet padding
	}
	p.proto = b[9]
	p.src = netip.AddrFrom4([4]byte(b[12:16]))
	p.dst = netip.AddrFrom4([4]byte(b[16:20]))
	if be16(b[6:])&0x1fff != 0 { // non-first fragment: no transport header
		return true
	}
	decodeL4(b[ihl:], p)
	return true
}

func decodeIPv6(b []byte, p *packet) bool {
	if len(b) < 40 || b[0]>>4 != 6 {
		return false
	}
	plen := int(be16(b[4:]))
	nh := b[6]
	p.src = netip.AddrFrom16([16]byte(b[8:24]))
	p.dst = netip.AddrFrom16([16]byte(b[24:40]))
	b = b[40:]
	if plen < len(b) {
		b = b[:plen]
	}
	for i := 0; i < 8; i++ {
		switch nh {
		case 0, 43, 60: // hop-by-hop, routing, destination options
			if len(b) < 8 {
				p.proto = nh
				return true
			}
			l := (int(b[1]) + 1) * 8
			nh = b[0]
			if len(b) < l {
				p.proto = nh
				return true
			}
			b = b[l:]
		case 44: // fragment
			if len(b) < 8 {
				p.proto = nh
				return true
			}
			off := be16(b[2:]) & 0xfff8
			nh = b[0]
			b = b[8:]
			if off != 0 {
				p.proto = nh
				return true
			}
		default:
			p.proto = nh
			decodeL4(b, p)
			return true
		}
	}
	p.proto = nh
	return true
}

func decodeL4(b []byte, p *packet) {
	switch p.proto {
	case protoTCP:
		if len(b) < 14 {
			return
		}
		p.sport, p.dport = be16(b[0:]), be16(b[2:])
		p.hasPorts = true
		p.tcpFlags = b[13]
		if off := int(b[12]>>4) * 4; off >= 20 && off <= len(b) {
			p.payload = b[off:]
		}
	case protoUDP:
		if len(b) < 8 {
			return
		}
		p.sport, p.dport = be16(b[0:]), be16(b[2:])
		p.hasPorts = true
		p.payload = b[8:]
	case protoICMP, protoICMPv6:
		p.payload = b // type, code, … (+ embedded original packet for errors)
	}
}

// icmpUnreachable parses an ICMP "destination unreachable" message and
// returns the 5-tuple of the original packet that could not be delivered.
func icmpUnreachable(proto uint8, b []byte) (k connKey, code uint8, ok bool) {
	if len(b) < 8 {
		return k, 0, false
	}
	typ, code := b[0], b[1]
	inner := b[8:]
	switch {
	case proto == protoICMP && typ == 3:
		if len(inner) < 20 || inner[0]>>4 != 4 {
			return k, 0, false
		}
		ihl := int(inner[0]&0x0f) * 4
		if ihl < 20 || len(inner) < ihl+4 {
			return k, 0, false
		}
		k.proto = inner[9]
		k.lip = netip.AddrFrom4([4]byte(inner[12:16]))
		k.rip = netip.AddrFrom4([4]byte(inner[16:20]))
		k.lport, k.rport = be16(inner[ihl:]), be16(inner[ihl+2:])
	case proto == protoICMPv6 && typ == 1:
		if len(inner) < 44 || inner[0]>>4 != 6 {
			return k, 0, false
		}
		k.proto = inner[6]
		k.lip = netip.AddrFrom16([16]byte(inner[8:24]))
		k.rip = netip.AddrFrom16([16]byte(inner[24:40]))
		k.lport, k.rport = be16(inner[40:]), be16(inner[42:])
	default:
		return k, 0, false
	}
	if k.proto != protoTCP && k.proto != protoUDP {
		return k, 0, false
	}
	k.lip, k.rip = k.lip.Unmap(), k.rip.Unmap()
	return k, code, true
}
