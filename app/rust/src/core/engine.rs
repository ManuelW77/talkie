//! Engine: verbindet Netzwerk, Audio und Zustand und meldet Änderungen an die Oberfläche.

use crate::core::config::{Config, Settings, Storage};
use crate::core::device::{self, AudioIo};
use crate::core::dsp::CaptureParams;
use crate::core::dsp::CaptureProcessor;
use crate::core::cues::{self, Cue};
use crate::core::mixer::Mixer;
use crate::core::net::{self, ConnectError};
use crate::core::protocol::*;
use anyhow::Result;
use bytes::Bytes;
use ed25519_dalek::SigningKey;
use parking_lot::Mutex;
use std::collections::BTreeMap;
use std::sync::atomic::{AtomicBool, AtomicU16, AtomicU64, Ordering};
use std::sync::{mpsc, Arc};
use std::time::Duration;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Status {
    /// Nicht verbunden (z. B. vor dem ersten Start oder nach „Trennen“).
    Idle,
    Connecting,
    Connected,
    Reconnecting,
    /// Verbindung nicht möglich ohne Eingreifen, siehe `error_code`.
    Failed,
}

#[derive(Debug, Clone)]
pub struct UserView {
    pub id: i64,
    pub name: String,
    pub room: i64,
    pub muted: bool,
    pub deaf: bool,
    pub talking: bool,
    /// Nur für mich stumm geschaltet.
    pub local_muted: bool,
    /// Lautstärke nur für mich (1.0 = normal).
    pub volume: f32,
}

#[derive(Debug, Clone)]
pub struct Snapshot {
    pub status: Status,
    pub message: String,
    pub error_code: String,
    pub me: i64,
    pub room: i64,
    pub rooms: Vec<WireRoom>,
    pub users: Vec<UserView>,
    pub muted: bool,
    pub deaf: bool,
    /// Darf andere rauswerfen/sperren (verknüpftes Webkonto mit Recht).
    pub moderator: bool,
    /// Verknüpftes Admin-Konto (darf den Chat leeren).
    pub admin: bool,
    /// So viele Stunden bleibt der Chat erhalten (0 = unbegrenzt).
    pub chat_hours: i64,
}

#[derive(Debug, Clone)]
pub enum CoreEvent {
    State(Snapshot),
    Chat(WireChat),
    History(i64, Vec<WireChat>),
    Level(f32),
    Notice(String),
}

pub type Emitter = Arc<dyn Fn(CoreEvent) + Send + Sync>;

struct NetState {
    status: Status,
    message: String,
    error_code: String,
    me: i64,
    rooms: Vec<WireRoom>,
    users: BTreeMap<i64, WireUser>,
    talking: Vec<i64>,
    moderator: bool,
    admin: bool,
    chat_hours: i64,
}

struct Inner {
    storage: Storage,
    config: Mutex<Config>,
    key: Mutex<SigningKey>,
    emitter: Mutex<Option<Emitter>>,
    net: Mutex<NetState>,
    muted: AtomicBool,
    deaf: AtomicBool,
    self_talking: AtomicBool,
    conn: Mutex<Option<quinn::Connection>>,
    cmd_tx: Mutex<Option<tokio::sync::mpsc::UnboundedSender<Envelope>>>,
    session: Mutex<Option<tokio::task::JoinHandle<()>>>,
    mixer: Arc<Mutex<Mixer>>,
    params: Mutex<CaptureParams>,
    audio: Mutex<Option<AudioIo>>,
    capture_tx: mpsc::Sender<Vec<f32>>,
    seq: AtomicU16,
    audio_enabled: bool,
    /// Aufgeben nach (erster Verbindungsaufbau, nach Abbruch). Für Tests verkürzbar.
    give_up: Mutex<(Duration, Duration)>,
    /// Wird bei jedem Verbinden/Trennen erhöht; alte Sitzungs-Tasks erkennen daran, dass sie veraltet sind.
    generation: AtomicU64,
}

