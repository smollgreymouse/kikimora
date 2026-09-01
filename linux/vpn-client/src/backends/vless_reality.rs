use crate::backend::{BackendError, BackendIo, BackendStatus, VpnBackend};
use crate::config::{ClientConfig, VlessRealityConfig};
use crate::state::{EndpointState, StateReason};
use async_trait::async_trait;
use serde_json::{json, Map, Value};
use tokio::time::{interval, Duration, MissedTickBehavior};
use url::Url;
use xray_config::parse_xray_json;
use xray_core_rs::{Core, TunRuntimeOptions, TunRuntimeProfile};

const HEALTH_POLL: Duration = Duration::from_millis(250);

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ReportedState {
    Online,
    Reconnecting,
}

pub struct VlessRealityBackend {
    config: VlessRealityConfig,
}

impl VlessRealityBackend {
    pub fn new(config: &ClientConfig) -> Result<Self, BackendError> {
        let vless = config
            .vless_reality
            .clone()
            .ok_or_else(|| BackendError::Config("missing [vless_reality] section".into()))?;
        Ok(Self { config: vless })
    }

    fn endpoint(&self) -> Result<(String, u16), BackendError> {
        let parsed = Url::parse(&format!("tcp://{}", self.config.endpoint)).map_err(|error| {
            BackendError::Config(format!(
                "invalid vless_reality.endpoint {}: {error}",
                self.config.endpoint
            ))
        })?;
        let host = parsed
            .host_str()
            .filter(|value| !value.is_empty())
            .ok_or_else(|| BackendError::Config("VLESS endpoint has no host".into()))?;
        let port = parsed
            .port()
            .ok_or_else(|| BackendError::Config("VLESS endpoint has no port".into()))?;
        Ok((host.to_owned(), port))
    }

    fn xray_config_json(&self, host: &str, port: u16) -> Result<String, BackendError> {
        let mut user = Map::new();
        user.insert("id".into(), Value::String(self.config.uuid.clone()));
        user.insert("encryption".into(), Value::String("none".into()));
        if let Some(flow) = self.config.flow.as_ref().filter(|value| !value.is_empty()) {
            user.insert("flow".into(), Value::String(flow.clone()));
        }

        let mut reality = Map::new();
        reality.insert(
            "serverName".into(),
            Value::String(self.config.server_name.clone()),
        );
        reality.insert(
            "fingerprint".into(),
            Value::String(
                self.config
                    .fingerprint
                    .clone()
                    .unwrap_or_else(|| "chrome".into()),
            ),
        );
        reality.insert(
            "publicKey".into(),
            Value::String(self.config.public_key.clone()),
        );
        if let Some(short_id) = self
            .config
            .short_id
            .as_ref()
            .filter(|value| !value.is_empty())
        {
            reality.insert("shortId".into(), Value::String(short_id.clone()));
        }
        if let Some(spider_x) = self
            .config
            .spider_x
            .as_ref()
            .filter(|value| !value.is_empty())
        {
            reality.insert("spiderX".into(), Value::String(spider_x.clone()));
        }

        let network = match self.config.transport.as_str() {
            "tcp" | "raw" => "tcp",
            other => {
                return Err(BackendError::Config(format!(
                    "unsupported VLESS transport: {other}"
                )))
            }
        };

        serde_json::to_string(&json!({
            "inbounds": [{
                "tag": "kikimora-tun",
                "protocol": "tun"
            }],
            "outbounds": [{
                "tag": "kikimora-vless",
                "protocol": "vless",
                "settings": {
                    "vnext": [{
                        "address": host,
                        "port": port,
                        "users": [Value::Object(user)]
                    }]
                },
                "streamSettings": {
                    "network": network,
                    "security": "reality",
                    "realitySettings": Value::Object(reality)
                }
            }],
            "routing": { "domainStrategy": "AsIs" }
        }))
        .map_err(|error| BackendError::Config(format!("failed to build Xray config: {error}")))
    }
}

