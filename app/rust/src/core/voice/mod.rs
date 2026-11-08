//! Handy: Ton über die Sprachverarbeitung des Systems (Echo-Unterdrückung, damit es ohne
//! Kopfhörer kein Echo bei den anderen gibt). Desktop nutzt weiter cpal.
//!
//! - iOS: eine „Voice Processing IO“-Einheit für Mikrofon und Lautsprecher zusammen
//! - Android: AAudio mit Sprach-Voreinstellung (Android ab 9; auf 8.x ohne Voreinstellung)
//!
//! Klappt das Öffnen nicht, fällt `device::start` auf cpal zurück.

#[cfg(target_os = "android")]
mod android;
#[cfg(target_os = "ios")]
mod ios;

use crate::core::mixer::Mixer;
use parking_lot::Mutex;
use std::sync::atomic::AtomicBool;
use std::sync::{mpsc, Arc};

/// Hält die geöffneten Ströme; Drop schließt sie.
pub struct VoiceIo {
    #[allow(dead_code)]
    inner: Box<dyn std::any::Any>,
}

/// Öffnet Mikrofon und Lautsprecher mit Echo-Unterdrückung.
#[allow(unused_variables)]
pub fn open(
    capture_tx: mpsc::Sender<Vec<f32>>,
    mixer: Arc<Mutex<Mixer>>,
    failed: Arc<AtomicBool>,
) -> anyhow::Result<VoiceIo> {
    #[cfg(target_os = "android")]
    return Ok(VoiceIo { inner: Box::new(android::open(capture_tx, mixer, failed)?) });
    #[cfg(target_os = "ios")]
    return Ok(VoiceIo { inner: Box::new(ios::open(capture_tx, mixer, failed)?) });
    #[allow(unreachable_code)]
    Err(anyhow::anyhow!("Sprachverarbeitung gibt es nur am Handy"))
}

pub const AVAILABLE: bool = cfg!(any(target_os = "android", target_os = "ios"));
