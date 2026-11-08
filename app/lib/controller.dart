import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart' show AppLifecycleListener;

import 'platform.dart';
import 'version.dart';
import 'src/rust/api/talkie.dart';

/// Beim Bauen festlegbar: flutter build ... --dart-define=TALKIE_SERVER=talkie.example.org
const String kDefaultServer = String.fromEnvironment('TALKIE_SERVER');

/// Optional: Zertifikats-Fingerabdruck fest einbauen (sonst Trust on First Use).
const String kServerFingerprint = String.fromEnvironment('TALKIE_FINGERPRINT');

/// Fehler, bei denen man zurück zur Einrichtung muss.
const _setupErrors = {'need_invite', 'auth_failed', 'invite_invalid', 'name_taken', 'name_invalid', 'bad_server'};

class TalkieController extends ChangeNotifier {
  TalkieController(this.config) {
    showSetup = !config.registered || config.server.isEmpty;
  }

  ConfigDto config;
  StateDto? state;
  bool showSetup = true;
  String? setupError;
  bool setupBusy = false;
  double level = 0;
  final Map<int, List<ChatDto>> chats = {};
  final _notices = StreamController<String>.broadcast();
  /// Ungelesene Nachrichten pro Raum (nur andere Räume).
  final Map<int, int> unread = {};

  /// Handy: Chat statt Räumen zeigen.
  bool phoneChat = false;
  /// Handy: neue Nachrichten im eigenen Raum, während die Räume angezeigt werden.
  int unreadHere = 0;

  void showPhoneChat(bool chat) {
    phoneChat = chat;
    if (chat) unreadHere = 0;
    notifyListeners();
  }
  StreamSubscription<UiEvent>? _sub;

  /// Kurze Hinweise für eine Snackbar.
  Stream<String> get notices => _notices.stream;

  /// Getrennt vom ChangeNotifier, damit der Pegel nicht alles neu zeichnet.
  final ValueNotifier<double> levelNotifier = ValueNotifier(0);

  AppLifecycleListener? _life;

