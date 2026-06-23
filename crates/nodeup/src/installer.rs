use std::{
    collections::HashMap,
    fs::{self, File, OpenOptions},
    io::{BufRead, BufReader, Read, Write},
    path::{Path, PathBuf},
};

use sha2::{Digest, Sha256};
use tracing::info;
use zip::ZipArchive;

use crate::{
    errors::{sanitize_url_text, ErrorDiagnostics, NodeupError, Result},
    paths::NodeupPaths,
    release_index::{normalize_version, ReleaseIndexClient},
    store::Store,
    types::{ArchiveFormat, PlatformTarget},
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

        let target = PlatformTarget::ensure_supported_host("runtime installation")?;

        let _lock = InstallLock::acquire(&self.paths.toolchains_dir, &canonical_version)?;

        if store.is_installed(&canonical_version) {
            return Ok(InstallReport {
                version: canonical_version,
                archive_path: PathBuf::new(),
                state: InstallState::AlreadyInstalled,
            });
        }

        let archive_url = release_client.archive_url(&canonical_version, target);
        let archive_url_sanitized = sanitize_url_text(&archive_url);
        let archive_filename = archive_url.rsplit('/').next().ok_or_else(|| {
            NodeupError::internal_with_hint(
                format!(
                    "Failed to parse archive file name from download URL \
                     (runtime={canonical_version}, url={archive_url_sanitized})"
                ),
                "Retry the install command. If it persists, run with `RUST_LOG=nodeup=debug` and \
                 check the computed archive URL.",
            )
        })?;
        let archive_path = self.paths.downloads_dir.join(archive_filename);

        info!(
            command_path = "nodeup.installer.download",
            runtime = %canonical_version,
            url = %archive_url_sanitized,
            download_path = %archive_path.display(),
            "Downloading runtime archive"
        );

        download_file(
            release_client,
            &archive_url,
            &archive_path,
            &canonical_version,
            archive_filename,
        )?;

        let shasums_url = release_client.shasums_url(&canonical_version);
        let shasums_url_sanitized = sanitize_url_text(&shasums_url);
        let shasums_content = release_client
            .http()
            .get(&shasums_url)
            .send()?
            .error_for_status()
            .map_err(|error| {
                let status = error
                    .status()
                    .map(|status| status.as_u16().to_string())
                    .unwrap_or_else(|| "none".to_string());
                network_error_with_optional_mirror_diagnostics(
                    release_client,
                    format!(
                        "Failed to fetch SHASUMS256.txt (runtime={canonical_version}, \
                         url={shasums_url_sanitized}, status={status}, {})",
                        reqwest_error_context(&error)
                    ),
                    "Check network connectivity and retry the install command.",
                    &canonical_version,
                    archive_filename,
                )
            })?
            .text()
            .map_err(|error| {
                network_error_with_optional_mirror_diagnostics(
                    release_client,
                    format!(
                        "Failed to read SHASUMS256.txt body (runtime={canonical_version}, \
                         url={shasums_url_sanitized}, {})",
                        reqwest_error_context(&error)
                    ),
                    "Retry the command. If it keeps failing, run with `RUST_LOG=nodeup=debug` and \
                     inspect HTTP response details.",
                    &canonical_version,
                    archive_filename,
                )
            })?;

        let checksum_table = parse_shasums_for_archive(
            &shasums_content,
            &shasums_url_sanitized,
            &canonical_version,
            archive_filename,
        )?;
        let expected_checksum = checksum_table.get(archive_filename).ok_or_else(|| {
            NodeupError::not_found_with_hint(
                format!(
                    "Checksum entry not found in SHASUMS256.txt (runtime={canonical_version}, \
                     archive={archive_filename}, source={shasums_url_sanitized})"
                ),
                "Retry later in case upstream metadata is still propagating, or verify the \
                 release exists for this platform.",
            )
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
            let diagnostics =
                mirror_error_diagnostics(release_client, &canonical_version, archive_filename);
            let mirror_context = mirror_context_for_message(release_client);

            return Err(NodeupError::conflict_with_diagnostics(
                format!(
                    "Checksum mismatch for {archive_filename}. expected={expected_checksum}, \
                     observed={observed_checksum}{mirror_context}"
                ),
                checksum_mismatch_hint(release_client),
                diagnostics,
            ));
        }

        let runtime_dir = self.paths.runtime_dir(&canonical_version);
        extract_archive_to_runtime(&archive_path, &runtime_dir, target)?;

        Ok(InstallReport {
            version: canonical_version,
            archive_path,
            state: InstallState::Installed,
        })
    }
}

