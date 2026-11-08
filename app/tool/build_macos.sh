#!/bin/bash
# Baut die macOS-App.
#   tool/build_macos.sh [--dart-define=TALKIE_SERVER=host:port] [--dart-define=TALKIE_FINGERPRINT=...]
#
# Mit Xcode bis 26: Universal Binary (Apple Silicon + Intel), läuft ab macOS 10.15.
# Mit Xcode 27+:    Apple kann nur noch für macOS 12+ bauen, und das neue lipo verträgt
#                   Flutters Architektur-Prüfung nicht → nur Build für diesen Mac (zum Testen).
#                   Für ältere Macs (z. B. Big Sur, Intel) baut die GitHub-Action.
set -euo pipefail
cd "$(dirname "$0")/.."
# Rust/Cargo nach vorn; swiftly-Shims (falls kaputt) aus dem PATH entfernen
CLEAN_PATH="$(echo "$PATH" | tr ':' '\n' | grep -v '\.swiftly' | paste -sd: -)"
export PATH="/opt/homebrew/opt/rustup/bin:$HOME/.cargo/bin:$CLEAN_PATH"

XCODE_MAJOR="$(xcodebuild -version | awk '/^Xcode/{split($2,v,"."); print v[1]}')"
if [ "${XCODE_MAJOR:-0}" -ge 27 ]; then
  echo "Hinweis: Xcode $XCODE_MAJOR → Build nur für $(uname -m) und macOS 12+."
  export FLUTTER_XCODE_MACOSX_DEPLOYMENT_TARGET=12.0
  export FLUTTER_XCODE_ARCHS="$(uname -m)"
  export FLUTTER_XCODE_ONLY_ACTIVE_ARCH=YES
fi
flutter build macos --release "$@"
APP="build/macos/Build/Products/Release/Talkie.app"
echo
echo "Fertig: $APP"
lipo -info "$APP/Contents/MacOS/Talkie"
