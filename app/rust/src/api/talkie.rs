//! Schnittstelle für Flutter (flutter_rust_bridge).

use crate::core::config::Settings;
use crate::core::engine::{CoreEvent, Engine, Snapshot, Status};
use crate::core::protocol::WireChat;
use crate::frb_generated::StreamSink;
use anyhow::{anyhow, Result};
use std::sync::{Arc, OnceLock};

static ENGINE: OnceLock<Engine> = OnceLock::new();

fn engine() -> Result<&'static Engine> {
    ENGINE.get().ok_or_else(|| anyhow!("talkie_init wurde nicht aufgerufen"))
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConnStatus {
    Idle,
    Connecting,
    Connected,
    Reconnecting,
    Failed,
}

#[derive(Debug, Clone)]
pub struct UserDto {
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
pub struct RoomDto {
    pub id: i64,
    pub name: String,
}

#[derive(Debug, Clone)]
pub struct ChatDto {
    pub room: i64,
    pub from: i64,
    pub name: String,
    pub text: String,
    pub ts: i64,
}

#[derive(Debug, Clone)]
pub struct StateDto {
    pub status: ConnStatus,
    pub message: String,
    pub error_code: String,
    pub me: i64,
    pub room: i64,
    pub rooms: Vec<RoomDto>,
    pub users: Vec<UserDto>,
    pub muted: bool,
    pub deaf: bool,
    /// Darf andere rauswerfen/sperren.
    pub moderator: bool,
    /// Verknüpftes Admin-Konto (darf den Chat leeren).
    pub admin: bool,
    /// So viele Stunden bleibt der Chat erhalten (0 = unbegrenzt).
    pub chat_hours: i64,
    /// Neueste App-Version laut Server ("" = unbekannt) und Download-Adresse.
    pub latest_version: String,
    pub latest_url: String,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EventKind {
    State,
    Chat,
    History,
    Level,
    Notice,
    /// Geräte-Code (steht in `notice`).
    DeviceCode,
}

/// Ein Ereignis an die Oberfläche; welche Felder gefüllt sind, hängt von `kind` ab.
#[derive(Debug, Clone)]
pub struct UiEvent {
    pub kind: EventKind,
    pub state: Option<StateDto>,
    pub chat: Option<ChatDto>,
    pub history_room: i64,
    pub history: Vec<ChatDto>,
    pub level: f32,
    pub notice: String,
}

#[derive(Debug, Clone)]
pub struct SettingsDto {
    pub vad_threshold_db: f32,
    pub noise_suppression: bool,
    pub input_device: String,
    pub output_device: String,
    pub input_gain: f32,
    pub output_volume: f32,
    /// Hinweistöne bei Kommen/Gehen und neuen Chatnachrichten.
    pub sounds: bool,
    /// Push-to-Talk statt Sprach-Erkennung, und die Taste dafür.
    pub ptt: bool,
    pub ptt_key: String,
    /// Handy: Echo-Unterdrückung des Systems (ohne Kopfhörer wichtig).
    pub echo_cancel: bool,
    /// Mikrofon stumm, solange die App nicht im Vordergrund ist.
    pub mute_in_background: bool,
}

#[derive(Debug, Clone)]
pub struct PttKeyDto {
    pub id: String,
    pub label: String,
}

#[derive(Debug, Clone)]
pub struct ConfigDto {
    pub server: String,
    pub name: String,
    pub registered: bool,
    pub settings: SettingsDto,
}

fn chat_dto(m: WireChat) -> ChatDto {
    ChatDto { room: m.room, from: m.from, name: m.name, text: m.text, ts: m.ts }
}

fn state_dto(s: Snapshot) -> StateDto {
    StateDto {
        status: match s.status {
            Status::Idle => ConnStatus::Idle,
            Status::Connecting => ConnStatus::Connecting,
            Status::Connected => ConnStatus::Connected,
            Status::Reconnecting => ConnStatus::Reconnecting,
            Status::Failed => ConnStatus::Failed,
        },
        message: s.message,
        error_code: s.error_code,
        me: s.me,
        room: s.room,
        rooms: s.rooms.into_iter().map(|r| RoomDto { id: r.id, name: r.name }).collect(),
        users: s
            .users
            .into_iter()
            .map(|u| UserDto {
                id: u.id,
                name: u.name,
                room: u.room,
                muted: u.muted,
                deaf: u.deaf,
                talking: u.talking,
                local_muted: u.local_muted,
                volume: u.volume,
            })
            .collect(),
        muted: s.muted,
        deaf: s.deaf,
        moderator: s.moderator,
        admin: s.admin,
        chat_hours: s.chat_hours,
        latest_version: s.latest_version,
        latest_url: s.latest_url,
    }
}

fn ui_event(e: CoreEvent) -> UiEvent {
    let mut ev = UiEvent {
        kind: EventKind::Notice,
        state: None,
        chat: None,
        history_room: 0,
        history: Vec::new(),
        level: 0.0,
        notice: String::new(),
    };
    match e {
        CoreEvent::State(s) => {
            ev.kind = EventKind::State;
            ev.state = Some(state_dto(s));
        }
        CoreEvent::Chat(m) => {
            ev.kind = EventKind::Chat;
            ev.chat = Some(chat_dto(m));
        }
        CoreEvent::History(room, msgs) => {
            ev.kind = EventKind::History;
            ev.history_room = room;
            ev.history = msgs.into_iter().map(chat_dto).collect();
        }
        CoreEvent::Level(l) => {
            ev.kind = EventKind::Level;
            ev.level = l;
        }
        CoreEvent::Notice(n) => ev.notice = n,
        CoreEvent::DeviceCode(c) => {
            ev.kind = EventKind::DeviceCode;
            ev.notice = c;
        }
    }
    ev
}

fn settings_dto(s: &Settings) -> SettingsDto {
    SettingsDto {
        vad_threshold_db: s.vad_threshold_db,
        noise_suppression: s.noise_suppression,
        input_device: s.input_device.clone(),
        output_device: s.output_device.clone(),
        input_gain: s.input_gain,
        output_volume: s.output_volume,
        sounds: s.sounds,
        ptt: s.ptt,
        ptt_key: s.ptt_key.clone(),
        echo_cancel: s.echo_cancel,
        mute_in_background: s.mute_in_background,
    }
}

#[flutter_rust_bridge::frb(init)]
pub fn init_app() {
    flutter_rust_bridge::setup_default_user_utils();
}

/// Startet die Engine (einmal pro Programmstart) und liefert die gespeicherte Konfiguration.
/// `data_dir` leer = Standardordner des Systems.
pub fn talkie_init(data_dir: String) -> Result<ConfigDto> {
    if ENGINE.get().is_none() {
        let dir = if data_dir.is_empty() { crate::core::config::Storage::default_dir() } else { data_dir.into() };
        let e = Engine::new(dir, true)?;
        let _ = ENGINE.set(e);
    }
    talkie_config()
}

#[flutter_rust_bridge::frb(sync)]
pub fn talkie_config() -> Result<ConfigDto> {
    let c = engine()?.config();
    Ok(ConfigDto { server: c.server, name: c.name, registered: c.registered, settings: settings_dto(&c.settings) })
}

/// Ereignisstrom für die Oberfläche. Ein neuer Aufruf ersetzt den alten.
pub fn talkie_events(sink: StreamSink<UiEvent>) -> Result<()> {
    let sink = Arc::new(sink);
    engine()?.set_emitter(Arc::new(move |e| {
        let _ = sink.add(ui_event(e));
    }));
    Ok(())
}

/// `invite` leer = bekanntes Gerät meldet sich an. `fingerprint` leer = Trust on First Use.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_connect(server: String, name: String, invite: String, fingerprint: String) -> Result<()> {
    engine()?.connect(&server, &name, &invite, &fingerprint)
}

#[flutter_rust_bridge::frb(sync)]
pub fn talkie_disconnect() -> Result<()> {
    engine()?.disconnect();
    Ok(())
}

pub fn talkie_logout() -> Result<()> {
    engine()?.logout()
}

#[flutter_rust_bridge::frb(sync)]
pub fn talkie_join(room: i64) -> Result<()> {
    engine()?.join(room);
    Ok(())
}

#[flutter_rust_bridge::frb(sync)]
pub fn talkie_chat(text: String) -> Result<()> {
    engine()?.chat(&text);
    Ok(())
}

#[flutter_rust_bridge::frb(sync)]
pub fn talkie_set_muted(muted: bool) -> Result<()> {
    engine()?.set_muted(muted);
    Ok(())
}

/// Lautstärke einer Person nur für mich (0.0 … 3.0, 1.0 = normal). Bleibt gespeichert.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_set_user_volume(user: i64, volume: f32) -> Result<()> {
    engine()?.set_user_volume(user, volume);
    Ok(())
}

