import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../controller.dart';
import '../platform.dart';
import '../src/rust/api/talkie.dart';
import '../theme.dart';
import '../widgets/avatar.dart';
import '../widgets/level_meter.dart';
import 'settings_dialog.dart';

/// Unterhalb dieser Breite (Handy hochkant) wird das Layout einspaltig.
const double kNarrowWidth = 640;

class MainScreen extends StatefulWidget {
  const MainScreen({super.key, required this.controller});

  final TalkieController controller;

  @override
  State<MainScreen> createState() => _MainScreenState();
}

class _MainScreenState extends State<MainScreen> {
  StreamSubscription<String>? _notices;

  @override
  void initState() {
    super.initState();
    _notices = widget.controller.notices.listen((n) {
      if (!mounted) return;
      ScaffoldMessenger.of(context)
        ..hideCurrentSnackBar()
        ..showSnackBar(SnackBar(content: Text(n), behavior: SnackBarBehavior.floating));
    });
  }

  @override
  void dispose() {
    _notices?.cancel();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.controller;
    return CallbackShortcuts(
      bindings: {
        const SingleActivator(LogicalKeyboardKey.keyM, control: true): c.toggleMute,
        const SingleActivator(LogicalKeyboardKey.keyM, meta: true): c.toggleMute,
      },
      child: Focus(
        autofocus: true,
        child: LayoutBuilder(
          builder: (context, box) {
            // Handy hochkant: Chat füllt den Bildschirm, Räume kommen als Seitenleiste
            final narrow = box.maxWidth < kNarrowWidth;
            return Scaffold(
              body: SafeArea(
                child: Column(
                  children: [
                    _StatusBanner(controller: c),
                    if (c.micDenied) _MicBanner(controller: c),
                    if (c.updateVersion != null) _UpdateBanner(controller: c),
                    if (narrow) _PhoneTabs(controller: c),
                    Expanded(
                      child: narrow
                          ? (c.phoneChat
                              ? _ChatPanel(controller: c, compact: true, onShowRooms: () => c.showPhoneChat(false))
                              : _RoomsPanel(controller: c))
                          : Row(
                              crossAxisAlignment: CrossAxisAlignment.stretch,
                              children: [
                                SizedBox(width: box.maxWidth < 900 ? 240.0 : 280.0, child: _RoomsPanel(controller: c)),
                                const VerticalDivider(width: 1),
                                Expanded(child: _ChatPanel(controller: c)),
                              ],
                            ),
                    ),
                    const Divider(height: 1),
                    _ControlBar(controller: c, compact: narrow),
                  ],
                ),
              ),
            );
          },
        ),
      ),
    );
  }
}

class _StatusBanner extends StatelessWidget {
  const _StatusBanner({required this.controller});

  final TalkieController controller;

  @override
  Widget build(BuildContext context) {
    final s = controller.state;
    final status = controller.status;
    if (status == ConnStatus.connected) return const SizedBox.shrink();

    final failed = status == ConnStatus.failed || status == ConnStatus.idle;
    final offline = s?.errorCode == 'offline';
    final text = switch (status) {
      ConnStatus.connecting => 'Verbinde mit dem Server …',
      ConnStatus.reconnecting =>
        (s?.message.isNotEmpty ?? false) ? s!.message : 'Verbindung verloren, verbinde neu …',
      ConnStatus.failed => (s?.message.isNotEmpty ?? false) ? s!.message : 'Verbindung nicht möglich.',
      _ => 'Nicht verbunden.',
    };
    return Material(
      color: failed ? kDanger.withValues(alpha: 0.18) : Colors.amber.withValues(alpha: 0.15),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          children: [
            if (failed || offline)
              Icon(offline ? Icons.wifi_off_rounded : Icons.cloud_off_rounded, color: kDanger)
            else
              const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
            const SizedBox(width: 12),
            Expanded(child: Text(text)),
            if (failed)
              FilledButton.tonal(
                onPressed: controller.reconnect,
                style: FilledButton.styleFrom(minimumSize: const Size(0, 36)),
                child: const Text('Nochmal verbinden'),
              )
            else
              TextButton(onPressed: controller.cancelConnect, child: const Text('Abbrechen')),
          ],
        ),
      ),
    );
  }
}

/// Neue App-Version verfügbar: Laden oder wegklicken.
class _UpdateBanner extends StatelessWidget {
  const _UpdateBanner({required this.controller});

