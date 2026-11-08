import 'package:flutter/material.dart';

import '../controller.dart';
import '../platform.dart';
import '../src/rust/api/talkie.dart';
import '../theme.dart';
import '../widgets/level_meter.dart';

class SettingsDialog extends StatefulWidget {
  const SettingsDialog({super.key, required this.controller});

  final TalkieController controller;

  @override
  State<SettingsDialog> createState() => _SettingsDialogState();
}

class _SettingsDialogState extends State<SettingsDialog> {
  final List<PttKeyDto> _pttKeys = talkiePttKeys();
  String _pttAccess = talkiePttAccess();
  late SettingsDto _s = widget.controller.config.settings;
  List<String> _inputs = const [];
  List<String> _outputs = const [];

  @override
  void initState() {
    super.initState();
    _loadDevices();
  }

  Future<void> _loadDevices() async {
    final ins = await talkieInputDevices();
    final outs = await talkieOutputDevices();
    if (!mounted) return;
    setState(() {
      _inputs = ins;
      _outputs = outs;
    });
  }

  void _update(SettingsDto s) {
    setState(() => _s = s);
    widget.controller.saveSettings(s);
  }

  SettingsDto _copy({
    double? vad,
    bool? ns,
    String? input,
    String? output,
    double? gain,
    double? volume,
    bool? sounds,
    bool? ptt,
    String? pttKey,
  }) => SettingsDto(
    vadThresholdDb: vad ?? _s.vadThresholdDb,
    noiseSuppression: ns ?? _s.noiseSuppression,
    inputDevice: input ?? _s.inputDevice,
    outputDevice: output ?? _s.outputDevice,
    inputGain: gain ?? _s.inputGain,
    outputVolume: volume ?? _s.outputVolume,
    sounds: sounds ?? _s.sounds,
    ptt: ptt ?? _s.ptt,
    pttKey: pttKey ?? _s.pttKey,
  );