pub struct Engine {
    rt: tokio::runtime::Runtime,
    inner: Arc<Inner>,
}

fn params_from(s: &Settings, muted: bool) -> CaptureParams {
    CaptureParams {
        threshold_db: s.vad_threshold_db.clamp(-80.0, 0.0),
        noise_suppression: s.noise_suppression,
        gain: s.input_gain.clamp(0.0, 8.0),
        muted,
    }
}

impl Inner {
    fn emit(&self, e: CoreEvent) {
        let em = self.emitter.lock().clone();
        if let Some(em) = em {
            em(e);
        }
    }

    fn snapshot(&self) -> Snapshot {
        // Mixer vor net sperren (nie beide verschachtelt in umgekehrter Reihenfolge)
        let (ignored, gains) = {
            let m = self.mixer.lock();
            (m.ignored.clone(), m.gains_snapshot())
        };
        let n = self.net.lock();
        let muted = self.muted.load(Ordering::Relaxed);
        let self_talking = self.self_talking.load(Ordering::Relaxed) && !muted;
        let users: Vec<UserView> = n
            .users
            .values()
            .map(|u| UserView {
                id: u.id,
                name: u.name.clone(),
                room: u.room,
                muted: u.muted,
                deaf: u.deaf,
                talking: if u.id == n.me { self_talking } else { n.talking.binary_search(&u.id).is_ok() },
                local_muted: ignored.contains(&u.id),
                volume: gains.get(&u.id).copied().unwrap_or(1.0),
            })
            .collect();
        Snapshot {
            status: n.status,
            message: n.message.clone(),
            error_code: n.error_code.clone(),
            me: n.me,
            room: n.users.get(&n.me).map(|u| u.room).unwrap_or(0),
            rooms: n.rooms.clone(),
            users,
            muted,
            deaf: self.deaf.load(Ordering::Relaxed),
            moderator: n.moderator,
            admin: n.admin,
            chat_hours: n.chat_hours,
        }
    }

    fn emit_state(&self) {
        let s = self.snapshot();
        self.emit(CoreEvent::State(s));
    }

    fn set_status(&self, status: Status, message: impl Into<String>, code: &str) {
        {
            let mut n = self.net.lock();
            n.status = status;
            n.message = message.into();
            n.error_code = code.to_string();
            if status != Status::Connected {
                n.users.clear();
                n.talking.clear();
            }
        }
        self.emit_state();
    }

    fn send_cmd(&self, e: Envelope) {
        if let Some(tx) = self.cmd_tx.lock().as_ref() {
            let _ = tx.send(e);
        }
    }

    fn send_voice(&self, opus: &[u8]) {
        if let Some(c) = self.conn.lock().as_ref() {
            let seq = self.seq.fetch_add(1, Ordering::Relaxed);
            let _ = c.send_datagram(Bytes::from(voice_out(seq, opus)));
        }
    }

    fn is_current(&self, generation: u64) -> bool {
        self.generation.load(Ordering::SeqCst) == generation
    }

    fn save_config(&self) {
        let c = self.config.lock().clone();
        if let Err(e) = self.storage.save_config(&c) {
            log::warn!("config speichern: {e}");
        }
    }

    fn ignore_envelope(&self) -> Envelope {
        let mut e = Envelope::new("ignore");
        e.ids = self.config.lock().muted_users.clone();
        e
    }

    fn start_audio(&self) {
        if !self.audio_enabled {
            return;
        }
        let mut a = self.audio.lock();
        if a.is_some() {
            return;
        }
        let s = self.config.lock().settings.clone();
        let (io, warnings) =
            device::start(s.input_device, s.output_device, self.capture_tx.clone(), self.mixer.clone());
        *a = Some(io);
        drop(a);
        for w in warnings {
            self.emit(CoreEvent::Notice(w));
        }
    }

    /// Gibt Mikrofon und Lautsprecher frei (z. B. wenn keine Verbindung mehr möglich ist).
    fn stop_audio(&self) {
        let io = self.audio.lock().take();
        drop(io);
        self.self_talking.store(false, Ordering::Relaxed);
    }

