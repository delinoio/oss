// SPDX-License-Identifier: Apache-2.0
//! Shim installation, content-verified ownership and removal.

use std::{
    collections::BTreeMap,
    fs,
    io::{self, Write},
};

use anyhow::{Result, anyhow};
use serde::{Deserialize, Serialize};

use super::{Entry, State, Tool, private};
use crate::paths;

pub const MANIFEST_NAME: &str = ".cachecow-shims.json";
pub const MANIFEST_OWNER: &str = "cachecow-native-shims";

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
pub struct Manifest {
    pub version: i64,
    pub owner: String,
    pub hashes: BTreeMap<Tool, Vec<String>>,
}

#[derive(Deserialize)]
struct RawManifest {
    #[serde(default)]
    version: i64,
    #[serde(default)]
    owner: String,
    #[serde(default)]
    hashes: Option<BTreeMap<String, Vec<String>>>,
}

/// Shim file name for `tool` on this platform.
pub fn filename(tool: Tool) -> String {
    if cfg!(windows) {
        format!("{tool}.exe")
    } else {
        tool.as_str().to_string()
    }
}

pub fn digest(bytes: &[u8]) -> String {
    crate::jsonx::sha256_hex(bytes)
}

pub fn read_manifest(dir: &str) -> Result<Manifest> {
    let mut m = Manifest {
        version: 1,
        owner: MANIFEST_OWNER.to_string(),
        hashes: BTreeMap::new(),
    };
    let path = paths::join2(dir, MANIFEST_NAME);
    let st = match fs::symlink_metadata(&path) {
        Ok(st) => st,
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(m),
        Err(e) => return Err(e.into()),
    };
    if !st.file_type().is_file() {
        return Err(anyhow!("manifest is not a regular file: {path}"));
    }
    let bytes = fs::read(&path)?;
    // Like Go's json.Unmarshal into a prefilled manifest: absent fields keep
    // their defaults, while an explicit null hash map is invalid.
    let value: serde_json::Value = serde_json::from_slice(&bytes)?;
    let raw: RawManifest = serde_json::from_value(value.clone())?;
    let version = if value.get("version").is_some() {
        raw.version
    } else {
        1
    };
    let owner = if value.get("owner").is_some() {
        raw.owner
    } else {
        MANIFEST_OWNER.to_string()
    };
    let hashes = match value.get("hashes") {
        None => Some(BTreeMap::new()),
        Some(_) => raw.hashes,
    };
    let Some(hashes) = hashes else {
        return Err(anyhow!(
            "unknown shim manifest; restore a compatible CacheCow version"
        ));
    };
    if version != 1 || owner != MANIFEST_OWNER {
        return Err(anyhow!(
            "unknown shim manifest; restore a compatible CacheCow version"
        ));
    }
    m.version = version;
    m.owner = owner;
    for (name, values) in hashes {
        let Some(tool) = Tool::parse(&name) else {
            return Err(anyhow!("invalid shim manifest"));
        };
        if values.is_empty() || values.len() > 2 {
            return Err(anyhow!("invalid shim manifest"));
        }
        for h in &values {
            if !hex::decode(h).is_ok_and(|raw| raw.len() == 32) {
                return Err(anyhow!("invalid shim digest"));
            }
        }
        m.hashes.insert(tool, values);
    }
    Ok(m)
}

fn entry(dir: &str, tool: Tool, m: &Manifest) -> Result<Entry> {
    let mut e = Entry {
        tool,
        path: paths::join2(dir, &filename(tool)),
        state: State::Missing,
    };
    let st = match fs::symlink_metadata(&e.path) {
        Ok(st) => st,
        Err(err) if err.kind() == io::ErrorKind::NotFound => return Ok(e),
        Err(err) => return Err(err.into()),
    };
    e.state = State::Conflict;
    if !st.file_type().is_file() {
        return Ok(e);
    }
    let actual = digest(&fs::read(&e.path)?);
    if m.hashes
        .get(&tool)
        .is_some_and(|hashes| hashes.contains(&actual))
    {
        e.state = State::Owned;
    }
    Ok(e)
}

pub fn list(dir: &str) -> Result<Vec<Entry>> {
    let m = read_manifest(dir)?;
    Tool::ALL.into_iter().map(|t| entry(dir, t, &m)).collect()
}