  final TalkieController controller;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: kAccent.withValues(alpha: 0.15),
      child: Padding(
        padding: const EdgeInsets.fromLTRB(16, 6, 4, 6),
        child: Row(
          children: [
            const Icon(Icons.system_update_rounded, color: kAccent),
            const SizedBox(width: 12),
            Expanded(child: Text('Neue Talkie-Version ${controller.updateVersion} ist da.')),
            if (controller.updateUrl.isNotEmpty)
              TextButton(
                onPressed: () => TalkiePlatform.openUrl(controller.updateUrl),
                child: const Text('Laden'),
              ),
            IconButton(
              onPressed: controller.dismissUpdate,
              tooltip: 'Ausblenden',
              icon: const Icon(Icons.close_rounded, size: 20),
            ),
          ],
        ),
      ),
    );
  }
}

/// Handy: Mikrofon nicht erlaubt → erklären und zu den Einstellungen führen.
class _MicBanner extends StatelessWidget {
  const _MicBanner({required this.controller});

  final TalkieController controller;

  @override
  Widget build(BuildContext context) {
    return Material(
      color: kDanger.withValues(alpha: 0.18),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
        child: Row(
          children: [
            const Icon(Icons.mic_off_rounded, color: kDanger),
            const SizedBox(width: 12),
            const Expanded(child: Text('Talkie darf dein Mikrofon nicht benutzen. Die anderen hören dich nicht.')),
            FilledButton.tonal(
              onPressed: TalkiePlatform.openSettings,
              style: FilledButton.styleFrom(minimumSize: const Size(0, 36)),
              child: const Text('Erlauben'),
            ),
          ],
        ),
      ),
    );
  }
}

class _RoomsPanel extends StatelessWidget {
  const _RoomsPanel({required this.controller});

  final TalkieController controller;

  @override
  Widget build(BuildContext context) {
    final rooms = controller.state?.rooms ?? const <RoomDto>[];
    final theme = Theme.of(context);
    return Container(
      color: theme.colorScheme.surface,
      child: ListView(
        padding: const EdgeInsets.all(12),
        children: [
          Padding(
            padding: const EdgeInsets.fromLTRB(8, 4, 8, 10),
            child: Text('RÄUME',
                style: theme.textTheme.labelMedium
                    ?.copyWith(letterSpacing: 1.5, color: theme.colorScheme.onSurfaceVariant)),
          ),
          for (final r in rooms) _RoomTile(controller: controller, room: r),
          Padding(
            padding: const EdgeInsets.only(top: 4),
            child: TextButton.icon(
              onPressed: () => _createRoom(context, controller),
              icon: const Icon(Icons.add_rounded, size: 18),
              label: const Text('Raum erstellen'),
              style: TextButton.styleFrom(alignment: Alignment.centerLeft),
            ),
          ),
        ],
      ),
    );
  }
}

class _RoomTile extends StatelessWidget {
  const _RoomTile({required this.controller, required this.room});

  final TalkieController controller;
  final RoomDto room;

