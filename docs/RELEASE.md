# Release

Ein Release entsteht aus einem Tag `v<version>` auf `main`. Die CI baut dann alle Apps,
**signiert und notarisiert die Mac-App** und legt ein GitHub-Release mit allen Downloads an.
iPhone/iPad laufen getrennt über Xcode und TestFlight.
Die iOS-Buildnummer zählt Xcode bei jedem *Archive* automatisch um 1 hoch (Build-Schritt
„Buildnummer setzen“). Sie steht in `app/pubspec.yaml` hinter dem Plus (`version: 1.0.0+2`) –
nach dem Upload diese Änderung committen. Nie von Hand zurücksetzen.

## Neue Version veröffentlichen

1. Versionsnummer an **beiden** Stellen gleich setzen (ein Test prüft das):
   - `app/pubspec.yaml` → `version: 1.0.0+1`
   - `app/lib/version.dart` → `kAppVersion = '1.0.0'`
2. `devel` nach `main` mergen und pushen.
3. Tag setzen und pushen – genau passend zur Version:

   ```bash
   git tag v1.0.0
   git push origin v1.0.0
   ```

4. Unter *Actions* zusehen. Danach steht das Release unter *Releases*. Die Server melden den Apps
   die neue Version innerhalb eines Tages („Neue Talkie-Version … ist da“).

## Secrets für die Notarisierung (einmalig)

Unter *Settings → Secrets and variables → Actions* (oder mit `gh secret set`). Ohne diese Secrets
bricht ein Release-Build ab, statt eine unsignierte Mac-App zu veröffentlichen.

| Secret | Inhalt |
|---|---|
| `MACOS_CERTIFICATE_P12` | Zertifikat „Developer ID Application“ **mit privatem Schlüssel** als .p12, Base64 |
| `MACOS_CERTIFICATE_PASSWORD` | Passwort, das beim Export der .p12 vergeben wurde |
| `NOTARY_API_KEY_P8` | App-Store-Connect-API-Schlüssel (`AuthKey_….p8`), Base64 |
| `NOTARY_API_KEY_ID` | Schlüssel-ID (10 Zeichen) |
| `NOTARY_API_ISSUER_ID` | Issuer-ID (UUID, steht über der Schlüsselliste) |

Es sind dieselben Werte wie bei Schnittwerk – wer die .p12 und .p8 noch hat, kann sie wiederverwenden.

### Zertifikat

1. Schlüsselbundverwaltung → *Meine Zertifikate* → „Developer ID Application: Manuel Weiser (LWWM5RV3D9)“
   (aufklappen: der private Schlüssel muss darunter hängen).
2. Rechtsklick → *Exportieren …* → *Persönlicher Informationsaustausch (.p12)* → Passwort vergeben.
3. Speichern und die Datei danach löschen:

   ```bash
   base64 -i ~/Desktop/DeveloperID.p12 | gh secret set MACOS_CERTIFICATE_P12 --repo ManuelW77/talkie
   gh secret set MACOS_CERTIFICATE_PASSWORD --repo ManuelW77/talkie
   rm ~/Desktop/DeveloperID.p12
   ```

### API-Schlüssel

1. [App Store Connect](https://appstoreconnect.apple.com) → *Benutzer und Zugriff* → *Integrationen* →
   *App Store Connect API* → *Team-Schlüssel* → **+**, Zugriff **Developer** (oder den vorhandenen nehmen).
2. `AuthKey_XXXXXXXXXX.p8` herunterladen (geht nur einmal), Schlüssel-ID und Issuer-ID notieren.
3. Speichern:

   ```bash
   base64 -i ~/Downloads/AuthKey_XXXXXXXXXX.p8 | gh secret set NOTARY_API_KEY_P8 --repo ManuelW77/talkie
   gh secret set NOTARY_API_KEY_ID --repo ManuelW77/talkie
   gh secret set NOTARY_API_ISSUER_ID --repo ManuelW77/talkie
   ```

   (`gh secret set` ohne Wert fragt verdeckt ab – keine Leerzeichen oder Zeilenumbrüche mitkopieren.)

## Lokal signieren und notarisieren (Ersatzweg)

```bash
cd app
tool/build_macos.sh                         # mit Xcode 27 nur arm64, für ältere Macs die CI nehmen
NOTARY_KEY_PATH=~/AuthKey_XXXX.p8 NOTARY_KEY_ID=XXXX NOTARY_ISSUER=<uuid> \
  tool/notarize_macos.sh build/macos/Build/Products/Release/Talkie.app Talkie-macOS.dmg
```

`NOTARIZE=0` signiert nur (zum Ausprobieren), `SIGN_IDENTITY=-` signiert ohne Zertifikat.

## Windows und Linux

Windows-Builds sind nicht signiert (dafür bräuchte es ein kostenpflichtiges Code-Signing-Zertifikat);
beim ersten Start „Weitere Informationen → Trotzdem ausführen“. Linux braucht keine Signatur.
