use crate::backend::{BackendError, BackendIo, BackendStatus, VpnBackend};
use crate::config::{Awg2Config, ClientConfig};
use crate::state::{EndpointState, StateReason};
use async_trait::async_trait;
use nblib_awg::core::config::{InterfaceConfig, PeerConfig};
use nblib_awg::core::cryptography::Key;
use nblib_awg::core::node::Node;
use nblib_awg::core::routing::Cidr;
use nblib_awg::core::utils::configure_crypto_workers;
use nblib_awg::ffi::common::ChannelTunDevice;
use std::net::SocketAddr;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};
use tokio::sync::{mpsc, oneshot};
use tokio::time::{interval, timeout, MissedTickBehavior};

const EXECUTOR_POLL_MS: u64 = 250;
const STARTUP_TIMEOUT: Duration = Duration::from_secs(5);
const INITIAL_HANDSHAKE_TIMEOUT: Duration = Duration::from_secs(15);
const REKEY_HEALTH_AGE_MS: u64 = 135_000;
const RECENT_TUN_INPUT: Duration = Duration::from_secs(10);
const AWG_PACKET_WORKERS: usize = 1;
const AWG_CRYPTO_WORKERS: usize = 3;

#[derive(Debug, Clone, Copy)]
struct HealthSample {
    last_handshake_unix_ms: i64,
    rx_bytes: u64,
    tx_bytes: u64,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
enum ReportedState {
    Connecting,
    Online,
    Reconnecting,
}

pub struct Awg2Backend {
    config: Awg2Config,
    addresses: Vec<String>,
    interface_name: String,
    mtu: u16,
    queue_packets: usize,
}

impl Awg2Backend {
    pub fn new(config: &ClientConfig) -> Result<Self, BackendError> {
        let awg2 = config
            .awg2
            .clone()
            .ok_or_else(|| BackendError::Config("missing [awg2] section".into()))?;

        validate_key("awg2.private_key", &awg2.private_key)?;
        validate_key("awg2.peer_public_key", &awg2.peer_public_key)?;
        if let Some(psk) = &awg2.preshared_key {
            validate_key("awg2.preshared_key", psk)?;
        }
        if awg2.allowed_ips.is_empty() {
            return Err(BackendError::Config(
                "awg2.allowed_ips must contain at least one CIDR".into(),
            ));
        }

        Ok(Self {
            config: awg2,
            addresses: config.address.clone(),
            interface_name: config.interface.clone(),
            mtu: config.mtu,
            queue_packets: config.queue_packets,
        })
    }

    async fn resolve_endpoint(&self) -> Result<SocketAddr, BackendError> {
        let mut addresses = tokio::net::lookup_host(self.config.endpoint.as_str())
            .await
            .map_err(|error| {
                BackendError::Config(format!(
                    "cannot resolve awg2.endpoint {}: {error}",
                    self.config.endpoint
                ))
            })?;
        addresses.next().ok_or_else(|| {
            BackendError::Config(format!(
                "awg2.endpoint {} resolved to no addresses",
                self.config.endpoint
            ))
        })
    }

