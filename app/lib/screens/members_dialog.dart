import 'dart:typed_data';

import 'package:flutter/material.dart';

import '../controller.dart';
import '../src/rust/api/talkie.dart';
import '../theme.dart';
import '../time_ago.dart';
import '../widgets/avatar.dart';

/// Öffnet die Liste aller Mitglieder (fragt den Server beim Öffnen neu ab).
Future<void> showMembersDialog(BuildContext context, TalkieController c) {
  c.requestMembers();
  return showDialog<void>(
    context: context,
    builder: (_) => MembersDialog(controller: c),
  );
}

/// Online zuerst (alphabetisch), danach nach letztem Besuch absteigend, wer noch nie da war, ganz unten.
List<MemberDto> sortMembers(List<MemberDto> list) {
  final l = List.of(list);
  l.sort((a, b) {
    if (a.online != b.online) return a.online ? -1 : 1;
    if (!a.online) {
      final c = b.lastSeen.compareTo(a.lastSeen);
      if (c != 0) return c;
    }
    return a.name.toLowerCase().compareTo(b.name.toLowerCase());
  });
  return l;
}

class MembersDialog extends StatelessWidget {
  const MembersDialog({super.key, required this.controller, this.now});

  final TalkieController controller;

  /// Nur für Tests: feste Uhrzeit für „vor 5 Minuten“.
  final DateTime? now;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    return AlertDialog(
      title: ListenableBuilder(
        listenable: controller.members,
        builder: (context, _) => _Header(list: controller.members.value),
      ),
      content: SizedBox(
        width: 420,
        child: ConstrainedBox(
          constraints: const BoxConstraints(maxHeight: 520),
          child: ListenableBuilder(
            // Neue Profilbilder kommen über den Controller, die Liste über [members]
            listenable: Listenable.merge([controller, controller.members]),
            builder: (context, _) {
              final list = controller.members.value;
              if (list == null) {
                return const SizedBox(
                  height: 160,
                  child: Center(child: CircularProgressIndicator()),
                );
              }
              if (list.isEmpty) {
                return SizedBox(
                  height: 160,
                  child: Center(
                    child: Column(
                      mainAxisSize: MainAxisSize.min,
                      children: [
                        Icon(
                          Icons.people_outline,
                          size: 40,
                          color: theme.colorScheme.onSurfaceVariant,
                        ),
                        const SizedBox(height: 8),
                        Text(
                          'Noch niemand da.',
                          style: TextStyle(
                            color: theme.colorScheme.onSurfaceVariant,
                          ),
                        ),
                      ],
                    ),
                  ),
                );
              }
              final sorted = sortMembers(list);
              final online = sorted.where((m) => m.online).toList();
              final offline = sorted.where((m) => !m.online).toList();
              Widget tile(MemberDto m) => _MemberTile(
                member: m,
                me: m.id.toInt() == controller.me,
                image: controller.avatarOf(m.id.toInt()),
                now: now,
              );
              return ListView(
                shrinkWrap: true,
                children: [
                  if (online.isNotEmpty) ...[
                    const _SectionTitle('Online'),
                    for (final m in online) tile(m),
                  ],
                  if (offline.isNotEmpty) ...[
                    const _SectionTitle('Gerade nicht da'),
                    for (final m in offline) tile(m),
                  ],
                ],
              );
            },
          ),
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.pop(context),
          style: FilledButton.styleFrom(minimumSize: const Size(100, 44)),
          child: const Text('Fertig'),
        ),
      ],
    );
  }
}

class _Header extends StatelessWidget {
  const _Header({required this.list});

  final List<MemberDto>? list;

  @override
  Widget build(BuildContext context) {
    final l = list;
    final online = l?.where((m) => m.online).length ?? 0;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.start,
      children: [
        const Row(
          children: [
            Icon(Icons.groups_rounded, color: kAccent),
            SizedBox(width: 10),
            Expanded(
              child: Text('Alle Mitglieder', overflow: TextOverflow.ellipsis),
            ),
          ],
        ),
        if (l != null && l.isNotEmpty) ...[
          const SizedBox(height: 10),
          Wrap(
            spacing: 8,
            runSpacing: 6,
            children: [
              _CountChip('$online online', kTalking),
              _CountChip(
                '${l.length} insgesamt',
                Theme.of(context).colorScheme.onSurfaceVariant,
              ),
            ],
          ),
        ],
      ],
    );
  }
}

