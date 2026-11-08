#!/bin/sh
# Richtet Talkie im Startmenü ein (mit Icon im Dock/in der Taskleiste).
# Einmal ausführen: ./install.sh   (Ordner danach nicht mehr verschieben, sonst nochmal ausführen)
# Entfernen: ./install.sh --remove
set -eu
DIR="$(cd "$(dirname "$0")" && pwd)"
APPS="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
ICONS="${XDG_DATA_HOME:-$HOME/.local/share}/icons/hicolor/256x256/apps"
DESKTOP="$APPS/de.talkie.talkie.desktop"

if [ "${1:-}" = "--remove" ]; then
  rm -f "$DESKTOP" "$ICONS/de.talkie.talkie.png"
  echo "Talkie aus dem Startmenü entfernt."
  exit 0
fi

mkdir -p "$APPS" "$ICONS"
cp "$DIR/data/flutter_assets/assets/icon/talkie_256.png" "$ICONS/de.talkie.talkie.png"
cat > "$DESKTOP" <<DESK
[Desktop Entry]
Type=Application
Name=Talkie
Comment=Sprach- und Text-Chat mit deinen Freunden
Exec="$DIR/talkie"
Icon=de.talkie.talkie
Terminal=false
Categories=Network;Chat;AudioVideo;
StartupWMClass=de.talkie.talkie
DESK
chmod +x "$DESKTOP"
command -v update-desktop-database >/dev/null 2>&1 && update-desktop-database "$APPS" >/dev/null 2>&1 || true
command -v gtk-update-icon-cache >/dev/null 2>&1 && gtk-update-icon-cache -q "${ICONS%/256x256/apps}" >/dev/null 2>&1 || true
echo "Fertig: Talkie steht jetzt im Startmenü."