    fn state_envelope(&self) -> Envelope {
        let mut e = Envelope::new("state");
        e.muted = Some(self.muted.load(Ordering::Relaxed));
        e.deaf = Some(self.deaf.load(Ordering::Relaxed));
        e
    }
}

/// Mikrofon-Verarbeitung: läuft für die ganze Lebensdauer der Engine.
fn capture_loop(inner: std::sync::Weak<Inner>, rx: mpsc::Receiver<Vec<f32>>) {
    let mut proc = match CaptureProcessor::new() {
        Ok(p) => p,
        Err(e) => {
            log::error!("opus encoder: {e}");
            return;
        }
    };
    let mut since_level = 0usize;
    while let Ok(buf) = rx.recv() {
        let Some(inner) = inner.upgrade() else { return };
        let p = *inner.params.lock();
        proc.process(&buf, p, |enc| inner.send_voice(&enc.data));
        since_level += buf.len();
        if since_level >= 4800 {
            since_level = 0;
            inner.emit(CoreEvent::Level(proc.take_level()));
            let t = proc.transmitting;
            if inner.self_talking.swap(t, Ordering::Relaxed) != t {
                inner.emit_state();
            }
        }
    }
}

enum SessionEnd {
    Lost(String),
    Kicked(String),
}

impl Engine {
    /// `audio_enabled = false` für Tests ohne Soundkarte (Audio wird dann per `inject_capture` eingespeist).
    pub fn new(data_dir: std::path::PathBuf, audio_enabled: bool) -> Result<Engine> {
        let storage = Storage::new(data_dir)?;
        let config = storage.load_config();
        let key = storage.load_or_create_key()?;
        let rt = tokio::runtime::Builder::new_multi_thread().worker_threads(2).enable_all().build()?;
        let (capture_tx, capture_rx) = mpsc::channel();
        let mixer = Arc::new(Mutex::new(Mixer::default()));
        {
            let mut m = mixer.lock();
            m.volume = config.settings.output_volume;
            m.ignored = config.muted_users.iter().copied().collect();
            for (&id, &g) in &config.user_volumes {
                m.set_gain(id, g);
            }
        }
        let params = params_from(&config.settings, false);
        let inner = Arc::new(Inner {
            storage,
            config: Mutex::new(config),
            key: Mutex::new(key),
            emitter: Mutex::new(None),
            net: Mutex::new(NetState {
                status: Status::Idle,
                message: String::new(),
                error_code: String::new(),
                me: 0,
                rooms: Vec::new(),
                users: BTreeMap::new(),
                talking: Vec::new(),
                moderator: false,
                admin: false,
                chat_hours: 0,
            }),
            muted: AtomicBool::new(false),
            deaf: AtomicBool::new(false),
            self_talking: AtomicBool::new(false),
            conn: Mutex::new(None),
            cmd_tx: Mutex::new(None),
            session: Mutex::new(None),
            mixer,
            params: Mutex::new(params),
            audio: Mutex::new(None),
            capture_tx,
            seq: AtomicU16::new(0),
            audio_enabled,
            generation: AtomicU64::new(0),
            give_up: Mutex::new((GIVE_UP_FIRST, GIVE_UP_LOST)),
        });
        let weak = Arc::downgrade(&inner);
        std::thread::Builder::new().name("talkie-capture".into()).spawn(move || capture_loop(weak, capture_rx))?;
        Ok(Engine { rt, inner })
    }

    pub fn set_emitter(&self, em: Emitter) {
        *self.inner.emitter.lock() = Some(em);
        self.inner.emit_state();
    }

    pub fn config(&self) -> Config {
        self.inner.config.lock().clone()
    }

    pub fn snapshot(&self) -> Snapshot {
        self.inner.snapshot()
    }

    fn start_audio(&self) {
        self.inner.start_audio();
    }

    fn stop_audio(&self) {
        self.inner.stop_audio();
    }

