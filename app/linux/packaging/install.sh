#!/bin/sh
# Installiert Talkie für den aktuellen Nutzer und richtet es im Startmenü ein.
# Im entpackten Ordner einmal ausführen:  ./install.sh
# Die App wird nach ~/.local/share/talkie-app kopiert, der entpackte Ordner kann danach weg.
# Update: neue Version entpacken und wieder ./install.sh ausführen.
# Entfernen: ./install.sh --remove
set -eu

SRC="$(cd "$(dirname "$0")" && pwd)"
DATA="${XDG_DATA_HOME:-$HOME/.local/share}"
TARGET="$DATA/talkie-app"
APPS="$DATA/applications"
HICOLOR="$DATA/icons/hicolor"
DESKTOP="$APPS/de.talkie.talkie.desktop"
ICON_REL="data/flutter_assets/assets/icon/talkie_256.png"

refresh() {
  command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS" >/dev/null 2>&1 || true
  command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q -f -t "$HICOLOR" >/dev/null 2>&1 || true
  command -v xdg-desktop-menu >/dev/null 2>&1 && xdg-desktop-menu forceupdate >/dev/null 2>&1 || true
}

# Löscht den Installationsordner nur, wenn er wirklich Talkie enthält.
remove_target() {
  if [ -d "$TARGET" ]; then
    if [ -f "$TARGET/talkie" ] && [ -d "$TARGET/data" ]; then
      rm -rf "$TARGET"
    else
      echo "Abbruch: $TARGET sieht nicht nach Talkie aus, ich lösche dort nichts." >&2
      exit 1
    fi
  fi
}

if [ "${1:-}" = "--remove" ]; then
  rm -f "$DESKTOP" "$HICOLOR/256x256/apps/de.talkie.talkie.png"
  remove_target
  refresh
  echo "Talkie wurde entfernt. Deine Einstellungen bleiben erhalten."
  exit 0
fi

if [ ! -f "$SRC/talkie" ] || [ ! -f "$SRC/$ICON_REL" ]; then
  echo "Abbruch: Bitte install.sh im entpackten Talkie-Ordner ausführen." >&2
  exit 1
fi

# App an ihren festen Platz kopieren (außer das Skript läuft schon von dort).
if [ "$SRC" != "$TARGET" ]; then
  remove_target
  mkdir -p "$TARGET"
  cp -R "$SRC/." "$TARGET/"
fi
chmod +x "$TARGET/talkie"

# Icon ins Icon-Thema legen und im Starter zusätzlich mit vollem Pfad angeben.
# Der volle Pfad klappt auch dort, wo der Icon-Cache nicht neu geladen wird (z. B. Pop!_OS/COSMIC).
mkdir -p "$APPS" "$HICOLOR/256x256/apps"
cp "$TARGET/$ICON_REL" "$HICOLOR/256x256/apps/de.talkie.talkie.png"

cat > "$DESKTOP" <<DESK
[Desktop Entry]
Type=Application
Name=Talkie
Comment=Sprach- und Text-Chat mit deinen Freunden
Exec="$TARGET/talkie"
Path=$TARGET
Icon=$TARGET/$ICON_REL
Terminal=false
Categories=Network;Chat;AudioVideo;
StartupWMClass=de.talkie.talkie
DESK
chmod +x "$DESKTOP"
refresh

echo "Fertig: Talkie ist installiert und steht im Startmenü."
if [ "$SRC" != "$TARGET" ]; then
  echo "Den entpackten Ordner $SRC kannst du jetzt löschen."
fi
