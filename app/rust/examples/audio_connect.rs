//! Diagnose: Engine mit echter Soundkarte verbinden und Status ausgeben.
//!   cargo run --example audio_connect -- 127.0.0.1:7777 <CODE>
use rust_lib_talkie::core::engine::Engine;
use std::time::{Duration, Instant};

fn main() {
    let a: Vec<String> = std::env::args().collect();
    let dir = std::env::temp_dir().join(format!("talkie-diag-{}", std::process::id()));
    let e = Engine::new(dir.clone(), true).expect("engine");
    let t = Instant::now();
    println!("connect …");
    e.connect(&a[1], "Diagnose", a.get(2).map(|s| s.as_str()).unwrap_or(""), "").expect("connect");
    println!("connect kehrte nach {:?} zurück", t.elapsed());
    for _ in 0..20 {
        let s = e.snapshot();
        println!("{:>5.1}s  {:?}  {}", t.elapsed().as_secs_f32(), s.status, s.message);
        std::thread::sleep(Duration::from_millis(500));
    }
    let _ = std::fs::remove_dir_all(dir);
}
