//! Audiogeräte über cpal. Läuft in einem eigenen Thread, weil cpal-Streams
//! auf manchen Systemen nicht zwischen Threads verschoben werden dürfen.

use crate::core::dsp::{PullResampler, PushResampler, RATE};
use crate::core::mixer::Mixer;
use anyhow::{anyhow, Result};
use cpal::traits::{DeviceTrait, HostTrait, StreamTrait};
use cpal::{FromSample, Sample, SampleFormat, SizedSample};
use parking_lot::Mutex;
use std::sync::mpsc;
use std::sync::Arc;

fn device_name(d: &cpal::Device) -> String {
    d.description().map(|d| d.name().to_string()).unwrap_or_default()
}

/// Auf Handy und Tablet wählt das System das Gerät (Lautsprecher, Kopfhörer, Bluetooth) selbst.
const SYSTEM_PICKS_DEVICE: bool = cfg!(any(target_os = "android", target_os = "ios"));

pub fn input_devices() -> Vec<String> {
    if SYSTEM_PICKS_DEVICE {
        return Vec::new();
    }
    let host = cpal::default_host();
    host.input_devices().map(|it| it.map(|d| device_name(&d)).filter(|n| !n.is_empty()).collect()).unwrap_or_default()
}

pub fn output_devices() -> Vec<String> {
    if SYSTEM_PICKS_DEVICE {
        return Vec::new();
    }
    let host = cpal::default_host();
    host.output_devices().map(|it| it.map(|d| device_name(&d)).filter(|n| !n.is_empty()).collect()).unwrap_or_default()
}

fn find_input(name: &str) -> Option<cpal::Device> {
    let host = cpal::default_host();
    if !name.is_empty() {
        if let Ok(mut it) = host.input_devices() {
            if let Some(d) = it.find(|d| device_name(d) == name) {
                return Some(d);
            }
        }
    }
    host.default_input_device()
}

fn find_output(name: &str) -> Option<cpal::Device> {
    let host = cpal::default_host();
    if !name.is_empty() {
        if let Ok(mut it) = host.output_devices() {
            if let Some(d) = it.find(|d| device_name(d) == name) {
                return Some(d);
            }
        }
    }
    host.default_output_device()
}

fn build_input<T>(dev: &cpal::Device, cfg: cpal::StreamConfig, tx: mpsc::Sender<Vec<f32>>) -> Result<cpal::Stream>
where
    T: SizedSample + Send + 'static,
    f32: FromSample<T>,
{
    let ch = cfg.channels.max(1) as usize;
    let mut rs = PushResampler::new(cfg.sample_rate, RATE);
    let mut mono = Vec::with_capacity(4096);
    let stream = dev.build_input_stream::<T, _, _>(
        cfg,
        move |data: &[T], _| {
            mono.clear();
            for f in data.chunks(ch) {
                let sum: f32 = f.iter().map(|s| f32::from_sample(*s)).sum();
                mono.push(sum / ch as f32);
            }
            let mut out = Vec::with_capacity(mono.len() * 2);
            rs.process(&mono, &mut out);
            let _ = tx.send(out);
        },
        |e| log::warn!("mikrofon: {e}"),
        None,
    )?;
    Ok(stream)
}

fn build_output<T>(dev: &cpal::Device, cfg: cpal::StreamConfig, mixer: Arc<Mutex<Mixer>>) -> Result<cpal::Stream>
where
    T: SizedSample + FromSample<f32> + Send + 'static,
{
    let ch = cfg.channels.max(1) as usize;
    let mut rs = PullResampler::new(RATE, cfg.sample_rate);
    let stream = dev.build_output_stream::<T, _, _>(
        cfg,
        move |data: &mut [T], _| {
            let mut m = mixer.lock();
            for f in data.chunks_mut(ch) {
                let v = T::from_sample(rs.next(|| m.next_sample()));
                f.fill(v);
            }
        },
        |e| log::warn!("lautsprecher: {e}"),
        None,
    )?;
    Ok(stream)
}

