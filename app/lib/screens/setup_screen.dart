import 'package:flutter/material.dart';

import '../src/rust/api/talkie.dart' show ConnStatus;
import '../controller.dart';
import '../theme.dart';

class SetupScreen extends StatefulWidget {
  const SetupScreen({super.key, required this.controller});

  final TalkieController controller;

  @override
  State<SetupScreen> createState() => _SetupScreenState();
}

class _SetupScreenState extends State<SetupScreen> {
  late final _name = TextEditingController(text: widget.controller.config.name);
  final _invite = TextEditingController();
  late final _server = TextEditingController(
    text: widget.controller.config.server.isNotEmpty ? widget.controller.config.server : kDefaultServer,
  );
  final _form = GlobalKey<FormState>();
  late bool _showServer = kDefaultServer.isEmpty;

  @override
  void dispose() {
    _name.dispose();
    _invite.dispose();
    _server.dispose();
    super.dispose();
  }

  void _submit() {
    if (!_form.currentState!.validate()) return;
    widget.controller.register(server: _server.text.trim(), name: _name.text.trim(), invite: _invite.text.trim());
  }

  @override
  Widget build(BuildContext context) {
    final c = widget.controller;
    final theme = Theme.of(context);
    return Scaffold(
      body: Center(
        child: SingleChildScrollView(
          padding: const EdgeInsets.all(24),
          child: ConstrainedBox(
            constraints: const BoxConstraints(maxWidth: 400),
            child: Form(
              key: _form,
              child: Column(
                crossAxisAlignment: CrossAxisAlignment.stretch,
                children: [
                  const Icon(Icons.headset_mic_rounded, size: 72, color: kAccent),
                  const SizedBox(height: 12),
                  Text('Talkie', textAlign: TextAlign.center, style: theme.textTheme.displaySmall),
                  const SizedBox(height: 8),
                  Text(
                    'Gib deinen Namen und den Einladungscode ein, den du bekommen hast.',
                    textAlign: TextAlign.center,
                    style: theme.textTheme.bodyLarge?.copyWith(color: theme.colorScheme.onSurfaceVariant),
                  ),
                  const SizedBox(height: 28),
                  TextFormField(
                    controller: _name,
                    autofocus: _name.text.isEmpty,
                    textInputAction: TextInputAction.next,
                    maxLength: 20,
                    decoration: const InputDecoration(
                      labelText: 'Dein Name',
                      prefixIcon: Icon(Icons.person_rounded),
                      counterText: '',
                    ),
                    validator: (v) => (v == null || v.trim().length < 2) ? 'Mindestens 2 Zeichen' : null,
                  ),
                  const SizedBox(height: 14),
                  TextFormField(
                    controller: _invite,
                    autofocus: _name.text.isNotEmpty,
                    textCapitalization: TextCapitalization.characters,
                    textInputAction: TextInputAction.go,
                    onFieldSubmitted: (_) => _submit(),
                    style: const TextStyle(letterSpacing: 2, fontWeight: FontWeight.w600),
                    decoration: const InputDecoration(
                      labelText: 'Einladungscode',
                      hintText: 'ABCD-EFGH',
                      prefixIcon: Icon(Icons.vpn_key_rounded),
                    ),
                    validator: (v) {
                      final clean = (v ?? '').replaceAll(RegExp(r'[\s-]'), '');
                      return clean.length != 8 ? 'Der Code hat 8 Zeichen, z. B. ABCD-EFGH' : null;
                    },
                  ),
                  if (_showServer) ...[
                    const SizedBox(height: 14),
                    TextFormField(
                      controller: _server,
                      textInputAction: TextInputAction.go,
                      onFieldSubmitted: (_) => _submit(),
                      decoration: const InputDecoration(
                        labelText: 'Server',
                        hintText: 'talkie.example.org',
                        prefixIcon: Icon(Icons.dns_rounded),
                      ),
                      validator: (v) => (v == null || v.trim().isEmpty) ? 'Bitte Serveradresse eingeben' : null,
                    ),
                  ],
                  if (c.setupError != null) ...[
                    const SizedBox(height: 16),
                    Container(
                      padding: const EdgeInsets.all(12),
                      decoration: BoxDecoration(
                        color: kDanger.withValues(alpha: 0.15),
                        borderRadius: BorderRadius.circular(12),
                      ),
                      child: Row(
                        children: [
                          const Icon(Icons.error_outline_rounded, color: kDanger),
                          const SizedBox(width: 10),
                          Expanded(child: Text(c.setupError!)),
                        ],
                      ),
                    ),
                  ],
                  const SizedBox(height: 24),
                  FilledButton(
                    onPressed: c.setupBusy ? null : _submit,
                    child: c.setupBusy
                        ? const SizedBox(width: 22, height: 22, child: CircularProgressIndicator(strokeWidth: 2.5))
                        : const Text("Los geht's"),
                  ),
                  // Klappt es nicht auf Anhieb, sieht man warum und kann abbrechen
                  if (c.setupBusy && c.status == ConnStatus.reconnecting) ...[
                    const SizedBox(height: 12),
                    Text(
                      c.state?.message ?? '',
                      textAlign: TextAlign.center,
                      style: Theme.of(context).textTheme.bodySmall,
                    ),
                    TextButton(onPressed: c.cancelSetup, child: const Text('Abbrechen')),
                  ],
                  if (!_showServer) ...[
                    const SizedBox(height: 8),
                    TextButton(
                      onPressed: () => setState(() => _showServer = true),
                      child: const Text('Erweitert'),
                    ),
                  ],
                ],
              ),
            ),
          ),
        ),
      ),
    );
  }
}
