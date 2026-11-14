import 'package:flutter/material.dart';

// Farben aus dem App-Icon (assets/icon/talkie.svg):
// oranger Verlauf #FFB13B → #F2661D, dunkles Gehäuse #1C2028 / #323947, türkiser Knopf #2EC4B6.

/// Markenfarbe (Orange aus dem Icon): Hauptknöpfe, aktueller Raum, Hinweise.
const kAccent = Color(0xFFFF9F43);

/// Wer spricht (türkiser Sprechknopf aus dem Icon): Ring am Profilbild, Pegel, Name.
const kTalking = Color(0xFF2EC4B6);

/// Gefahr/stumm: ins Rosa verschoben, damit es sich klar vom Orange abhebt.
const kDanger = Color(0xFFFF5D6E);

/// Gefahr als Knopffläche: dunkler, damit weiße Schrift lesbar bleibt (4,6:1).
const kDangerFill = Color(0xFFD63A52);

// Dunkle Flächen im Blaugrau des Icon-Gehäuses statt Flutters grünstichiger Standardtöne.
const _bg = Color(0xFF0F1115);
const _surface = Color(0xFF15181D);
const _container = Color(0xFF1C2028);
const _containerHigh = Color(0xFF262B35);
const _containerHighest = Color(0xFF323947);
const _outline = Color(0xFF5C6678);

ThemeData buildTheme(Brightness b) {
  final dark = b == Brightness.dark;
  final base = ColorScheme.fromSeed(seedColor: const Color(0xFFF2661D), brightness: b);
  final scheme = dark
      ? base.copyWith(
          primary: kAccent,
          onPrimary: _container,
          primaryContainer: const Color(0xFF4A2E14),
          onPrimaryContainer: const Color(0xFFFFD8B0),
          secondary: kTalking,
          onSecondary: _container,
          secondaryContainer: const Color(0xFF3D2A18),
          onSecondaryContainer: const Color(0xFFFFD8B0),
          tertiary: kTalking,
          error: kDanger,
          surface: _surface,
          onSurface: const Color(0xFFE8EAEE),
          onSurfaceVariant: const Color(0xFFA9B1BE),
          surfaceContainerLowest: _bg,
          surfaceContainerLow: const Color(0xFF181B21),
          surfaceContainer: _container,
          surfaceContainerHigh: _containerHigh,
          surfaceContainerHighest: _containerHighest,
          outline: _outline,
          outlineVariant: _containerHighest,
        )
      : base;
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: dark ? _bg : null,
    inputDecorationTheme: InputDecorationTheme(
      filled: true,
      border: OutlineInputBorder(borderRadius: BorderRadius.circular(12), borderSide: BorderSide.none),
    ),
    filledButtonTheme: FilledButtonThemeData(
      style: FilledButton.styleFrom(
        minimumSize: const Size(0, 52),
        shape: RoundedRectangleBorder(borderRadius: BorderRadius.circular(12)),
        textStyle: const TextStyle(fontSize: 16, fontWeight: FontWeight.w600),
      ),
    ),
  );
}

/// Feste, gut unterscheidbare Farbe pro Name.
Color nameColor(String name) {
  const palette = [
    Color(0xFFFF9F1C),
    Color(0xFF2EC4B6),
    Color(0xFFE71D73),
    Color(0xFF9B5DE5),
    Color(0xFF00BBF9),
    Color(0xFFF15BB5),
    Color(0xFF8AC926),
    Color(0xFFFFCA3A),
  ];
  var h = 0;
  for (final c in name.toLowerCase().codeUnits) {
    h = (h * 31 + c) & 0x7fffffff;
  }
  return palette[h % palette.length];
}
