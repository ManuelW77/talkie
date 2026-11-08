#!/bin/bash
# Baut die iOS-App für den Simulator (zum Testen ohne iPhone).
#   tool/build_ios_sim.sh
# Mit Xcode 27+ nur arm64 (lipo verträgt Flutters Architektur-Prüfung sonst nicht).
# Für echte iPhones braucht es eine Apple-Signatur; die unsignierte Prüf-Build macht die CI.
set -euo pipefail
cd "$(dirname "$0")/.."
CLEAN_PATH="$(echo "$PATH" | tr ':' '\n' | grep -v '\.swiftly' | paste -sd: -)"
export PATH="/opt/homebrew/opt/rustup/bin:$HOME/.cargo/bin:$CLEAN_PATH"
export FLUTTER_XCODE_ARCHS=arm64 FLUTTER_XCODE_ONLY_ACTIVE_ARCH=YES
flutter build ios --simulator --debug "$@"
echo
echo "Fertig: build/ios/iphonesimulator/Talkie.app"
echo "Installieren: xcrun simctl install booted build/ios/iphonesimulator/Talkie.app"
