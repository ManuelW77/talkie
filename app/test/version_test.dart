import 'dart:io';

import 'package:flutter_test/flutter_test.dart';
import 'package:talkie/version.dart';

void main() {
  test('kAppVersion passt zur pubspec.yaml', () {
    final m = RegExp(r'^version:\s*([0-9.]+)', multiLine: true).firstMatch(File('pubspec.yaml').readAsStringSync());
    expect(kAppVersion, m!.group(1));
  });

  test('Versionen vergleichen', () {
    expect(isNewerVersion('1.0.1', '1.0.0'), isTrue);
    expect(isNewerVersion('1.10.0', '1.9.9'), isTrue);
    expect(isNewerVersion('2.0', '1.9.9'), isTrue);
    expect(isNewerVersion('1.0.0', '1.0.0'), isFalse);
    expect(isNewerVersion('0.9.0', '1.0.0'), isFalse);
    expect(isNewerVersion('1.0', '1.0.0'), isFalse);
  });
}