  Widget _pttSection(ThemeData theme) {
    final hint = theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant);
    final access = TalkiePlatform.isMobile ? 'unsupported' : _pttAccess;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Sprechen', style: theme.textTheme.titleSmall),
        const SizedBox(height: 8),
        SegmentedButton<bool>(
          showSelectedIcon: false,
          segments: const [
            ButtonSegment(value: false, label: Text('Automatisch'), icon: Icon(Icons.graphic_eq_rounded)),
            ButtonSegment(value: true, label: Text('Taste halten'), icon: Icon(Icons.touch_app_rounded)),
          ],
          selected: {_s.ptt},
          onSelectionChanged: (v) => _update(_copy(ptt: v.first)),
        ),
        const SizedBox(height: 6),
        Text(
          _s.ptt
              ? 'Du wirst nur gehört, solange du die Taste oder unten den Knopf „Halten“ gedrückt hältst.'
              : 'Talkie sendet von selbst, sobald du sprichst.',
          style: hint,
        ),
        if (_s.ptt && access != 'unsupported') ...[
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            initialValue: _pttKeys.any((k) => k.id == _s.pttKey) ? _s.pttKey : null,
            decoration: const InputDecoration(labelText: 'Sprechtaste', prefixIcon: Icon(Icons.keyboard_rounded)),
            items: [for (final k in _pttKeys) DropdownMenuItem(value: k.id, child: Text(k.label))],
            onChanged: (v) {
              if (v != null) _update(_copy(pttKey: v));
            },
          ),
          const SizedBox(height: 4),
          Text('Funktioniert auch, während ein Spiel im Vordergrund ist.', style: hint),
          if (access == 'permission') ...[
            const SizedBox(height: 8),
            Row(
              children: [
                const Icon(Icons.warning_amber_rounded, color: kDanger, size: 20),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    'macOS muss Talkie die „Eingabeüberwachung“ erlauben, sonst reagiert Talkie nicht auf die Taste.',
                  ),
                ),
                TextButton(
                  onPressed: () async {
                    await talkiePttRequestAccess();
                    setState(() => _pttAccess = talkiePttAccess());
                  },
                  child: const Text('Erlauben'),
                ),
              ],
            ),
          ],
        ],
      ],
    );
  }

  Widget _deviceDropdown(
    String label,
    IconData icon,
    String value,
    List<String> options,
    ValueChanged<String> onChanged,
  ) {
    final items = <DropdownMenuItem<String>>[
      const DropdownMenuItem(value: '', child: Text('Systemstandard')),
      for (final o in options)
        DropdownMenuItem(
          value: o,
          child: Text(o, overflow: TextOverflow.ellipsis),
        ),
    ];
    final v = value.isEmpty || options.contains(value) ? value : '';
    return DropdownButtonFormField<String>(
      initialValue: v,
      isExpanded: true,
      items: items,
      onChanged: (x) => onChanged(x ?? ''),
      decoration: InputDecoration(labelText: label, prefixIcon: Icon(icon)),
    );
  }

  Future<void> _confirmLogout() async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Gerät abmelden?'),
        content: const Text(
          'Danach brauchst du einen neuen Einladungscode, um wieder reinzukommen. Das ist nur nötig, wenn du einen anderen Namen oder Server willst.',
        ),
        actions: [
          TextButton(onPressed: () => Navigator.pop(ctx, false), child: const Text('Abbrechen')),
          FilledButton(
            style: FilledButton.styleFrom(backgroundColor: kDanger, minimumSize: const Size(0, 40)),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Abmelden'),
          ),
        ],
      ),
    );
    if (ok == true && mounted) {
      Navigator.pop(context);
      await widget.controller.logout();
    }
  }

  @override
  Widget build(BuildContext context) {
    final theme = Theme.of(context);
    final c = widget.controller;
    return AlertDialog(
      title: const Text('Einstellungen'),
      content: SizedBox(
        width: 460,
        child: SingleChildScrollView(
          child: Column(
            mainAxisSize: MainAxisSize.min,
            crossAxisAlignment: CrossAxisAlignment.stretch,
            children: [
              // Auf Handy und Tablet wählt das System Mikrofon und Lautsprecher selbst
              if (!TalkiePlatform.isMobile) ...[
                _deviceDropdown(
                  'Mikrofon',
                  Icons.mic_rounded,
                  _s.inputDevice,
                  _inputs,
                  (v) => _update(_copy(input: v)),
                ),
                const SizedBox(height: 12),
                _deviceDropdown(
                  'Kopfhörer / Lautsprecher',
                  Icons.headphones_rounded,
                  _s.outputDevice,
                  _outputs,
                  (v) => _update(_copy(output: v)),
                ),
                const SizedBox(height: 20),
              ],
              _pttSection(theme),
              const SizedBox(height: 20),
              if (!_s.ptt) ...[
                Text('Empfindlichkeit', style: theme.textTheme.titleSmall),
                Text(
                  'Sprich normal: Der Balken muss über den weißen Strich kommen. '
                  'Schiebe nach links, wenn du nicht gehört wirst, nach rechts, wenn Tastatur & Co. mitgesendet werden.',
                  style: theme.textTheme.bodySmall?.copyWith(color: theme.colorScheme.onSurfaceVariant),
                ),
                const SizedBox(height: 10),
                ValueListenableBuilder<double>(
                  valueListenable: c.levelNotifier,
                  builder: (_, v, _) => LevelMeter(level: v, threshold: (_s.vadThresholdDb + 60) / 60, height: 12),
                ),
                Slider(
                  value: _s.vadThresholdDb.clamp(-60.0, -15.0),
                  min: -60,
                  max: -15,
                  divisions: 45,
                  label: '${_s.vadThresholdDb.round()} dB',
                  onChanged: (v) => setState(() => _s = _copy(vad: v)),
                  onChangeEnd: (v) => _update(_copy(vad: v)),
                ),
              ],
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('Rauschfilter'),
                subtitle: const Text('Filtert Lüfter, Tastatur und Hintergrundgeräusche'),
                value: _s.noiseSuppression,
                onChanged: (v) => _update(_copy(ns: v)),
              ),
              SwitchListTile(
                contentPadding: EdgeInsets.zero,
                title: const Text('Töne'),
                subtitle: const Text('Kurzer Ton, wenn jemand kommt oder geht und bei neuen Chatnachrichten'),
                value: _s.sounds,
                onChanged: (v) => _update(_copy(sounds: v)),
              ),
              const SizedBox(height: 8),
              Text('Mikrofon-Lautstärke', style: theme.textTheme.titleSmall),
              Slider(
                value: _s.inputGain.clamp(0.25, 4.0),
                min: 0.25,
                max: 4,
                label: '${(_s.inputGain * 100).round()} %',
                divisions: 15,
                onChanged: (v) => setState(() => _s = _copy(gain: v)),
                onChangeEnd: (v) => _update(_copy(gain: v)),
              ),
              Text('Lautstärke der anderen', style: theme.textTheme.titleSmall),
              Slider(
                value: _s.outputVolume.clamp(0.0, 2.0),
                min: 0,
                max: 2,
                label: '${(_s.outputVolume * 100).round()} %',
                divisions: 20,
                onChanged: (v) => setState(() => _s = _copy(volume: v)),
                onChangeEnd: (v) => _update(_copy(volume: v)),
              ),
              const Divider(height: 32),
              Text('Server: ${c.config.server}', style: theme.textTheme.bodySmall),
              const SizedBox(height: 8),
              Align(
                alignment: Alignment.centerLeft,
                child: TextButton.icon(
                  onPressed: _confirmLogout,
                  icon: const Icon(Icons.logout_rounded, color: kDanger),
                  label: const Text('Gerät abmelden', style: TextStyle(color: kDanger)),
                ),
              ),
            ],
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
