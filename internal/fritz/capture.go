package fritz

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sabieler/fritzFlow/internal/pcap"
)

var errIdle = errors.New("keine Daten von der FRITZ!Box empfangen (Zeitüberschreitung)")

// ErrBusy means the box refused to start a capture (usually because another
// capture on the same interface is still running).
var ErrBusy = errors.New("FRITZ!Box startet keinen Mitschnitt")

// ErrStreamEnded means the box closed the capture stream.
var ErrStreamEnded = errors.New("Mitschnitt wurde von der FRITZ!Box beendet")

// ErrNetChanged means the local address the capture stream ran over vanished
// (network adapter switched off, LAN/WLAN change). The box usually keeps that
// capture running for a while, so it has to be stopped before restarting.
var ErrNetChanged = errors.New("Netzwerkverbindung dieses Rechners hat sich geändert – verbinde neu")

// netWatchInterval is how often Capture checks that its local address still exists.
var netWatchInterval = 2 * time.Second

// watchLocalAddr calls gone once addr is no longer assigned to any interface.
func watchLocalAddr(ctx context.Context, addr netip.Addr, gone func()) {
	t := time.NewTicker(netWatchInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		addrs, err := net.InterfaceAddrs()
		if err != nil {
			continue // can't tell: keep going, the idle timeout still applies
		}
		if !hasAddr(addrs, addr) {
			gone()
			return
		}
	}
}

// CaptureAvailable reports whether a capture on iface can be started right
// now. It starts one, checks that the box sends a capture header and stops
// it again right away.
func (c *Client) CaptureAvailable(ctx context.Context, sid, iface string) bool {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.captureURL(sid, iface, 68), nil)
	if err != nil {
		return false
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return false
	}
	head := make([]byte, 4)
	_, err = io.ReadFull(resp.Body, head)
	resp.Body.Close() // closes the connection: the box ends this capture
	ok := resp.StatusCode == http.StatusOK && err == nil && !isText(head)
	if ok {
		sctx, scancel := context.WithTimeout(context.Background(), 8*time.Second)
		c.StopCapture(sctx, sid, iface)
		scancel()
	}
	return ok
}

func isText(b []byte) bool {
	for _, c := range b {
		if c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func hasAddr(addrs []net.Addr, want netip.Addr) bool {
	want = want.WithZone("")
	for _, a := range addrs {
		var ip net.IP
		switch v := a.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}
		if got, ok := netip.AddrFromSlice(ip); ok && got.Unmap() == want {
			return true
		}
	}
	return false
}

type countingReader struct {
	r    io.Reader
	n    *atomic.Uint64
	wd   *time.Timer
	idle time.Duration
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.wd.Reset(c.idle)
		if c.n != nil {
			c.n.Add(uint64(n))
		}
	}
	return n, err
}

func (c *Client) captureURL(sid, iface string, snaplen int) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/cgi-bin/capture_notimeout"
	q := url.Values{}
	q.Set("ifaceorminor", iface)
	q.Set("snaplen", strconv.Itoa(snaplen))
	q.Set("capture", "Start")
	q.Set("sid", sid)
	u.RawQuery = q.Encode()
	return u.String()
}

var stopButtonRe = regexp.MustCompile(`id="uiStart_([^"]+)"\s+value="([^"]+)"[\s\S]{0,400}?id="uiStop_([^"]+)"\s+value="([^"]+)"`)

// stopParams maps a start id like "1-lan" to the stop triple (type, minor,
// iface) the box expects. It reads the capture page; "1-<name>" falls back
// to the well-known pattern "1;-1;<name>".
func (c *Client) stopParams(ctx context.Context, sid, iface string) (string, string, string, bool) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/data.lua"
	form := url.Values{"sid": {sid}, "page": {"cap"}, "xhr": {"1"}, "xhrId": {"all"}}
	if req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode())); err == nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if resp, err := c.short.Do(req); err == nil {
			page, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			for _, m := range stopButtonRe.FindAllStringSubmatch(string(page), -1) {
				if m[1] == m[3] && m[2] == iface {
					if p := strings.SplitN(m[4], ";", 3); len(p) == 3 {
						return p[0], p[1], p[2], true
					}
				}
			}
		}
	}
	if name, ok := strings.CutPrefix(iface, "1-"); ok && name != "" {
		return "1", "-1", name, true
	}
	return "", "", "", false
}