    /// Verbindet (und registriert, falls das Gerät noch nicht registriert ist).
    /// `fingerprint` (optional) pinnt das Server-Zertifikat fest.
    pub fn connect(&self, server: &str, name: &str, invite: &str, fingerprint: &str) -> Result<()> {
        let pin = if fingerprint.trim().is_empty() {
            None
        } else {
            Some(net::parse_fingerprint(fingerprint).ok_or_else(|| anyhow::anyhow!("ungültiger Fingerabdruck"))?)
        };
        self.stop_session();
        {
            let mut c = self.inner.config.lock();
            let server = server.trim().to_string();
            if net::split_host_port(&c.server) != net::split_host_port(&server) {
                c.pinned_fingerprint.clear(); // neuer Server: neu vertrauen
                c.registered = false;
                c.last_room = 0;
            }
            c.server = server;
            if !name.trim().is_empty() {
                c.name = name.trim().to_string();
            }
            if let Some(fp) = pin {
                c.pinned_fingerprint = hex::encode(fp);
            }
        }
        self.inner.save_config();
        self.start_audio();
        let inner = self.inner.clone();
        let invite = invite.trim().to_string();
        let generation = self.inner.generation.load(Ordering::SeqCst);
        let handle = self.rt.spawn(async move { session_loop(inner, invite, generation).await });
        *self.inner.session.lock() = Some(handle);
        Ok(())
    }

    fn stop_session(&self) {
        {
            // Unter dem conn-Lock: eine alte Sitzung kann danach keine Verbindung mehr eintragen
            let mut conn = self.inner.conn.lock();
            self.inner.generation.fetch_add(1, Ordering::SeqCst);
            if let Some(c) = conn.take() {
                c.close(0u32.into(), b"bye");
            }
        }
        if let Some(h) = self.inner.session.lock().take() {
            h.abort();
        }
        *self.inner.cmd_tx.lock() = None;
        self.inner.mixer.lock().clear();
    }

    pub fn disconnect(&self) {
        self.stop_session();
        self.stop_audio();
        self.inner.set_status(Status::Idle, "", "");
    }

    /// Gerät abmelden: Schlüssel vergessen, danach ist eine neue Einladung nötig.
    pub fn logout(&self) -> Result<()> {
        self.disconnect();
        self.inner.storage.reset_key()?;
        *self.inner.key.lock() = self.inner.storage.load_or_create_key()?;
        {
            let mut c = self.inner.config.lock();
            c.registered = false;
            c.last_room = 0;
            c.muted_users.clear(); // IDs gelten nur auf diesem Server
            c.user_volumes.clear();
        }
        {
            let mut m = self.inner.mixer.lock();
            m.ignored.clear();
            for id in m.gains_snapshot().into_keys() {
                m.set_gain(id, 1.0);
            }
        }
        self.inner.save_config();
        Ok(())
    }

    pub fn join(&self, room: i64) {
        let mut e = Envelope::new("join");
        e.room = room;
        self.inner.send_cmd(e);
    }

    pub fn chat(&self, text: &str) {
        let t = text.trim();
        if t.is_empty() {
            return;
        }
        let mut e = Envelope::new("chat");
        e.text = t.chars().take(500).collect();
        self.inner.send_cmd(e);
    }

    pub fn set_muted(&self, muted: bool) {
        self.inner.muted.store(muted, Ordering::Relaxed);
        if !muted && self.inner.deaf.swap(false, Ordering::Relaxed) {
            self.inner.mixer.lock().deaf = false;
        }
        self.inner.params.lock().muted = muted;
        self.inner.send_cmd(self.inner.state_envelope());
        self.inner.emit_state();
    }

    /// Lautstärke einer Person nur für mich (0.0 … 3.0). Bleibt gespeichert.
    pub fn set_user_volume(&self, user: i64, volume: f32) {
        let v = volume.clamp(0.0, 3.0);
        {
            let mut c = self.inner.config.lock();
            if (v - 1.0).abs() < 1e-3 {
                c.user_volumes.remove(&user);
            } else {
                c.user_volumes.insert(user, v);
            }
        }
        self.inner.save_config();
        self.inner.mixer.lock().set_gain(user, v);
        self.inner.emit_state();
    }

