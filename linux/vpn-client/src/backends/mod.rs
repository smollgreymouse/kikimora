pub mod awg2;
pub mod stub;
pub mod vless_reality;

use crate::backend::{BackendError, VpnBackend};
use crate::config::{ClientConfig, ProtocolKind};
use awg2::Awg2Backend;
use stub::StubBackend;
use vless_reality::VlessRealityBackend;

pub fn build_backend(config: &ClientConfig) -> Result<Box<dyn VpnBackend>, BackendError> {
    match config.protocol {
        ProtocolKind::Stub => Ok(Box::new(StubBackend::new(config.stub.clone()))),
        ProtocolKind::Amneziawg2 => Ok(Box::new(Awg2Backend::new(config)?)),
        ProtocolKind::VlessReality => Ok(Box::new(VlessRealityBackend::new(config)?)),
    }
}
