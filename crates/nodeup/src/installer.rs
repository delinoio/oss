use std::{
    collections::HashMap,
    fs::{self, File, OpenOptions},
    io::{BufRead, BufReader, Read, Write},
    path::{Path, PathBuf},
};

use sha2::{Digest, Sha256};
use tracing::info;

use crate::{
    errors::{NodeupError, Result},
    paths::NodeupPaths,
    release_index::{normalize_version, ReleaseIndexClient},
    store::Store,
    types::PlatformTarget,
};

#[derive(Debug, Clone)]
pub struct RuntimeInstaller {
    paths: NodeupPaths,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum InstallState {
    Installed,
    AlreadyInstalled,
}

#[derive(Debug, Clone)]
pub struct InstallReport {
    pub version: String,
    pub archive_path: PathBuf,
    pub state: InstallState,
}

impl RuntimeInstaller {
    pub fn new(paths: NodeupPaths) -> Self {
        Self { paths }
    }

    pub fn ensure_installed(
        &self,
        version: &str,
        release_client: &ReleaseIndexClient,
    ) -> Result<InstallReport> {
        let canonical_version = normalize_version(version);
        let store = Store::new(self.paths.clone());
        if store.is_installed(&canonical_version) {
            return Ok(InstallReport {
                version: canonical_version,
                archive_path: PathBuf::new(),
                state: InstallState::AlreadyInstalled,
            });
        }

        release_client.ensure_version_available(&canonical_version)?;

        let target = PlatformTarget::from_host().ok_or_else(|| {
            NodeupError::unsupported_platform(format!(
                "nodeup currently supports macOS/Linux x64/arm64 only. host={}/{}",
                std::env::consts::OS,
                std::env::consts::ARCH
            ))
        })?;

        let _lock = InstallLock::acquire(&self.paths.toolchains_dir, &canonical_version)?;

        if store.is_installed(&canonical_version) {
            return Ok(InstallReport {
                version: canonical_version,
                archive_path: PathBuf::new(),
                state: InstallState::AlreadyInstalled,
            });
        }

        let archive_url = release_client.archive_url(&canonical_version, target.archive_segment());
        let archive_filename = archive_url
            .rsplit('/')
            .next()
            .ok_or_else(|| NodeupError::internal("Failed to parse archive file name"))?;
        let archive_path = self.paths.downloads_dir.join(archive_filename);

        info!(
            command_path = "nodeup.installer.download",
            runtime = %canonical_version,
            url = %archive_url,
            download_path = %archive_path.display(),
            "Downloading runtime archive"
        );

        download_file(release_client, &archive_url, &archive_path)?;

        let shasums_url = release_client.shasums_url(&canonical_version);
        let shasums_content = release_client
            .http()
            .get(&shasums_url)
            .send()?
            .error_for_status()
            .map_err(|error| {
                NodeupError::network(format!("Failed to fetch SHASUMS256.txt: {error}"))
            })?
            .text()
            .map_err(|error| {
                NodeupError::network(format!("Failed to read SHASUMS256.txt body: {error}"))
            })?;

        let checksum_table = parse_shasums(&shasums_content)?;
        let expected_checksum = checksum_table.get(archive_filename).ok_or_else(|| {
            NodeupError::not_found(format!(
                "Checksum for {} not found in SHASUMS256.txt",
                archive_filename
            ))
        })?;

        let observed_checksum = sha256_file(&archive_path)?;

        info!(
            command_path = "nodeup.installer.verify",
            runtime = %canonical_version,
            archive = %archive_filename,
            checksum_algorithm = "sha256",
            expected = %expected_checksum,
            observed = %observed_checksum,
            validation_result = %(*expected_checksum == observed_checksum),
            "Validating archive checksum"
        );

        if *expected_checksum != observed_checksum {
            return Err(NodeupError::conflict(format!(
                "Checksum mismatch for {}. expected={}, observed={}",
                archive_filename, expected_checksum, observed_checksum
            )));
        }

        let runtime_dir = self.paths.runtime_dir(&canonical_version);
        extract_archive_to_runtime(&archive_path, &runtime_dir)?;

        Ok(InstallReport {
            version: canonical_version,
            archive_path,
            state: InstallState::Installed,
        })
    }
}

fn download_file(release_client: &ReleaseIndexClient, url: &str, destination: &Path) -> Result<()> {
    let mut response = release_client
        .http()
        .get(url)
        .send()?
        .error_for_status()
        .map_err(|error| {
            NodeupError::network(format!("Download request failed for {url}: {error}"))
        })?;

    let mut output = File::create(destination)?;
    response.copy_to(&mut output).map_err(|error| {
        NodeupError::network(format!("Failed to write downloaded bytes: {error}"))
    })?;
    output.flush()?;
    Ok(())
}

fn extract_archive_to_runtime(archive_path: &Path, runtime_dir: &Path) -> Result<()> {
    if runtime_dir.exists() {
        return Ok(());
    }

    let parent = runtime_dir.parent().ok_or_else(|| {
        NodeupError::internal(format!(
            "Cannot get runtime parent for {}",
            runtime_dir.display()
        ))
    })?;

    let temp_dir = tempfile::Builder::new()
        .prefix("nodeup-extract-")
        .tempdir_in(parent)?;

    let archive_file = File::open(archive_path)?;
    let decoder = xz2::read::XzDecoder::new(archive_file);
    let mut archive = tar::Archive::new(decoder);
    archive.unpack(temp_dir.path())?;

    let extracted_root = fs::read_dir(temp_dir.path())?
        .next()
        .ok_or_else(|| NodeupError::internal("Archive unpack produced empty directory"))??
        .path();

    fs::rename(extracted_root, runtime_dir)?;
    Ok(())
}

pub fn parse_shasums(content: &str) -> Result<HashMap<String, String>> {
    let reader = BufReader::new(content.as_bytes());
    let mut checksums = HashMap::new();

    for line in reader.lines() {
        let line = line?;
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        let mut parts = trimmed.split_whitespace();
        let checksum = parts
            .next()
            .ok_or_else(|| NodeupError::invalid_input("Malformed SHASUMS256.txt line"))?;
        let filename = parts
            .next()
            .ok_or_else(|| NodeupError::invalid_input("Malformed SHASUMS256.txt line"))?
            .trim_start_matches('*');

        checksums.insert(filename.to_string(), checksum.to_string());
    }

    Ok(checksums)
}

pub fn sha256_file(path: &Path) -> Result<String> {
    let mut file = File::open(path)?;
    let mut hasher = Sha256::new();
    let mut buffer = [0u8; 8 * 1024];

    loop {
        let read = file.read(&mut buffer)?;
        if read == 0 {
            break;
        }
        hasher.update(&buffer[..read]);
    }

    let hash = hasher.finalize();
    Ok(format!("{hash:x}"))
}

struct InstallLock {
    path: PathBuf,
    _file: File,
}

impl InstallLock {
    fn acquire(toolchains_dir: &Path, version: &str) -> Result<Self> {
        let lock_name = format!(".{version}.install.lock");
        let path = toolchains_dir.join(lock_name);
        match OpenOptions::new().write(true).create_new(true).open(&path) {
            Ok(file) => Ok(Self { path, _file: file }),
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {
                Err(NodeupError::conflict(format!(
                    "Another install is already running for runtime {version}"
                )))
            }
            Err(error) => Err(NodeupError::internal(format!(
                "Failed to create install lock {}: {error}",
                path.display()
            ))),
        }
    }
}

impl Drop for InstallLock {
    fn drop(&mut self) {
        let _ = fs::remove_file(&self.path);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn parses_shasums_table() {
        let parsed = parse_shasums(
            "abc123  node-v1.2.3-linux-x64.tar.xz\ndef456  node-v1.2.3-linux-arm64.tar.xz\n",
        )
        .unwrap();

        assert_eq!(
            parsed.get("node-v1.2.3-linux-x64.tar.xz").unwrap(),
            "abc123"
        );
    }

    #[test]
    fn computes_sha256_checksum() {
        let dir = tempfile::tempdir().unwrap();
        let path = dir.path().join("sample.bin");
        fs::write(&path, b"hello").unwrap();
        assert_eq!(
            sha256_file(&path).unwrap(),
            "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"
        );
    }
}