fn checksum_mismatch_hint(release_client: &ReleaseIndexClient) -> &'static str {
    if release_client.mirror_override_present() {
        return "Delete the downloaded archive from the nodeup downloads directory and retry the \
                install. Because a mirror override is configured, verify NODEUP_INDEX_URL and \
                NODEUP_DOWNLOAD_BASE_URL point to matching Node.js release data.";
    }

    "Delete the downloaded archive from the nodeup downloads directory and retry the install."
}

fn download_file(
    release_client: &ReleaseIndexClient,
    url: &str,
    destination: &Path,
    runtime: &str,
    archive: &str,
) -> Result<()> {
    let sanitized_url = sanitize_url_text(url);
    let mut response = release_client
        .http()
        .get(url)
        .send()?
        .error_for_status()
        .map_err(|error| {
            let status = error
                .status()
                .map(|status| status.as_u16().to_string())
                .unwrap_or_else(|| "none".to_string());
            network_error_with_optional_mirror_diagnostics(
                release_client,
                format!(
                    "Download request failed (url={sanitized_url}, status={status}, {})",
                    reqwest_error_context(&error)
                ),
                "Check network connectivity and retry the command.",
                runtime,
                archive,
            )
        })?;

    let mut output = File::create(destination)?;
    response.copy_to(&mut output).map_err(|error| {
        network_error_with_optional_mirror_diagnostics(
            release_client,
            format!(
                "Failed to stream downloaded bytes (url={sanitized_url}, destination={}, {})",
                destination.display(),
                reqwest_error_context(&error)
            ),
            "Ensure the downloads directory is writable, then retry.",
            runtime,
            archive,
        )
    })?;
    output.flush()?;
    Ok(())
}

fn reqwest_error_context(error: &reqwest::Error) -> String {
    let classification = if error.is_timeout() {
        "timeout"
    } else if error.is_connect() {
        "connect"
    } else if error.is_status() {
        "http-status"
    } else if error.is_decode() {
        "decode"
    } else if error.is_request() {
        "request"
    } else if error.is_body() {
        "body"
    } else {
        "other"
    };
    let status = error
        .status()
        .map(|status| status.as_u16().to_string())
        .unwrap_or_else(|| "none".to_string());
    let url = error
        .url()
        .map(|url| sanitize_url_text(url.as_str()))
        .unwrap_or_else(|| "none".to_string());

    format!("classification={classification}, error_status={status}, error_url={url}")
}

fn network_error_with_optional_mirror_diagnostics(
    release_client: &ReleaseIndexClient,
    cause: String,
    hint: &'static str,
    runtime: &str,
    archive: &str,
) -> NodeupError {
    if release_client.mirror_override_present() {
        return NodeupError::network_with_diagnostics(
            format!("{cause}{}", mirror_context_for_message(release_client)),
            mirror_consistency_hint(hint),
            mirror_error_diagnostics(release_client, runtime, archive),
        );
    }

    NodeupError::network_with_hint(cause, hint)
}

fn mirror_consistency_hint(base_hint: &'static str) -> String {
    format!(
        "{base_hint} If a mirror override is configured, verify NODEUP_INDEX_URL and \
         NODEUP_DOWNLOAD_BASE_URL point to matching Node.js release data."
    )
}

