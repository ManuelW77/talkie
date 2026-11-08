import 'dart:io';
import 'dart:ui' as ui;

import 'package:flutter/material.dart';
import 'package:flutter/rendering.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:talkie/controller.dart';
import 'package:talkie/screens/main_screen.dart';
import 'package:talkie/screens/setup_screen.dart';
import 'package:talkie/src/rust/api/talkie.dart';
import 'package:talkie/theme.dart';

// Erzeugt die Bilder für den App Store aus den echten Bildschirmen mit Beispieldaten.
// Läuft nur, wenn TALKIE_STORE_SHOTS auf einen Ausgabeordner zeigt:
//   TALKIE_STORE_SHOTS=../dist/appstore flutter test test/store_screenshots_test.dart

final _outDir = Platform.environment['TALKIE_STORE_SHOTS'] ?? '';

const _settings = SettingsDto(
  vadThresholdDb: -45,
  noiseSuppression: true,
  inputDevice: '',
  outputDevice: '',
  inputGain: 1,
  outputVolume: 1,
);

/// Zielgröße in Pixeln, Pixeldichte und Größe des gezeigten Geräte-Bildschirms.
class _Device {
  const _Device(this.folder, this.pixels, this.ratio, this.screen, this.radius);
  final String folder;
  final Size pixels;
  final double ratio;
  final Size screen;
  final double radius;
}

const _iphone = _Device('iphone-6.9', Size(1320, 2868), 3, Size(390, 844), 46);
const _ipad = _Device('ipad-13', Size(2064, 2752), 2, Size(834, 1112), 28);

Future<void> _loadFonts() async {
  Future<void> load(String family, List<String> files) async {
    final loader = FontLoader(family);
    for (final f in files) {
      final file = File(f);
      if (file.existsSync()) loader.addFont(file.readAsBytes().then((b) => ByteData.view(b.buffer)));
    }
    await loader.load();
  }

  const arial = ['/System/Library/Fonts/Supplemental/Arial.ttf', '/System/Library/Fonts/Supplemental/Arial Bold.ttf'];
  await load('Roboto', arial);
  final flutterRoot = Platform.environment['FLUTTER_ROOT'] ?? '';
  await load('MaterialIcons', ['$flutterRoot/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf']);
}

TalkieController _setupController() => TalkieController(
      const ConfigDto(server: 'talkie.example.org', name: '', registered: false, settings: _settings),
    );

TalkieController _mainController() {
  final c = TalkieController(
    const ConfigDto(server: 'talkie.example.org', name: 'Max', registered: true, settings: _settings),
  )..state = const StateDto(
      status: ConnStatus.connected,
      message: '',
      errorCode: '',
      me: 1,
      room: 10,
      rooms: [
        RoomDto(id: 10, name: 'Lobby'),
        RoomDto(id: 11, name: 'Minecraft'),
        RoomDto(id: 12, name: 'Fortnite'),
        RoomDto(id: 13, name: 'Hausaufgaben'),
      ],
      users: [
        UserDto(id: 1, name: 'Max', room: 10, muted: false, deaf: false, talking: true, localMuted: false),
        UserDto(id: 2, name: 'Lena', room: 10, muted: false, deaf: false, talking: false, localMuted: false),
        UserDto(id: 5, name: 'Paul', room: 10, muted: true, deaf: false, talking: false, localMuted: false),
        UserDto(id: 3, name: 'Jonas', room: 11, muted: false, deaf: false, talking: true, localMuted: false),
        UserDto(id: 4, name: 'Emil', room: 11, muted: false, deaf: false, talking: false, localMuted: false),
        UserDto(id: 6, name: 'Mia', room: 12, muted: false, deaf: false, talking: false, localMuted: false),
      ],
      muted: false,
      deaf: false,
    );
  c.chats[10] = const [
    ChatDto(room: 10, from: 2, name: 'Lena', text: 'Wer ist dabei? Die Runde startet gleich!', ts: 1762500000),
    ChatDto(room: 10, from: 5, name: 'Paul', text: 'Ich! Bin noch kurz beim Essen.', ts: 1762500040),
    ChatDto(room: 10, from: 2, name: 'Lena', text: 'Lobby-Code: 4711', ts: 1762500060),
    ChatDto(room: 10, from: 1, name: 'Max', text: 'Bin in 2 Minuten da!', ts: 1762500100),
  ];
  c.unread[11] = 3;
  c.levelNotifier.value = 0.62;
  return c;
}

/// Texte ohne Schriftfamilie (z. B. der Knopf-Stil aus dem Theme) zeigt der Test sonst als Kästchen;
/// auf dem Gerät nimmt iOS dafür die Systemschrift.
ThemeData _theme() {
  final base = buildTheme(Brightness.dark);
  return base.copyWith(
    filledButtonTheme: FilledButtonThemeData(
      style: base.filledButtonTheme.style!.copyWith(
        textStyle: const WidgetStatePropertyAll(
          TextStyle(fontFamily: 'Roboto', fontSize: 16, fontWeight: FontWeight.w600),
        ),
      ),
    ),
  );
}

