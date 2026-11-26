package fritz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// BoxLoad is the current utilisation of the FRITZ!Box (FRITZ!OS 8 REST API).
type BoxLoad struct {
	CPU       float64   `json:"cpu"`      // percent
	Temp      float64   `json:"temp"`     // °C
	RAMUsed   float64   `json:"ramUsed"`  // percent strictly used
	RAMCache  float64   `json:"ramCache"` // percent cache
	RAMFree   float64   `json:"ramFree"`  // percent free
	Power     float64   `json:"power"`    // percent of the maximum consumption
	PowerSys  float64   `json:"powerSys"` // main processor
	PowerWLAN float64   `json:"powerWlan"`
	PowerDSL  float64   `json:"powerDsl"`
	PowerUSB  float64   `json:"powerUsb"`
	CPUHist   []float64 `json:"cpuHist"`  // oldest → newest, 6-minute steps (max 24 h)
	TempHist  []float64 `json:"tempHist"` // oldest → newest
}

// ErrNoLoadAPI means the box has no FRITZ!OS 8 REST API (older firmware).
var ErrNoLoadAPI = errors.New("Auslastung erst ab FRITZ!OS 8 verfügbar")

func (c *Client) restGet(ctx context.Context, sid, path string, v any) error {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "AVM-SID "+sid)
	resp, err := c.short.Do(req)
	if err != nil {
		return c.netErr(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return ErrNoLoadAPI
	case resp.StatusCode != http.StatusOK:
		return fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(v)
}

// series parses "77,78,81,…" (newest first) and returns it oldest→newest
// without the zero padding the box uses for time before the last reboot.
func series(s string) []float64 {
	parts := strings.Split(s, ",")
	out := make([]float64, 0, len(parts))
	for _, p := range parts {
		v, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil {
			break
		}
		out = append(out, v)
	}
	// trailing zeros (oldest end) = no data yet
	end := len(out)
	for end > 0 && out[end-1] == 0 {
		end--
	}
	out = out[:end]
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func last(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	return v[len(v)-1]
}

func num(s string) float64 {
	v, _ := strconv.ParseFloat(strings.TrimSpace(s), 64)
	return v
}

// Load reads CPU, temperature, RAM and energy figures.
func (c *Client) Load(ctx context.Context, sid string) (*BoxLoad, error) {
	var cpu map[string]string
	if err := c.restGet(ctx, sid, "/api/v0/generic/cpu", &cpu); err != nil {
		return nil, err
	}
	l := &BoxLoad{
		CPUHist:  series(cpu["StatCPU"]),
		TempHist: series(cpu["StatTemperature"]),
	}
	l.CPU, l.Temp = last(l.CPUHist), last(l.TempHist)
	l.RAMUsed = last(series(cpu["StatRAMStrictlyUsed"]))
	l.RAMCache = last(series(cpu["StatRAMCacheUsed"]))
	l.RAMFree = last(series(cpu["StatRAMPhysFree"]))
	var pw map[string]string
	if err := c.restGet(ctx, sid, "/api/v0/generic/power", &pw); err == nil {
		l.Power, l.PowerSys = num(pw["rate_sumact"]), num(pw["rate_systemact"])
		l.PowerWLAN, l.PowerDSL, l.PowerUSB = num(pw["rate_wlanact"]), num(pw["rate_dspact"]), num(pw["rate_usbhostact"])
	}
	return l, nil
}

// WANMonitor is the real throughput at the internet connection (online
// monitor) plus line data. Account data such as the PPPoE user name or the
// public IP address is deliberately not read.
type WANMonitor struct {
	Down      float64   `json:"down"` // bytes/s, latest 5 s sample
	Up        float64   `json:"up"`
	DownHist  []float64 `json:"downHist"` // oldest → newest, 5 s steps, 10 min
	UpHist    []float64 `json:"upHist"`
	LineDown  float64   `json:"lineDown"` // kbit/s
	LineUp    float64   `json:"lineUp"`
	Media     string    `json:"media"`
	Connected bool      `json:"connected"`
	Uptime    float64   `json:"uptime"` // seconds
	Sample    int64     `json:"sample"` // unix time of the last sample
}

// Online reads the online monitor and the active internet connection.
func (c *Client) Online(ctx context.Context, sid string) (*WANMonitor, error) {
	var mon struct {
		LastSampleTime int64 `json:"lastSampleTime"`
		Categories     []struct {
			Up   []float64 `json:"upstream"`
			Down []float64 `json:"downstream"`
		} `json:"categories"`
	}
	if err := c.restGet(ctx, sid, "/api/v0/monitor/online/0", &mon); err != nil {
		return nil, err
	}
	w := &WANMonitor{Sample: mon.LastSampleTime}
	add := func(dst *[]float64, src []float64) {
		if len(*dst) < len(src) {
			*dst = append(*dst, make([]float64, len(src)-len(*dst))...)
		}
		off := len(*dst) - len(src) // align newest samples
		for i, v := range src {
			(*dst)[off+i] += v
		}
	}
	for _, cat := range mon.Categories {
		add(&w.DownHist, cat.Down)
		add(&w.UpHist, cat.Up)
	}
	w.Down, w.Up = last(w.DownHist), last(w.UpHist)

	var conns struct {
		Connection []map[string]string `json:"connection"`
	}
	if err := c.restGet(ctx, sid, "/api/v0/generic/connections", &conns); err == nil {
		for _, cn := range conns.Connection {
			if cn["is_active_internet_connection"] != "1" {
				continue
			}
			w.LineDown, w.LineUp = num(cn["max_speed_down"]), num(cn["max_speed_up"])
			w.Media = cn["media_type"]
			w.Connected = cn["ip4_connstatus"] == "connected" || cn["ip6_connstatus"] == "connected"
			w.Uptime = max(num(cn["ip4_uptime"]), num(cn["ip6_uptime"]))
			break
		}
	}
	return w, nil
}
