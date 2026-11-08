//! Kurze Hinweistöne (Kommen, Gehen, Chat), direkt erzeugt – keine Sounddateien nötig.

use crate::core::dsp::RATE;
use std::f32::consts::PI;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Cue {
    /// Jemand kommt in meinen Raum: zwei Töne aufwärts
    Join,
    /// Jemand geht aus meinem Raum: zwei Töne abwärts
    Leave,
    /// Neue Chatnachricht: kurzes „Pling“
    Chat,
}

/// Lautstärke der Hinweise relativ zur Sprache (leise, damit sie beim Zocken nicht stören).
const LEVEL: f32 = 0.22;

/// Ein Ton mit weichem Ein- und Ausklingen (kein Knacken).
fn tone(out: &mut Vec<f32>, freq: f32, ms: u32, decay: bool) {
    let n = (RATE as u32 * ms / 1000) as usize;
    let fade = (RATE as usize / 200).min(n / 2); // 5 ms
    for i in 0..n {
        let t = i as f32 / RATE as f32;
        let mut env = 1.0;
        if i < fade {
            env = i as f32 / fade as f32;
        } else if i >= n - fade {
            env = (n - i) as f32 / fade as f32;
        }
        if decay {
            env *= (-(i as f32) / (n as f32 * 0.35)).exp();
        }
        // Grundton + etwas Oberton klingt freundlicher als reiner Sinus
        let s = (2.0 * PI * freq * t).sin() + 0.25 * (4.0 * PI * freq * t).sin();
        out.push(LEVEL * env * s / 1.25);
    }
}

fn pause(out: &mut Vec<f32>, ms: u32) {
    out.extend(std::iter::repeat_n(0.0, (RATE as u32 * ms / 1000) as usize));
}

/// Samples (mono, 48 kHz) für einen Hinweiston.
pub fn render(cue: Cue) -> Vec<f32> {
    let mut out = Vec::with_capacity(RATE as usize / 4);
    match cue {
        Cue::Join => {
            tone(&mut out, 660.0, 70, false);
            pause(&mut out, 15);
            tone(&mut out, 880.0, 110, true);
        }
        Cue::Leave => {
            tone(&mut out, 880.0, 70, false);
            pause(&mut out, 15);
            tone(&mut out, 587.0, 110, true);
        }
        Cue::Chat => tone(&mut out, 1320.0, 140, true),
    }
    out
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cues_are_short_quiet_and_click_free() {
        for c in [Cue::Join, Cue::Leave, Cue::Chat] {
            let s = render(c);
            assert!(!s.is_empty() && s.len() < RATE as usize / 2, "{c:?}: {} samples", s.len());
            assert!(s.iter().all(|v| v.abs() <= LEVEL + 1e-3), "{c:?} zu laut");
            assert!(s[0].abs() < 0.01 && s[s.len() - 1].abs() < 0.01, "{c:?} knackt");
        }
        assert_ne!(render(Cue::Join), render(Cue::Leave));
    }
}
