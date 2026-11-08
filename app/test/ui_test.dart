import 'dart:io';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';
import 'package:flutter_test/flutter_test.dart';
import 'package:talkie/controller.dart';
import 'package:talkie/screens/main_screen.dart';
import 'package:talkie/screens/setup_screen.dart';
import 'package:talkie/src/rust/api/talkie.dart';
import 'package:talkie/theme.dart';

// UI-Tests ohne Rust: der Controller wird direkt mit einem Zustand befüllt.
// Mit `flutter test --update-goldens` entstehen Bilder in test/goldens/ zum Anschauen.

const _settings = SettingsDto(
  vadThresholdDb: -45,
  noiseSuppression: true,
  inputDevice: '',
  outputDevice: '',
  inputGain: 1,
  outputVolume: 1,
  sounds: true,
  ptt: false,
  pttKey: 'f8',
);

TalkieController _controller({bool registered = true}) =>
    TalkieController(ConfigDto(server: 'talkie.example.org', name: 'Max', registered: registered, settings: _settings));

Future<void> _loadFonts() async {
  Future<void> load(String family, List<String> files) async {
    final loader = FontLoader(family);
    for (final f in files) {
      final file = File(f);
      if (file.existsSync()) loader.addFont(file.readAsBytes().then((b) => ByteData.view(b.buffer)));
    }
    await loader.load();
  }

  await load('Roboto', [
    '/System/Library/Fonts/Supplemental/Arial.ttf',
    '/System/Library/Fonts/Supplemental/Arial Bold.ttf',
  ]);
  final flutterRoot = Platform.environment['FLUTTER_ROOT'] ?? '';
  await load('MaterialIcons', ['$flutterRoot/bin/cache/artifacts/material_fonts/MaterialIcons-Regular.otf']);
}

Widget _wrap(Widget child) =>
    MaterialApp(debugShowCheckedModeBanner: false, theme: buildTheme(Brightness.dark), home: child);

TalkieController _mainController() {
  final c = _controller()
    ..state = const StateDto(
      status: ConnStatus.connected,
      message: '',
      errorCode: '',
      me: 1,
      room: 10,
      rooms: [
        RoomDto(id: 10, name: 'Lobby'),
        RoomDto(id: 11, name: 'Zocken 1'),
        RoomDto(id: 12, name: 'Zocken 2'),
      ],
      users: [
        UserDto(id: 1, name: 'Max', room: 10, muted: false, deaf: false, talking: true, localMuted: false, volume: 1),
        UserDto(id: 2, name: 'Lena', room: 10, muted: true, deaf: false, talking: false, localMuted: false, volume: 1),
        UserDto(id: 3, name: 'Jonas', room: 11, muted: false, deaf: false, talking: true, localMuted: true, volume: 1),
        UserDto(id: 4, name: 'Emil', room: 11, muted: false, deaf: true, talking: false, localMuted: false, volume: 1),
      ],
      muted: false,
      deaf: false,
      moderator: false,
      admin: false,
      chatHours: 0,
        latestVersion: '',
        latestUrl: '',
    );
  c.chats[10] = const [
    ChatDto(room: 10, from: 2, name: 'Lena', text: 'Wer ist dabei? Runde startet gleich!', ts: 1762500000),
    ChatDto(room: 10, from: 2, name: 'Lena', text: 'Lobby-Code: 4711', ts: 1762500030),
    ChatDto(room: 10, from: 1, name: 'Max', text: 'Bin in 2 Minuten da 👍', ts: 1762500100),
  ];
  c.unread[11] = 2;
  c.levelNotifier.value = 0.62;
  return c;
}

