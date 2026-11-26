package fritz

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/sabieler/fritzFlow/internal/pcap"
)

var errIdle = errors.New("keine Daten von der FRITZ!Box empfangen (Zeitüberschreitung)")

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

func (c *Client) captureURL(sid, iface string, snaplen int, start bool) string {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/cgi-bin/capture_notimeout"
	q := url.Values{}
	q.Set("ifaceorminor", iface)
	if start {
		q.Set("snaplen", strconv.Itoa(snaplen))
		q.Set("capture", "Start")
	} else {
		q.Set("capture", "Stop")
	}
	q.Set("sid", sid)
	u.RawQuery = q.Encode()
	return u.String()
}

// Capture streams packets from the box until ctx is cancelled or the stream
// fails. fn is called synchronously for every packet. received counts the raw
// bytes the box sends (to show the capture overhead in the UI).
func (c *Client) Capture(ctx context.Context, sid, iface string, snaplen int, idle time.Duration,
	received *atomic.Uint64, fn func(pcap.Packet)) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.captureURL(sid, iface, snaplen, true), nil)
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
			return fmt.Errorf("%w: leere Antwort – Schnittstelle %q existiert evtl. nicht", ErrSession, iface)
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

// StopCapture asks the box to stop a running capture (best effort; closing
// the stream already ends it).
func (c *Client) StopCapture(ctx context.Context, sid, iface string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.captureURL(sid, iface, 0, false), nil)
	if err != nil {
		return
	}
	if resp, err := c.short.Do(req); err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
		resp.Body.Close()
	}
}
