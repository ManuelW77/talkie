import 'dart:async';

import 'package:flutter/foundation.dart';

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
  StreamSubscription<UiEvent>? _sub;

  /// Kurze Hinweise für eine Snackbar.
  Stream<String> get notices => _notices.stream;

  /// Getrennt vom ChangeNotifier, damit der Pegel nicht alles neu zeichnet.
  final ValueNotifier<double> levelNotifier = ValueNotifier(0);

  void start() {
    _sub = talkieEvents().listen(_onEvent);
    if (!showSetup) {
      talkieConnect(server: config.server, name: config.name, invite: '', fingerprint: kServerFingerprint);
    }
  }

  @override
  void dispose() {
    _sub?.cancel();
    _notices.close();
    levelNotifier.dispose();
    super.dispose();
  }

  ConnStatus get status => state?.status ?? ConnStatus.idle;
  int get me => state?.me ?? 0;
  int get currentRoom => state?.room ?? 0;
  bool get muted => state?.muted ?? false;
  bool get deaf => state?.deaf ?? false;
  List<ChatDto> get currentChat => chats[currentRoom] ?? const [];

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
          setupBusy = false;
          if (_setupErrors.contains(s.errorCode)) {
            showSetup = true;
            setupError = s.message;
            config = talkieConfig();
          }
        }
        if (prev?.room != s.room) unread.remove(s.room);
        notifyListeners();
      case EventKind.chat:
        final m = e.chat!;
        (chats[m.room] ??= []).add(m);
        if (m.room != currentRoom) unread[m.room] = (unread[m.room] ?? 0) + 1;
        notifyListeners();
      case EventKind.history:
        chats[e.historyRoom] = List.of(e.history);
        notifyListeners();
      case EventKind.level:
        level = e.level;
        levelNotifier.value = e.level;
      case EventKind.notice:
        if (e.notice.isNotEmpty) _notices.add(e.notice);
    }
  }

  // --- Aktionen ---

  /// Verbindungsaufbau während der Einrichtung abbrechen.
  void cancelSetup() {
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