  void start() {
    _sub = talkieEvents().listen(_onEvent);
    // Zurück aus den Einstellungen: Mikrofon-Erlaubnis neu prüfen
    _life = AppLifecycleListener(onResume: () => checkMic());
    // iPhone: nach Anruf/Siri den Ton neu öffnen
    TalkiePlatform.onNative((m) {
      if (m == 'audioResume' && _active) talkieRestartAudio();
    });
    if (!showSetup) {
      talkieConnect(server: config.server, name: config.name, invite: '', fingerprint: kServerFingerprint);
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    _life?.dispose();
    _notices.close();
    levelNotifier.dispose();
    super.dispose();
  }

  ConnStatus get status => state?.status ?? ConnStatus.idle;
  int get me => state?.me ?? 0;
  int get currentRoom => state?.room ?? 0;
  bool get muted => state?.muted ?? false;
  bool get deaf => state?.deaf ?? false;
  /// Darf andere rauswerfen/sperren (verknüpftes Webkonto mit Recht).
  bool get moderator => state?.moderator ?? false;
  String? _dismissedUpdate;

  /// Neuere App-Version, auf die hingewiesen werden soll (null = keine).
  /// Auf dem iPhone kommen Updates über TestFlight/App Store von selbst.
  String? get updateVersion {
    final v = state?.latestVersion ?? '';
    if (v.isEmpty || TalkiePlatform.isIOS || v == _dismissedUpdate) return null;
    if (!RegExp(r'^\d+(\.\d+){1,3}$').hasMatch(v)) return null;
    return isNewerVersion(v, kAppVersion) ? v : null;
  }

  /// Nur https-Links öffnen.
  String get updateUrl {
    final u = state?.latestUrl ?? '';
    return u.startsWith('https://') ? u : '';
  }

  void dismissUpdate() {
    _dismissedUpdate = state?.latestVersion;
    notifyListeners();
  }

  /// Darf den Chat leeren (verknüpftes Admin-Konto).
  bool get admin => state?.admin ?? false;

  /// Nachrichten des aktuellen Raums; ältere als die Aufbewahrungsfrist des Servers fallen weg
  /// (die App kann tagelang offen sein).
  List<ChatDto> get currentChat {
    final all = chats[currentRoom] ?? const <ChatDto>[];
    final hours = state?.chatHours ?? 0;
    if (hours <= 0) return all;
    final cutoff = DateTime.now().millisecondsSinceEpoch ~/ 1000 - hours * 3600;
    return all.where((m) => m.ts >= cutoff).toList();
  }

  UserDto? get meUser {
    final s = state;
    if (s == null) return null;
    for (final u in s.users) {
      if (u.id == s.me) return u;
    }
    return null;
  }

  List<UserDto> usersIn(int room) {
    final s = state;
    if (s == null) return const [];
    final list = s.users.where((u) => u.room == room).toList()
      ..sort((a, b) => a.name.toLowerCase().compareTo(b.name.toLowerCase()));
    return list;
  }

  void _onEvent(UiEvent e) {
    switch (e.kind) {
      case EventKind.state:
        final s = e.state!;
        final prev = state;
        state = s;
        if (s.status == ConnStatus.connected) {
          if (showSetup) {
            showSetup = false;
            config = talkieConfig();
          }
          setupBusy = false;
          setupError = null;
        } else if (s.status == ConnStatus.failed) {
          if (_setupErrors.contains(s.errorCode)) {
            showSetup = true;
            setupError = s.message;
            config = talkieConfig();
          } else if (showSetup && setupBusy) {
            // z. B. Server nicht erreichbar: in der Einrichtung als Fehler zeigen
            setupError = s.message;
          }
          setupBusy = false;
        }
        if (prev?.room != s.room) {
          unread.remove(s.room);
          unreadHere = 0;
        }
        _updateBackground(s.status);
        notifyListeners();
      case EventKind.chat:
        final m = e.chat!;
        (chats[m.room] ??= []).add(m);
        if (m.room != currentRoom) {
          unread[m.room] = (unread[m.room] ?? 0) + 1;
        } else if (!phoneChat && m.from != me) {
          unreadHere++;
        }
        notifyListeners();
      case EventKind.history:
        if (e.historyRoom == 0) {
          // Admin hat den Chat in allen Räumen geleert
          chats.clear();
          unread.clear();
          unreadHere = 0;
        } else {
          chats[e.historyRoom] = List.of(e.history);
          if (e.history.isEmpty) unread.remove(e.historyRoom);
        }
        notifyListeners();
      case EventKind.level:
        level = e.level;
        levelNotifier.value = e.level;
      case EventKind.notice:
        if (e.notice.isNotEmpty) _notices.add(e.notice);
    }
  }

  /// Handy: Mikrofon ist nicht erlaubt → Hinweis mit Knopf zu den Einstellungen.
  bool micDenied = false;

  /// Prüft die Mikrofon-Erlaubnis (ask = ggf. nachfragen). Kommt sie neu dazu,
  /// wird das Audio neu geöffnet, damit das Mikrofon wirklich läuft.
  Future<void> checkMic({bool ask = false}) async {
    if (!TalkiePlatform.isMobile) return;
    final before = await TalkiePlatform.micStatus();
    var st = before;
    if (st != 'granted' && ask) {
      st = await TalkiePlatform.requestMic() ? 'granted' : 'denied';
    }
    final denied = st != 'granted';
    if (!denied && (before != 'granted' || micDenied)) {
      await talkieRestartAudio(); // Erlaubnis kam gerade dazu
    }
    if (denied != micDenied) {
      micDenied = denied;
      notifyListeners();
    }
  }

  bool _active = false;

  /// Handy: solange eine Verbindung besteht oder aufgebaut wird, im Hintergrund weiterlaufen.
  void _updateBackground(ConnStatus st) {
    final on = st == ConnStatus.connected || st == ConnStatus.connecting || st == ConnStatus.reconnecting;
    if (on == _active) return;
    _active = on;
    TalkiePlatform.setActive(on);
  }

  // --- Aktionen ---

  /// Laufenden Verbindungsversuch abbrechen (Einrichtung oder Banner).
  void cancelConnect() {
    talkieDisconnect();
    setupBusy = false;
    notifyListeners();
  }

  void register({required String server, required String name, required String invite}) {
    setupBusy = true;
    setupError = null;
    notifyListeners();
    try {
      talkieConnect(server: server, name: name, invite: invite, fingerprint: kServerFingerprint);
    } catch (e) {
      setupBusy = false;
      setupError = 'Interner Fehler: $e';
      notifyListeners();
    }
  }

  void reconnect() {
    talkieConnect(server: config.server, name: config.name, invite: '', fingerprint: kServerFingerprint);
  }

  void join(int room) {
    if (room != currentRoom) talkieJoin(room: room);
  }

  void sendChat(String text) => talkieChat(text: text);

  void toggleMute() => talkieSetMuted(muted: !muted);

  void toggleDeaf() => talkieSetDeaf(deaf: !deaf);

  /// Chat leeren: aktueller Raum oder alle Räume. Ergebnis kommt als Hinweis.
  void clearChat({required bool all}) => talkieClearChat(room: all ? 0 : currentRoom);

  /// Rauswerfen (ban = auch sperren). Das Ergebnis kommt als Hinweis (Snackbar).
  void moderate(UserDto u, {required bool ban}) => talkieModerate(user: u.id, ban: ban);

  /// Lautstärke einer Person nur für mich (0 … 3, 1 = normal).
  void setUserVolume(UserDto u, double v) => talkieSetUserVolume(user: u.id, volume: v);

  /// Schaltet jemanden nur für mich stumm – die anderen hören ihn weiter.
  void toggleUserMuted(UserDto u) => talkieSetUserMuted(user: u.id, muted: !u.localMuted);

  Future<void> saveSettings(SettingsDto s) async {
    await talkieSetSettings(s: s);
    config = talkieConfig();
    notifyListeners();
  }

  Future<void> logout() async {
    await talkieLogout();
    config = talkieConfig();
    chats.clear();
    state = null;
    showSetup = true;
    setupError = null;
    notifyListeners();
  }
}
