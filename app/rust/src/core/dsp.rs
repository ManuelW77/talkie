//! Signalverarbeitung: Resampling, Rauschfilter, Sprachaktivierung, Opus-Kodierung.

use nnnoiseless::DenoiseState;

pub const RATE: u32 = 48_000;
/// 10 ms bei 48 kHz — Blockgröße von nnnoiseless.
pub const SUB: usize = 480;
/// 40 ms bei 48 kHz — ein Opus-Paket.
pub const FRAME: usize = 1920;
pub const BITRATE: i32 = 16_000;
/// Nachlauf der Sprachaktivierung in 10-ms-Blöcken.
const HANGOVER: u32 = 40;

/// Linearer Resampler für einen kontinuierlichen Eingangsstrom (Mikrofon → 48 kHz).
pub struct PushResampler {
    step: f64,
    pos: f64,
    prev: Option<f32>,
}

impl PushResampler {
    pub fn new(from: u32, to: u32) -> Self {
        PushResampler { step: from as f64 / to as f64, pos: 0.0, prev: None }
    }

    pub fn process(&mut self, input: &[f32], out: &mut Vec<f32>) {
        for &x in input {
            let Some(prev) = self.prev else {
                self.prev = Some(x);
                continue;
            };
            while self.pos < 1.0 {
                out.push(prev + (x - prev) * self.pos as f32);
                self.pos += self.step;
            }
            self.pos -= 1.0;
            self.prev = Some(x);
        }
    }
}

/// Linearer Resampler, der bei Bedarf Samples aus einer Quelle zieht (48 kHz → Gerät).
pub struct PullResampler {
    step: f64,
    pos: f64,
    prev: f32,
    cur: f32,
}

impl PullResampler {
    pub fn new(from: u32, to: u32) -> Self {
        PullResampler { step: from as f64 / to as f64, pos: 1.0, prev: 0.0, cur: 0.0 }
    }

    #[inline]
    pub fn next(&mut self, mut src: impl FnMut() -> f32) -> f32 {
        while self.pos >= 1.0 {
            self.prev = self.cur;
            self.cur = src();
            self.pos -= 1.0;
        }
        let out = self.prev + (self.cur - self.prev) * self.pos as f32;
        self.pos += self.step;
        out
    }
}

pub fn db(rms: f32) -> f32 {
    20.0 * (rms + 1e-9).log10()
}

fn rms(x: &[f32]) -> f32 {
    (x.iter().map(|v| v * v).sum::<f32>() / x.len().max(1) as f32).sqrt()
}

/// Laufzeit-Parameter, die sich während des Sprechens ändern dürfen.
#[derive(Clone, Copy, Debug)]
pub struct CaptureParams {
    pub threshold_db: f32,
    pub noise_suppression: bool,
    pub gain: f32,
    pub muted: bool,
}

/// Ergebnis eines fertigen 40-ms-Pakets.
pub struct Encoded {
    pub data: Vec<u8>,
}

/// Mikrofonkette: 48-kHz-Samples rein, Opus-Pakete raus (nur während gesprochen wird).
pub struct CaptureProcessor {
    denoise: Box<DenoiseState<'static>>,
    sub: Vec<f32>,
    frame: Vec<f32>,
    frame_active: bool,
    hang: u32,
    encoder: opus::Encoder,
    scaled: [f32; SUB],
    cleaned: [f32; SUB],
    out_buf: Vec<u8>,
    /// Höchster Pegel seit dem letzten Abholen, 0..1
    level_peak: f32,
    pub transmitting: bool,
}

impl CaptureProcessor {
    pub fn new() -> anyhow::Result<Self> {
        let mut encoder = opus::Encoder::new(RATE, opus::Channels::Mono, opus::Application::Voip)?;
        encoder.set_bitrate(opus::Bitrate::Bits(BITRATE))?;
        encoder.set_vbr(true)?;
        encoder.set_dtx(true)?;
        encoder.set_signal(opus::Signal::Voice)?;
        encoder.set_complexity(8)?;
        Ok(CaptureProcessor {
            denoise: DenoiseState::new(),
            sub: Vec::with_capacity(SUB),
            frame: Vec::with_capacity(FRAME),
            frame_active: false,
            hang: 0,
            encoder,
            scaled: [0.0; SUB],
            cleaned: [0.0; SUB],
            out_buf: vec![0u8; 1500],
            level_peak: 0.0,
            transmitting: false,
        })
    }

