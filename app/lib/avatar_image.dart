import 'dart:ui' as ui;

import 'package:flutter/foundation.dart';
import 'package:image/image.dart' as img;

/// Kantenlänge des Profilbilds (der Server verlangt genau 128×128).
const int kAvatarSize = 128;

/// So groß darf das fertige JPEG höchstens sein (Server: 24 KiB).
const int kAvatarMaxBytes = 24 * 1024;

/// Mittig quadratisch zuschneiden, auf 128×128 verkleinern und als JPEG kodieren (Qualität stufenweise).
Uint8List? _squareJpeg(img.Image image) {
  final side = image.width < image.height ? image.width : image.height;
  final square = img.copyCrop(
    image,
    x: (image.width - side) ~/ 2,
    y: (image.height - side) ~/ 2,
    width: side,
    height: side,
  );
  final small = img.copyResize(
    square,
    width: kAvatarSize,
    height: kAvatarSize,
    interpolation: img.Interpolation.average,
  );
  for (final quality in const [80, 70, 60, 50, 40, 30]) {
    final jpg = Uint8List.fromList(img.encodeJpg(small, quality: quality));
    if (jpg.length <= kAvatarMaxBytes) return jpg;
  }
  return null;
}

/// Macht aus einem beliebigen Bild (JPEG, PNG, GIF, ...) ein 128×128-JPEG:
/// mittig quadratisch zuschneiden, verkleinern, JPEG-Qualität 80 und bei Bedarf stufenweise weniger.
/// Gibt null zurück, wenn das Bild nicht gelesen werden kann.
/// Rechenintensiv – im Alltag [prepareAvatar] nehmen (läuft im Hintergrund).
Uint8List? prepareAvatarSync(Uint8List input) {
  try {
    var image = img.decodeImage(input);
    if (image == null || image.width < 1 || image.height < 1) return null;
    image = img.bakeOrientation(image); // Handyfotos stehen oft „gedreht“ in den Metadaten
    return _squareJpeg(image);
  } catch (_) {
    // kaputte oder nicht unterstützte Datei
  }
  return null;
}

/// Schneidet rohe RGBA-Pixel mittig quadratisch zu und macht daraus das 128×128-JPEG (null = zu groß/kaputt).
Uint8List? _cropAndEncode((Uint8List, int, int) a) {
  try {
    final (rgba, w, h) = a;
    final image = img.Image.fromBytes(width: w, height: h, bytes: rgba.buffer, numChannels: 4);
    return _squareJpeg(image);
  } catch (_) {
    return null;
  }
}

/// Macht aus einem Foto das Profilbild. Dekodiert zuerst mit dem Dekoder des Systems (schnell, kann auch
/// HEIC und wendet die EXIF-Drehung selbst an) und gleich auf ~128 px verkleinert; klappt das nicht,
/// Rückfall auf das reine Dart-Paket im Hintergrund. Null = Bild nicht lesbar.
Future<Uint8List?> prepareAvatar(Uint8List input) async {
  try {
    final buffer = await ui.ImmutableBuffer.fromUint8List(input);
    ui.Codec? codec;
    ui.Image? image;
    try {
      codec = await ui.instantiateImageCodecWithSize(
        buffer,
        getTargetSize: (w, h) {
          final shorter = w < h ? w : h;
          if (shorter <= kAvatarSize) return ui.TargetImageSize(width: w, height: h);
          final k = kAvatarSize / shorter;
          return ui.TargetImageSize(width: (w * k).round(), height: (h * k).round());
        },
      );
      image = (await codec.getNextFrame()).image;
      final data = await image.toByteData(format: ui.ImageByteFormat.rawRgba);
      if (data == null) throw StateError('keine Pixel');
      final rgba = data.buffer.asUint8List(data.offsetInBytes, data.lengthInBytes);
      return await compute(_cropAndEncode, (rgba, image.width, image.height));
    } finally {
      image?.dispose();
      codec?.dispose();
      buffer.dispose();
    }
  } catch (_) {
    return compute(prepareAvatarSync, input);
  }
}
