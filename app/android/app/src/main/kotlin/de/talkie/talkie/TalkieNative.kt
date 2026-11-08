package de.talkie.talkie

import android.content.Context

/** Verbindung zum Rust-Kern (librust_lib_talkie.so). */
object TalkieNative {
    init {
        System.loadLibrary("rust_lib_talkie")
    }

    /** Gibt dem Audio-Kern den App-Kontext (braucht cpal/AAudio). Einmal beim Start. */
    @JvmStatic
    external fun initAudio(context: Context)
}
