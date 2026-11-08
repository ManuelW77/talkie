//! Ende-zu-Ende-Test gegen einen laufenden Server, ohne Soundkarte.
//!
//!   cargo run --example e2e -- 127.0.0.1:7777 <EINLADUNG-für-mind.-2-Personen>
//!
//! Prüft: Registrierung, erneute Anmeldung ohne Code, Chat, Sprache (Opus über
//! QUIC-Datagramme inkl. Datenrate), Raumwechsel und Zertifikats-Pinning.

use rust_lib_talkie::core::engine::{CoreEvent, Engine, Status};
use std::sync::mpsc;
use std::sync::Arc;
use std::time::{Duration, Instant};

fn engine(dir: &std::path::Path) -> (Engine, mpsc::Receiver<CoreEvent>) {
    let e = Engine::new(dir.to_path_buf(), false).expect("engine");
    let (tx, rx) = mpsc::channel();
    let tx = std::sync::Mutex::new(tx);
    e.set_emitter(Arc::new(move |ev| {
        let _ = tx.lock().unwrap().send(ev);
    }));
    (e, rx)
}

fn wait_status(e: &Engine, want: Status, secs: u64) -> bool {
    let t = Instant::now();
    while t.elapsed() < Duration::from_secs(secs) {
        if e.snapshot().status == want {
            return true;
        }
        std::thread::sleep(Duration::from_millis(50));
    }
    false
}

fn check(ok: bool, what: &str) {
    if ok {
        println!("  OK   {what}");
    } else {
        println!("  FEHLER {what}");
        std::process::exit(1);
    }
}

