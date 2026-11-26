package fritz

import (
	"bytes"
	"context"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

// Host is one entry of the box's device list.
type Host struct {
	IP     string
	MAC    string
	Name   string
	Iface  string
	Active bool
	Router bool   // the FRITZ!Box itself
	Net    string // "LAN", "WLAN 2,4 GHz", "WLAN 5 GHz", "WLAN", "Gast" or ""
}

var bandRe = regexp.MustCompile(`(2,4|2\.4|5|6) GHz`)

// netLabel derives the network segment of a device.
func netLabel(typ, port, classes, props, ip, iface string) string {
	all := strings.ToLower(typ + " " + port + " " + classes + " " + props)
	switch {
	case strings.Contains(all, "guest") || strings.Contains(all, "gast") || strings.HasPrefix(ip, "192.168.179."):
		return "Gast"
	case strings.Contains(all, "wlan") || iface == "802.11":
		if m := bandRe.FindStringSubmatch(props); m != nil {
			return "WLAN " + strings.Replace(m[1], ".", ",", 1) + " GHz"
		}
		return "WLAN"
	case strings.Contains(all, "ethernet") || strings.Contains(all, "lan") || strings.EqualFold(iface, "Ethernet"):
		return "LAN"
	}
	return ""
}

const hostsService = "urn:dslforum-org:service:Hosts:1"

type digestState struct {
	realm, nonce, opaque, qop string
	nc                        int
}

func md5hex(s string) string {
	h := md5.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

// parseDigest parses a WWW-Authenticate Digest challenge.
func parseDigest(h string) (*digestState, bool) {
	h = strings.TrimSpace(h)
	if len(h) < 7 || !strings.EqualFold(h[:7], "digest ") {
		return nil, false
	}
	d := &digestState{}
	rest := h[7:]
	for rest != "" {
		rest = strings.TrimLeft(rest, " ,")
		eq := strings.IndexByte(rest, '=')
		if eq < 0 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(rest[:eq]))
		rest = rest[eq+1:]
		var val string
		if strings.HasPrefix(rest, `"`) {
			end := strings.IndexByte(rest[1:], '"')
			if end < 0 {
				return nil, false
			}
			val, rest = rest[1:1+end], rest[2+end:]
		} else {
			end := strings.IndexByte(rest, ',')
			if end < 0 {
				end = len(rest)
			}
			val, rest = strings.TrimSpace(rest[:end]), rest[end:]
		}
		switch key {
		case "realm":
			d.realm = val
		case "nonce":
			d.nonce = val
		case "opaque":
			d.opaque = val
		case "qop":
			d.qop = val
		}
	}
	return d, d.nonce != ""
}

func (c *Client) authorization(method, uri string) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	d := c.dg
	if d == nil {
		return ""
	}
	d.nc++
	user := c.user
	if user == "" {
		user = c.lastUser
	}
	ha1 := md5hex(user + ":" + d.realm + ":" + c.password)
	ha2 := md5hex(method + ":" + uri)
	var b strings.Builder
	fmt.Fprintf(&b, `Digest username="%s", realm="%s", nonce="%s", uri="%s", algorithm=MD5`, user, d.realm, d.nonce, uri)
	if strings.Contains(d.qop, "auth") {
		var cn [8]byte
		_, _ = rand.Read(cn[:])
		cnonce := hex.EncodeToString(cn[:])
		nc := fmt.Sprintf("%08x", d.nc)
		resp := md5hex(ha1 + ":" + d.nonce + ":" + nc + ":" + cnonce + ":auth:" + ha2)
		fmt.Fprintf(&b, `, response="%s", qop=auth, nc=%s, cnonce="%s"`, resp, nc, cnonce)
	} else {
		fmt.Fprintf(&b, `, response="%s"`, md5hex(ha1+":"+d.nonce+":"+ha2))
	}
	if d.opaque != "" {
		fmt.Fprintf(&b, `, opaque="%s"`, d.opaque)
	}
	return b.String()
}

