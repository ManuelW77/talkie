import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../theme.dart';

/// Rundes Profilbild (oder Anfangsbuchstabe); leuchtet grün, solange die Person spricht.
class Avatar extends StatelessWidget {
  const Avatar({super.key, required this.name, this.talking = false, this.size = 32, this.image});

  final String name;
  final bool talking;
  final double size;

  /// JPEG-Bytes des Profilbilds; null = Anfangsbuchstabe.
  final Uint8List? image;

  @override
  Widget build(BuildContext context) {
    final color = nameColor(name);
    final initial = name.isEmpty ? '?' : name.characters.first.toUpperCase();
    final letter = CircleAvatar(
      backgroundColor: color.withValues(alpha: 0.25),
      child: Text(
        initial,
        style: TextStyle(color: color, fontWeight: FontWeight.bold, fontSize: size * 0.42),
      ),
    );
    final bytes = image;
    return AnimatedContainer(
      duration: const Duration(milliseconds: 120),
      width: size,
      height: size,
      padding: const EdgeInsets.all(2),
      decoration: BoxDecoration(
        shape: BoxShape.circle,
        border: Border.all(color: talking ? kTalking : Colors.transparent, width: 2.5),
        boxShadow: talking ? [BoxShadow(color: kTalking.withValues(alpha: 0.5), blurRadius: 8)] : null,
      ),
      child: bytes == null || bytes.isEmpty
          ? letter
          : ClipOval(
              child: Image.memory(
                bytes,
                fit: BoxFit.cover,
                gaplessPlayback: true,
                filterQuality: FilterQuality.medium,
                // Kaputtes Bild: einfach den Buchstaben zeigen
                errorBuilder: (_, _, _) => letter,
              ),
            ),
    );
  }
}
