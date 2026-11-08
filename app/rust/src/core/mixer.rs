//! Empfang: Jitter-Puffer pro Sprecher, Opus-Dekodierung und Mischen.

use crate::core::dsp::{FRAME, RATE};
use std::collections::{HashMap, HashSet, VecDeque};
use std::time::{Duration, Instant};

/// So viel muss gepuffert sein, bevor ein Sprecher abgespielt wird (60 ms).
const PREBUFFER: usize = (RATE as usize) * 60 / 1000;
/// Mehr als das wird abgeschnitten, damit die Verzögerung klein bleibt (400 ms → 120 ms).
const MAX_BUFFER: usize = (RATE as usize) * 400 / 1000;
const TRIM_TO: usize = (RATE as usize) * 120 / 1000;
/// Nach dieser Pause beginnt ein neuer „Redeschwall“ ohne Verlustverschleierung.
const SPURT_GAP: Duration = Duration::from_millis(300);
const TALKING_WINDOW: Duration = Duration::from_millis(250);
/// Fehlt ein Paket, so viele spätere abwarten, bevor es als verloren gilt.
const REORDER_DEPTH: usize = 3;
/// Höchstens so viele verlorene Pakete pro Empfang verschleiern (je 40 ms).
const MAX_PLC: usize = 3;

struct Speaker {
    decoder: opus::Decoder,
    pending: HashMap<u16, Vec<u8>>,
    expected: u16,
    started: bool,
    pcm: VecDeque<f32>,
    prebuffering: bool,
    last_rx: Instant,
    /// Lautstärke nur für diese Person (1.0 = normal).
    gain: f32,
}

impl Speaker {
    fn new() -> anyhow::Result<Self> {
        Ok(Speaker {
            decoder: opus::Decoder::new(RATE, opus::Channels::Mono)?,
            pending: HashMap::new(),
            expected: 0,
            started: false,
            pcm: VecDeque::with_capacity(MAX_BUFFER),
            prebuffering: true,
            last_rx: Instant::now(),
            gain: 1.0,
        })
    }

    fn decode(&mut self, data: Option<&[u8]>, buf: &mut [f32]) {
        let res = match data {
            Some(d) => self.decoder.decode_float(d, buf, false),
            None => self.decoder.decode_float(&[], &mut buf[..FRAME], false),
        };
        match res {
            Ok(n) => self.pcm.extend(&buf[..n]),
            Err(e) => log::debug!("opus decode: {e}"),
        }
    }
}

pub struct Mixer {
    speakers: HashMap<i64, Speaker>,
    buf: Vec<f32>,
    pub volume: f32,
    pub deaf: bool,
    /// Nur für sich stumm geschaltete Nutzer (falls der Server sie doch schickt).
    pub ignored: HashSet<i64>,
    /// Hinweistöne, die gerade abgespielt werden.
    cues: VecDeque<f32>,
    /// Lautstärke pro Person (fehlt = 1.0).
    gains: HashMap<i64, f32>,
    /// Zähler für Tests/Diagnose
    pub packets: u64,
    pub lost: u64,
}

impl Default for Mixer {
    fn default() -> Self {
        Mixer {
            speakers: HashMap::new(),
            buf: vec![0.0; 5760],
            volume: 1.0,
            deaf: false,
            ignored: HashSet::new(),
            cues: VecDeque::new(),
            gains: HashMap::new(),
            packets: 0,
            lost: 0,
        }
    }
}

