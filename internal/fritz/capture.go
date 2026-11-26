package fritz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
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
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/cgi-bin/capture_notimeout"
	u.RawQuery = url.Values{"iface": {name}, "minor": {minor}, "type": {typ}, "capture": {"Stop"}, "sid": {sid}}.Encode()
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
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.captureURL(sid, iface, snaplen), nil)
	if err != nil {
		return err
	}
	resp, err := c.stream.Do(req)
	if err != nil {
		return c.netErr(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized:
		return ErrSession
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("Mitschnitt abgelehnt: HTTP %d", resp.StatusCode)
	}
	wd := time.AfterFunc(idle, func() { cancel(errIdle) })
	defer wd.Stop()
	rd, err := pcap.NewReader(&countingReader{r: resp.Body, n: received, wd: wd, idle: idle})
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