void main() {
  setUpAll(_loadFonts);

  testWidgets('Einrichtung zeigt Name, Code und Fehler', (tester) async {
    tester.view.physicalSize = const Size(960, 700);
    tester.view.devicePixelRatio = 1;
    final c = _controller(registered: false)..setupError = 'Der Einladungscode stimmt nicht oder ist abgelaufen.';
    await tester.pumpWidget(_wrap(SetupScreen(controller: c)));
    expect(find.text('Dein Name'), findsOneWidget);
    expect(find.text('Einladungscode'), findsOneWidget);
    expect(find.textContaining('Einladungscode stimmt nicht'), findsOneWidget);

    // Validierung: zu kurzer Code
    await tester.enterText(find.byType(TextFormField).at(1), 'ABC');
    await tester.tap(find.text("Los geht's"));
    await tester.pump();
    expect(find.textContaining('8 Zeichen'), findsOneWidget);
    await expectLater(find.byType(SetupScreen), matchesGoldenFile('goldens/setup.png'));
  });

  testWidgets('Hauptfenster zeigt Räume, Sprecher und Chat', (tester) async {
    tester.view.physicalSize = const Size(1000, 640);
    tester.view.devicePixelRatio = 1;
    final c = _mainController();
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    await tester.pump(const Duration(milliseconds: 300));

    expect(find.text('Lobby'), findsWidgets);
    expect(find.text('Max (du)'), findsOneWidget);
    expect(find.text('Jonas'), findsOneWidget);
    expect(find.text('Lobby-Code: 4711'), findsOneWidget);
    expect(find.text('2'), findsOneWidget); // ungelesen in Zocken 1
    expect(find.byIcon(Icons.mic_off_rounded), findsOneWidget); // Lena stumm
    expect(find.byIcon(Icons.headset_off_rounded), findsOneWidget); // Emil taub
    await expectLater(find.byType(MainScreen), matchesGoldenFile('goldens/main.png'));
  });

  testWidgets('Verbindungsproblem zeigt Banner mit Knopf', (tester) async {
    tester.view.physicalSize = const Size(1000, 640);
    tester.view.devicePixelRatio = 1;
    final c = _controller()
      ..state = const StateDto(
        status: ConnStatus.failed,
        message: 'Du wurdest vom Admin rausgeworfen.',
        errorCode: 'kicked',
        me: 0,
        room: 0,
        rooms: [],
        users: [],
        muted: false,
        deaf: false,
        moderator: false,
        admin: false,
        chatHours: 0,
        latestVersion: '',
        latestUrl: '',
      );
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    expect(find.text('Du wurdest vom Admin rausgeworfen.'), findsOneWidget);
    expect(find.text('Nochmal verbinden'), findsOneWidget);
  });

  testWidgets('Kein Netzwerk: Banner mit WLAN-Symbol und Abbrechen', (tester) async {
    tester.view.physicalSize = const Size(390, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = _mainController()
      ..state = const StateDto(
        status: ConnStatus.reconnecting,
        message: 'Kein Netzwerk – prüfe WLAN oder Kabel. Talkie verbindet sich dann von selbst.',
        errorCode: 'offline',
        me: 1,
        room: 10,
        rooms: [RoomDto(id: 10, name: 'Lobby')],
        users: [],
        muted: false,
        deaf: false,
        moderator: false,
        admin: false,
        chatHours: 0,
        latestVersion: '',
        latestUrl: '',
      );
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.textContaining('Kein Netzwerk'), findsOneWidget);
    expect(find.byIcon(Icons.wifi_off_rounded), findsOneWidget);
    expect(find.text('Abbrechen'), findsOneWidget);
    expect(find.text('Nochmal verbinden'), findsNothing);
    await expectLater(find.byType(MainScreen), matchesGoldenFile('goldens/phone_offline.png'));
  });

  testWidgets('Handy: Umschalten zwischen Räumen und Chat, Tastatur einklappen', (tester) async {
    tester.view.physicalSize = const Size(390, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = _mainController()..unreadHere = 1;
    await tester.pumpWidget(
      _wrap(
        ListenableBuilder(
          listenable: c,
          builder: (_, _) => MainScreen(controller: c),
        ),
      ),
    );
    await tester.pump(const Duration(milliseconds: 300));
    // Start: Räume mit allen Leuten (wer spricht, sieht man hier)
    expect(find.text('Zocken 1'), findsOneWidget);
    expect(find.text('Jonas'), findsOneWidget);
    expect(find.text('Lobby-Code: 4711'), findsNothing);
    await expectLater(find.byType(MainScreen), matchesGoldenFile('goldens/phone_rooms.png'));

    await tester.tap(find.text('Chat'));
    await tester.pumpAndSettle();
    expect(find.text('Lobby-Code: 4711'), findsOneWidget);
    expect(c.unreadHere, 0);
    expect(find.byTooltip('Tastatur einklappen'), findsNothing);
    await tester.tap(find.byType(TextField));
    await tester.pump();
    expect(find.byTooltip('Tastatur einklappen'), findsOneWidget);
    await expectLater(find.byType(MainScreen), matchesGoldenFile('goldens/phone_chat.png'));
    await tester.tap(find.byTooltip('Tastatur einklappen'));
    await tester.pump();
    expect(find.byTooltip('Tastatur einklappen'), findsNothing);
  });

  testWidgets('Moderator sieht Menü mit Rauswerfen und Sperren', (tester) async {
    tester.view.physicalSize = const Size(1000, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = _mainController();
    final s = c.state!;
    c.state = StateDto(
      status: s.status,
      message: s.message,
      errorCode: s.errorCode,
      me: s.me,
      room: s.room,
      rooms: s.rooms,
      users: s.users,
      muted: s.muted,
      deaf: s.deaf,
      moderator: true,
      admin: true,
      chatHours: 0,
        latestVersion: '',
        latestUrl: '',
    );
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    await tester.pump(const Duration(milliseconds: 300));
    // ein Menü pro anderer Person (Lena, Jonas, Emil), keins bei mir
    expect(find.byTooltip('Mehr zu Lena'), findsOneWidget);
    expect(find.byTooltip('Mehr zu Max'), findsNothing);
    await tester.tap(find.byTooltip('Mehr zu Lena'));
    await tester.pumpAndSettle();
    expect(find.text('Rauswerfen'), findsOneWidget);
    expect(find.text('Sperren'), findsOneWidget);
    await tester.tap(find.text('Sperren'));
    await tester.pumpAndSettle();
    expect(find.text('Lena sperren?'), findsOneWidget);
    await expectLater(find.byType(Overlay).first, matchesGoldenFile('goldens/mod_confirm.png'));
    await tester.tap(find.text('Abbrechen'));
    await tester.pumpAndSettle();
    expect(find.text('Lena sperren?'), findsNothing);
  });

  testWidgets('Admin: Chat-leeren-Menü; alte Nachrichten fallen nach der Frist weg', (tester) async {
    tester.view.physicalSize = const Size(1000, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final now = DateTime.now().millisecondsSinceEpoch ~/ 1000;
    final c = _mainController();
    final s = c.state!;
    c.state = StateDto(
      status: s.status,
      message: s.message,
      errorCode: s.errorCode,
      me: s.me,
      room: s.room,
      rooms: s.rooms,
      users: s.users,
      muted: s.muted,
      deaf: s.deaf,
      moderator: false,
      admin: true,
      chatHours: 24,
      latestVersion: '',
      latestUrl: '',
    );
    c.chats[10] = [
      ChatDto(room: 10, from: 2, name: 'Lena', text: 'von vorgestern', ts: now - 48 * 3600),
      ChatDto(room: 10, from: 2, name: 'Lena', text: 'von eben', ts: now - 60),
    ];
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('von eben'), findsOneWidget);
    expect(find.text('von vorgestern'), findsNothing);

    await tester.tap(find.byTooltip('Chat leeren'));
    await tester.pumpAndSettle();
    expect(find.text('„Lobby“ leeren'), findsOneWidget);
    await tester.tap(find.text('Alle Räume leeren'));
    await tester.pumpAndSettle();
    expect(find.text('Chat in allen Räumen leeren?'), findsOneWidget);
    await tester.tap(find.text('Abbrechen'));
    await tester.pumpAndSettle();
  });

  testWidgets('Person antippen: Lautstärke und stumm, ohne Rauswerfen für normale Nutzer', (tester) async {
    tester.view.physicalSize = const Size(390, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = _mainController();
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.byTooltip('Mehr zu Max'), findsNothing); // nicht bei mir selbst
    await tester.tap(find.byTooltip('Mehr zu Lena'));
    await tester.pumpAndSettle();
    expect(find.text('Lautstärke für dich: 100 %'), findsOneWidget);
    expect(find.byType(Slider), findsOneWidget);
    expect(find.text('Für mich stumm'), findsOneWidget);
    expect(find.text('Rauswerfen'), findsNothing);
    await expectLater(find.byType(Overlay).first, matchesGoldenFile('goldens/phone_person.png'));
  });

  testWidgets('Neue Version: Hinweis mit Laden, wegklickbar', (tester) async {
    tester.view.physicalSize = const Size(1000, 640);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = _mainController();
    final s = c.state!;
    c.state = StateDto(
      status: s.status,
      message: s.message,
      errorCode: s.errorCode,
      me: s.me,
      room: s.room,
      rooms: s.rooms,
      users: s.users,
      muted: s.muted,
      deaf: s.deaf,
      moderator: false,
      admin: false,
      chatHours: 0,
      latestVersion: '99.0.0',
      latestUrl: 'https://github.com/ManuelW77/talkie/releases/latest',
    );
    await tester.pumpWidget(_wrap(ListenableBuilder(listenable: c, builder: (_, _) => MainScreen(controller: c))));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('Neue Talkie-Version 99.0.0 ist da.'), findsOneWidget);
    expect(find.text('Laden'), findsOneWidget);
    await tester.tap(find.byTooltip('Ausblenden'));
    await tester.pump();
    expect(find.textContaining('Neue Talkie-Version'), findsNothing);
  });

  testWidgets('Push-to-Talk: Halten-Knopf in der Bedienleiste (Handy)', (tester) async {
    tester.view.physicalSize = const Size(390, 760);
    tester.view.devicePixelRatio = 1;
    addTearDown(tester.view.reset);
    final c = _mainController();
    c.config = ConfigDto(
      server: c.config.server,
      name: c.config.name,
      registered: true,
      settings: const SettingsDto(
        vadThresholdDb: -45,
        noiseSuppression: true,
        inputDevice: '',
        outputDevice: '',
        inputGain: 1,
        outputVolume: 1,
        sounds: true,
        ptt: true,
        pttKey: 'f8',
      ),
    );
    await tester.pumpWidget(_wrap(MainScreen(controller: c)));
    await tester.pump(const Duration(milliseconds: 300));
    expect(find.text('Halten'), findsOneWidget);
    expect(tester.takeException(), isNull); // passt ohne Überlauf aufs Handy
    await expectLater(find.byType(MainScreen), matchesGoldenFile('goldens/phone_ptt.png'));
  });
}
