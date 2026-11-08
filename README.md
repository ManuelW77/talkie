# Talkie

Sprach- und Textchat für eine kleine, geschlossene Gruppe (z. B. Kinder und ihre Freunde beim Zocken).
Einfach, sicher und sparsam:

- **Rein nur mit Einladungscode.** Danach meldet sich das Gerät automatisch an, ohne Passwort.
- **Verschlüsselt** (QUIC/TLS 1.3). Die Clients merken sich das Server-Zertifikat und verweigern einen fremden Server.
- **Datensparsam:** Opus mit 16 kbit/s, 40-ms-Pakete, gesendet wird nur beim Sprechen.
  Das sind etwa **25 kbit/s pro sprechender Person** (rund 11 MB pro Stunde Dauerreden), in Pausen nahezu 0.
- **Rauschfilter** (RNNoise) und automatische Sprachaktivierung, keine Taste nötig.
- Räume, Text-Chat pro Raum, Stumm- und Taub-Schalter.

```
server/   Go-Server (Docker), Verwaltung per Kommandozeile
app/      Flutter-App (Windows, macOS, Linux) mit Rust-Kern in app/rust
docs/     Protokoll
```

---

## 1. Server starten

Voraussetzung: Docker. Der Server braucht nur **einen UDP-Port: 7777**.

```bash
cd server
docker compose up -d
docker logs talkie        # zeigt den Fingerabdruck des Zertifikats
```

Läuft der Server zu Hause, muss im Router eine **Portfreigabe für UDP 7777** auf den Rechner mit Docker
eingerichtet werden. Für eine wechselnde IP braucht es zusätzlich einen DynDNS-Namen.
Einfacher geht es mit einem kleinen VPS (ca. 4 €/Monat).

### Verwaltung

Alle Befehle laufen im Container:

```bash
docker exec talkie talkie-server invite          # Einladungscode für 1 Person, 7 Tage gültig
docker exec talkie talkie-server invite 5 14     # für 5 Personen, 14 Tage
docker exec talkie talkie-server users           # wer ist registriert / online
docker exec talkie talkie-server kick Max        # rauswerfen (darf wiederkommen)
docker exec talkie talkie-server ban Max         # sperren
docker exec talkie talkie-server unban Max
docker exec talkie talkie-server rename Max Maxi
docker exec talkie talkie-server user-del Max    # Zugang löschen (braucht neue Einladung)
docker exec talkie talkie-server rooms
docker exec talkie talkie-server room-add "Minecraft"
docker exec talkie talkie-server room-del "Zocken 2"
docker exec talkie talkie-server chat Lobby 50   # letzte 50 Chatnachrichten eines Raums
docker exec talkie talkie-server status          # Fingerabdruck + Anzahl online
docker exec talkie talkie-server web-admin du@example.org   # Admin für die Verwaltungsseite (auch Notfall)
```

### Verwaltungsseite im Browser

Alles, was die Befehle oben können, geht auch bequem im Browser, auch für andere Eltern:
Kids einladen (auch per Mail), Nutzer umbenennen, rauswerfen, sperren und entfernen, Räume anlegen,
umbenennen, sortieren und löschen, den Chat mitlesen und (Admins) leeren. Ein Protokoll zeigt, wer was geändert hat.
Chatnachrichten werden nach 24 Stunden automatisch gelöscht (`TALKIE_CHAT_HOURS`, 0 = nie).

**Konten und Rechte:** Die Web-Konten (mit E-Mail-Adresse) sind getrennt von den Talkie-Nutzern der Kids.
Es gibt zwei Gruppen:

- **Admin** darf alles, auch Web-Konten anlegen und Rechte vergeben.
- **Benutzer** darf nur, was der Admin anhakt: Kids einladen, Talkie-Nutzer bearbeiten/rauswerfen/sperren,
  Talkie-Nutzer entfernen, Räume verwalten, Chat mitlesen.

**Anmeldung** ohne Passwort, auf zwei Wegen (einer reicht):

- **Per Mail:** E-Mail-Adresse eingeben, dann kommt ein Link und ein 8-stelliger Code (15 Minuten gültig,
  5 Versuche). Braucht einen SMTP-Zugang.
- **Per OIDC:** z. B. Pocket ID, Authentik, Keycloak, Google. Angemeldet wird nur, wer als Web-Konto mit
  derselben (bestätigten) E-Mail-Adresse eingetragen ist. Beim ersten Login wird das Konto fest mit der
  Identität beim Anbieter verknüpft.

**Einrichten:**

1. `server/.env.example` nach `server/.env` kopieren und ausfüllen. Die `.env` enthält Passwörter und
   wird nicht committet.
