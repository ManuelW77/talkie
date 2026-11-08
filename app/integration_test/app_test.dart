// Echter Ablauf auf Simulator/Emulator: App mit Rust-Kern starten, mit Einladung
// einrichten, verbinden, Chat senden. Braucht einen laufenden Server.
//
//   flutter test integration_test -d <gerät> \
//     --dart-define=TEST_SERVER=127.0.0.1:7777 --dart-define=TEST_INVITE=ABCD-EFGH
//
// Android-Emulator: Server ist 10.0.2.2:7777. Mikrofon vorher erlauben
// (iOS: xcrun simctl privacy booted grant microphone de.talkie.talkie,
//  Android: adb shell pm grant de.talkie.talkie android.permission.RECORD_AUDIO),
// sonst wartet die App auf die Systemabfrage.
import 'package:flutter/material.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:integration_test/integration_test.dart';
import 'package:talkie/main.dart' as app;

const _server = String.fromEnvironment('TEST_SERVER');
const _invite = String.fromEnvironment('TEST_INVITE');
const _name = String.fromEnvironment('TEST_NAME', defaultValue: 'Handy');
const _hold = int.fromEnvironment('TEST_HOLD', defaultValue: 2);

Future<bool> _waitFor(WidgetTester t, Finder f, {int seconds = 20}) async {
  for (var i = 0; i < seconds * 4; i++) {
    await Future<void>.delayed(const Duration(milliseconds: 250)); // echte Zeit, das Netz läuft wirklich
    await t.pump();
    if (f.evaluate().isNotEmpty) return true;
  }
  return false;
}

void main() {
  IntegrationTestWidgetsFlutterBinding.ensureInitialized();

  testWidgets('Einrichten, verbinden, Chat', (t) async {
    await app.main();
    await Future<void>.delayed(const Duration(seconds: 1));
    await t.pump();

    final setup = find.text("Los geht's");
    if (setup.evaluate().isNotEmpty) {
      expect(_invite, isNotEmpty, reason: 'TEST_INVITE fehlt');
      await t.enterText(find.widgetWithText(TextFormField, 'Dein Name'), _name);
      await t.enterText(find.widgetWithText(TextFormField, 'Einladungscode'), _invite);
      final server = find.widgetWithText(TextFormField, 'Server');
      if (server.evaluate().isNotEmpty) await t.enterText(server, _server);
      await t.tap(setup);
    }

    expect(await _waitFor(t, find.text('Nachricht schreiben …')), isTrue, reason: 'Hauptansicht kam nicht');
    expect(await _waitFor(t, find.text('Lobby')), isTrue, reason: 'nicht verbunden');
    expect(find.textContaining('Verbinde'), findsNothing);

    final msg = 'Hallo vom Handy ${DateTime.now().millisecondsSinceEpoch % 100000}';
    await t.enterText(find.byType(TextField).last, msg);
    await t.testTextInput.receiveAction(TextInputAction.send);
    expect(await _waitFor(t, find.text(msg), seconds: 10), isTrue, reason: 'Chat kam nicht zurück');

    // Kurz verbunden bleiben (Zeit für Prüfungen von außen, z. B. Android-Dienst)
    await Future<void>.delayed(const Duration(seconds: _hold));
  });
}
