import 'package:flutter/material.dart';

import 'controller.dart';
import 'platform.dart';
import 'screens/main_screen.dart';
import 'screens/setup_screen.dart';
import 'src/rust/api/talkie.dart';
import 'src/rust/frb_generated.dart';
import 'theme.dart';

Future<void> main() async {
  WidgetsFlutterBinding.ensureInitialized();
  await RustLib.init();
  final config = await talkieInit(dataDir: await TalkiePlatform.dataDir());
  final controller = TalkieController(config)..start();
  runApp(TalkieApp(controller: controller));
  // Erst fragen, wenn die App sichtbar ist (iOS zeigt die Abfrage sonst evtl. nicht)
  WidgetsBinding.instance.addPostFrameCallback((_) => controller.checkMic(ask: true));
}

class TalkieApp extends StatelessWidget {
  const TalkieApp({super.key, required this.controller});

  final TalkieController controller;

  @override
  Widget build(BuildContext context) {
    return MaterialApp(
      title: 'Talkie',
      debugShowCheckedModeBanner: false,
      theme: buildTheme(Brightness.light),
      darkTheme: buildTheme(Brightness.dark),
      themeMode: ThemeMode.dark,
      home: ListenableBuilder(
        listenable: controller,
        builder: (context, _) => AnimatedSwitcher(
          duration: const Duration(milliseconds: 250),
          child: controller.showSetup
              ? SetupScreen(key: const ValueKey('setup'), controller: controller)
              : MainScreen(key: const ValueKey('main'), controller: controller),
        ),
      ),
    );
  }
}