// doDigest performs a request with HTTP digest auth, retrying once on 401.
func (c *Client) doDigest(ctx context.Context, method, rawURL string, body []byte, hdr map[string]string) ([]byte, int, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, 0, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
		if err != nil {
			return nil, 0, err
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		if a := c.authorization(method, u.RequestURI()); a != "" {
			req.Header.Set("Authorization", a)
		}
		resp, err := c.short.Do(req)
		if err != nil {
			return nil, 0, fmt.Errorf("TR-064 nicht erreichbar: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		resp.Body.Close()
		if err != nil {
			return nil, 0, err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			var ok bool
			for _, h := range resp.Header.Values("WWW-Authenticate") {
				var d *digestState
				if d, ok = parseDigest(h); ok {
					c.mu.Lock()
					c.dg = d
					c.mu.Unlock()
					break
				}
			}
			if ok {
				continue
			}
		}
		return data, resp.StatusCode, nil
	}
	return nil, http.StatusUnauthorized, nil
}

// soap calls a TR-064 action and returns all leaf values of the response.
func (c *Client) soap(ctx context.Context, control, service, action string, args [][2]string) (map[string]string, error) {
	var a strings.Builder
	for _, kv := range args {
		a.WriteString("<" + kv[0] + ">")
		_ = xml.EscapeText(&a, []byte(kv[1]))
		a.WriteString("</" + kv[0] + ">")
	}
	env := fmt.Sprintf(`<?xml version="1.0" encoding="utf-8"?>`+
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">`+
		`<s:Body><u:%s xmlns:u="%s">%s</u:%s></s:Body></s:Envelope>`, action, service, a.String(), action)
	u := *c.tr064
	u.Path = strings.TrimRight(u.Path, "/") + "/upnp/control/" + control
	data, status, err := c.doDigest(ctx, http.MethodPost, u.String(), []byte(env), map[string]string{
		"Content-Type": `text/xml; charset="utf-8"`,
		"SOAPAction":   service + "#" + action,
	})
	if err != nil {
		return nil, err
	}
	vals := leafValues(data)
	switch {
	case status == http.StatusUnauthorized:
		return nil, errors.New("TR-064: Zugriff verweigert (Benutzer/Passwort?)")
	case status != http.StatusOK:
		if d := vals["errorDescription"]; d != "" {
			return nil, fmt.Errorf("TR-064 %s: %s", action, d)
		}
		return nil, fmt.Errorf("TR-064 %s: HTTP %d – ist \"Zugriff für Anwendungen zulassen\" aktiviert?", action, status)
	}
	return vals, nil
}

func leafValues(data []byte) map[string]string {
	out := map[string]string{}
	dec := xml.NewDecoder(bytes.NewReader(data))
	var cur string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return out
		}
		switch t := tok.(type) {
		case xml.StartElement:
			cur = t.Name.Local
			text.Reset()
		case xml.CharData:
			if text.Len() < 4096 {
				text.Write(t)
			}
		case xml.EndElement:
			if t.Name.Local == cur {
				out[cur] = strings.TrimSpace(text.String())
			}
			cur = ""
		}
	}
}

type hostList struct {
	Items []struct {
		IP     string `xml:"IPAddress"`
		MAC    string `xml:"MACAddress"`
		Active string `xml:"Active"`
		Name   string `xml:"HostName"`
		Iface  string `xml:"InterfaceType"`
	} `xml:"Item"`
}