fn main() {
    let args: Vec<String> = std::env::args().collect();
    let server = args.get(1).cloned().unwrap_or_else(|| "127.0.0.1:7777".into());
    let invite = args.get(2).cloned().expect("einladungscode angeben");
    let base = std::env::temp_dir().join(format!("talkie-e2e-{}", std::process::id()));
    let (da, db, dc) = (base.join("a"), base.join("b"), base.join("c"));
    let suffix = std::process::id() % 1000;

    println!("Talkie E2E gegen {server}");
    let (a, _ra) = engine(&da);
    a.connect(&server, &format!("Anna{suffix}"), &invite, "").expect("connect");
    check(wait_status(&a, Status::Connected, 10), "Anna registriert und verbunden");
    let (b, rb) = engine(&db);
    b.connect(&server, &format!("Ben{suffix}"), &invite, "").expect("connect");
    check(wait_status(&b, Status::Connected, 10), "Ben registriert und verbunden");

    let sa = a.snapshot();
    check(sa.rooms.len() >= 1 && sa.room == sa.rooms[0].id, "beide starten im ersten Raum");
    std::thread::sleep(Duration::from_millis(300));
    check(b.snapshot().users.iter().any(|u| u.id == sa.me), "Ben sieht Anna");

    // Chat
    while rb.try_recv().is_ok() {}
    a.chat("Hallo Ben!");
    let t = Instant::now();
    let mut got = false;
    while t.elapsed() < Duration::from_secs(3) && !got {
        if let Ok(CoreEvent::Chat(m)) = rb.recv_timeout(Duration::from_millis(100)) {
            got = m.text == "Hallo Ben!" && m.from == sa.me;
        }
    }
    check(got, "Chat kommt an");

    // Sprache: 2 s Ton in Echtzeit-Häppchen
    let (p0, _) = b.mixer_stats();
    let secs = 2.0;
    let chunk = 960; // 20 ms
    let total = (48_000.0 * secs) as usize;
    let mut i = 0;
    let mut talking_seen = false;
    while i < total {
        let buf: Vec<f32> = (i..i + chunk)
            .map(|n| 0.3 * (n as f32 * 2.0 * std::f32::consts::PI * 300.0 / 48_000.0).sin())
            .collect();
        a.inject_capture(buf);
        i += chunk;
        std::thread::sleep(Duration::from_millis(20));
        if b.snapshot().users.iter().any(|u| u.id == sa.me && u.talking) {
            talking_seen = true;
        }
    }
    std::thread::sleep(Duration::from_millis(300));
    let (p1, lost) = b.mixer_stats();
    let pkts = p1 - p0;
    println!("       {pkts} Sprachpakete empfangen, {lost} verloren (erwartet ~{})", (secs * 25.0) as u64);
    check(pkts >= 40, "Sprachpakete kommen bei Ben an");
    check(talking_seen, "Ben sieht, dass Anna spricht");
    let rms = b.mixer_drain_rms(4800);
    check(rms > 0.02, &format!("Ben hört den Ton (Pegel {rms:.3})"));

    // Nur für Ben stumm: Annas Sprache kommt bei ihm nicht mehr an, nach dem Aufheben wieder
    let speak = |n: usize| {
        for k in 0..n {
            let buf: Vec<f32> = (0..1920).map(|s| 0.3 * ((k * 1920 + s) as f32 * 0.04).sin()).collect();
            a.inject_capture(buf);
            std::thread::sleep(Duration::from_millis(40));
        }
        std::thread::sleep(Duration::from_millis(300));
    };
    b.set_user_muted(sa.me, true);
    std::thread::sleep(Duration::from_millis(200));
    check(b.snapshot().users.iter().any(|u| u.id == sa.me && u.local_muted), "Ben hat Anna für sich stumm");
    check(!a.snapshot().users.iter().any(|u| u.local_muted), "Anna merkt davon nichts");
    let (s0, _) = b.mixer_stats();
    speak(25);
    check(b.mixer_stats().0 == s0, "Annas Sprache kommt bei Ben nicht an");
    b.set_user_muted(sa.me, false);
    std::thread::sleep(Duration::from_millis(200));
    speak(25);
    check(b.mixer_stats().0 > s0 + 15, "nach dem Aufheben hört Ben Anna wieder");

    // Raumwechsel: Ben in Raum 2 → keine Sprache mehr
    let room2 = sa.rooms.get(1).map(|r| r.id).expect("zweiter raum");
    b.join(room2);
    std::thread::sleep(Duration::from_millis(400));
    check(b.snapshot().room == room2, "Ben ist im zweiten Raum");
    let (q0, _) = b.mixer_stats();
    for k in 0..25 {
        let buf: Vec<f32> = (0..1920).map(|n| 0.3 * ((k * 1920 + n) as f32 * 0.04).sin()).collect();
        a.inject_capture(buf);
        std::thread::sleep(Duration::from_millis(40));
    }
    std::thread::sleep(Duration::from_millis(300));
    check(b.mixer_stats().0 == q0, "keine Sprache aus anderem Raum");

    // Stumm: nichts wird gesendet
    b.join(sa.room);
    std::thread::sleep(Duration::from_millis(300));
    a.set_muted(true);
    let (m0, _) = b.mixer_stats();
    for k in 0..25 {
        let buf: Vec<f32> = (0..1920).map(|n| 0.3 * ((k * 1920 + n) as f32 * 0.04).sin()).collect();
        a.inject_capture(buf);
        std::thread::sleep(Duration::from_millis(40));
    }
    std::thread::sleep(Duration::from_millis(300));
    check(b.mixer_stats().0 == m0, "stumm geschaltet → nichts gesendet");
    check(b.snapshot().users.iter().any(|u| u.id == sa.me && u.muted), "Ben sieht Anna als stumm");

    // Erneut anmelden ohne Code
    drop(a);
    std::thread::sleep(Duration::from_millis(300));
    let (a2, _) = engine(&da);
    let cfg = a2.config();
    check(cfg.registered && !cfg.pinned_fingerprint.is_empty(), "Registrierung und Fingerabdruck gespeichert");
    a2.connect(&server, "", "", "").expect("connect");
    check(wait_status(&a2, Status::Connected, 10), "Anna meldet sich ohne Code wieder an");

    // Falscher Fingerabdruck → abgelehnt
    let (c, _) = engine(&dc);
    c.connect(&server, "Fremd", &invite, &"00".repeat(32)).expect("connect");
    check(wait_status(&c, Status::Failed, 10), "falscher Fingerabdruck wird abgelehnt");
    check(c.snapshot().error_code == "fingerprint_mismatch", "Fehlercode fingerprint_mismatch");

    // Ohne Code und unbekannt → braucht Einladung
    let (d, _) = engine(&base.join("d"));
    d.connect(&server, "Niemand", "", "").expect("connect");
    check(wait_status(&d, Status::Failed, 5) && d.snapshot().error_code == "need_invite", "ohne Einladung kein Zugang");

    let _ = std::fs::remove_dir_all(&base);
    println!("Alles bestanden.");
}
