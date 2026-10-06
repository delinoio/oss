// SPDX-License-Identifier: Apache-2.0
//! Closed native installation infrastructure. Go independently owns signed
//! release verification, immutable download and the durable once-only journal.
use sha2::{Digest, Sha256};

use super::*;

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum Action {
    Prepare,
    Install,
    Inspect,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum Phase {
    Prepared,
    Installing,
    Installed,
    Failed,
    Uncertain,
}
#[derive(Clone, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Prepared {
    version: u32,
    pub operation_id: String,
    pub server_id: String,
    pub generation: String,
    pub release_version: String,
    pub target: String,
    pub phase: Phase,
    artifact_path: PathBuf,
    artifact_sha256: String,
    artifact_size: u64,
    manifest_sha256: String,
}
#[derive(Serialize)]
pub struct PublicResult {
    pub operation_id: String,
    pub release_version: String,
    pub phase: Phase,
}
impl Prepared {
    pub fn public(&self) -> PublicResult {
        PublicResult {
            operation_id: self.operation_id.clone(),
            release_version: self.release_version.clone(),
            phase: self.phase,
        }
    }

    fn validate(&self, root: &Path, id: &str, server: &str, generation: &str) -> Result<()> {
        for value in [&self.operation_id, &self.server_id, &self.generation] {
            canonical_id(value)?;
        }
        let target = format!(
            "{}-{}",
            std::env::consts::OS,
            match std::env::consts::ARCH {
                "x86_64" => "amd64",
                "aarch64" => "arm64",
                _ => return Err(NativeFailure::Incompatible),
            }
        );
        let extension = match std::env::consts::OS {
            "macos" => ".dmg",
            "windows" => ".exe",
            "linux" => ".AppImage",
            _ => return Err(NativeFailure::Incompatible),
        };
        let target = target.replace("macos-", "darwin-");
        let digest = |v: &str| {
            v.len() == 64
                && v.bytes()
                    .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
        };
        if self.artifact_size == 0
            || self.artifact_size > 2 << 30
            || self.version != 1
            || self.operation_id != id
            || self.server_id != server
            || self.generation != generation
            || self.target != target
            || !digest(&self.manifest_sha256)
            || !digest(&self.artifact_sha256)
            || self.artifact_path
                != root
                    .join("desktop-updates/downloads")
                    .join(format!("{}{}", self.artifact_sha256, extension))
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(())
    }
}
pub struct DesktopUpdateRequest<'a> {
    pub server: &'a str,
    pub id: &'a str,
    pub revision: u64,
    pub generation: &'a str,
    pub action: &'a str,
    pub outcome: Option<Phase>,
}
impl Connector {
    pub fn desktop_update(
        &self,
        expected: Option<&SavedConnection>,
        request: DesktopUpdateRequest<'_>,
    ) -> Result<Prepared> {
        let DesktopUpdateRequest {
            server,
            id,
            revision,
            generation,
            action,
            outcome,
        } = request;
        canonical_id(server)?;
        canonical_id(id)?;
        canonical_id(generation)?;
        if revision == 0
            || revision >= 1 << 63
            || ![
                "native-prepare",
                "native-verify",
                "native-begin",
                "native-outcome",
                "native-inspect",
            ]
            .contains(&action)
        {
            return Err(NativeFailure::InvalidInput);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        if let Some(profile) = expected {
            self.check_saved_profile(profile)?;
            if profile.server_id != server {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        let mut args: Vec<OsString> = vec![
            "update".into(),
            action.into(),
            "--id".into(),
            id.into(),
            "--revision".into(),
            revision.to_string().into(),
            "--server-id".into(),
            server.into(),
            "--native-generation".into(),
            generation.into(),
        ];
        if let Some(profile) = expected {
            args.extend(["--saved-connection".into(), profile.id.as_str().into()]);
        }
        if let Some(phase) = outcome {
            args.extend([
                "--outcome".into(),
                match phase {
                    Phase::Installed => "installed",
                    Phase::Failed => "failed",
                    Phase::Uncertain => "uncertain",
                    _ => return Err(NativeFailure::InvalidInput),
                }
                .into(),
            ]);
        }
        let value = self.run_with_input_bound(&args, None, Duration::from_secs(660))?;
        let prepared: Prepared =
            serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?;
        prepared.validate(
            &self.root,
            id,
            server,
            if action == "native-inspect" {
                &prepared.generation
            } else {
                generation
            },
        )?;
        Ok(prepared)
    }

    pub fn install_desktop(&self, prepared: &Prepared) -> Phase {
        tracing::info!(
            operation = "desktop_update",
            phase = "installer-start",
            target = prepared.target
        );
        let result = match stage_verified(prepared) {
            Ok(staged) => install(&staged, &self.exiting),
            Err(_) => Phase::Failed,
        };
        tracing::info!(operation="desktop_update",phase=?result,target=prepared.target);
        result
    }
}

// Stream once from the opened original signed artifact into native-owned,
// exclusive private staging. Hash the exact bytes the OS installer consumes.
fn stage_verified(p: &Prepared) -> Result<Prepared> {
    if !no_symlink_ancestors(&p.artifact_path) {
        return Err(NativeFailure::InvalidEvidence);
    }
    let mut source = File::open(&p.artifact_path).map_err(|_| NativeFailure::StorageUnavailable)?;
    if source
        .metadata()
        .map_err(|_| NativeFailure::StorageUnavailable)?
        .len()
        != p.artifact_size
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    let root = p
        .artifact_path
        .parent()
        .and_then(|v| v.parent())
        .ok_or(NativeFailure::InvalidEvidence)?
        .join("native");
    let created = match fs::create_dir(&root) {
        Ok(()) => true,
        Err(e) if e.kind() == std::io::ErrorKind::AlreadyExists => false,
        Err(_) => return Err(NativeFailure::StorageUnavailable),
    };
    if !no_symlink_ancestors(&root) {
        return Err(NativeFailure::InvalidEvidence);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::{MetadataExt, PermissionsExt};
        let m = fs::metadata(&root).map_err(|_| NativeFailure::StorageUnavailable)?;
        let owner = fs::metadata(root.parent().ok_or(NativeFailure::InvalidEvidence)?)
            .map_err(|_| NativeFailure::StorageUnavailable)?
            .uid();
        if m.uid() != owner {
            return Err(NativeFailure::PermissionDenied);
        }
        if m.permissions().mode() & 0o077 != 0 {
            if !created {
                return Err(NativeFailure::PermissionDenied);
            };
            fs::set_permissions(&root, fs::Permissions::from_mode(0o700))
                .map_err(|_| NativeFailure::StorageUnavailable)?;
        }
    }
    let extension = p
        .artifact_path
        .extension()
        .ok_or(NativeFailure::InvalidEvidence)?;
    let path = root.join(format!("{}.{}", p.generation, extension.to_string_lossy()));
    let mut options = fs::OpenOptions::new();
    options.write(true).create_new(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let mut dest = options
        .open(&path)
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    let mut hash = Sha256::new();
    let mut count = 0u64;
    let mut buffer = [0u8; 65536];
    loop {
        let n = source
            .read(&mut buffer)
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        if n == 0 {
            break;
        };
        count = count
            .checked_add(n as u64)
            .ok_or(NativeFailure::InvalidEvidence)?;
        if count > p.artifact_size {
            return Err(NativeFailure::InvalidEvidence);
        };
        dest.write_all(&buffer[..n])
            .map_err(|_| NativeFailure::StorageUnavailable)?;
        hash.update(&buffer[..n]);
    }
    if count != p.artifact_size
        || hash
            .finalize()
            .iter()
            .map(|v| format!("{v:02x}"))
            .collect::<String>()
            != p.artifact_sha256
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    dest.sync_all()
        .map_err(|_| NativeFailure::StorageUnavailable)?;
    let mut staged = p.clone();
    staged.artifact_path = path;
    Ok(staged)
}

// Commands and destinations are native-selected; no argument, environment or
// path from the renderer reaches an OS installer. Unknown completion is
// retained.
#[cfg(any(target_os = "macos", target_os = "windows"))]
fn command(program: &Path, args: &[OsString], exiting: &AtomicBool, seconds: u64) -> Result<()> {
    let mut cmd = Command::new(program);
    cmd.args(args)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .env_clear();
    for key in [
        "HOME",
        "TMPDIR",
        "TEMP",
        "TMP",
        "SystemRoot",
        "WINDIR",
        "USERPROFILE",
        "LOCALAPPDATA",
        "APPDATA",
    ] {
        if let Some(value) = std::env::var_os(key) {
            cmd.env(key, value);
        }
    }
    #[cfg(unix)]
    cmd.env("PATH", "/usr/bin:/bin:/usr/sbin:/sbin");
    let mut child = cmd.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
    let start = Instant::now();
    loop {
        match child.try_wait() {
            Ok(Some(status)) => {
                return if status.success() {
                    Ok(())
                } else {
                    Err(NativeFailure::SidecarFailed)
                };
            }
            Ok(None)
                if !exiting.load(Ordering::Acquire)
                    && start.elapsed() < Duration::from_secs(seconds) =>
            {
                thread::sleep(Duration::from_millis(25))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                return Err(NativeFailure::TimedOut);
            }
        }
    }
}
#[cfg(any(target_os = "windows", target_os = "linux"))]
fn regular(path: &Path) -> bool {
    fs::symlink_metadata(path).is_ok_and(|m| m.is_file() && !m.file_type().is_symlink())
}
fn no_symlink_ancestors(path: &Path) -> bool {
    path.ancestors()
        .all(|p| fs::symlink_metadata(p).is_ok_and(|m| !m.file_type().is_symlink()))
}
#[cfg(target_os = "macos")]
fn exchange(a: &Path, b: &Path) -> Result<()> {
    use std::{ffi::CString, os::unix::ffi::OsStrExt};
    unsafe extern "C" {
        fn renamex_np(a: *const std::ffi::c_char, b: *const std::ffi::c_char, flags: u32) -> i32;
    }
    let a = CString::new(a.as_os_str().as_bytes()).map_err(|_| NativeFailure::InvalidInput)?;
    let b = CString::new(b.as_os_str().as_bytes()).map_err(|_| NativeFailure::InvalidInput)?;
    // SAFETY: live NUL-terminated sibling paths. macOS SDK RENAME_SWAP=2
    // atomically exchanges same-volume directories, preserving the old bundle.
    if unsafe { renamex_np(a.as_ptr(), b.as_ptr(), 2) } == 0 {
        Ok(())
    } else {
        Err(NativeFailure::StorageUnavailable)
    }
}
#[cfg(target_os = "macos")]
fn plist_is(path: &Path, key: &str, value: &str, exiting: &AtomicBool) -> bool {
    // PlistBuddy's output remains private, bounded and never logged. The fixed
    // key is compared inside the OS command without invoking a shell.
    let mut cmd = Command::new("/usr/libexec/PlistBuddy");
    cmd.args(["-c", &format!("Print :{key}")])
        .arg(path)
        .env_clear()
        .stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::null());
    let Ok(mut child) = cmd.spawn() else {
        return false;
    };
    let Some(out) = child.stdout.take() else {
        return false;
    };
    let reader = thread::spawn(move || read_bounded(out, 4096));
    let start = Instant::now();
    let success = loop {
        match child.try_wait() {
            Ok(Some(s)) => break s.success(),
            Ok(None)
                if !exiting.load(Ordering::Acquire)
                    && start.elapsed() < Duration::from_secs(10) =>
            {
                thread::sleep(Duration::from_millis(25))
            }
            _ => {
                let _ = child.kill();
                let _ = child.wait();
                break false;
            }
        }
    };
    success
        && reader
            .join()
            .ok()
            .and_then(|v| v.ok())
            .is_some_and(|v| String::from_utf8(v).is_ok_and(|v| v.trim() == value))
}
#[cfg(target_os = "macos")]
fn install(p: &Prepared, exiting: &AtomicBool) -> Phase {
    let Ok(exe) = std::env::current_exe() else {
        return Phase::Failed;
    };
    let Some(bundle) = exe
        .ancestors()
        .find(|path| path.extension().is_some_and(|v| v == "app"))
    else {
        return Phase::Failed;
    };
    if exe != bundle.join("Contents/MacOS/delidev-desktop")
        || !no_symlink_ancestors(bundle)
        || !plist_is(
            &bundle.join("Contents/Info.plist"),
            "CFBundleIdentifier",
            "io.delino.delidev",
            exiting,
        )
    {
        return Phase::Failed;
    }
    let Some(parent) = bundle.parent() else {
        return Phase::Failed;
    };
    let backup = parent.join(format!(".DeliDev.previous-{}.app", p.operation_id));
    if fs::symlink_metadata(&backup).is_ok() {
        return Phase::Uncertain;
    }
    let mount = std::env::temp_dir().join(format!("delidev-update-{}", p.generation));
    if fs::create_dir(&mount).is_err() {
        return Phase::Failed;
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if fs::set_permissions(&mount, fs::Permissions::from_mode(0o700)).is_err() {
            return Phase::Failed;
        }
    }
    let attach = command(
        Path::new("/usr/bin/hdiutil"),
        &[
            "attach".into(),
            "-readonly".into(),
            "-nobrowse".into(),
            "-noautoopen".into(),
            "-mountpoint".into(),
            mount.as_os_str().into(),
            p.artifact_path.as_os_str().into(),
        ],
        exiting,
        60,
    );
    let mut result = Phase::Failed;
    if attach.is_ok() {
        let app = mount.join("DeliDev.app");
        let plist = app.join("Contents/Info.plist");
        let valid = fs::symlink_metadata(&app)
            .is_ok_and(|m| m.is_dir() && !m.file_type().is_symlink())
            && plist_is(&plist, "CFBundleIdentifier", "io.delino.delidev", exiting)
            && plist_is(
                &plist,
                "CFBundleShortVersionString",
                &p.release_version,
                exiting,
            )
            && command(
                Path::new("/usr/bin/codesign"),
                &[
                    "--verify".into(),
                    "--deep".into(),
                    "--strict".into(),
                    app.as_os_str().into(),
                ],
                exiting,
                60,
            )
            .is_ok();
        if valid
            && command(
                Path::new("/usr/bin/ditto"),
                &[app.as_os_str().into(), backup.as_os_str().into()],
                exiting,
                120,
            )
            .is_ok()
            && !exiting.load(Ordering::Acquire)
            && exchange(bundle, &backup).is_ok()
        {
            // The old bundle remains at backup; live server/harness processes
            // are neither restarted nor overwritten by installation.
            result = if File::open(parent).and_then(|f| f.sync_all()).is_ok() {
                Phase::Installed
            } else {
                Phase::Uncertain
            };
        }
    } else {
        result = Phase::Uncertain
    }
    let cleanup = AtomicBool::new(false);
    if command(
        Path::new("/usr/bin/hdiutil"),
        &["detach".into(), mount.as_os_str().into()],
        &cleanup,
        30,
    )
    .is_err()
    {
        tracing::warn!(
            operation = "desktop_update",
            phase = "mount-cleanup",
            code = "unconfirmed"
        );
    }
    let _ = fs::remove_dir(&mount);
    result
}
#[cfg(target_os = "windows")]
fn install(p: &Prepared, exiting: &AtomicBool) -> Phase {
    if !regular(&p.artifact_path) {
        return Phase::Failed;
    }
    // NSIS can commit files before a nonzero/timeout result. Never claim an old
    // version rollback after invoking it; retain restart-only uncertainty.
    if command(&p.artifact_path, &[], exiting, 300).is_ok() {
        Phase::Installed
    } else {
        Phase::Uncertain
    }
}
#[cfg(target_os = "linux")]
fn install(p: &Prepared, exiting: &AtomicBool) -> Phase {
    use std::os::unix::fs::OpenOptionsExt;
    let Some(image) = std::env::var_os("APPIMAGE").map(PathBuf::from) else {
        return Phase::Failed;
    };
    let Some(dir) = std::env::var_os("APPDIR").map(PathBuf::from) else {
        return Phase::Failed;
    };
    let Ok(exe) = std::env::current_exe() else {
        return Phase::Failed;
    };
    if !image.is_absolute()
        || !dir.is_absolute()
        || !exe.starts_with(&dir)
        || !regular(&image)
        || !no_symlink_ancestors(&image)
    {
        return Phase::Failed;
    }
    // Correlate the mounted runtime with the actual backing AppImage through
    // the kernel mount inventory, in addition to the environment/executable.
    let Ok(mounts) = fs::read_to_string("/proc/self/mountinfo") else {
        return Phase::Failed;
    };
    if mounts.len() > 1 << 20
        || !mounts.lines().any(|line| {
            let fields: Vec<_> = line.split_whitespace().collect();
            fields.get(4).is_some_and(|v| Path::new(v) == dir)
                && line.split(" - ").nth(1).is_some_and(|v| {
                    v.split_whitespace()
                        .nth(1)
                        .is_some_and(|v| Path::new(v) == image)
                })
        })
    {
        return Phase::Failed;
    }
    let Some(parent) = image.parent() else {
        return Phase::Failed;
    };
    let backup = parent.join(format!(".DeliDev.previous-{}.AppImage", p.operation_id));
    let staged = parent.join(format!(".DeliDev.pending-{}.AppImage", p.operation_id));
    if fs::symlink_metadata(&backup).is_ok() || fs::symlink_metadata(&staged).is_ok() {
        return Phase::Uncertain;
    }
    let copied = (|| -> std::io::Result<()> {
        let mut source = File::open(&p.artifact_path)?;
        let mut dest = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o700)
            .open(&staged)?;
        std::io::copy(&mut source, &mut dest)?;
        dest.sync_all()?;
        Ok(())
    })();
    if copied.is_err() || exiting.load(Ordering::Acquire) {
        let _ = fs::remove_file(&staged);
        return Phase::Failed;
    }
    if fs::hard_link(&image, &backup).is_err() {
        return Phase::Failed;
    }
    if File::open(parent).and_then(|f| f.sync_all()).is_err() {
        return Phase::Uncertain;
    }
    if fs::rename(&staged, &image).is_err() {
        return Phase::Failed;
    }
    if File::open(parent).and_then(|f| f.sync_all()).is_err() {
        return Phase::Uncertain;
    }
    Phase::Installed
}

#[cfg(test)]
mod tests {
    use sha2::{Digest, Sha256};

    use super::*;
    #[test]
    fn descriptor_cannot_select_paths_or_another_authority() {
        let root = Path::new("/private/product");
        let id = uuid::Uuid::now_v7().to_string();
        let server = uuid::Uuid::now_v7().to_string();
        let generation = uuid::Uuid::now_v7().to_string();
        let target = format!(
            "{}-{}",
            std::env::consts::OS,
            if cfg!(target_arch = "aarch64") {
                "arm64"
            } else {
                "amd64"
            }
        )
        .replace("macos-", "darwin-");
        let ext = if cfg!(target_os = "macos") {
            ".dmg"
        } else if cfg!(windows) {
            ".exe"
        } else {
            ".AppImage"
        };
        let mut p = Prepared {
            version: 1,
            operation_id: id.clone(),
            server_id: server.clone(),
            generation: generation.clone(),
            release_version: "0.2.0".into(),
            target,
            phase: Phase::Prepared,
            artifact_path: root.join("desktop-updates/downloads").join(format!(
                "{}{}",
                "a".repeat(64),
                ext
            )),
            artifact_sha256: "a".repeat(64),
            artifact_size: 1,
            manifest_sha256: "b".repeat(64),
        };
        assert!(p.validate(root, &id, &server, &generation).is_ok());
        p.artifact_path = PathBuf::from("/tmp/external");
        assert!(p.validate(root, &id, &server, &generation).is_err());
        p.operation_id = uuid::Uuid::now_v7().to_string();
        assert!(p.validate(root, &id, &server, &generation).is_err());
    }
    #[test]
    fn native_staging_checks_actual_bytes_and_never_replaces_a_generation() {
        let root = tempfile::tempdir().unwrap();
        // macOS exposes the temporary directory through `/var`, which is a
        // symlink to `/private/var`. Use the physical path so this fixture
        // exercises the staging checks instead of rejecting the platform's
        // stable system alias as an untrusted ancestor.
        let root_path = root.path().canonicalize().unwrap();
        let downloads = root_path.join("downloads");
        fs::create_dir(&downloads).unwrap();
        let source = downloads.join("source.dmg");
        fs::write(&source, b"verified").unwrap();
        let p = Prepared {
            version: 1,
            operation_id: uuid::Uuid::now_v7().to_string(),
            server_id: uuid::Uuid::now_v7().to_string(),
            generation: uuid::Uuid::now_v7().to_string(),
            release_version: "0.2.0".into(),
            target: "darwin-arm64".into(),
            phase: Phase::Installing,
            artifact_path: source,
            artifact_size: 8,
            artifact_sha256: Sha256::digest(b"verified")
                .iter()
                .map(|v| format!("{v:02x}"))
                .collect::<String>(),
            manifest_sha256: "a".repeat(64),
        };
        let staged = stage_verified(&p).unwrap();
        assert_eq!(fs::read(&staged.artifact_path).unwrap(), b"verified");
        assert!(stage_verified(&p).is_err());
        let mut bad = p.clone();
        bad.generation = uuid::Uuid::now_v7().to_string();
        bad.artifact_sha256 = "b".repeat(64);
        assert!(stage_verified(&bad).is_err());
    }
    #[cfg(target_os = "macos")]
    #[test]
    fn exchange_preserves_both_native_generations() {
        let root = tempfile::tempdir().unwrap();
        let a = root.path().join("a");
        let b = root.path().join("b");
        fs::create_dir(&a).unwrap();
        fs::create_dir(&b).unwrap();
        fs::write(a.join("old"), b"old").unwrap();
        fs::write(b.join("new"), b"new").unwrap();
        exchange(&a, &b).unwrap();
        assert_eq!(fs::read(a.join("new")).unwrap(), b"new");
        assert_eq!(fs::read(b.join("old")).unwrap(), b"old");
        exchange(&a, &b).unwrap();
        assert!(a.join("old").exists());
    }
}
