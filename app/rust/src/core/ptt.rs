//! Push-to-Talk: fragt systemweit ab, ob die gewählte Taste gerade gedrückt ist – auch wenn
//! ein Spiel im Vordergrund ist. Abfrage statt Tastatur-Hook: liest nur den Zustand einer Taste,
//! keine Texteingaben.
//!
//! - Windows: GetAsyncKeyState
//! - macOS: CGEventSourceKeyState / CGEventSourceButtonState (braucht „Eingabeüberwachung“)
//! - Linux (X11): XQueryKeymap über libX11 (zur Laufzeit geladen); unter reinem Wayland nicht möglich
//! - Handy: keine Taste, nur der Knopf in der App

/// Wählbare Tasten: (ID für die Einstellungen, Anzeige).
pub const KEYS: &[(&str, &str)] = &[
    ("f1", "F1"),
    ("f2", "F2"),
    ("f3", "F3"),
    ("f4", "F4"),
    ("f5", "F5"),
    ("f6", "F6"),
    ("f7", "F7"),
    ("f8", "F8"),
    ("f9", "F9"),
    ("f10", "F10"),
    ("f11", "F11"),
    ("f12", "F12"),
    ("ctrl_l", "Strg links"),
    ("ctrl_r", "Strg rechts"),
    ("alt_l", "Alt links"),
    ("alt_r", "Alt rechts / Alt Gr"),
    ("shift_l", "Umschalt links"),
    ("shift_r", "Umschalt rechts"),
    ("mouse3", "Mittlere Maustaste"),
    ("mouse4", "Maus-Seitentaste hinten"),
    ("mouse5", "Maus-Seitentaste vorn"),
];

pub const DEFAULT_KEY: &str = "f8";

/// Zustand der systemweiten Tastenabfrage.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Access {
    Ok,
    /// macOS: Talkie braucht die Erlaubnis „Eingabeüberwachung“.
    NeedsPermission,
    /// Hier nicht möglich (Handy, Wayland ohne X11) – nur der Knopf in der App.
    Unsupported,
}

pub fn access() -> Access {
    imp::access()
}

/// macOS: fragt nach „Eingabeüberwachung“ (öffnet ggf. die Systemeinstellungen).
pub fn request_access() {
    imp::request_access()
}

/// Ist die Taste gerade gedrückt? None = Taste unbekannt oder Abfrage hier nicht möglich.
pub fn key_down(key: &str) -> Option<bool> {
    imp::key_down(key)
}

#[cfg(target_os = "windows")]
mod imp {
    use super::Access;
    use windows_sys::Win32::UI::Input::KeyboardAndMouse::GetAsyncKeyState;

    fn vk(key: &str) -> Option<i32> {
        Some(match key {
            k if k.starts_with('f') && k[1..].parse::<i32>().is_ok_and(|n| (1..=12).contains(&n)) => {
                0x70 + k[1..].parse::<i32>().ok()? - 1
            }
            "ctrl_l" => 0xA2,
            "ctrl_r" => 0xA3,
            "alt_l" => 0xA4,
            "alt_r" => 0xA5,
            "shift_l" => 0xA0,
            "shift_r" => 0xA1,
            "mouse3" => 0x04,
            "mouse4" => 0x05,
            "mouse5" => 0x06,
            _ => return None,
        })
    }

    pub fn access() -> Access {
        Access::Ok
    }
    pub fn request_access() {}
    pub fn key_down(key: &str) -> Option<bool> {
        let v = vk(key)?;
        // Oberstes Bit = gerade gedrückt
        Some(unsafe { GetAsyncKeyState(v) } as u16 & 0x8000 != 0)
    }
}

#[cfg(target_os = "macos")]
mod imp {
    use super::Access;

    #[link(name = "CoreGraphics", kind = "framework")]
    extern "C" {
        fn CGEventSourceKeyState(state: i32, key: u16) -> bool;
        fn CGEventSourceButtonState(state: i32, button: u32) -> bool;
        fn CGPreflightListenEventAccess() -> bool;
        fn CGRequestListenEventAccess() -> bool;
    }
    const HID_SYSTEM_STATE: i32 = 1;

    enum Code {
        Key(u16),
        Button(u32),
    }

