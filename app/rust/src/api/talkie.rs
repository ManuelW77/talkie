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
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EventKind {
    State,
    Chat,
    History,
    Level,
    Notice,
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
            })
            .collect(),
        muted: s.muted,
        deaf: s.deaf,
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

/// Einen anderen Nutzer nur für mich stumm schalten (bleibt gespeichert).
#[flutter_rust_bridge::frb(sync)]
pub fn talkie_set_user_muted(user: i64, muted: bool) -> Result<()> {
    engine()?.set_user_muted(user, muted);
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
    });
    Ok(())
}

pub fn talkie_input_devices() -> Vec<String> {
    crate::core::device::input_devices()
}

pub fn talkie_output_devices() -> Vec<String> {
    crate::core::device::output_devices()
}