    fn build_protocol_config(
        &self,
        endpoint: SocketAddr,
    ) -> Result<(InterfaceConfig, Vec<PeerConfig>), BackendError> {
        let addresses = self
            .addresses
            .iter()
            .map(|cidr| {
                Cidr::try_from_string(cidr).map_err(|error| {
                    BackendError::Config(format!("invalid client address {cidr}: {error}"))
                })
            })
            .collect::<Result<Vec<_>, _>>()?;
        let allowed_ips = self
            .config
            .allowed_ips
            .iter()
            .map(|cidr| {
                Cidr::try_from_string(cidr).map_err(|error| {
                    BackendError::Config(format!("invalid awg2.allowed_ips entry {cidr}: {error}"))
                })
            })
            .collect::<Result<Vec<_>, _>>()?;

        let interface = InterfaceConfig {
            private_key: self.config.private_key.clone(),
            addresses,
            listen_port: None,
            dns_servers: Vec::new(),
            post_up: Vec::new(),
            post_down: Vec::new(),
            mtu: self.mtu as u32,
            awg_jc: self.config.jc,
            awg_jmin: self.config.jmin,
            awg_jmax: self.config.jmax,
            awg_s1: self.config.s1,
            awg_s2: self.config.s2,
            awg_s3: self.config.s3,
            awg_s4: self.config.s4,
            awg_h1: self.config.h1.clone().unwrap_or_else(|| "1".into()),
            awg_h2: self.config.h2.clone().unwrap_or_else(|| "2".into()),
            awg_h3: self.config.h3.clone().unwrap_or_else(|| "3".into()),
            awg_h4: self.config.h4.clone().unwrap_or_else(|| "4".into()),
            awg_i1: self.config.i1.clone(),
            awg_i2: self.config.i2.clone(),
            awg_i3: self.config.i3.clone(),
            awg_i4: self.config.i4.clone(),
            awg_i5: self.config.i5.clone(),
            header_protection_key: None,
            content_padding_addition: "0".into(),
            rekey_after_time: "120".into(),
            rekey_timeout: "5".into(),
            reject_after_time: "180".into(),
            keepalive_timeout: "10".into(),
            max_handshake_attempts: "0".into(),
            infinite_handshakes: true,
            random_trailers: false,
            disable_cookies: false,
            mlkem512_private_key: None,
            mlkem768_private_key: None,
        };
        let peer = PeerConfig {
            public_key: self.config.peer_public_key.clone(),
            preshared_key: self.config.preshared_key.clone(),
            allowed_ips,
            endpoint: Some(endpoint),
            persistent_keepalive: self.config.persistent_keepalive.map(u32::from),
            persistent_keepalive_range: None,
            mlkem512_public_key: None,
            mlkem768_public_key: None,
        };

        Ok((interface, vec![peer]))
    }
}

#[async_trait]
impl VpnBackend for Awg2Backend {
    fn protocol_name(&self) -> &'static str {
        "amneziawg2"
    }

