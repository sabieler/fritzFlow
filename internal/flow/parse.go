package flow

import (
	"bytes"
	"net/netip"
	"strings"
)

// parseDNS walks a DNS response and calls fn for every A/AAAA record with the
// question name (what the client asked for) and the record's owner name.
// Truncated messages are handled gracefully: whatever was parsed is reported.
func parseDNS(msg []byte, fn func(qname, owner string, ip netip.Addr, ttl uint32)) {
	if len(msg) < 12 || msg[2]&0x80 == 0 { // QR bit: responses only
		return
	}
	qd := int(be16(msg[4:]))
	an := int(be16(msg[6:]))
	off := 12
	qname := ""
	for i := 0; i < qd && i < 4; i++ {
		name, n, ok := readName(msg, off)
		if !ok || n+4 > len(msg) {
			return
		}
		if i == 0 {
			qname = name
		}
		off = n + 4
	}
	for i := 0; i < an && i < 64; i++ {
		owner, n, ok := readName(msg, off)
		if !ok || n+10 > len(msg) {
			return
		}
		typ := be16(msg[n:])
		ttl := uint32(msg[n+4])<<24 | uint32(msg[n+5])<<16 | uint32(msg[n+6])<<8 | uint32(msg[n+7])
		rdl := int(be16(msg[n+8:]))
		rd := n + 10
		if rd+rdl > len(msg) {
			return
		}
		switch {
		case typ == 1 && rdl == 4:
			fn(qname, owner, netip.AddrFrom4([4]byte(msg[rd:rd+4])), ttl)
		case typ == 28 && rdl == 16:
			fn(qname, owner, netip.AddrFrom16([16]byte(msg[rd:rd+16])), ttl)
		}
		off = rd + rdl
	}
}

// readName decodes a (possibly compressed) domain name starting at off and
// returns the name and the offset after it in the original position.
func readName(msg []byte, off int) (string, int, bool) {
	var sb strings.Builder
	end := -1
	for hops := 0; hops < 128; hops++ {
		if off >= len(msg) {
			return "", 0, false
		}
		l := int(msg[off])
		switch {
		case l == 0:
			if end < 0 {
				end = off + 1
			}
			return sb.String(), end, true
		case l&0xC0 == 0xC0:
			if off+1 >= len(msg) {
				return "", 0, false
			}
			if end < 0 {
				end = off + 2
			}
			ptr := (l&0x3f)<<8 | int(msg[off+1])
			if ptr >= len(msg) {
				return "", 0, false
			}
			off = ptr
		case l&0xC0 != 0:
			return "", 0, false
		default:
			if off+1+l > len(msg) || sb.Len()+l > 253 {
				return "", 0, false
			}
			if sb.Len() > 0 {
				sb.WriteByte('.')
			}
			for _, c := range msg[off+1 : off+1+l] {
				if c >= 'A' && c <= 'Z' {
					c += 'a' - 'A'
				}
				sb.WriteByte(c)
			}
			off += 1 + l
		}
	}
	return "", 0, false
}

// parseSNI extracts the server name from a TLS ClientHello.
func parseSNI(b []byte) string {
	if len(b) < 44 || b[0] != 0x16 || b[1] != 3 || b[5] != 1 {
		return ""
	}
	p := 5 + 4 + 2 + 32
	p += 1 + int(b[p]) // session id
	if p+2 > len(b) {
		return ""
	}
	p += 2 + int(be16(b[p:])) // cipher suites
	if p+1 > len(b) {
		return ""
	}
	p += 1 + int(b[p]) // compression methods
	if p+2 > len(b) {
		return ""
	}
	end := min(p+2+int(be16(b[p:])), len(b))
	p += 2
	for p+4 <= end {
		typ := be16(b[p:])
		l := int(be16(b[p+2:]))
		p += 4
		if typ == 0 {
			if p+5 > len(b) || b[p+2] != 0 {
				return ""
			}
			n := int(be16(b[p+3:]))
			s := p + 5
			if n == 0 || s+n > len(b) {
				return ""
			}
			return cleanHost(b[s : s+n])
		}
		p += l
	}
	return ""
}

