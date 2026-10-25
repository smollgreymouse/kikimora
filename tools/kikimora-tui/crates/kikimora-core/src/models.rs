use serde::Deserialize;
use serde_json::Value;

#[derive(Clone, Debug, Deserialize)]
pub struct StatusResponse {
    pub schema_version: u32,
    pub service: String,
    pub services: ServiceStates,
    pub profiles: StatusProfiles,
    pub interfaces: ManagedInterfaces,
    pub dns: StatusDns,
    pub startup: StartupState,
    pub endpoint_underlay_migration_pending: bool,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ServiceStates {
    pub leshy: String,
    pub route_watch: String,
    pub health_watch: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct StatusProfiles {
    pub active: Option<String>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ManagedInterfaces {
    pub primary: InterfaceStatus,
    pub secondary: InterfaceStatus,
    pub dns: InterfaceStatus,
}

#[derive(Clone, Debug, Deserialize)]
pub struct InterfaceStatus {
    pub name: String,
    pub state: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct StatusDns {
    pub provider: String,
    pub default_zone: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct StartupState {
    pub enabled: bool,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ProfilesResponse {
    pub schema_version: u32,
    pub active: Option<String>,
    pub profiles: Vec<Profile>,
}

#[derive(Clone, Debug, Deserialize)]
pub struct Profile {
    pub name: String,
    pub active: bool,
    pub primary: ProfileRole,
    pub secondary: ProfileRole,
}

#[derive(Clone, Debug, Deserialize)]
pub struct ProfileRole {
    pub interface: String,
    pub endpoint_provider: String,
    pub provider_args: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct EndpointsResponse {
    pub schema_version: u32,
    pub roles: EndpointRoles,
}

#[derive(Clone, Debug, Deserialize)]
pub struct EndpointRoles {
    pub primary: EndpointRole,
    pub secondary: EndpointRole,
}

#[derive(Clone, Debug, Deserialize)]
pub struct EndpointRole {
    pub interface: String,
    pub provider: String,
    pub provider_args: String,
    pub state: String,
    pub pending: bool,
    #[serde(default)]
    pub configured: Vec<String>,
    #[serde(default)]
    pub installed: Vec<String>,
    pub actions: EndpointActions,
}

#[derive(Clone, Debug, Deserialize)]
pub struct EndpointActions {
    pub rediscover: bool,
    pub invalidate: bool,
}

#[derive(Clone, Debug, Deserialize)]
pub struct DnsResponse {
    pub schema_version: u32,
    pub provider: String,
    pub interface: InterfaceStatus,
    pub service: String,
    pub listen: String,
}

#[derive(Clone, Debug, Deserialize)]
pub struct LogsResponse {
    pub schema_version: u32,
    pub units: Vec<String>,
    pub limit: usize,
    #[serde(default)]
    pub entries: Vec<Value>,
}