#[async_trait]
impl VpnBackend for VlessRealityBackend {
    fn protocol_name(&self) -> &'static str {
        "vless-reality"
    }

    async fn run(&mut self, mut io: BackendIo) -> Result<(), BackendError> {
        let (host, port) = self.endpoint()?;
        let endpoint = EndpointState {
            address: host.clone(),
            port,
        };
        send_status(
            &io,
            with_endpoint(
                BackendStatus::connecting(StateReason::ConnectStarted),
                &endpoint,
            ),
        )
        .await?;

        let raw = self.xray_config_json(&host, port)?;
        let parsed = parse_xray_json(&raw)
            .map_err(|error| BackendError::Config(format!("Xray config rejected: {error}")))?;
        if parsed.diagnostics.iter().any(|diagnostic| {
            diagnostic
                .message
                .to_ascii_lowercase()
                .contains("unsupported")
        }) {
            return Err(BackendError::Config(format!(
                "Xray config produced unsupported-field diagnostics: {:?}",
                parsed.diagnostics
            )));
        }

        let options = TunRuntimeOptions::with_profile(TunRuntimeProfile::Desktop);
        let mut core = Core::with_tun_runtime_options(parsed.config, options)
            .map_err(|error| BackendError::Fatal(format!("failed to create Xray core: {error}")))?;
        core.start()
            .await
            .map_err(|error| BackendError::Fatal(format!("failed to start Xray core: {error}")))?;
        let tun = core.tun_handle();

        send_status(
            &io,
            with_endpoint(
                BackendStatus::online(StateReason::TransportEstablished),
                &endpoint,
            ),
        )
        .await?;

        let mut reported = ReportedState::Online;
        let mut last_open_events = 0_u64;
        let mut last_open_errors = 0_u64;
        let mut health = interval(HEALTH_POLL);
        health.set_missed_tick_behavior(MissedTickBehavior::Delay);
        health.tick().await;

        loop {
            tokio::select! {
                changed = io.shutdown.changed() => {
                    if changed.is_err() || *io.shutdown.borrow() {
                        core.stop().await.map_err(|error| BackendError::Fatal(format!("failed to stop Xray core: {error}")))?;
                        return Ok(());
                    }
                }
                packet = io.from_tun.recv() => {
                    let Some(packet) = packet else {
                        core.stop().await.map_err(|error| BackendError::Fatal(format!("failed to stop Xray core: {error}")))?;
                        return Ok(());
                    };
                    tun.push_inbound(packet.into())
                        .await
                        .map_err(|error| BackendError::Fatal(format!("Xray TUN input failed: {error}")))?;
                }
                packet = tun.poll_outbound() => {
                    let packet = packet
                        .map_err(|error| BackendError::Fatal(format!("Xray TUN output failed: {error}")))?;
                    if io.to_tun.send(packet.to_vec()).await.is_err() {
                        core.stop().await.map_err(|error| BackendError::Fatal(format!("failed to stop Xray core: {error}")))?;
                        return Ok(());
                    }
                }
                _ = health.tick() => {
                    let stats = tun.stats().await;
                    let open_events = stats.tcp_open_events.saturating_add(stats.udp_remote_open_events);
                    let open_errors = stats.tcp_open_errors.saturating_add(stats.udp_open_errors);

                    if open_errors > last_open_errors
                        && open_events == last_open_events
                        && reported == ReportedState::Online
                    {
                        send_status(
                            &io,
                            with_endpoint(
                                BackendStatus::reconnecting(StateReason::TransportReset),
                                &endpoint,
                            ),
                        )
                        .await?;
                        reported = ReportedState::Reconnecting;
                    } else if open_events > last_open_events
                        && reported == ReportedState::Reconnecting
                    {
                        send_status(
                            &io,
                            with_endpoint(
                                BackendStatus::online(StateReason::TransportEstablished),
                                &endpoint,
                            ),
                        )
                        .await?;
                        reported = ReportedState::Online;
                    }

                    last_open_events = open_events;
                    last_open_errors = open_errors;
                }
            }
        }
    }
}

fn with_endpoint(mut status: BackendStatus, endpoint: &EndpointState) -> BackendStatus {
    status.endpoint = Some(endpoint.clone());
    status
}

async fn send_status(io: &BackendIo, status: BackendStatus) -> Result<(), BackendError> {
    io.status
        .send(status)
        .await
        .map_err(|_| BackendError::Fatal("runtime status channel closed".into()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{ProtocolKind, StubConfig};

    fn config() -> ClientConfig {
        ClientConfig {
            name: "xray-test".into(),
            protocol: ProtocolKind::VlessReality,
            interface: "kk-xray0".into(),
            address: vec!["10.88.0.2/24".into()],
            mtu: 1380,
            state_dir: None,
            queue_packets: 32,
            queue_bytes: 65536,
            stub: StubConfig::default(),
            awg2: None,
            vless_reality: Some(VlessRealityConfig {
                endpoint: "192.0.2.1:443".into(),
                uuid: "00010203-0405-0607-0809-0a0b0c0d0e0f".into(),
                server_name: "www.example.com".into(),
                public_key: "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE".into(),
                short_id: Some("02030405".into()),
                flow: Some("xtls-rprx-vision".into()),
                fingerprint: Some("chrome".into()),
                spider_x: Some("/".into()),
                transport: "tcp".into(),
            }),
        }
    }

    #[test]
    fn generated_config_is_accepted_by_pinned_xray_parser() {
        let backend = VlessRealityBackend::new(&config()).unwrap();
        let raw = backend.xray_config_json("192.0.2.1", 443).unwrap();
        let parsed = parse_xray_json(&raw).unwrap();
        assert_eq!(parsed.config.inbounds.len(), 1);
        assert_eq!(parsed.config.outbounds.len(), 1);
    }

    #[test]
    fn endpoint_parser_supports_ipv6_literals() {
        let mut cfg = config();
        cfg.vless_reality.as_mut().unwrap().endpoint = "[2001:db8::1]:443".into();
        let backend = VlessRealityBackend::new(&cfg).unwrap();
        assert_eq!(backend.endpoint().unwrap(), ("2001:db8::1".into(), 443));
    }
}