  /// Gesperrte Räume fragen nach dem Passwort (Admins kommen ohne hinein).
  Future<void> _open(BuildContext context) async {
    if (controller.currentRoom == room.id) return;
    if (!room.locked || controller.admin) {
      controller.join(room.id);
      return;
    }
    final pw = await showDialog<String>(
      context: context,
      builder: (_) => _PasswordDialog(roomName: room.name),
    );
    if (pw != null) controller.join(room.id, password: pw);
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final current = controller.currentRoom == room.id;
    final users = controller.usersIn(room.id);
    final unread = controller.unread[room.id] ?? 0;
    return Padding(
      padding: const EdgeInsets.only(bottom: 6),
      child: Material(
        color: current ? kAccent.withValues(alpha: 0.16) : Colors.transparent,
        borderRadius: BorderRadius.circular(12),
        child: InkWell(
          borderRadius: BorderRadius.circular(12),
          onTap: () => _open(context),
          child: Padding(
            padding: const EdgeInsets.fromLTRB(12, 10, 12, 10),
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Row(
                  children: [
                    Icon(Icons.volume_up_rounded, size: 18, color: current ? kAccent : theme.colorScheme.onSurfaceVariant),
                    const SizedBox(width: 8),
                    Expanded(
                      child: Text(room.name,
                          overflow: TextOverflow.ellipsis,
                          style: theme.textTheme.titleSmall?.copyWith(fontWeight: current ? FontWeight.w700 : null)),
                    ),
                    if (room.temp)
                      Tooltip(
                        message: 'Verschwindet, wenn alle gegangen sind',
                        child: Padding(
                          padding: const EdgeInsets.only(left: 6),
                          child: Icon(Icons.hourglass_bottom_rounded,
                              size: 15, color: theme.colorScheme.onSurfaceVariant.withValues(alpha: 0.6)),
                        ),
                      ),
                    if (room.locked)
                      Tooltip(
                        message: 'Mit Passwort',
                        child: Padding(
                          padding: const EdgeInsets.only(left: 6),
                          child: Icon(Icons.lock_rounded, size: 15, color: theme.colorScheme.onSurfaceVariant),
                        ),
                      ),
                    if (unread > 0)
                      Container(
                        padding: const EdgeInsets.symmetric(horizontal: 7, vertical: 2),
                        decoration: BoxDecoration(color: kDanger, borderRadius: BorderRadius.circular(10)),
                        child: Text('$unread', style: const TextStyle(fontSize: 11, fontWeight: FontWeight.bold)),
                      ),
                  ],
                ),
                for (final u in users)
                  Padding(
                    padding: const EdgeInsets.only(left: 10, top: 8),
                    child: Row(
                      children: [
                        Avatar(name: u.name, talking: u.talking, size: 26),
                        const SizedBox(width: 10),
                        Expanded(
                          child: Text(
                            u.id == controller.me ? '${u.name} (du)' : u.name,
                            overflow: TextOverflow.ellipsis,
                            style: TextStyle(
                              color: u.localMuted
                                  ? theme.colorScheme.onSurfaceVariant
                                  : (u.talking ? kTalking : null),
                            ),
                          ),
                        ),
                        if (u.deaf)
                          const Icon(Icons.headset_off_rounded, size: 16, color: kDanger)
                        else if (u.muted)
                          const Icon(Icons.mic_off_rounded, size: 16, color: kDanger),
                        if (u.id != controller.me)
                          _UserMuteButton(controller: controller, user: u)
                        else
                          const SizedBox(width: 30, height: 30),
                        if (u.id != controller.me)
                          _UserMenuButton(controller: controller, user: u)
                        else
                          const SizedBox(width: 30, height: 30),
                      ],
                    ),
                  ),
              ],
            ),
          ),
        ),
      ),
    );
  }
}

/// „Raum erstellen“: Name und optional ein Passwort.
Future<void> _createRoom(BuildContext context, TalkieController controller) async {
  final r = await showDialog<({String name, String password})>(
    context: context,
    builder: (_) => const _CreateRoomDialog(),
  );
  if (r != null) controller.createRoom(r.name, password: r.password);
}

class _CreateRoomDialog extends StatefulWidget {
  const _CreateRoomDialog();

  @override
  State<_CreateRoomDialog> createState() => _CreateRoomDialogState();
}

class _CreateRoomDialogState extends State<_CreateRoomDialog> {
  final _name = TextEditingController();
  final _password = TextEditingController();

  @override
  void dispose() {
    _name.dispose();
    _password.dispose();
    super.dispose();
  }

  void _submit() {
    final name = _name.text.trim();
    if (name.length < 2) return;
    Navigator.pop(context, (name: name, password: _password.text));
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: const Text('Raum erstellen'),
      content: SizedBox(
        width: 340,
        child: Column(
          mainAxisSize: MainAxisSize.min,
          crossAxisAlignment: CrossAxisAlignment.start,
          children: [
            TextField(
              controller: _name,
              autofocus: true,
              maxLength: 20,
              textInputAction: TextInputAction.next,
              decoration: const InputDecoration(labelText: 'Name des Raums'),
            ),
            TextField(
              controller: _password,
              maxLength: 64,
              obscureText: true,
              textInputAction: TextInputAction.done,
              onSubmitted: (_) => _submit(),
              decoration: const InputDecoration(labelText: 'Passwort (kann leer bleiben)'),
            ),
            const SizedBox(height: 8),
            Text('Der Raum verschwindet, wenn alle ihn verlassen haben.',
                style: Theme.of(context).textTheme.bodySmall),
          ],
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Abbrechen')),
        FilledButton(onPressed: _submit, child: const Text('Erstellen')),
      ],
    );
  }
}

class _PasswordDialog extends StatefulWidget {
  const _PasswordDialog({required this.roomName});

  final String roomName;

