import 'dart:io' show Platform, Process;

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

  /// "granted", "denied" (nur iOS sicher erkennbar) oder "undetermined". Desktop: immer "granted".
  static Future<String> micStatus() async {
    if (!isMobile) return 'granted';
    try {
      return await _ch.invokeMethod<String>('micStatus') ?? 'undetermined';
    } on PlatformException {
      return 'undetermined';
    }
  }

  /// Öffnet die System-Einstellungen von Talkie (Mikrofon erlauben).
  static Future<void> openSettings() async {
    if (!isMobile) return;
    try {
      await _ch.invokeMethod<void>('openSettings');
    } on PlatformException {
      // nichts zu tun
    }
  }

  static bool get isIOS => Platform.isIOS;

  /// Meldungen vom System an die App (z. B. „audioResume“ nach einem Anruf).
  static void onNative(void Function(String method) handler) {
    if (!isMobile) return;
    _ch.setMethodCallHandler((call) async => handler(call.method));
  }

  /// Öffnet eine Internetadresse im Browser.
  static Future<void> openUrl(String url) async {
    if (!url.startsWith('https://') && !url.startsWith('http://')) return;
    try {
      if (isMobile) {
        await _ch.invokeMethod<void>('openUrl', url);
      } else if (Platform.isMacOS) {
        await Process.run('open', [url]);
      } else if (Platform.isWindows) {
        await Process.run('rundll32', ['url.dll,FileProtocolHandler', url]);
      } else {
        await Process.run('xdg-open', [url]);
      }
    } catch (_) {
      // kein Browser? Dann eben nicht.
    }
  }

  /// Android: Kommunikationsmodus für die Echo-Unterdrückung (iOS regelt das selbst).
  static Future<void> setVoiceMode(bool on) async {
    if (!Platform.isAndroid) return;
    try {
      await _ch.invokeMethod<void>('setVoiceMode', on);
    } on PlatformException {
      // dann eben ohne
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
