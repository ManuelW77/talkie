import 'dart:typed_data';

import 'package:flutter_test/flutter_test.dart';
import 'package:image/image.dart' as img;
import 'package:talkie/avatar_image.dart';
import 'package:talkie/time_ago.dart';

void main() {
  group('lastSeenText', () {
    final now = DateTime(2026, 11, 14, 20, 0);
    int ts(DateTime t) => t.millisecondsSinceEpoch ~/ 1000;
    String at(Duration ago) => lastSeenText(ts(now.subtract(ago)), now: now);

    test('nie', () => expect(lastSeenText(0, now: now), 'noch nie'));
    test('gerade eben', () {
      expect(at(Duration.zero), 'gerade eben');
      expect(at(const Duration(seconds: 59)), 'gerade eben');
      expect(at(const Duration(seconds: -30)), 'gerade eben'); // Uhr geht leicht vor
    });
    test('Minuten', () {
      expect(at(const Duration(seconds: 60)), 'vor 1 Minute');
      expect(at(const Duration(minutes: 5)), 'vor 5 Minuten');
      expect(at(const Duration(minutes: 59, seconds: 59)), 'vor 59 Minuten');
    });
    test('Stunden am selben Tag', () {
      expect(at(const Duration(hours: 1)), 'vor 1 Stunde');
      expect(at(const Duration(hours: 3)), 'vor 3 Stunden');
    });
    test('gestern ist der Kalendertag, nicht 24 Stunden', () {
      // 20:00 Uhr: vor 21 Stunden war es 23:00 Uhr am Vortag
      expect(at(const Duration(hours: 21)), 'gestern');
      expect(lastSeenText(ts(DateTime(2026, 11, 13, 8)), now: now), 'gestern');
      expect(lastSeenText(ts(DateTime(2026, 11, 13, 0, 1)), now: now), 'gestern');
    });
    test('Tage', () {
      expect(lastSeenText(ts(DateTime(2026, 11, 12, 12)), now: now), 'vor 2 Tagen');
      expect(lastSeenText(ts(DateTime(2026, 11, 10, 12)), now: now), 'vor 4 Tagen');
      expect(lastSeenText(ts(DateTime(2026, 11, 8, 12)), now: now), 'vor 6 Tagen');
    });
    test('ab einer Woche mit Datum', () {
      expect(lastSeenText(ts(DateTime(2026, 11, 7, 12)), now: now), 'am 07.11.2026');
      expect(lastSeenText(ts(DateTime(2025, 3, 1, 12)), now: now), 'am 01.03.2025');
    });
  });

  group('prepareAvatarSync', () {
    Uint8List png(int w, int h) {
      final im = img.Image(width: w, height: h);
      for (final p in im) {
        p
          ..r = (p.x * 7) % 256
          ..g = (p.y * 13) % 256
          ..b = (p.x * p.y) % 256;
      }
      return Uint8List.fromList(img.encodePng(im));
    }

    test('macht genau 128×128 und höchstens 24 KiB, auch aus Querformat', () {
      final out = prepareAvatarSync(png(1200, 800))!;
      expect(out.length, lessThanOrEqualTo(kAvatarMaxBytes));
      final back = img.decodeJpg(out)!;
      expect((back.width, back.height), (128, 128));
    });

    test('Hochformat und winzige Bilder', () {
      for (final (w, h) in [(300, 900), (16, 16), (1, 1)]) {
        final back = img.decodeJpg(prepareAvatarSync(png(w, h))!)!;
        expect((back.width, back.height), (128, 128));
      }
    });

    test('Müll ergibt null', () {
      expect(prepareAvatarSync(Uint8List.fromList([1, 2, 3, 4])), isNull);
      expect(prepareAvatarSync(Uint8List(0)), isNull);
    });
  });

  testWidgets('prepareAvatar: nativer Weg macht aus 300×200 ein 128×128-JPEG', (tester) async {
    final im = img.Image(width: 300, height: 200);
    for (final p in im) {
      p
        ..r = p.x % 256
        ..g = p.y % 256
        ..b = 100;
    }
    final out = await tester.runAsync(() => prepareAvatar(Uint8List.fromList(img.encodePng(im))));
    expect(out, isNotNull);
    expect(out!.length, lessThanOrEqualTo(kAvatarMaxBytes));
    final back = img.decodeJpg(out)!;
    expect((back.width, back.height), (128, 128));
    final junk = await tester.runAsync(() => prepareAvatar(Uint8List.fromList([1, 2, 3])));
    expect(junk, isNull);
  });
}
