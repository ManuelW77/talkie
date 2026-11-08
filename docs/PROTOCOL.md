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
S → C  {"t":"welcome","you":User,"rooms":[Room],"users":[User],"mod":true}   mod nur, wenn man rauswerfen/sperren darf
S → C  {"t":"error","code":"...","reason":"..."}  // danach wird die Verbindung geschlossen
```

Signiert wird `"talkie-auth-v1" || nonce || sha256(server-zertifikat)`.
Damit ist die Anmeldung an genau diese Verbindung und diesen Server gebunden.

Fehlercodes: `bad_request`, `version`, `invite_invalid`, `name_invalid`,
`name_taken`, `banned`, `auth_failed`, `rate_limited`, `server_full`.

### Danach

```
C → S  {"t":"join","room":3}
C → S  {"t":"chat","text":"Hi"}
C → S  {"t":"state","muted":false,"deaf":false}
C → S  {"t":"ignore","ids":[7,9]}   ersetzt die Liste der für sich stumm geschalteten Nutzer
C → S  {"t":"kick","id":7}          rauswerfen   } nur mit verknüpftem Webkonto, das „Talkie-Nutzer
C → S  {"t":"ban","id":7}           sperren      } bearbeiten“ darf (Admins immer); Admins sind geschützt

S → C  {"t":"user_joined","user":User}
S → C  {"t":"user_update","user":User}
S → C  {"t":"user_left","id":7}
S → C  {"t":"rooms","rooms":[Room]}
S → C  {"t":"history","room":3,"messages":[Chat]}
S → C  {"t":"chat","msg":Chat}
S → C  {"t":"kicked","reason":"..."}
S → C  {"t":"info","reason":"Ben wurde rausgeworfen."}   Erfolgsmeldung
S → C  {"t":"error","code":"forbidden","reason":"..."}  Meldung für den Nutzer
```

```
User = {"id":7,"name":"Max","room":3,"muted":false,"deaf":false}
Room = {"id":3,"name":"Lobby"}
Chat = {"room":3,"from":7,"name":"Max","text":"Hi","ts":1731000000}
```

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
