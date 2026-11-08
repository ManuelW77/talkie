import 'package:flutter/material.dart';

import '../theme.dart';

/// Rundes Initialen-Bild; leuchtet grün, solange die Person spricht.
class Avatar extends StatelessWidget {
  const Avatar({super.key, required this.name, this.talking = false, this.size = 32});

  final String name;
  final bool talking;
  final double size;

  @override
  Widget build(BuildContext context) {
    final color = nameColor(name);
    final initial = name.isEmpty ? '?' : name.characters.first.toUpperCase();
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
      child: CircleAvatar(
        backgroundColor: color.withValues(alpha: 0.25),
        child: Text(
          initial,
          style: TextStyle(color: color, fontWeight: FontWeight.bold, fontSize: size * 0.42),
        ),
      ),
    );
  }
}
