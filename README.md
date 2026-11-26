# fritzFlow

**Live sehen, welches Gerät in deinem Heimnetz mit wem spricht – direkt aus der FRITZ!Box.**

FritzFlow nutzt den eingebauten Paketmitschnitt der FRITZ!Box und macht daraus ein
Live-Dashboard: Geräte, Ziele (Domains/Dienste), Durchsatz und jede einzelne Verbindung.
Eine einzige Programmdatei für Windows, macOS und Linux – ohne Installation, ohne
Abhängigkeiten.

## Funktionen

- **Live-Verkehrskarte**: Geräte → FRITZ!Box → Ziele, animiert nach Durchsatz
- **Verbindungstabelle**: Gerät, Ziel-Hostname, Dienst, Rate, Volumen, Dauer – sortier- und filterbar
- **Hostnamen** aus DNS-Antworten, TLS-SNI und HTTP-Host; optional Reverse-DNS
- **Gerätenamen** aus der FRITZ!Box (TR-064 oder – falls deaktiviert – Heimnetz-Übersicht)
- **Download/Upload**, Verbindungen, aktive Geräte, 5-Minuten-Verlauf, Dienste-Verteilung
- Dark Mode (Standard) und Light Mode
- **Demo-Modus** mit simulierter FRITZ!Box: `fritzflow -demo`

## Schnellstart

1. Passende Datei aus `dist/` herunterladen und starten:
   - Windows: `fritzflow-…-windows-amd64.exe` doppelklicken
   - macOS: `chmod +x fritzflow-…-darwin-arm64 && ./fritzflow-…-darwin-arm64`
     (bei Gatekeeper-Warnung: `xattr -d com.apple.quarantine <datei>`)
   - Linux: `chmod +x fritzflow-…-linux-amd64 && ./fritzflow-…-linux-amd64`
2. Der Browser öffnet sich automatisch unter <http://localhost:8484>.
3. Im Einrichtungsdialog FRITZ!Box-Adresse, Benutzer und Passwort eingeben →
   **Verbindung testen** → **Speichern & starten**.

### Empfohlener FRITZ!Box-Benutzer

Lege unter *System → FRITZ!Box-Benutzer* einen eigenen Benutzer an (z. B. `fritzFlow`):

- Recht **„FRITZ!Box Einstellungen“** (zwingend für den Paketmitschnitt)
- **kein** „Zugang aus dem Internet“

So bleibt dein Admin-Zugang unberührt, und der Zugang lässt sich jederzeit sperren.

## Schonung der FRITZ!Box

- Pro Paket überträgt die Box nur die ersten Bytes (**Detailgrad**, Standard 200 B) – keine Inhalte.
- Standardmäßig läuft der Mitschnitt **nur, solange die Ansicht geöffnet ist** (20 s Nachlauf).
- Die Geräteliste wird nur alle 5 Minuten abgefragt.
- Die aktuelle Zusatzlast siehst du unten in der Fußzeile („Mitschnitt-Last“).
- Bei Fehlern wird mit wachsendem Abstand neu verbunden; nach falschem Passwort frühestens nach 60 s,
  damit die Box keine Anmeldesperre verhängt.

| Detailgrad | Bytes/Paket | Wofür |
|---|---|---|
| Sparsam | 128 | geringste Last, weniger Hostnamen |
| Ausgewogen | 200 | Standard |
| Detailliert | 512 | erkennt mehr Hostnamen (TLS-SNI) |

## Konfiguration

Die Einstellungen werden im Browser vorgenommen und gespeichert unter

- Linux: `~/.config/fritzflow/config.json`
- macOS: `~/Library/Application Support/fritzflow/config.json`
- Windows: `%AppData%\fritzflow\config.json`

Die Datei ist nur für den eigenen Benutzer lesbar (0600). Alternativ per Umgebungsvariablen
(werden nicht in die Datei geschrieben): `FRITZFLOW_HOST`, `FRITZFLOW_USER`, `FRITZFLOW_PASSWORD`.

| Schlüssel | Standard | Bedeutung |
|---|---|---|
| `fritzbox` | `http://fritz.box` | Adresse der Box |
| `username` / `password` | – | FRITZ!Box-Benutzer |
| `interface` | `1-lan` | Mitschnitt-Schnittstelle (`1-lan` = LAN+WLAN, `2-1` = Internet) |
| `snaplen` | `200` | Bytes pro Paket (64–1600) |
| `capture_mode` | `on-demand` | oder `always` |
| `reverse_dns` | `true` | unbekannte IPs per PTR benennen |
| `listen` | `127.0.0.1:8484` | Adresse der Weboberfläche |
| `open_browser` | `true` | Browser beim Start öffnen |

Kommandozeile: `-config <pfad>`, `-listen <addr>`, `-no-browser`, `-demo`, `-version`.

## Sicherheit

- Die Oberfläche lauscht standardmäßig nur auf `127.0.0.1`. Andere Hostnamen werden abgewiesen
  (Schutz vor DNS-Rebinding), schreibende Anfragen von fremden Webseiten ebenfalls.
- Wer `listen` auf `0.0.0.0` stellt, macht den Datenverkehr für alle im LAN sichtbar.
- Der Mitschnitt zeigt, mit welchen Diensten Geräte kommunizieren. Informiere Mitbewohner/Gäste.

## Wie es funktioniert

```
FRITZ!Box ──/cgi-bin/capture_notimeout──▶ pcap-Stream (nur Header)
          ──login_sid.lua (PBKDF2)──────▶ Session-ID
          ──TR-064 / data.lua───────────▶ Gerätenamen
                         │
                 FritzFlow (Go, Standardbibliothek)
   pcap/pcapng-Parser → Decoder (Ethernet/VLAN/IPv4/IPv6/TCP/UDP)
   → DNS/SNI/HTTP-Namen → Verbindungstabelle → Snapshot (1/s)
                         │  Server-Sent Events
                 Browser-UI (Vanilla JS, Canvas)
```

Router-MAC-Erkennung ordnet jede Verbindung dem lokalen Gerät zu – auch bei IPv6
mit Privacy-Adressen.

## Entwicklung

```sh
go test ./...                 # Unit- und Ende-zu-Ende-Tests (gegen simulierte Box)
go test -fuzz=FuzzDecode ./internal/flow
go run . -demo                # UI mit simulierten Daten
./scripts/build.sh            # alle Plattformen nach dist/
```

Struktur:

| Paket | Aufgabe |
|---|---|
| `internal/fritz` | Login, Mitschnitt-Stream, TR-064, Geräteliste |
| `internal/pcap` | pcap / pcapng / „modified pcap“ (FRITZ!OS) |
| `internal/flow` | Paket-Decoder, DNS/SNI, Verbindungs- und Geräte-Tracking |
| `internal/engine` | Ablaufsteuerung, Reconnect, On-Demand-Mitschnitt |
| `internal/web` | HTTP/SSE-Server, eingebettete Oberfläche |
| `internal/fakebox` | simulierte FRITZ!Box für Demo und Tests |

Keine externen Go-Module, keine CDN-Abhängigkeiten.
