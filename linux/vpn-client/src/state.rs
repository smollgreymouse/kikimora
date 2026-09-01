use serde::Serialize;
use std::fs::{self, File};
use std::io;
use std::os::unix::fs::PermissionsExt;
use std::path::{Path, PathBuf};
use std::time::{SystemTime, UNIX_EPOCH};
use uuid::Uuid;

pub const STATE_SCHEMA_VERSION: u32 = 1;

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum ClientState {
    Starting,
    Armed,
    Connecting,
    Online,
    Reconnecting,
    Degraded,
    Failed,
    Stopping,
}

#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum StateReason {
    Startup,
    ConfigLoaded,
    TunCreated,
    ConnectStarted,
    HandshakeEstablished,
    TransportEstablished,
    HandshakeTimeout,
    TransportReset,
    EndpointUnreachable,
    EndpointReresolved,
    RetryBackoff,
    RetryWindowExceeded,
    ConfigInvalid,
    TunFatal,
    BackendFatal,
    ShutdownRequested,
    StubOnline,
    StubReconnect,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct InterfaceState {
    pub name: String,
    pub ifindex: u32,
    pub mtu: u16,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct EndpointState {
    pub address: String,
    pub port: u16,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct SessionState {
    pub connected: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub last_handshake_age_ms: Option<u64>,
}

#[derive(Debug, Clone, Default, Serialize, PartialEq, Eq)]
pub struct RuntimeCounters {
    pub reconnects: u64,
    pub rx_bytes: u64,
    pub tx_bytes: u64,
    pub dropped_packets: u64,
}

#[derive(Debug, Clone, Serialize, PartialEq, Eq)]
pub struct StateSnapshot {
    pub schema: u32,
    pub name: String,
    pub protocol: String,
    pub instance_id: String,
    pub generation: u64,
    pub updated_at_unix_ms: u64,
    pub state: ClientState,
    pub reason: StateReason,
    pub route_ready: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub interface: Option<InterfaceState>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub endpoint: Option<EndpointState>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub session: Option<SessionState>,
    pub counters: RuntimeCounters,
}

impl StateSnapshot {
    pub fn new(name: impl Into<String>, protocol: impl Into<String>) -> Self {
        Self {
            schema: STATE_SCHEMA_VERSION,
            name: name.into(),
            protocol: protocol.into(),
            instance_id: Uuid::new_v4().to_string(),
            generation: 0,
            updated_at_unix_ms: now_unix_ms(),
            state: ClientState::Starting,
            reason: StateReason::Startup,
            route_ready: false,
            interface: None,
            endpoint: None,
            session: None,
            counters: RuntimeCounters::default(),
        }
    }

    pub fn transition(&mut self, state: ClientState, reason: StateReason) {
        if self.state != state || self.reason != reason {
            self.generation = self.generation.saturating_add(1);
        }
        self.state = state;
        self.reason = reason;
        self.updated_at_unix_ms = now_unix_ms();
    }

    pub fn heartbeat(&mut self) {
        self.updated_at_unix_ms = now_unix_ms();
    }
}

#[derive(Debug, Clone)]
pub struct StatePublisher {
    dir: PathBuf,
    path: PathBuf,
}

impl StatePublisher {
    pub fn new(dir: impl Into<PathBuf>) -> Self {
        let dir = dir.into();
        let path = dir.join("state.json");
        Self { dir, path }
    }

    pub fn path(&self) -> &Path {
        &self.path
    }

    pub fn publish(&self, snapshot: &StateSnapshot) -> io::Result<()> {
        fs::create_dir_all(&self.dir)?;
        fs::set_permissions(&self.dir, fs::Permissions::from_mode(0o755))?;

        let temporary = self.dir.join(format!(
            ".state.json.tmp.{}.{}",
            std::process::id(),
            snapshot.generation
        ));
        let data = serde_json::to_vec_pretty(snapshot)
            .map_err(|err| io::Error::new(io::ErrorKind::InvalidData, err))?;

        fs::write(&temporary, data)?;
        fs::set_permissions(&temporary, fs::Permissions::from_mode(0o644))?;
        File::open(&temporary)?.sync_all()?;
        fs::rename(&temporary, &self.path)?;
        File::open(&self.dir)?.sync_all()?;
        Ok(())
    }
}

pub fn now_unix_ms() -> u64 {
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

    #[test]
    fn generation_changes_only_for_semantic_transition() {
        let mut state = StateSnapshot::new("test", "stub");
        assert_eq!(state.generation, 0);
        state.transition(ClientState::Starting, StateReason::Startup);
        assert_eq!(state.generation, 0);
        state.transition(ClientState::Armed, StateReason::TunCreated);
        assert_eq!(state.generation, 1);
        state.heartbeat();
        assert_eq!(state.generation, 1);
    }

    #[test]
    fn publisher_is_atomic_and_contains_no_unmodeled_secret_fields() {
        let dir = tempfile::tempdir().unwrap();
        let publisher = StatePublisher::new(dir.path());
        let mut state = StateSnapshot::new("test", "stub");
        state.interface = Some(InterfaceState {
            name: "kk-test0".into(),
            ifindex: 7,
            mtu: 1380,
        });
        state.route_ready = true;
        publisher.publish(&state).unwrap();

        let text = std::fs::read_to_string(publisher.path()).unwrap();
        assert!(text.contains("\"route_ready\": true"));
        for forbidden in ["private_key", "PrivateKey", "password", "reality_private_key"] {
            assert!(!text.contains(forbidden));
        }
        let leftovers: Vec<_> = std::fs::read_dir(dir.path())
            .unwrap()
            .filter_map(Result::ok)
            .filter(|entry| entry.file_name().to_string_lossy().starts_with(".state.json.tmp"))
            .collect();
        assert!(leftovers.is_empty());
    }
}