/// Einen anderen Nutzer nur für mich stumm schalten (bleibt gespeichert).
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_set_user_muted(user: i64, muted: bool) -> Result<()> {
    engine()?.set_user_muted(user, muted);
    Ok(())
}

/// Code für ein weiteres Gerät anfordern; kommt als Ereignis DeviceCode zurück.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_request_device_code() -> Result<()> {
    engine()?.request_device_code();
    Ok(())
}

/// Push-to-Talk-Knopf in der App gedrückt halten / loslassen.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_set_ptt_pressed(down: bool) -> Result<()> {
    engine()?.set_ptt_pressed(down);
    Ok(())
}

/// Wählbare Push-to-Talk-Tasten.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_ptt_keys() -> Vec<PttKeyDto> {
    crate::core::ptt::KEYS.iter().map(|(id, label)| PttKeyDto { id: (*id).into(), label: (*label).into() }).collect()
}

/// "ok", "permission" (macOS: Eingabeüberwachung fehlt) oder "unsupported" (nur Knopf in der App).
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_ptt_access() -> String {
    use crate::core::ptt::Access;
    match crate::core::ptt::access() {
        Access::Ok => "ok",
        Access::NeedsPermission => "permission",
        Access::Unsupported => "unsupported",
    }
    .into()
}

