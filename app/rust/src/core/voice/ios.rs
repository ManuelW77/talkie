//! iOS: eine „Voice Processing IO“-Einheit für Mikrofon und Lautsprecher. Nur wenn beide
//! über dieselbe Einheit laufen, kennt die Echo-Unterdrückung das, was gerade abgespielt wird.

use crate::core::dsp::{PullResampler, PushResampler, RATE};
use crate::core::mixer::Mixer;
use anyhow::{anyhow, Result};
use coreaudio::audio_unit::audio_format::LinearPcmFlags;
use coreaudio::audio_unit::render_callback::{data, Args};
use coreaudio::audio_unit::{AudioUnit, Element, IOType, SampleFormat, Scope, StreamFormat};
use parking_lot::Mutex;
use std::sync::atomic::AtomicBool;
use std::sync::{mpsc, Arc};

const ENABLE_IO: u32 = 2003; // kAudioOutputUnitProperty_EnableIO
const STREAM_FORMAT: u32 = 8; // kAudioUnitProperty_StreamFormat
const OTHER_AUDIO_DUCKING: u32 = 2108; // kAUVoiceIOProperty_OtherAudioDuckingConfiguration (iOS 17+)

/// AUVoiceIOOtherAudioDuckingConfiguration
#[repr(C)]
struct DuckingConfig {
    enable_advanced: u8,
    level: u32, // 10 = so wenig wie möglich
}

pub struct IosIo {
    unit: AudioUnit,
}

impl Drop for IosIo {
    fn drop(&mut self) {
        let _ = self.unit.stop();
    }
}

pub fn open(tx: mpsc::Sender<Vec<f32>>, mixer: Arc<Mutex<Mixer>>, _failed: Arc<AtomicBool>) -> Result<IosIo> {
    let e = |what: &'static str| move |err: coreaudio::Error| anyhow!("{what}: {err:?}");
    let mut unit = AudioUnit::new_uninitialized(IOType::VoiceProcessingIO).map_err(e("Voice-Processing-Einheit"))?;
    let on: u32 = 1;
    unit.set_property(ENABLE_IO, Scope::Input, Element::Input, Some(&on)).map_err(e("Mikrofon einschalten"))?;
    unit.set_property(ENABLE_IO, Scope::Output, Element::Output, Some(&on)).map_err(e("Lautsprecher einschalten"))?;

    // Mono, 32-Bit-Float, 48 kHz in beide Richtungen; die Einheit wandelt selbst um
    let fmt = StreamFormat {
        sample_rate: RATE as f64,
        sample_format: SampleFormat::F32,
        flags: LinearPcmFlags::IS_FLOAT | LinearPcmFlags::IS_PACKED,
        channels: 1,
    };
    let asbd = fmt.to_asbd();
    unit.set_property(STREAM_FORMAT, Scope::Output, Element::Input, Some(&asbd)).map_err(e("Mikrofonformat"))?;
    unit.set_property(STREAM_FORMAT, Scope::Input, Element::Output, Some(&asbd)).map_err(e("Lautsprecherformat"))?;

    // Spielton nur so wenig wie möglich leiser machen (iOS 17+, sonst egal)
    let duck = DuckingConfig { enable_advanced: 1, level: 10 };
    if let Err(err) = unit.set_property(OTHER_AUDIO_DUCKING, Scope::Global, Element::Output, Some(&duck)) {
        log::debug!("ducking-einstellung: {err:?}");
    }

    let mut in_rs = PushResampler::new(RATE, RATE);
    unit.set_input_callback(move |args: Args<data::Interleaved<f32>>| {
        let mut out = Vec::with_capacity(args.num_frames * 2);
        let ch = args.data.channels.max(1);
        if ch == 1 {
            in_rs.process(args.data.buffer, &mut out);
        } else {
            let mono: Vec<f32> = args.data.buffer.chunks(ch).map(|f| f.iter().sum::<f32>() / ch as f32).collect();
            in_rs.process(&mono, &mut out);
        }
        let _ = tx.send(out);
        Ok(())
    })
    .map_err(e("Mikrofon-Callback"))?;

    let mut out_rs = PullResampler::new(RATE, RATE);
    unit.set_render_callback(move |args: Args<data::Interleaved<f32>>| {
        let ch = args.data.channels.max(1);
        let mut m = mixer.lock();
        for f in args.data.buffer.chunks_mut(ch) {
            f.fill(out_rs.next(|| m.next_sample()));
        }
        Ok(())
    })
    .map_err(e("Lautsprecher-Callback"))?;

    unit.initialize().map_err(e("Einheit vorbereiten"))?;
    unit.start().map_err(e("Einheit starten"))?;
    log::info!("voice processing io gestartet");
    Ok(IosIo { unit })
}