fn mirror_error_diagnostics(
    release_client: &ReleaseIndexClient,
    runtime: &str,
    archive: &str,
) -> ErrorDiagnostics {
    let mirror_diagnostic = release_client.mirror_diagnostic();
    let mut diagnostics = ErrorDiagnostics::new();
    diagnostics.insert(
        "archive".to_string(),
        serde_json::Value::String(archive.to_string()),
    );
    diagnostics.insert(
        "runtime".to_string(),
        serde_json::Value::String(runtime.to_string()),
    );
    diagnostics.insert(
        "index_url".to_string(),
        serde_json::Value::String(mirror_diagnostic.index_url.clone()),
    );
    diagnostics.insert(
        "index_url_source".to_string(),
        serde_json::Value::String(mirror_diagnostic.index_url_source.to_string()),
    );
    diagnostics.insert(
        "download_base_url".to_string(),
        serde_json::Value::String(mirror_diagnostic.download_base_url.clone()),
    );
    diagnostics.insert(
        "download_base_url_source".to_string(),
        serde_json::Value::String(mirror_diagnostic.download_base_url_source.to_string()),
    );
    diagnostics.insert(
        "mirror_override_present".to_string(),
        serde_json::Value::Bool(mirror_diagnostic.mirror_override_present),
    );
    diagnostics.insert(
        "mirror_mismatch_indicators".to_string(),
        serde_json::Value::Array(
            mirror_diagnostic
                .mismatch_indicators
                .iter()
                .map(|indicator| serde_json::Value::String((*indicator).to_string()))
                .collect(),
        ),
    );
    diagnostics
}

fn mirror_context_for_message(release_client: &ReleaseIndexClient) -> String {
    let diagnostic = release_client.mirror_diagnostic();
    if !diagnostic.mirror_override_present {
        return String::new();
    }

    format!(
        ", index_url={}, index_url_source={}, download_base_url={}, download_base_url_source={}, \
         mirror_mismatch_indicators={}",
        diagnostic.index_url,
        diagnostic.index_url_source,
        diagnostic.download_base_url,
        diagnostic.download_base_url_source,
        diagnostic.mismatch_indicators.join("|")
    )
}

fn extract_archive_to_runtime(
    archive_path: &Path,
    runtime_dir: &Path,
    target: PlatformTarget,
) -> Result<()> {
    if runtime_dir.exists() {
        return Ok(());
    }

    let parent = runtime_dir.parent().ok_or_else(|| {
        NodeupError::internal_with_hint(
            format!(
                "Cannot determine runtime parent directory for {}",
                runtime_dir.display()
            ),
            "Check the nodeup data directory layout and retry. If needed, run with \
             `RUST_LOG=nodeup=debug`.",
        )
    })?;

    let temp_dir = tempfile::Builder::new()
        .prefix("nodeup-extract-")
        .tempdir_in(parent)?;

    match target.archive_format() {
        ArchiveFormat::TarXz => unpack_tar_xz_archive(archive_path, temp_dir.path())?,
        ArchiveFormat::Zip => unpack_zip_archive(archive_path, temp_dir.path())?,
    }

    let extracted_root = normalize_runtime_root(temp_dir.path(), target)?;

    fs::rename(extracted_root, runtime_dir)?;
    Ok(())
}

fn unpack_tar_xz_archive(archive_path: &Path, destination: &Path) -> Result<()> {
    let archive_file = File::open(archive_path)?;
    let decoder = xz2::read::XzDecoder::new(archive_file);
    let mut archive = tar::Archive::new(decoder);
    archive.unpack(destination)?;
    Ok(())
}

fn unpack_zip_archive(archive_path: &Path, destination: &Path) -> Result<()> {
    let archive_file = File::open(archive_path)?;
    let mut archive = ZipArchive::new(archive_file).map_err(|error| {
        NodeupError::internal_with_hint(
            format!(
                "Failed to open zip archive {}: {error}",
                archive_path.display()
            ),
            "Retry the install command. If it repeats, remove the archive and re-download.",
        )
    })?;

    for index in 0..archive.len() {
        let mut entry = archive.by_index(index).map_err(|error| {
            NodeupError::internal_with_hint(
                format!(
                    "Failed to read zip entry {index} from {}: {error}",
                    archive_path.display()
                ),
                "Retry the install command. If it repeats, remove the archive and re-download.",
            )
        })?;

        let entry_path = entry.enclosed_name().ok_or_else(|| {
            NodeupError::invalid_input_with_hint(
                format!(
                    "Zip archive contains an unsafe path (archive={}, entry={})",
                    archive_path.display(),
                    entry.name()
                ),
                "Retry later in case the upstream archive is incomplete, or inspect the archive \
                 contents.",
            )
        })?;
        let destination_path = destination.join(entry_path);

        if entry.is_dir() {
            fs::create_dir_all(&destination_path)?;
            continue;
        }

        if let Some(parent) = destination_path.parent() {
            fs::create_dir_all(parent)?;
        }

        let mut output = File::create(&destination_path)?;
        std::io::copy(&mut entry, &mut output)?;
        output.flush()?;

        #[cfg(unix)]
        if let Some(mode) = entry.unix_mode() {
            use std::os::unix::fs::PermissionsExt;

            fs::set_permissions(&destination_path, fs::Permissions::from_mode(mode))?;
        }
    }

    Ok(())
}

