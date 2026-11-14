# Talkie-Protokoll v1

Transport: **QUIC** (UDP, ein Port, Standard 7777), ALPN `talkie/1`, TLS 1.3.
Der Server nutzt ein selbst erzeugtes Zertifikat. Der Client pinnt den
SHA-256-Fingerabdruck des Zertifikats (DER) beim ersten Verbinden
(Trust on First Use) oder bekommt ihn fest beim Build mitgegeben.

## Steuerkanal

Der Client öffnet direkt nach dem Verbinden **einen** bidirektionalen Stream.
Jede Nachricht: `u32 Länge (Big Endian)` + JSON-Objekt (max. 64 KiB).
Feld `t` ist der Nachrichtentyp.

### Anmeldung

```
C → S  {"t":"hello","v":1,"pubkey":"<base64 Ed25519 public key>"}
S → C  {"t":"challenge","nonce":"<base64 32 Byte>","known":true|false}
C → S  {"t":"auth","sig":"<base64>"}                          // bekannter Nutzer
C → S  {"t":"register","sig":"<base64>","name":"Max","invite":"ABCD-EFGH"}
S → C  {"t":"welcome","you":User,"rooms":[Room],"users":[User],"mod":true,"admin":true,"chat_hours":24}
       mod/admin nur, wenn gesetzt; chat_hours: so lange bleibt der Chat erhalten (fehlt = unbegrenzt)
       optional "latest":"1.2.0","latest_url":"https://github.com/…": neueste App-Version (Hinweis in der App)
S → C  {"t":"error","code":"...","reason":"..."}  // danach wird die Verbindung geschlossen
```

Signiert wird `"talkie-auth-v1" || nonce || sha256(server-zertifikat)`.
Damit ist die Anmeldung an genau diese Verbindung und diesen Server gebunden.

Einladungen können einen vorgegebenen Namen haben (Verwaltungsseite/CLI, nur bei Einladung für
1 Person). Die App fragt ihn vor dem Registrieren über eine eigene, kurze Verbindung ab:

```
C → S  {"t":"hello",…}   S → C  {"t":"challenge",…,"known":false}
C → S  {"t":"invite_info","sig":"<base64>","invite":"ABCD-EFGH"}
S → C  {"t":"invite_info","name":"Max"}   name fehlt = kein Name hinterlegt; danach schließt der Server
```

Ungültiger Code → `error` mit `invite_invalid` (zählt als Fehlversuch fürs Rate-Limit). Der Name ist nur
ein Vorschlag: `register` schickt den (evtl. geänderten) Namen. Schickt ein Client bei `register`
einen leeren Namen und hat die Einladung einen, übernimmt der Server den Einladungsnamen.

Fehlercodes: `bad_request`, `version`, `invite_invalid`, `name_invalid`,
`name_taken`, `banned`, `auth_failed`, `rate_limited`, `server_full`.

### Danach

```
C → S  {"t":"join","room":3,"password":"…"}   password nur für gesperrte Räume
C → S  {"t":"create_room","name":"Zimmer","password":"…"}   eigenen, temporären Raum erstellen (password optional); der Ersteller ist sofort drin
C → S  {"t":"chat","text":"Hi"}
C → S  {"t":"state","muted":false,"deaf":false}
C → S  {"t":"ignore","ids":[7,9]}   ersetzt die Liste der für sich stumm geschalteten Nutzer
C → S  {"t":"kick","id":7}          rauswerfen   } nur mit verknüpftem Webkonto, das „Talkie-Nutzer
C → S  {"t":"ban","id":7}           sperren      } bearbeiten“ darf (Admins immer); Admins sind geschützt
C → S  {"t":"device_code"}           Code für ein weiteres Gerät desselben Nutzers (15 min, einmalig)
C → S  {"t":"clear_chat","room":3}  Chat leeren (room fehlt = alle Räume), nur mit verknüpftem Admin-Konto
C → S  {"t":"still_here"}            „Ich bin noch da“: setzt nur die Inaktivitäts-Uhr zurück (siehe unten)

S → C  {"t":"user_joined","user":User}
S → C  {"t":"user_update","user":User}
S → C  {"t":"user_left","id":7}
S → C  {"t":"rooms","rooms":[Room]}
S → C  {"t":"history","room":3,"messages":[Chat]}
S → C  {"t":"chat","msg":Chat}
S → C  {"t":"kicked","reason":"..."}
S → C  {"t":"info","reason":"Ben wurde rausgeworfen."}   Erfolgsmeldung
S → C  {"t":"device_code","code":"ABCD-EFGH"}
S → C  {"t":"idle_warning","secs":300}   gleich wegen Inaktivität getrennt, noch so viele Sekunden
S → C  {"t":"chat_cleared","room":3}   Chat wurde geleert (room fehlt = alle Räume)
S → C  {"t":"error","code":"forbidden","reason":"..."}  Meldung für den Nutzer
```