var httpMethods = [][]byte{[]byte("GET "), []byte("POST "), []byte("HEAD "), []byte("PUT "), []byte("OPTIONS "), []byte("DELETE "), []byte("PATCH ")}

// parseHTTPHost extracts the Host header of a plain HTTP request.
func parseHTTPHost(b []byte) string {
	ok := false
	for _, m := range httpMethods {
		if bytes.HasPrefix(b, m) {
			ok = true
			break
		}
	}
	if !ok {
		return ""
	}
	for {
		i := bytes.Index(b, []byte("\r\n"))
		if i < 0 {
			return ""
		}
		b = b[i+2:]
		if len(b) > 5 && (b[0]|0x20) == 'h' && (b[1]|0x20) == 'o' && (b[2]|0x20) == 's' && (b[3]|0x20) == 't' && b[4] == ':' {
			v := b[5:]
			j := bytes.IndexAny(v, "\r\n")
			if j < 0 {
				return "" // truncated: header value may be incomplete
			}
			v = bytes.TrimSpace(v[:j])
			if j := bytes.LastIndexByte(v, ':'); j > 0 && bytes.IndexByte(v, ']') < j {
				v = v[:j] // strip port
			}
			return cleanHost(v)
		}
	}
}

// cleanHost lowercases and validates a hostname; invalid names yield "".
func cleanHost(b []byte) string {
	if len(b) == 0 || len(b) > 253 {
		return ""
	}
	out := make([]byte, len(b))
	for i, c := range b {
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '.', c == '-', c == '_':
		case c >= 'A' && c <= 'Z':
			c += 'a' - 'A'
		default:
			return ""
		}
		out[i] = c
	}
	return strings.TrimSuffix(string(out), ".")
}

// service names a connection by its well-known port.
func service(proto uint8, lport, rport uint16) string {
	switch proto {
	case protoTCP:
		if s, ok := tcpServices[rport]; ok {
			return s
		}
		if s, ok := tcpServices[lport]; ok {
			return s
		}
		return "TCP"
	case protoUDP:
		if s, ok := udpServices[rport]; ok {
			return s
		}
		if s, ok := udpServices[lport]; ok {
			return s
		}
		return "UDP"
	case protoICMP, protoICMPv6:
		return "ICMP"
	case 50:
		return "IPsec"
	case 47:
		return "GRE"
	}
	return "IP"
}

func protoName(p uint8) string {
	switch p {
	case protoTCP:
		return "TCP"
	case protoUDP:
		return "UDP"
	case protoICMP:
		return "ICMP"
	case protoICMPv6:
		return "ICMPv6"
	}
	return "IP"
}

var tcpServices = map[uint16]string{
	20: "FTP", 21: "FTP", 22: "SSH", 23: "Telnet", 25: "SMTP", 53: "DNS", 80: "HTTP",
	110: "POP3", 143: "IMAP", 443: "HTTPS", 445: "SMB", 465: "SMTPS", 548: "AFP",
	554: "RTSP", 587: "SMTP", 631: "IPP", 853: "DNS-over-TLS", 993: "IMAPS", 995: "POP3S",
	1883: "MQTT", 1935: "RTMP", 3389: "RDP", 3478: "STUN", 5060: "SIP", 5061: "SIP-TLS",
	5222: "XMPP", 5223: "Apple Push", 5228: "Google Push", 6881: "BitTorrent",
	7000: "AirPlay", 8008: "Chromecast", 8009: "Chromecast", 8080: "HTTP", 8443: "HTTPS",
	8883: "MQTT-TLS", 9100: "Drucken", 27015: "Steam", 32400: "Plex",
}

var udpServices = map[uint16]string{
	53: "DNS", 67: "DHCP", 68: "DHCP", 123: "NTP", 137: "NetBIOS", 138: "NetBIOS",
	443: "QUIC", 500: "IPsec", 1194: "OpenVPN", 1900: "SSDP", 3074: "Xbox Live",
	3478: "STUN", 3479: "STUN", 4500: "IPsec", 5060: "SIP", 5353: "mDNS",
	19302: "STUN", 51820: "WireGuard",
}
