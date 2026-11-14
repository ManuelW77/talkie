import 'dart:async';

import 'package:flutter/foundation.dart';
import 'package:flutter/widgets.dart' show AppLifecycleListener, AppLifecycleState;

import 'platform.dart';
import 'version.dart';
import 'src/rust/api/talkie.dart';

/// Beim Bauen festlegbar: flutter build ... --dart-define=TALKIE_SERVER=talkie.example.org
const String kDefaultServer = String.fromEnvironment('TALKIE_SERVER');

/// Optional: Zertifikats-Fingerabdruck fest einbauen (sonst Trust on First Use).
const String kServerFingerprint = String.fromEnvironment('TALKIE_FINGERPRINT');

/// Fehler, bei denen man zurück zur Einrichtung muss.
const _setupErrors = {'need_invite', 'auth_failed', 'invite_invalid', 'name_taken', 'name_invalid', 'bad_server'};

/// Geräteliste des Kontos samt offenem Geräte-Code (code leer = keiner).
class DevicesInfo {
  const DevicesInfo(this.devices, this.code, this.codeName, this.expires);
  final List<DeviceDto> devices;
  final String code;
  final String codeName;

  /// Ablauf des Codes in Unix-Sekunden.
  final int expires;
}

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

  /// Zuletzt angeforderter Geräte-Code (null = noch keiner da).
  final ValueNotifier<String?> deviceCode = ValueNotifier(null);

  /// Code für ein weiteres Gerät anfordern; landet in [deviceCode]. [name] = Name des neuen Geräts.
  void requestDeviceCode([String name = '']) {
    deviceCode.value = null;
    talkieRequestDeviceCode(name: name);
  }

  /// Eigene Geräte und offener Geräte-Code (null = noch keine Antwort, z. B. älterer Server).
  final ValueNotifier<DevicesInfo?> devices = ValueNotifier(null);

  void requestDevices() => talkieRequestDevices();
  void deleteDevice(int id) => talkieDeleteDevice(id: id);
  void renameDevice(int id, String name) =>
      talkieRenameDevice(id: id, name: name);
  void deleteDeviceCode() => talkieDeleteDeviceCode();

  // --- Profilbilder ---

  /// Bilder im Speicher: Nutzer-ID → (Version, JPEG). Leere Bytes = Server hat kein Bild geliefert.
  final Map<int, ({int version, Uint8List bytes})> _avatars = {};

  /// Gerade angefragte Bilder (ID → Version), damit nichts doppelt geladen wird.
  final Map<int, int> _avatarPending = {};
  final _avatarErrors = StreamController<String>.broadcast();

  /// Fehlermeldungen zum eigenen Profilbild (für die Einstellungen).
  Stream<String> get avatarErrors => _avatarErrors.stream;

  /// Profilbild einer Person (null = keins, dann zeigt Talkie den Anfangsbuchstaben).
  Uint8List? avatarOf(int id) {
    final b = _avatars[id]?.bytes;
    return b == null || b.isEmpty ? null : b;
  }

  /// Eigene Bild-Version (0 = kein Bild).
  int get myAvatarVersion => meUser?.avatar ?? 0;

  /// Lädt ein Bild nur, wenn es neu oder in einer neueren Version da ist; bei 0 wird es vergessen.
  void _syncAvatar(int id, int version) {
    if (version <= 0) {
      _avatars.remove(id);
      _avatarPending.remove(id);
      return;
    }
    final have = _avatars[id]?.version ?? 0;
    if (have >= version || _avatarPending[id] != null && _avatarPending[id]! >= version) return;
    _avatarPending[id] = version;
    try {
      talkieGetAvatar(id: id);
    } catch (_) {
      _avatarPending.remove(id);
    }
  }

  /// Nur für Tests: Bild direkt in den Speicher legen.
  @visibleForTesting
  void debugSetAvatar(int id, int version, Uint8List bytes) => _avatars[id] = (version: version, bytes: bytes);

  /// Eigenes Bild setzen (JPEG 128×128); leer = Bild entfernen.
  void setAvatar(Uint8List jpeg) => talkieSetAvatar(jpeg: jpeg);

  /// Alle registrierten Nutzer; null = noch keine Antwort (z. B. älterer Server).
  final ValueNotifier<List<MemberDto>?> members = ValueNotifier(null);

  /// Mitgliederliste neu holen.
  void requestMembers() => talkieRequestMembers();

  /// Sekunden bis zur Trennung wegen Inaktivität, solange der Server gewarnt hat (sonst null).
  /// Die Oberfläche zeigt dann „Bist du noch da?“.
  final ValueNotifier<int?> idleSecs = ValueNotifier(null);

  /// „Ich bin noch da“: schließt die Warnung und hält die Verbindung am Leben.
  void stillHere() {
    idleSecs.value = null;
    talkieStillHere();
  }

  /// Getrennt vom ChangeNotifier, damit der Pegel nicht alles neu zeichnet.
  final ValueNotifier<double> levelNotifier = ValueNotifier(0);

  AppLifecycleListener? _life;

  void start() {
    _sub = talkieEvents().listen(_onEvent);
    // Zurück aus den Einstellungen: Mikrofon-Erlaubnis neu prüfen
    _life = AppLifecycleListener(onResume: () => checkMic(), onStateChange: handleLifecycle);
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
    _avatarErrors.close();
    members.dispose();
    levelNotifier.dispose();
    idleSecs.dispose();
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
        if (s.status != ConnStatus.connected) idleSecs.value = null; // Verbindung weg: Frage schließen
        _updateBackground(s.status);
        if (s.status == ConnStatus.connected) {
          for (final u in s.users) {
            _syncAvatar(u.id, u.avatar);
          }
        }
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
      case EventKind.deviceCode:
        deviceCode.value = e.notice;
      case EventKind.devices:
        devices.value = DevicesInfo(
          e.devices,
          e.deviceCode,
          e.deviceCodeName,
          e.deviceCodeExpires.toInt(),
        );
      case EventKind.idleWarning:
        idleSecs.value = e.idleSecs;
      case EventKind.avatar:
        final id = e.avatarId.toInt();
        final wanted = _avatarPending.remove(id) ?? 0;
        final version = e.avatarVersion.toInt() > wanted ? e.avatarVersion.toInt() : wanted;
        if (e.avatarBytes.isEmpty) {
          _avatars[id] = (version: version, bytes: Uint8List(0));
        } else {
          _avatars[id] = (version: version, bytes: Uint8List.fromList(e.avatarBytes));
        }
        notifyListeners();
      case EventKind.members:
        final list = List.of(e.members);
        for (final m in list) {
          _syncAvatar(m.id.toInt(), m.avatar.toInt());
        }
        members.value = list;
      case EventKind.avatarRejected:
        _avatarErrors.add(e.notice);
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
    TalkiePlatform.setVoiceMode(on && config.settings.echoCancel);
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

  /// Selbst vom Server getrennt (Knopf „Trennen“), nicht durch einen Fehler.
  bool userDisconnected = false;

  /// Vom Server trennen, bis man wieder auf „Verbinden“ tippt.
  void disconnect() {
    userDisconnected = true;
    talkieDisconnect();
    notifyListeners();
  }

  void reconnect() {
    userDisconnected = false;
    talkieConnect(server: config.server, name: config.name, invite: '', fingerprint: kServerFingerprint);
  }

  void join(int room, {String? password}) {
    if (room != currentRoom) talkieJoin(room: room, password: password);
  }

  /// Eigenen, temporären Raum erstellen; man landet gleich darin.
  void createRoom(String name, {String? password}) {
    final pw = (password ?? '').isEmpty ? null : password;
    talkieCreateRoom(name: name.trim(), password: pw);
  }

  void sendChat(String text) => talkieChat(text: text);

  void toggleMute() {
    // Wer selbst schaltet, übernimmt: kein automatisches Entstummen mehr
    _autoMuted = false;
    talkieSetMuted(muted: !muted);
  }

  /// Mikro wurde automatisch stumm geschaltet, weil Talkie nicht vorne ist.
  bool _autoMuted = false;

  /// App kommt in den Hintergrund oder zurück: Mikro je nach Einstellung stumm/an.
  void handleLifecycle(AppLifecycleState s) {
    final action = backgroundMuteAction(
      enabled: config.settings.muteInBackground,
      foreground: s == AppLifecycleState.resumed,
      muted: muted,
      autoMuted: _autoMuted,
    );
    switch (action) {
      case BackgroundMuteAction.mute:
        _autoMuted = true;
        talkieSetMuted(muted: true);
      case BackgroundMuteAction.unmute:
        _autoMuted = false;
        talkieSetMuted(muted: false);
      case BackgroundMuteAction.none:
        break;
    }
  }

  void toggleDeaf() => talkieSetDeaf(deaf: !deaf);

  /// Chat leeren: aktueller Raum oder alle Räume. Ergebnis kommt als Hinweis.
  void clearChat({required bool all}) => talkieClearChat(room: all ? 0 : currentRoom);

  /// Rauswerfen (ban = auch sperren). Das Ergebnis kommt als Hinweis (Snackbar).
  void moderate(UserDto u, {required bool ban}) => talkieModerate(user: u.id, ban: ban);

  /// Push-to-Talk-Knopf in der App gedrückt/losgelassen.
  void setPttPressed(bool down) => talkieSetPttPressed(down: down);

  /// Lautstärke einer Person nur für mich (0 … 3, 1 = normal).
  void setUserVolume(UserDto u, double v) => talkieSetUserVolume(user: u.id, volume: v);

  /// Schaltet jemanden nur für mich stumm – die anderen hören ihn weiter.
  void toggleUserMuted(UserDto u) => talkieSetUserMuted(user: u.id, muted: !u.localMuted);

  Future<void> saveSettings(SettingsDto s) async {
    await talkieSetSettings(s: s);
    config = talkieConfig();
    // Einstellung ausgeschaltet, während das Mikro automatisch stumm war: wieder an
    if (!config.settings.muteInBackground && _autoMuted) {
      _autoMuted = false;
      talkieSetMuted(muted: false);
    }
    if (_active) TalkiePlatform.setVoiceMode(config.settings.echoCancel);
    notifyListeners();
  }

  Future<void> logout() async {
    await talkieLogout();
    config = talkieConfig();
    chats.clear();
    _avatars.clear();
    _avatarPending.clear();
    members.value = null;
    state = null;
    showSetup = true;
    setupError = null;
    notifyListeners();
  }
}

enum BackgroundMuteAction { none, mute, unmute }

/// Entscheidet, ob das Mikro beim Wechsel vorne/hinten automatisch stumm oder wieder an geht.
/// Wer vorher selbst stumm war, bleibt stumm.
BackgroundMuteAction backgroundMuteAction({
  required bool enabled,
  required bool foreground,
  required bool muted,
  required bool autoMuted,
}) {
  if (foreground) return autoMuted ? BackgroundMuteAction.unmute : BackgroundMuteAction.none;
  if (enabled && !muted && !autoMuted) return BackgroundMuteAction.mute;
  return BackgroundMuteAction.none;
}
