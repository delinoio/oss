use std::{
    collections::{BTreeMap, BTreeSet},
    fs,
    io::{ErrorKind, Write},
    path::{Component, Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use tracing::{debug, info};

use crate::{
    assets::{ArtifactKind, CandidateDecision},
    contract::{ArchiveFormat, ChecksumSource, HostTarget, SourceProvider, SourceSpec, TargetOs},
    error::{BinpmError, Result},
};

pub const MANIFEST_FILE: &str = "binpm.toml";
pub const LOCKFILE_FILE: &str = "binpm.lock";
const STORAGE_VERSION: u8 = 1;

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Manifest {
    pub version: u8,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub tools: BTreeMap<String, ManifestTool>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ManifestTool {
    pub source: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub version: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub bin: Option<String>,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub targets: BTreeMap<String, ManifestTargetOverride>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ManifestTargetOverride {
    pub asset: String,
    pub bin: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub checksum_source: Option<ChecksumSource>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize)]
pub struct Lockfile {
    pub version: u8,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub tools: BTreeMap<String, LockTool>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct LockTool {
    pub source: String,
    #[serde(default, skip_serializing_if = "BTreeMap::is_empty")]
    pub targets: BTreeMap<String, PackageRecord>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct CacheRecord {
    pub version: u8,
    pub cache_key: String,
    pub source_provider: SourceProvider,
    pub source_host: String,
    pub source_path: String,
    pub release_tag: String,
    pub asset_name: String,
    pub asset_url: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub byte_size: Option<u64>,
    pub sha256: String,
    pub checksum_source: ChecksumSource,
    pub created_at: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub last_used_at: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct PackageRecord {
    pub package_spec: String,
    pub source: String,
    pub source_provider: SourceProvider,
    pub source_host: String,
    pub source_path: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub requested_version: Option<String>,
    pub release_tag: String,
    pub asset_name: String,
    pub asset_url: String,
    pub target_os: crate::contract::TargetOs,
    pub target_arch: crate::contract::TargetArch,
    pub target_libc: crate::contract::TargetLibc,
    pub archive_format: ArchiveFormat,
    pub selected_binary: String,
    pub installed_path: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cache_key: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub cache_path: Option<String>,
    pub sha256: String,
    pub checksum_source: ChecksumSource,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub installed_at: Option<String>,
    pub signature_available: bool,
    pub signature_verified: bool,
}

impl PackageRecord {
    pub fn lock_record(&self) -> Self {
        let mut record = self.clone();
        record.cache_key = None;
        record.cache_path = None;
        record.installed_at = None;
        record
    }

    pub fn has_verified_source(&self) -> bool {
        self.checksum_source.is_upstream_verified()
    }
}

#[derive(Debug, Clone)]
pub struct ScopePaths {
    pub root: PathBuf,
    pub bin: PathBuf,
    pub packages: PathBuf,
    pub tmp: PathBuf,
}

impl ScopePaths {
    pub fn global(home: PathBuf) -> Self {
        Self {
            root: home.clone(),
            bin: home.join("bin"),
            packages: home.join("packages"),
            tmp: home.join("tmp"),
        }
    }

    pub fn local(project_root: PathBuf) -> Self {
        let root = project_root.join(".binpm");
        Self {
            root: root.clone(),
            bin: root.join("bin"),
            packages: root.join("packages"),
            tmp: root.join("tmp"),
        }
    }

    pub fn ensure(&self) -> Result<()> {
        ensure_dir(&self.bin)?;
        ensure_dir(&self.packages)?;
        ensure_dir(&self.tmp)?;
        Ok(())
    }
}

#[derive(Debug, Clone)]
pub struct CachePaths {
    pub root: PathBuf,
    pub tmp: PathBuf,
    pub refs: PathBuf,
}

impl CachePaths {
    pub fn new(home: &Path) -> Self {
        Self {
            root: home.join("cache"),
            tmp: home.join("tmp"),
            refs: home.join("cache").join("refs"),
        }
    }

    pub fn ensure(&self) -> Result<()> {
        ensure_dir(&self.root)?;
        ensure_dir(&self.tmp)?;
        ensure_dir(&self.refs)?;
        Ok(())
    }

    pub fn entry_dir(&self, sha256: &str) -> PathBuf {
        self.root.join("sha256").join(sha256)
    }

    pub fn asset_path(&self, sha256: &str) -> PathBuf {
        self.entry_dir(sha256).join("asset")
    }

    pub fn metadata_path(&self, sha256: &str) -> PathBuf {
        self.entry_dir(sha256).join("record.toml")
    }
}

#[derive(Debug, Clone)]
pub struct ResolvedAsset {
    pub source: SourceSpec,
    pub release_tag: String,
    pub target: HostTarget,
    pub decision: CandidateDecision,
    pub archive_format: ArchiveFormat,
    pub selected_binary: String,
    pub provider_digest_sha256: Option<String>,
    pub checksum_source: ChecksumSource,
    pub signature_available: bool,
    pub signature_verified: bool,
}

pub fn read_manifest(path: &Path) -> Result<Manifest> {
    let manifest: Manifest = read_toml(path)?;
    ensure_supported_version("manifest", path, manifest.version)?;
    Ok(manifest)
}

pub fn write_manifest(path: &Path, manifest: &Manifest) -> Result<()> {
    write_toml_atomic(path, manifest)
}

pub fn read_lockfile(path: &Path) -> Result<Lockfile> {
    if !path.exists() {
        return Ok(Lockfile {
            version: STORAGE_VERSION,
            tools: BTreeMap::new(),
        });
    }
    let lockfile: Lockfile = read_toml(path)?;
    ensure_supported_version("lockfile", path, lockfile.version)?;
    Ok(lockfile)
}

pub fn write_lockfile(path: &Path, lockfile: &Lockfile) -> Result<()> {
    write_toml_atomic(path, lockfile)
}

pub fn package_record_path(paths: &ScopePaths, cmd: &str) -> PathBuf {
    paths.packages.join(format!("{cmd}.toml"))
}

pub fn validate_command_name(cmd: &str) -> Result<()> {
    if cmd.is_empty()
        || cmd == "."
        || cmd == ".."
        || cmd.contains(['/', '\\'])
        || Path::new(cmd).components().any(|component| {
            !matches!(
                component,
                Component::Normal(name) if name == std::ffi::OsStr::new(cmd)
            )
        })
    {
        return Err(BinpmError::InvalidCommandName {
            cmd: cmd.to_string(),
        });
    }
    Ok(())
}

pub fn read_package_record(path: &Path) -> Result<PackageRecord> {
    read_toml(path)
}

pub fn write_package_record(paths: &ScopePaths, cmd: &str, record: &PackageRecord) -> Result<()> {
    validate_command_name(cmd)?;
    paths.ensure()?;
    write_toml_atomic(&package_record_path(paths, cmd), record)
}

pub fn remove_package_record(paths: &ScopePaths, cmd: &str) -> Result<()> {
    validate_command_name(cmd)?;
    remove_path_if_exists(&package_record_path(paths, cmd))
}

pub fn list_package_records(paths: &ScopePaths) -> Result<Vec<(String, PackageRecord)>> {
    let mut records = Vec::new();
    let entries = match fs::read_dir(&paths.packages) {
        Ok(entries) => entries,
        Err(source) if source.kind() == ErrorKind::NotFound => return Ok(records),
        Err(source) => {
            return Err(BinpmError::ReadFile {
                path: paths.packages.clone(),
                source,
            })
        }
    };

    for entry in entries {
        let entry = entry.map_err(|source| BinpmError::ReadFile {
            path: paths.packages.clone(),
            source,
        })?;
        let path = entry.path();
        if path.extension().and_then(|extension| extension.to_str()) != Some("toml") {
            continue;
        }
        let Some(cmd) = path.file_stem().and_then(|stem| stem.to_str()) else {
            continue;
        };
        records.push((cmd.to_string(), read_package_record(&path)?));
    }
    records.sort_by(|left, right| left.0.cmp(&right.0));
    Ok(records)
}

pub fn write_cache_record(paths: &CachePaths, record: &CacheRecord) -> Result<()> {
    let dir = paths.entry_dir(&record.sha256);
    ensure_dir(&dir)?;
    write_toml_atomic(&paths.metadata_path(&record.sha256), record)
}

pub fn read_cache_records(paths: &CachePaths) -> Result<Vec<CacheRecord>> {
    let mut records = Vec::new();
    for (_, record) in read_cache_record_entries(paths)? {
        records.push(record);
    }
    records.sort_by(|left: &CacheRecord, right| left.cache_key.cmp(&right.cache_key));
    Ok(records)
}

fn cache_entry_dirs(paths: &CachePaths) -> Result<Vec<PathBuf>> {
    let root = paths.root.join("sha256");
    let entries = match fs::read_dir(&root) {
        Ok(entries) => entries,
        Err(source) if source.kind() == ErrorKind::NotFound => return Ok(Vec::new()),
        Err(source) => return Err(BinpmError::ReadFile { path: root, source }),
    };

    let mut dirs = Vec::new();
    for entry in entries {
        let entry = entry.map_err(|source| BinpmError::ReadFile {
            path: root.clone(),
            source,
        })?;
        if entry
            .file_type()
            .map_err(|source| BinpmError::ReadFile {
                path: entry.path(),
                source,
            })?
            .is_dir()
        {
            dirs.push(entry.path());
        }
    }
    dirs.sort();
    Ok(dirs)
}

fn read_cache_record_entries(paths: &CachePaths) -> Result<Vec<(PathBuf, CacheRecord)>> {
    let mut records = Vec::new();
    for dir in cache_entry_dirs(paths)? {
        let path = dir.join("record.toml");
        if path.exists() {
            records.push((dir, read_toml::<CacheRecord>(&path)?));
        }
    }
    records.sort_by(|left, right| left.1.cache_key.cmp(&right.1.cache_key));
    Ok(records)
}

pub fn cache_key(sha256: &str) -> String {
    format!("sha256:{sha256}")
}

pub fn validate_download_url(raw: &str) -> Result<()> {
    let without_fragment = raw.split('#').next().unwrap_or(raw);
    let without_query = without_fragment
        .split('?')
        .next()
        .unwrap_or(without_fragment);
    let diagnostic_url = redact_url_credentials(without_query);

    if !without_query.starts_with("https://") {
        return Err(BinpmError::UnsafeUrl {
            url: diagnostic_url,
            message: "persisted release asset URLs must use https".to_string(),
        });
    }

    let rest = without_query.trim_start_matches("https://");
    let authority = rest.split('/').next().unwrap_or(rest);
    if authority.contains('@') {
        return Err(BinpmError::UnsafeUrl {
            url: diagnostic_url,
            message: "persisted release asset URLs must not include credentials".to_string(),
        });
    }

    Ok(())
}

pub fn sanitize_persisted_url(raw: &str) -> Result<String> {
    validate_download_url(raw)?;
    if raw.contains('?') || raw.contains('#') {
        return Err(BinpmError::UnsafeUrl {
            url: redact_url_credentials(raw.split(['?', '#']).next().unwrap_or(raw)),
            message: "persisted release asset URLs must not include query strings or fragments"
                .to_string(),
        });
    }

    Ok(raw.to_string())
}

pub fn validate_sha256_digest(value: &str) -> Result<()> {
    if value.len() == 64 && value.chars().all(|character| character.is_ascii_hexdigit()) {
        return Ok(());
    }
    Err(BinpmError::InvalidSha256 {
        value: value.to_string(),
    })
}

pub fn sha256_file(path: &Path) -> Result<String> {
    let bytes = fs::read(path).map_err(|source| BinpmError::ReadFile {
        path: path.to_path_buf(),
        source,
    })?;
    Ok(format!("{:x}", Sha256::digest(bytes)))
}

pub fn verify_sha256(path: &Path, expected: &str) -> Result<()> {
    validate_sha256_digest(expected)?;
    let actual = sha256_file(path)?;
    if actual == expected {
        return Ok(());
    }
    Err(BinpmError::DigestMismatch {
        path: path.to_path_buf(),
        expected: expected.to_string(),
        actual,
    })
}

pub fn atomic_write_bytes(path: &Path, bytes: &[u8]) -> Result<()> {
    if let Some(parent) = path.parent() {
        ensure_dir(parent)?;
    }

    let tmp = tmp_sibling(path);
    {
        let mut file = fs::File::create(&tmp).map_err(|source| BinpmError::WriteFile {
            path: tmp.clone(),
            source,
        })?;
        file.write_all(bytes)
            .map_err(|source| BinpmError::WriteFile {
                path: tmp.clone(),
                source,
            })?;
        file.sync_all().map_err(|source| BinpmError::WriteFile {
            path: tmp.clone(),
            source,
        })?;
    }
    replace_path(&tmp, path).map_err(|source| BinpmError::RenamePath {
        from: tmp,
        to: path.to_path_buf(),
        source,
    })
}

#[cfg(windows)]
fn replace_path(from: &Path, to: &Path) -> std::io::Result<()> {
    use std::os::windows::ffi::OsStrExt;

    const MOVEFILE_REPLACE_EXISTING: u32 = 0x1;
    const MOVEFILE_WRITE_THROUGH: u32 = 0x8;

    unsafe extern "system" {
        fn MoveFileExW(existing: *const u16, new: *const u16, flags: u32) -> i32;
    }

    let from_wide: Vec<u16> = from.as_os_str().encode_wide().chain(Some(0)).collect();
    let to_wide: Vec<u16> = to.as_os_str().encode_wide().chain(Some(0)).collect();
    let replaced = unsafe {
        MoveFileExW(
            from_wide.as_ptr(),
            to_wide.as_ptr(),
            MOVEFILE_REPLACE_EXISTING | MOVEFILE_WRITE_THROUGH,
        )
    };
    if replaced == 0 {
        Err(std::io::Error::last_os_error())
    } else {
        Ok(())
    }
}

#[cfg(not(windows))]
fn replace_path(from: &Path, to: &Path) -> std::io::Result<()> {
    fs::rename(from, to)
}

pub fn populate_cache_from_bytes(
    paths: &CachePaths,
    resolved: &ResolvedAsset,
    bytes: &[u8],
) -> Result<(String, PathBuf)> {
    paths.ensure()?;
    let sha256 = format!("{:x}", Sha256::digest(bytes));
    let asset_path = paths.asset_path(&sha256);
    let had_verified_asset = asset_path.exists() && verify_sha256(&asset_path, &sha256).is_ok();
    let record = CacheRecord {
        version: 1,
        cache_key: cache_key(&sha256),
        source_provider: resolved.source.provider,
        source_host: resolved.source.host.clone(),
        source_path: resolved.source.path.clone(),
        release_tag: resolved.release_tag.clone(),
        asset_name: resolved.decision.asset_name.clone(),
        asset_url: sanitize_persisted_url(&resolved.decision.canonical_url)?,
        byte_size: Some(bytes.len() as u64),
        sha256: sha256.clone(),
        checksum_source: resolved.checksum_source,
        created_at: now_timestamp(),
        last_used_at: Some(now_timestamp()),
    };

    if asset_path.exists() {
        if verify_sha256(&asset_path, &sha256).is_ok() {
            debug!(
                cache_key = cache_key(&sha256),
                cache_path = %asset_path.display(),
                cache_action = "reuse",
                cache_reused = true,
                "Reused verified cache entry"
            );
        } else {
            atomic_write_bytes(&asset_path, bytes)?;
            debug!(
                cache_key = cache_key(&sha256),
                cache_path = %asset_path.display(),
                cache_action = "repair",
                cache_reused = false,
                cache_bytes = bytes.len(),
                "Replaced corrupted cache entry"
            );
        }
    } else {
        let dir = paths.entry_dir(&sha256);
        ensure_dir(&dir)?;
        atomic_write_bytes(&asset_path, bytes)?;
        debug!(
            cache_key = cache_key(&sha256),
            cache_path = %asset_path.display(),
            cache_action = "populate",
            cache_reused = false,
            cache_bytes = bytes.len(),
            "Populated cache entry"
        );
    }

    if let Err(error) = write_cache_record(paths, &record) {
        if !had_verified_asset {
            let _ = remove_path_if_exists(&paths.entry_dir(&sha256));
        }
        return Err(error);
    }
    Ok((sha256, asset_path))
}

pub fn record_verified_cache_hit(paths: &CachePaths, resolved: &ResolvedAsset) -> Result<PathBuf> {
    let sha256 =
        resolved
            .provider_digest_sha256
            .as_deref()
            .ok_or_else(|| BinpmError::InvalidSha256 {
                value: String::new(),
            })?;
    validate_sha256_digest(sha256)?;
    let asset_path = paths.asset_path(sha256);
    verify_sha256(&asset_path, sha256)?;
    let byte_size = fs::metadata(&asset_path)
        .map_err(|source| BinpmError::ReadFile {
            path: asset_path.clone(),
            source,
        })?
        .len();
    let record = CacheRecord {
        version: 1,
        cache_key: cache_key(sha256),
        source_provider: resolved.source.provider,
        source_host: resolved.source.host.clone(),
        source_path: resolved.source.path.clone(),
        release_tag: resolved.release_tag.clone(),
        asset_name: resolved.decision.asset_name.clone(),
        asset_url: sanitize_persisted_url(&resolved.decision.canonical_url)?,
        byte_size: Some(byte_size),
        sha256: sha256.to_string(),
        checksum_source: resolved.checksum_source,
        created_at: now_timestamp(),
        last_used_at: Some(now_timestamp()),
    };
    write_cache_record(paths, &record)?;
    Ok(asset_path)
}

pub fn install_bare_executable(cache_asset: &Path, installed_path: &Path) -> Result<()> {
    let bytes = fs::read(cache_asset).map_err(|source| BinpmError::ReadFile {
        path: cache_asset.to_path_buf(),
        source,
    })?;
    atomic_write_bytes(installed_path, &bytes)?;
    make_executable(installed_path)?;
    Ok(())
}

pub fn managed_installed_path(paths: &ScopePaths, cmd: &str, target_os: TargetOs) -> PathBuf {
    paths.bin.join(installed_filename(cmd, target_os))
}

pub fn deterministic_installed_path(cmd: &str, target_os: TargetOs) -> String {
    format!(".binpm/bin/{}", installed_filename(cmd, target_os))
}

pub fn installed_filename(cmd: &str, target_os: TargetOs) -> String {
    if target_os == TargetOs::Windows && !cmd.to_ascii_lowercase().ends_with(".exe") {
        format!("{cmd}.exe")
    } else {
        cmd.to_string()
    }
}

pub fn remove_installed_binary(
    paths: &ScopePaths,
    cmd: &str,
    record: &PackageRecord,
) -> Result<()> {
    let expected = validate_installed_binary_path(paths, cmd, record)?;
    remove_path_if_exists(&expected)
}

pub fn validate_installed_binary_path(
    paths: &ScopePaths,
    cmd: &str,
    record: &PackageRecord,
) -> Result<PathBuf> {
    validate_command_name(cmd)?;
    let expected = managed_installed_path(paths, cmd, record.target_os);
    let recorded = PathBuf::from(&record.installed_path);
    if recorded != expected {
        return Err(BinpmError::UnsafeInstalledPath {
            path: recorded,
            expected,
        });
    }
    Ok(expected)
}

pub fn prune_cache(paths: &CachePaths, referenced_keys: &BTreeSet<String>) -> Result<usize> {
    let mut removed = 0;
    for (dir, record) in read_cache_record_entries(paths)? {
        if referenced_keys.contains(&record.cache_key) {
            continue;
        }
        remove_path_if_exists(&dir)?;
        removed += 1;
        info!(
            cache_key = record.cache_key,
            cache_path = %dir.display(),
            cache_action = "prune",
            cache_evicted = true,
            "Pruned unreferenced cache entry"
        );
    }
    Ok(removed)
}

pub fn clean_cache(paths: &CachePaths) -> Result<usize> {
    let count = cache_entry_dirs(paths)?.len();
    remove_path_if_exists(&paths.root)?;
    ensure_dir(&paths.root)?;
    Ok(count)
}

pub fn referenced_cache_keys(
    global: &ScopePaths,
    local: Option<&ScopePaths>,
    cache: &CachePaths,
) -> Result<BTreeSet<String>> {
    let mut keys = BTreeSet::new();
    for (_, record) in list_package_records(global)? {
        if let Some(key) = record.cache_key {
            keys.insert(key);
        }
    }
    if let Some(local) = local {
        for (_, record) in list_package_records(local)? {
            if let Some(key) = record.cache_key {
                keys.insert(key);
            }
        }
    }
    for key in read_cache_ref_keys(cache)? {
        keys.insert(key);
    }
    Ok(keys)
}

pub fn write_cache_ref(
    cache: &CachePaths,
    project_root: &Path,
    cmd: &str,
    record: &PackageRecord,
) -> Result<()> {
    let Some(key) = &record.cache_key else {
        return Ok(());
    };
    validate_command_name(cmd)?;
    cache.ensure()?;
    let ref_path = cache_ref_path(cache, project_root, cmd);
    atomic_write_bytes(&ref_path, key.as_bytes())
}

pub fn remove_cache_ref(cache: &CachePaths, project_root: &Path, cmd: &str) -> Result<()> {
    validate_command_name(cmd)?;
    remove_path_if_exists(&cache_ref_path(cache, project_root, cmd))
}

fn read_cache_ref_keys(cache: &CachePaths) -> Result<BTreeSet<String>> {
    let mut keys = BTreeSet::new();
    let entries = match fs::read_dir(&cache.refs) {
        Ok(entries) => entries,
        Err(source) if source.kind() == ErrorKind::NotFound => return Ok(keys),
        Err(source) => {
            return Err(BinpmError::ReadFile {
                path: cache.refs.clone(),
                source,
            })
        }
    };
    for entry in entries {
        let entry = entry.map_err(|source| BinpmError::ReadFile {
            path: cache.refs.clone(),
            source,
        })?;
        let path = entry.path();
        if path.extension().and_then(|extension| extension.to_str()) != Some("ref") {
            continue;
        }
        let key = fs::read_to_string(&path).map_err(|source| BinpmError::ReadFile {
            path: path.clone(),
            source,
        })?;
        if !key.trim().is_empty() {
            keys.insert(key.trim().to_string());
        }
    }
    Ok(keys)
}

fn cache_ref_path(cache: &CachePaths, project_root: &Path, cmd: &str) -> PathBuf {
    let digest = Sha256::digest(format!("{}:{cmd}", project_root.display()).as_bytes());
    cache.refs.join(format!("{digest:x}.ref"))
}

pub fn package_record_from_resolved(
    _cmd: &str,
    resolved: &ResolvedAsset,
    sha256: String,
    cache_asset: &Path,
    installed_path: &Path,
    include_runtime_fields: bool,
) -> Result<PackageRecord> {
    let cache_path = if include_runtime_fields {
        Some(cache_asset.display().to_string())
    } else {
        None
    };
    let installed_at = if include_runtime_fields {
        Some(now_timestamp())
    } else {
        None
    };
    let requested_version = resolved.source.version.clone();
    let source = resolved.source.source_without_version();
    let package_spec = if let Some(version) = &requested_version {
        format!("{source}@{version}")
    } else {
        format!("{}@{}", source, resolved.release_tag)
    };

    Ok(PackageRecord {
        package_spec,
        source,
        source_provider: resolved.source.provider,
        source_host: resolved.source.host.clone(),
        source_path: resolved.source.path.clone(),
        requested_version,
        release_tag: resolved.release_tag.clone(),
        asset_name: resolved.decision.asset_name.clone(),
        asset_url: sanitize_persisted_url(&resolved.decision.canonical_url)?,
        target_os: resolved.target.os,
        target_arch: resolved.target.arch,
        target_libc: resolved.target.libc,
        archive_format: resolved.archive_format,
        selected_binary: resolved.selected_binary.clone(),
        installed_path: installed_path.display().to_string(),
        cache_key: if include_runtime_fields {
            Some(cache_key(&sha256))
        } else {
            None
        },
        cache_path,
        sha256,
        checksum_source: resolved.checksum_source,
        installed_at,
        signature_available: resolved.signature_available,
        signature_verified: resolved.signature_verified,
    })
}

pub fn archive_format(kind: ArtifactKind) -> Option<ArchiveFormat> {
    match kind {
        ArtifactKind::Archive(format) => Some(format),
        ArtifactKind::BareExecutable => Some(ArchiveFormat::BareExecutable),
        _ => None,
    }
}

pub fn ensure_dir(path: &Path) -> Result<()> {
    fs::create_dir_all(path).map_err(|source| BinpmError::CreateDirectory {
        path: path.to_path_buf(),
        source,
    })
}

pub fn remove_path_if_exists(path: &Path) -> Result<()> {
    match fs::symlink_metadata(path) {
        Ok(metadata) if metadata.is_dir() => {
            fs::remove_dir_all(path).map_err(|source| BinpmError::RemovePath {
                path: path.to_path_buf(),
                source,
            })
        }
        Ok(_) => fs::remove_file(path).map_err(|source| BinpmError::RemovePath {
            path: path.to_path_buf(),
            source,
        }),
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => Ok(()),
        Err(source) => Err(BinpmError::ReadFile {
            path: path.to_path_buf(),
            source,
        }),
    }
}

fn read_toml<T>(path: &Path) -> Result<T>
where
    T: for<'de> Deserialize<'de>,
{
    let raw = fs::read_to_string(path).map_err(|source| BinpmError::ReadFile {
        path: path.to_path_buf(),
        source,
    })?;
    toml::from_str(&raw).map_err(|source| BinpmError::ParseToml {
        path: path.to_path_buf(),
        source,
    })
}

fn ensure_supported_version(kind: &'static str, path: &Path, version: u8) -> Result<()> {
    if version == STORAGE_VERSION {
        return Ok(());
    }
    Err(BinpmError::UnsupportedStorageVersion {
        kind,
        path: path.to_path_buf(),
        version,
    })
}

fn redact_url_credentials(url: &str) -> String {
    let Some((scheme, rest)) = url.split_once("://") else {
        return url.to_string();
    };
    let Some((authority, path)) = rest.split_once('/') else {
        return match rest.rsplit_once('@') {
            Some((_, host)) => format!("{scheme}://{host}"),
            None => url.to_string(),
        };
    };
    match authority.rsplit_once('@') {
        Some((_, host)) => format!("{scheme}://{host}/{path}"),
        None => url.to_string(),
    }
}

fn write_toml_atomic<T>(path: &Path, value: &T) -> Result<()>
where
    T: Serialize,
{
    let raw = toml::to_string_pretty(value).map_err(|source| BinpmError::SerializeToml {
        path: path.to_path_buf(),
        source,
    })?;
    atomic_write_bytes(path, raw.as_bytes())
}

fn tmp_sibling(path: &Path) -> PathBuf {
    let file_name = path
        .file_name()
        .and_then(|name| name.to_str())
        .unwrap_or("binpm-tmp");
    path.with_file_name(format!(
        ".{file_name}.{}.{}.tmp",
        std::process::id(),
        unique_nanos()
    ))
}

fn unique_nanos() -> u128 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_nanos())
        .unwrap_or_default()
}

fn now_timestamp() -> String {
    DateTime::<Utc>::from(SystemTime::now()).to_rfc3339()
}

#[cfg(unix)]
fn make_executable(path: &Path) -> Result<()> {
    use std::os::unix::fs::PermissionsExt;

    let mut permissions = fs::metadata(path)
        .map_err(|source| BinpmError::ReadFile {
            path: path.to_path_buf(),
            source,
        })?
        .permissions();
    permissions.set_mode(0o755);
    fs::set_permissions(path, permissions).map_err(|source| BinpmError::WriteFile {
        path: path.to_path_buf(),
        source,
    })
}

#[cfg(not(unix))]
fn make_executable(_path: &Path) -> Result<()> {
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::collections::{BTreeMap, BTreeSet};

    use sha2::{Digest, Sha256};

    use super::{
        clean_cache, install_bare_executable, list_package_records, managed_installed_path,
        populate_cache_from_bytes, prune_cache, read_cache_records, read_lockfile, read_manifest,
        record_verified_cache_hit, referenced_cache_keys, remove_installed_binary,
        sanitize_persisted_url, validate_command_name, validate_download_url,
        validate_sha256_digest, verify_sha256, write_cache_ref, write_lockfile, write_manifest,
        CachePaths, LockTool, Lockfile, Manifest, PackageRecord, ResolvedAsset, ScopePaths,
    };
    use crate::{
        assets::{ArtifactKind, CandidateDecision},
        contract::{
            ArchiveFormat, ChecksumSource, HostTarget, SourceProvider, SourceSpec, TargetArch,
            TargetLibc, TargetOs,
        },
    };

    #[test]
    fn persisted_urls_reject_query_and_fragment() {
        let error = sanitize_persisted_url(
            "https://github.com/owner/repo/releases/download/v1/tool?token=secret#frag",
        )
        .expect_err("query-bearing persisted url");

        assert!(error.to_string().contains("must not include query"));
        assert!(!error.to_string().contains("secret"));
    }

    #[test]
    fn runtime_download_urls_allow_query_and_fragment() {
        validate_download_url(
            "https://github.com/owner/repo/releases/download/v1/tool?token=secret#frag",
        )
        .expect("runtime download url");
    }

    #[test]
    fn rejects_credential_bearing_urls() {
        let error =
            sanitize_persisted_url("https://token@example.com/tool").expect_err("credential URL");

        assert!(error.to_string().contains("credentials"));
        assert!(!error.to_string().contains("token"));
    }

    #[test]
    fn unsafe_url_diagnostics_strip_query_and_fragment() {
        let error = sanitize_persisted_url("http://example.com/tool?token=secret#frag")
            .expect_err("unsafe URL");

        assert!(error.to_string().contains("http://example.com/tool"));
        assert!(!error.to_string().contains("secret"));
        assert!(!error.to_string().contains("frag"));
    }

    #[test]
    fn sha256_digests_must_be_fixed_length_hex() {
        validate_sha256_digest("abcdefabcdef0123456789abcdef0123456789abcdef0123456789abcdef0123")
            .expect("valid digest");

        let traversal = validate_sha256_digest("../outside").expect_err("path-like digest");
        assert!(traversal.to_string().contains("Invalid SHA-256"));
        let short = validate_sha256_digest("abc123").expect_err("short digest");
        assert!(short.to_string().contains("Invalid SHA-256"));
    }

    #[test]
    fn rejects_unsupported_manifest_and_lockfile_versions() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let manifest_path = temp_dir.path().join("binpm.toml");
        let lockfile_path = temp_dir.path().join("binpm.lock");
        std::fs::write(&manifest_path, "version = 2\n").expect("write manifest");
        std::fs::write(&lockfile_path, "version = 2\n").expect("write lockfile");

        let manifest_error = read_manifest(&manifest_path).expect_err("manifest version");
        let lockfile_error = read_lockfile(&lockfile_path).expect_err("lockfile version");

        assert!(manifest_error
            .to_string()
            .contains("Unsupported manifest version 2"));
        assert!(lockfile_error
            .to_string()
            .contains("Unsupported lockfile version 2"));
    }

    #[test]
    fn rejects_command_names_with_path_components() {
        for cmd in ["", ".", "..", "../tool", "nested/tool", r"nested\tool"] {
            assert!(validate_command_name(cmd).is_err(), "{cmd} should fail");
        }

        validate_command_name("tool.exe").expect("basename command");
    }

    #[test]
    fn missing_package_record_directory_is_empty_but_invalid_directory_errors() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let missing_paths = ScopePaths::local(temp_dir.path().join("missing"));
        assert!(list_package_records(&missing_paths)
            .expect("missing package dir")
            .is_empty());

        let invalid_root = temp_dir.path().join("invalid");
        std::fs::create_dir_all(&invalid_root).expect("create invalid root");
        std::fs::write(invalid_root.join("packages"), b"not a directory").expect("write file");
        let invalid_paths = ScopePaths {
            root: invalid_root.clone(),
            bin: invalid_root.join("bin"),
            packages: invalid_root.join("packages"),
            tmp: invalid_root.join("tmp"),
        };

        assert!(list_package_records(&invalid_paths).is_err());
    }

    #[test]
    fn writes_manifest_atomically_without_temp_leftover() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let path = temp_dir.path().join("binpm.toml");

        write_manifest(
            &path,
            &Manifest {
                version: 1,
                tools: Default::default(),
            },
        )
        .expect("write manifest");

        assert!(path.exists());
        let leftovers = std::fs::read_dir(temp_dir.path())
            .expect("read tempdir")
            .filter_map(|entry| entry.ok())
            .filter(|entry| entry.file_name().to_string_lossy().contains(".tmp"))
            .count();
        assert_eq!(leftovers, 0);
    }

    #[test]
    fn populates_and_reuses_cache_by_verified_sha256() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset();
        let bytes = b"#!/bin/sh\nexit 0\n";
        let expected = format!("{:x}", Sha256::digest(bytes));

        let (first_sha, first_path) =
            populate_cache_from_bytes(&cache, &resolved, bytes).expect("populate cache");
        let (second_sha, second_path) =
            populate_cache_from_bytes(&cache, &resolved, bytes).expect("reuse cache");

        assert_eq!(first_sha, expected);
        assert_eq!(second_sha, expected);
        assert_eq!(first_path, second_path);
        assert_eq!(read_cache_records(&cache).expect("records").len(), 1);
    }

    #[test]
    fn replaces_corrupted_cache_entry_with_verified_bytes() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset();
        let bytes = b"good bytes";
        let (sha, path) =
            populate_cache_from_bytes(&cache, &resolved, bytes).expect("populate cache");
        std::fs::write(&path, b"bad bytes").expect("corrupt cache");

        let (repaired_sha, repaired_path) =
            populate_cache_from_bytes(&cache, &resolved, bytes).expect("repair cache");

        assert_eq!(repaired_sha, sha);
        assert_eq!(repaired_path, path);
        assert_eq!(std::fs::read(&path).expect("read repaired"), bytes);
        verify_sha256(&path, &sha).expect("repaired digest");
    }

    #[test]
    fn provider_digest_cache_hit_reuses_verified_asset_without_downloading_bytes() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut resolved = resolved_asset();
        let bytes = b"digest bytes";
        let expected = format!("{:x}", Sha256::digest(bytes));
        resolved.provider_digest_sha256 = Some(expected.clone());
        resolved.checksum_source = ChecksumSource::GitHubDigest;
        let asset_path = cache.asset_path(&expected);
        std::fs::create_dir_all(asset_path.parent().expect("asset parent"))
            .expect("create cache entry");
        std::fs::write(&asset_path, bytes).expect("write cached asset");

        let reused = record_verified_cache_hit(&cache, &resolved).expect("cache hit");

        assert_eq!(reused, asset_path);
        let records = read_cache_records(&cache).expect("cache records");
        assert_eq!(records[0].sha256, expected);
        assert_eq!(records[0].checksum_source, ChecksumSource::GitHubDigest);
    }

    #[test]
    fn package_records_do_not_trust_persisted_signature_flags() {
        let mut record = package_record();
        record.checksum_source = ChecksumSource::Signature;
        record.signature_verified = true;

        assert!(!record.has_verified_source());

        record.checksum_source = ChecksumSource::GitHubDigest;

        assert!(record.has_verified_source());
    }

    #[test]
    fn cache_population_does_not_publish_asset_when_metadata_is_invalid() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let mut resolved = resolved_asset();
        resolved.decision.canonical_url =
            "https://github.com/owner/tool/releases/download/1.0.0/tool?token=secret".to_string();
        let bytes = b"not cached";
        let sha = format!("{:x}", Sha256::digest(bytes));

        let error = populate_cache_from_bytes(&cache, &resolved, bytes)
            .expect_err("metadata URL should be rejected");

        assert!(error.to_string().contains("must not include query"));
        assert!(!cache.entry_dir(&sha).exists());
    }

    #[test]
    fn detects_cache_digest_mismatch() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset();
        let (sha, path) =
            populate_cache_from_bytes(&cache, &resolved, b"good").expect("populate cache");
        std::fs::write(&path, b"bad").expect("corrupt cache");

        let error = verify_sha256(&path, &sha).expect_err("digest mismatch");

        assert!(error.to_string().contains("SHA-256 mismatch"));
    }

    #[test]
    fn prune_removes_only_unreferenced_cache_entries() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset();
        let (kept_sha, _) =
            populate_cache_from_bytes(&cache, &resolved, b"keep").expect("populate kept");
        let (removed_sha, _) =
            populate_cache_from_bytes(&cache, &resolved, b"remove").expect("populate removed");
        let mut referenced = BTreeSet::new();
        referenced.insert(format!("sha256:{kept_sha}"));

        let removed = prune_cache(&cache, &referenced).expect("prune cache");

        assert_eq!(removed, 1);
        assert!(cache.asset_path(&kept_sha).exists());
        assert!(!cache.asset_path(&removed_sha).exists());
    }

    #[test]
    fn prune_uses_enumerated_cache_directory_not_record_sha_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let entry = cache.root.join("sha256").join("entry");
        let outside = temp_dir.path().join("outside");
        std::fs::create_dir_all(&entry).expect("create entry");
        std::fs::create_dir_all(&outside).expect("create outside");
        std::fs::write(outside.join("keep"), b"keep").expect("write outside marker");
        std::fs::write(
            entry.join("record.toml"),
            r#"
version = 1
cache_key = "sha256:entry"
source_provider = "github"
source_host = "github.com"
source_path = "owner/tool"
release_tag = "1.0.0"
asset_name = "tool"
asset_url = "https://example.com/tool"
sha256 = "../../outside"
checksum_source = "local"
created_at = "2026-01-01T00:00:00Z"
"#,
        )
        .expect("write record");

        let removed = prune_cache(&cache, &BTreeSet::new()).expect("prune");

        assert_eq!(removed, 1);
        assert!(!entry.exists());
        assert!(outside.join("keep").exists());
    }

    #[test]
    fn referenced_cache_keys_include_cross_project_refs() {
        let home = tempfile::tempdir().expect("home");
        let project = tempfile::tempdir().expect("project");
        let cache = CachePaths::new(home.path());
        let paths = ScopePaths::global(home.path().join("global"));
        let mut record = package_record();
        record.cache_key = Some("sha256:cross-project".to_string());

        write_cache_ref(&cache, project.path(), "tool", &record).expect("write ref");
        let referenced = referenced_cache_keys(&paths, None, &cache).expect("referenced keys");

        assert!(referenced.contains("sha256:cross-project"));
    }

    #[test]
    fn unreadable_cache_ref_directory_errors() {
        let home = tempfile::tempdir().expect("home");
        let cache = CachePaths::new(home.path());
        std::fs::create_dir_all(&cache.root).expect("create cache root");
        std::fs::write(&cache.refs, b"not a directory").expect("write refs file");
        let paths = ScopePaths::global(home.path().join("global"));

        let error = referenced_cache_keys(&paths, None, &cache).expect_err("cache refs error");

        assert!(error.to_string().contains("Failed to read"));
    }

    #[test]
    fn clean_cache_preserves_non_cache_directories() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let resolved = resolved_asset();
        populate_cache_from_bytes(&cache, &resolved, b"bytes").expect("populate cache");
        let bin = temp_dir.path().join("bin");
        std::fs::create_dir_all(&bin).expect("create bin");
        std::fs::write(bin.join("tool"), b"installed").expect("write bin");

        let removed = clean_cache(&cache).expect("clean cache");

        assert_eq!(removed, 1);
        assert!(bin.join("tool").exists());
    }

    #[test]
    fn clean_cache_removes_malformed_cache_records() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let cache = CachePaths::new(temp_dir.path());
        let entry = cache.root.join("sha256").join("bad");
        std::fs::create_dir_all(&entry).expect("create bad entry");
        std::fs::write(entry.join("record.toml"), "not = [valid").expect("write malformed");

        let removed = clean_cache(&cache).expect("clean cache");

        assert_eq!(removed, 1);
        assert!(!entry.exists());
        assert!(cache.root.exists());
    }

    #[test]
    fn lockfile_serializes_deterministic_target_records_without_runtime_cache_paths() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let path = temp_dir.path().join("binpm.lock");
        let mut lockfile = Lockfile {
            version: 1,
            tools: BTreeMap::new(),
        };
        lockfile.tools.insert(
            "tool".to_string(),
            LockTool {
                source: "github:owner/tool".to_string(),
                targets: BTreeMap::from([("linux-x86_64-gnu".to_string(), package_record())]),
            },
        );

        write_lockfile(&path, &lockfile).expect("write lockfile");
        let raw = std::fs::read_to_string(&path).expect("read lockfile");
        let parsed = read_lockfile(&path).expect("parse lockfile");

        assert!(raw.contains("[tools.tool.targets.linux-x86_64-gnu]"));
        assert!(!raw.contains("cache_path"));
        assert!(!raw.contains("installed_at"));
        assert_eq!(
            parsed.tools["tool"].targets["linux-x86_64-gnu"].sha256,
            "abc123"
        );
    }

    #[test]
    fn bare_executable_install_is_atomic_and_executable() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let source = temp_dir.path().join("asset");
        let installed = temp_dir.path().join("bin").join("tool");
        std::fs::write(&source, b"#!/bin/sh\n").expect("write source");

        install_bare_executable(&source, &installed).expect("install executable");

        assert_eq!(
            std::fs::read_to_string(installed).expect("read installed"),
            "#!/bin/sh\n"
        );
    }

    #[test]
    fn remove_installed_binary_rejects_paths_outside_managed_bin() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        let outside = temp_dir.path().join("outside");
        std::fs::write(&outside, b"do not remove").expect("write outside");
        let mut record = package_record();
        record.installed_path = outside.display().to_string();

        let error =
            remove_installed_binary(&paths, "tool", &record).expect_err("unsafe installed path");

        assert!(error.to_string().contains("Unsafe installed path"));
        assert!(outside.exists());
    }

    #[test]
    fn remove_installed_binary_removes_only_expected_managed_path() {
        let temp_dir = tempfile::tempdir().expect("tempdir");
        let paths = ScopePaths::local(temp_dir.path().to_path_buf());
        let installed = managed_installed_path(&paths, "tool", TargetOs::Linux);
        std::fs::create_dir_all(installed.parent().expect("parent")).expect("create bin");
        std::fs::write(&installed, b"remove").expect("write installed");
        let mut record = package_record();
        record.installed_path = installed.display().to_string();

        remove_installed_binary(&paths, "tool", &record).expect("remove installed");

        assert!(!installed.exists());
    }

    fn resolved_asset() -> ResolvedAsset {
        ResolvedAsset {
            source: SourceSpec {
                provider: SourceProvider::GitHub,
                host: "github.com".to_string(),
                path: "owner/tool".to_string(),
                version: Some("1.0.0".to_string()),
            },
            release_tag: "1.0.0".to_string(),
            target: HostTarget {
                os: TargetOs::Linux,
                arch: TargetArch::X86_64,
                libc: TargetLibc::Gnu,
            },
            decision: CandidateDecision {
                asset_name: "tool-linux-x64".to_string(),
                canonical_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64".to_string(),
                download_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64?token=secret".to_string(),
                kind: ArtifactKind::BareExecutable,
                detected_os: Some(TargetOs::Linux),
                detected_arch: Some(TargetArch::X86_64),
                detected_libc: Some(TargetLibc::Gnu),
                score: Some(235),
                eligible: true,
                recognized_pattern: true,
                rejection_reason: None,
            },
            archive_format: ArchiveFormat::BareExecutable,
            selected_binary: "tool-linux-x64".to_string(),
            provider_digest_sha256: None,
            checksum_source: ChecksumSource::Local,
            signature_available: false,
            signature_verified: false,
        }
    }

    fn package_record() -> PackageRecord {
        PackageRecord {
            package_spec: "github:owner/tool@1.0.0".to_string(),
            source: "github:owner/tool".to_string(),
            source_provider: SourceProvider::GitHub,
            source_host: "github.com".to_string(),
            source_path: "owner/tool".to_string(),
            requested_version: Some("1.0.0".to_string()),
            release_tag: "1.0.0".to_string(),
            asset_name: "tool-linux-x64".to_string(),
            asset_url: "https://github.com/owner/tool/releases/download/1.0.0/tool-linux-x64"
                .to_string(),
            target_os: TargetOs::Linux,
            target_arch: TargetArch::X86_64,
            target_libc: TargetLibc::Gnu,
            archive_format: ArchiveFormat::BareExecutable,
            selected_binary: "tool-linux-x64".to_string(),
            installed_path: ".binpm/bin/tool".to_string(),
            cache_key: None,
            cache_path: None,
            sha256: "abc123".to_string(),
            checksum_source: ChecksumSource::Local,
            installed_at: None,
            signature_available: false,
            signature_verified: false,
        }
    }
}