    async fn run(&mut self, mut io: BackendIo) -> Result<(), BackendError> {
        let endpoint = self.resolve_endpoint().await?;
        let endpoint_state = EndpointState {
            address: endpoint.ip().to_string(),
            port: endpoint.port(),
        };
        if !send_status(
            &io,
            with_endpoint(
                BackendStatus::connecting(StateReason::ConnectStarted),
                &endpoint_state,
            ),
        )
        .await
        {
            return Ok(());
        }

        let (interface_config, peer_configs) = self.build_protocol_config(endpoint)?;
        let (app_to_lib_tx, app_to_lib_rx) = smol::channel::bounded(self.queue_packets);
        let (lib_to_app_tx, lib_to_app_rx) = smol::channel::bounded(self.queue_packets);
        let (executor_stop_tx, executor_stop_rx) = smol::channel::bounded::<()>(1);
        let (health_tx, mut health_rx) = mpsc::channel::<HealthSample>(8);
        let (ready_tx, ready_rx) = oneshot::channel::<()>();
        let bridge_name = self.interface_name.clone();
        let mtu = self.mtu as u32;

        let executor = std::thread::Builder::new()
            .name(format!("awg-{}", self.interface_name))
            .spawn(move || {
                let _ = configure_crypto_workers(AWG_CRYPTO_WORKERS);
                smol::block_on(async move {
                    let device = Arc::new(ChannelTunDevice {
                        name: bridge_name,
                        mtu,
                        app_to_lib_rx,
                        lib_to_app_tx,
                    });
                    let node = Node::new(
                        interface_config,
                        peer_configs,
                        device,
                        false,
                        false,
                        AWG_PACKET_WORKERS,
                        0,
                    )
                    .await;
                    node.clone().start().await;
                    let _ = ready_tx.send(());

                    loop {
                        if executor_stop_rx.try_recv().is_ok() {
                            for peer in node.peers_by_public_key.iter() {
                                peer.value().shutdown();
                            }
                            return;
                        }

                        if let Some(peer) = node.peers_by_public_key.iter().next() {
                            let peer = peer.value();
                            let _ = health_tx.try_send(HealthSample {
                                last_handshake_unix_ms: peer
                                    .last_handshake_time
                                    .load(Ordering::Relaxed),
                                rx_bytes: peer.rx_bytes.load(Ordering::Relaxed),
                                tx_bytes: peer.tx_bytes.load(Ordering::Relaxed),
                            });
                        }
                        smol::Timer::after(Duration::from_millis(EXECUTOR_POLL_MS)).await;
                    }
                });
            })?;

        match timeout(STARTUP_TIMEOUT, ready_rx).await {
            Ok(Ok(())) => {}
            Ok(Err(_)) => {
                return Err(BackendError::Fatal(
                    "AmneziaWG executor terminated during startup".into(),
                ));
            }
            Err(_) => {
                let _ = executor_stop_tx.try_send(());
                return Err(BackendError::Fatal(
                    "AmneziaWG executor startup timed out".into(),
                ));
            }
        }

        let started_at = Instant::now();
        let mut last_tun_input = None;
        let mut last_handshake = 0_i64;
        let mut reported_state = ReportedState::Connecting;
        let mut health_tick = interval(Duration::from_millis(EXECUTOR_POLL_MS));
        health_tick.set_missed_tick_behavior(MissedTickBehavior::Delay);

        loop {
            tokio::select! {
                changed = io.shutdown.changed() => {
                    if changed.is_err() || *io.shutdown.borrow() {
                        let _ = executor_stop_tx.try_send(());
                        let _ = tokio::task::spawn_blocking(move || executor.join()).await;
                        return Ok(());
                    }
                }
                packet = io.from_tun.recv() => {
                    let Some(packet) = packet else {
                        let _ = executor_stop_tx.try_send(());
                        return Ok(());
                    };
                    last_tun_input = Some(Instant::now());
                    if app_to_lib_tx.send(packet).await.is_err() {
                        return Err(BackendError::Fatal("AmneziaWG packet input channel closed".into()));
                    }
                }
                packet = lib_to_app_rx.recv() => {
                    match packet {
                        Ok(packet) => {
                            if io.to_tun.send(packet).await.is_err() {
                                let _ = executor_stop_tx.try_send(());
                                return Ok(());
                            }
                        }
                        Err(_) => {
                            return Err(BackendError::Fatal("AmneziaWG packet output channel closed".into()));
                        }
                    }
                }
                sample = health_rx.recv() => {
                    if let Some(sample) = sample {
                        let now = unix_ms();
                        let age_ms = if sample.last_handshake_unix_ms > 0 {
                            now.saturating_sub(sample.last_handshake_unix_ms as u64)
                        } else {
                            0
                        };
                        let handshake_advanced = sample.last_handshake_unix_ms > last_handshake;
                        let initial_timeout = sample.last_handshake_unix_ms == 0
                            && started_at.elapsed() >= INITIAL_HANDSHAKE_TIMEOUT
                            && reported_state == ReportedState::Connecting;
                        let stale_handshake_with_traffic = sample.last_handshake_unix_ms > 0
                            && age_ms >= REKEY_HEALTH_AGE_MS
                            && last_tun_input
                                .is_some_and(|instant: Instant| instant.elapsed() <= RECENT_TUN_INPUT)
                            && reported_state == ReportedState::Online;

                        if handshake_advanced {
                            last_handshake = sample.last_handshake_unix_ms;
                            if reported_state != ReportedState::Online {
                                if !send_status(
                                    &io,
                                    with_endpoint(
                                        BackendStatus::online(StateReason::HandshakeEstablished),
                                        &endpoint_state,
                                    ),
                                )
                                .await
                                {
                                    let _ = executor_stop_tx.try_send(());
                                    return Ok(());
                                }
                                reported_state = ReportedState::Online;
                            }
                        } else if initial_timeout || stale_handshake_with_traffic {
                            if !send_status(
                                &io,
                                with_endpoint(
                                    BackendStatus::reconnecting(StateReason::HandshakeTimeout),
                                    &endpoint_state,
                                ),
                            )
                            .await
                            {
                                let _ = executor_stop_tx.try_send(());
                                return Ok(());
                            }
                            reported_state = ReportedState::Reconnecting;
                        }

                        let _ = (sample.rx_bytes, sample.tx_bytes);
                    }
                }
                _ = health_tick.tick() => {
                    if executor.is_finished() {
                        return Err(BackendError::Fatal("AmneziaWG executor thread exited".into()));
                    }
                }
            }
        }
    }
}

