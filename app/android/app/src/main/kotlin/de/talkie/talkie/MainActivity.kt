package de.talkie.talkie

import android.Manifest
import android.content.Intent
import android.content.pm.PackageManager
import android.os.Build
import io.flutter.embedding.android.FlutterActivity
import io.flutter.embedding.engine.FlutterEngine
import io.flutter.plugin.common.MethodChannel

class MainActivity : FlutterActivity() {
    private var micResult: MethodChannel.Result? = null

    override fun configureFlutterEngine(flutterEngine: FlutterEngine) {
        super.configureFlutterEngine(flutterEngine)
        TalkieNative.initAudio(applicationContext)
        MethodChannel(flutterEngine.dartExecutor.binaryMessenger, "talkie/platform").setMethodCallHandler { call, result ->
            when (call.method) {
                "dataDir" -> result.success(filesDir.absolutePath)
                "requestMic" -> requestMic(result)
                "setActive" -> {
                    setActive(call.arguments as? Boolean ?: false)
                    result.success(null)
                }
                else -> result.notImplemented()
            }
        }
    }

    private fun requestMic(result: MethodChannel.Result) {
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) == PackageManager.PERMISSION_GRANTED) {
            result.success(true)
            return
        }
        micResult?.success(false)
        micResult = result
        val perms = mutableListOf(Manifest.permission.RECORD_AUDIO)
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
            perms.add(Manifest.permission.POST_NOTIFICATIONS)
        }
        requestPermissions(perms.toTypedArray(), REQ_MIC)
    }

    override fun onRequestPermissionsResult(requestCode: Int, permissions: Array<out String>, grantResults: IntArray) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode != REQ_MIC) return
        val i = permissions.indexOf(Manifest.permission.RECORD_AUDIO)
        micResult?.success(i >= 0 && grantResults.getOrNull(i) == PackageManager.PERMISSION_GRANTED)
        micResult = null
    }

    private fun setActive(on: Boolean) {
        val intent = Intent(this, VoiceService::class.java)
        if (!on) {
            stopService(intent)
            return
        }
        // Ohne Mikrofon-Erlaubnis lehnt Android 14+ den Dienst ab
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) return
        try {
            startForegroundService(intent)
        } catch (e: Exception) {
            android.util.Log.w("Talkie", "Dienst nicht gestartet: $e")
        }
    }

    companion object {
        private const val REQ_MIC = 7
    }
}
