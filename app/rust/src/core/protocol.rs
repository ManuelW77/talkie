//! Talkie-Protokoll v1 — siehe docs/PROTOCOL.md.

use anyhow::{bail, Result};
use serde::{Deserialize, Serialize};
use tokio::io::{AsyncReadExt, AsyncWriteExt};

pub const PROTOCOL_VERSION: i32 = 1;
pub const ALPN: &[u8] = b"talkie/1";
pub const MAX_FRAME: usize = 64 * 1024;
pub const AUTH_CONTEXT: &[u8] = b"talkie-auth-v1";
pub const DATAGRAM_VOICE: u8 = 0x01;
pub const DEFAULT_PORT: u16 = 7777;

#[derive(Debug, Clone, Default, Serialize, Deserialize, PartialEq)]
pub struct WireUser {
    pub id: i64,
    pub name: String,
    #[serde(default)]
    pub room: i64,
    #[serde(default)]
    pub muted: bool,
    #[serde(default)]
    pub deaf: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct WireRoom {
    pub id: i64,
    pub name: String,
}

#[derive(Debug, Clone, Serialize, Deserialize, PartialEq)]
pub struct WireChat {
    pub room: i64,
    pub from: i64,
    pub name: String,
    pub text: String,
    pub ts: i64,
}

/// Obermenge aller Steuernachrichten, Feld `t` ist der Typ.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Envelope {
    pub t: String,
    #[serde(default, skip_serializing_if = "is_zero_i32")]
    pub v: i32,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub pubkey: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub nonce: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub known: Option<bool>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub sig: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub name: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub invite: String,
    /// register: Art des Geräts (nur zur Anzeige)
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub device: String,

    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub you: Option<WireUser>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub user: Option<WireUser>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub users: Vec<WireUser>,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub rooms: Vec<WireRoom>,
    #[serde(default, skip_serializing_if = "is_zero_i64")]
    pub id: i64,
    #[serde(default, skip_serializing_if = "is_zero_i64")]
    pub room: i64,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub messages: Vec<WireChat>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub msg: Option<WireChat>,

    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub text: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub muted: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub deaf: Option<bool>,

    /// ignore: Nutzer, die dieser Client für sich stumm geschaltet hat
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub ids: Vec<i64>,
    /// welcome: darf rauswerfen/sperren
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub r#mod: Option<bool>,
    /// welcome: verknüpftes Admin-Konto (darf den Chat leeren)
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub admin: Option<bool>,
    /// welcome: so viele Stunden bleibt der Chat erhalten (0 = unbegrenzt)
    #[serde(default, skip_serializing_if = "is_zero_i64")]
    pub chat_hours: i64,
    /// welcome: neueste App-Version und wo es sie gibt
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub latest: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub latest_url: String,

    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub code: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason: String,
}

fn is_zero_i32(v: &i32) -> bool {
    *v == 0
}
fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}

impl Envelope {
    pub fn new(t: &str) -> Self {
        Envelope { t: t.to_string(), ..Default::default() }
    }
}

pub fn encode_frame(e: &Envelope) -> Result<Vec<u8>> {
    let body = serde_json::to_vec(e)?;
    if body.len() > MAX_FRAME {
        bail!("nachricht zu groß");
    }
    let mut out = Vec::with_capacity(4 + body.len());
    out.extend_from_slice(&(body.len() as u32).to_be_bytes());
    out.extend_from_slice(&body);
    Ok(out)
}

pub async fn read_frame<R: AsyncReadExt + Unpin>(r: &mut R) -> Result<Envelope> {
    let mut hdr = [0u8; 4];
    r.read_exact(&mut hdr).await?;
    let n = u32::from_be_bytes(hdr) as usize;
    if n == 0 || n > MAX_FRAME {
        bail!("ungültige nachrichtengröße {n}");
    }
    let mut buf = vec![0u8; n];
    r.read_exact(&mut buf).await?;
    Ok(serde_json::from_slice(&buf)?)
}

pub async fn write_frame<W: AsyncWriteExt + Unpin>(w: &mut W, e: &Envelope) -> Result<()> {
    w.write_all(&encode_frame(e)?).await?;
    Ok(())
}

pub fn auth_message(nonce: &[u8], cert_hash: &[u8]) -> Vec<u8> {
    let mut m = Vec::with_capacity(AUTH_CONTEXT.len() + nonce.len() + cert_hash.len());
    m.extend_from_slice(AUTH_CONTEXT);
    m.extend_from_slice(nonce);
    m.extend_from_slice(cert_hash);
    m
}

/// Client → Server: [0x01][seq u16][opus]
pub fn voice_out(seq: u16, opus: &[u8]) -> Vec<u8> {
    let mut p = Vec::with_capacity(3 + opus.len());
    p.push(DATAGRAM_VOICE);
    p.extend_from_slice(&seq.to_be_bytes());
    p.extend_from_slice(opus);
    p
}

/// Server → Client: [0x01][user u32][seq u16][opus]
pub fn parse_voice_in(d: &[u8]) -> Option<(i64, u16, &[u8])> {
    if d.len() < 8 || d[0] != DATAGRAM_VOICE {
        return None;
    }
    let uid = u32::from_be_bytes([d[1], d[2], d[3], d[4]]) as i64;
    let seq = u16::from_be_bytes([d[5], d[6]]);
    Some((uid, seq, &d[7..]))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn envelope_skips_empty_fields() {
        let mut e = Envelope::new("join");
        e.room = 3;
        assert_eq!(serde_json::to_string(&e).unwrap(), r#"{"t":"join","room":3}"#);
    }

    #[test]
    fn parses_server_welcome() {
        let j = r#"{"t":"welcome","you":{"id":1,"name":"A","room":2,"muted":false,"deaf":false},
            "rooms":[{"id":2,"name":"Lobby"}],"users":[{"id":1,"name":"A","room":2,"muted":false,"deaf":false}]}"#;
        let e: Envelope = serde_json::from_str(j).unwrap();
        assert_eq!(e.you.unwrap().name, "A");
        assert_eq!(e.rooms[0].name, "Lobby");
    }

    #[test]
    fn voice_roundtrip() {
        let mut d = vec![DATAGRAM_VOICE, 0, 0, 0, 42];
        d.extend_from_slice(&voice_out(513, b"xyz")[1..]);
        let (uid, seq, data) = parse_voice_in(&d).unwrap();
        assert_eq!((uid, seq, data), (42, 513, &b"xyz"[..]));
    }
}