```
User = {"id":7,"name":"Max","room":3,"muted":false,"deaf":false}
Room = {"id":3,"name":"Lobby"}
       temporäre Räume zusätzlich {"temp":true} und bei Passwort {"locked":true}
Chat = {"room":3,"from":7,"name":"Max","text":"Hi","ts":1731000000}
```

### Temporäre Räume

Jeder angemeldete Nutzer kann mit `create_room` einen Raum erstellen, optional mit Passwort.
Diese Räume gibt es nur im Speicher des Servers (IDs ab 1 000 000 000, kollidieren nie mit
Räumen aus der Verwaltungsseite). Verlässt der letzte Nutzer den Raum (Wechsel, Trennung,
Rauswurf), wird er samt Chat gelöscht und die Raumliste neu verschickt. Der Chat solcher
Räume wird nie in der Datenbank gespeichert.

- Name: gleiche Regeln wie bei Verwaltungs-Räumen, darf noch nicht existieren (Groß/Klein egal).
- Grenzen: 1 temporärer Raum pro Ersteller gleichzeitig, höchstens 20 insgesamt, Passwort höchstens 64 Zeichen.
- Das Passwort wird nur gesalzen gehasht im Speicher gehalten und nie an Clients geschickt
  (nur `"locked":true`). Falsches oder fehlendes Passwort bei `join` → `error` mit Code
  `room_password`. Höchstens 5 Fehlversuche pro Minute und Nutzer, danach `rate_limited`.
- Nutzer mit verknüpftem Admin-Konto kommen ohne Passwort hinein.
- Fehlercodes von `create_room`: `name_invalid`, `password_invalid`, `room_exists`, `room_limit`.

### Mehrere Geräte

Ein Nutzer kann bis zu 5 Geräte haben, jedes mit eigenem Schlüssel. Ein neues Gerät registriert sich
mit einem Geräte-Code statt einer Einladung: `register` mit `invite` = Geräte-Code, `name` leer, optional
`device` (z. B. „iPhone/iPad“, nur zur Anzeige). Der Server antwortet mit dem vorhandenen Nutzer.
Verbunden ist pro Nutzer immer nur ein Gerät; eine neue Anmeldung trennt die ältere (`kicked`).

### Inaktivität

Wer länger als die eingestellte Zeit nichts tut, wird getrennt (Verwaltungsseite, Standard 60 Minuten,
0 = aus, erlaubt 10–1440), auch wenn andere im Raum sind. Als Aktion zählt jede Steuernachricht des
Clients (die vom Rate-Limit durchgelassen wird, also auch `still_here`) und echte Sprache: Opus-Pakete
mit höchstens 2 Byte Nutzlast (DTX/Komfortrauschen) zählen nicht. 5 Minuten vor Ablauf schickt der Server
einmal `idle_warning` mit den restlichen Sekunden (nach neuer Aktivität kann erneut gewarnt werden).
Bei Ablauf kommt `kicked` mit dem Grund „Du warst N Minuten nicht aktiv und wurdest getrennt.“.
Clients, die `idle_warning` nicht kennen, ignorieren es und werden ohne Vorwarnung getrennt.

## Sprache (QUIC-Datagramme)

Opus, 48 kHz, mono, 40-ms-Frames, Ziel 16 kbit/s, VoIP-Modus.
Gesendet wird nur, während jemand spricht (Sprachaktivierung + Nachlauf).

```
C → S  [0x01][seq u16 BE][opus-Daten]
S → C  [0x01][user-id u32 BE][seq u16 BE][opus-Daten]
```

Der Server leitet ein Paket an alle Nutzer im selben Raum weiter, außer an
den Absender, an Nutzer, die „taub“ (deaf) geschaltet sind, und an Nutzer,
die den Absender per `ignore` für sich stumm geschaltet haben (spart deren
Download; die anderen hören ihn weiter). Der Client schickt `ignore` nach jeder
Änderung und nach jeder Anmeldung mit der vollen Liste (höchstens 500 IDs).
Ältere Server kennen `ignore` nicht – der Client verwirft die Pakete dann selbst. Pakete von
stummgeschalteten Nutzern werden verworfen.