pub fn install(dir: &str, executable: &str) -> Result<()> {
    private::private_dir(dir)?;
    let _lock = lock(dir)?;
    let mut m = read_manifest(dir)?;
    let entries = list(dir)?;
    for e in &entries {
        if e.state == State::Conflict {
            return Err(anyhow!(
                "shim conflict at {}; move the unrelated file or choose a different shim directory",
                e.path
            ));
        }
    }
    let bytes = fs::read(executable)?;
    if bytes.is_empty() {
        return Err(anyhow!("CacheCow executable is empty"));
    }
    let h = digest(&bytes);
    for e in &entries {
        let mut hashes = vec![h.clone()];
        if e.state == State::Owned {
            let old = digest(&fs::read(&e.path)?);
            if old != h {
                hashes.push(old);
            }
        }
        m.hashes.insert(e.tool, hashes);
    }
    save_manifest(dir, &m)?;
    for e in &entries {
        atomic_write(&e.path, &bytes, 0o700)?;
    }
    for t in Tool::ALL {
        m.hashes.insert(t, vec![h.clone()]);
    }
    save_manifest(dir, &m)?;
    tracing::debug!(directory = dir, "CacheCow shims installed");
    Ok(())
}

pub fn uninstall(dir: &str) -> Result<()> {
    match fs::symlink_metadata(dir) {
        Err(e) if e.kind() == io::ErrorKind::NotFound => return Ok(()),
        _ => {}
    }
    private::private_dir(dir)?;
    let _lock = lock(dir)?;
    let entries = list(dir)?;
    for e in &entries {
        if e.state == State::Conflict {
            return Err(anyhow!(
                "refusing to remove modified or unrelated shim {}; move it manually before \
                 retrying",
                e.path
            ));
        }
    }
    for e in &entries {
        if e.state == State::Owned {
            fs::remove_file(&e.path)?;
        }
    }
    match fs::remove_file(paths::join2(dir, MANIFEST_NAME)) {
        Err(e) if e.kind() == io::ErrorKind::NotFound => Ok(()),
        other => Ok(other?),
    }
}

pub fn save_manifest(dir: &str, m: &Manifest) -> Result<()> {
    let bytes = serde_json::to_vec(m)?;
    atomic_write(&paths::join2(dir, MANIFEST_NAME), &bytes, 0o600)
}

/// Exclusive creation with Go's private 0600 default mode.
fn create_new_private(path: &str, read: bool) -> io::Result<fs::File> {
    let mut options = fs::OpenOptions::new();
    options.read(read).write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    options.open(path)
}

/// Creates a new file `<dir>/<prefix><random>` exclusively with mode 0600.
pub fn create_temp(dir: &str, prefix: &str) -> io::Result<(fs::File, String)> {
    loop {
        let name = paths::join2(dir, &format!("{prefix}{}", uuid::Uuid::now_v7().simple()));
        match create_new_private(&name, true) {
            Ok(f) => return Ok((f, name)),
            Err(e) if e.kind() == io::ErrorKind::AlreadyExists => {}
            Err(e) => return Err(e),
        }
    }
}

/// Applies Go-style permission bits. On Windows only the read-only attribute
/// follows the owner write bit.
pub fn set_mode(file: &fs::File, mode: u32) -> io::Result<()> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        file.set_permissions(fs::Permissions::from_mode(mode))
    }
    #[cfg(windows)]
    {
        let mut permissions = file.metadata()?.permissions();
        permissions.set_readonly(mode & 0o200 == 0);
        file.set_permissions(permissions)
    }
}

struct TempGuard(String);

impl Drop for TempGuard {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

fn atomic_write(path: &str, bytes: &[u8], mode: u32) -> Result<()> {
    let (mut f, name) = create_temp(&paths::dir(path), ".cachecow-stage-")?;
    let _guard = TempGuard(name.clone());
    set_mode(&f, mode)?;
    f.write_all(bytes)?;
    f.sync_all()?;
    drop(f);
    fs::rename(&name, path)?;
    Ok(())
}

/// Removes the installation lock when dropped.
pub struct Lock(String);

impl Drop for Lock {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.0);
    }
}

pub fn lock(dir: &str) -> Result<Lock> {
    let p = paths::join2(dir, ".cachecow-install.lock");
    match create_new_private(&p, false) {
        Ok(_) => Ok(Lock(p)),
        Err(e) => Err(anyhow!(
            "shim installation is locked: open {p}: {e}; if no installer is running, remove {p} \
             and retry"
        )),
    }
}
