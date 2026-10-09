// SPDX-License-Identifier: Apache-2.0
//! Trusted original-connection observations and private once-only edge storage.
use std::{
    fs::{self, File, OpenOptions},
    io::{Read, Write},
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use zeroize::Zeroizing;

use crate::{
    Connection, Connector, NativeFailure, canonical_id,
    notifications::{Notice, NotificationKind},
};

#[derive(Clone, Debug, Deserialize)]
#[serde(tag = "state", rename_all = "kebab-case", deny_unknown_fields)]
pub enum Observation {
    AuthenticatedSuccess {
        server_id: String,
        revision: Option<String>,
        lost: Option<bool>,
        restored: Option<bool>,
    },
    NetworkUnavailable {},
    NetworkDeadline {},
    Unusable {},
}
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Preferences {
    version: u32,
    server_id: String,
    client_id: String,
    revision: u64,
    lost: bool,
    restored: bool,
}
#[derive(Default)]
pub struct Transition {
    baseline: bool,
    failures: u8,
    lost: bool,
}
impl Transition {
    pub fn observe(
        &mut self,
        value: &Observation,
        server: &str,
    ) -> Result<Option<NotificationKind>, NativeFailure> {
        match value {
            Observation::AuthenticatedSuccess { server_id, .. } if server_id == server => {
                let restored = self.baseline && self.lost;
                self.baseline = true;
                self.failures = 0;
                self.lost = false;
                Ok(restored.then_some(NotificationKind::ServerRestored))
            }
            Observation::NetworkUnavailable {} | Observation::NetworkDeadline {} => {
                if !self.baseline {
                    return Ok(None);
                }
                self.failures = self.failures.saturating_add(1);
                if self.failures >= 2 && !self.lost {
                    self.lost = true;
                    Ok(Some(NotificationKind::ServerLost))
                } else {
                    Ok(None)
                }
            }
            _ => Err(NativeFailure::InvalidEvidence),
        }
    }
}
impl Connector {
    pub fn observe_notification_connection(
        &self,
        connection: &Connection,
    ) -> Result<Observation, NativeFailure> {
        canonical_id(&connection.server_id)?;
        canonical_id(&connection.device_id)?;
        let args = [
            "--server".into(),
            connection.endpoint.as_str().into(),
            "--token-stdin".into(),
            "notification".into(),
            "observe-connection".into(),
        ];
        let value = self.short_request_with_input_mode(
            &args,
            Some(Zeroizing::new(connection.token.as_bytes().to_vec())),
            std::time::Duration::from_secs(12),
            true,
        )?;
        serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)
    }

    pub fn notification_connection_ledger(
        &self,
        connection: &Connection,
        profile: Option<&str>,
    ) -> Result<Ledger, NativeFailure> {
        canonical_id(&connection.server_id)?;
        canonical_id(&connection.device_id)?;
        let mut digest = Sha256::new();
        for value in [
            connection.server_id.as_str(),
            connection.device_id.as_str(),
            connection.endpoint.as_str(),
            connection.runtime_generation.as_deref().unwrap_or(""),
            profile.unwrap_or(""),
            connection.token.as_str(),
        ] {
            digest.update(value.len().to_le_bytes());
            digest.update(value.as_bytes());
        }
        let key = digest
            .finalize()
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect::<String>();
        let root = self.root.join("desktop-notification-observations");
        private_directory(&root)?;
        let directory = root.join(&key);
        // Never evict an immutable original scope to make room for replacement
        // authority. A full private ledger suppresses presentation.
        if !directory.exists()
            && fs::read_dir(&root)
                .map_err(|_| NativeFailure::StorageUnavailable)?
                .take(257)
                .count()
                >= 256
        {
            return Err(NativeFailure::Busy);
        }
        private_directory(&directory)?;
        Ok(Ledger {
            directory,
            key,
            server: connection.server_id.clone(),
            client: connection.device_id.clone(),
        })
    }
}
pub struct Ledger {
    directory: PathBuf,
    pub key: String,
    server: String,
    client: String,
}
impl Ledger {
    pub fn acknowledged(&self, value: &Observation) -> Result<bool, NativeFailure> {
        let Observation::AuthenticatedSuccess {
            server_id,
            revision,
            lost,
            restored,
        } = value
        else {
            return Err(NativeFailure::InvalidEvidence);
        };
        if server_id != &self.server {
            return Err(NativeFailure::InvalidEvidence);
        }
        if revision.is_none() && lost.is_none() && restored.is_none() {
            return Ok(false);
        }
        let (Some(revision), Some(lost), Some(restored)) = (revision, lost, restored) else {
            return Err(NativeFailure::InvalidEvidence);
        };
        let generation = revision
            .parse::<u64>()
            .map_err(|_| NativeFailure::InvalidEvidence)?;
        if server_id != &self.server
            || generation == 0
            || generation >= i64::MAX as u64
            || generation.to_string() != *revision
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let next = Preferences {
            version: 1,
            server_id: self.server.clone(),
            client_id: self.client.clone(),
            revision: generation,
            lost: *lost,
            restored: *restored,
        };
        let mut reset_baseline = true;
        if let Some(previous) = self.preferences()? {
            reset_baseline = previous.lost != next.lost || previous.restored != next.restored;
            if previous.revision > generation || previous.revision == generation && previous != next
            {
                return Err(NativeFailure::InvalidEvidence);
            }
            if previous == next {
                return Ok(false);
            }
        }
        let path = self.directory.join("preferences.json");
        let temporary = self
            .directory
            .join(format!(".preferences-{}", uuid::Uuid::now_v7()));
        write_new(&temporary, &next)?;
        let result = crate::appearance::replace(&temporary, &path)
            .map_err(|_| NativeFailure::StorageUnavailable)
            .and_then(|_| sync_directory(&self.directory));
        let _ = fs::remove_file(&temporary);
        result.map(|()| reset_baseline)
    }