impl Mixer {
    pub fn push(&mut self, uid: i64, seq: u16, data: &[u8]) {
        if self.deaf || self.ignored.contains(&uid) {
            return;
        }
        let now = Instant::now();
        let sp = match self.speakers.entry(uid) {
            std::collections::hash_map::Entry::Occupied(o) => o.into_mut(),
            std::collections::hash_map::Entry::Vacant(v) => match Speaker::new() {
                Ok(mut s) => {
                    s.gain = self.gains.get(&uid).copied().unwrap_or(1.0);
                    v.insert(s)
                }
                Err(e) => {
                    log::warn!("decoder: {e}");
                    return;
                }
            },
        };
        self.packets += 1;
        if !sp.started || now.duration_since(sp.last_rx) > SPURT_GAP {
            sp.started = true;
            sp.expected = seq;
            sp.pending.clear();
        }
        sp.last_rx = now;

        let diff = seq.wrapping_sub(sp.expected) as i16;
        if diff < 0 {
            return; // zu spät, schon verschleiert
        }
        if diff > 50 {
            sp.expected = seq; // großer Sprung: neu synchronisieren
            sp.pending.clear();
        }
        sp.pending.insert(seq, data.to_vec());

        let mut plc = 0;
        loop {
            if let Some(p) = sp.pending.remove(&sp.expected) {
                sp.decode(Some(&p), &mut self.buf);
            } else if sp.pending.len() >= REORDER_DEPTH {
                self.lost += 1;
                if plc < MAX_PLC {
                    sp.decode(None, &mut self.buf); // Paketverlust verschleiern
                    plc += 1;
                } else {
                    // große Lücke: nicht ewig verschleiern (hält sonst die Audio-Sperre zu lange)
                    let exp = sp.expected;
                    if let Some(next) = sp.pending.keys().copied().min_by_key(|k| k.wrapping_sub(exp)) {
                        self.lost += next.wrapping_sub(exp).saturating_sub(1) as u64;
                        sp.expected = next;
                        continue;
                    }
                }
            } else {
                break;
            }
            sp.expected = sp.expected.wrapping_add(1);
        }

        if sp.pcm.len() > MAX_BUFFER {
            let drop = sp.pcm.len() - TRIM_TO;
            sp.pcm.drain(..drop);
        }
    }

    /// Lautstärke einer Person (0.0 … 3.0, 1.0 = normal).
    pub fn set_gain(&mut self, uid: i64, gain: f32) {
        let g = gain.clamp(0.0, 3.0);
        if (g - 1.0).abs() < 1e-3 {
            self.gains.remove(&uid);
        } else {
            self.gains.insert(uid, g);
        }
        if let Some(sp) = self.speakers.get_mut(&uid) {
            sp.gain = g;
        }
    }

    pub fn gains_snapshot(&self) -> HashMap<i64, f32> {
        self.gains.clone()
    }

    pub fn gain(&self, uid: i64) -> f32 {
        self.gains.get(&uid).copied().unwrap_or(1.0)
    }

    /// Hinweiston einreihen. Mehr als eine Sekunde Rückstau wird verworfen (z. B. viele Leute auf einmal).
    pub fn play_cue(&mut self, samples: &[f32]) {
        if self.cues.len() < RATE as usize {
            self.cues.extend(samples);
        }
    }

    /// Ein gemischtes Sample bei 48 kHz.
    #[inline]
    pub fn next_sample(&mut self) -> f32 {
        let mut acc = self.cues.pop_front().unwrap_or(0.0);
        for sp in self.speakers.values_mut() {
            if sp.prebuffering {
                if sp.pcm.len() >= PREBUFFER {
                    sp.prebuffering = false;
                } else {
                    continue;
                }
            }
            match sp.pcm.pop_front() {
                Some(s) => acc += s * sp.gain,
                None => sp.prebuffering = true,
            }
        }
        (acc * self.volume).clamp(-1.0, 1.0)
    }

    /// IDs, von denen gerade Sprache ankommt.
    pub fn talking(&self) -> Vec<i64> {
        let now = Instant::now();
        let mut v: Vec<i64> = self
            .speakers
            .iter()
            .filter(|(_, s)| now.duration_since(s.last_rx) < TALKING_WINDOW)
            .map(|(id, _)| *id)
            .collect();
        v.sort_unstable();
        v
    }

    pub fn remove(&mut self, uid: i64) {
        self.speakers.remove(&uid);
    }

    pub fn clear(&mut self) {
        self.speakers.clear();
    }

