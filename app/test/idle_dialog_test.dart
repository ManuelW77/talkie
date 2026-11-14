import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:talkie/theme.dart';
import 'package:talkie/widgets/idle_dialog.dart';

Widget _wrap(Widget child) =>
    MaterialApp(debugShowCheckedModeBanner: false, theme: buildTheme(Brightness.dark), home: child);

void main() {
  testWidgets('Idle-Dialog zählt herunter und bestätigt', (tester) async {
    var confirmed = 0;
    await tester.pumpWidget(_wrap(Builder(
      builder: (context) => TextButton(
        onPressed: () => showDialog<void>(
          context: context,
          barrierDismissible: false,
          builder: (_) => IdleDialog(secs: 300, onConfirm: () => confirmed++),
        ),
        child: const Text('auf'),
      ),
    )));
    await tester.tap(find.text('auf'));
    await tester.pump();

    expect(find.text('Bist du noch da?'), findsOneWidget);
    expect(find.text('5:00'), findsOneWidget);
    await tester.pump(const Duration(seconds: 3));
    expect(find.text('4:57'), findsOneWidget);

    await tester.tap(find.text('Ich bin noch da'));
    await tester.pumpAndSettle();
    expect(confirmed, 1);
    expect(find.text('Bist du noch da?'), findsNothing);
  });

  testWidgets('Idle-Dialog bleibt bei 0:00 stehen', (tester) async {
    await tester.pumpWidget(_wrap(IdleDialog(secs: 2, onConfirm: () {})));
    await tester.pump(const Duration(seconds: 5));
    expect(find.text('0:00'), findsOneWidget);
  });
}
