// FritzFlow shows live which device in your home network talks to whom,
// based on the packet capture built into the FRITZ!Box.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/sabieler/fritzFlow/internal/browser"
	"github.com/sabieler/fritzFlow/internal/config"
	"github.com/sabieler/fritzFlow/internal/engine"
	"github.com/sabieler/fritzFlow/internal/fakebox"
	"github.com/sabieler/fritzFlow/internal/web"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Printf("Fehler: %v", err)
		if runtime.GOOS == "windows" {
			fmt.Fprintln(os.Stderr, "\nEnter drücken zum Beenden …")
			_, _ = fmt.Scanln()
		}
		os.Exit(1)
	}
}

func run() error {
	cfgPath := flag.String("config", config.DefaultPath(), "Pfad zur Konfigurationsdatei")
	demo := flag.Bool("demo", false, "Demo-Modus mit simulierter FRITZ!Box (keine echte Box nötig)")
	listen := flag.String("listen", "", "Adresse der Weboberfläche, z. B. 127.0.0.1:8484 (überschreibt Konfiguration)")
	noBrowser := flag.Bool("no-browser", false, "Browser beim Start nicht automatisch öffnen")
	showVersion := flag.Bool("version", false, "Version anzeigen")
	flag.Parse()
	if *showVersion {
		fmt.Println("FritzFlow", version)
		return nil
	}
	logger := log.New(os.Stderr, "", log.LstdFlags)
	log.SetFlags(log.LstdFlags)

	cfg, exists, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	opts := engine.Options{ConfigPath: *cfgPath, Version: version, Logger: logger}
	if *demo {
		box, err := fakebox.Start("demo", "demo")
		if err != nil {
			return err
		}
		defer box.Close()
		cfg.FritzBox, cfg.TR064URL = box.URL, box.URL
		cfg.Username, cfg.Password = "demo", "demo"
		cfg.ReverseDNS = false
		opts.ReadOnly, opts.Demo = true, true
		logger.Printf("Demo-Modus: simulierte FRITZ!Box unter %s", box.URL)
	} else if !exists {
		logger.Printf("Noch keine Konfiguration – die Einrichtung erfolgt im Browser (%s)", *cfgPath)
	} else {
		logger.Printf("Konfiguration: %s", *cfgPath)
	}

	ln, err := listenWithFallback(cfg.Listen)
	if err != nil {
		return err
	}
	addr := ln.Addr().(*net.TCPAddr)
	allowRemote := !addr.IP.IsLoopback()
	if allowRemote {
		logger.Printf("ACHTUNG: Die Oberfläche ist im Netzwerk erreichbar (%s) – jeder im LAN sieht den Datenverkehr.", addr)
	}

	eng := engine.New(cfg, opts)
	srv := &http.Server{
		Handler:           web.Handler(eng, allowRemote),
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       2 * time.Minute,
		ErrorLog:          log.New(discard{}, "", 0),
	}
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Printf("Webserver: %v", err)
			stop()
		}
	}()

	host := addr.IP.String()
	if addr.IP.IsLoopback() || addr.IP.IsUnspecified() {
		host = "localhost"
	}
	uiURL := "http://" + net.JoinHostPort(host, strconv.Itoa(addr.Port))
	logger.Printf("FritzFlow %s läuft: %s  (Beenden mit Strg+C)", version, uiURL)
	if cfg.OpenBrowser && !*noBrowser {
		if err := browser.Open(cfg.Browser, uiURL); err != nil {
			logger.Printf("Browser %q konnte nicht gestartet werden (%v) – öffne Standardbrowser", cfg.Browser, err)
			_ = browser.Open("", uiURL)
		}
	}

	eng.Run(ctx)
	logger.Printf("Beende …")
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = srv.Shutdown(sctx)
	return nil
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

// listenWithFallback tries the configured port and the next nine.
func listenWithFallback(addr string) (net.Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err == nil {
		return ln, nil
	}
	host, portStr, perr := net.SplitHostPort(addr)
	port, aerr := strconv.Atoi(portStr)
	if perr != nil || aerr != nil {
		return nil, fmt.Errorf("ungültige Listen-Adresse %q: %w", addr, err)
	}
	for p := port + 1; p <= port+9; p++ {
		if ln, err2 := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(p))); err2 == nil {
			log.Printf("Port %d belegt – verwende %d", port, p)
			return ln, nil
		}
	}
	return nil, fmt.Errorf("kann %s nicht öffnen: %w", addr, err)
}
