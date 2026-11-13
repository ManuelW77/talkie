/// Version dieser App. Muss zu `version:` in pubspec.yaml passen (prüft test/version_test.dart).
/// Release-Tags heißen genauso: v1.0.0.
const String kAppVersion = '1.0.1';

/// true, wenn [latest] neuer ist als [current] (Versionen wie „1.2.3“).
bool isNewerVersion(String latest, String current) {
  List<int> parse(String v) => v.split('.').map((p) => int.tryParse(p) ?? 0).toList();
  final a = parse(latest), b = parse(current);
  for (var i = 0; i < a.length || i < b.length; i++) {
    final x = i < a.length ? a[i] : 0, y = i < b.length ? b[i] : 0;
    if (x != y) return x > y;
  }
  return false;
}
