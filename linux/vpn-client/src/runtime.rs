use crate::backend::{BackendError, BackendIo, BackendStatus, VpnBackend};
use crate::config::ClientConfig;
use crate::state::{
    ClientState, InterfaceState, SessionState, StatePublisher, StateReason, StateSnapshot,
};
use crate::tun::{TunDevice, TunReader, TunWriter};
use std::io;
use thiserror::Error;
use tokio::sync::{mpsc, watch};
use tokio::task::JoinError;
use tokio::time::{interval, Duration, MissedTickBehavior};

const PACKET_BUFFER_SIZE: usize = 65_536;
const HEARTBEAT_SECONDS: u64 = 5;

#[derive(Debug, Error)]
pub enum RuntimeError {
    #[error("runtime I/O error: {0}")]
    Io(#[from] io::Error),
    #[error("backend error: {0}")]
    Backend(#[from] BackendError),
    #[error("runtime task failed: {0}")]
    Join(#[from] JoinError),
    #[error("backend exited unexpectedly")]
    BackendExited,
    #[error("TUN task exited unexpectedly")]
    TunExited,
}

enum TunEvent {
    ToBackend(usize),
    FromBackend(usize),
    Dropped,
    Fatal(String),
}

pub async fn run_runtime(
    config: ClientConfig,
    tun: Box<dyn TunDevice>,
    mut backend: Box<dyn VpnBackend>,
    mut shutdown: watch::Receiver<bool>,
) -> Result<(), RuntimeError> {
    let publisher = StatePublisher::new(config.runtime_state_dir());
    let identity = tun.identity();
    let mut snapshot = StateSnapshot::new(config.name.clone(), backend.protocol_name());
    snapshot.interface = Some(InterfaceState {
        name: identity.name.clone(),
        ifindex: identity.ifindex,
        mtu: identity.mtu,
    });
    snapshot.route_ready = true;
    snapshot.transition(ClientState::Armed, StateReason::TunCreated);
    publisher.publish(&snapshot)?;

    let (reader, writer) = tun.split();
    let (tun_to_backend_tx, tun_to_backend_rx) = mpsc::channel(config.queue_packets);
    let (backend_to_tun_tx, backend_to_tun_rx) = mpsc::channel(config.queue_packets);
    let (tun_event_tx, mut tun_event_rx) = mpsc::channel::<TunEvent>(64);
    let (status_tx, mut status_rx) = mpsc::channel::<BackendStatus>(64);
    let (task_shutdown_tx, task_shutdown_rx) = watch::channel(false);

    let reader_task = tokio::spawn(run_tun_reader(
        reader,
        tun_to_backend_tx,
        tun_event_tx.clone(),
        task_shutdown_rx.clone(),
        config.queue_bytes,
    ));
    let writer_task = tokio::spawn(run_tun_writer(
        writer,
        backend_to_tun_rx,
        tun_event_tx,
        task_shutdown_rx.clone(),
    ));

    let backend_io = BackendIo {
        from_tun: tun_to_backend_rx,
        to_tun: backend_to_tun_tx,
        status: status_tx,
        shutdown: task_shutdown_rx,
    };
    let mut backend_task = tokio::spawn(async move { backend.run(backend_io).await });

    snapshot.transition(ClientState::Connecting, StateReason::ConnectStarted);
    snapshot.session = Some(SessionState {
        connected: false,
        last_handshake_age_ms: None,
    });
    publisher.publish(&snapshot)?;

    let mut heartbeat = interval(Duration::from_secs(HEARTBEAT_SECONDS));
    heartbeat.set_missed_tick_behavior(MissedTickBehavior::Delay);
    heartbeat.tick().await;
    let mut status_open = true;

    let result = loop {
        tokio::select! {
            changed = shutdown.changed() => {
                if changed.is_err() || *shutdown.borrow() {
                    snapshot.transition(ClientState::Stopping, StateReason::ShutdownRequested);
                    snapshot.route_ready = false;
                    publisher.publish(&snapshot)?;
                    let _ = task_shutdown_tx.send(true);
                    break Ok(());
                }
            }
            status = status_rx.recv(), if status_open => {
                match status {
                    Some(status) => {
                        apply_backend_status(&mut snapshot, status);
                        publisher.publish(&snapshot)?;
                    }
                    None => status_open = false,
                }
            }
            event = tun_event_rx.recv() => {
                match event {
                    Some(TunEvent::ToBackend(bytes)) => {
                        snapshot.counters.tx_bytes = snapshot.counters.tx_bytes.saturating_add(bytes as u64);
                    }
                    Some(TunEvent::FromBackend(bytes)) => {
                        snapshot.counters.rx_bytes = snapshot.counters.rx_bytes.saturating_add(bytes as u64);
                    }
                    Some(TunEvent::Dropped) => {
                        snapshot.counters.dropped_packets = snapshot.counters.dropped_packets.saturating_add(1);
                    }
                    Some(TunEvent::Fatal(message)) => {
                        snapshot.transition(ClientState::Failed, StateReason::TunFatal);
                        snapshot.route_ready = false;
                        publisher.publish(&snapshot)?;
                        let _ = task_shutdown_tx.send(true);
                        break Err(RuntimeError::Io(io::Error::other(message)));
                    }
                    None => {
                        let _ = task_shutdown_tx.send(true);
                        break Err(RuntimeError::TunExited);
                    }
                }
            }
            backend_result = &mut backend_task => {
                let _ = task_shutdown_tx.send(true);
                match backend_result? {
                    Ok(()) => {
                        snapshot.transition(ClientState::Failed, StateReason::BackendFatal);
                        snapshot.route_ready = false;
                        publisher.publish(&snapshot)?;
                        break Err(RuntimeError::BackendExited);
                    }
                    Err(error) => {
                        snapshot.transition(ClientState::Failed, StateReason::BackendFatal);
                        snapshot.route_ready = false;
                        publisher.publish(&snapshot)?;
                        break Err(RuntimeError::Backend(error));
                    }
                }
            }
            _ = heartbeat.tick() => {
                snapshot.heartbeat();
                publisher.publish(&snapshot)?;
            }
        }
    };

    let _ = task_shutdown_tx.send(true);
    if !backend_task.is_finished() {
        let _ = backend_task.await;
    }
    let _ = reader_task.await;
    let _ = writer_task.await;
    result
}

fn apply_backend_status(snapshot: &mut StateSnapshot, status: BackendStatus) {
    snapshot.transition(status.state, status.reason);
    snapshot.endpoint = status.endpoint;
    snapshot.session = Some(SessionState {
        connected: status.connected,
        last_handshake_age_ms: None,
    });
    snapshot.counters.reconnects = snapshot
        .counters
        .reconnects
        .saturating_add(status.reconnect_increment);
}

async fn run_tun_reader(
    mut reader: Box<dyn TunReader>,
    to_backend: mpsc::Sender<Vec<u8>>,
    events: mpsc::Sender<TunEvent>,
    mut shutdown: watch::Receiver<bool>,
    max_packet_bytes: usize,
) {
    let mut buffer = vec![0u8; PACKET_BUFFER_SIZE];
    loop {
        tokio::select! {
            changed = shutdown.changed() => {
                if changed.is_err() || *shutdown.borrow() {
                    return;
                }
            }
            read = reader.read_packet(&mut buffer) => {
                match read {
                    Ok(0) => continue,
                    Ok(size) => {
                        if size > max_packet_bytes {
                            let _ = events.send(TunEvent::Dropped).await;
                            continue;
                        }
                        let packet = buffer[..size].to_vec();
                        match to_backend.try_send(packet) {
                            Ok(()) => {
                                let _ = events.send(TunEvent::ToBackend(size)).await;
                            }
                            Err(mpsc::error::TrySendError::Full(_)) => {
                                let _ = events.send(TunEvent::Dropped).await;
                            }
                            Err(mpsc::error::TrySendError::Closed(_)) => return,
                        }
                    }
                    Err(error) => {
                        let _ = events.send(TunEvent::Fatal(error.to_string())).await;
                        return;
                    }
                }
            }
        }
    }
}

async fn run_tun_writer(
    mut writer: Box<dyn TunWriter>,
    mut from_backend: mpsc::Receiver<Vec<u8>>,
    events: mpsc::Sender<TunEvent>,
    mut shutdown: watch::Receiver<bool>,
) {
    loop {
        tokio::select! {
            changed = shutdown.changed() => {
                if changed.is_err() || *shutdown.borrow() {
                    return;
                }
            }
            packet = from_backend.recv() => {
                let Some(packet) = packet else { return; };
                if let Err(error) = writer.write_packet(&packet).await {
                    let _ = events.send(TunEvent::Fatal(error.to_string())).await;
                    return;
                }
                let _ = events.send(TunEvent::FromBackend(packet.len())).await;
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::backends::stub::StubBackend;
    use crate::config::{ProtocolKind, StubConfig};
    use crate::tun::{TunIdentity, TunReader, TunWriter};
    use async_trait::async_trait;
    use std::path::PathBuf;

    struct MemoryTun {
        identity: TunIdentity,
        input: mpsc::Receiver<Vec<u8>>,
        output: mpsc::Sender<Vec<u8>>,
    }

    struct MemoryReader(mpsc::Receiver<Vec<u8>>);
    struct MemoryWriter(mpsc::Sender<Vec<u8>>);

    impl TunDevice for MemoryTun {
        fn identity(&self) -> TunIdentity {
            self.identity.clone()
        }

        fn split(self: Box<Self>) -> (Box<dyn TunReader>, Box<dyn TunWriter>) {
            (
                Box::new(MemoryReader(self.input)),
                Box::new(MemoryWriter(self.output)),
            )
        }
    }

    #[async_trait]
    impl TunReader for MemoryReader {
        async fn read_packet(&mut self, buffer: &mut [u8]) -> io::Result<usize> {
            let packet = self
                .0
                .recv()
                .await
                .ok_or_else(|| io::Error::new(io::ErrorKind::UnexpectedEof, "closed"))?;
            buffer[..packet.len()].copy_from_slice(&packet);
            Ok(packet.len())
        }
    }

    #[async_trait]
    impl TunWriter for MemoryWriter {
        async fn write_packet(&mut self, packet: &[u8]) -> io::Result<()> {
            self.0
                .send(packet.to_vec())
                .await
                .map_err(|_| io::Error::new(io::ErrorKind::BrokenPipe, "closed"))
        }
    }

    fn config(state_dir: PathBuf) -> ClientConfig {
        ClientConfig {
            name: "test".into(),
            protocol: ProtocolKind::Stub,
            interface: "kk-test0".into(),
            address: vec!["10.77.0.2/32".into()],
            mtu: 1380,
            state_dir: Some(state_dir),
            queue_packets: 16,
            queue_bytes: 65536,
            stub: StubConfig {
                mode: "reconnect-once".into(),
                reconnect_after_ms: 10,
            },
            awg2: None,
            vless_reality: None,
        }
    }

    #[tokio::test]
    async fn reconnect_keeps_route_ready_and_tun_identity() {
        let dir = tempfile::tempdir().unwrap();
        let (input_tx, input_rx) = mpsc::channel(4);
        let (output_tx, _output_rx) = mpsc::channel(4);
        let tun = MemoryTun {
            identity: TunIdentity {
                name: "kk-test0".into(),
                ifindex: 77,
                mtu: 1380,
            },
            input: input_rx,
            output: output_tx,
        };
        let cfg = config(dir.path().to_path_buf());
        let backend = StubBackend::new(cfg.stub.clone());
        let (shutdown_tx, shutdown_rx) = watch::channel(false);
        let task = tokio::spawn(run_runtime(
            cfg,
            Box::new(tun),
            Box::new(backend),
            shutdown_rx,
        ));

        input_tx.send(vec![0x45, 0, 0, 20]).await.unwrap();
        let state_path = dir.path().join("state.json");
        let json = tokio::time::timeout(Duration::from_secs(1), async {
            loop {
                if let Ok(text) = std::fs::read_to_string(&state_path) {
                    let json: serde_json::Value = serde_json::from_str(&text).unwrap();
                    if json["counters"]["reconnects"].as_u64().unwrap_or(0) >= 1 {
                        break json;
                    }
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .expect("reconnect transition was not published");

        assert_eq!(json["route_ready"], true);
        assert_eq!(json["interface"]["ifindex"], 77);
        assert!(json["counters"]["reconnects"].as_u64().unwrap() >= 1);

        shutdown_tx.send(true).unwrap();
        task.await.unwrap().unwrap();
    }
}
