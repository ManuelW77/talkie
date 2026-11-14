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
  final _scroll = ScrollController();

  @override
  void initState() {
    super.initState();
    _loadDevices();
    if (widget.controller.status == ConnStatus.connected) {
      widget.controller.requestDevices();
    }
  }

  @override
  void dispose() {
    _scroll.dispose();
    super.dispose();
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
    bool? echoCancel,
    bool? muteInBackground,
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
    echoCancel: echoCancel ?? _s.echoCancel,
    muteInBackground: muteInBackground ?? _s.muteInBackground,
  );

  Widget _pttSection(ThemeData theme) {
    final hint = theme.textTheme.bodySmall?.copyWith(
      color: theme.colorScheme.onSurfaceVariant,
    );
    final access = TalkiePlatform.isMobile ? 'unsupported' : _pttAccess;
    return Column(
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        Text('Sprechen', style: theme.textTheme.titleSmall),
        const SizedBox(height: 8),
        // Auf schmalen Handys ohne Symbole, damit „Automatisch“ nicht mitten im Wort umbricht
        LayoutBuilder(
          builder: (context, box) {
            final icons = box.maxWidth >= 400;
            Widget label(String t) => FittedBox(
              fit: BoxFit.scaleDown,
              child: Text(t, maxLines: 1, softWrap: false),
            );
            return SegmentedButton<bool>(
              showSelectedIcon: false,
              segments: [
                ButtonSegment(
                  value: false,
                  label: label('Automatisch'),
                  icon: icons ? const Icon(Icons.graphic_eq_rounded) : null,
                ),
                ButtonSegment(
                  value: true,
                  label: label('Taste halten'),
                  icon: icons ? const Icon(Icons.touch_app_rounded) : null,
                ),
              ],
              selected: {_s.ptt},
              onSelectionChanged: (v) => _update(_copy(ptt: v.first)),
            );
          },
        ),
        const SizedBox(height: 6),
        Text(
          _s.ptt
              ? 'Du wirst nur gehört, solange du die Taste oder unten den Knopf „Halten“ gedrückt hältst.'
              : 'Talkie sendet von selbst, sobald du sprichst.',
          style: hint,
          textAlign: TextAlign.justify,
        ),
        if (_s.ptt && access != 'unsupported') ...[
          const SizedBox(height: 12),
          DropdownButtonFormField<String>(
            initialValue: _pttKeys.any((k) => k.id == _s.pttKey)
                ? _s.pttKey
                : null,
            decoration: const InputDecoration(
              labelText: 'Sprechtaste',
              prefixIcon: Icon(Icons.keyboard_rounded),
            ),
            items: [
              for (final k in _pttKeys)
                DropdownMenuItem(value: k.id, child: Text(k.label)),
            ],
            onChanged: (v) {
              if (v != null) _update(_copy(pttKey: v));
            },
          ),
          const SizedBox(height: 4),
          Text(
            'Funktioniert auch, während ein Spiel im Vordergrund ist.',
            style: hint,
            textAlign: TextAlign.justify,
          ),
          if (access == 'permission') ...[
            const SizedBox(height: 8),
            Row(
              children: [
                const Icon(
                  Icons.warning_amber_rounded,
                  color: kDanger,
                  size: 20,
                ),
                const SizedBox(width: 8),
                const Expanded(
                  child: Text(
                    textAlign: TextAlign.justify,
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

  /// Code für ein weiteres Gerät (z. B. iPhone zum Laptop) anzeigen.
  /// Erst fragt ein Feld nach dem Namen des Geräts, dann kommt der Code.
  Future<void> _showDeviceCode() async {
    final c = widget.controller;
    final nameCtrl = TextEditingController();
    var asked = false;
    await showDialog<void>(
      context: context,
      builder: (ctx) => StatefulBuilder(
        builder: (ctx, setLocal) => AlertDialog(
          title: const Text('Auf weiterem Gerät nutzen'),
          content: SizedBox(
            width: 380,
            child: asked ? _deviceCodeBody(ctx, c) : _deviceNameForm(nameCtrl),
          ),
          actions: [
            if (!asked)
              TextButton(
                onPressed: () => Navigator.pop(ctx),
                child: const Text('Abbrechen'),
              ),
            FilledButton(
              onPressed: asked
                  ? () => Navigator.pop(ctx)
                  : () {
                      c.requestDeviceCode(nameCtrl.text.trim());
                      setLocal(() => asked = true);
                    },
              child: Text(asked ? 'Fertig' : 'Code holen'),
            ),
          ],
        ),
      ),
    );
    nameCtrl.dispose();
    c.requestDevices();
  }

  Widget _deviceNameForm(TextEditingController ctrl) => Column(
    mainAxisSize: MainAxisSize.min,
    crossAxisAlignment: CrossAxisAlignment.stretch,
    children: [
      const Text(
        'Gib dem neuen Gerät einen Namen, dann erkennst du es später in der Liste wieder.',
        textAlign: TextAlign.justify,
      ),
      const SizedBox(height: 12),
      TextField(
        controller: ctrl,
        maxLength: 40,
        autofocus: true,
        decoration: const InputDecoration(
          labelText: 'Wie heißt das Gerät? (z. B. Mein iPhone)',
          prefixIcon: Icon(Icons.edit_rounded),
        ),
      ),
    ],
  );

  Widget _deviceCodeBody(
    BuildContext ctx,
    TalkieController c,
  ) => ValueListenableBuilder<String?>(
    valueListenable: c.deviceCode,
    builder: (ctx, code, _) => Column(
      mainAxisSize: MainAxisSize.min,
      crossAxisAlignment: CrossAxisAlignment.stretch,
      children: [
        const Text(
          'Öffne Talkie auf dem anderen Gerät, tippe auf „Ich nutze Talkie schon auf einem anderen Gerät“ '
          'und gib diesen Code ein:',
          textAlign: TextAlign.justify,
        ),
        const SizedBox(height: 16),
        if (code == null)
          // Alte Server kennen Geräte-Codes nicht und antworten gar nicht
          FutureBuilder<void>(
            future: Future<void>.delayed(const Duration(seconds: 6)),
            builder: (ctx, snap) => snap.connectionState == ConnectionState.done
                ? const Text(
                    'Der Server antwortet nicht. Vermutlich läuft dort noch eine ältere Talkie-Version '
                    'ohne Geräte-Codes – bitte den Server aktualisieren.',
                    textAlign: TextAlign.center,
                    style: TextStyle(color: kDanger),
                  )
                : const Center(child: CircularProgressIndicator()),
          )
        else
          SelectableText(
            code,
            textAlign: TextAlign.center,
            style: Theme.of(ctx).textTheme.headlineMedium?.copyWith(
              letterSpacing: 4,
              fontWeight: FontWeight.w700,
              color: kAccent,
            ),
          ),
        const SizedBox(height: 16),
        Text(
          'Der Code gilt 15 Minuten und nur einmal. Verbunden ist immer nur ein Gerät gleichzeitig – '
          'meldest du dich auf dem neuen an, wird dieses getrennt.',
          style: Theme.of(ctx).textTheme.bodySmall,
        ),
      ],
    ),
  );

  static String _two(int n) => n.toString().padLeft(2, '0');

  static String _clock(DateTime d) => '${_two(d.hour)}:${_two(d.minute)}';

  static String _dateTime(DateTime d) =>
      '${_two(d.day)}.${_two(d.month)}.${d.year}, ${_clock(d)} Uhr';

  /// „heute, 19:50 Uhr“ oder „14.11.2026“; 0 = noch nie.
  static String _lastSeen(int secs) {
    if (secs <= 0) return 'noch nie';
    final d = DateTime.fromMillisecondsSinceEpoch(secs * 1000);
    final now = DateTime.now();
    if (d.year == now.year && d.month == now.month && d.day == now.day) {
      return 'heute, ${_clock(d)} Uhr';
    }
    return '${_two(d.day)}.${_two(d.month)}.${d.year}';
  }

  static IconData _deviceIcon(String label) {
    final l = label.toLowerCase();
    const phones = [
      'iphone',
      'ipad',
      'ios',
      'android',
      'handy',
      'tablet',
      'phone',
    ];
    const computers = [
      'mac',
      'windows',
      'linux',
      'pc',
      'laptop',
      'rechner',
      'computer',
    ];
    if (phones.any(l.contains)) return Icons.smartphone_rounded;
    if (computers.any(l.contains)) return Icons.computer_rounded;
    return Icons.devices_other_rounded;
  }

  Future<void> _renameDevice(DeviceDto d) async {
    final ctrl = TextEditingController(text: d.label);
    final name = await showDialog<String>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: const Text('Gerät umbenennen'),
        content: TextField(
          controller: ctrl,
          maxLength: 40,
          autofocus: true,
          decoration: const InputDecoration(labelText: 'Name (z. B. Mein Mac)'),
          onSubmitted: (v) => Navigator.pop(ctx, v.trim()),
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx),
            child: const Text('Abbrechen'),
          ),
          FilledButton(
            onPressed: () => Navigator.pop(ctx, ctrl.text.trim()),
            child: const Text('Speichern'),
          ),
        ],
      ),
    );
    ctrl.dispose();
    if (name != null) widget.controller.renameDevice(d.id, name);
  }

  Future<void> _confirmDeleteDevice(DeviceDto d) async {
    final ok = await showDialog<bool>(
      context: context,
      builder: (ctx) => AlertDialog(
        title: Text(
          '„${d.label.isEmpty ? 'Unbekanntes Gerät' : d.label}“ entfernen?',
        ),
        content: const Text(
          'Dieses Gerät kann sich danach nicht mehr anmelden.',
        ),
        actions: [
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Abbrechen'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: kDanger,
              minimumSize: const Size(0, 40),
            ),
            onPressed: () => Navigator.pop(ctx, true),
            child: const Text('Entfernen'),
          ),
        ],
      ),
    );
    if (ok == true) widget.controller.deleteDevice(d.id);
  }

  /// Offener Code und Liste der Geräte (nichts, solange der Server nicht geantwortet hat).
  Widget _devicesSection(ThemeData theme) {
    final c = widget.controller;
    return ValueListenableBuilder<DevicesInfo?>(
      valueListenable: c.devices,
      builder: (ctx, info, _) {
        if (info == null || c.status != ConnStatus.connected) {
          return const SizedBox.shrink();
        }
        final exp = DateTime.fromMillisecondsSinceEpoch(info.expires * 1000);
        final soon = exp.difference(DateTime.now()) < const Duration(days: 1);
        return Column(
          crossAxisAlignment: CrossAxisAlignment.stretch,
          children: [
            if (info.code.isNotEmpty)
              ListTile(
                contentPadding: EdgeInsets.zero,
                leading: const Icon(Icons.vpn_key_rounded, color: kAccent),
                title: Text('Offener Code ${info.code}'),
                subtitle: Text(
                  '${info.codeName.isEmpty ? '' : '${info.codeName} · '}'
                  'gültig bis ${soon ? _clock(exp) : _dateTime(exp)}',
                ),
                trailing: IconButton(
                  tooltip: 'Code löschen',
                  icon: const Icon(
                    Icons.delete_outline_rounded,
                    color: kDanger,
                  ),
                  onPressed: c.deleteDeviceCode,
                ),
              ),
            if (info.devices.isNotEmpty) ...[
              const SizedBox(height: 8),
              Text('Deine Geräte', style: theme.textTheme.titleSmall),
              for (final d in info.devices)
                ListTile(
                  contentPadding: EdgeInsets.zero,
                  leading: Icon(_deviceIcon(d.label)),
                  title: Text(d.label.isEmpty ? 'Unbekanntes Gerät' : d.label),
                  subtitle: Text(
                    '${d.thisDevice ? 'Dieses Gerät · ' : ''}'
                    'Zuletzt benutzt: ${d.thisDevice ? 'jetzt' : _lastSeen(d.lastSeen)}',
                  ),
                  trailing: Row(
                    mainAxisSize: MainAxisSize.min,
                    children: [
                      IconButton(
                        tooltip: 'Umbenennen',
                        icon: const Icon(Icons.edit_rounded),
                        onPressed: () => _renameDevice(d),
                      ),
                      if (!d.thisDevice)
                        IconButton(
                          tooltip: 'Gerät entfernen',
                          icon: const Icon(
                            Icons.delete_outline_rounded,
                            color: kDanger,
                          ),
                          onPressed: () => _confirmDeleteDevice(d),
                        ),
                    ],
                  ),
                ),
            ],
          ],
        );
      },
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
          TextButton(
            onPressed: () => Navigator.pop(ctx, false),
            child: const Text('Abbrechen'),
          ),
          FilledButton(
            style: FilledButton.styleFrom(
              backgroundColor: kDanger,
              minimumSize: const Size(0, 40),
            ),
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
        // Fester Scrollbalken, damit man sieht, dass es weiter unten weitergeht
        child: Scrollbar(
          controller: _scroll,
          thumbVisibility: true,
          trackVisibility: true,
          child: SingleChildScrollView(
            controller: _scroll,
            padding: const EdgeInsets.only(right: 16),
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
                const SizedBox(height: 12),
                // Gehört zum Sprechen: leicht orange hervorgehoben
                Material(
                  color: Colors.orange.withValues(alpha: 0.14),
                  shape: RoundedRectangleBorder(
                    borderRadius: BorderRadius.circular(12),
                    side: BorderSide(
                      color: Colors.orange.withValues(alpha: 0.35),
                    ),
                  ),
                  clipBehavior: Clip.antiAlias,
                  child: Column(
                    crossAxisAlignment: CrossAxisAlignment.stretch,
                    children: [
                      SwitchListTile(
                        contentPadding: const EdgeInsets.symmetric(
                          horizontal: 12,
                        ),
                        title: const Text(
                          'Talkie im Hintergrund stumm schalten',
                        ),
                        subtitle: const Text(
                          'Dein Mikro geht aus, solange Talkie nicht vorne ist.',
                          textAlign: TextAlign.justify,
                        ),
                        value: _s.muteInBackground,
                        onChanged: (v) => _update(_copy(muteInBackground: v)),
                      ),
                      if (_s.muteInBackground)
                        Padding(
                          padding: const EdgeInsets.fromLTRB(12, 0, 12, 12),
                          child: Row(
                            children: [
                              const Icon(
                                Icons.mic_off_rounded,
                                color: kDanger,
                                size: 18,
                              ),
                              const SizedBox(width: 8),
                              Expanded(
                                child: Text(
                                  'Du wirst nicht gehört, wenn die App im Hintergrund ist.',
                                  style: theme.textTheme.bodyMedium?.copyWith(
                                    color: kDanger,
                                    fontWeight: FontWeight.w600,
                                  ),
                                ),
                              ),
                            ],
                          ),
                        ),
                    ],
                  ),
                ),
                const Divider(height: 32),
                if (!_s.ptt) ...[
                  Text('Empfindlichkeit', style: theme.textTheme.titleSmall),
                  Text(
                    'Sprich normal: Der Balken muss über den weißen Strich kommen. '
                    'Schiebe nach links, wenn du nicht gehört wirst, nach rechts, wenn Tastatur & Co. mitgesendet werden.',
                    style: theme.textTheme.bodySmall?.copyWith(
                      color: theme.colorScheme.onSurfaceVariant,
                    ),
                    textAlign: TextAlign.justify,
                  ),
                  const SizedBox(height: 10),
                  ValueListenableBuilder<double>(
                    valueListenable: c.levelNotifier,
                    builder: (_, v, _) => LevelMeter(
                      level: v,
                      threshold: (_s.vadThresholdDb + 60) / 60,
                      height: 12,
                    ),
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
                // Handy: Echo-Unterdrückung des Systems (ohne Kopfhörer wichtig)
                if (TalkiePlatform.isMobile)
                  SwitchListTile(
                    contentPadding: EdgeInsets.zero,
                    title: const Text('Echo-Unterdrückung'),
                    subtitle: const Text(
                      'Damit die anderen sich nicht selbst hören, wenn du ohne Kopfhörer sprichst',
                      textAlign: TextAlign.justify,
                    ),
                    value: _s.echoCancel,
                    onChanged: (v) => _update(_copy(echoCancel: v)),
                  ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Rauschfilter'),
                  subtitle: const Text(
                    'Filtert Lüfter, Tastatur und Hintergrundgeräusche',
                    textAlign: TextAlign.justify,
                  ),
                  value: _s.noiseSuppression,
                  onChanged: (v) => _update(_copy(ns: v)),
                ),
                SwitchListTile(
                  contentPadding: EdgeInsets.zero,
                  title: const Text('Töne'),
                  subtitle: const Text(
                    'Kurzer Ton, wenn jemand kommt oder geht und bei neuen Chatnachrichten',
                    textAlign: TextAlign.justify,
                  ),
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
                Text(
                  'Lautstärke der anderen',
                  style: theme.textTheme.titleSmall,
                ),
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
                Text('Weiteres Gerät', style: theme.textTheme.titleSmall),
                const SizedBox(height: 4),
                Text(
                  c.status == ConnStatus.connected
                      ? 'Du willst Talkie auch auf dem Handy, Tablet oder einem '
                            'zweiten Rechner nutzen? Hol dir hier einen Code und gib '
                            'ihn auf dem anderen Gerät ein. Dort bist du dann auch du.'
                      : 'Du willst Talkie auch auf einem anderen Gerät nutzen? '
                            'Das geht, sobald Talkie verbunden ist.',
                  style: theme.textTheme.bodyMedium,
                  textAlign: TextAlign.justify,
                ),
                const SizedBox(height: 12),
                FilledButton.tonalIcon(
                  onPressed: c.status == ConnStatus.connected
                      ? _showDeviceCode
                      : null,
                  style: FilledButton.styleFrom(
                    minimumSize: const Size.fromHeight(48),
                  ),
                  icon: const Icon(Icons.devices_rounded),
                  label: const Text('Code für weiteres Gerät holen'),
                ),
                const SizedBox(height: 8),
                _devicesSection(theme),
                const SizedBox(height: 16),
                Text(
                  'Server: ${c.config.server}',
                  style: theme.textTheme.bodySmall,
                ),
                const SizedBox(height: 12),
                FilledButton.icon(
                  onPressed: _confirmLogout,
                  style: FilledButton.styleFrom(
                    backgroundColor: kDanger,
                    foregroundColor: Colors.white,
                    minimumSize: const Size.fromHeight(48),
                  ),
                  icon: const Icon(Icons.logout_rounded),
                  label: const Text('Vom Server abmelden'),
                ),
              ],
            ),
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