    /// Schaltet einen anderen Nutzer nur für mich stumm (oder wieder laut).
    /// Bleibt gespeichert; der Server schickt seine Sprache dann gar nicht erst.
    pub fn set_user_muted(&self, user: i64, muted: bool) {
        {
            let mut c = self.inner.config.lock();
            c.muted_users.retain(|&id| id != user);
            if muted {
                c.muted_users.push(user);
            }
        }
        self.inner.save_config();
        {
            let mut m = self.inner.mixer.lock();
            if muted {
                m.ignored.insert(user);
                m.remove(user);
            } else {
                m.ignored.remove(&user);
            }
        }
        self.inner.send_cmd(self.inner.ignore_envelope());
        self.inner.emit_state();
    }

    /// Chat leeren (room 0 = alle Räume). Nur mit verknüpftem Admin-Konto, prüft der Server.
    pub fn clear_chat(&self, room: i64) {
        let mut e = Envelope::new("clear_chat");
        e.room = room;
        self.inner.send_cmd(e);
    }

    /// Jemanden rauswerfen (`ban` = auch sperren). Der Server prüft das Recht.
    pub fn moderate(&self, user: i64, ban: bool) {
        let mut e = Envelope::new(if ban { "ban" } else { "kick" });
        e.id = user;
        self.inner.send_cmd(e);
    }

    /// Taub schalten: hört niemanden und ist dabei auch stumm.
    pub fn set_deaf(&self, deaf: bool) {
        self.inner.deaf.store(deaf, Ordering::Relaxed);
        self.inner.muted.store(deaf, Ordering::Relaxed);
        self.inner.params.lock().muted = deaf;
        {
            let mut m = self.inner.mixer.lock();
            m.deaf = deaf;
            m.clear();
        }
        self.inner.send_cmd(self.inner.state_envelope());
        self.inner.emit_state();
    }

    /// Mikrofon und Lautsprecher neu öffnen (z. B. nachdem das Mikrofon erlaubt wurde).
    pub fn restart_audio(&self) {
        if self.inner.audio.lock().is_some() {
            self.stop_audio();
            self.start_audio();
        }
    }

    pub fn set_settings(&self, s: Settings) {
        let devices_changed = {
            let mut c = self.inner.config.lock();
            let changed = c.settings.input_device != s.input_device || c.settings.output_device != s.output_device;
            c.settings = s.clone();
            changed
        };
        self.inner.save_config();
        let muted = self.inner.muted.load(Ordering::Relaxed);
        *self.inner.params.lock() = params_from(&s, muted);
        self.inner.mixer.lock().volume = s.output_volume.clamp(0.0, 4.0);
        if devices_changed && self.inner.audio.lock().is_some() {
            self.stop_audio();
            self.start_audio();
        }
    }

    // --- für Tests und den Test-Bot ---

    pub fn inject_capture(&self, samples: Vec<f32>) {
        let _ = self.inner.capture_tx.send(samples);
    }

    /// Für Tests: schneller aufgeben.
    pub fn set_give_up(&self, first: Duration, lost: Duration) {
        *self.inner.give_up.lock() = (first, lost);
    }

    pub fn mixer_stats(&self) -> (u64, u64) {
        let m = self.inner.mixer.lock();
        (m.packets, m.lost)
    }

    /// Zieht n Samples aus dem Mischer und liefert deren Effektivwert.
    pub fn mixer_drain_rms(&self, n: usize) -> f32 {
        let mut m = self.inner.mixer.lock();
        let mut e = 0.0f32;
        for _ in 0..n {
            let s = m.next_sample();
            e += s * s;
        }
        (e / n.max(1) as f32).sqrt()
    }
}

impl Drop for Engine {
    fn drop(&mut self) {
        self.stop_session();
        self.stop_audio();
    }
}

