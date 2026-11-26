package fritz

import (
	"net"
	"net/netip"
	"testing"
)

// Test vectors from the AVM Technical Note "Session ID".
func TestSolveChallengePBKDF2(t *testing.T) {
	got, err := SolveChallenge("2$10000$5A1711$2000$5A1722", "1example!")
	if err != nil {
		t.Fatal(err)
	}
	want := "5A1722$1798a1672bca7c6463d6b245f82b53703b0f50813401b03e4045a5861e689adb"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSolveChallengeMD5(t *testing.T) {
	got, err := SolveChallenge("1234567z", "äbc")
	if err != nil {
		t.Fatal(err)
	}
	if want := "1234567z-9e224a41eeefa284df7bb0f26c2913e2"; got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestSolveChallengeInvalid(t *testing.T) {
	for _, c := range []string{"2$", "2$x$aa$1$bb", "2$1$zz$1$bb", "2$99999999$aa$1$bb"} {
		if _, err := SolveChallenge(c, "pw"); err == nil {
			t.Errorf("%q: expected error", c)
		}
	}
}

func TestParseDigest(t *testing.T) {
	d, ok := parseDigest(`Digest realm="F!Box SOAP-Auth", nonce="ABC123", algorithm=MD5, qop="auth"`)
	if !ok || d.realm != "F!Box SOAP-Auth" || d.nonce != "ABC123" || d.qop != "auth" {
		t.Fatalf("bad parse: %+v %v", d, ok)
	}
	if _, ok := parseDigest(`Basic realm="x"`); ok {
		t.Fatal("basic must not parse")
	}
}

func TestFirmwareVersion(t *testing.T) {
	cases := map[[2]string]string{
		{"154.08.54-138651", "BETA"}: "8.54 (Beta)",
		{"154.07.57", ""}:            "7.57",
		{"113.06.83", "inhaus"}:      "6.83 (Labor)",
		{"", ""}:                     "",
	}
	for in, want := range cases {
		if got := firmwareVersion(in[0], in[1]); got != want {
			t.Errorf("%v: got %q want %q", in, got, want)
		}
	}
}

func TestNetLabel(t *testing.T) {
	cases := []struct{ typ, port, classes, props, ip, iface, want string }{
		{"wlan", "WLAN", "wlan", "2,4 GHz, 72 / 72 Mbit/s", "192.168.3.5", "802.11", "WLAN 2,4 GHz"},
		{"wlan", "WLAN", "wlan", "5 GHz, 520 / 585 Mbit/s", "192.168.3.6", "802.11", "WLAN 5 GHz"},
		{"ethernet", "LAN 1 mit 1 Gbit/s", "ethernet", "", "192.168.3.7", "Ethernet", "LAN"},
		{"wlan", "WLAN", "wlan guest", "", "192.168.3.8", "802.11", "Gast"},
		{"", "", "", "", "192.168.179.20", "802.11", "Gast"},
		{"", "", "", "", "192.168.3.9", "802.11", "WLAN"},
	}
	for _, c := range cases {
		if got := netLabel(c.typ, c.port, c.classes, c.props, c.ip, c.iface); got != c.want {
			t.Errorf("%+v: got %q", c, got)
		}
	}
}

func TestSeries(t *testing.T) {
	got := series("77,78,81,0,0")
	if len(got) != 3 || got[0] != 81 || got[2] != 77 {
		t.Fatalf("got %v", got)
	}
	if len(series("")) != 0 {
		t.Fatal("empty")
	}
}

func TestHasAddr(t *testing.T) {
	_, n4, _ := net.ParseCIDR("192.168.3.20/24")
	n4.IP = net.ParseIP("192.168.3.20")
	n6 := &net.IPNet{IP: net.ParseIP("fe80::1"), Mask: net.CIDRMask(64, 128)}
	addrs := []net.Addr{n4, n6}
	for _, tc := range []struct {
		a    string
		want bool
	}{{"192.168.3.20", true}, {"::ffff:192.168.3.20", true}, {"fe80::1%eth0", true}, {"192.168.3.21", false}} {
		if got := hasAddr(addrs, netip.MustParseAddr(tc.a).Unmap()); got != tc.want {
			t.Errorf("hasAddr(%s) = %v, want %v", tc.a, got, tc.want)
		}
	}
}
