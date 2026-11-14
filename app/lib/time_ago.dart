/// „vor 5 Minuten“, „gestern“, „am 03.11.2026“ … für die Mitgliederliste.
/// [lastSeen] in Unix-Sekunden, 0 = noch nie. [now] ist nur für Tests austauschbar.
String lastSeenText(int lastSeen, {DateTime? now}) {
  if (lastSeen <= 0) return 'noch nie';
  final n = now ?? DateTime.now();
  final t = DateTime.fromMillisecondsSinceEpoch(lastSeen * 1000);
  final diff = n.difference(t);
  if (diff.inSeconds < 60) return 'gerade eben'; // auch bei leicht falsch gehender Uhr
  if (diff.inMinutes < 60) {
    final m = diff.inMinutes;
    return m == 1 ? 'vor 1 Minute' : 'vor $m Minuten';
  }
  // Kalendertage (nicht 24-Stunden-Blöcke), damit „gestern“ wirklich gestern heißt
  final days = DateTime.utc(n.year, n.month, n.day).difference(DateTime.utc(t.year, t.month, t.day)).inDays;
  if (days <= 0) {
    final h = diff.inHours;
    return h == 1 ? 'vor 1 Stunde' : 'vor $h Stunden';
  }
  if (days == 1) return 'gestern';
  if (days < 7) return 'vor $days Tagen';
  String two(int v) => v.toString().padLeft(2, '0');
  return 'am ${two(t.day)}.${two(t.month)}.${t.year}';
}