    /// Pegel (0..1) seit dem letzten Aufruf; setzt zurück.
    pub fn take_level(&mut self) -> f32 {
        std::mem::take(&mut self.level_peak)
    }

    /// Verarbeitet 48-kHz-Mono-Samples. `emit` bekommt jedes fertige Opus-Paket.
    pub fn process(&mut self, input: &[f32], p: CaptureParams, mut emit: impl FnMut(Encoded)) {
        for &x in input {
            self.sub.push(x * p.gain);
            if self.sub.len() == SUB {
                self.process_sub(p);
                if self.frame.len() == FRAME {
                    let send = self.frame_active && !p.muted;
                    self.transmitting = send;
                    if send {
                        match self.encoder.encode_float(&self.frame, &mut self.out_buf) {
                            Ok(n) if n > 2 => emit(Encoded { data: self.out_buf[..n].to_vec() }),
                            Ok(_) => {} // DTX: Stille, nichts senden
                            Err(e) => log::warn!("opus encode: {e}"),
                        }
                    }
                    self.frame.clear();
                    self.frame_active = false;
                }
            }
        }
    }

    fn process_sub(&mut self, p: CaptureParams) {
        for (d, s) in self.scaled.iter_mut().zip(self.sub.iter()) {
            *d = s * 32767.0;
        }
        let vad = self.denoise.process_frame(&mut self.cleaned, &self.scaled);
        let signal: &[f32] = if p.noise_suppression { &self.cleaned } else { &self.scaled };
        let level_db = db(rms(signal) / 32767.0);

        // Pegelanzeige: -60 dB … 0 dB → 0 … 1
        let lvl = ((level_db + 60.0) / 60.0).clamp(0.0, 1.0);
        self.level_peak = self.level_peak.max(lvl);

        let active = level_db > p.threshold_db && (vad > 0.2 || level_db > p.threshold_db + 15.0);
        if active {
            self.hang = HANGOVER;
        } else if self.hang > 0 {
            self.hang -= 1;
        }
        if self.hang > 0 {
            self.frame_active = true;
        }
        self.frame.extend(signal.iter().map(|v| (v / 32767.0).clamp(-1.0, 1.0)));
        self.sub.clear();
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn tone(n: usize, amp: f32) -> Vec<f32> {
        (0..n).map(|i| amp * (i as f32 * 2.0 * std::f32::consts::PI * 220.0 / RATE as f32).sin()).collect()
    }

    #[test]
    fn resampler_lengths() {
        let mut r = PushResampler::new(44_100, 48_000);
        let mut out = Vec::new();
        r.process(&vec![0.5; 44_100], &mut out);
        assert!((out.len() as i64 - 48_000).abs() < 3, "{}", out.len());

        let mut pr = PullResampler::new(48_000, 44_100);
        let mut pulled = 0;
        for _ in 0..44_100 {
            pr.next(|| {
                pulled += 1;
                0.0
            });
        }
        assert!((pulled as i64 - 48_000).abs() < 3, "{pulled}");
    }

    #[test]
    fn silence_sends_nothing_loud_sends_packets() {
        let mut c = CaptureProcessor::new().unwrap();
        let p = CaptureParams { threshold_db: -45.0, noise_suppression: false, gain: 1.0, muted: false };
        let mut n = 0;
        c.process(&vec![0.0; RATE as usize], p, |_| n += 1);
        assert_eq!(n, 0);

        let mut sizes = Vec::new();
        c.process(&tone(RATE as usize, 0.3), p, |e| sizes.push(e.data.len()));
        assert!(sizes.len() >= 20, "nur {} pakete", sizes.len());
        let avg = sizes.iter().sum::<usize>() as f32 / sizes.len() as f32;
        // 16 kbit/s * 40 ms = 80 Byte, VBR schwankt etwas
        assert!(avg < 140.0, "pakete zu groß: {avg}");

        let mut muted = 0;
        c.process(&tone(RATE as usize, 0.3), CaptureParams { muted: true, ..p }, |_| muted += 1);
        assert_eq!(muted, 0);
    }
}
