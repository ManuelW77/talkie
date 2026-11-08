# Talkie – Hinweise für Claude

Sprach- und Text-Chat für Manuels Sohn und seine Freunde: sicher, kinderleicht einzurichten,
datensparsam (die Kids zocken nebenher) und lauffähig auf älteren Rechnern.

- **Server:** Go, läuft im Docker-Container (`server/`)
- **Desktop-Client:** Flutter-Oberfläche + Rust-Kern über flutter_rust_bridge (`app/`)
- **Handy/Tablet:** dieselbe Flutter-App mit demselben Rust-Kern (`app/android`, `app/ios`)

## Parallele Sitzungen

Arbeiten mehrere Claude-Sitzungen gleichzeitig, bekommt jede einen eigenen Arbeitsordner
(`git worktree add ../talkie-<name> -b feature/<name> devel`). Im selben Ordner wechseln sich die
Sitzungen sonst gegenseitig den Branch weg. Beim Committen nur eigene Dateien hinzufügen.

## Sprache

- Antworten, Kommentare, UI-Texte, Commit-Nachrichten und Doku auf **Deutsch** mit korrekten Umlauten
  (ä, ö, ü, ß – nie ae/oe/ue). Code-Bezeichner bleiben englisch.
- Die Oberfläche richtet sich an Kinder und Eltern: kurze, freundliche Texte ohne Fachbegriffe.

## Git-Workflow

| Branch | Zweck | CI |
|---|---|---|
| `main` | Releases. Tags `v*` erzeugen ein GitHub-Release mit allen Builds. | Tests + Builds |
| `devel` | Integrations- und Test-Stand | Tests + Builds |
| `feature/<name>` | ein Feature pro Branch, von `devel` abzweigen | nur Tests |
| `fix/<name>`, `chore/<name>` | Fehlerbehebung, Pflege (Doku, CI, Abhängigkeiten) | nur Tests |

1. Neue Arbeit immer auf einem eigenen Branch von `devel` beginnen (`git checkout -b feature/xyz devel`).
2. **Nach jedem erfolgreichen Build/Testlauf committen** (auf dem Feature-Branch).
3. Am Ende der Arbeit: Feature-Branch pushen, nach `devel` mergen (`git merge --no-ff`) und **`devel` pushen**.
   Die CI baut dann Windows, Linux und macOS zum Testen.
4. Merge `devel` → `main` und Release-Tags **nur auf ausdrückliche Anweisung von Manuel**.
   Versionsnummer: `version:` in `app/pubspec.yaml` und `kAppVersion` in `app/lib/version.dart` gleich halten
   (Test prüft das), Tag heißt `v<version>`. Der Server meldet den Apps das neueste GitHub-Release.
   Ablauf und Secrets für die Mac-Notarisierung: `docs/RELEASE.md`.
5. Nie direkt auf `main` committen. Kein Force-Push auf `main` oder `devel`.
6. Commit-Nachrichten auf Deutsch: kurze Betreffzeile, darunter Stichpunkte.

Das Repo ist **öffentlich** (github.com/ManuelW77/talkie). Keine Geheimnisse committen: keine `.env`,
keine Server-Adressen, Tokens oder Schlüssel. Server-Adresse und Fingerabdruck kommen als
Repo-Variablen `TALKIE_SERVER` / `TALKIE_FINGERPRINT` in die CI.

## Obsidian-Notizen (Vault „Manuel“, Ordner `Development/Talkie/`)

- `Testserver.md` – Links und Zugang zum lokalen Docker-Testbetrieb
- `Befehle und Installation.md` – Installation, Verwaltungsbefehle, Builds, Verteilung
- `ToDo Build.md` – alles, was noch zu bauen ist. **Neue Ideen und Aufträge von Manuel hier eintragen.**
- `ToDo Tests.md` – was Manuel aktiv testen soll

Pflege: Ist eine Aufgabe gebaut, wird sie in `ToDo Build` abgehakt (`- [x]`) und unter „Erledigt“
verschoben; konkrete Testschritte (Abschnitt mit Branch-Namen) kommen nach `ToDo Tests`. Ändern sich Befehle, Ports oder Links, die beiden
anderen Notizen mitziehen. Bearbeiten über die Obsidian-MCP-Tools.

## Aufbau

```
server/            Go-Server (QUIC/UDP 7777), Admin-CLI, Verwaltungsseite (HTTP 8080)
  web/             HTML-Vorlagen und CSS/JS der Verwaltungsseite (per go:embed eingebettet)
app/lib/           Flutter-Oberfläche (ChangeNotifier-Controller, deutsche UI, dunkles Theme)
app/rust/src/core/ Rust-Kern: Netzwerk (quinn), Audio (cpal), Opus, Rauschfilter, Jitter-Puffer
app/rust/src/api/  Schnittstelle zu Flutter (flutter_rust_bridge)
app/assets/icon/   App-Icon (talkie.svg ist die Vorlage)
docs/PROTOCOL.md   Netzwerkprotokoll – bei jeder Protokolländerung mitpflegen
.github/workflows/ CI: Tests, Builds für Windows/Linux/macOS, Release
```

## Wichtige Entscheidungen

- **Transport:** QUIC (ALPN `talkie/1`), Sprache als Datagramme, Steuerung als JSON-Frames.
  Selbstsigniertes Zertifikat, der Client pinnt den Fingerabdruck.