    /// Räumt Sprecher weg, die lange still sind (Decoder-Speicher freigeben).
    pub fn gc(&mut self) {
        let now = Instant::now();
        self.speakers.retain(|_, s| now.duration_since(s.last_rx) < Duration::from_secs(60) || !s.pcm.is_empty());
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::core::dsp::{CaptureParams, CaptureProcessor};

    fn encoded_tone(seconds: f32) -> Vec<Vec<u8>> {
        let n = (RATE as f32 * seconds) as usize;
        let tone: Vec<f32> =
            (0..n).map(|i| 0.3 * (i as f32 * 2.0 * std::f32::consts::PI * 300.0 / RATE as f32).sin()).collect();
        let mut c = CaptureProcessor::new().unwrap();
        let mut pkts = Vec::new();
        c.process(
            &tone,
            CaptureParams { threshold_db: -45.0, noise_suppression: false, gain: 1.0, muted: false, ptt: None },
            |e| pkts.push(e.data),
        );
        pkts
    }

    fn drain(m: &mut Mixer, n: usize) -> f32 {
        let mut e = 0.0;
        for _ in 0..n {
            let s = m.next_sample();
            e += s * s;
        }
        (e / n as f32).sqrt()
    }

    #[test]
    fn decodes_in_order_and_reordered_and_lossy() {
        let pkts = encoded_tone(1.0);
        assert!(pkts.len() > 20);

        // vertauschte Reihenfolge und ein verlorenes Paket
        let mut m = Mixer::default();
        let mut order: Vec<usize> = (0..pkts.len()).collect();
        order.swap(3, 4);
        order.remove(10);
        for i in order {
            m.push(7, 1000u16.wrapping_add(i as u16), &pkts[i]);
        }
        assert_eq!(m.talking(), vec![7]);
        let r = drain(&mut m, RATE as usize / 2);
        assert!(r > 0.05, "zu leise: {r}");
        assert_eq!(m.lost, 1);
    }

    #[test]
    fn big_gap_is_capped() {
        let pkts = encoded_tone(0.5);
        let mut m = Mixer::default();
        m.push(1, 0, &pkts[0]);
        // 40 Pakete fehlen, dann kommt der Rest
        for (i, p) in pkts.iter().enumerate().skip(1) {
            m.push(1, 40 + i as u16, p);
        }
        assert_eq!(m.lost, 40);
        assert!(m.speakers[&1].pcm.len() < 48_000, "zu viel verschleiert");
    }

    #[test]
    fn seq_wraparound() {
        let pkts = encoded_tone(0.5);
        let mut m = Mixer::default();
        for (i, p) in pkts.iter().enumerate() {
            m.push(1, 65530u16.wrapping_add(i as u16), p);
        }
        assert_eq!(m.lost, 0);
        assert!(drain(&mut m, 9600) > 0.05);
    }

    #[test]
    fn ignored_speaker_is_dropped_others_play() {
        let pkts = encoded_tone(0.5);
        let mut m = Mixer::default();
        m.ignored.insert(1);
        for (i, p) in pkts.iter().enumerate() {
            m.push(1, i as u16, p);
        }
        assert!(m.talking().is_empty());
        assert!(drain(&mut m, 4800) < 1e-6);
        for (i, p) in pkts.iter().enumerate() {
            m.push(2, i as u16, p);
        }
        assert_eq!(m.talking(), vec![2]);
        assert!(drain(&mut m, 9600) > 0.05);
    }

    #[test]
    fn per_person_volume() {
        let pkts = encoded_tone(0.5);
        let level = |gain: f32| {
            let mut m = Mixer::default();
            m.set_gain(1, gain);
            for (i, p) in pkts.iter().enumerate() {
                m.push(1, i as u16, p);
            }
            drain(&mut m, 9600)
        };
        let normal = level(1.0);
        assert!(level(0.5) < normal * 0.6 && level(0.5) > normal * 0.4, "halb so laut");
        assert!(level(0.0) < 1e-6, "0 = still");
        // Änderung wirkt auch auf laufende Sprecher
        let mut m = Mixer::default();
        for (i, p) in pkts.iter().enumerate() {
            m.push(1, i as u16, p);
        }
        m.set_gain(1, 0.0);
        assert!(drain(&mut m, 9600) < 1e-6);
        assert_eq!(m.gain(1), 0.0);
        m.set_gain(1, 1.0);
        assert!(m.gains.is_empty(), "1.0 wird nicht gespeichert");
    }

    #[test]
    fn cue_is_played_and_capped() {
        let mut m = Mixer::default();
        let c = crate::core::cues::render(crate::core::cues::Cue::Chat);
        m.play_cue(&c);
        assert!(drain(&mut m, c.len()) > 0.01);
        assert!(drain(&mut m, 480) < 1e-6, "danach still");
        for _ in 0..100 {
            m.play_cue(&c);
        }
        assert!(m.cues.len() < 2 * RATE as usize, "Rückstau begrenzt");
    }

    #[test]
    fn deaf_ignores() {
        let pkts = encoded_tone(0.3);
        let mut m = Mixer { deaf: true, ..Default::default() };
        for (i, p) in pkts.iter().enumerate() {
            m.push(1, i as u16, p);
        }
        assert!(m.talking().is_empty());
    }
}