fn validate_key(name: &str, value: &str) -> Result<(), BackendError> {
    Key::try_from_base64(value)
        .map(|_| ())
        .map_err(|error| BackendError::Config(format!("{name}: {error}")))
}

fn with_endpoint(mut status: BackendStatus, endpoint: &EndpointState) -> BackendStatus {
    status.endpoint = Some(endpoint.clone());
    status
}

async fn send_status(io: &BackendIo, status: BackendStatus) -> bool {
    io.status.send(status).await.is_ok()
}

fn unix_ms() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_millis()
        .try_into()
        .unwrap_or(u64::MAX)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::config::{ProtocolKind, StubConfig};

    const PRIVATE_KEY: &str = "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE=";
    const PUBLIC_KEY: &str = "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI=";

    fn client_config() -> ClientConfig {
        ClientConfig {
            name: "awg-test".into(),
            protocol: ProtocolKind::Amneziawg2,
            interface: "kk-awg0".into(),
            address: vec!["10.77.0.2/32".into()],
            mtu: 1380,
            state_dir: None,
            queue_packets: 32,
            queue_bytes: 65_536,
            stub: StubConfig::default(),
            awg2: Some(Awg2Config {
                private_key: PRIVATE_KEY.into(),
                peer_public_key: PUBLIC_KEY.into(),
                preshared_key: None,
                endpoint: "192.0.2.1:51820".into(),
                allowed_ips: vec!["0.0.0.0/0".into()],
                persistent_keepalive: Some(25),
                jc: 4,
                jmin: 40,
                jmax: 80,
                s1: 15,
                s2: 15,
                s3: 0,
                s4: 0,
                h1: Some("1111".into()),
                h2: Some("2222".into()),
                h3: Some("3333".into()),
                h4: Some("4444".into()),
                i1: String::new(),
                i2: String::new(),
                i3: String::new(),
                i4: String::new(),
                i5: String::new(),
            }),
            vless_reality: None,
        }
    }

    #[test]
    fn maps_awg2_without_os_side_effects() {
        let backend = Awg2Backend::new(&client_config()).unwrap();
        let endpoint: SocketAddr = "192.0.2.1:51820".parse().unwrap();
        let (interface, peers) = backend.build_protocol_config(endpoint).unwrap();

        assert!(interface.dns_servers.is_empty());
        assert!(interface.post_up.is_empty());
        assert!(interface.post_down.is_empty());
        assert!(interface.header_protection_key.is_none());
        assert_eq!(interface.awg_jc, 4);
        assert_eq!(interface.awg_h1, "1111");
        assert_eq!(peers.len(), 1);
        assert_eq!(peers[0].endpoint, Some(endpoint));
        assert_eq!(peers[0].allowed_ips.len(), 1);
    }

    #[test]
    fn rejects_invalid_key_before_upstream_can_panic() {
        let mut config = client_config();
        config.awg2.as_mut().unwrap().private_key = "not-base64".into();
        let error = Awg2Backend::new(&config).err().unwrap().to_string();
        assert!(error.contains("awg2.private_key"));
    }
}