fn normalize_runtime_root(extraction_dir: &Path, target: PlatformTarget) -> Result<PathBuf> {
    let mut entries =
        fs::read_dir(extraction_dir)?.collect::<std::result::Result<Vec<_>, std::io::Error>>()?;

    if entries.is_empty() {
        return Err(NodeupError::internal_with_hint(
            format!(
                "Archive unpack produced an empty directory (temp_dir={})",
                extraction_dir.display()
            ),
            "Retry the install command. If it repeats, remove the archive and re-download.",
        ));
    }

    if entries.len() == 1 && entries[0].file_type()?.is_dir() {
        let extracted_root = entries.pop().unwrap().path();
        if matches!(target.archive_format(), ArchiveFormat::Zip) {
            normalize_windows_runtime_layout(&extracted_root)?;
        }
        return Ok(extracted_root);
    }

    let normalized_root = extraction_dir.join("nodeup-runtime");
    fs::create_dir(&normalized_root)?;

    if matches!(target.archive_format(), ArchiveFormat::Zip) {
        let bin_dir = normalized_root.join("bin");
        fs::create_dir(&bin_dir)?;
        for entry in entries {
            let destination = bin_dir.join(entry.file_name());
            fs::rename(entry.path(), destination)?;
        }
    } else {
        for entry in entries {
            let destination = normalized_root.join(entry.file_name());
            fs::rename(entry.path(), destination)?;
        }
    }

    Ok(normalized_root)
}

fn normalize_windows_runtime_layout(runtime_root: &Path) -> Result<()> {
    let bin_dir = runtime_root.join("bin");
    if bin_dir.exists() {
        return Ok(());
    }

    let entries =
        fs::read_dir(runtime_root)?.collect::<std::result::Result<Vec<_>, std::io::Error>>()?;
    fs::create_dir(&bin_dir)?;

    for entry in entries {
        let file_name = entry.file_name();
        if file_name == "bin" {
            continue;
        }

        fs::rename(entry.path(), bin_dir.join(file_name))?;
    }

    Ok(())
}

pub fn parse_shasums(content: &str) -> Result<HashMap<String, String>> {
    parse_shasums_for_archive(content, "unknown", "unknown", "unknown")
}

fn parse_shasums_for_archive(
    content: &str,
    source: &str,
    runtime: &str,
    archive_filename: &str,
) -> Result<HashMap<String, String>> {
    let reader = BufReader::new(content.as_bytes());
    let mut checksums = HashMap::new();

    for (index, line) in reader.lines().enumerate() {
        let line_number = index + 1;
        let line = line?;
        let trimmed = line.trim();
        if trimmed.is_empty() {
            continue;
        }

        let mut parts = trimmed.split_whitespace();
        let checksum = parts.next().ok_or_else(|| {
            NodeupError::invalid_input_with_hint(
                format!(
                    "Malformed SHASUMS256.txt line: missing checksum value (runtime={runtime}, \
                     archive={archive_filename}, source={source}, line={line_number}, \
                     preview='{}')",
                    shasums_preview(trimmed)
                ),
                "Retry the command. If the issue persists, inspect upstream SHASUMS256.txt \
                 content.",
            )
        })?;
        let filename = parts
            .next()
            .ok_or_else(|| {
                NodeupError::invalid_input_with_hint(
                    format!(
                        "Malformed SHASUMS256.txt line: missing archive filename \
                         (runtime={runtime}, archive={archive_filename}, source={source}, \
                         line={line_number}, preview='{}')",
                        shasums_preview(trimmed)
                    ),
                    "Retry the command. If the issue persists, inspect upstream SHASUMS256.txt \
                     content.",
                )
            })?
            .trim_start_matches('*');

        checksums.insert(filename.to_string(), checksum.to_string());
    }

    Ok(checksums)
}

