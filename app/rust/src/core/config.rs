//! Dauerhafte Einstellungen und der Geräte-Schlüssel.

use anyhow::{Context, Result};
use ed25519_dalek::SigningKey;
use serde::{Deserialize, Serialize};
use std::fs;
use std::path::{Path, PathBuf};

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(default)]
pub struct Settings {
    /// Schwelle der Sprachaktivierung in dBFS (-70 = sehr empfindlich, -20 = unempfindlich).
    pub vad_threshold_db: f32,
    pub noise_suppression: bool,
    /// Leer = Systemstandard.
    pub input_device: String,
    pub output_device: String,
    /// Mikrofonverstärkung, 1.0 = unverändert.
    pub input_gain: f32,
    /// Lautstärke, 1.0 = unverändert.
    pub output_volume: f32,
    /// Hinweistöne bei Kommen/Gehen und neuen Chatnachrichten.
    pub sounds: bool,
    /// Push-to-Talk statt Sprach-Erkennung.
    pub ptt: bool,
    /// Taste für Push-to-Talk (siehe ptt::KEYS).
    pub ptt_key: String,
    /// Handy: Echo-Unterdrückung des Systems nutzen (ohne Kopfhörer wichtig).
    pub echo_cancel: bool,
}

impl Default for Settings {
    fn default() -> Self {
        Settings {
            vad_threshold_db: -45.0,
            noise_suppression: true,
            input_device: String::new(),
            output_device: String::new(),
            input_gain: 1.0,
            output_volume: 1.0,
            sounds: true,
            ptt: false,
            ptt_key: crate::core::ptt::DEFAULT_KEY.into(),
            echo_cancel: true,
        }
    }
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Config {
    pub server: String,
    pub name: String,
    pub registered: bool,
    /// Hex-SHA-256 des Server-Zertifikats (Trust on First Use).
    pub pinned_fingerprint: String,
    pub last_room: i64,
    /// Nutzer-IDs, die man nur für sich selbst stumm geschaltet hat.
    pub muted_users: Vec<i64>,
    /// Lautstärke pro Person (Nutzer-ID → Faktor, 1.0 = normal).
    pub user_volumes: std::collections::BTreeMap<i64, f32>,
    pub settings: Settings,
}

pub struct Storage {
    dir: PathBuf,
}

impl Storage {
    pub fn new(dir: PathBuf) -> Result<Self> {
        fs::create_dir_all(&dir).with_context(|| format!("ordner {dir:?} anlegen"))?;
        Ok(Storage { dir })
    }

    pub fn default_dir() -> PathBuf {
        directories::ProjectDirs::from("de", "talkie", "Talkie")
            .map(|p| p.data_dir().to_path_buf())
            .unwrap_or_else(|| std::env::temp_dir().join("talkie"))
    }

    fn config_path(&self) -> PathBuf {
        self.dir.join("config.json")
    }

    fn key_path(&self) -> PathBuf {
        self.dir.join("device.key")
    }

    pub fn load_config(&self) -> Config {
        fs::read(self.config_path()).ok().and_then(|b| serde_json::from_slice(&b).ok()).unwrap_or_default()
    }

    pub fn save_config(&self, c: &Config) -> Result<()> {
        write_atomic(&self.config_path(), &serde_json::to_vec_pretty(c)?)
    }

    /// Lädt den Geräte-Schlüssel oder erzeugt einen neuen.
    pub fn load_or_create_key(&self) -> Result<SigningKey> {
        if let Ok(b) = fs::read(self.key_path()) {
            if let Ok(arr) = <[u8; 32]>::try_from(b.as_slice()) {
                return Ok(SigningKey::from_bytes(&arr));
            }
        }
        let mut seed = [0u8; 32];
        getrandom::fill(&mut seed).map_err(|e| anyhow::anyhow!("zufall: {e}"))?;
        write_atomic(&self.key_path(), &seed)?;
        restrict_permissions(&self.key_path());
        Ok(SigningKey::from_bytes(&seed))
    }

    /// Vergisst den Schlüssel — danach ist eine neue Einladung nötig.
    pub fn reset_key(&self) -> Result<()> {
        match fs::remove_file(self.key_path()) {
            Err(e) if e.kind() != std::io::ErrorKind::NotFound => Err(e.into()),
            _ => Ok(()),
        }
    }
}

fn write_atomic(path: &Path, data: &[u8]) -> Result<()> {
    let tmp = path.with_extension("tmp");
    fs::write(&tmp, data)?;
    fs::rename(&tmp, path)?;
    Ok(())
}

#[cfg(unix)]
fn restrict_permissions(p: &Path) {
    use std::os::unix::fs::PermissionsExt;
    let _ = fs::set_permissions(p, fs::Permissions::from_mode(0o600));
}

#[cfg(not(unix))]
fn restrict_permissions(_p: &Path) {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn key_is_stable_and_resettable() {
        let dir = std::env::temp_dir().join(format!("talkie-test-{}", std::process::id()));
        let s = Storage::new(dir.clone()).unwrap();
        let k1 = s.load_or_create_key().unwrap();
        let k2 = s.load_or_create_key().unwrap();
        assert_eq!(k1.to_bytes(), k2.to_bytes());
        s.reset_key().unwrap();
        let k3 = s.load_or_create_key().unwrap();
        assert_ne!(k1.to_bytes(), k3.to_bytes());

        let mut c = Config::default();
        c.name = "Max".into();
        s.save_config(&c).unwrap();
        assert_eq!(s.load_config().name, "Max");
        let _ = fs::remove_dir_all(dir);
    }
}
