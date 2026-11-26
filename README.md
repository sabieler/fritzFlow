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
- **Filter** für die ganze Ansicht: Verkehr *Internet / Intern / Alle*, Netz *LAN / WLAN 2,4 GHz / WLAN 5 GHz / Gast*, einzelnes Gerät, Suche
- **Sortierung pro Ansicht**: nach Aktivität oder ruhig/statisch (Fest, Volumen, A–Z)
- **Name, Modell und FRITZ!OS-Version** der Box werden angezeigt
- Dark Mode (Standard) und Light Mode
- **Demo-Modus** mit simulierter FRITZ!Box: `fritzflow -demo`

## Schnellstart

1. Unter **[Releases](https://github.com/sabieler/fritzFlow/releases/latest)** die passende Datei herunterladen
   (das Repo ist privat – dafür musst du bei GitHub angemeldet und Mitglied des Repos sein):

   | System | Datei |
   |---|---|
   | Windows (Intel/AMD) | `fritzflow-…-windows-amd64.exe` |
   | Windows (ARM) | `fritzflow-…-windows-arm64.exe` |
   | macOS (Apple Silicon M1–M4) | `fritzflow-…-darwin-arm64` |
   | macOS (Intel) | `fritzflow-…-darwin-amd64` |
   | Linux (PC) | `fritzflow-…-linux-amd64` |
   | Raspberry Pi 4/5 (64 Bit) | `fritzflow-…-linux-arm64` |

   Starten:
   - **Windows:** Datei doppelklicken. Bei „Der Computer wurde durch Windows geschützt“
     auf *Weitere Informationen → Trotzdem ausführen* klicken (die Datei ist nicht signiert).
   - **macOS:** im Terminal `chmod +x fritzflow-…-darwin-arm64 && xattr -d com.apple.quarantine fritzflow-…-darwin-arm64 && ./fritzflow-…-darwin-arm64`
   - **Linux:** `chmod +x fritzflow-…-linux-amd64 && ./fritzflow-…-linux-amd64`

   Oder selbst bauen (Go ≥ 1.21): `go build -o fritzflow .` bzw. `./scripts/build.sh` für alle Plattformen.
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
| `browser` | *(Systemstandard)* | Browser für die Oberfläche, z. B. `vivaldi`, `Firefox` (macOS-App-Name) oder `C:\…\chrome.exe` – auch im Einstellungsdialog wählbar |

Kommandozeile: `-config <pfad>`, `-listen <addr>`, `-no-browser`, `-demo`, `-version`.

## Fehlerbehebung

- **Keine Live-Daten im Browser:** Werbe-/Tracker-Blocker (z. B. in Vivaldi, Brave, uBlock Origin)
  können lokale Datenströme stören. FritzFlow weicht automatisch auf einfache Abfragen aus und
  zeigt sonst einen Hinweis – dann den Blocker für `localhost` deaktivieren.
- **„Auf 1-lan läuft bereits ein anderer Mitschnitt“:** Die FRITZ!Box erlaubt pro Schnittstelle nur
  einen Mitschnitt. Läuft FritzFlow auf zwei Rechnern oder ist der Mitschnitt in der FRITZ!Box-Oberfläche
  aktiv, wartet FritzFlow. Hängende Mitschnitte einer abgestürzten Instanz beendet FritzFlow selbst.
- **Gerätenamen fehlen:** FritzFlow liest sie aus der Heimnetz-Übersicht bzw. per TR-064
  (*Heimnetz → Netzwerk → Netzwerkeinstellungen → Zugriff für Anwendungen zulassen*).

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
./scripts/build.sh            # alle Plattformen nach dist/ (lokal, nicht im Repo)
git tag v0.2.0 && git push origin v0.2.0   # baut & veröffentlicht automatisch ein Release
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
