//! Device-local browser ownership. No browsing content enters product RPCs.
use std::{
    ffi::OsString,
    fs::{self, File, OpenOptions},
    io::Write,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

use crate::{Connector, NativeFailure, Result, SavedConnection, canonical_id, connection_origin};
#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum ProfileState {
    Active,
    RemovalPending,
    Removed,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Profile {
    pub server_id: String,
    pub device_id: String,
    pub account_id: String,
    pub state: ProfileState,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub deletion_request_id: String,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ProfileRecord {
    pub id: String,
    pub revision: u64,
    pub data: Profile,
}
impl ProfileRecord {
    pub fn validate(&self) -> Result<()> {
        for id in [
            &self.id,
            &self.data.server_id,
            &self.data.device_id,
            &self.data.account_id,
        ] {
            canonical_id(id)?;
        }
        if self.revision == 0 {
            return Err(NativeFailure::InvalidEvidence);
        }
        match self.data.state {
            ProfileState::Active if self.data.deletion_request_id.is_empty() => Ok(()),
            ProfileState::RemovalPending | ProfileState::Removed => {
                canonical_id(&self.data.deletion_request_id)
            }
            _ => Err(NativeFailure::InvalidEvidence),
        }
    }
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Tab {
    pub id: String,
    pub url: String,
}
#[derive(Clone, Debug, Deserialize, Serialize, Default)]
#[serde(deny_unknown_fields)]
pub struct Tabs {
    pub tabs: Vec<Tab>,
    pub selected: String,
}
impl Tabs {
    pub fn validate(&self, policy: &Policy) -> Result<()> {
        if self.tabs.len() > 16 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut ids = std::collections::BTreeSet::new();
        for t in &self.tabs {
            canonical_id(&t.id)?;
            if !ids.insert(&t.id) || !policy.navigation(&t.url) {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        if (self.tabs.is_empty() && !self.selected.is_empty())
            || (!self.tabs.is_empty() && !ids.contains(&self.selected))
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}
#[derive(Clone, Debug)]
pub struct Policy {
    pub product_origin: String,
    pub loopback_origins: Vec<String>,
}
impl Policy {
    pub fn new(endpoint: &str, url: &str) -> Result<Self> {
        let product_origin = connection_origin(endpoint)?;
        let parsed = url::Url::parse(url).map_err(|_| NativeFailure::InvalidInput)?;
        let loopback_origin = if loopback(&parsed) {
            Some(parsed.origin().ascii_serialization())
        } else {
            None
        };
        let policy = Self {
            product_origin,
            loopback_origins: loopback_origin.into_iter().collect(),
        };
        if !policy.navigation(url) {
            return Err(NativeFailure::InvalidInput);
        };
        Ok(policy)
    }

    // Only a trusted address action extends this bounded loopback allowlist.
    // Redirects and external resource requests cannot add an origin.
    pub fn with_explicit(&self, raw: &str) -> Result<Self> {
        let url = url::Url::parse(raw).map_err(|_| NativeFailure::InvalidInput)?;
        let mut policy = self.clone();
        if loopback(&url) {
            let origin = url.origin().ascii_serialization();
            if !policy.loopback_origins.contains(&origin) {
                if policy.loopback_origins.len() >= 32 {
                    return Err(NativeFailure::InvalidInput);
                }
                policy.loopback_origins.push(origin);
            }
        }
        if !policy.navigation(raw) {
            return Err(NativeFailure::InvalidInput);
        }
        Ok(policy)
    }

    pub fn navigation(&self, raw: &str) -> bool {
        if raw.len() > 8192 || raw.contains(['\0', '\r', '\n']) {
            return false;
        }
        let Ok(url) = url::Url::parse(raw) else {
            return false;
        };
        matches!(url.scheme(), "http" | "https")
            && self.network(&url)
            && url.username().is_empty()
            && url.password().is_none()
    }

    pub fn resource(&self, raw: &str) -> bool {
        let Ok(url) = url::Url::parse(raw) else {
            return false;
        };
        match url.scheme() {
            "http" | "https" | "ws" | "wss" => self.network(&url),
            "data" | "blob" | "about" => true,
            _ => false,
        }
    }

    fn network(&self, url: &url::Url) -> bool {
        let mut authority = url.clone();
        if url.scheme() == "ws" {
            let _ = authority.set_scheme("http");
        }
        if url.scheme() == "wss" {
            let _ = authority.set_scheme("https");
        }
        let origin = authority.origin().ascii_serialization();
        if !url.username().is_empty()
            || url.password().is_some()
            || origin == self.product_origin
            || matches!(url.host_str(), Some("tauri.localhost" | "ipc.localhost"))
            || (loopback(url) && matches!(url.port_or_known_default(), Some(46310 | 46311)))
        {
            return false;
        }
        !loopback(url) || self.loopback_origins.contains(&origin)
    }
}
fn loopback(url: &url::Url) -> bool {
    match url.host() {
        Some(url::Host::Ipv4(ip)) => ip.is_loopback(),
        Some(url::Host::Ipv6(ip)) => {
            ip.is_loopback()
                || ip
                    .to_ipv4_mapped()
                    .is_some_and(|mapped| mapped.is_loopback())
        }
        Some(url::Host::Domain(host)) => host == "localhost" || host.ends_with(".localhost"),
        None => false,
    }
}
impl Connector {
    // Cleanup discovery is read-only and cannot own the interactive mutation
    // gate while a saved endpoint is offline. Give its independent controller
    // a short joined-child deadline; it uses the same validated private scope.
    pub fn browser_observer(&self) -> Result<Self> {
        let mut observer = Self::new(self.executable.clone(), self.root.clone())?;
        observer.command_timeout = std::time::Duration::from_secs(2);
        Ok(observer)
    }

    pub fn prepare_browser_storage(&self) -> Result<PathBuf> {
        self.run(&["browser-storage".into(), "prepare".into()])?;
        Ok(self.root.join("browser-data"))
    }

    pub fn browser_query(
        &self,
        saved: Option<&SavedConnection>,
        arguments: &[OsString],
    ) -> Result<serde_json::Value> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let client = if let Some(p) = saved {
            self.check_saved_profile(p)?;
            self.root.join("connections").join(&p.id).join("client")
        } else {
            self.root.join("desktop-client")
        };
        let mut args = vec!["--data-dir".into(), client.into_os_string()];
        let operations = if arguments.first().is_some_and(|s| s == "--request-id") {
            if arguments.len() < 3 {
                return Err(NativeFailure::InvalidInput);
            };
            args.extend_from_slice(&arguments[..2]);
            &arguments[2..]
        } else {
            arguments
        };
        args.push("browser-profile".into());
        args.extend_from_slice(operations);
        self.run(&args)
    }

    pub fn browser_profile(
        &self,
        saved: Option<&SavedConnection>,
        id: &str,
    ) -> Result<ProfileRecord> {
        canonical_id(id)?;
        let value = self.browser_query(saved, &["status".into(), "--id".into(), id.into()])?;
        let r: ProfileRecord = serde_json::from_value(
            value
                .get("profile")
                .cloned()
                .ok_or(NativeFailure::InvalidEvidence)?,
        )
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        r.validate()?;
        if r.id != id {
            return Err(NativeFailure::InvalidEvidence);
        };
        Ok(r)
    }

    pub fn browser_confirm(
        &self,
        saved: Option<&SavedConnection>,
        record: &ProfileRecord,
        request: &str,
    ) -> Result<()> {
        record.validate()?;
        canonical_id(request)?;
        let result = self.browser_query(
            saved,
            &[
                "--request-id".into(),
                request.into(),
                "confirm-removal".into(),
                "--id".into(),
                record.id.clone().into(),
                "--revision".into(),
                record.revision.to_string().into(),
                "--deletion-request-id".into(),
                record.data.deletion_request_id.clone().into(),
            ],
        )?;
        let confirmed: ProfileRecord = serde_json::from_value(
            result
                .get("profile")
                .cloned()
                .ok_or(NativeFailure::InvalidEvidence)?,
        )
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        confirmed.validate()?;
        if confirmed.id != record.id
            || confirmed.revision
                != record
                    .revision
                    .checked_add(1)
                    .ok_or(NativeFailure::InvalidEvidence)?
            || confirmed.data.server_id != record.data.server_id
            || confirmed.data.device_id != record.data.device_id
            || confirmed.data.account_id != record.data.account_id
            || confirmed.data.deletion_request_id != record.data.deletion_request_id
            || confirmed.data.state != ProfileState::Removed
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}
pub fn private_dir(path: &Path) -> Result<()> {
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
    }
    let metadata = fs::symlink_metadata(path).map_err(|_| NativeFailure::StorageUnavailable)?;
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(NativeFailure::InvalidEvidence);
    }
    #[cfg(windows)]
    {
        use std::os::windows::fs::MetadataExt;
        if metadata.file_attributes() & 0x400 != 0 {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if metadata.mode() & 0o077 != 0 || metadata.uid() != unsafe { libc::geteuid() } {
            tracing::warn!(
                operation_id = "",
                check = "file_owner_or_permissions",
                result = "mismatch",
                next_action = "continue",
                "ownership_observation"
            );
        }
    }
    Ok(())
}
pub fn profile_path(root: &Path, p: &ProfileRecord) -> Result<PathBuf> {
    p.validate()?;
    private_dir(root)?;
    let mut path = root.to_path_buf();
    for id in [&p.data.server_id, &p.data.device_id, &p.data.account_id] {
        path.push(id);
        private_dir(&path)?;
    }
    Ok(path)
}
pub fn write_private(path: &Path, value: &impl Serialize) -> Result<()> {
    stage_private(path, value)?.publish()
}

// Keep slow serialization/write/fsync separate from the ownership-checked
// replacement. Dropping an obsolete preparation leaves the prior file intact.
pub struct PrivateWrite {
    path: PathBuf,
    pending: PathBuf,
}
impl PrivateWrite {
    pub fn publish(self) -> Result<()> {
        replace_file(&self.pending, &self.path).map_err(|_| NativeFailure::StorageUnavailable)?;
        #[cfg(unix)]
        File::open(self.path.parent().unwrap())
            .and_then(|parent| parent.sync_all())
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        Ok(())
    }
}
impl Drop for PrivateWrite {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.pending);
    }
}
pub fn stage_private(path: &Path, value: &impl Serialize) -> Result<PrivateWrite> {
    let bytes = serde_json::to_vec(value).map_err(|_| NativeFailure::InvalidEvidence)?;
    if bytes.len() > 256 << 10 {
        return Err(NativeFailure::InvalidEvidence);
    }
    let pending = path.with_extension(format!("{}.pending", uuid::Uuid::now_v7()));
    let mut options = OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut f = options
        .open(&pending)
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    let staged = PrivateWrite {
        path: path.into(),
        pending,
    };
    let outcome = (|| {
        f.write_all(&bytes)?;
        f.sync_all()?;
        Ok::<_, std::io::Error>(())
    })();
    // Windows cannot remove an open staging file when preparation fails.
    drop(f);
    if outcome.is_err() {
        return Err(NativeFailure::StorageUnavailable);
    };
    Ok(staged)
}

#[cfg(not(windows))]
fn replace_file(from: &Path, to: &Path) -> std::io::Result<()> {
    fs::rename(from, to)
}
#[cfg(windows)]
fn replace_file(from: &Path, to: &Path) -> std::io::Result<()> {
    use std::os::windows::ffi::OsStrExt;
    #[link(name = "kernel32")]
    unsafe extern "system" {
        fn MoveFileExW(from: *const u16, to: *const u16, flags: u32) -> i32;
    }
    let from: Vec<u16> = from.as_os_str().encode_wide().chain(Some(0)).collect();
    let to: Vec<u16> = to.as_os_str().encode_wide().chain(Some(0)).collect();
    // Replacement and write-through preserve a retry's original durable
    // identity.
    if unsafe { MoveFileExW(from.as_ptr(), to.as_ptr(), 1 | 8) } == 0 {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[cfg(unix)]
    #[test]
    fn offline_cleanup_observation_has_a_separate_gate_and_joined_short_deadline() {
        use std::os::unix::fs::PermissionsExt;
        let temp = tempfile::tempdir().unwrap();
        let sidecar = temp.path().join("sidecar");
        fs::write(&sidecar, "#!/bin/sh\nexec /bin/sleep 10\n").unwrap();
        fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
        let interactive = Connector::new(sidecar, temp.path().to_path_buf()).unwrap();
        let mut observer = interactive.browser_observer().unwrap();
        assert_eq!(observer.command_timeout, std::time::Duration::from_secs(2));
        observer.command_timeout = std::time::Duration::from_millis(150);
        let _interactive_operation = interactive.gate.lock().unwrap();
        let started = std::time::Instant::now();
        assert!(matches!(
            observer.browser_query(None, &["list".into()]),
            Err(NativeFailure::TimedOut)
        ));
        assert!(started.elapsed() < std::time::Duration::from_secs(2));
        assert!(observer.gate.try_lock().is_ok());
    }
    #[test]
    fn hostile_urls_have_no_product_or_native_authority() {
        let p = Policy::new("http://127.0.0.1:46310", "https://example.test/").unwrap();
        for u in [
            "tauri://localhost/",
            "http://tauri.localhost/",
            "http://ipc.localhost/",
            "file:///etc/passwd",
            "http://127.0.0.1:46310/",
            "http://127.1:46311/",
            "http://localhost:46310/",
            "http://[::ffff:127.0.0.1]:46310/",
            "http://[::ffff:7f00:1]:46311/",
            "https://user:password@example.test/",
        ] {
            assert!(!p.navigation(u), "{u}")
        }
        assert!(p.navigation("https://example.test/path"));
        assert!(!p.resource("file:///tmp/secret"));
        assert!(!p.resource("http://127.0.0.1:46310/"));
    }
    #[test]
    fn forwarded_loopback_is_explicit_and_bounded() {
        let p = Policy::new("https://server.test", "http://127.0.0.1:40221/").unwrap();
        assert!(p.navigation("http://127.0.0.1:40221/app"));
        assert!(!p.navigation("http://127.0.0.1:40222/"));
        assert!(!p.navigation("https://server.test/"));
        assert!(!p.navigation("http://[::ffff:127.0.0.1]:40221/"));
        assert!(!p.resource("ws://[::ffff:127.0.0.1]:40221/socket"));
        let explicit = p.with_explicit("http://[::ffff:127.0.0.1]:40221/").unwrap();
        assert!(explicit.navigation("http://[::ffff:127.0.0.1]:40221/app"));
        assert!(!explicit.resource("http://[::ffff:127.0.0.1]:40222/"));
        assert!(!explicit.resource("ws://[::ffff:127.0.0.1]:46310/"));
    }
    #[test]
    fn profiles_share_only_original_scope_and_reject_symlinks() {
        let temp = tempfile::tempdir().unwrap();
        let make = |server, device, account| ProfileRecord {
            id: uuid::Uuid::now_v7().to_string(),
            revision: 1,
            data: Profile {
                server_id: server,
                device_id: device,
                account_id: account,
                state: ProfileState::Active,
                deletion_request_id: String::new(),
            },
        };
        let p = make(
            uuid::Uuid::now_v7().to_string(),
            uuid::Uuid::now_v7().to_string(),
            uuid::Uuid::now_v7().to_string(),
        );
        let root = temp.path().join("cef");
        let original = profile_path(&root, &p).unwrap();
        assert_eq!(original, profile_path(&root, &p).unwrap());
        let mut other = p.clone();
        other.data.account_id = uuid::Uuid::now_v7().to_string();
        assert_ne!(original, profile_path(&root, &other).unwrap());
        other.data.server_id = uuid::Uuid::now_v7().to_string();
        assert_ne!(original, profile_path(&root, &other).unwrap());
        #[cfg(unix)]
        {
            fs::remove_dir(&original).unwrap();
            std::os::unix::fs::symlink(temp.path(), &original).unwrap();
            assert!(profile_path(&root, &p).is_err());
        }
    }
    #[test]
    fn local_tabs_survive_atomic_replacement_and_restart_without_product_data() {
        let temp = tempfile::tempdir().unwrap();
        let root = temp.path().join("profile");
        private_dir(&root).unwrap();
        let policy = Policy::new("https://product.test", "https://fixture.test/").unwrap();
        let tab = Tab {
            id: uuid::Uuid::now_v7().to_string(),
            url: "https://fixture.test/original".into(),
        };
        let mut tabs = Tabs {
            selected: tab.id.clone(),
            tabs: vec![tab],
        };
        let path = root.join("tabs.json");
        write_private(&path, &tabs).unwrap();
        tabs.tabs[0].url = "https://fixture.test/reloaded".into();
        write_private(&path, &tabs).unwrap();
        let restarted: Tabs = serde_json::from_slice(&fs::read(path).unwrap()).unwrap();
        restarted.validate(&policy).unwrap();
        assert_eq!(restarted.tabs[0].url, "https://fixture.test/reloaded");
        assert_eq!(fs::read_dir(root).unwrap().count(), 1);
        assert!(!policy.resource("wss://product.test/socket"));
        assert!(Policy::new("https://remote-product.test", "http://127.0.0.1:46310/").is_err());
    }
}