  @override
  State<_PasswordDialog> createState() => _PasswordDialogState();
}

class _PasswordDialogState extends State<_PasswordDialog> {
  final _password = TextEditingController();

  @override
  void dispose() {
    _password.dispose();
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    return AlertDialog(
      title: Text('„${widget.roomName}“ ist gesperrt'),
      content: SizedBox(
        width: 340,
        child: TextField(
          controller: _password,
          autofocus: true,
          obscureText: true,
          onSubmitted: (v) => Navigator.pop(context, v),
          decoration: const InputDecoration(labelText: 'Passwort'),
        ),
      ),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context), child: const Text('Abbrechen')),
        FilledButton(onPressed: () => Navigator.pop(context, _password.text), child: const Text('Rein')),
      ],
    );
  }
}

/// Lautsprecher neben einem Namen: schaltet die Person nur für mich stumm.
class _UserMuteButton extends StatelessWidget {
  const _UserMuteButton({required this.controller, required this.user});

  final TalkieController controller;
  final UserDto user;

  @override
  Widget build(BuildContext context) {
    final muted = user.localMuted;
    return IconButton(
      onPressed: () => controller.toggleUserMuted(user),
      tooltip: muted ? '${user.name} wieder hören' : '${user.name} für mich stumm schalten',
      icon: Icon(muted ? Icons.volume_off_rounded : Icons.volume_up_rounded),
      iconSize: 18,
      color: muted ? kDanger : Theme.of(context).colorScheme.onSurfaceVariant,
      visualDensity: VisualDensity.compact,
      padding: EdgeInsets.zero,
      constraints: const BoxConstraints.tightFor(width: 30, height: 30),
    );
  }
}

/// Rückfrage vor dem Rauswerfen/Sperren; führt die Aktion bei „Ja“ aus.
Future<void> _confirmModerate(BuildContext context, TalkieController controller, UserDto user, bool ban) async {
  final ok = await showDialog<bool>(
    context: context,
    builder: (context) => AlertDialog(
      title: Text(ban ? '${user.name} sperren?' : '${user.name} rauswerfen?'),
      content: Text(ban
          ? '${user.name} fliegt raus und kann erst wieder rein, wenn ein Admin auf der Verwaltungsseite entsperrt.'
          : '${user.name} fliegt raus, kann sich aber gleich wieder verbinden.'),
      actions: [
        TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Abbrechen')),
        FilledButton(
          style: FilledButton.styleFrom(backgroundColor: kDanger, foregroundColor: Colors.white),
          onPressed: () => Navigator.pop(context, true),
          child: Text(ban ? 'Sperren' : 'Rauswerfen'),
        ),
      ],
    ),
  );
  if (ok == true) controller.moderate(user, ban: ban);
}

/// ⋮ neben einer anderen Person: Lautstärke, nur für mich stumm, (Moderatoren) rauswerfen/sperren.
class _UserMenuButton extends StatelessWidget {
  const _UserMenuButton({required this.controller, required this.user});

  final TalkieController controller;
  final UserDto user;

  @override
  Widget build(BuildContext context) {
    return IconButton(
      tooltip: 'Mehr zu ${user.name}',
      icon: const Icon(Icons.more_vert_rounded, size: 18),
      visualDensity: VisualDensity.compact,
      padding: EdgeInsets.zero,
      constraints: const BoxConstraints.tightFor(width: 30, height: 30),
      onPressed: () => showDialog<void>(
        context: context,
        builder: (_) => _UserDialog(controller: controller, userId: user.id),
      ),
    );
  }
}

class _UserDialog extends StatefulWidget {
  const _UserDialog({required this.controller, required this.userId});

  final TalkieController controller;
  final int userId;

  @override
  State<_UserDialog> createState() => _UserDialogState();
}

class _UserDialogState extends State<_UserDialog> {
  double? _volume; // während des Ziehens

  UserDto? get _user {
    for (final u in widget.controller.state?.users ?? const <UserDto>[]) {
      if (u.id == widget.userId) return u;
    }
    return null;
  }

