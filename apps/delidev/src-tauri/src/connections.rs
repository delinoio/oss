use std::{
    fs::{self, File},
    path::PathBuf,
};

use serde::{Deserialize, Serialize};
use zeroize::Zeroizing;

use crate::{
    Connection, Connector, DeviceMetadata, DeviceType, NativeFailure, Result, read_bounded,
    validated_connection,
};

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum SavedConnectionState {
    Pending,
    Paired,
}

#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct SavedConnection {
    pub version: u32,
    #[serde(default = "initial_revision")]
    pub revision: u64,
    pub id: String,
    pub name: String,
    pub endpoint: String,
    pub server_id: String,
    pub pairing_id: String,
    #[serde(default)]
    pub device_id: String,
    pub state: SavedConnectionState,
    pub created_at: String,
}

fn initial_revision() -> u64 {
    1
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Inventory {
    connections: Vec<SavedConnection>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Verification {
    profile: SavedConnection,
    server_version: String,
    protocol_version: u32,
    observed_at: String,
}

pub fn canonical_id(value: &str) -> Result<()> {
    let id = uuid::Uuid::parse_str(value).map_err(|_| NativeFailure::InvalidEvidence)?;
    if id.get_version_num() != 7 || id.to_string() != value {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(())
}
// The fixed Go inspector validates the original raw grant. This independent
// native check emits only one URL origin suitable as a single CSP source.
pub fn connection_origin(endpoint: &str) -> Result<String> {
    let url = url::Url::parse(endpoint).map_err(|_| NativeFailure::InvalidEvidence)?;
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path() != "/"
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    let loopback = match url.host() {
        Some(url::Host::Ipv4(ip)) => ip.is_loopback(),
        Some(url::Host::Ipv6(ip)) => ip.is_loopback(),
        Some(url::Host::Domain("localhost")) => true,
        _ => false,
    };
    if url.scheme() != "https" && !(url.scheme() == "http" && loopback) {
        return Err(NativeFailure::PermissionDenied);
    }
    let origin = url.origin().ascii_serialization();
    let default_port = if url.scheme() == "https" { 443 } else { 80 };
    let with_default = format!("{origin}:{default_port}");
    if endpoint != origin
        && endpoint != format!("{origin}/")
        && !(url.port().is_none()
            && (endpoint == with_default || endpoint == format!("{with_default}/")))
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(origin)
}
impl SavedConnection {
    pub fn validate(&self) -> Result<()> {
        if self.version != 1
            || !(1..=1025).contains(&self.revision)
            || self.name.is_empty()
            || self.name.len() > 256
            || self.created_at.len() > 64
            || self.created_at.is_empty()
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        for id in [&self.id, &self.server_id, &self.pairing_id] {
            canonical_id(id)?;
        }
        if self.state == SavedConnectionState::Paired || !self.device_id.is_empty() {
            canonical_id(&self.device_id)?;
        }
        connection_origin(&self.endpoint)?;
        Ok(())
    }

    // Display edits never replace a saved window's credential, server or
    // instance binding. Compare every original authority field independently.
    pub fn same_authority(&self, other: &Self) -> bool {
        self.version == other.version
            && self.id == other.id
            && self.endpoint == other.endpoint
            && self.server_id == other.server_id
            && self.pairing_id == other.pairing_id
            && self.device_id == other.device_id
            && self.state == other.state
            && self.created_at == other.created_at
    }

    fn metadata(&self) -> DeviceMetadata {
        DeviceMetadata {
            version: 1,
            kind: DeviceType::Client,
            endpoint: self.endpoint.clone(),
            server_id: self.server_id.clone(),
            device_id: self.device_id.clone(),
            pairing_id: self.pairing_id.clone(),
            machine_id: String::new(),
        }
    }
}
impl Connector {
    pub(crate) fn check_saved_profile(&self, expected: &SavedConnection) -> Result<()> {
        expected.validate()?;
        if expected.state != SavedConnectionState::Paired {
            return Err(NativeFailure::InvalidEvidence);
        }
        let current = Self::saved_result(
            self.run(&[
                "connection".into(),
                "inspect".into(),
                "--id".into(),
                expected.id.as_str().into(),
            ])?,
            &expected.id,
        )?;
        if !current.same_authority(expected) {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }

    pub fn saved_connections(&self) -> Result<Vec<SavedConnection>> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let values: Inventory =
            serde_json::from_value(self.run(&["connection".into(), "list".into()])?)
                .map_err(|_| NativeFailure::InvalidEvidence)?;
        if values.connections.len() > 32 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut seen = std::collections::HashSet::new();
        for value in &values.connections {
            value.validate()?;
            if !seen.insert(&value.id) {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        Ok(values.connections)
    }

    pub fn pair_saved(
        &self,
        id: &str,
        name: &str,
        grant: Zeroizing<Vec<u8>>,
    ) -> Result<SavedConnection> {
        canonical_id(id)?;
        if name.is_empty() || name.len() > 256 || grant.len() > 32 << 10 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let result = self.run_with_input(
            &[
                "connection".into(),
                "pair".into(),
                "--id".into(),
                id.into(),
                "--name".into(),
                name.into(),
                "--code-stdin".into(),
            ],
            Some(grant),
        )?;
        Self::saved_result(result, id)
    }

    pub fn retry_saved(&self, id: &str) -> Result<SavedConnection> {
        canonical_id(id)?;
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        Self::saved_result(
            self.run(&[
                "connection".into(),
                "retry".into(),
                "--id".into(),
                id.into(),
            ])?,
            id,
        )
    }

    pub fn rename_saved(
        &self,
        id: &str,
        request_id: &str,
        revision: u64,
        name: &str,
    ) -> Result<SavedConnection> {
        canonical_id(id)?;
        canonical_id(request_id)?;
        if !(1..=1025).contains(&revision) || name.is_empty() || name.len() > 256 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        Self::saved_result(
            self.run(&[
                "--request-id".into(),
                request_id.into(),
                "connection".into(),
                "rename".into(),
                "--id".into(),
                id.into(),
                "--revision".into(),
                revision.to_string().into(),
                "--name".into(),
                name.into(),
            ])?,
            id,
        )
    }

    pub fn inspect_saved(&self, id: &str) -> Result<SavedConnection> {
        canonical_id(id)?;
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        Self::saved_result(
            self.run(&[
                "connection".into(),
                "inspect".into(),
                "--id".into(),
                id.into(),
            ])?,
            id,
        )
    }

    fn saved_result(value: serde_json::Value, id: &str) -> Result<SavedConnection> {
        let result: SavedConnection =
            serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?;
        result.validate()?;
        if result.id != id {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(result)
    }

    pub fn connect_saved(&self, expected: &SavedConnection) -> Result<Connection> {
        expected.validate()?;
        if expected.state != SavedConnectionState::Paired {
            return Err(NativeFailure::InvalidEvidence);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let value: Verification = serde_json::from_value(self.run(&[
            "connection".into(),
            "verify".into(),
            "--id".into(),
            expected.id.as_str().into(),
        ])?)
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        if !value.profile.same_authority(expected)
            || value.server_version != "0.1.0"
            || value.protocol_version != 1
            || value.observed_at.is_empty()
        {
            return Err(NativeFailure::Incompatible);
        }
        let path: PathBuf = self
            .root
            .join("connections")
            .join(&expected.id)
            .join("client")
            .join("device.json");
        let metadata =
            fs::symlink_metadata(&path).map_err(|_| NativeFailure::CredentialUnavailable)?;
        if !metadata.is_file() || metadata.file_type().is_symlink() || metadata.len() > 16 << 10 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let bytes = Zeroizing::new(read_bounded(
            File::open(path).map_err(|_| NativeFailure::CredentialUnavailable)?,
            16 << 10,
        )?);
        validated_connection(&bytes, &expected.metadata(), DeviceType::Client, true)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn origins_never_grant_url_aliases_or_extra_csp_sources() {
        for (input, expected) in [
            ("https://example.test", "https://example.test"),
            ("https://example.test:443/", "https://example.test"),
            ("http://127.0.0.1:40123", "http://127.0.0.1:40123"),
            ("http://[::1]:1234/", "http://[::1]:1234"),
        ] {
            assert_eq!(connection_origin(input).unwrap(), expected);
        }
        for input in [
            "http://example.test",
            "https://user:secret@example.test",
            "https://example.test/path",
            "https://example.test/#",
            "https://example.test?",
            "https://example.test/%2f",
            "https://EXAMPLE.test",
            "https://127.1",
            "https://0x7f000001",
            "https://example.test:0443",
            "https://example.test;connect-src *",
            "https://example.test\\evil",
            "data:text/html,private",
        ] {
            assert!(connection_origin(input).is_err(), "{input}");
        }
    }
}
