package fritz

import "testing"

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
