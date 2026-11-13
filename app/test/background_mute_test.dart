import 'package:flutter_test/flutter_test.dart';
import 'package:talkie/controller.dart';

void main() {
  BackgroundMuteAction act({bool enabled = true, bool fg = false, bool muted = false, bool auto = false}) =>
      backgroundMuteAction(enabled: enabled, foreground: fg, muted: muted, autoMuted: auto);

  test('Hintergrund: Mikro an -> stumm', () => expect(act(), BackgroundMuteAction.mute));
  test('Einstellung aus: nichts', () => expect(act(enabled: false), BackgroundMuteAction.none));
  test('schon selbst stumm: nichts, und bleibt später stumm', () {
    expect(act(muted: true), BackgroundMuteAction.none);
    expect(act(fg: true, muted: true), BackgroundMuteAction.none);
  });
  test('schon automatisch stumm: nicht doppelt', () => expect(act(muted: true, auto: true), BackgroundMuteAction.none));
  test('zurück vorne: automatisch stummes Mikro wieder an', () {
    expect(act(fg: true, muted: true, auto: true), BackgroundMuteAction.unmute);
  });
}
