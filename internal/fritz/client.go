// Package fritz talks to a FRITZ!Box: session login, packet capture stream
// and the TR-064 host list.
package fritz

import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors with user-facing German messages.
var (
	ErrAuth    = errors.New("Anmeldung fehlgeschlagen: Benutzername oder Passwort falsch")
	ErrSession = errors.New("Sitzung ungültig oder fehlende Berechtigung")
)

// BlockedError means the box refuses logins for a while (brute-force guard).
type BlockedError struct{ Wait time.Duration }

func (e *BlockedError) Error() string {
	return fmt.Sprintf("FRITZ!Box sperrt Anmeldungen noch %d s (zu viele Fehlversuche)", int(e.Wait.Seconds()))
}

const emptySID = "0000000000000000"

// Client is a FRITZ!Box API client. It is safe for concurrent use.
type Client struct {
	base     *url.URL
	tr064    *url.URL
	user     string
	password string
	short    *http.Client // regular requests with timeout
	stream   *http.Client // capture stream without overall timeout

	mu       sync.Mutex
	lastUser string
	dg       *digestState
}

// New creates a client. tr064Override may be empty (default: host:49000).
func New(base, tr064Override, user, password string, insecureTLS bool) (*Client, error) {
	u, err := url.Parse(strings.TrimRight(base, "/"))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("ungültige FRITZ!Box-Adresse %q", base)
	}
	var t64 *url.URL
	if tr064Override != "" {
		if t64, err = url.Parse(strings.TrimRight(tr064Override, "/")); err != nil {
			return nil, err
		}
	} else {
		t64 = &url.URL{Scheme: "http", Host: net.JoinHostPort(u.Hostname(), "49000")}
	}
	tr := &http.Transport{
		Proxy: nil, // the box is always local: never route through a proxy
		DialContext: (&net.Dialer{
			Timeout:   8 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: insecureTLS}, //nolint:gosec // self-signed box cert
		TLSHandshakeTimeout:   8 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
	}
	return &Client{
		base:     u,
		tr064:    t64,
		user:     user,
		password: password,
		short:    &http.Client{Transport: tr, Timeout: 20 * time.Second},
		stream:   &http.Client{Transport: tr},
	}, nil
}

// Base returns the box address.
func (c *Client) Base() string { return c.base.String() }

type sessionInfo struct {
	SID       string `xml:"SID"`
	Challenge string `xml:"Challenge"`
	BlockTime int    `xml:"BlockTime"`
	Users     []struct {
		Name string `xml:",chardata"`
		Last string `xml:"last,attr"`
	} `xml:"Users>User"`
}

func (s *sessionInfo) defaultUser() string {
	for _, u := range s.Users {
		if u.Last == "1" {
			return strings.TrimSpace(u.Name)
		}
	}
	if len(s.Users) > 0 {
		return strings.TrimSpace(s.Users[0].Name)
	}
	return ""
}

func (c *Client) netErr(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("FRITZ!Box unter %s nicht erreichbar: %w", c.base.Host, err)
}

