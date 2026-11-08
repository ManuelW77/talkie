import 'dart:async';

import 'package:flutter/material.dart';
import 'package:flutter/services.dart';

import '../controller.dart';
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
              drawer: narrow
                  ? Drawer(
                      width: 300,
                      child: SafeArea(
                        child: Builder(
                          builder: (context) => _RoomsPanel(
                            controller: c,
                            onPicked: () => Navigator.of(context).maybePop(),
                          ),
                        ),
                      ),
                    )
                  : null,
              body: SafeArea(
                child: Column(
                  children: [
                    _StatusBanner(controller: c),
                    Expanded(
                      child: narrow
                          ? Builder(
                              builder: (context) =>
                                  _ChatPanel(controller: c, onMenu: () => Scaffold.of(context).openDrawer()),
                            )
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
            if (failed)
              const Icon(Icons.wifi_off_rounded, color: kDanger)
            else
              const SizedBox(width: 18, height: 18, child: CircularProgressIndicator(strokeWidth: 2)),
            const SizedBox(width: 12),
            Expanded(child: Text(text)),
            if (failed)
              FilledButton.tonal(
                onPressed: controller.reconnect,
                style: FilledButton.styleFrom(minimumSize: const Size(0, 36)),
                child: const Text('Nochmal verbinden'),
              ),
          ],
        ),
      ),
    );
  }
}

class _RoomsPanel extends StatelessWidget {
  const _RoomsPanel({required this.controller, this.onPicked});

  final TalkieController controller;
  /// Nach dem Antippen eines Raums (schließt die Seitenleiste).
  final VoidCallback? onPicked;

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
          for (final r in rooms) _RoomTile(controller: controller, room: r, onPicked: onPicked),
        ],
      ),
    );
  }
}

class _RoomTile extends StatelessWidget {
  const _RoomTile({required this.controller, required this.room, this.onPicked});

  final TalkieController controller;
  final RoomDto room;
  final VoidCallback? onPicked;

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
          onTap: () {
            controller.join(room.id);
            onPicked?.call();
          },
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

class _ChatPanel extends StatefulWidget {
  const _ChatPanel({required this.controller, this.onMenu});

  final TalkieController controller;
  /// Gesetzt im schmalen Layout: öffnet die Räume.
  final VoidCallback? onMenu;

  @override
  State<_ChatPanel> createState() => _ChatPanelState();
}

class _ChatPanelState extends State<_ChatPanel> {
  final _text = TextEditingController();
  final _focus = FocusNode();

  @override
  void dispose() {
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
          padding: widget.onMenu != null
              ? const EdgeInsets.fromLTRB(4, 4, 16, 4)
              : const EdgeInsets.fromLTRB(20, 16, 20, 8),
          child: Row(
            children: [
              if (widget.onMenu != null)
                IconButton(
                  onPressed: widget.onMenu,
                  tooltip: 'Räume',
                  icon: Badge(
                    isLabelVisible: widget.controller.unread.values.any((n) => n > 0),
                    backgroundColor: kDanger,
                    smallSize: 9,
                    child: const Icon(Icons.menu_rounded),
                  ),
                )
              else ...[
                const Icon(Icons.chat_bubble_rounded, size: 18, color: kAccent),
                const SizedBox(width: 10),
              ],
              Expanded(
                child: Text(_roomName(), overflow: TextOverflow.ellipsis, style: theme.textTheme.titleMedium),
              ),
              if (widget.onMenu != null) _RoomPeople(controller: widget.controller, onTap: widget.onMenu!),
            ],
          ),
        ),
        const Divider(height: 1),
        Expanded(
          child: msgs.isEmpty
              ? Center(
                  child: Text('Noch keine Nachrichten. Schreib was!',
                      style: TextStyle(color: theme.colorScheme.onSurfaceVariant)))
              : ListView.builder(
                  reverse: true,
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
        Padding(
          padding: const EdgeInsets.fromLTRB(16, 4, 16, 12),
          child: Row(
            children: [
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
              ],
            ),
          ),
          SizedBox(width: compact ? 8 : 24),
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
