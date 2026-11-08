//! QUIC-Verbindung, Zertifikats-Pinning (TOFU) und Anmeldung.

use crate::core::protocol::*;
use anyhow::{anyhow, Result};
use base64::{engine::general_purpose::STANDARD as B64, Engine as _};
use ed25519_dalek::{Signer, SigningKey};
use parking_lot::Mutex;
use rustls::client::danger::{HandshakeSignatureValid, ServerCertVerified, ServerCertVerifier};
use rustls::crypto::CryptoProvider;
use rustls::pki_types::{CertificateDer, ServerName, UnixTime};
use rustls::{DigitallySignedStruct, SignatureScheme};
use sha2::{Digest, Sha256};
use std::net::SocketAddr;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::Arc;
use std::time::Duration;

/// Prüft das Server-Zertifikat gegen einen gepinnten Fingerabdruck.
/// Ohne Pin wird der gesehene Fingerabdruck gemerkt (Trust on First Use).
/// Die TLS-Signatur wird immer echt geprüft, damit der Server den Schlüssel besitzen muss.
#[derive(Debug)]
pub struct PinVerifier {
    expected: Option<Vec<u8>>,
    seen: Mutex<Option<Vec<u8>>>,
    mismatch: AtomicBool,
    provider: Arc<CryptoProvider>,
}

impl ServerCertVerifier for PinVerifier {
    fn verify_server_cert(
        &self,
        end_entity: &CertificateDer<'_>,
        _intermediates: &[CertificateDer<'_>],
        _server_name: &ServerName<'_>,
        _ocsp: &[u8],
        _now: UnixTime,
    ) -> Result<ServerCertVerified, rustls::Error> {
        let h = Sha256::digest(end_entity.as_ref()).to_vec();
        if let Some(exp) = &self.expected {
            if *exp != h {
                self.mismatch.store(true, Ordering::SeqCst);
                return Err(rustls::Error::General("fingerprint_mismatch".into()));
            }
        }
        *self.seen.lock() = Some(h);
        Ok(ServerCertVerified::assertion())
    }

    fn verify_tls12_signature(
        &self,
        message: &[u8],
        cert: &CertificateDer<'_>,
        dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls12_signature(message, cert, dss, &self.provider.signature_verification_algorithms)
    }

    fn verify_tls13_signature(
        &self,
        message: &[u8],
        cert: &CertificateDer<'_>,
        dss: &DigitallySignedStruct,
    ) -> Result<HandshakeSignatureValid, rustls::Error> {
        rustls::crypto::verify_tls13_signature(message, cert, dss, &self.provider.signature_verification_algorithms)
    }

    fn supported_verify_schemes(&self) -> Vec<SignatureScheme> {
        self.provider.signature_verification_algorithms.supported_schemes()
    }
}

/// Fehler beim Verbinden: `Fatal` braucht ein Eingreifen (z. B. neuer Code),
/// `Temporary` wird automatisch erneut versucht.
#[derive(Debug)]
pub enum ConnectError {
    Fatal { code: String, message: String },
    Temporary(anyhow::Error),
}

impl From<anyhow::Error> for ConnectError {
    fn from(e: anyhow::Error) -> Self {
        ConnectError::Temporary(e)
    }
}

impl std::fmt::Display for ConnectError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            ConnectError::Fatal { code, message } => write!(f, "{message} ({code})"),
            ConnectError::Temporary(e) => write!(f, "{e}"),
        }
    }
}

fn fatal(code: &str, message: &str) -> ConnectError {
    ConnectError::Fatal { code: code.into(), message: message.into() }
}

pub struct Session {
    pub endpoint: quinn::Endpoint,
    pub conn: quinn::Connection,
    pub send: quinn::SendStream,
    pub recv: quinn::RecvStream,
    pub welcome: Envelope,
    pub cert_hash: Vec<u8>,
}

/// Zerlegt "host", "host:port" oder "[v6]:port".
pub fn split_host_port(server: &str) -> (String, u16) {
    let s = server.trim();
    let s = s.strip_prefix("talkie://").unwrap_or(s).trim_end_matches('/');
    if let Some(rest) = s.strip_prefix('[') {
        if let Some((h, p)) = rest.split_once(']') {
            let port = p.strip_prefix(':').and_then(|p| p.parse().ok()).unwrap_or(DEFAULT_PORT);
            return (h.to_string(), port);
        }
    }
    match s.rsplit_once(':') {
        Some((h, p)) if !h.contains(':') => (h.to_string(), p.parse().unwrap_or(DEFAULT_PORT)),
        _ => (s.to_string(), DEFAULT_PORT),
    }
}

async fn resolve(server: &str) -> Result<SocketAddr, ConnectError> {
    let (host, port) = split_host_port(server);
    if host.is_empty() {
        return Err(fatal("bad_server", "Bitte gib eine Serveradresse ein."));
    }
    let mut addrs = tokio::net::lookup_host((host.as_str(), port))
        .await
        .map_err(|e| ConnectError::Temporary(anyhow!("Server „{host}“ nicht gefunden: {e}")))?;
    let all: Vec<SocketAddr> = addrs.by_ref().collect();
    // IPv4 bevorzugen: funktioniert in mehr Heimnetzen zuverlässig
    all.iter()
        .find(|a| a.is_ipv4())
        .or(all.first())
        .copied()
        .ok_or_else(|| ConnectError::Temporary(anyhow!("Server „{host}“ nicht gefunden")))
}

pub fn parse_fingerprint(s: &str) -> Option<Vec<u8>> {
    let clean: String = s.chars().filter(|c| c.is_ascii_hexdigit()).collect();
    let b = hex::decode(clean).ok()?;
    (b.len() == 32).then_some(b)
}