  @override
  Widget build(BuildContext context) {
    return ListenableBuilder(
      listenable: widget.controller,
      builder: (context, _) {
        final u = _user;
        if (u == null) {
          return AlertDialog(
            content: const Text('Diese Person ist nicht mehr da.'),
            actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('OK'))],
          );
        }
        final theme = Theme.of(context);
        final v = _volume ?? u.volume;
        return AlertDialog(
          title: Row(
            children: [
              Avatar(name: u.name, talking: u.talking && !u.localMuted, size: 32),
              const SizedBox(width: 12),
              Expanded(child: Text(u.name, overflow: TextOverflow.ellipsis)),
            ],
          ),
          content: SizedBox(
            width: 380,
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.stretch,
              children: [
                Text('Lautstärke für dich: ${(v * 100).round()} %', style: theme.textTheme.titleSmall),
                Text('Nur bei dir. Die anderen hören ${u.name} wie immer.',
                    style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
                Slider(
                  value: v.clamp(0.0, 3.0),
                  max: 3,
                  divisions: 30,
                  label: '${(v * 100).round()} %',
                  onChanged: u.localMuted ? null : (x) => setState(() => _volume = x),
                  onChangeEnd: (x) {
                    widget.controller.setUserVolume(u, x);
                    _volume = null;
                  },
                ),
                if ((v - 1).abs() > 0.01 && !u.localMuted)
                  Align(
                    alignment: Alignment.centerRight,
                    child: TextButton(
                      onPressed: () => widget.controller.setUserVolume(u, 1),
                      child: const Text('Normal (100 %)'),
                    ),
                  ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Für mich stumm'),
                  value: u.localMuted,
                  onChanged: (_) => widget.controller.toggleUserMuted(u),
                ),
                if (widget.controller.moderator) ...[
                  const Divider(height: 24),
                  Row(
                    children: [
                      Expanded(
                        child: OutlinedButton.icon(
                          onPressed: () => _confirmModerate(context, widget.controller, u, false),
                          icon: const Icon(Icons.logout_rounded),
                          label: const Text('Rauswerfen'),
                        ),
                      ),
                      const SizedBox(width: 8),
                      Expanded(
                        child: OutlinedButton.icon(
                          style: OutlinedButton.styleFrom(foregroundColor: kDanger),
                          onPressed: () => _confirmModerate(context, widget.controller, u, true),
                          icon: const Icon(Icons.block_rounded),
                          label: const Text('Sperren'),
                        ),
                      ),
                    ],
                  ),
                ],
              ],
            ),
          ),
          actions: [TextButton(onPressed: () => Navigator.pop(context), child: const Text('Fertig'))],
        );
      },
    );
  }
}

/// Handy: Umschalter zwischen Räumen (wer ist da, wer spricht) und Chat.
class _PhoneTabs extends StatelessWidget {
  const _PhoneTabs({required this.controller});

  final TalkieController controller;

  @override
  Widget build(BuildContext context) {
    final c = controller;
    final otherUnread = c.unread.values.any((n) => n > 0);
    return Padding(
      padding: const EdgeInsets.fromLTRB(12, 8, 12, 8),
      child: SegmentedButton<bool>(
        showSelectedIcon: false,
        segments: [
          ButtonSegment(
            value: false,
            label: const Text('Räume'),
            icon: Badge(
              isLabelVisible: otherUnread,
              backgroundColor: kDanger,
              smallSize: 8,
              child: const Icon(Icons.groups_rounded),
            ),
          ),
          ButtonSegment(
            value: true,
            label: const Text('Chat'),
            icon: Badge(
              isLabelVisible: c.unreadHere > 0,
              label: Text('${c.unreadHere}'),
              backgroundColor: kDanger,
              child: const Icon(Icons.chat_bubble_rounded),
            ),
          ),
        ],
        selected: {c.phoneChat},
        onSelectionChanged: (s) => c.showPhoneChat(s.first),
      ),
    );
  }
}

/// Nur für Admins: Chat dieses Raums oder aller Räume leeren, mit Rückfrage.
class _ClearChatMenu extends StatelessWidget {
  const _ClearChatMenu({required this.controller, required this.roomName});

  final TalkieController controller;
  final String roomName;

  Future<void> _confirm(BuildContext context, bool all) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (context) => AlertDialog(
        title: Text(all ? 'Chat in allen Räumen leeren?' : 'Chat in „$roomName“ leeren?'),
        content: const Text('Die Nachrichten sind dann für alle weg.'),
        actions: [
          TextButton(onPressed: () => Navigator.pop(context, false), child: const Text('Abbrechen')),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: kDanger, foregroundColor: Colors.white),
            onPressed: () => Navigator.pop(context, true),
            child: const Text('Leeren'),
          ),
        ],
      ),
    );
    if (ok == true) controller.clearChat(all: all);
  }

  @override
  Widget build(BuildContext context) {
    return PopupMenuButton<bool>(
      tooltip: 'Chat leeren',
      icon: const Icon(Icons.delete_sweep_rounded),
      onSelected: (all) => _confirm(context, all),
      itemBuilder: (_) => [
        PopupMenuItem(value: false, child: Text('„$roomName“ leeren')),
        const PopupMenuItem(value: true, child: Text('Alle Räume leeren')),
      ],
    );
  }
}

