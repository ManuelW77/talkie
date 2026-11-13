#!/bin/bash
# Baut das Talkie-DMG: Laufwerk-Icon, Hintergrundbild, feste Anordnung (App links, „Programme“ rechts).
# Aufruf: tool/make_dmg.sh <Talkie.app> <Ausgabe.dmg>
# Signieren/Notarisieren passiert danach im aufrufenden Skript.
# Das Layout (.DS_Store) setzt der Finder per AppleScript. Schlägt das fehl (z. B. ohne Oberfläche),
# gibt es nur eine Warnung – ein DMG ohne Layout ist besser als kein DMG.
set -euo pipefail

[ $# -eq 2 ] || { echo "Aufruf: $0 <Talkie.app> <Ausgabe.dmg>" >&2; exit 2; }
APP="$1"
OUT="$2"
[ -d "$APP" ] || { echo "Fehler: $APP nicht gefunden" >&2; exit 1; }

HERE="$(cd "$(dirname "$0")/.." && pwd)"
ICONSET_SRC="$HERE/macos/Runner/Assets.xcassets/AppIcon.appiconset"
BG1="$HERE/assets/dmg/background.png"
BG2="$HERE/assets/dmg/background@2x.png"
VOL=Talkie
MNT="/Volumes/$VOL"
APPNAME="$(basename "$APP")"

WORK="$(mktemp -d)"
DEV=""
cleanup() {
  if [ -n "$DEV" ]; then hdiutil detach "$DEV" -force >/dev/null 2>&1 || true; fi
  rm -rf "$WORK"
}
trap cleanup EXIT

# Ein altes, noch eingehängtes Laufwerk gleichen Namens aushängen
if [ -d "$MNT" ]; then hdiutil detach "$MNT" -force >/dev/null 2>&1 || true; fi

# 1. Laufwerk-Icon aus dem App-Icon
ICONSET="$WORK/vol.iconset"
mkdir "$ICONSET"
cp "$ICONSET_SRC/app_icon_16.png"   "$ICONSET/icon_16x16.png"
cp "$ICONSET_SRC/app_icon_32.png"   "$ICONSET/icon_16x16@2x.png"
cp "$ICONSET_SRC/app_icon_32.png"   "$ICONSET/icon_32x32.png"
cp "$ICONSET_SRC/app_icon_64.png"   "$ICONSET/icon_32x32@2x.png"
cp "$ICONSET_SRC/app_icon_128.png"  "$ICONSET/icon_128x128.png"
cp "$ICONSET_SRC/app_icon_256.png"  "$ICONSET/icon_128x128@2x.png"
cp "$ICONSET_SRC/app_icon_256.png"  "$ICONSET/icon_256x256.png"
cp "$ICONSET_SRC/app_icon_512.png"  "$ICONSET/icon_256x256@2x.png"
cp "$ICONSET_SRC/app_icon_512.png"  "$ICONSET/icon_512x512.png"
cp "$ICONSET_SRC/app_icon_1024.png" "$ICONSET/icon_512x512@2x.png"
iconutil -c icns "$ICONSET" -o "$WORK/VolumeIcon.icns"

# Hintergrund: ein TIFF mit 1x und 2x, damit Retina-Bildschirme scharf sind
tiffutil -cathidpicheck "$BG1" "$BG2" -out "$WORK/background.tiff" >/dev/null 2>&1

# 2. Beschreibbares DMG befüllen
SIZE_MB=$(( $(du -sm "$APP" | cut -f1) + 30 ))
RW="$WORK/rw.dmg"
hdiutil create -volname "$VOL" -size "${SIZE_MB}m" -fs HFS+ -type UDIF -ov "$RW" >/dev/null
DEV="$(hdiutil attach "$RW" -nobrowse -noautoopen -mountpoint "$MNT" | awk '/^\/dev\// {print $1; exit}')"

cp -R "$APP" "$MNT/"
ln -s /Applications "$MNT/Programme"
mkdir "$MNT/.background"
cp "$WORK/background.tiff" "$MNT/.background/background.tiff"
SETFILE="$(command -v SetFile || xcrun -f SetFile)"
"$SETFILE" -a V "$MNT/.background" 2>/dev/null || true

# 3. Fensterlayout über den Finder (nur Warnung bei Fehlschlag)
if ! osascript >/dev/null 2>&1 <<OSA
tell application "Finder"
  tell disk "$VOL"
    open
    set current view of container window to icon view
    set toolbar visible of container window to false
    set statusbar visible of container window to false
    set the bounds of container window to {200, 120, 800, 548}
    set opts to the icon view options of container window
    set arrangement of opts to not arranged
    set icon size of opts to 128
    set text size of opts to 13
    set background picture of opts to file ".background:background.tiff"
    set position of item "$APPNAME" of container window to {150, 190}
    set position of item "Programme" of container window to {450, 190}
    close
    open
    update without registering applications
    delay 2
    close
  end tell
end tell
OSA
then
  echo "Warnung: Fensterlayout konnte nicht gesetzt werden, DMG wird ohne Layout gebaut." >&2
fi

# Laufwerk-Icon erst NACH dem Finder legen: er löscht .VolumeIcon.icns sonst wieder
cp "$WORK/VolumeIcon.icns" "$MNT/.VolumeIcon.icns"
"$SETFILE" -a V "$MNT/.VolumeIcon.icns" 2>/dev/null || true
"$SETFILE" -a C "$MNT"

# Warten, bis der Finder die .DS_Store geschrieben hat (höchstens ~10 s)
for _ in 1 2 3 4 5 6 7 8 9 10; do [ -f "$MNT/.DS_Store" ] && break; sleep 1; done
[ -f "$MNT/.DS_Store" ] || echo "Warnung: keine .DS_Store, kein Layout im DMG." >&2
sync

# 4. Aushängen und komprimiert ablegen
hdiutil detach "$DEV" >/dev/null 2>&1 || { sleep 3; hdiutil detach "$DEV" -force >/dev/null; }
DEV=""
rm -f "$OUT"
hdiutil convert "$RW" -format UDZO -imagekey zlib-level=9 -o "$OUT" >/dev/null
echo "DMG erstellt: $OUT"
