import 'package:flutter/material.dart';

const kAccent = Color(0xFF2EC4B6);
const kTalking = Color(0xFF3DDC84);
const kDanger = Color(0xFFFF5D5D);

ThemeData buildTheme(Brightness b) {
  final scheme = ColorScheme.fromSeed(
    seedColor: kAccent,
    brightness: b,
    surface: b == Brightness.dark ? const Color(0xFF15181D) : null,
  );
  return ThemeData(
    useMaterial3: true,
    colorScheme: scheme,
    scaffoldBackgroundColor: b == Brightness.dark ? const Color(0xFF0F1115) : null,
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
