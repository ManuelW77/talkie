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
  return showDialog<void>(context: context, builder: (_) => MembersDialog(controller: c));
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
    return AlertDialog(
      title: const Text('Alle Mitglieder'),
      content: SizedBox(
        width: 420,
        height: 440,
        child: ListenableBuilder(
          // Neue Profilbilder kommen über den Controller, die Liste über [members]
          listenable: Listenable.merge([controller, controller.members]),
          builder: (context, _) {
            final list = controller.members.value;
            if (list == null) {
              return const Center(child: CircularProgressIndicator());
            }
            if (list.isEmpty) {
              return const Center(child: Text('Noch niemand da.'));
            }
            final sorted = sortMembers(list);
            return ListView.builder(
              itemCount: sorted.length,
              itemBuilder: (context, i) => _MemberTile(
                member: sorted[i],
                me: sorted[i].id.toInt() == controller.me,
                image: controller.avatarOf(sorted[i].id.toInt()),
                now: now,
              ),
            );
          },
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

class _MemberTile extends StatelessWidget {
  const _MemberTile({required this.member, required this.me, required this.image, this.now});

  final MemberDto member;
  final bool me;
  final Uint8List? image;
  final DateTime? now;

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final muted = theme.colorScheme.onSurfaceVariant;
    return ListTile(
      contentPadding: EdgeInsets.zero,
      leading: Avatar(name: member.name, size: 40, image: image),
      title: Text(me ? '${member.name} (du)' : member.name, overflow: TextOverflow.ellipsis),
      subtitle: member.online
          ? Row(
              children: [
                Container(
                  width: 9,
                  height: 9,
                  decoration: const BoxDecoration(color: kTalking, shape: BoxShape.circle),
                ),
                const SizedBox(width: 6),
                const Text('Online', style: TextStyle(color: kTalking)),
              ],
            )
          : Text(
              'zuletzt da: ${lastSeenText(member.lastSeen.toInt(), now: now)}',
              style: TextStyle(color: muted),
            ),
    );
  }
}
