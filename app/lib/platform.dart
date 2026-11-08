import 'dart:io' show Platform;

import 'package:flutter/services.dart';

/// Handy- und Tablet-Besonderheiten (Android/iOS). Auf dem Desktop tut alles nichts.
class TalkiePlatform {
  static const _ch = MethodChannel('talkie/platform');

  static bool get isMobile => Platform.isAndroid || Platform.isIOS;

  /// Ordner für Einstellungen und Geräteschlüssel. Leer = Standard des Rust-Kerns (Desktop).
  static Future<String> dataDir() async {
    if (!isMobile) return '';
    return await _ch.invokeMethod<String>('dataDir') ?? '';
  }

  /// Fragt nach dem Mikrofon (Android zusätzlich nach Benachrichtigungen).
  static Future<bool> requestMic() async {
    if (!isMobile) return true;
    try {
      return await _ch.invokeMethod<bool>('requestMic') ?? false;
    } on PlatformException {
      return false;
    }
  }

  /// Verbunden: Android startet den Hintergrund-Dienst, iOS aktiviert die Audio-Sitzung.
  static Future<void> setActive(bool on) async {
    if (!isMobile) return;
    try {
      await _ch.invokeMethod<void>('setActive', on);
    } on PlatformException {
      // Ohne Hintergrund-Betrieb geht es im Vordergrund trotzdem
    }
  }
}