/// macOS: nach „Eingabeüberwachung“ fragen.
pub fn talkie_ptt_request_access() {
    crate::core::ptt::request_access();
}

/// Chat leeren (room 0 = alle Räume). Ergebnis kommt als Hinweis zurück.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_clear_chat(room: i64) -> Result<()> {
    engine()?.clear_chat(room);
    Ok(())
}

/// Jemanden rauswerfen (`ban` = auch sperren). Ergebnis kommt als Hinweis zurück.
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_moderate(user: i64, ban: bool) -> Result<()> {
    engine()?.moderate(user, ban);
    Ok(())
}

/// Audio neu starten, z. B. nachdem das Mikrofon erlaubt wurde.
pub fn talkie_restart_audio() -> Result<()> {
    engine()?.restart_audio();
    Ok(())
}

#[flutter_rust_bridge::frb(sync)]
pub fn talkie_set_deaf(deaf: bool) -> Result<()> {
    engine()?.set_deaf(deaf);
    Ok(())
}

pub fn talkie_set_settings(s: SettingsDto) -> Result<()> {
    engine()?.set_settings(Settings {
        vad_threshold_db: s.vad_threshold_db,
        noise_suppression: s.noise_suppression,
        input_device: s.input_device,
        output_device: s.output_device,
        input_gain: s.input_gain,
        output_volume: s.output_volume,
        sounds: s.sounds,
        ptt: s.ptt,
        ptt_key: s.ptt_key,
        echo_cancel: s.echo_cancel,
        mute_in_background: s.mute_in_background,
    });
    Ok(())
}

pub fn talkie_input_devices() -> Vec<String> {
    crate::core::device::input_devices()
}

pub fn talkie_output_devices() -> Vec<String> {
    crate::core::device::output_devices()
}
