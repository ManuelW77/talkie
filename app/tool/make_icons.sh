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

# iOS: quadratisch ohne Transparenz, iOS rundet die Ecken selbst
sed 's/rx="185"/rx="0"/' assets/icon/talkie.svg > "$T/square.svg"
PATH=/usr/bin:$PATH swift tool/render_svg.swift "$T/square.svg" "$T/square.png" 1024
magick "$T/square.png" -crop 824x824+100+100 +repage -resize 1024x1024 -background "#F2661D" -alpha remove -alpha off "$T/ios.png"
IOS=ios/Runner/Assets.xcassets/AppIcon.appiconset
for spec in 20:1 20:2 20:3 29:1 29:2 29:3 40:1 40:2 40:3 60:2 60:3 76:1 76:2 83.5:2 1024:1; do
  pt=${spec%:*}; sc=${spec#*:}
  px=$(echo "$pt * $sc" | bc | cut -d. -f1)
  magick "$T/ios.png" -resize ${px}x${px} "$IOS/Icon-App-${pt}x${pt}@${sc}x.png"
done

# Android: altes Icon (gerundet) + adaptives Icon (Vordergrund ohne Hintergrund, Rand für Kreis-Masken)
grep -v 'fill="url(#g)"' assets/icon/talkie.svg > "$T/fg.svg"
PATH=/usr/bin:$PATH swift tool/render_svg.swift "$T/fg.svg" "$T/fg.png" 1024
magick "$T/fg.png" -crop 824x824+100+100 +repage -resize 62% -background none -gravity center -extent 824x824 "$T/fg_safe.png"
magick -size 824x824 gradient:"#FFB13B"-"#F2661D" "$T/bg.png"
RES=android/app/src/main/res
for spec in mdpi:48:108 hdpi:72:162 xhdpi:96:216 xxhdpi:144:324 xxxhdpi:192:432; do
  d=$(echo $spec | cut -d: -f1); l=$(echo $spec | cut -d: -f2); a=$(echo $spec | cut -d: -f3)
  mkdir -p "$RES/mipmap-$d"
  magick "$T/mac.png" -crop 824x824+100+100 +repage -resize ${l}x${l} "$RES/mipmap-$d/ic_launcher.png"
  magick "$T/fg_safe.png" -resize ${a}x${a} "$RES/mipmap-$d/ic_launcher_foreground.png"
  magick "$T/bg.png" -resize ${a}x${a} "$RES/mipmap-$d/ic_launcher_background.png"
done
echo "Icons erzeugt."
