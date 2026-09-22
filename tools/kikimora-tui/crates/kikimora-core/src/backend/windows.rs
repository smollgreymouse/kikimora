use async_trait::async_trait;

use super::{Backend, BackendError, ServiceAction};
use crate::models::{DnsResponse, EndpointsResponse, LogsResponse, ProfilesResponse, StatusResponse};

#[derive(Clone, Debug, Default)]
pub struct WindowsBackend;

fn unsupported<T>() -> Result<T, BackendError> {
    Err(BackendError::new(
        "the Windows Kikimora backend is scaffolded but not implemented yet",
    ))
}

#[async_trait]
impl Backend for WindowsBackend {
    async fn status(&self) -> Result<StatusResponse, BackendError> { unsupported() }
    async fn profiles(&self) -> Result<ProfilesResponse, BackendError> { unsupported() }
    async fn endpoints(&self) -> Result<EndpointsResponse, BackendError> { unsupported() }
    async fn dns(&self) -> Result<DnsResponse, BackendError> { unsupported() }
    async fn logs(&self, _lines: usize) -> Result<LogsResponse, BackendError> { unsupported() }
    async fn service(&self, _action: ServiceAction) -> Result<(), BackendError> { unsupported() }
    async fn use_profile(&self, _name: &str) -> Result<(), BackendError> { unsupported() }
    async fn set_dns(&self, _use_leshy: bool) -> Result<(), BackendError> { unsupported() }
    async fn set_startup(&self, _enabled: bool) -> Result<(), BackendError> { unsupported() }
    async fn rediscover_endpoints(&self, _role: &str) -> Result<(), BackendError> { unsupported() }
    async fn invalidate_endpoint_cache(&self, _role: &str) -> Result<(), BackendError> { unsupported() }
}