// StopCapture stops a running capture on iface (also one left over by a
// previous, crashed FritzFlow process).
func (c *Client) StopCapture(ctx context.Context, sid, iface string) {
	typ, minor, name, ok := c.stopParams(ctx, sid, iface)
	if !ok {
		return
	}
	c.stop(ctx, sid, typ, minor, name)
}

// StopAllCaptures presses "Alle stoppen" on the capture page. Needed when an
// orphaned capture (its client vanished, e.g. after a network switch) blocks
// an interface and does not react to the per-interface stop. It also ends
// captures started by others (FRITZ!Box UI, another FritzFlow).
//
// The box answers this request only once all captures have ended, which can
// take minutes for an orphaned one, so callers should use a short timeout:
// the stop is triggered as soon as the request arrives.
func (c *Client) StopAllCaptures(ctx context.Context, sid string) {
	c.get(ctx, url.Values{"iface": {"stopall"}, "capture": {"Stop"}, "sid": {sid}})
}

func (c *Client) stop(ctx context.Context, sid, typ, minor, name string) {
	c.get(ctx, url.Values{"iface": {name}, "minor": {minor}, "type": {typ}, "capture": {"Stop"}, "sid": {sid}})
}

func (c *Client) get(ctx context.Context, q url.Values) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/cgi-bin/capture_notimeout"
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return
	}
	if resp, err := c.short.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
	}
}

// Capture streams packets from the box until ctx is cancelled or the stream
// fails. fn is called synchronously for every packet. received counts the raw
// bytes the box sends (to show the capture overhead in the UI).
func (c *Client) Capture(ctx context.Context, sid, iface string, snaplen int, idle time.Duration,
	received *atomic.Uint64, fn func(pcap.Packet)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var local netip.Addr
	trace := &httptrace.ClientTrace{GotConn: func(i httptrace.GotConnInfo) {
		if a, ok := i.Conn.LocalAddr().(*net.TCPAddr); ok {
			local, _ = netip.AddrFromSlice(a.IP)
		}
	}}
	req, err := http.NewRequestWithContext(httptrace.WithClientTrace(ctx, trace), http.MethodGet, c.captureURL(sid, iface, snaplen), nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return c.netErr(err)
	}
	defer resp.Body.Close()
	if local.IsValid() && !local.IsLoopback() {
		go watchLocalAddr(ctx, local.Unmap(), func() {
			c.stream.CloseIdleConnections() // pooled connections use the vanished address, too
			cancel(ErrNetChanged)
		})
	}
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return ErrSession
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("Mitschnitt abgelehnt: HTTP %d", resp.StatusCode)
	}
	wd := time.AfterFunc(idle, func() { cancel(errIdle) })
	defer wd.Stop()
	body := bufio.NewReader(resp.Body)
	if head, _ := body.Peek(4); len(head) == 4 && isText(head) {
		// Not a pcap header but a message, e.g. while the interface is
		// still blocked by another capture.
		msg, _ := io.ReadAll(io.LimitReader(body, 300))
		return fmt.Errorf("%w: FRITZ!Box meldet %q", ErrBusy, strings.TrimSpace(string(msg)))
	}
	rd, err := pcap.NewReader(&countingReader{r: body, n: received, wd: wd, idle: idle})
	if err != nil {
		if cause := context.Cause(ctx); cause != nil {
			return cause
		}
		if errors.Is(err, pcap.ErrFormat) {
			return fmt.Errorf("%w: die FRITZ!Box liefert keinen Mitschnitt – hat der Benutzer das Recht \"FRITZ!Box Einstellungen\" und stimmt die Schnittstelle %q? (%v)", ErrSession, iface, err)
		}
		if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
			return fmt.Errorf("%w: leere Antwort – läuft auf %q schon ein anderer Mitschnitt?", ErrBusy, iface)
		}
		return c.netErr(err)
	}
	for {
		p, err := rd.Next()
		if err != nil {
			if cause := context.Cause(ctx); cause != nil {
				return cause
			}
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return ErrStreamEnded
			}
			return err
		}
		fn(p)
	}
}