2. `docker compose up -d` startet den Server neu. Das Admin-Konto aus `TALKIE_ADMIN_EMAIL` wird angelegt,
   solange es noch keinen Admin gibt.
3. Die Seite lauscht nur lokal auf Port 8080. Von außen erreichst du sie über einen Reverse-Proxy mit HTTPS,
   zum Beispiel Caddy:
   ```
   talkie.example.org {
       reverse_proxy 127.0.0.1:8080
   }
   ```
   `TALKIE_PUBLIC_URL` muss genau die Adresse sein, die im Browser steht.

**Pocket ID:** Unter *OIDC Clients* einen Client anlegen, Callback-URL
`https://talkie.example.org/auth/oidc/callback`. Client-ID und Secret in die `.env`, dazu
`TALKIE_OIDC_ISSUER=https://id.example.org` (die Pocket-ID-Adresse) und `TALKIE_OIDC_NAME=Pocket ID`.
Meldet die Seite „E-Mail-Adresse nicht bestätigt“, bestätigt der Anbieter die Adressen nicht.
Bei einem eigenen Anbieter, bei dem nur du Konten anlegst, kannst du das mit
`TALKIE_OIDC_ALLOW_UNVERIFIED=1` abschalten. Bei öffentlichen Anbietern lass es aus.

