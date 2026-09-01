use crate::backend::{BackendError, BackendIo, BackendStatus, VpnBackend};
use crate::config::StubConfig;
use crate::state::StateReason;
use async_trait::async_trait;
use tokio::time::{sleep, Duration};

pub struct StubBackend {
    config: StubConfig,
}

impl StubBackend {
    pub fn new(config: StubConfig) -> Self {
        Self { config }
    }
}

#[async_trait]
impl VpnBackend for StubBackend {
    fn protocol_name(&self) -> &'static str {
        "stub"
    }

    async fn run(&mut self, mut io: BackendIo) -> Result<(), BackendError> {
        let _ = io
            .status
            .send(BackendStatus::connecting(StateReason::ConnectStarted));
        let _ = io
            .status
            .send(BackendStatus::online(StateReason::StubOnline));

        let mut reconnect_done = false;
        loop {
            tokio::select! {
                changed = io.shutdown.changed() => {
                    if changed.is_err() || *io.shutdown.borrow() {
                        return Ok(());
                    }
                }
                packet = io.from_tun.recv() => {
                    let Some(packet) = packet else { return Ok(()); };
                    match self.config.mode.as_str() {
                        "online" => {
                            // Echo is intentionally only a deterministic test behavior. It does
                            // not attempt to emulate IP semantics.
                            if io.to_tun.send(packet).await.is_err() {
                                return Ok(());
                            }
                        }
                        "blackhole" | "reconnect-once" => {
                            // A disconnected/blackhole backend is a fail-closed sink.
                        }
                        _ => return Err(BackendError::Config("invalid stub mode".into())),
                    }
                }
                _ = sleep(Duration::from_millis(self.config.reconnect_after_ms)), if self.config.mode == "reconnect-once" && !reconnect_done => {
                    reconnect_done = true;
                    let _ = io.status.send(BackendStatus::reconnecting(StateReason::StubReconnect));
                    sleep(Duration::from_millis(self.config.reconnect_after_ms)).await;
                    let _ = io.status.send(BackendStatus::online(StateReason::StubOnline));
                }
            }
        }
    }
}
