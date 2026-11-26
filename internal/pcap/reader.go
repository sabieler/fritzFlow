// Package pcap reads classic pcap and pcapng streams incrementally.
// It never trusts length fields: every size is bounded, so a corrupt or
// hostile stream yields an error instead of a crash or huge allocation.
package pcap

import (
	"bufio"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

// Link types we understand downstream.
const (
	LinkNull      = 0
	LinkEthernet  = 1
	LinkRaw       = 101
	LinkRawDLT12  = 12 // DLT_RAW on most platforms
	LinkRawDLT14  = 14 // DLT_RAW as used by FRITZ!OS for the routing interface (3-0)
	LinkLinuxSLL  = 113
	LinkIPv4      = 228
	LinkIPv6      = 229
	LinkLinuxSLL2 = 276
)

const (
	maxPacket = 256 * 1024
	maxBlock  = 4 * 1024 * 1024
	ngMagic   = 0x0A0D0D0A
)

var (
	// ErrFormat means the stream does not start like a pcap/pcapng file.
	ErrFormat = errors.New("pcap: unbekanntes Datenformat")
	// ErrCorrupt means a length field is out of range.
	ErrCorrupt = errors.New("pcap: beschädigter Datenstrom")
)

// Packet is one captured frame. Data is only valid until the next call to Next.
type Packet struct {
	TS      time.Time
	Link    uint32
	Data    []byte
	OrigLen int
}

type ngIface struct {
	link  uint32
	units uint64 // timestamp units per second
}

// Reader decodes packets from a stream.
type Reader struct {
	r      *bufio.Reader
	ng     bool
	bo     binary.ByteOrder
	nano   bool
	extra  int // extra per-record header bytes (modified pcap: 8)
	link   uint32
	ifaces []ngIface
	buf    []byte
	hdr    [24]byte
}

// NewReader detects the format and reads the file header.
func NewReader(r io.Reader) (*Reader, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	magic, err := br.Peek(4)
	if err != nil {
		return nil, err
	}
	rd := &Reader{r: br}
	switch m := binary.LittleEndian.Uint32(magic); m {
	case ngMagic:
		rd.ng = true
		return rd, nil
	case 0xa1b2c3d4:
		rd.bo = binary.LittleEndian
	case 0xa1b23c4d:
		rd.bo, rd.nano = binary.LittleEndian, true
	case 0xd4c3b2a1:
		rd.bo = binary.BigEndian
	case 0x4d3cb2a1:
		rd.bo, rd.nano = binary.BigEndian, true
	case 0xa1b2cd34: // "modified pcap" (Kuznetsov patch) – used by FRITZ!OS
		rd.bo, rd.extra = binary.LittleEndian, 8
	case 0x34cdb2a1:
		rd.bo, rd.extra = binary.BigEndian, 8
	default:
		return nil, fmt.Errorf("%w (magic %08x)", ErrFormat, m)
	}
	var h [24]byte
	if _, err := io.ReadFull(br, h[:]); err != nil {
		return nil, err
	}
	rd.link = rd.bo.Uint32(h[20:24]) & 0x0FFFFFFF
	return rd, nil
}

// Next returns the next packet.
func (rd *Reader) Next() (Packet, error) {
	if rd.ng {
		return rd.nextNG()
	}
	h := rd.hdr[:16+rd.extra]
	if _, err := io.ReadFull(rd.r, h); err != nil {
		return Packet{}, err
	}
	sec := rd.bo.Uint32(h[0:4])
	frac := rd.bo.Uint32(h[4:8])
	incl := rd.bo.Uint32(h[8:12])
	orig := rd.bo.Uint32(h[12:16])
	if incl > maxPacket {
		return Packet{}, fmt.Errorf("%w: Paketlänge %d", ErrCorrupt, incl)
	}
	data := rd.grow(int(incl))
	if _, err := io.ReadFull(rd.r, data); err != nil {
		return Packet{}, err
	}
	ns := int64(frac)
	if !rd.nano {
		ns *= 1000
	}
	if orig < incl || orig > maxPacket {
		orig = incl
	}
	return Packet{TS: time.Unix(int64(sec), ns), Link: rd.link, Data: data, OrigLen: int(orig)}, nil
}

func (rd *Reader) grow(n int) []byte {
	if cap(rd.buf) < n {
		rd.buf = make([]byte, n, max(n, 64*1024))
	}
	return rd.buf[:n]
}

func (rd *Reader) nextNG() (Packet, error) {
	for {
		typ, body, err := rd.readBlock()
		if err != nil {
			return Packet{}, err
		}
		switch typ {
		case ngMagic: // section header: interfaces reset
			rd.ifaces = rd.ifaces[:0]
		case 1: // interface description
			if len(body) < 8 {
				return Packet{}, ErrCorrupt
			}
			ifc := ngIface{link: uint32(rd.bo.Uint16(body[0:2])), units: 1_000_000}
			rd.parseIDBOptions(body[8:], &ifc)
			if len(rd.ifaces) < 256 {
				rd.ifaces = append(rd.ifaces, ifc)
			}
		case 6, 2: // enhanced packet / obsolete packet block
			if len(body) < 20 {
				return Packet{}, ErrCorrupt
			}
			var id uint32
			if typ == 6 {
				id = rd.bo.Uint32(body[0:4])
			} else {
				id = uint32(rd.bo.Uint16(body[0:2]))
			}
			ts := uint64(rd.bo.Uint32(body[4:8]))<<32 | uint64(rd.bo.Uint32(body[8:12]))
			capLen := rd.bo.Uint32(body[12:16])
			orig := rd.bo.Uint32(body[16:20])
			if uint64(capLen) > uint64(len(body)-20) {
				return Packet{}, fmt.Errorf("%w: EPB-Länge", ErrCorrupt)
			}
			ifc := rd.iface(id)
			if orig < capLen || orig > maxPacket {
				orig = capLen
			}
			return Packet{TS: tsFromUnits(ts, ifc.units), Link: ifc.link, Data: body[20 : 20+capLen], OrigLen: int(orig)}, nil
		case 3: // simple packet block
			if len(body) < 4 {
				return Packet{}, ErrCorrupt
			}
			orig := rd.bo.Uint32(body[0:4])
			data := body[4:]
			if uint64(orig) < uint64(len(data)) {
				data = data[:orig]
			}
			if orig > maxPacket {
				orig = uint32(len(data))
			}
			return Packet{TS: time.Now(), Link: rd.iface(0).link, Data: data, OrigLen: int(orig)}, nil
		}
	}
}

func (rd *Reader) iface(id uint32) ngIface {
	if int(id) < len(rd.ifaces) {
		return rd.ifaces[id]
	}
	return ngIface{link: LinkEthernet, units: 1_000_000}
}

func (rd *Reader) parseIDBOptions(opts []byte, ifc *ngIface) {
	for len(opts) >= 4 {
		code := rd.bo.Uint16(opts[0:2])
		l := int(rd.bo.Uint16(opts[2:4]))
		opts = opts[4:]
		if code == 0 || l > len(opts) {
			return
		}
		if code == 9 && l >= 1 { // if_tsresol
			v := opts[0]
			exp := uint(v & 0x7f)
			if v&0x80 != 0 {
				if exp <= 30 {
					ifc.units = 1 << exp
				}
			} else if exp <= 9 {
				u := uint64(1)
				for i := uint(0); i < exp; i++ {
					u *= 10
				}
				ifc.units = u
			}
		}
		pad := (l + 3) &^ 3
		if pad > len(opts) {
			return
		}
		opts = opts[pad:]
	}
}

func tsFromUnits(v, units uint64) time.Time {
	if units == 0 {
		units = 1_000_000
	}
	sec := v / units
	frac := v % units
	return time.Unix(int64(sec), int64(frac*1_000_000_000/units))
}

// readBlock returns the block type and its body (without the trailing length).
func (rd *Reader) readBlock() (uint32, []byte, error) {
	h := rd.hdr[:8]
	if _, err := io.ReadFull(rd.r, h); err != nil {
		return 0, nil, err
	}
	if binary.LittleEndian.Uint32(h[0:4]) == ngMagic {
		var bom [4]byte
		if _, err := io.ReadFull(rd.r, bom[:]); err != nil {
			return 0, nil, err
		}
		switch binary.LittleEndian.Uint32(bom[:]) {
		case 0x1A2B3C4D:
			rd.bo = binary.LittleEndian
		case 0x4D3C2B1A:
			rd.bo = binary.BigEndian
		default:
			return 0, nil, fmt.Errorf("%w: Byte-Order", ErrCorrupt)
		}
		l := rd.bo.Uint32(h[4:8])
		if l < 28 || l%4 != 0 || l > maxBlock {
			return 0, nil, fmt.Errorf("%w: SHB-Länge %d", ErrCorrupt, l)
		}
		b := rd.grow(int(l) - 12)
		if _, err := io.ReadFull(rd.r, b); err != nil {
			return 0, nil, err
		}
		return ngMagic, b[:len(b)-4], nil
	}
	if rd.bo == nil {
		return 0, nil, ErrFormat
	}
	typ := rd.bo.Uint32(h[0:4])
	l := rd.bo.Uint32(h[4:8])
	if l < 12 || l%4 != 0 || l > maxBlock {
		return 0, nil, fmt.Errorf("%w: Blocklänge %d", ErrCorrupt, l)
	}
	b := rd.grow(int(l) - 8)
	if _, err := io.ReadFull(rd.r, b); err != nil {
		return 0, nil, err
	}
	return typ, b[:len(b)-4], nil
}
