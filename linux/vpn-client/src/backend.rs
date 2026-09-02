use crate::state::{ClientState, EndpointState, StateReason};
use async_trait::async_trait;
use thiserror::Error;
use tokio::sync::{mpsc, watch};

#[derive(Debug, Clone, PartialEq, Eq)]
pub struct BackendStatus {
    pub state: ClientState,
    pub reason: StateReason,
    pub connected: bool,
    pub endpoint: Option<EndpointState>,
    pub reconnect_increment: u64,
}

impl BackendStatus {
    pub fn connecting(reason: StateReason) -> Self {
        Self {
            state: ClientState::Connecting,
            reason,
            connected: false,
            endpoint: None,
            reconnect_increment: 0,
        }
    }

    pub fn online(reason: StateReason) -> Self {
        Self {
            state: ClientState::Online,
            reason,
            connected: true,
            endpoint: None,
            reconnect_increment: 0,
        }
    }

    pub fn reconnecting(reason: StateReason) -> Self {
        Self {
            state: ClientState::Reconnecting,
            reason,
            connected: false,
            endpoint: None,
            reconnect_increment: 1,
        }
    }
}

pub struct BackendIo {
    pub from_tun: mpsc::Receiver<Vec<u8>>,
    pub to_tun: mpsc::Sender<Vec<u8>>,
    pub status: mpsc::Sender<BackendStatus>,
    pub shutdown: watch::Receiver<bool>,
}

#[derive(Debug, Error)]
pub enum BackendError {
    #[error("backend configuration error: {0}")]
    Config(String),
    #[error("backend I/O error: {0}")]
    Io(#[from] std::io::Error),
    #[error("backend failed: {0}")]
    Fatal(String),
}

#[async_trait]
pub trait VpnBackend: Send {
    fn protocol_name(&self) -> &'static str;
    async fn run(&mut self, io: BackendIo) -> Result<(), BackendError>;
}
