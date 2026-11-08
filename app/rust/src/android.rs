//! Android: cpal (AAudio) fragt über JNI Systemwerte ab und braucht dafür den
//! Java-Kontext der App. MainActivity ruft `TalkieNative.initAudio(context)` einmal auf.

use std::sync::Once;

static INIT: Once = Once::new();

/// # Safety
/// Wird nur von der JVM mit gültigem `env` und `context` aufgerufen.
#[no_mangle]
pub unsafe extern "system" fn Java_de_talkie_talkie_TalkieNative_initAudio(
    env: *mut jni_sys::JNIEnv,
    _class: jni_sys::jclass,
    context: jni_sys::jobject,
) {
    INIT.call_once(|| {
        let f = &**env;
        let (Some(get_vm), Some(new_global)) = (f.GetJavaVM, f.NewGlobalRef) else {
            return;
        };
        let mut vm: *mut jni_sys::JavaVM = std::ptr::null_mut();
        if get_vm(env, &mut vm) != 0 || vm.is_null() {
            return;
        }
        // Globale Referenz: der Kontext lebt so lange wie der Prozess
        let ctx = new_global(env, context);
        ndk_context::initialize_android_context(vm.cast(), ctx.cast());
    });
}