/// Bricht Kind-Tasks ab, sobald die Sitzung endet (auch wenn sie selbst abgebrochen wird).
struct AbortOnDrop(tokio::task::JoinHandle<()>);

impl Drop for AbortOnDrop {
    fn drop(&mut self) {
        self.0.abort();
    }
}

/// So lange wird es beim Start versucht, bevor „nicht erreichbar“ kommt.
pub const GIVE_UP_FIRST: Duration = Duration::from_secs(45);
/// So lange nach einem Verbindungsabbruch (WLAN weg, Laptop zugeklappt …).
pub const GIVE_UP_LOST: Duration = Duration::from_secs(180);

async fn session_loop(inner: Arc<Inner>, invite: String, generation: u64) {
    let mut backoff = 1u64;
    let mut first = true;
    let mut ever_connected = false;
    // Seit wann es nicht klappt (ab Start bzw. ab dem letzten Abbruch)
    let mut failing_since = std::time::Instant::now();
    let mut attempt = 0u32;
    loop {
        if first {
            if !inner.is_current(generation) {
                return;
            }
            inner.set_status(Status::Connecting, "Verbinde …", "");
        }
        let (server, pinned, register) = {
            let c = inner.config.lock();
            let pinned = if c.pinned_fingerprint.is_empty() { None } else { hex::decode(&c.pinned_fingerprint).ok() };
            let register = (!c.registered).then(|| (c.name.clone(), invite.clone()));
            (c.server.clone(), pinned, register)
        };
        if register.as_ref().is_some_and(|(_, inv)| inv.is_empty()) {
            inner.stop_audio();
            if !inner.is_current(generation) {
                return;
            }
            inner.set_status(Status::Failed, "Für dieses Gerät brauchst du einen Einladungscode.", "need_invite");
            return;
        }
        let key = inner.key.lock().clone();
        match net::connect(&server, &key, pinned, register).await {
            Ok(sess) => {
                if !inner.is_current(generation) {
                    sess.conn.close(0u32.into(), b"bye");
                    return;
                }
                backoff = 1;
                attempt = 0;
                ever_connected = true;
                {
                    let mut c = inner.config.lock();
                    c.registered = true;
                    c.pinned_fingerprint = hex::encode(&sess.cert_hash);
                    if let Some(you) = &sess.welcome.you {
                        c.name = you.name.clone();
                    }
                }
                inner.save_config();
                match run_session(&inner, sess, generation).await {
                    SessionEnd::Kicked(reason) => {
                        inner.stop_audio();
                        if !inner.is_current(generation) {
                            return;
                        }
                        inner.set_status(Status::Failed, reason, "kicked");
                        return;
                    }
                    SessionEnd::Lost(msg) => {
                        if !inner.is_current(generation) {
                            return;
                        }
                        failing_since = std::time::Instant::now();
                        inner.set_status(Status::Reconnecting, msg, "");
                    }
                }
            }
            Err(ConnectError::Fatal { code, message }) => {
                if code == "auth_failed" {
                    inner.config.lock().registered = false;
                    inner.save_config();
                }
                inner.stop_audio();
                if !inner.is_current(generation) {
                    return;
                }
                inner.set_status(Status::Failed, message, &code);
                return;
            }
            Err(err @ (ConnectError::Temporary(_) | ConnectError::Offline)) => {
                if !inner.is_current(generation) {
                    return;
                }
                attempt += 1;
                let offline = matches!(err, ConnectError::Offline);
                let limit = if ever_connected { inner.give_up.lock().1 } else { inner.give_up.lock().0 };
                if failing_since.elapsed() >= limit {
                    inner.stop_audio();
                    let msg = if offline {
                        "Kein Netzwerk. Prüfe WLAN oder Kabel und tippe dann auf „Nochmal verbinden“."
                    } else {
                        "Der Server ist nicht erreichbar. Läuft er? Stimmt die Adresse?"
                    };
                    inner.set_status(Status::Failed, msg, if offline { "offline" } else { "unreachable" });
                    return;
                }
                let wait = backoff;
                match err {
                    ConnectError::Offline => inner.set_status(
                        Status::Reconnecting,
                        "Kein Netzwerk – prüfe WLAN oder Kabel. Talkie verbindet sich dann von selbst.",
                        "offline",
                    ),
                    ConnectError::Temporary(e) => inner.set_status(
                        Status::Reconnecting,
                        format!("{e} – neuer Versuch in {wait} s (Versuch {attempt})"),
                        "",
                    ),
                    ConnectError::Fatal { .. } => unreachable!(),
                }
            }
        }
        first = false;
        tokio::time::sleep(Duration::from_secs(backoff)).await;
        backoff = (backoff * 2).min(10);
    }
}