class _ChatPanel extends StatefulWidget {
  const _ChatPanel({required this.controller, this.compact = false, this.onShowRooms});

  final TalkieController controller;
  /// Handy: schlanker Kopf mit den Leuten im Raum, Knopf zum Einklappen der Tastatur.
  final bool compact;
  /// Handy: Antippen der Leute oben zeigt die Räume.
  final VoidCallback? onShowRooms;

  @override
  State<_ChatPanel> createState() => _ChatPanelState();
}

class _ChatPanelState extends State<_ChatPanel> {
  final _text = TextEditingController();
  final _focus = FocusNode();

  @override
  void initState() {
    super.initState();
    _focus.addListener(_onFocus); // Tastatur-Knopf ein-/ausblenden
  }

  void _onFocus() => setState(() {});

  @override
  void dispose() {
    _focus.removeListener(_onFocus);
    _text.dispose();
    _focus.dispose();
    super.dispose();
  }

  void _send() {
    final t = _text.text.trim();
    if (t.isEmpty) return;
    widget.controller.sendChat(t);
    _text.clear();
    _focus.requestFocus();
  }

  String _roomName() {
    final rooms = widget.controller.state?.rooms ?? const <RoomDto>[];
    for (final r in rooms) {
      if (r.id == widget.controller.currentRoom) return r.name;
    }
    return '';
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final msgs = widget.controller.currentChat;
    final connected = widget.controller.status == ConnStatus.connected;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Padding(
          padding: widget.compact
              ? const EdgeInsets.fromLTRB(20, 4, 12, 4)
              : const EdgeInsets.fromLTRB(20, 16, 20, 8),
          child: Row(
            children: [
              const Icon(Icons.chat_bubble_rounded, size: 18, color: kAccent),
              const SizedBox(width: 10),
              Expanded(
                child: Text(_roomName(), overflow: TextOverflow.ellipsis, style: theme.textTheme.titleMedium),
              ),
              if (widget.onShowRooms != null) _RoomPeople(controller: widget.controller, onTap: widget.onShowRooms!),
              if (widget.controller.admin) _ClearChatMenu(controller: widget.controller, roomName: _roomName()),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          // Tippen in den Verlauf klappt die Tastatur ein
          child: GestureDetector(
            behavior: HitTestBehavior.translucent,
            onTap: _focus.unfocus,
            child: msgs.isEmpty
              ? Center(
                  child: Text('Noch keine Nachrichten. Schreib was!',
                      style: TextStyle(color: theme.colorScheme.onSurfaceVariant)))
              : ListView.builder(
                  reverse: true,
                  // Wischen durch den Verlauf klappt die Tastatur ein
                  keyboardDismissBehavior: ScrollViewKeyboardDismissBehavior.onDrag,
                  padding: const EdgeInsets.symmetric(horizontal: 20, vertical: 12),
                  itemCount: msgs.length,
                  itemBuilder: (context, i) {
                    final m = msgs[msgs.length - 1 - i];
                    final prev = i + 1 < msgs.length ? msgs[msgs.length - 2 - i] : null;
                    final grouped = prev != null && prev.from == m.from && (m.ts - prev.ts) < 120;
                    return _ChatLine(msg: m, grouped: grouped);
                  },
                ),
          ),
        ),
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
          child: Row(
            children: [
              if (widget.compact && _focus.hasFocus) ...[
                IconButton(
                  onPressed: _focus.unfocus,
                  icon: const Icon(Icons.keyboard_hide_rounded),
                  tooltip: 'Tastatur einklappen',
                ),
                const SizedBox(width: 4),
              ],
              Expanded(
                child: TextField(
                  controller: _text,
                  focusNode: _focus,
                  enabled: connected,
                  maxLength: 500,
                  minLines: 1,
                  maxLines: 4,
                  textInputAction: TextInputAction.send,
                  onSubmitted: (_) => _send(),
                  decoration: const InputDecoration(
                    hintText: 'Nachricht schreiben …',
                    counterText: '',
                    isDense: true,
                    contentPadding: EdgeInsets.symmetric(horizontal: 14, vertical: 14),
                  ),
                ),
              ),
              const SizedBox(width: 8),
              IconButton.filled(
                onPressed: connected ? _send : null,
                icon: const Icon(Icons.send_rounded),
                tooltip: 'Senden',
              ),
            ],
          ),
        ),
      ],
    );
  }
}