**Lokal ausprobieren:** `server/dev.sh up` startet den Server mit Verwaltungsseite und
[Mailpit](https://mailpit.axllent.org) als Mail-Fänger. Alle Mails (Login-Codes, Einladungen) landen dort
und gehen nicht raus.

- Verwaltung: http://localhost:8090, anmelden mit `admin@talkie.local`
- Mails: http://localhost:8026
- Stoppen: `server/dev.sh down`

**Ausgesperrt?** `docker exec talkie talkie-server web-admin du@example.org` macht das Konto wieder zum
aktiven Admin.

Alle Daten (Datenbank, Zertifikat) liegen im Docker-Volume `talkie-data`.
**Wird das Volume gelöscht, entsteht ein neues Zertifikat.** Dann verweigern alle Clients die Verbindung
(Schutz gegen fremde Server) und müssen sich mit „Gerät abmelden“ und einem neuen Code neu einrichten.
Deshalb das Volume sichern:

```bash
docker run --rm -v server_talkie-data:/data -v "$PWD":/backup alpine tar czf /backup/talkie-backup.tgz -C /data .
```

---

## 2. Clients bauen

Am einfachsten über **GitHub Actions** (`.github/workflows/build.yml`). Jeder Push baut:

| Datei | System |
|---|---|
| `Talkie-macOS.dmg` | macOS ab 10.15, Intel **und** Apple Silicon (läuft also auch auf Big Sur) |
| `Talkie-Windows.zip` | Windows 10 und 11 (64 Bit), Laufzeit-DLLs liegen bei |
| `Talkie-Linux-x64.tar.gz` | Linux x64 (braucht GTK 3 und ALSA, ist auf gängigen Desktops vorhanden) |
| `Talkie-Android.apk` | Android ab 8.0, direkt installierbar (nicht über den Play Store) |

**Tipp:** Unter *Settings → Secrets and variables → Actions → Variables* im Repo
`TALKIE_SERVER` (z. B. `talkie.example.org:7777`) und `TALKIE_FINGERPRINT` (aus `talkie-server status`) setzen.
Dann ist der Server fest eingebaut und die Kids geben **nur Name und Einladungscode** ein.
Mit dem Fingerabdruck ist der Client sogar beim allerersten Verbinden gegen fremde Server geschützt.

Ein Tag `v1.0.0` pushen → die Dateien landen automatisch in einem GitHub-Release.

### Lokal bauen

Voraussetzungen: Flutter 3.44, Rust (rustup), cmake; unter Linux zusätzlich `libasound2-dev libgtk-3-dev`.

```bash
cd app
flutter_rust_bridge_codegen generate     # nur nach Änderungen an app/rust/src/api
tool/build_macos.sh --dart-define=TALKIE_SERVER=talkie.example.org   # macOS
flutter build windows --release --dart-define=TALKIE_SERVER=...       # auf Windows
flutter build linux --release --dart-define=TALKIE_SERVER=...         # auf Linux
flutter build apk --release --dart-define=TALKIE_SERVER=...           # Android (braucht Android-SDK mit NDK)
tool/build_ios_sim.sh                                                 # iOS-Simulator
```

> **Android/iOS:** Dieselbe App läuft auch auf Handy und Tablet (Android ab 8.0, iOS ab 15).
> Rust-Ziele vorher installieren: `rustup target add aarch64-linux-android armv7-linux-androideabi
> x86_64-linux-android aarch64-apple-ios aarch64-apple-ios-sim`. iOS baut und signiert man in Xcode
> (nicht in der CI): einmal `cd app && flutter build ios --config-only --release`, dann
> `app/ios/Runner.xcworkspace` öffnen, Team unter *Signing* prüfen, *Product → Archive* und über den
> Organizer zu App Store Connect/TestFlight hochladen.

> **macOS mit Xcode 27:** Apple erlaubt damit nur noch macOS 12+ als Ziel, und das neue `lipo` verträgt
> Flutters Universal-Prüfung nicht. `tool/build_macos.sh` baut dann nur für den eigenen Mac.
> Die Version für ältere Macs (Big Sur, Intel) kommt aus der GitHub-Action, die Xcode 16 benutzt.

---

## 3. An die Kids verteilen

1. Einladungscode erzeugen: `docker exec talkie talkie-server invite`
2. Datei für das jeweilige System schicken und den Code dazu
3. Beim ersten Start: Name und Code eingeben → fertig. Ab dann startet Talkie direkt verbunden.

**Erster Start, weil die App nicht bei Apple/Microsoft registriert ist:**

- **macOS:** Talkie in „Programme“ ziehen, dann **Rechtsklick → Öffnen → Öffnen**. Nur beim ersten Mal nötig.
  Beim ersten Verbinden fragt macOS nach dem Mikrofon → „Erlauben“.
- **Windows:** Bei „Der Computer wurde durch Windows geschützt“ → **Weitere Informationen → Trotzdem ausführen**.
- **Linux:** Archiv entpacken, im Ordner `Talkie` einmal `./install.sh` ausführen → Talkie steht im
  Startmenü und hat sein Icon im Dock. Starten geht auch direkt mit `./talkie`.
- **Android:** APK aufs Handy schicken und öffnen, Installation aus dieser Quelle erlauben.
  Mikrofon und Benachrichtigungen erlauben: Die Benachrichtigung „Talkie ist verbunden“ hält Talkie
  im Hintergrund am Leben, während gezockt wird.
- **Handy allgemein:** Am besten mit Kopfhörern. Über den Lautsprecher hören die anderen sich sonst als Echo.

Bedienung: **Raum anklicken** = Raum wechseln. Mikrofon-Knopf = stumm (auch **Strg+M** bzw. **⌘M**),
Kopfhörer-Knopf = niemanden hören. In den Einstellungen lässt sich die Empfindlichkeit einstellen:
Beim normalen Sprechen soll der grüne Balken über den weißen Strich kommen.

---

## Entwicklung & Tests

```bash
cd server && go test -race ./...                       # Server inkl. echter QUIC-Verbindungen
cd app/rust && cargo test --lib                        # Kern: Opus, Jitter-Puffer, Protokoll, Schlüssel
cd app/rust && cargo run --example e2e -- 127.0.0.1:7777 CODE   # Ende-zu-Ende gegen laufenden Server
cd app && flutter test                                 # Oberfläche
```

`CODE` muss für mindestens 2 Personen gelten: `talkie-server invite 3 1`.

### Architektur kurz

- **Transport:** ein QUIC-Port. Steuerung und Chat laufen als JSON über einen Stream, Sprache als QUIC-Datagramme
  (unzuverlässig, ohne Verzögerung). Details in [docs/PROTOCOL.md](docs/PROTOCOL.md).
- **Anmeldung:** Jedes Gerät hat einen Ed25519-Schlüssel. Bei der Registrierung wird ein Einladungscode verbraucht.
  Danach unterschreibt der Client eine Zufallszahl vom Server, gebunden an dessen Zertifikat.
  Nach 10 Fehlversuchen ist eine IP für 10 Minuten gesperrt.
- **Server:** leitet Sprache nur an Leute im selben Raum weiter, nicht an „taube“ Nutzer und nicht von „stummen“.
  Chat-Verlauf: die letzten 200 Nachrichten pro Raum.
- **Client-Kern (Rust):** cpal (Audio), Opus, nnnoiseless (Rauschfilter + Spracherkennung), Jitter-Puffer mit
  Verlustverschleierung, quinn (QUIC). Die Flutter-Oberfläche spricht über flutter_rust_bridge mit dem Kern.

## Lizenz

Talkie steht unter der [GNU General Public License v3.0](LICENSE). Du darfst es nutzen, ändern und weitergeben. Weitergegebene Versionen müssen ebenfalls unter der GPL-3.0 stehen und ihren Quellcode offenlegen.