async fn run_session(inner: &Arc<Inner>, sess: net::Session, generation: u64) -> SessionEnd {
    let net::Session { endpoint: _endpoint, conn, mut send, mut recv, welcome, .. } = sess;
    let last_room = inner.config.lock().last_room;
    let mut my_room = 0;
    {
        let mut n = inner.net.lock();
        n.me = welcome.you.as_ref().map(|u| u.id).unwrap_or(0);
        n.rooms = welcome.rooms.clone();
        n.users = welcome.users.iter().map(|u| (u.id, u.clone())).collect();
        if let Some(you) = &welcome.you {
            n.users.insert(you.id, you.clone());
            my_room = you.room;
        }
        n.talking.clear();
        n.moderator = welcome.r#mod.unwrap_or(false);
        n.admin = welcome.admin.unwrap_or(false);
        n.chat_hours = welcome.chat_hours;
        n.status = Status::Connected;
        n.message.clear();
        n.error_code.clear();
    }
    {
        let mut g = inner.conn.lock();
        if !inner.is_current(generation) {
            drop(g);
            conn.close(0u32.into(), b"bye");
            return SessionEnd::Lost(String::new());
        }
        *g = Some(conn.clone());
    }
    inner.emit_state();

    let (tx, mut rx) = tokio::sync::mpsc::unbounded_channel::<Envelope>();
    if inner.is_current(generation) {
        *inner.cmd_tx.lock() = Some(tx.clone());
    }
    if inner.muted.load(Ordering::Relaxed) || inner.deaf.load(Ordering::Relaxed) {
        let _ = tx.send(inner.state_envelope());
    }
    if !inner.config.lock().muted_users.is_empty() {
        let _ = tx.send(inner.ignore_envelope());
    }
    if last_room != 0 && last_room != my_room && welcome.rooms.iter().any(|r| r.id == last_room) {
        let mut j = Envelope::new("join");
        j.room = last_room;
        let _ = tx.send(j);
    }

    // Sprache empfangen
    let dg_conn = conn.clone();
    let mixer = inner.mixer.clone();
    let _dg_task = AbortOnDrop(tokio::spawn(async move {
        while let Ok(d) = dg_conn.read_datagram().await {
            if let Some((uid, seq, data)) = parse_voice_in(&d) {
                mixer.lock().push(uid, seq, data);
            }
        }
    }));

    // Steuernachrichten lesen (eigene Task, weil read_exact nicht abbruchsicher ist)
    let (ftx, mut frx) = tokio::sync::mpsc::unbounded_channel::<Result<Envelope>>();
    let _reader = AbortOnDrop(tokio::spawn(async move {
        loop {
            let r = read_frame(&mut recv).await;
            let stop = r.is_err();
            if ftx.send(r).is_err() || stop {
                return;
            }
        }
    }));

    let mut tick = tokio::time::interval(Duration::from_millis(100));
    let mut ticks = 0u64;
    let end = loop {
        tokio::select! {
            f = frx.recv() => match f {
                Some(Ok(e)) => {
                    if let Some(end) = handle_message(inner, e) {
                        break end;
                    }
                }
                _ => break SessionEnd::Lost("Verbindung verloren".into()),
            },
            c = rx.recv() => {
                if let Some(env) = c {
                    if write_frame(&mut send, &env).await.is_err() {
                        break SessionEnd::Lost("Verbindung verloren".into());
                    }
                }
            }
            _ = tick.tick() => {
                ticks += 1;
                let talking = {
                    let mut m = inner.mixer.lock();
                    if ticks % 100 == 0 { m.gc(); }
                    m.talking()
                };
                let changed = {
                    let mut n = inner.net.lock();
                    if n.talking != talking { n.talking = talking; true } else { false }
                };
                if changed {
                    inner.emit_state();
                }
            }
            reason = conn.closed() => {
                break SessionEnd::Lost(format!("Verbindung verloren ({reason})"));
            }
        }
    };

    {
        let mut g = inner.conn.lock();
        if inner.is_current(generation) {
            *g = None;
            *inner.cmd_tx.lock() = None;
            inner.mixer.lock().clear();
        }
    }
    if let SessionEnd::Kicked(_) = &end {
        // Server trennt selbst; kurz warten, damit nichts hängen bleibt
        tokio::time::sleep(Duration::from_millis(100)).await;
    }
    conn.close(0u32.into(), b"bye");
    end
}