- **Audio:** Opus 16 kbit/s, 40-ms-Frames, mono, DTX → ca. 25 kbit/s pro Sprecher. Datensparsamkeit hat Vorrang.
- **Anmeldung in der App:** Ed25519-Geräteschlüssel + einmaliger Einladungscode, kein Passwort, keine E-Mail.
- **Verwaltungsseite:** eigene Web-Konten (E-Mail), getrennt von den Talkie-Nutzern. Gruppen Admin/Benutzer,
  Rechte pro Konto. Login per Mail-Link + 8-stelligem Code oder OIDC (Manuel nutzt Pocket ID).
- **Alte Systeme:** macOS ab 10.15 (Intel-Mac des Neffen mit Big Sur), Windows ab 10, Linux x64,
  Android ab 8.0 (API 26, wegen AAudio), iOS ab 15 (Manuel will mindestens 16 unterstützen).
  Keine neuen Plattform-APIs ohne Rückfallebene.

## Befehle

Rust und Flutter brauchen einen angepassten PATH (siehe Build-Fallen):

```bash
export PATH=/opt/homebrew/opt/rustup/bin:$HOME/.cargo/bin:/usr/bin:/bin:/usr/sbin:/sbin:/opt/homebrew/bin
```

| Was | Befehl |
|---|---|
| Server-Tests | `cd server && go test -race ./...` |
| Rust-Tests | `cd app/rust && cargo test --lib` |
| Ende-zu-Ende gegen laufenden Server | `cd app/rust && cargo run --release --example e2e -- 127.0.0.1:7777 <CODE für ≥3 Personen>` |
| Flutter prüfen | `cd app && flutter analyze && flutter test` (braucht `FLUTTER_ROOT`) |
| macOS-App bauen | `app/tool/build_macos.sh` |
| Android-APK bauen | `cd app && flutter build apk --release` (mit PATH wie oben) |
| iOS-App für den Simulator | `app/tool/build_ios_sim.sh` |
| iOS fürs iPhone | macht Manuel selbst in Xcode (signieren, Archive, Upload). Vorher `flutter build ios --config-only --release`. Nicht in die CI einbauen. Buildnummer zählt Xcode bei jedem Archive in `pubspec.yaml` hoch (+N), danach committen. |
| Handy-Ende-zu-Ende (Simulator/Emulator) | `flutter test integration_test -d <gerät> --dart-define=TEST_SERVER=127.0.0.1:7777 --dart-define=TEST_INVITE=<CODE>` (Android-Emulator: `10.0.2.2:7777`, Mikrofon vorher erlauben, siehe Kopf von `integration_test/app_test.dart`) |
| Icons neu erzeugen | `app/tool/make_icons.sh` |
| Lokaler Testbetrieb mit Verwaltung + Mailpit | `server/dev.sh up` → http://localhost:8090 (admin@talkie.local), Mails: http://localhost:8026 |
| Browser-Smoke-Test der Verwaltungsseite (echter Chrome, braucht `dev.sh up`) | `node server/tool/browser_smoke.mjs` |
| Lokaler Server ohne Webseite | `cd server && docker compose up -d --build` |
| Einladung (lokal) | `docker exec talkie talkie-server invite 3` |
| Screenshots der Verwaltungsseite | `TALKIE_SNAPSHOT=<ordner> go test -run Snapshot ./...` in `server/`, dann Headless-Chrome |

Nach dem Ende-zu-Ende-Test die Testnutzer (`Anna…`, `Ben…`) und die Test-Einladung vom lokalen Server
wieder löschen (`user-del`, `invite-del`).

## Build-Fallen auf Manuels Mac

- `~/.swiftly/bin/cc` und `lipo` zeigen auf eine fehlende Swift-Toolchain → `.swiftly` aus dem PATH lassen.
- Rust liegt unter `/opt/homebrew/opt/rustup/bin` und `~/.cargo/bin`, nicht im Standard-PATH.
- **`~/.zshrc` nicht ändern** – den PATH pro Befehl setzen.
- Xcode 27 baut nur für macOS 12+ und nur eine Architektur. `build_macos.sh` baut lokal daher nur arm64.
  Den Universal-Build ab macOS 10.15 macht die CI mit Xcode 16.
- iOS: Xcode 27 kann erst ab iOS 15 bauen und lokal nur arm64 → `build_ios_sim.sh`. Nach Änderungen am
  Podspec (`rust_builder/ios`) einmal `cd app/ios && pod install`.
- Android: libopus wird per CMake gebaut; `rust_builder/cargokit/gradle/plugin.gradle` reicht dafür
  `ANDROID_NDK_ROOT` weiter. Der Emulator „Medium_Phone_API_35“ ist fast voll – zum Testen „test_API_27“ nehmen.
  Das Emulator-Mikrofon geht nicht (fehlender Mac-Zugriff), Sprache nur auf echten Geräten prüfbar.
- Fenster-Screenshots gehen nicht (keine Bildschirmaufnahme-Berechtigung). Flutter-UI über die
  Golden-Tests in `app/test/` prüfen, die Verwaltungsseite per Headless-Chrome.

## Qualität

- Vor jedem Merge nach `devel`: alle betroffenen Tests grün (Server mit `-race`). Bei Änderungen an der
  Verwaltungsseite zusätzlich `browser_smoke.mjs` – Go-Tests sehen kein echtes Browserverhalten
  (z. B. schickt Chrome bei `Referrer-Policy: no-referrer` „Origin: null“). Bei Protokoll- oder
  Kernänderungen zusätzlich den Ende-zu-Ende-Test laufen lassen.
- Sicherheit ernst nehmen: Rate-Limits, atomare Prüfungen, keine Nutzerdaten in Logs, CSRF auf der
  Verwaltungsseite. Neue Abhängigkeiten in der CI auf Commit-SHAs pinnen (Fremd-Actions).
- Was nicht getestet werden konnte (z. B. echtes Mikrofon, Windows-Build lokal), offen dazusagen.