    fn preferences(&self) -> Result<Option<Preferences>, NativeFailure> {
        let path = self.directory.join("preferences.json");
        let metadata = match fs::symlink_metadata(&path) {
            Ok(v) => v,
            Err(e) if e.kind() == std::io::ErrorKind::NotFound => return Ok(None),
            Err(_) => return Err(NativeFailure::StorageUnavailable),
        };
        if !metadata.is_file() || linked(&metadata) || metadata.len() > 4096 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut bytes = Vec::new();
        File::open(path)
            .and_then(|f| f.take(4097).read_to_end(&mut bytes))
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        let value: Preferences =
            serde_json::from_slice(&bytes).map_err(|_| NativeFailure::InvalidEvidence)?;
        if value.version != 1
            || value.server_id != self.server
            || value.client_id != self.client
            || value.revision == 0
            || value.revision >= i64::MAX as u64
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(Some(value))
    }

    pub fn reserve(&self, kind: NotificationKind) -> Result<Option<Notice>, NativeFailure> {
        let Some(preferences) = self.preferences()? else {
            return Ok(None);
        };
        let enabled = match kind {
            NotificationKind::ServerLost => preferences.lost,
            NotificationKind::ServerRestored => preferences.restored,
            _ => return Err(NativeFailure::InvalidEvidence),
        };
        if !enabled {
            return Ok(None);
        }
        // Never evict or replay retained edges. Exhaustion fails closed. Files
        // are immutable create-new reservations synchronized before OS
        // submission.
        if fs::read_dir(&self.directory)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .take(10002)
            .count()
            >= 10001
        {
            return Err(NativeFailure::Busy);
        }
        let id = uuid::Uuid::now_v7().to_string();
        #[derive(Serialize)]
        struct Edge<'a> {
            version: u32,
            id: &'a str,
            kind: NotificationKind,
            preference_generation: u64,
            server_id: &'a str,
            client_id: &'a str,
        }
        write_new(
            &self.directory.join(format!("edge-{id}.json")),
            &Edge {
                version: 1,
                id: &id,
                kind,
                preference_generation: preferences.revision,
                server_id: &self.server,
                client_id: &self.client,
            },
        )?;
        sync_directory(&self.directory)?;
        Ok(Some(Notice {
            claim_id: id,
            inbox_id: String::new(),
            kind,
        }))
    }
}
fn linked(metadata: &fs::Metadata) -> bool {
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        metadata.file_attributes() & 0x400 != 0
    }
    #[cfg(not(windows))]
    {
        metadata.file_type().is_symlink()
    }
}
fn private_directory(path: &Path) -> Result<(), NativeFailure> {
    if !path.exists() {
        let mut builder = fs::DirBuilder::new();
        #[cfg(unix)]
        {
            use std::os::unix::fs::DirBuilderExt;
            builder.mode(0o700);
        }
        builder
            .create(path)
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        if let Some(parent) = path.parent() {
            sync_directory(parent)?;
        }
    }
    let metadata = fs::symlink_metadata(path).map_err(|_| NativeFailure::StorageUnavailable)?;
    if !metadata.is_dir() || linked(&metadata) {
        return Err(NativeFailure::InvalidEvidence);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.mode() & 0o077 != 0 || metadata.uid() != unsafe { libc::geteuid() } {
            return Err(NativeFailure::PermissionDenied);
        }
    }
    Ok(())
}
fn write_new(path: &Path, value: &impl Serialize) -> Result<(), NativeFailure> {
    let bytes = serde_json::to_vec(value).map_err(|_| NativeFailure::InvalidEvidence)?;
    if bytes.len() > 4096 {
        return Err(NativeFailure::InvalidEvidence);
    }
    let mut options = OpenOptions::new();
    options.create_new(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut file = options
        .open(path)
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    file.write_all(&bytes)
        .and_then(|_| file.sync_all())
        .map_err(|_| NativeFailure::StorageUnavailable)
}
fn sync_directory(path: &Path) -> Result<(), NativeFailure> {
    #[cfg(unix)]
    {
        File::open(path)
            .and_then(|f| f.sync_all())
            .map_err(|_| NativeFailure::StorageUnavailable)?;
    }
    #[cfg(not(unix))]
    let _ = path;
    Ok(())
}
#[cfg(test)]
mod tests {
    use super::*;
    fn success(server: &str) -> Observation {
        Observation::AuthenticatedSuccess {
            server_id: server.into(),
            revision: Some("1".into()),
            lost: Some(true),
            restored: Some(true),
        }
    }
    #[test]
    fn original_success_two_raw_failures_one_recovery_and_no_restart_edges() {
        let server = uuid::Uuid::now_v7().to_string();
        let mut state = Transition::default();
        assert_eq!(
            state
                .observe(&Observation::NetworkUnavailable {}, &server)
                .unwrap(),
            None
        );
        assert_eq!(state.observe(&success(&server), &server).unwrap(), None);
        assert_eq!(
            state
                .observe(&Observation::NetworkDeadline {}, &server)
                .unwrap(),
            None
        );
        assert_eq!(
            state
                .observe(&Observation::NetworkUnavailable {}, &server)
                .unwrap(),
            Some(NotificationKind::ServerLost)
        );
        assert_eq!(
            state
                .observe(&Observation::NetworkUnavailable {}, &server)
                .unwrap(),
            None
        );
        assert_eq!(
            state.observe(&success(&server), &server).unwrap(),
            Some(NotificationKind::ServerRestored)
        );
        assert_eq!(state.observe(&success(&server), &server).unwrap(), None);
        assert!(state.observe(&Observation::Unusable {}, &server).is_err());
        assert!(state.observe(&success("replacement"), &server).is_err());
        assert_eq!(
            Transition::default()
                .observe(&success(&server), &server)
                .unwrap(),
            None
        );
    }
    #[test]
    fn acknowledged_generation_and_immutable_reservation_survive_reopen() {
        let root = tempfile::tempdir().unwrap();
        let ledger = Ledger {
            directory: root.path().into(),
            key: "opaque".into(),
            server: uuid::Uuid::now_v7().to_string(),
            client: uuid::Uuid::now_v7().to_string(),
        };
        assert!(
            ledger
                .reserve(NotificationKind::ServerLost)
                .unwrap()
                .is_none()
        );
        ledger.acknowledged(&success(&ledger.server)).unwrap();
        let edge = ledger
            .reserve(NotificationKind::ServerLost)
            .unwrap()
            .unwrap();
        assert!(edge.inbox_id.is_empty());
        assert!(
            root.path()
                .join(format!("edge-{}.json", edge.claim_id))
                .exists()
        );
        let mut changed = success(&ledger.server);
        if let Observation::AuthenticatedSuccess { lost, .. } = &mut changed {
            *lost = Some(false)
        };
        assert!(ledger.acknowledged(&changed).is_err());
        assert_eq!(ledger.preferences().unwrap().unwrap().revision, 1);
        assert!(
            serde_json::from_value::<Observation>(
                serde_json::json!({"state":"network-unavailable","diagnostic":"private"})
            )
            .is_err()
        );
    }
}
