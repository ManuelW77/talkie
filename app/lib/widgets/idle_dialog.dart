import 'dart:async';

import 'package:flutter/material.dart';

/// „Bist du noch da?“ mit Countdown. Der Server trennt, wenn niemand bestätigt.
class IdleDialog extends StatefulWidget {
  const IdleDialog({super.key, required this.secs, required this.onConfirm});

  /// Sekunden bis zur Trennung beim Öffnen.
  final int secs;

  /// Wird beim Tippen auf „Ich bin noch da“ aufgerufen; schließt den Dialog.
  final VoidCallback onConfirm;

  @override
  State<IdleDialog> createState() => _IdleDialogState();
}

class _IdleDialogState extends State<IdleDialog> {
  late int _left = widget.secs;
  Timer? _timer;

  @override
  void initState() {
    super.initState();
    _timer = Timer.periodic(const Duration(seconds: 1), (_) {
      if (_left > 0) setState(() => _left--);
    });
  }

  @override
  void dispose() {
    _timer?.cancel();
    super.dispose();
  }

  String get _time => '${_left ~/ 60}:${(_left % 60).toString().padLeft(2, '0')}';

  @override
  Widget build(BuildContext context) {
    return PopScope(
      canPop: false,
      child: AlertDialog(
        title: const Text('Bist du noch da?'),
        content: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            const Text('Du hast eine Weile nichts gemacht. Sonst wirst du gleich getrennt.'),
            const SizedBox(height: 16),
            Center(
              child: Text(_time, key: const Key('idle-countdown'), style: Theme.of(context).textTheme.displaySmall),
            ),
          ],
        ),
        actions: [
          FilledButton(
            onPressed: () {
              // Erst schließen, dann melden: sonst schließt der Listener im
              // Hauptfenster den Dialog ein zweites Mal (und damit das Fenster).
              Navigator.of(context).pop();
              widget.onConfirm();
            },
            child: const Text('Ich bin noch da'),
          ),
        ],
      ),
    );
  }
}
