#!/bin/sh
# Erzeugt alle App-Icons aus assets/icon/talkie.svg (nur auf macOS, braucht ImageMagick).
set -eu
cd "$(dirname "$0")/.."
T=$(mktemp -d); trap 'rm -rf "$T"' EXIT
PATH=/usr/bin:$PATH swift tool/render_svg.swift assets/icon/talkie.svg "$T/mac.png" 1024
# macOS: Icon mit Rand (Big-Sur-Raster)
for s in 16 32 64 128 256 512 1024; do
  magick "$T/mac.png" -resize ${s}x${s} macos/Runner/Assets.xcassets/AppIcon.appiconset/app_icon_$s.png
done
# Windows/Linux: ohne Rand, füllt die Fläche
magick "$T/mac.png" -crop 824x824+100+100 +repage -resize 256x256 "$T/full.png"
magick "$T/full.png" -define icon:auto-resize=256,128,64,48,32,24,16 windows/runner/resources/app_icon.ico
cp "$T/full.png" assets/icon/talkie_256.png
echo "Icons erzeugt."