fn shasums_preview(line: &str) -> String {
    const MAX_CHARS: usize = 80;
    let escaped = line
        .replace('\\', "\\\\")
        .replace('\n', "\\n")
        .replace('\r', "\\r")
        .replace('\t', "\\t")
        .replace('\'', "\\'");

    let mut chars = escaped.chars();
    let preview = chars.by_ref().take(MAX_CHARS).collect::<String>();
    if chars.next().is_some() {
        format!("{preview}...")
    } else {
        preview
    }
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
                Err(NodeupError::conflict_with_hint(
                    format!("Another install is already running for runtime {version}"),
                    "Wait for the other install to finish, or remove a stale lock file if no \
                     install is active.",
                ))
            }
            Err(error) => Err(NodeupError::internal_with_hint(
                format!("Failed to create install lock {}: {error}", path.display()),
                "Check filesystem permissions for the toolchains directory and retry.",
            )),
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
    use zip::{write::FileOptions, ZipWriter};

    use super::*;
    use crate::errors::ErrorKind;

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

    fn make_test_tar_xz() -> Vec<u8> {
        let mut tar_payload = Vec::new();
        {
            let mut builder = tar::Builder::new(&mut tar_payload);
            let mut header = tar::Header::new_gnu();
            header.set_mode(0o755);
            header.set_size(4);
            header.set_cksum();
            builder
                .append_data(
                    &mut header,
                    "node-v22.1.0-linux-arm64/bin/node",
                    &b"echo"[..],
                )
                .unwrap();
            builder.finish().unwrap();
        }

        let mut encoder = xz2::write::XzEncoder::new(Vec::new(), 6);
        encoder.write_all(&tar_payload).unwrap();
        encoder.finish().unwrap()
    }

    fn make_test_zip() -> Vec<u8> {
        let mut cursor = std::io::Cursor::new(Vec::new());
        {
            let mut writer = ZipWriter::new(&mut cursor);
            let options = FileOptions::default().unix_permissions(0o755);
            writer.start_file("node.exe", options).unwrap();
            writer.write_all(b"node").unwrap();
            writer.start_file("npm.cmd", options).unwrap();
            writer.write_all(b"npm").unwrap();
            writer.finish().unwrap();
        }
        cursor.into_inner()
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

    #[test]
    fn tar_xz_extracts_to_runtime_root() {
        let dir = tempfile::tempdir().unwrap();
        let archive_path = dir.path().join("node-v22.1.0-linux-arm64.tar.xz");
        let runtime_dir = dir.path().join("toolchains").join("v22.1.0");
        fs::create_dir_all(runtime_dir.parent().unwrap()).unwrap();
        fs::write(&archive_path, make_test_tar_xz()).unwrap();

        extract_archive_to_runtime(&archive_path, &runtime_dir, PlatformTarget::LinuxArm64)
            .unwrap();

        assert!(runtime_dir.join("bin").join("node").exists());
    }

    #[test]
    fn zip_extracts_and_normalizes_flat_windows_layout() {
        let dir = tempfile::tempdir().unwrap();
        let archive_path = dir.path().join("node-v22.1.0-win-arm64.zip");
        let runtime_dir = dir.path().join("toolchains").join("v22.1.0");
        fs::create_dir_all(runtime_dir.parent().unwrap()).unwrap();
        fs::write(&archive_path, make_test_zip()).unwrap();

        extract_archive_to_runtime(&archive_path, &runtime_dir, PlatformTarget::WindowsArm64)
            .unwrap();

        assert!(runtime_dir.join("bin").join("node.exe").exists());
        assert!(runtime_dir.join("bin").join("npm.cmd").exists());
    }

    #[test]
    fn parse_shasums_failure_includes_runtime_archive_and_line_context() {
        let error = parse_shasums_for_archive(
            "abc123\n",
            "https://nodejs.org/download/release/v22.1.0/SHASUMS256.txt",
            "v22.1.0",
            "node-v22.1.0-linux-x64.tar.xz",
        )
        .unwrap_err();

        assert_eq!(error.kind, ErrorKind::InvalidInput);
        assert!(error.message.contains("runtime=v22.1.0"));
        assert!(error
            .message
            .contains("archive=node-v22.1.0-linux-x64.tar.xz"));
        assert!(error.message.contains("source=https://nodejs.org"));
        assert!(error.message.contains("line=1"));
        assert!(error.message.contains("preview='abc123'"));
    }
}