    fn code(key: &str) -> Option<Code> {
        const F: [u16; 12] = [0x7A, 0x78, 0x63, 0x76, 0x60, 0x61, 0x62, 0x64, 0x65, 0x6D, 0x67, 0x6F];
        Some(match key {
            k if k.starts_with('f') => Code::Key(*F.get(k[1..].parse::<usize>().ok()?.checked_sub(1)?)?),
            "ctrl_l" => Code::Key(0x3B),
            "ctrl_r" => Code::Key(0x3E),
            "alt_l" => Code::Key(0x3A),
            "alt_r" => Code::Key(0x3D),
            "shift_l" => Code::Key(0x38),
            "shift_r" => Code::Key(0x3C),
            "mouse3" => Code::Button(2),
            "mouse4" => Code::Button(3),
            "mouse5" => Code::Button(4),
            _ => return None,
        })
    }

    pub fn access() -> Access {
        if unsafe { CGPreflightListenEventAccess() } {
            Access::Ok
        } else {
            Access::NeedsPermission
        }
    }
    pub fn request_access() {
        unsafe { CGRequestListenEventAccess() };
    }
    pub fn key_down(key: &str) -> Option<bool> {
        Some(match code(key)? {
            Code::Key(k) => unsafe { CGEventSourceKeyState(HID_SYSTEM_STATE, k) },
            Code::Button(b) => unsafe { CGEventSourceButtonState(HID_SYSTEM_STATE, b) },
        })
    }
}

#[cfg(all(target_os = "linux", not(target_os = "android")))]
mod imp {
    use super::Access;
    use std::sync::OnceLock;
    use x11_dl::xlib;

    struct X {
        lib: xlib::Xlib,
        display: *mut xlib::Display,
    }
    // Nur der Abfrage-Thread benutzt die Verbindung, und nur lesend
    unsafe impl Send for X {}
    unsafe impl Sync for X {}

    static X11: OnceLock<Option<std::sync::Mutex<X>>> = OnceLock::new();

    fn x11() -> Option<&'static std::sync::Mutex<X>> {
        X11.get_or_init(|| {
            let lib = xlib::Xlib::open().ok()?;
            let display = unsafe { (lib.XOpenDisplay)(std::ptr::null()) };
            (!display.is_null()).then(|| std::sync::Mutex::new(X { lib, display }))
        })
        .as_ref()
    }

    fn keysym(key: &str) -> Option<u64> {
        Some(match key {
            k if k.starts_with('f') => {
                let n: u64 = k[1..].parse().ok()?;
                if !(1..=12).contains(&n) {
                    return None;
                }
                0xFFBE + n - 1
            }
            "ctrl_l" => 0xFFE3,
            "ctrl_r" => 0xFFE4,
            "alt_l" => 0xFFE9,
            "alt_r" => 0xFE03, // Alt Gr auf deutschen Tastaturen
            "shift_l" => 0xFFE1,
            "shift_r" => 0xFFE2,
            _ => return None, // Maus-Seitentasten lassen sich unter X11 so nicht abfragen
        })
    }

    pub fn access() -> Access {
        if x11().is_some() {
            Access::Ok
        } else {
            Access::Unsupported
        }
    }
    pub fn request_access() {}
    pub fn key_down(key: &str) -> Option<bool> {
        let sym = keysym(key)?;
        let x = x11()?.lock().ok()?;
        unsafe {
            let mut code = (x.lib.XKeysymToKeycode)(x.display, sym);
            if code == 0 && key == "alt_r" {
                code = (x.lib.XKeysymToKeycode)(x.display, 0xFFEA); // Alt_R ohne Alt Gr
            }
            if code == 0 {
                return None;
            }
            let mut keys = [0 as std::os::raw::c_char; 32];
            (x.lib.XQueryKeymap)(x.display, keys.as_mut_ptr());
            Some(keys[(code / 8) as usize] as u8 & (1 << (code % 8)) != 0)
        }
    }
}

#[cfg(any(target_os = "android", target_os = "ios"))]
mod imp {
    use super::Access;
    pub fn access() -> Access {
        Access::Unsupported
    }
    pub fn request_access() {}
    pub fn key_down(_key: &str) -> Option<bool> {
        None
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn all_listed_keys_are_known_here() {
        if access() != Access::Ok {
            return; // z. B. CI ohne Bildschirm
        }
        for (id, _) in KEYS {
            // Maustasten gibt es unter X11 nicht
            if cfg!(target_os = "linux") && id.starts_with("mouse") {
                continue;
            }
            assert!(key_down(id).is_some(), "Taste {id} unbekannt");
        }
        assert!(key_down("quatsch").is_none());
    }
}
