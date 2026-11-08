import 'package:flutter/material.dart';

import '../theme.dart';

/// Pegelanzeige 0..1 mit Markierung der Sprachaktivierungs-Schwelle.
class LevelMeter extends StatelessWidget {
  const LevelMeter({super.key, required this.level, required this.threshold, this.height = 8});

  final double level;
  final double threshold;
  final double height;

  @override
  Widget build(BuildContext context) {
    final bg = Theme.of(context).colorScheme.surfaceContainerHighest;
    final l = level.clamp(0.0, 1.0);
    final t = threshold.clamp(0.0, 1.0);
    return LayoutBuilder(
      builder: (context, box) {
        final w = box.maxWidth;
        return SizedBox(
          height: height,
          child: Stack(
            children: [
              Container(decoration: BoxDecoration(color: bg, borderRadius: BorderRadius.circular(height))),
              AnimatedContainer(
                duration: const Duration(milliseconds: 80),
                width: w * l,
                decoration: BoxDecoration(
                  color: l >= t ? kTalking : Colors.grey,
                  borderRadius: BorderRadius.circular(height),
                ),
              ),
              Positioned(
                left: (w * t - 1).clamp(0.0, w - 2),
                top: 0,
                bottom: 0,
                child: Container(width: 2, color: Colors.white70),
              ),
            ],
          ),
        );
      },
    );
  }
}
