#!/bin/bash
# Signiert die fertige Mac-App mit „Developer ID Application“, packt sie ins DMG,
# notarisiert das DMG bei Apple und heftet das Ticket an. Danach öffnet macOS Talkie
# ohne Warnung („nicht verifizierter Entwickler“).
#
#   tool/notarize_macos.sh <Talkie.app> <Ausgabe.dmg>
#
# Umgebung:
#   SIGN_IDENTITY     Name des Zertifikats (sonst: erstes „Developer ID Application“ im Schlüsselbund)
#   NOTARY_KEY_PATH   App-Store-Connect-API-Schlüssel (.p8)
#   NOTARY_KEY_ID     Schlüssel-ID
#   NOTARY_ISSUER     Issuer-ID (bei Team-Schlüsseln nötig)
#   NOTARIZE=0        nur signieren und DMG bauen, nicht notarisieren (zum Ausprobieren)
set -euo pipefail
APP="$1"
DMG="$2"
cd "$(dirname "$0")/.."
ENTITLEMENTS="macos/Runner/Release.entitlements"
LIPO=/usr/bin/lipo

IDENTITY="${SIGN_IDENTITY:-$(security find-identity -v -p codesigning | awk -F'"' '/Developer ID Application/{print $2; exit}')}"
if [ -z "$IDENTITY" ]; then
  echo "Kein Zertifikat „Developer ID Application“ gefunden." >&2
  exit 1
fi
echo "Signiere mit: $IDENTITY"
SIGN=(codesign --force --timestamp --options runtime --sign "$IDENTITY")
[ "$IDENTITY" = "-" ] && SIGN=(codesign --force --options runtime --sign -) # Probelauf ohne Zertifikat

# 1. Von innen nach außen: Bibliotheken und Frameworks, dann die App mit ihren Rechten
while IFS= read -r -d '' f; do
  "${SIGN[@]}" "$f"
done < <(find "$APP/Contents/Frameworks" -depth \( -name "*.dylib" -o -name "*.framework" \) -print0 2>/dev/null)
"${SIGN[@]}" --entitlements "$ENTITLEMENTS" "$APP"

codesign --verify --deep --strict --verbose=2 "$APP"
# Apple lehnt Apps mit Debug-Recht ab
if codesign -d --entitlements - "$APP" 2>/dev/null | grep -q "get-task-allow"; then
  echo "Fehler: App hat noch com.apple.security.get-task-allow" >&2
  exit 1
fi
"$LIPO" -info "$APP/Contents/MacOS/Talkie"

# 2. DMG mit Hintergrund, Laufwerk-Icon und Verknüpfung zu „Programme“
rm -f "$DMG"
"$(dirname "$0")/make_dmg.sh" "$APP" "$DMG"
"${SIGN[@]}" "$DMG"

if [ "${NOTARIZE:-1}" = "0" ] || [ "$IDENTITY" = "-" ]; then
  echo "Signiert, nicht notarisiert: $DMG"
  exit 0
fi

# 3. Notarisieren (wartet, bis Apple fertig ist; meist 1–5 Minuten)
: "${NOTARY_KEY_PATH:?NOTARY_KEY_PATH fehlt}" "${NOTARY_KEY_ID:?NOTARY_KEY_ID fehlt}"
AUTH=(--key "$NOTARY_KEY_PATH" --key-id "$NOTARY_KEY_ID")
[ -n "${NOTARY_ISSUER:-}" ] && AUTH+=(--issuer "$NOTARY_ISSUER")
OUT="$(xcrun notarytool submit "$DMG" "${AUTH[@]}" --wait --output-format json)" || true
echo "$OUT"
ID="$(echo "$OUT" | /usr/bin/python3 -c 'import json,sys; print(json.load(sys.stdin).get("id",""))' 2>/dev/null || true)"
STATUS="$(echo "$OUT" | /usr/bin/python3 -c 'import json,sys; print(json.load(sys.stdin).get("status",""))' 2>/dev/null || true)"
if [ "$STATUS" != "Accepted" ]; then
  echo "Notarisierung abgelehnt ($STATUS). Apples Begründung:" >&2
  [ -n "$ID" ] && xcrun notarytool log "$ID" "${AUTH[@]}" >&2 || true
  exit 1
fi

# 4. Ticket anheften (dann klappt die Prüfung auch ohne Internet) und gegenprüfen
xcrun stapler staple "$DMG"
spctl -a -t open --context context:primary-signature -v "$DMG"
echo "Fertig: $DMG (signiert und notarisiert)"