func (c *Client) sessionInfo(ctx context.Context, q url.Values, form url.Values) (*sessionInfo, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/login_sid.lua"
	if q == nil {
		q = url.Values{}
	}
	q.Set("version", "2")
	u.RawQuery = q.Encode()
	var req *http.Request
	var err error
	if form == nil {
		req, err = http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	} else {
		req, err = http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
		if req != nil {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	if err != nil {
		return nil, err
	}
	resp, err := c.short.Do(req)
	if err != nil {
		return nil, c.netErr(err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, c.netErr(err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("FRITZ!Box antwortet mit HTTP %d auf login_sid.lua", resp.StatusCode)
	}
	var si sessionInfo
	if err := xml.Unmarshal(body, &si); err != nil || (si.SID == "" && si.Challenge == "") {
		return nil, fmt.Errorf("%s antwortet nicht wie eine FRITZ!Box (login_sid.lua)", c.base.Host)
	}
	return &si, nil
}

// Login performs the challenge-response login and returns a session ID.
func (c *Client) Login(ctx context.Context) (string, error) {
	si, err := c.sessionInfo(ctx, nil, nil)
	if err != nil {
		return "", err
	}
	if si.BlockTime > 0 {
		return "", &BlockedError{Wait: time.Duration(si.BlockTime) * time.Second}
	}
	user := c.user
	if user == "" {
		user = si.defaultUser()
	}
	c.mu.Lock()
	c.lastUser = user
	c.mu.Unlock()
	resp, err := SolveChallenge(si.Challenge, c.password)
	if err != nil {
		return "", err
	}
	si2, err := c.sessionInfo(ctx, nil, url.Values{"username": {user}, "response": {resp}})
	if err != nil {
		return "", err
	}
	if si2.SID != "" && si2.SID != emptySID {
		return si2.SID, nil
	}
	if si2.BlockTime > 0 {
		return "", fmt.Errorf("%w (nächster Versuch in %d s möglich)", ErrAuth, si2.BlockTime)
	}
	return "", ErrAuth
}

// Logout ends a session (best effort).
func (c *Client) Logout(ctx context.Context, sid string) {
	if sid == "" || sid == emptySID {
		return
	}
	_, _ = c.sessionInfo(ctx, url.Values{"logout": {"1"}, "sid": {sid}}, nil)
}

// SolveChallenge computes the login response for PBKDF2 ("2$...") or MD5 challenges.
func SolveChallenge(challenge, password string) (string, error) {
	if strings.HasPrefix(challenge, "2$") {
		parts := strings.Split(challenge, "$")
		if len(parts) != 5 {
			return "", errors.New("unbekanntes Challenge-Format der FRITZ!Box")
		}
		it1, err1 := strconv.Atoi(parts[1])
		salt1, err2 := hex.DecodeString(parts[2])
		it2, err3 := strconv.Atoi(parts[3])
		salt2, err4 := hex.DecodeString(parts[4])
		if err := errors.Join(err1, err2, err3, err4); err != nil || it1 < 1 || it2 < 1 || it1 > 10_000_000 || it2 > 10_000_000 {
			return "", errors.New("ungültige PBKDF2-Challenge der FRITZ!Box")
		}
		h1 := pbkdf2SHA256([]byte(password), salt1, it1)
		h2 := pbkdf2SHA256(h1, salt2, it2)
		return parts[4] + "$" + hex.EncodeToString(h2), nil
	}
	// Legacy MD5 over UTF-16LE; code points > 255 become '.'
	s := challenge + "-" + password
	buf := make([]byte, 0, len(s)*2)
	for _, r := range s {
		if r > 255 {
			r = '.'
		}
		buf = append(buf, byte(r), 0)
	}
	sum := md5.Sum(buf)
	return challenge + "-" + hex.EncodeToString(sum[:]), nil
}

// pbkdf2SHA256 derives a 32-byte key (one block) with HMAC-SHA256.
func pbkdf2SHA256(password, salt []byte, iter int) []byte {
	prf := hmac.New(sha256.New, password)
	prf.Write(salt)
	prf.Write([]byte{0, 0, 0, 1})
	u := prf.Sum(nil)
	t := append([]byte(nil), u...)
	for i := 1; i < iter; i++ {
		prf.Reset()
		prf.Write(u)
		u = prf.Sum(u[:0])
		for j := range t {
			t[j] ^= u[j]
		}
	}
	return t
}

// BoxInfo describes the FRITZ!Box hardware and firmware.
type BoxInfo struct {
	Model    string // e.g. "FRITZ!Box 7590"
	Firmware string // e.g. "8.54 (Beta)"
}

// Info reads jason_boxinfo.xml (no login needed). The serial number in that
// file is deliberately ignored.
func (c *Client) Info(ctx context.Context) (BoxInfo, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/jason_boxinfo.xml"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return BoxInfo{}, err
	}
	resp, err := c.short.Do(req)
	if err != nil {
		return BoxInfo{}, c.netErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return BoxInfo{}, fmt.Errorf("jason_boxinfo.xml: HTTP %d", resp.StatusCode)
	}
	var x struct {
		Name    string `xml:"Name"`
		Version string `xml:"Version"`
		Lab     string `xml:"Lab"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 64*1024)).Decode(&x); err != nil {
		return BoxInfo{}, err
	}
	return BoxInfo{Model: strings.TrimSpace(x.Name), Firmware: firmwareVersion(x.Version, x.Lab)}, nil
}

// firmwareVersion turns "154.08.54-138651" into "8.54" (+ lab marker).
func firmwareVersion(v, lab string) string {
	v = strings.TrimSpace(v)
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v = v[:i]
	}
	if p := strings.Split(v, "."); len(p) == 3 {
		major := strings.TrimLeft(p[1], "0")
		if major == "" {
			major = "0"
		}
		v = major + "." + p[2]
	}
	switch lab = strings.TrimSpace(lab); {
	case v == "":
	case strings.EqualFold(lab, "beta"):
		v += " (Beta)"
	case lab != "":
		v += " (Labor)"
	}
	return v
}