/// Schmales Layout: wer gerade im Raum ist (Avatare), antippen öffnet die Raumliste.
class _RoomPeople extends StatelessWidget {
  const _RoomPeople({required this.controller, required this.onTap});

  final TalkieController controller;
  final VoidCallback onTap;

  @override
  Widget build(BuildContext context) {
    final users = controller.usersIn(controller.currentRoom);
    final shown = users.take(4).toList();
    return InkWell(
      onTap: onTap,
      borderRadius: BorderRadius.circular(20),
      child: Padding(
        padding: const EdgeInsets.symmetric(horizontal: 6, vertical: 6),
        child: Row(
          mainAxisSize: MainAxisSize.min,
          children: [
            for (final u in shown)
              Padding(
                padding: const EdgeInsets.only(left: 4),
                child: Avatar(name: u.name, talking: u.talking && !u.localMuted, size: 28),
              ),
            if (users.length > shown.length)
              Padding(
                padding: const EdgeInsets.only(left: 6),
                child: Text('+${users.length - shown.length}', style: Theme.of(context).textTheme.labelMedium),
              ),
          ],
        ),
      ),
    );
  }
}

class _ChatLine extends StatelessWidget {
  const _ChatLine({required this.msg, required this.grouped});

  final ChatDto msg;
  final bool grouped;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final t = DateTime.fromMillisecondsSinceEpoch(msg.ts * 1000);
    final time = '${t.hour.toString().padLeft(2, '0')}:${t.minute.toString().padLeft(2, '0')}';
    return Padding(
      padding: EdgeInsets.only(top: grouped ? 2 : 12),
      child: Row(
        crossAxisAlignment: CrossAxisAlignment.start,
        children: [
          SizedBox(
            width: 40,
            child: grouped ? null : Avatar(name: msg.name, size: 32),
          ),
          const SizedBox(width: 10),
          Expanded(
            child: Column(
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                if (!grouped)
                  Row(
                    children: [
                      Text(msg.name, style: TextStyle(fontWeight: FontWeight.w700, color: nameColor(msg.name))),
                      const SizedBox(width: 8),
                      Text(time, style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant)),
                    ],
                  ),
                SelectableText(msg.text, style: theme.textTheme.bodyLarge),
              ],
            ),
          ),
        ],
      ),
    );
  }
}

class _ControlBar extends StatelessWidget {
  const _ControlBar({required this.controller, this.compact = false});

  final TalkieController controller;
  /// Handy: kürzere Beschriftung, Pegel passt sich der Breite an.
  final bool compact;

  @override
  Widget build(BuildContext context) {
    final c = controller;
    final me = c.meUser;
    final theme = Theme.of(context);
    return Container(
      color: theme.colorScheme.surface,
      padding: const EdgeInsets.symmetric(horizontal: 16, vertical: 10),
      child: Row(
        children: [
          Avatar(name: me?.name ?? c.config.name, talking: me?.talking ?? false, size: 40),
          const SizedBox(width: 12),
          Expanded(
            child: Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.start,
              children: [
                Text(me?.name ?? c.config.name,
                    overflow: TextOverflow.ellipsis, style: theme.textTheme.titleSmall),
                const SizedBox(height: 6),
                ConstrainedBox(
                  constraints: const BoxConstraints(maxWidth: 160),
                  child: ValueListenableBuilder<double>(
                    valueListenable: c.levelNotifier,
                    builder: (_, v, _) => LevelMeter(
                      level: c.muted ? 0 : v,
                      threshold: (c.config.settings.vadThresholdDb + 60) / 60,
                    ),
                  ),
                ),
                if (c.config.settings.muteInBackground) ...[
                  const SizedBox(height: 6),
                  const _BackgroundMuteHint(),
                ],
              ],
            ),
          ),
          SizedBox(width: compact ? 8 : 24),
          if (c.config.settings.ptt) ...[
            _PttButton(controller: c, compact: compact),
            SizedBox(width: compact ? 6 : 10),
          ],
          _RoundButton(
            icon: c.muted ? Icons.mic_off_rounded : Icons.mic_rounded,
            label: c.muted ? 'Stumm' : 'Mikro an',
            active: c.muted,
            onTap: c.toggleMute,
            tooltip: 'Mikrofon an/aus (Strg+M)',
          ),
          SizedBox(width: compact ? 6 : 10),
          _RoundButton(
            icon: c.deaf ? Icons.headset_off_rounded : Icons.headset_rounded,
            label: c.deaf ? 'Taub' : 'Hören',
            active: c.deaf,
            onTap: c.toggleDeaf,
            tooltip: 'Niemanden hören (und selbst stumm)',
          ),
          SizedBox(width: compact ? 6 : 10),
          _RoundButton(
            icon: Icons.settings_rounded,
            label: compact ? 'Mehr' : 'Einstellungen',
            active: false,
            onTap: () => showDialog(context: context, builder: (_) => SettingsDialog(controller: c)),
            tooltip: 'Einstellungen',
          ),
        ],
      ),
    );
  }
}