/// Verbindet, prüft das Zertifikat und meldet an.
/// `register = Some((name, invite))` registriert ein neues Gerät.
pub async fn connect(
    server: &str,
    key: &SigningKey,
    pinned: Option<Vec<u8>>,
    register: Option<(String, String)>,
) -> Result<Session, ConnectError> {
    let addr = resolve(server).await?;

    let provider = Arc::new(rustls::crypto::ring::default_provider());
    let verifier = Arc::new(PinVerifier {
        expected: pinned,
        seen: Mutex::new(None),
        mismatch: AtomicBool::new(false),
        provider: provider.clone(),
    });
    let mut tls = rustls::ClientConfig::builder_with_provider(provider)
        .with_protocol_versions(&[&rustls::version::TLS13])
        .map_err(|e| anyhow!("tls: {e}"))?
        .dangerous()
        .with_custom_certificate_verifier(verifier.clone())
        .with_no_client_auth();
    tls.alpn_protocols = vec![ALPN.to_vec()];
    let qcc = quinn::crypto::rustls::QuicClientConfig::try_from(tls).map_err(|e| anyhow!("quic-tls: {e}"))?;
    let mut cc = quinn::ClientConfig::new(Arc::new(qcc));
    let mut tc = quinn::TransportConfig::default();
    tc.keep_alive_interval(Some(Duration::from_secs(10)));
    tc.max_idle_timeout(Some(Duration::from_secs(30).try_into().map_err(|e| anyhow!("{e}"))?));
    cc.transport_config(Arc::new(tc));

    let bind: SocketAddr = if addr.is_ipv4() { "0.0.0.0:0".parse().unwrap() } else { "[::]:0".parse().unwrap() };
    let endpoint = quinn::Endpoint::client(bind).map_err(|e| anyhow!("udp-socket: {e}"))?;

    let connecting = endpoint
        .connect_with(cc, addr, "talkie")
        .map_err(|e| anyhow!("verbinden: {e}"))?;
    let conn = match tokio::time::timeout(Duration::from_secs(8), connecting).await {
        Err(_) => return Err(ConnectError::Temporary(anyhow!("Server antwortet nicht"))),
        Ok(Err(e)) => {
            if verifier.mismatch.load(Ordering::SeqCst) {
                return Err(fatal(
                    "fingerprint_mismatch",
                    "Der Server hat sich verändert und könnte ein Betrüger sein. Frag deinen Admin!",
                ));
            }
            return Err(ConnectError::Temporary(anyhow!("Verbindung fehlgeschlagen: {e}")));
        }
        Ok(Ok(c)) => c,
    };
    let cert_hash = verifier.seen.lock().clone().ok_or_else(|| anyhow!("kein zertifikat"))?;

    let hs = async {
        let (mut send, mut recv) = conn.open_bi().await.map_err(|e| anyhow!("{e}"))?;
        let mut hello = Envelope::new("hello");
        hello.v = PROTOCOL_VERSION;
        hello.pubkey = B64.encode(key.verifying_key().as_bytes());
        write_frame(&mut send, &hello).await?;

        let ch = read_frame(&mut recv).await?;
        if ch.t == "error" {
            return Ok::<_, anyhow::Error>(Err(ConnectError::Fatal { code: ch.code, message: ch.reason }));
        }
        if ch.t != "challenge" {
            return Err(anyhow!("unerwartete antwort {}", ch.t));
        }
        let nonce = B64.decode(&ch.nonce)?;
        let sig = key.sign(&auth_message(&nonce, &cert_hash));
        let mut resp = match &register {
            Some((name, invite)) if !ch.known.unwrap_or(false) => {
                let mut r = Envelope::new("register");
                r.name = name.clone();
                r.invite = invite.clone();
                r
            }
            _ => Envelope::new("auth"),
        };
        resp.sig = B64.encode(sig.to_bytes());
        write_frame(&mut send, &resp).await?;

        let w = read_frame(&mut recv).await?;
        match w.t.as_str() {
            "welcome" => Ok(Ok((send, recv, w))),
            "error" => Ok(Err(ConnectError::Fatal { code: w.code, message: w.reason })),
            other => Err(anyhow!("unerwartete antwort {other}")),
        }
    };
    let (send, recv, welcome) = match tokio::time::timeout(Duration::from_secs(10), hs).await {
        Err(_) => return Err(ConnectError::Temporary(anyhow!("Anmeldung dauert zu lange"))),
        Ok(Err(e)) => return Err(ConnectError::Temporary(e)),
        Ok(Ok(Err(ConnectError::Fatal { code, message }))) if code == "server_full" => {
            return Err(ConnectError::Temporary(anyhow!(message)))
        }
        Ok(Ok(Err(e))) => return Err(e),
        Ok(Ok(Ok(v))) => v,
    };
    Ok(Session { endpoint, conn, send, recv, welcome, cert_hash })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn host_port() {
        assert_eq!(split_host_port("example.org"), ("example.org".into(), 7777));
        assert_eq!(split_host_port(" example.org:1234 "), ("example.org".into(), 1234));
        assert_eq!(split_host_port("talkie://1.2.3.4:99/"), ("1.2.3.4".into(), 99));
        assert_eq!(split_host_port("[::1]:5"), ("::1".into(), 5));
        assert_eq!(split_host_port("::1"), ("::1".into(), 7777));
    }

    #[test]
    fn fingerprint_parsing() {
        let fp = "7CC5:BD21:7105:6694:1402:D964:D9E5:C090:2E80:8252:8693:9B14:2759:5F8B:919E:C23E";
        assert_eq!(parse_fingerprint(fp).unwrap().len(), 32);
        assert!(parse_fingerprint("abc").is_none());
    }
}