// Hosts returns the device list known to the box.
func (c *Client) Hosts(ctx context.Context) ([]Host, error) {
	vals, err := c.soap(ctx, "hosts", hostsService, "X_AVM-DE_GetHostListPath", nil)
	if err == nil {
		if p := vals["NewX_AVM-DE_HostListPath"]; p != "" {
			if hs, err2 := c.fetchHostList(ctx, p); err2 == nil {
				return hs, nil
			}
		}
	}
	// Fallback for older firmware: enumerate entries one by one.
	vals, err2 := c.soap(ctx, "hosts", hostsService, "GetHostNumberOfEntries", nil)
	if err2 != nil {
		if err != nil {
			return nil, err
		}
		return nil, err2
	}
	n, _ := strconv.Atoi(vals["NewHostNumberOfEntries"])
	n = min(n, 512)
	hs := make([]Host, 0, n)
	for i := 0; i < n; i++ {
		v, err := c.soap(ctx, "hosts", hostsService, "GetGenericHostEntry", [][2]string{{"NewIndex", strconv.Itoa(i)}})
		if err != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			continue
		}
		hs = append(hs, Host{IP: v["NewIPAddress"], MAC: v["NewMACAddress"], Name: v["NewHostName"],
			Iface: v["NewInterfaceType"], Active: v["NewActive"] == "1",
			Net: netLabel("", "", "", "", v["NewIPAddress"], v["NewInterfaceType"])})
	}
	return hs, nil
}

func (c *Client) fetchHostList(ctx context.Context, path string) ([]Host, error) {
	ref, err := url.Parse(path)
	if err != nil {
		return nil, err
	}
	u := c.tr064.ResolveReference(ref)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.short.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Geräteliste: HTTP %d", resp.StatusCode)
	}
	var hl hostList
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&hl); err != nil {
		return nil, err
	}
	hs := make([]Host, 0, len(hl.Items))
	for _, it := range hl.Items {
		hs = append(hs, Host{IP: it.IP, MAC: it.MAC, Name: it.Name, Iface: it.Iface, Active: it.Active == "1",
			Net: netLabel("", "", "", "", it.IP, it.Iface)})
	}
	return hs, nil
}

type netDevEntry struct {
	Name       string          `json:"name"`
	MAC        string          `json:"mac"`
	Type       string          `json:"type"`
	Port       string          `json:"port"`
	Classes    json.RawMessage `json:"classes"`
	Properties []struct {
		Txt string `json:"txt"`
	} `json:"properties"`
	IPv4 struct {
		IP string `json:"ip"`
	} `json:"ipv4"`
}

// NetDevices reads the device list from the web UI (data.lua, page netDev).
// It works with a normal session ID, even when TR-064 is disabled.
// The box itself is returned with Router=true.
func (c *Client) NetDevices(ctx context.Context, sid string) ([]Host, error) {
	u := *c.base
	u.Path = strings.TrimRight(u.Path, "/") + "/data.lua"
	form := url.Values{"sid": {sid}, "page": {"netDev"}, "xhrId": {"all"}, "xhr": {"1"}, "lang": {"de"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.String(), strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.short.Do(req)
	if err != nil {
		return nil, c.netErr(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("Geräteliste (data.lua): HTTP %d", resp.StatusCode)
	}
	var out struct {
		Data struct {
			Active  []netDevEntry `json:"active"`
			Passive []netDevEntry `json:"passive"`
			Fbox    []netDevEntry `json:"fbox"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&out); err != nil {
		return nil, errors.New("Geräteliste (data.lua) hat ein unbekanntes Format")
	}
	hs := make([]Host, 0, len(out.Data.Active)+len(out.Data.Passive)+1)
	add := func(list []netDevEntry, active, router bool) {
		for _, e := range list {
			if e.MAC == "" && e.IPv4.IP == "" {
				continue
			}
			iface := "Ethernet"
			if strings.EqualFold(e.Type, "wlan") || strings.Contains(strings.ToUpper(e.Port), "WLAN") {
				iface = "802.11"
			}
			var props []string
			for _, p := range e.Properties {
				props = append(props, p.Txt)
			}
			net := ""
			if !router && active {
				net = netLabel(e.Type, e.Port, string(e.Classes), strings.Join(props, " "), e.IPv4.IP, iface)
			}
			hs = append(hs, Host{IP: e.IPv4.IP, MAC: e.MAC, Name: e.Name, Iface: iface, Active: active, Router: router, Net: net})
		}
	}
	add(out.Data.Active, true, false)
	add(out.Data.Passive, false, false)
	add(out.Data.Fbox, true, true)
	if len(hs) == 0 {
		return nil, errors.New("Geräteliste (data.lua) ist leer")
	}
	return hs, nil
}
