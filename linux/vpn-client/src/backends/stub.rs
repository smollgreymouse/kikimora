use crate::backend::{BackendError, BackendIo, BackendStatus, VpnBackend};
use crate::config::StubConfig;
use crate::state::StateReason;
use async_trait::async_trait;
use tokio::time::{interval, sleep, Duration, MissedTickBehavior};

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
        if io
            .status
            .send(BackendStatus::connecting(StateReason::ConnectStarted))
            .await
            .is_err()
        {
            return Ok(());
        }
        if io
            .status
            .send(BackendStatus::online(StateReason::StubOnline))
            .await
            .is_err()
        {
            return Ok(());
        }

        let reconnect_period = Duration::from_millis(self.config.reconnect_after_ms);
        let mut reconnect_tick = interval(reconnect_period);
        reconnect_tick.set_missed_tick_behavior(MissedTickBehavior::Delay);
        reconnect_tick.tick().await;
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
                            // Echo is intentionally only deterministic test behavior. It does
                            // not attempt to emulate IP semantics.
                            if io.to_tun.send(packet).await.is_err() {
                                return Ok(());
                            }
                        }
                        "blackhole" | "reconnect-once" | "reconnect-loop" => {
                            // A disconnected/blackhole backend is a fail-closed sink.
                        }
                        _ => return Err(BackendError::Config("invalid stub mode".into())),
                    }
                }
                _ = reconnect_tick.tick(), if should_reconnect(&self.config.mode, reconnect_done) => {
                    reconnect_done = true;
                    if io
                        .status
                        .send(BackendStatus::reconnecting(StateReason::StubReconnect))
                        .await
                        .is_err()
                    {
                        return Ok(());
                    }
                    sleep(reconnect_period).await;
                    if io
                        .status
                        .send(BackendStatus::online(StateReason::StubOnline))
                        .await
                        .is_err()
                    {
                        return Ok(());
                    }
                }
            }
        }
    }
}

fn should_reconnect(mode: &str, reconnect_done: bool) -> bool {
    mode == "reconnect-loop" || (mode == "reconnect-once" && !reconnect_done)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn reconnect_loop_never_marks_itself_done() {
        assert!(should_reconnect("reconnect-loop", false));
        assert!(should_reconnect("reconnect-loop", true));
        assert!(should_reconnect("reconnect-once", false));
        assert!(!should_reconnect("reconnect-once", true));
    }
}
