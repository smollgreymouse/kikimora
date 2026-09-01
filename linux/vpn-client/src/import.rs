use crate::config::{
    Awg2Config, ClientConfig, ProtocolKind, StubConfig, VlessRealityConfig,
};
use base64::engine::general_purpose::{STANDARD, STANDARD_NO_PAD, URL_SAFE, URL_SAFE_NO_PAD};
use base64::Engine as _;
use std::collections::BTreeMap;
use std::fs::{self, OpenOptions};
use std::io::{self, Write};
use std::os::unix::fs::OpenOptionsExt;
use std::path::Path;
use thiserror::Error;
use url::Url;

#[derive(Debug, Error)]
pub enum ImportError {
    #[error("unsupported share-link scheme: {0}")]
    UnsupportedScheme(String),
    #[error("invalid share link: {0}")]
    InvalidLink(String),
    #[error("invalid WireGuard/AmneziaWG payload: {0}")]
    InvalidWg(String),
    #[error("invalid VLESS/REALITY link: {0}")]
    InvalidVless(String),
    #[error("generated client configuration is invalid: {0}")]
    InvalidConfig(String),
    #[error("failed to serialize imported configuration: {0}")]
    Serialize(#[from] toml::ser::Error),
    #[error("failed to write imported configuration: {0}")]
    Io(#[from] io::Error),
}

pub fn import_share_link(
    link: &str,
    name: &str,
    interface: &str,
) -> Result<ClientConfig, ImportError> {
    let scheme = link
        .split_once("://")
        .map(|(scheme, _)| scheme.to_ascii_lowercase())
        .ok_or_else(|| ImportError::InvalidLink("missing URI scheme".into()))?;

    let config = match scheme.as_str() {
        "wg" | "awg" | "amneziawg" | "wireguard" => import_wg(link, name, interface)?,
        "vless" => import_vless(link, name, interface)?,
        _ => return Err(ImportError::UnsupportedScheme(scheme)),
    };

    config
        .validate()
        .map_err(|error| ImportError::InvalidConfig(error.to_string()))?;
    Ok(config)
}

pub fn render_imported_toml(config: &ClientConfig) -> Result<String, ImportError> {
    Ok(toml::to_string_pretty(config)?)
}

pub fn write_imported_config(path: &Path, text: &str, force: bool) -> Result<(), ImportError> {
    if path.exists() && !force {
        return Err(ImportError::Io(io::Error::new(
            io::ErrorKind::AlreadyExists,
            format!("{} already exists; use --force to replace it", path.display()),
        )));
    }

    let parent = path.parent().unwrap_or_else(|| Path::new("."));
    fs::create_dir_all(parent)?;
    let file_name = path
        .file_name()
        .and_then(|name| name.to_str())
        .ok_or_else(|| ImportError::InvalidLink("output path has no valid file name".into()))?;
    let temporary = parent.join(format!(".{file_name}.tmp.{}", std::process::id()));

    let result = (|| -> io::Result<()> {
        let mut options = OpenOptions::new();
        options.write(true).create_new(true).mode(0o600);
        let mut file = options.open(&temporary)?;
        file.write_all(text.as_bytes())?;
        file.sync_all()?;
        fs::rename(&temporary, path)?;
        FileSync::sync_dir(parent)?;
        Ok(())
    })();

    if result.is_err() {
        let _ = fs::remove_file(&temporary);
    }
    result.map_err(ImportError::Io)
}

struct FileSync;

impl FileSync {
    fn sync_dir(path: &Path) -> io::Result<()> {
        std::fs::File::open(path)?.sync_all()
    }
}

fn import_wg(link: &str, name: &str, interface: &str) -> Result<ClientConfig, ImportError> {
    let (_, rest) = link
        .split_once("://")
        .ok_or_else(|| ImportError::InvalidLink("missing URI scheme".into()))?;
    let payload = rest.split('#').next().unwrap_or_default().trim();
    if payload.is_empty() {
        return Err(ImportError::InvalidWg("empty base64 payload".into()));
    }

    let decoded = decode_base64_lenient(payload)?;
    let text = String::from_utf8(decoded)
        .map_err(|_| ImportError::InvalidWg("decoded payload is not UTF-8 text".into()))?;
    let ini = parse_wg_ini(&text)?;

    let interface_section = ini
        .get("interface")
        .ok_or_else(|| ImportError::InvalidWg("missing [Interface] section".into()))?;
    let peers = ini
        .iter()
        .filter(|(section, _)| section.starts_with("peer#"))
        .map(|(_, values)| values)
        .collect::<Vec<_>>();
    if peers.len() != 1 {
        return Err(ImportError::InvalidWg(format!(
            "Stage 0 supports exactly one [Peer] per imported WG/AWG link, found {}",
            peers.len()
        )));
    }
    let peer = peers[0];

    for forbidden in ["preup", "postup", "predown", "postdown", "table"] {
        if interface_section.contains_key(forbidden) {
            return Err(ImportError::InvalidWg(format!(
                "[Interface] {forbidden} is intentionally unsupported; Kikimora owns routing and does not execute imported hooks"
            )));
        }
    }

    let address = split_csv(required(interface_section, "address", "[Interface]")?);
    let mtu = interface_section
        .get("mtu")
        .map(|value| {
            value
                .parse::<u16>()
                .map_err(|_| ImportError::InvalidWg(format!("invalid MTU: {value}")))
        })
        .transpose()?
        .unwrap_or(1380);

    let awg2 = Awg2Config {
        private_key: required(interface_section, "privatekey", "[Interface]")?.to_string(),
        peer_public_key: required(peer, "publickey", "[Peer]")?.to_string(),
        preshared_key: peer.get("presharedkey").cloned(),
        endpoint: required(peer, "endpoint", "[Peer]")?.to_string(),
        allowed_ips: peer
            .get("allowedips")
            .map(|value| split_csv(value))
            .unwrap_or_else(|| vec!["0.0.0.0/0".into(), "::/0".into()]),
        persistent_keepalive: peer
            .get("persistentkeepalive")
            .map(|value| {
                value.parse::<u16>().map_err(|_| {
                    ImportError::InvalidWg(format!("invalid PersistentKeepalive: {value}"))
                })
            })
            .transpose()?,
        jc: parse_u32(interface_section, "jc")?.unwrap_or(0),
        jmin: parse_u32(interface_section, "jmin")?.unwrap_or(0),
        jmax: parse_u32(interface_section, "jmax")?.unwrap_or(0),
        s1: parse_u32(interface_section, "s1")?.unwrap_or(0),
        s2: parse_u32(interface_section, "s2")?.unwrap_or(0),
        s3: parse_u32(interface_section, "s3")?.unwrap_or(0),
        s4: parse_u32(interface_section, "s4")?.unwrap_or(0),
        h1: interface_section.get("h1").cloned(),
        h2: interface_section.get("h2").cloned(),
        h3: interface_section.get("h3").cloned(),
        h4: interface_section.get("h4").cloned(),
        i1: interface_section.get("i1").cloned().unwrap_or_default(),
        i2: interface_section.get("i2").cloned().unwrap_or_default(),
        i3: interface_section.get("i3").cloned().unwrap_or_default(),
        i4: interface_section.get("i4").cloned().unwrap_or_default(),
        i5: interface_section.get("i5").cloned().unwrap_or_default(),
    };

    Ok(ClientConfig {
        name: name.to_string(),
        protocol: ProtocolKind::Amneziawg2,
        interface: interface.to_string(),
        address,
        mtu,
        state_dir: None,
        queue_packets: 256,
        queue_bytes: 2 * 1024 * 1024,
        stub: StubConfig::default(),
        awg2: Some(awg2),
        vless_reality: None,
    })
}

fn import_vless(link: &str, name: &str, interface: &str) -> Result<ClientConfig, ImportError> {
    let url = Url::parse(link).map_err(|error| ImportError::InvalidVless(error.to_string()))?;
    if url.scheme() != "vless" {
        return Err(ImportError::InvalidVless("scheme must be vless".into()));
    }

    let uuid = url.username();
    if uuid.is_empty() {
        return Err(ImportError::InvalidVless("missing UUID/userinfo".into()));
    }
    let host = url
        .host_str()
        .ok_or_else(|| ImportError::InvalidVless("missing server host".into()))?;
    let port = url
        .port()
        .ok_or_else(|| ImportError::InvalidVless("missing explicit server port".into()))?;

    let query = url.query_pairs().into_owned().collect::<BTreeMap<_, _>>();
    let security = query.get("security").map(String::as_str).unwrap_or("");
    if security != "reality" {
        return Err(ImportError::InvalidVless(format!(
            "Stage 0 requires security=reality, got {security:?}"
        )));
    }
    if let Some(encryption) = query.get("encryption") {
        if encryption != "none" {
            return Err(ImportError::InvalidVless(format!(
                "unsupported VLESS encryption={encryption}; expected none"
            )));
        }
    }

    let server_name = required_query(&query, "sni")?.to_string();
    let public_key = required_query(&query, "pbk")?.to_string();
    let transport = query
        .get("type")
        .cloned()
        .unwrap_or_else(|| "tcp".to_string());

    let vless_reality = VlessRealityConfig {
        endpoint: format_host_port(host, port),
        uuid: uuid.to_string(),
        server_name,
        public_key,
        short_id: query.get("sid").cloned().filter(|value| !value.is_empty()),
        flow: query.get("flow").cloned().filter(|value| !value.is_empty()),
        fingerprint: query.get("fp").cloned().filter(|value| !value.is_empty()),
        spider_x: query.get("spx").cloned().filter(|value| !value.is_empty()),
        transport,
    };

    Ok(ClientConfig {
        name: name.to_string(),
        protocol: ProtocolKind::VlessReality,
        interface: interface.to_string(),
        address: Vec::new(),
        mtu: 1380,
        state_dir: None,
        queue_packets: 256,
        queue_bytes: 2 * 1024 * 1024,
        stub: StubConfig::default(),
        awg2: None,
        vless_reality: Some(vless_reality),
    })
}

fn decode_base64_lenient(payload: &str) -> Result<Vec<u8>, ImportError> {
    for engine in [&STANDARD, &STANDARD_NO_PAD, &URL_SAFE, &URL_SAFE_NO_PAD] {
        if let Ok(decoded) = engine.decode(payload.as_bytes()) {
            return Ok(decoded);
        }
    }
    Err(ImportError::InvalidWg(
        "payload is not standard or URL-safe base64".into(),
    ))
}

fn parse_wg_ini(text: &str) -> Result<BTreeMap<String, BTreeMap<String, String>>, ImportError> {
    let mut sections = BTreeMap::<String, BTreeMap<String, String>>::new();
    let mut current = String::new();
    let mut peer_number = 0usize;

    for (line_number, raw) in text.lines().enumerate() {
        let line = raw.trim();
        if line.is_empty() || line.starts_with('#') || line.starts_with(';') {
            continue;
        }
        if line.starts_with('[') && line.ends_with(']') {
            let section = line[1..line.len() - 1].trim().to_ascii_lowercase();
            current = match section.as_str() {
                "interface" => "interface".to_string(),
                "peer" => {
                    peer_number += 1;
                    format!("peer#{peer_number}")
                }
                _ => {
                    return Err(ImportError::InvalidWg(format!(
                        "unsupported section [{section}] on line {}",
                        line_number + 1
                    )))
                }
            };
            sections.entry(current.clone()).or_default();
            continue;
        }
        if current.is_empty() {
            return Err(ImportError::InvalidWg(format!(
                "key/value before a section on line {}",
                line_number + 1
            )));
        }
        let (key, value) = line.split_once('=').ok_or_else(|| {
            ImportError::InvalidWg(format!("invalid key/value on line {}", line_number + 1))
        })?;
        sections
            .entry(current.clone())
            .or_default()
            .insert(key.trim().to_ascii_lowercase(), value.trim().to_string());
    }

    Ok(sections)
}

fn required<'a>(
    section: &'a BTreeMap<String, String>,
    key: &str,
    section_name: &str,
) -> Result<&'a str, ImportError> {
    section
        .get(key)
        .map(String::as_str)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| ImportError::InvalidWg(format!("missing {section_name} {key}")))
}

