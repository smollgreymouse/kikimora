use serde::{Deserialize, Serialize};
use std::net::IpAddr;
use std::path::{Path, PathBuf};
use std::str::FromStr;
use thiserror::Error;

const DEFAULT_MTU: u16 = 1380;
const DEFAULT_QUEUE_PACKETS: usize = 256;
const DEFAULT_QUEUE_BYTES: usize = 2 * 1024 * 1024;

#[derive(Debug, Clone, Copy, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum ProtocolKind {
    Stub,
    Amneziawg2,
    VlessReality,
}

impl ProtocolKind {
    pub fn as_str(self) -> &'static str {
        match self {
            Self::Stub => "stub",
            Self::Amneziawg2 => "amneziawg2",
            Self::VlessReality => "vless-reality",
        }
    }
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct ClientConfig {
    pub name: String,
    pub protocol: ProtocolKind,
    pub interface: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub address: Vec<String>,
    #[serde(default = "default_mtu")]
    pub mtu: u16,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub state_dir: Option<PathBuf>,
    #[serde(default = "default_queue_packets")]
    pub queue_packets: usize,
    #[serde(default = "default_queue_bytes")]
    pub queue_bytes: usize,
    #[serde(default, skip_serializing_if = "StubConfig::is_default")]
    pub stub: StubConfig,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub awg2: Option<Awg2Config>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub vless_reality: Option<VlessRealityConfig>,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct Awg2Config {
    pub private_key: String,
    pub peer_public_key: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub preshared_key: Option<String>,
    pub endpoint: String,
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub allowed_ips: Vec<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub persistent_keepalive: Option<u16>,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub jc: u32,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub jmin: u32,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub jmax: u32,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub s1: u32,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub s2: u32,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub s3: u32,
    #[serde(default, skip_serializing_if = "is_zero_u32")]
    pub s4: u32,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub h1: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub h2: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub h3: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub h4: Option<String>,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub i1: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub i2: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub i3: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub i4: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub i5: String,
}

#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct VlessRealityConfig {
    pub endpoint: String,
    pub uuid: String,
    pub server_name: String,
    pub public_key: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub short_id: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub flow: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub fingerprint: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub spider_x: Option<String>,
    #[serde(default = "default_transport")]
    pub transport: String,
}

#[derive(Debug, Clone, Deserialize, Serialize, PartialEq, Eq)]
pub struct StubConfig {
    #[serde(default = "default_stub_mode")]
    pub mode: String,
    #[serde(default = "default_stub_reconnect_ms")]
    pub reconnect_after_ms: u64,
}

impl StubConfig {
    fn is_default(value: &Self) -> bool {
        value == &Self::default()
    }
}

impl Default for StubConfig {
    fn default() -> Self {
        Self {
            mode: default_stub_mode(),
            reconnect_after_ms: default_stub_reconnect_ms(),
        }
    }
}

#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("failed to read config {path}: {source}")]
    Read {
        path: PathBuf,
        source: std::io::Error,
    },
    #[error("failed to parse config {path}: {source}")]
    Parse {
        path: PathBuf,
        source: toml::de::Error,
    },
    #[error("invalid configuration: {0}")]
    Invalid(String),
}

impl ClientConfig {
    pub fn load(path: &Path) -> Result<Self, ConfigError> {
        let text = std::fs::read_to_string(path).map_err(|source| ConfigError::Read {
            path: path.to_path_buf(),
            source,
        })?;
        let config: Self = toml::from_str(&text).map_err(|source| ConfigError::Parse {
            path: path.to_path_buf(),
            source,
        })?;
        config.validate()?;
        Ok(config)
    }

    pub fn runtime_state_dir(&self) -> PathBuf {
        self.state_dir
            .clone()
            .unwrap_or_else(|| PathBuf::from(format!("/run/kikimora/vpn/clients/{}", self.name)))
    }

