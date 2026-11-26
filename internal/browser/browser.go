// Package browser finds installed web browsers and opens URLs in them.
package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Browser is an installed browser. Cmd is the value stored in the config.
type Browser struct {
	Name string `json:"name"`
	Cmd  string `json:"cmd"`
}

// Validate rejects values that cannot be a browser name or path.
func Validate(b string) error {
	if len(b) > 512 {
		return errors.New("Browser-Angabe ist zu lang")
	}
	for _, r := range b {
		if r < 0x20 || r == 0x7f {
			return errors.New("Browser-Angabe enthält Steuerzeichen")
		}
	}
	if runtime.GOOS == "windows" && strings.ContainsAny(b, "&|<>^%\"") {
		return errors.New("Browser-Angabe enthält unzulässige Zeichen (& | < > ^ % \")")
	}
	return nil
}

// Detect lists browsers installed on this computer.
func Detect() []Browser {
	var out []Browser
	seen := map[string]bool{}
	add := func(name, cmd string) {
		if !seen[name] {
			seen[name] = true
			out = append(out, Browser{Name: name, Cmd: cmd})
		}
	}
	switch runtime.GOOS {
	case "darwin":
		dirs := []string{"/Applications"}
		if h, err := os.UserHomeDir(); err == nil {
			dirs = append(dirs, filepath.Join(h, "Applications"))
		}
		for _, app := range []string{"Safari", "Google Chrome", "Firefox", "Microsoft Edge", "Brave Browser", "Arc", "Vivaldi", "Opera", "Chromium", "Orion", "Zen Browser"} {
			for _, d := range dirs {
				if st, err := os.Stat(filepath.Join(d, app+".app")); err == nil && st.IsDir() {
					add(app, app)
					break
				}
			}
		}
	case "windows":
		bases := []string{os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData")}
		cands := []struct{ name, rel string }{
			{"Google Chrome", `Google\Chrome\Application\chrome.exe`},
			{"Mozilla Firefox", `Mozilla Firefox\firefox.exe`},
			{"Microsoft Edge", `Microsoft\Edge\Application\msedge.exe`},
			{"Brave", `BraveSoftware\Brave-Browser\Application\brave.exe`},
			{"Vivaldi", `Vivaldi\Application\vivaldi.exe`},
			{"Opera", `Programs\Opera\opera.exe`},
			{"Chromium", `Chromium\Application\chrome.exe`},
		}
		for _, c := range cands {
			for _, b := range bases {
				if b == "" {
					continue
				}
				p := filepath.Join(b, c.rel)
				if st, err := os.Stat(p); err == nil && !st.IsDir() {
					add(c.name, p)
					break
				}
			}
		}
	default:
		for _, c := range []struct{ bin, name string }{
			{"firefox", "Firefox"}, {"google-chrome", "Google Chrome"}, {"google-chrome-stable", "Google Chrome"},
			{"chromium", "Chromium"}, {"chromium-browser", "Chromium"}, {"brave-browser", "Brave"},
			{"microsoft-edge", "Microsoft Edge"}, {"microsoft-edge-stable", "Microsoft Edge"},
			{"vivaldi", "Vivaldi"}, {"vivaldi-stable", "Vivaldi"}, {"opera", "Opera"},
			{"epiphany", "GNOME Web"}, {"falkon", "Falkon"}, {"librewolf", "LibreWolf"},
		} {
			if _, err := exec.LookPath(c.bin); err == nil {
				add(c.name, c.bin)
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Open opens url in the given browser ("" = system default).
func Open(b, url string) error {
	b = strings.TrimSpace(b)
	if err := Validate(b); err != nil {
		return err
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		switch {
		case b == "":
			cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
		case fileExists(b):
			cmd = exec.Command(b, url)
		default: // e.g. "chrome", "msedge", "firefox" via App Paths
			cmd = exec.Command("cmd", "/c", "start", "", b, url)
		}
	case "darwin":
		switch {
		case b == "":
			cmd = exec.Command("open", url)
		case fileExists(b) && !strings.HasSuffix(strings.TrimRight(b, "/"), ".app"):
			cmd = exec.Command(b, url)
		default: // app name or .app bundle path
			cmd = exec.Command("open", "-a", b, url)
		}
	default:
		if b == "" {
			cmd = exec.Command("xdg-open", url)
		} else {
			p, err := exec.LookPath(b)
			if err != nil {
				return errors.New("Browser \"" + b + "\" nicht gefunden")
			}
			cmd = exec.Command(p, url)
		}
	}
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && (!st.IsDir() || strings.HasSuffix(p, ".app"))
}
