package pcap

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"math/rand"
	"testing"
)

func classic(bo binary.ByteOrder, magic uint32, pkts ...[]byte) []byte {
	var b bytes.Buffer
	h := make([]byte, 24)
	bo.PutUint32(h[0:], magic)
	bo.PutUint32(h[16:], 65535)
	bo.PutUint32(h[20:], 1)
	b.Write(h)
	for i, p := range pkts {
		r := make([]byte, 16)
		bo.PutUint32(r[0:], uint32(1000+i))
		bo.PutUint32(r[4:], 500)
		bo.PutUint32(r[8:], uint32(len(p)))
		bo.PutUint32(r[12:], uint32(len(p)+100))
		b.Write(r)
		b.Write(p)
	}
	return b.Bytes()
}

func TestClassicBothEndians(t *testing.T) {
	for _, bo := range []binary.ByteOrder{binary.LittleEndian, binary.BigEndian} {
		data := classic(bo, 0xa1b2c3d4, []byte{1, 2, 3}, []byte{4, 5})
		rd, err := NewReader(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		p, err := rd.Next()
		if err != nil || !bytes.Equal(p.Data, []byte{1, 2, 3}) || p.OrigLen != 103 || p.Link != LinkEthernet || p.TS.Unix() != 1000 {
			t.Fatalf("%v: bad packet %+v %v", bo, p, err)
		}
		p, _ = rd.Next()
		if !bytes.Equal(p.Data, []byte{4, 5}) {
			t.Fatalf("second packet %v", p.Data)
		}
		if _, err := rd.Next(); !errors.Is(err, io.EOF) {
			t.Fatalf("want EOF, got %v", err)
		}
	}
}

func ngBlock(typ uint32, body []byte) []byte {
	for len(body)%4 != 0 {
		body = append(body, 0)
	}
	l := uint32(12 + len(body))
	b := binary.LittleEndian.AppendUint32(nil, typ)
	b = binary.LittleEndian.AppendUint32(b, l)
	b = append(b, body...)
	return binary.LittleEndian.AppendUint32(b, l)
}

func TestPcapNG(t *testing.T) {
	shb := binary.LittleEndian.AppendUint32(nil, 0x1A2B3C4D)
	shb = append(shb, 1, 0, 0, 0)
	shb = append(shb, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	idb := []byte{1, 0, 0, 0, 0, 0, 1, 0}
	idb = append(idb, 9, 0, 1, 0, 9, 0, 0, 0) // if_tsresol = 9 (ns)
	idb = append(idb, 0, 0, 0, 0)
	epb := make([]byte, 20)
	binary.LittleEndian.PutUint32(epb[4:], 0)
	binary.LittleEndian.PutUint32(epb[8:], 2_500_000_000) // 2.5 s in ns
	binary.LittleEndian.PutUint32(epb[12:], 3)
	binary.LittleEndian.PutUint32(epb[16:], 60)
	epb = append(epb, 7, 8, 9)
	var b []byte
	b = append(b, ngBlock(0x0A0D0D0A, shb)...)
	b = append(b, ngBlock(1, idb)...)
	b = append(b, ngBlock(6, epb)...)
	rd, err := NewReader(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	p, err := rd.Next()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(p.Data, []byte{7, 8, 9}) || p.OrigLen != 60 || p.TS.UnixMilli() != 2500 {
		t.Fatalf("bad packet %+v", p)
	}
}

func TestRejectsGarbage(t *testing.T) {
	if _, err := NewReader(bytes.NewReader([]byte("<html><body>login</body></html>"))); !errors.Is(err, ErrFormat) {
		t.Fatalf("want ErrFormat, got %v", err)
	}
	// absurd packet length must not allocate or panic
	data := classic(binary.LittleEndian, 0xa1b2c3d4)
	rec := make([]byte, 16)
	binary.LittleEndian.PutUint32(rec[8:], 0xFFFFFFF0)
	data = append(data, rec...)
	rd, _ := NewReader(bytes.NewReader(data))
	if _, err := rd.Next(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("want ErrCorrupt, got %v", err)
	}
}

func TestRandomInputNeverPanics(t *testing.T) {
	rnd := rand.New(rand.NewSource(1))
	heads := [][]byte{{0xd4, 0xc3, 0xb2, 0xa1}, {0x0a, 0x0d, 0x0d, 0x0a}}
	for i := 0; i < 3000; i++ {
		b := append([]byte(nil), heads[i%2]...)
		junk := make([]byte, rnd.Intn(400))
		rnd.Read(junk)
		b = append(b, junk...)
		rd, err := NewReader(bytes.NewReader(b))
		if err != nil {
			continue
		}
		for j := 0; j < 50; j++ {
			if _, err := rd.Next(); err != nil {
				break
			}
		}
	}
}

// FRITZ!OS streams "modified pcap" (magic a1b2cd34, big-endian, 24-byte record headers).
func TestModifiedPcapFromFritzBox(t *testing.T) {
	hdr := []byte{0xa1, 0xb2, 0xcd, 0x34, 0, 2, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 8, 0, 0, 0, 0, 1}
	rec := []byte{0x6b, 0x08, 0x1f, 0x11, 0, 0, 0xf0, 0x6e, 0, 0, 0, 4, 0, 0, 0, 90, 0, 0, 0, 12, 0x86, 0xdd, 4, 0}
	data := append(append(hdr, rec...), 0xaa, 0xbb, 0xcc, 0xdd)
	data = append(data, rec...)
	data = append(data, 1, 2, 3, 4)
	rd, err := NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range [][]byte{{0xaa, 0xbb, 0xcc, 0xdd}, {1, 2, 3, 4}} {
		p, err := rd.Next()
		if err != nil || !bytes.Equal(p.Data, want) || p.OrigLen != 90 || p.Link != LinkEthernet {
			t.Fatalf("bad packet %+v %v", p, err)
		}
	}
}
