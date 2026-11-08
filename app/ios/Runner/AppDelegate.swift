import AVFoundation
import Flutter
import UIKit

@main
@objc class AppDelegate: FlutterAppDelegate, FlutterImplicitEngineDelegate {
  override func application(
    _ application: UIApplication,
    didFinishLaunchingWithOptions launchOptions: [UIApplication.LaunchOptionsKey: Any]?
  ) -> Bool {
    TalkieAudio.configure()
    return super.application(application, didFinishLaunchingWithOptions: launchOptions)
  }

  func didInitializeImplicitFlutterEngine(_ engineBridge: FlutterImplicitEngineBridge) {
    GeneratedPluginRegistrant.register(with: engineBridge.pluginRegistry)
    guard let registrar = engineBridge.pluginRegistry.registrar(forPlugin: "TalkiePlatform") else { return }
    let channel = FlutterMethodChannel(name: "talkie/platform", binaryMessenger: registrar.messenger())
    channel.setMethodCallHandler { call, result in
      switch call.method {
      case "dataDir":
        result(TalkieAudio.dataDir())
      case "requestMic":
        TalkieAudio.requestMic { ok in DispatchQueue.main.async { result(ok) } }
      case "micStatus":
        result(TalkieAudio.micStatus())
      case "openSettings":
        if let url = URL(string: UIApplication.openSettingsURLString) {
          UIApplication.shared.open(url)
        }
        result(nil)
      case "setActive":
        TalkieAudio.setActive(call.arguments as? Bool ?? false)
        result(nil)
      default:
        result(FlutterMethodNotImplemented)
      }
    }
  }
}

/// Audio-Sitzung und Ablage für Talkie.
enum TalkieAudio {
  /// Sprechen und Hören gleichzeitig, Lautsprecher statt Hörmuschel, Bluetooth-Headsets erlaubt.
  /// mixWithOthers: Spiel- und Musik-Ton laufen weiter.
  static func configure() {
    let s = AVAudioSession.sharedInstance()
    do {
      try s.setCategory(.playAndRecord, mode: .default,
                        options: [.defaultToSpeaker, .allowBluetooth, .allowBluetoothA2DP, .mixWithOthers])
      try s.setPreferredSampleRate(48_000)
    } catch {
      NSLog("Talkie: Audio-Sitzung: \(error)")
    }
  }

  static func setActive(_ on: Bool) {
    do {
      if on { configure() }
      try AVAudioSession.sharedInstance().setActive(on, options: on ? [] : [.notifyOthersOnDeactivation])
    } catch {
      NSLog("Talkie: Audio-Sitzung \(on ? "an" : "aus"): \(error)")
    }
  }

  /// "granted", "denied" oder "undetermined"
  static func micStatus() -> String {
    if #available(iOS 17.0, *) {
      switch AVAudioApplication.shared.recordPermission {
      case .granted: return "granted"
      case .denied: return "denied"
      default: return "undetermined"
      }
    }
    switch AVAudioSession.sharedInstance().recordPermission {
    case .granted: return "granted"
    case .denied: return "denied"
    default: return "undetermined"
    }
  }

  static func requestMic(_ done: @escaping (Bool) -> Void) {
    if #available(iOS 17.0, *) {
      AVAudioApplication.requestRecordPermission(completionHandler: done)
    } else {
      AVAudioSession.sharedInstance().requestRecordPermission(done)
    }
  }

  /// Application Support/Talkie – wird gesichert, aber nicht im Dateien-App angezeigt.
  static func dataDir() -> String {
    let fm = FileManager.default
    let base = (try? fm.url(for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true))
      ?? fm.temporaryDirectory
    let dir = base.appendingPathComponent("Talkie", isDirectory: true)
    try? fm.createDirectory(at: dir, withIntermediateDirectories: true)
    return dir.path
  }
}
