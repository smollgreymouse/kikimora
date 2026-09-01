pub mod stub;

use crate::backend::{BackendError, VpnBackend};
use crate::config::{ClientConfig, ProtocolKind};
use stub::StubBackend;

pub fn build_backend(config: &ClientConfig) -> Result<Box<dyn VpnBackend>, BackendError> {
    match config.protocol {
        ProtocolKind::Stub => Ok(Box::new(StubBackend::new(config.stub.clone()))),
        ProtocolKind::Amneziawg2 => Err(BackendError::Config(
            "AmneziaWG2 backend is reserved by the Stage 0 contract but is not enabled until its reference interoperability gate is present".into(),
        )),
        ProtocolKind::VlessReality => Err(BackendError::Config(
            "VLESS/REALITY backend is reserved by the Stage 0 contract but is not enabled until its reference interoperability gate is present".into(),
        )),
    }
}