class _CountChip extends StatelessWidget {
  const _CountChip(this.text, this.color);

  final String text;
  final Color color;

  @override
  Widget build(BuildContext context) {
    return Container(
      padding: const EdgeInsets.symmetric(horizontal: 10, vertical: 3),
      decoration: BoxDecoration(
        color: color.withValues(alpha: 0.14),
        borderRadius: BorderRadius.circular(20),
      ),
      child: Text(
        text,
        style: TextStyle(
          color: color,
          fontSize: 12,
          fontWeight: FontWeight.w600,
        ),
      ),
    );
  }
}

class _SectionTitle extends StatelessWidget {
  const _SectionTitle(this.text);

  final String text;

  @override
  Widget build(BuildContext context) {
    return Padding(
      padding: const EdgeInsets.fromLTRB(4, 8, 4, 8),
      child: Text(
        text,
        style: TextStyle(
          fontSize: 12,
          letterSpacing: 0.3,
          fontWeight: FontWeight.w600,
          color: Theme.of(context).colorScheme.onSurfaceVariant,
        ),
      ),
    );
  }
}

class _MemberTile extends StatelessWidget {
  const _MemberTile({
    required this.member,
    required this.me,
    required this.image,
    this.now,
  });

  final MemberDto member;
  final bool me;
  final Uint8List? image;
  final DateTime? now;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final muted = theme.colorScheme.onSurfaceVariant;
    final bg = theme.colorScheme.surfaceContainerHighest;
    return Padding(
      padding: const EdgeInsets.only(bottom: 8),
      child: Container(
        padding: const EdgeInsets.symmetric(horizontal: 12, vertical: 10),
        decoration: BoxDecoration(
          color: bg,
          borderRadius: BorderRadius.circular(14),
          border: Border.all(
            color: me ? kAccent.withValues(alpha: 0.5) : Colors.transparent,
          ),
        ),
        child: Row(
          children: [
            Stack(
              clipBehavior: Clip.none,
              children: [
                Opacity(
                  opacity: member.online ? 1 : 0.6,
                  child: Avatar(name: member.name, size: 44, image: image),
                ),
                if (member.online)
                  Positioned(
                    right: -2,
                    bottom: -2,
                    child: Container(
                      width: 14,
                      height: 14,
                      decoration: BoxDecoration(
                        color: kTalking,
                        shape: BoxShape.circle,
                        border: Border.all(color: bg, width: 2.5),
                      ),
                    ),
                  ),
              ],
            ),
            const SizedBox(width: 14),
            Expanded(
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.start,
                children: [
                  Row(
                    children: [
                      Flexible(
                        child: Text(
                          member.name,
                          overflow: TextOverflow.ellipsis,
                          style: const TextStyle(
                            fontSize: 16,
                            fontWeight: FontWeight.w500,
                          ),
                        ),
                      ),
                      if (me) ...[
                        const SizedBox(width: 8),
                        Container(
                          padding: const EdgeInsets.symmetric(
                            horizontal: 8,
                            vertical: 1,
                          ),
                          decoration: BoxDecoration(
                            color: kAccent.withValues(alpha: 0.18),
                            borderRadius: BorderRadius.circular(10),
                          ),
                          child: const Text(
                            'Du',
                            style: TextStyle(
                              color: kAccent,
                              fontSize: 11,
                              fontWeight: FontWeight.w700,
                            ),
                          ),
                        ),
                      ],
                    ],
                  ),
                  const SizedBox(height: 2),
                  member.online
                      ? const Text(
                          'Online',
                          style: TextStyle(color: kTalking, fontSize: 13),
                        )
                      : Text(
                          'zuletzt da: ${lastSeenText(member.lastSeen.toInt(), now: now)}',
                          overflow: TextOverflow.ellipsis,
                          style: TextStyle(color: muted, fontSize: 13),
                        ),
                ],
              ),
            ),
          ],
        ),
      ),
    );
  }
}