    pub fn validate(&self) -> Result<(), ConfigError> {
        validate_instance_name(&self.name)?;
        validate_interface_name(&self.interface)?;
        if !(576..=9000).contains(&self.mtu) {
            return Err(ConfigError::Invalid(format!(
                "MTU {} is outside supported range 576..9000",
                self.mtu
            )));
        }
        if self.queue_packets == 0 || self.queue_bytes == 0 {
            return Err(ConfigError::Invalid(
                "packet and byte queue limits must be positive".to_string(),
            ));
        }
        for address in &self.address {
            validate_cidr(address, "address")?;
        }

        match self.protocol {
            ProtocolKind::Stub => validate_stub(&self.stub),
            ProtocolKind::Amneziawg2 => {
                let awg = self
                    .awg2
                    .as_ref()
                    .ok_or_else(|| ConfigError::Invalid("missing [awg2] section".to_string()))?;
                validate_awg2(awg)
            }
            ProtocolKind::VlessReality => {
                let vless = self.vless_reality.as_ref().ok_or_else(|| {
                    ConfigError::Invalid("missing [vless_reality] section".to_string())
                })?;
                validate_vless_reality(vless)
            }
        }
    }
}

fn validate_stub(stub: &StubConfig) -> Result<(), ConfigError> {
    match stub.mode.as_str() {
        "online" | "reconnect-once" | "reconnect-loop" | "blackhole" => Ok(()),
        other => Err(ConfigError::Invalid(format!(
            "unsupported stub mode: {other}"
        ))),
    }
}

fn validate_awg2(awg: &Awg2Config) -> Result<(), ConfigError> {
    for (name, value) in [
        ("private_key", awg.private_key.as_str()),
        ("peer_public_key", awg.peer_public_key.as_str()),
        ("endpoint", awg.endpoint.as_str()),
    ] {
        if value.trim().is_empty() {
            return Err(ConfigError::Invalid(format!("awg2.{name} is empty")));
        }
    }
    if awg.jmin > awg.jmax {
        return Err(ConfigError::Invalid(
            "awg2.jmin cannot exceed awg2.jmax".to_string(),
        ));
    }
    for cidr in &awg.allowed_ips {
        validate_cidr(cidr, "awg2.allowed_ips")?;
    }
    for (name, value) in [
        ("h1", awg.h1.as_deref()),
        ("h2", awg.h2.as_deref()),
        ("h3", awg.h3.as_deref()),
        ("h4", awg.h4.as_deref()),
    ] {
        if let Some(value) = value {
            validate_u32_range(value, &format!("awg2.{name}"))?;
        }
    }
    Ok(())
}

fn validate_vless_reality(config: &VlessRealityConfig) -> Result<(), ConfigError> {
    for (name, value) in [
        ("endpoint", config.endpoint.as_str()),
        ("uuid", config.uuid.as_str()),
        ("server_name", config.server_name.as_str()),
        ("public_key", config.public_key.as_str()),
    ] {
        if value.trim().is_empty() {
            return Err(ConfigError::Invalid(format!(
                "vless_reality.{name} is empty"
            )));
        }
    }
    match config.transport.as_str() {
        "tcp" | "raw" => Ok(()),
        other => Err(ConfigError::Invalid(format!(
            "unsupported VLESS/REALITY transport in Stage 0: {other}"
        ))),
    }
}

fn validate_instance_name(name: &str) -> Result<(), ConfigError> {
    if name.is_empty() || name.len() > 64 || name == "." || name == ".." {
        return Err(ConfigError::Invalid(format!(
            "invalid instance name: {name:?}"
        )));
    }
    if !name
        .bytes()
        .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'_' | b'-' | b'.'))
    {
        return Err(ConfigError::Invalid(format!(
            "invalid instance name: {name:?}"
        )));
    }
    Ok(())
}

fn validate_interface_name(name: &str) -> Result<(), ConfigError> {
    if name.is_empty() || name.len() > 15 || name == "." || name == ".." {
        return Err(ConfigError::Invalid(format!(
            "invalid Linux interface name: {name:?}"
        )));
    }
    if !name
        .bytes()
        .all(|c| c.is_ascii_alphanumeric() || matches!(c, b'_' | b'-' | b'.'))
    {
        return Err(ConfigError::Invalid(format!(
            "invalid Linux interface name: {name:?}"
        )));
    }
    Ok(())
}