fn open_input(name: &str, tx: mpsc::Sender<Vec<f32>>) -> Result<cpal::Stream> {
    let dev = find_input(name).ok_or_else(|| anyhow!("Kein Mikrofon gefunden"))?;
    let sup = dev.default_input_config()?;
    let cfg: cpal::StreamConfig = sup.config();
    let build = |cfg: cpal::StreamConfig| match sup.sample_format() {
        SampleFormat::F32 => build_input::<f32>(&dev, cfg, tx.clone()),
        SampleFormat::I16 => build_input::<i16>(&dev, cfg, tx.clone()),
        SampleFormat::U16 => build_input::<u16>(&dev, cfg, tx.clone()),
        SampleFormat::I32 => build_input::<i32>(&dev, cfg, tx.clone()),
        SampleFormat::U8 => build_input::<u8>(&dev, cfg, tx.clone()),
        SampleFormat::F64 => build_input::<f64>(&dev, cfg, tx.clone()),
        f => Err(anyhow!("Mikrofonformat {f:?} nicht unterstützt")),
    };
    // Manche Mikrofone (v. a. Android) melden Stereo, öffnen aber nur Mono
    let s = match build(cfg.clone()) {
        Ok(s) => s,
        Err(e) if cfg.channels > 1 => {
            log::info!("mikrofon mit {} kanälen: {e} – versuche mono", cfg.channels);
            build(cpal::StreamConfig { channels: 1, ..cfg })?
        }
        Err(e) => return Err(e),
    };
    s.play()?;
    Ok(s)
}

fn open_output(name: &str, mixer: Arc<Mutex<Mixer>>) -> Result<cpal::Stream> {
    let dev = find_output(name).ok_or_else(|| anyhow!("Kein Lautsprecher gefunden"))?;
    let sup = dev.default_output_config()?;
    let cfg: cpal::StreamConfig = sup.config();
    let s = match sup.sample_format() {
        SampleFormat::F32 => build_output::<f32>(&dev, cfg, mixer),
        SampleFormat::I16 => build_output::<i16>(&dev, cfg, mixer),
        SampleFormat::U16 => build_output::<u16>(&dev, cfg, mixer),
        SampleFormat::I32 => build_output::<i32>(&dev, cfg, mixer),
        SampleFormat::U8 => build_output::<u8>(&dev, cfg, mixer),
        SampleFormat::F64 => build_output::<f64>(&dev, cfg, mixer),
        f => Err(anyhow!("Lautsprecherformat {f:?} nicht unterstützt")),
    }?;
    s.play()?;
    Ok(s)
}

/// Hält Mikrofon- und Lautsprecher-Stream offen, bis es fallen gelassen wird.
pub struct AudioIo {
    stop: Option<mpsc::Sender<()>>,
    thread: Option<std::thread::JoinHandle<()>>,
}

impl Drop for AudioIo {
    fn drop(&mut self) {
        self.stop.take();
        if let Some(t) = self.thread.take() {
            let _ = t.join();
        }
    }
}

/// Startet die Geräte. Liefert zusätzlich Warnungen (z. B. „kein Mikrofon“) —
/// die App läuft dann trotzdem weiter, nur eben ohne diese Richtung.
pub fn start(
    input_name: String,
    output_name: String,
    capture_tx: mpsc::Sender<Vec<f32>>,
    mixer: Arc<Mutex<Mixer>>,
) -> (AudioIo, Vec<String>) {
    let (stop_tx, stop_rx) = mpsc::channel::<()>();
    let (res_tx, res_rx) = mpsc::channel::<Vec<String>>();
    let thread = std::thread::Builder::new()
        .name("talkie-audio".into())
        .spawn(move || {
            let mut warnings = Vec::new();
            let input = open_input(&input_name, capture_tx)
                .map_err(|e| warnings.push(format!("Mikrofon: {e}")))
                .ok();
            let output = open_output(&output_name, mixer)
                .map_err(|e| warnings.push(format!("Lautsprecher: {e}")))
                .ok();
            let _ = res_tx.send(warnings);
            let _ = stop_rx.recv(); // blockiert bis Drop
            drop(input);
            drop(output);
        })
        .expect("audio-thread");
    let warnings = res_rx.recv().unwrap_or_default();
    (AudioIo { stop: Some(stop_tx), thread: Some(thread) }, warnings)
}
