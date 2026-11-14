import 'dart:async';
import 'dart:typed_data';

import 'package:file_picker/file_picker.dart';
import 'package:flutter/material.dart';

import '../avatar_image.dart';
import '../controller.dart';
import '../src/rust/api/talkie.dart';
import '../theme.dart';
import '../widgets/avatar.dart';

/// „Dein Profil“: eigenes Profilbild setzen oder entfernen.
class ProfileDialog extends StatefulWidget {
  const ProfileDialog({super.key, required this.controller});

  final TalkieController controller;

  @override
  State<ProfileDialog> createState() => _ProfileDialogState();
}

class _ProfileDialogState extends State<ProfileDialog> {
  bool _avatarBusy = false;
  String? _avatarError;
  StreamSubscription<String>? _avatarSub;

  static const _avatarFailed =
      'Das Bild hat nicht geklappt. Probier ein anderes.';

  @override
  void initState() {
    super.initState();
    _avatarSub = widget.controller.avatarErrors.listen((_) {
      if (!mounted) return;
      setState(() {
        _avatarBusy = false;
        _avatarError = _avatarFailed;
      });
    });
  }

  @override
  void dispose() {
    _avatarSub?.cancel();
    super.dispose();
  }

  /// Bild aussuchen, auf 128×128 zuschneiden und an den Server schicken.
  Future<void> _pickAvatar() async {
    setState(() {
      _avatarBusy = true;
      _avatarError = null;
    });
    try {
      final f = await FilePicker.pickFile(
        type: FileType.image,
        dialogTitle: 'Profilbild wählen',
      );
      if (f == null) {
        if (mounted) setState(() => _avatarBusy = false);
        return;
      }
      final jpg = await prepareAvatar(await f.readAsBytes());
      if (!mounted) return;
      if (jpg == null) {
        setState(() {
          _avatarBusy = false;
          _avatarError = _avatarFailed;
        });
        return;
      }
      widget.controller.setAvatar(jpg);
      setState(() => _avatarBusy = false);
    } catch (_) {
      if (mounted) {
        setState(() {
          _avatarBusy = false;
          _avatarError = _avatarFailed;
        });
      }
    }
  }

  void _removeAvatar() {
    setState(() => _avatarError = null);
    widget.controller.setAvatar(Uint8List(0));
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = widget.controller;
    return AlertDialog(
      title: const Text('Dein Profil'),
      content: SizedBox(
        width: 360,
        child: ListenableBuilder(
          listenable: c,
          builder: (context, _) {
            final online = c.status == ConnStatus.connected;
            final name = c.meUser?.name ?? c.config.name;
            final has = c.myAvatarVersion > 0;
            return Column(
              mainAxisSize: MainAxisSize.min,
              crossAxisAlignment: CrossAxisAlignment.center,
              children: [
                Avatar(name: name, size: 96, image: c.avatarOf(c.me)),
                const SizedBox(height: 10),
                Text(
                  name,
                  style: theme.textTheme.titleMedium,
                  overflow: TextOverflow.ellipsis,
                ),
                const SizedBox(height: 12),
                Wrap(
                  alignment: WrapAlignment.center,
                  spacing: 8,
                  children: [
                    FilledButton.tonalIcon(
                      onPressed: online && !_avatarBusy ? _pickAvatar : null,
                      icon: _avatarBusy
                          ? const SizedBox(
                              width: 18,
                              height: 18,
                              child: CircularProgressIndicator(strokeWidth: 2),
                            )
                          : const Icon(Icons.photo_rounded),
                      label: const Text('Bild wählen'),
                    ),
                    if (has)
                      TextButton.icon(
                        onPressed: online && !_avatarBusy
                            ? _removeAvatar
                            : null,
                        icon: const Icon(Icons.delete_outline_rounded),
                        label: const Text('Bild entfernen'),
                      ),
                  ],
                ),
                if (!online)
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(
                      'Ein Bild kannst du setzen, sobald Talkie verbunden ist.',
                      style: theme.textTheme.bodySmall?.copyWith(
                        color: theme.colorScheme.onSurfaceVariant,
                      ),
                      textAlign: TextAlign.center,
                    ),
                  ),
                if (_avatarError != null)
                  Padding(
                    padding: const EdgeInsets.only(top: 6),
                    child: Text(
                      _avatarError!,
                      style: theme.textTheme.bodyMedium?.copyWith(
                        color: kDanger,
                        fontWeight: FontWeight.w600,
                      ),
                      textAlign: TextAlign.center,
                    ),
                  ),
              ],
            );
          },
        ),
      ),
      actions: [
        FilledButton(
          onPressed: () => Navigator.pop(context),
          child: const Text('Fertig'),
        ),
      ],
    );
  }
}