/// Orangener Hintergrund wie das App-Icon, Überschrift oben, darunter das Gerät.
Widget _frame(_Device d, String title, String subtitle, Widget screen) {
  final big = d == _ipad;
  return MaterialApp(
    debugShowCheckedModeBanner: false,
    theme: _theme(),
    home: DecoratedBox(
      decoration: const BoxDecoration(
        gradient: LinearGradient(
          begin: Alignment.topLeft,
          end: Alignment.bottomRight,
          colors: [Color(0xFFFFB13B), Color(0xFFF2661D)],
        ),
      ),
      child: Padding(
        padding: EdgeInsets.fromLTRB(32, big ? 70 : 64, 32, 0),
        child: Column(
          children: [
            Text(
              title,
              textAlign: TextAlign.center,
              style: TextStyle(
                fontFamily: 'Roboto',
                fontSize: big ? 56 : 38,
                height: 1.1,
                fontWeight: FontWeight.w700,
                color: Colors.white,
                decoration: TextDecoration.none,
              ),
            ),
            SizedBox(height: big ? 18 : 12),
            Text(
              subtitle,
              textAlign: TextAlign.center,
              style: TextStyle(
                fontFamily: 'Roboto',
                fontSize: big ? 28 : 19,
                height: 1.25,
                fontWeight: FontWeight.w400,
                color: const Color(0xFF1C2028),
                decoration: TextDecoration.none,
              ),
            ),
            SizedBox(height: big ? 48 : 36),
            Expanded(
              child: Align(
                alignment: Alignment.topCenter,
                child: FittedBox(
                  alignment: Alignment.topCenter,
                  child: Container(
                    padding: const EdgeInsets.all(12),
                    decoration: BoxDecoration(
                      color: const Color(0xFF1C2028),
                      borderRadius: BorderRadius.circular(d.radius + 12),
                      boxShadow: const [BoxShadow(color: Color(0x55000000), blurRadius: 40, offset: Offset(0, 16))],
                    ),
                    child: ClipRRect(
                      borderRadius: BorderRadius.circular(d.radius),
                      child: SizedBox.fromSize(size: d.screen, child: screen),
                    ),
                  ),
                ),
              ),
            ),
          ],
        ),
      ),
    ),
  );
}

Future<void> _shot(WidgetTester tester, _Device d, String name) async {
  final boundary = tester.renderObject<RenderRepaintBoundary>(find.byKey(_shotKey));
  await tester.runAsync(() async {
    final image = await boundary.toImage(pixelRatio: d.ratio);
    final png = await image.toByteData(format: ui.ImageByteFormat.png);
    final file = File('$_outDir/${d.folder}/$name.png');
    file.parent.createSync(recursive: true);
    file.writeAsBytesSync(png!.buffer.asUint8List());
  });
}

const _shotKey = ValueKey('shot');

Future<void> _pump(WidgetTester tester, _Device d, Widget app) async {
  tester.view.physicalSize = d.pixels;
  tester.view.devicePixelRatio = d.ratio;
  addTearDown(tester.view.reset);
  await tester.pumpWidget(RepaintBoundary(key: _shotKey, child: app));
  await tester.pump(const Duration(milliseconds: 300));
}

void main() {
  setUpAll(_loadFonts);
  final skip = _outDir.isEmpty;

  for (final d in [_iphone, _ipad]) {
    testWidgets('${d.folder}: Chat', (tester) async {
      await _pump(
        tester,
        d,
        _frame(d, 'Quatschen beim Zocken', 'Reden und schreiben mit deinen Freunden', MainScreen(controller: _mainController())),
      );
      await _shot(tester, d, '1_chat');
    }, skip: skip);

    testWidgets('${d.folder}: Räume', (tester) async {
      await _pump(
        tester,
        d,
        _frame(d, 'Ein Raum pro Runde', 'Du siehst sofort, wer da ist und wer gerade spricht',
            MainScreen(controller: _mainController())),
      );
      if (d == _iphone) {
        await tester.tap(find.byTooltip('Räume'));
        await tester.pumpAndSettle();
      }
      await _shot(tester, d, '2_raeume');
    }, skip: skip);

    testWidgets('${d.folder}: Einrichtung', (tester) async {
      await _pump(
        tester,
        d,
        _frame(d, 'Name, Code, fertig', 'Kein Passwort, keine E-Mail – nur für eingeladene Freunde',
            SetupScreen(controller: _setupController())),
      );
      await tester.enterText(find.byType(TextFormField).at(0), 'Max');
      await tester.enterText(find.byType(TextFormField).at(1), 'K7QM2XPA');
      await tester.pump();
      FocusManager.instance.primaryFocus?.unfocus();
      await tester.pump(const Duration(milliseconds: 300));
      await _shot(tester, d, '3_einrichtung');
    }, skip: skip);
  }
}