fn required_query<'a>(
    query: &'a BTreeMap<String, String>,
    key: &str,
) -> Result<&'a str, ImportError> {
    query
        .get(key)
        .map(String::as_str)
        .filter(|value| !value.is_empty())
        .ok_or_else(|| ImportError::InvalidVless(format!("missing query parameter {key}")))
}

fn parse_u32(
    section: &BTreeMap<String, String>,
    key: &str,
) -> Result<Option<u32>, ImportError> {
    section
        .get(key)
        .map(|value| {
            value
                .parse::<u32>()
                .map_err(|_| ImportError::InvalidWg(format!("invalid {key}: {value}")))
        })
        .transpose()
}

fn split_csv(value: &str) -> Vec<String> {
    value
        .split(',')
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
        .collect()
}

fn format_host_port(host: &str, port: u16) -> String {
    if host.contains(':') && !host.starts_with('[') {
        format!("[{host}]:{port}")
    } else {
        format!("{host}:{port}")
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn encode_wg(text: &str, scheme: &str) -> String {
        format!("{scheme}://{}#Imported", URL_SAFE_NO_PAD.encode(text.as_bytes()))
    }

    #[test]
    fn imports_awg2_share_link_aliases() {
        let conf = r#"
[Interface]
PrivateKey = test-private
Address = 10.8.0.2/32
MTU = 1420
Jc = 4
Jmin = 40
Jmax = 70
S1 = 15
S2 = 20
H1 = 100-200
I1 = <b 0x01>
DNS = 1.1.1.1

[Peer]
PublicKey = test-public
PresharedKey = test-psk
Endpoint = 192.0.2.10:51820
AllowedIPs = 0.0.0.0/0, ::/0
PersistentKeepalive = 25
"#;

        for scheme in ["wg", "awg", "amneziawg", "wireguard"] {
            let config = import_share_link(&encode_wg(conf, scheme), "awg-main", "kk-awg0")
                .unwrap();
            assert_eq!(config.protocol, ProtocolKind::Amneziawg2);
            assert_eq!(config.interface, "kk-awg0");
            assert_eq!(config.address, vec!["10.8.0.2/32"]);
            let awg = config.awg2.unwrap();
            assert_eq!(awg.jc, 4);
            assert_eq!(awg.jmin, 40);
            assert_eq!(awg.jmax, 70);
            assert_eq!(awg.h1.as_deref(), Some("100-200"));
            assert_eq!(awg.i1, "<b 0x01>");
        }
    }

    #[test]
    fn imported_wg_rejects_hooks_and_table_policy() {
        let conf = r#"
[Interface]
PrivateKey = test-private
Address = 10.8.0.2/32
PostUp = ip route add default dev wg0

[Peer]
PublicKey = test-public
Endpoint = 192.0.2.10:51820
AllowedIPs = 0.0.0.0/0
"#;
        let error = import_share_link(&encode_wg(conf, "wg"), "bad", "kk-bad0").unwrap_err();
        assert!(error.to_string().contains("unsupported"));
    }

    #[test]
    fn imports_vless_reality_share_link() {
        let link = "vless://123e4567-e89b-12d3-a456-426614174000@203.0.113.10:443?encryption=none&security=reality&sni=www.example.com&fp=chrome&pbk=PUBLICKEY&sid=0123456789abcdef&type=tcp&flow=xtls-rprx-vision&spx=%2F#NL";
        let config = import_share_link(link, "xray-main", "kk-xray0").unwrap();
        assert_eq!(config.protocol, ProtocolKind::VlessReality);
        let vless = config.vless_reality.unwrap();
        assert_eq!(vless.endpoint, "203.0.113.10:443");
        assert_eq!(vless.server_name, "www.example.com");
        assert_eq!(vless.fingerprint.as_deref(), Some("chrome"));
        assert_eq!(vless.short_id.as_deref(), Some("0123456789abcdef"));
        assert_eq!(vless.spider_x.as_deref(), Some("/"));
    }

    #[test]
    fn rejects_non_reality_vless() {
        let link = "vless://123e4567-e89b-12d3-a456-426614174000@203.0.113.10:443?security=tls&sni=example.com&pbk=x&type=tcp";
        assert!(import_share_link(link, "x", "kk-x0").is_err());
    }
}
