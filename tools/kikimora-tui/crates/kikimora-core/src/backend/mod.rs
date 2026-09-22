use std::error::Error;
use std::fmt::{Display, Formatter};

use async_trait::async_trait;

use crate::models::{DnsResponse, EndpointsResponse, LogsResponse, ProfilesResponse, StatusResponse};

#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "macos")]
mod macos;
#[cfg(target_os = "windows")]
mod windows;

#[cfg(target_os = "linux")]
pub use linux::LinuxBackend as PlatformBackend;
#[cfg(target_os = "macos")]
pub use macos::MacosBackend as PlatformBackend;
#[cfg(target_os = "windows")]
pub use windows::WindowsBackend as PlatformBackend;

#[derive(Clone, Copy, Debug)]
pub enum ServiceAction {
    Start,
    Stop,
    Restart,
}

impl ServiceAction {
    pub fn as_cli_arg(self) -> &'static str {
        match self {
            Self::Start => "start",
            Self::Stop => "stop",
            Self::Restart => "restart",
        }
    }
}

#[derive(Debug)]
pub struct BackendError {
    message: String,
}

impl BackendError {
    pub fn new(message: impl Into<String>) -> Self {
        Self {
            message: message.into(),
        }
    }
}

impl Display for BackendError {
    fn fmt(&self, f: &mut Formatter<'_>) -> std::fmt::Result {
        f.write_str(&self.message)
    }
}

impl Error for BackendError {}

#[async_trait]
pub trait Backend: Send + Sync {
    async fn status(&self) -> Result<StatusResponse, BackendError>;
    async fn profiles(&self) -> Result<ProfilesResponse, BackendError>;
    async fn endpoints(&self) -> Result<EndpointsResponse, BackendError>;
    async fn dns(&self) -> Result<DnsResponse, BackendError>;
    async fn logs(&self, lines: usize) -> Result<LogsResponse, BackendError>;

    async fn service(&self, action: ServiceAction) -> Result<(), BackendError>;
    async fn use_profile(&self, name: &str) -> Result<(), BackendError>;
    async fn set_dns(&self, use_leshy: bool) -> Result<(), BackendError>;
    async fn set_startup(&self, enabled: bool) -> Result<(), BackendError>;
    async fn rediscover_endpoints(&self, role: &str) -> Result<(), BackendError>;
    async fn invalidate_endpoint_cache(&self, role: &str) -> Result<(), BackendError>;
}