fn validate_cidr(value: &str, field: &str) -> Result<(), ConfigError> {
    let (ip, prefix) = value
        .split_once('/')
        .ok_or_else(|| ConfigError::Invalid(format!("{field} entry must be CIDR: {value}")))?;
    let ip = IpAddr::from_str(ip)
        .map_err(|_| ConfigError::Invalid(format!("{field} contains invalid address: {value}")))?;
    let prefix: u8 = prefix
        .parse()
        .map_err(|_| ConfigError::Invalid(format!("{field} contains invalid prefix: {value}")))?;
    let max = if ip.is_ipv4() { 32 } else { 128 };
    if prefix > max {
        return Err(ConfigError::Invalid(format!(
            "{field} prefix is too large: {value}"
        )));
    }
    Ok(())
}

fn validate_u32_range(value: &str, field: &str) -> Result<(), ConfigError> {
    let parse = |s: &str| {
        s.trim()
            .parse::<u32>()
            .map_err(|_| ConfigError::Invalid(format!("{field} must be N or MIN-MAX: {value}")))
    };
    let (min, max) = if let Some((min, max)) = value.split_once('-') {
        (parse(min)?, parse(max)?)
    } else {
        let value = parse(value)?;
        (value, value)
    };
    if min > max {
        return Err(ConfigError::Invalid(format!(
            "{field} minimum cannot exceed maximum"
        )));
    }
    Ok(())
}

fn is_zero_u32(value: &u32) -> bool {
    *value == 0
}
fn default_mtu() -> u16 {
    DEFAULT_MTU
}
fn default_queue_packets() -> usize {
    DEFAULT_QUEUE_PACKETS
}
fn default_queue_bytes() -> usize {
    DEFAULT_QUEUE_BYTES
}
fn default_stub_mode() -> String {
    "online".to_string()
}
fn default_stub_reconnect_ms() -> u64 {
    250
}
fn default_transport() -> String {
    "tcp".to_string()
}

#[cfg(test)]
mod tests {
    use super::*;

    fn base_config() -> ClientConfig {
        ClientConfig {
            name: "test".into(),
            protocol: ProtocolKind::Stub,
            interface: "kk-test0".into(),
            address: vec!["10.77.0.2/32".into()],
            mtu: 1380,
            state_dir: None,
            queue_packets: 32,
            queue_bytes: 65536,
            stub: StubConfig::default(),
            awg2: None,
            vless_reality: None,
        }
    }

    #[test]
    fn accepts_safe_config() {
        base_config().validate().unwrap();
    }

    #[test]
    fn accepts_reconnect_loop_for_namespace_soak() {
        let mut cfg = base_config();
        cfg.stub.mode = "reconnect-loop".into();
        cfg.validate().unwrap();
    }

    #[test]
    fn rejects_unsafe_interface() {
        let mut cfg = base_config();
        cfg.interface = "bad/interface".into();
        assert!(cfg.validate().is_err());
    }

    #[test]
    fn rejects_bad_cidr() {
        let mut cfg = base_config();
        cfg.address = vec!["10.0.0.1/99".into()];
        assert!(cfg.validate().is_err());
    }

    #[test]
    fn rejects_awg_range_inversion() {
        let mut cfg = base_config();
        cfg.protocol = ProtocolKind::Amneziawg2;
        cfg.awg2 = Some(Awg2Config {
            private_key: "test-private".into(),
            peer_public_key: "test-peer".into(),
            preshared_key: None,
            endpoint: "192.0.2.1:1234".into(),
            allowed_ips: vec!["0.0.0.0/0".into()],
            persistent_keepalive: None,
            jc: 4,
            jmin: 100,
            jmax: 10,
            s1: 0,
            s2: 0,
            s3: 0,
            s4: 0,
            h1: None,
            h2: None,
            h3: None,
            h4: None,
            i1: String::new(),
            i2: String::new(),
            i3: String::new(),
            i4: String::new(),
            i5: String::new(),
        });
        assert!(cfg.validate().is_err());
    }
}