/// Push-to-Talk: gedrückt halten zum Sprechen (Maus, Finger). Die Taste aus den Einstellungen
/// funktioniert zusätzlich systemweit.
class _PttButton extends StatefulWidget {
  const _PttButton({required this.controller, required this.compact});

  final TalkieController controller;
  final bool compact;

  @override
  State<_PttButton> createState() => _PttButtonState();
}

class _PttButtonState extends State<_PttButton> {
  bool _down = false;

  void _set(bool down) {
    if (_down == down) return;
    setState(() => _down = down);
    widget.controller.setPttPressed(down);
  }

  @override
  void dispose() {
    if (_down) widget.controller.setPttPressed(false);
    super.dispose();
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Tooltip(
      message: 'Gedrückt halten zum Sprechen',
      child: Listener(
        onPointerDown: (_) => _set(true),
        onPointerUp: (_) => _set(false),
        onPointerCancel: (_) => _set(false),
        child: Column(
          mainAxisSize: MainAxisSize.min,
          children: [
            AnimatedContainer(
              duration: const Duration(milliseconds: 80),
              padding: EdgeInsets.symmetric(horizontal: widget.compact ? 14 : 20, vertical: 12),
              decoration: BoxDecoration(
                color: _down ? kTalking : theme.colorScheme.surfaceContainerHighest,
                borderRadius: BorderRadius.circular(24),
              ),
              child: Icon(Icons.record_voice_over_rounded, size: 24, color: _down ? Colors.black : null),
            ),
            const SizedBox(height: 4),
            Text(_down ? 'Sprichst …' : 'Halten', style: theme.textTheme.labelSmall),
          ],
        ),
      ),
    );
  }
}

class _RoundButton extends StatelessWidget {
  const _RoundButton({
    required this.icon,
    required this.label,
    required this.active,
    required this.onTap,
    required this.tooltip,
  });

  final IconData icon;
  final String label;
  final bool active;
  final VoidCallback onTap;
  final String tooltip;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return Tooltip(
      message: tooltip,
      child: Column(
        mainAxisSize: MainAxisSize.min,
        children: [
          Material(
            color: active ? kDanger : theme.colorScheme.surfaceContainerHighest,
            shape: const CircleBorder(),
            child: InkWell(
              customBorder: const CircleBorder(),
              onTap: onTap,
              child: Padding(padding: const EdgeInsets.all(12), child: Icon(icon, size: 24)),
            ),
          ),
          const SizedBox(height: 4),
          Text(label, style: theme.textTheme.labelSmall),
        ],
      ),
    );
  }
}

/// Hinweis unter dem Pegel: Mikro geht aus, wenn Talkie nicht vorne ist.
class _BackgroundMuteHint extends StatelessWidget {
  const _BackgroundMuteHint();

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 8, vertical: 2),
      decoration: BoxDecoration(
        color: kAccent.withValues(alpha: 0.18),
        borderRadius: BorderRadius.circular(10),
      ),
      // Schmales Handy: lieber etwas kleiner als abgeschnitten
      child: FittedBox(
        fit: BoxFit.scaleDown,
        child: Text(
          'Im Hintergrund stumm',
          maxLines: 1,
          style: Theme.of(context).textTheme.labelSmall?.copyWith(color: kAccent, fontWeight: FontWeight.w600),
        ),
      ),
    );
  }
}