/// Hinweiston abspielen (nicht, wenn Töne aus sind oder man taub geschaltet ist).
fn play_cue(inner: &Inner, cue: Cue) {
    if !inner.config.lock().settings.sounds || inner.deaf.load(Ordering::Relaxed) {
        return;
    }
    inner.mixer.lock().play_cue(&cues::render(cue));
}

fn handle_message(inner: &Arc<Inner>, e: Envelope) -> Option<SessionEnd> {
    match e.t.as_str() {
        "user_joined" | "user_update" => {
            if let Some(u) = e.user {
                let mut save_room = None;
                let mut cue = None;
                {
                    let mut n = inner.net.lock();
                    if u.id == n.me {
                        save_room = Some(u.room);
                    } else {
                        // Kommt jemand in meinen Raum oder geht er?
                        let my = n.users.get(&n.me).map(|m| m.room).unwrap_or(0);
                        let was_here = n.users.get(&u.id).is_some_and(|p| p.room == my);
                        let now_here = u.room == my;
                        cue = match (was_here, now_here) {
                            (false, true) => Some(Cue::Join),
                            (true, false) => Some(Cue::Leave),
                            _ => None,
                        };
                    }
                    n.users.insert(u.id, u);
                }
                if let Some(c) = cue {
                    play_cue(inner, c);
                }
                if let Some(r) = save_room {
                    let changed = {
                        let mut c = inner.config.lock();
                        let ch = c.last_room != r;
                        c.last_room = r;
                        ch
                    };
                    if changed {
                        inner.save_config();
                    }
                }
                inner.emit_state();
            }
        }
        "user_left" => {
            let was_here = {
                let mut n = inner.net.lock();
                let my = n.users.get(&n.me).map(|m| m.room).unwrap_or(0);
                e.id != n.me && n.users.remove(&e.id).is_some_and(|u| u.room == my)
            };
            if was_here {
                play_cue(inner, Cue::Leave);
            }
            inner.mixer.lock().remove(e.id);
            inner.emit_state();
        }
        "rooms" => {
            inner.net.lock().rooms = e.rooms;
            inner.emit_state();
        }
        "history" => inner.emit(CoreEvent::History(e.room, e.messages)),
        "chat" => {
            if let Some(m) = e.msg {
                if m.from != inner.net.lock().me {
                    play_cue(inner, Cue::Chat);
                }
                inner.emit(CoreEvent::Chat(m));
            }
        }
        "kicked" => {
            return Some(SessionEnd::Kicked(if e.reason.is_empty() { "Du wurdest getrennt.".into() } else { e.reason }))
        }
        "error" | "info" => inner.emit(CoreEvent::Notice(e.reason)),
        // Chat geleert: leerer Verlauf (Raum 0 = alle Räume)
        "chat_cleared" => inner.emit(CoreEvent::History(e.room, Vec::new())),
        _ => {}
    }
    None
}
