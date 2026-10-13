// SPDX-License-Identifier: Apache-2.0
//! Closed native installation infrastructure. Go independently owns signed
//! release verification, immutable download and the durable once-only journal.
#[cfg(any(target_os = "macos", target_os = "windows"))]
use std::{thread, time::Instant};

use sha2::{Digest, Sha256};

use super::{
    updater_outcome::{OutcomeRecord, OutcomeStore, StoredEntry},
    *,
};

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

// Constructed only by a successful original native-begin. The renderer cannot
// supply or change this cleanup authority, including its selected saved scope.
pub struct AcceptedInstallation {
    prepared: Prepared,
    revision: u64,
    saved: Option<SavedConnection>,
    outcome: Mutex<Option<Phase>>,
}

impl AcceptedInstallation {
    pub fn matches(&self, server: &str, id: &str, revision: u64) -> bool {
        self.prepared.server_id == server
            && self.prepared.operation_id == id
            && self.revision == revision
    }

    pub fn install(&self, connector: &Connector) -> Phase {
        self.install_with(connector.exiting.load(Ordering::Acquire), || {
            connector.install_desktop(&self.prepared)
        })
    }

    fn install_with(&self, stopping: bool, installer: impl FnOnce() -> Phase) -> Phase {
        let Ok(mut outcome) = self.outcome.lock() else {
            return Phase::Uncertain;
        };
        if let Some(phase) = *outcome {
            return phase;
        }
        let phase = if stopping {
            Phase::Failed
        } else {
            // Catch at the effect boundary while the original outcome and
            // host pending guards remain valid. An installer panic proves no
            // safe terminal effect; retain uncertainty without replay.
            std::panic::catch_unwind(std::panic::AssertUnwindSafe(installer)).unwrap_or_else(|_| {
                tracing::error!(
                    operation = "desktop_update",
                    phase = "installer-panic",
                    state = "original-uncertain"
                );
                Phase::Uncertain
            })
        };
        *outcome = Some(phase);
        phase
    }

    pub fn settle(&self, connector: &Connector, phase: Phase) -> Result<Prepared> {
        terminal_name(phase)?;
        let mut original = self.outcome.lock().map_err(|_| NativeFailure::Busy)?;
        if original.is_some_and(|previous| previous != phase) {
            return Err(NativeFailure::InvalidEvidence);
        }
        *original = Some(phase);
        let p = &self.prepared;
        settle_original(
            connector,
            &OriginalIdentity {
                server_id: &p.server_id,
                operation_id: &p.operation_id,
                revision: self.revision,
                generation: &p.generation,
                saved_connection: self.saved.as_ref().map(|saved| saved.id.as_str()),
            },
            phase,
        )
    }

    /// The durable record for the exact outcome this installation observed.
    pub fn outcome_record(&self, phase: Phase) -> Result<OutcomeRecord> {
        let p = &self.prepared;
        OutcomeRecord::outcome(
            &p.server_id,
            &p.operation_id,
            self.revision,
            &p.generation,
            self.saved.as_ref().map(|saved| saved.id.as_str()),
            phase,
        )
    }
}

fn terminal_name(phase: Phase) -> Result<&'static str> {
    match phase {
        Phase::Installed => Ok("installed"),
        Phase::Failed => Ok("failed"),
        Phase::Uncertain => Ok("uncertain"),
        _ => Err(NativeFailure::InvalidInput),
    }
}

// Every identity the offline Go writer verifies against the original journal.
struct OriginalIdentity<'a> {
    server_id: &'a str,
    operation_id: &'a str,
    revision: u64,
    generation: &'a str,
    saved_connection: Option<&'a str>,
}

// Writes one exact phase to the original Installing journal. It never invokes
// an installer and never changes the journal generation: Go accepts only the
// same phase or the first terminal write. The response must echo the same
// phase before any caller may retire retained evidence.
fn settle_original(
    connector: &Connector,
    original: &OriginalIdentity<'_>,
    phase: Phase,
) -> Result<Prepared> {
    let name = terminal_name(phase)?;
    let mut args: Vec<OsString> = vec![
        "update".into(),
        "native-outcome".into(),
        "--id".into(),
        original.operation_id.into(),
        "--revision".into(),
        original.revision.to_string().into(),
        "--server-id".into(),
        original.server_id.into(),
        "--native-generation".into(),
        original.generation.into(),
        "--outcome".into(),
        name.into(),
    ];
    if let Some(saved) = original.saved_connection {
        args.extend(["--saved-connection".into(), saved.into()]);
    }
    // Retry only the exact same-phase offline write. A lost atomic-write
    // reply grants neither another installer invocation nor a new claim.
    let started = std::time::Instant::now();
    let mut last = NativeFailure::TimedOut;
    for _ in 0..4 {
        let remaining = Duration::from_secs(40).saturating_sub(started.elapsed());
        if remaining.is_zero() {
            break;
        }
        let result = connector
            .short_request_with_input_mode(&args, None, remaining, false)
            .and_then(|value| {
                let result: Prepared =
                    serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?;
                result.validate(
                    &connector.root,
                    original.operation_id,
                    original.server_id,
                    original.generation,
                )?;
                if result.phase != phase {
                    return Err(NativeFailure::InvalidEvidence);
                }
                Ok(result)
            });
        match result {
            Ok(result) => return Ok(result),
            Err(code) => {
                tracing::warn!(
                    operation = "desktop_update_settlement",
                    id = original.operation_id,
                    ?phase,
                    ?code
                );
                last = code;
                if !matches!(
                    code,
                    NativeFailure::Busy | NativeFailure::SidecarFailed | NativeFailure::TimedOut
                ) {
                    break;
                }
                std::thread::sleep(Duration::from_millis(25));
            }
        }
    }
    Err(last)
}

/// Settlement-only authority rebuilt from a validated retained record. It has
/// no installer entry point, so it can finish one original journal write but
/// can never authorize installation.
pub struct SettlementToken {
    record: OutcomeRecord,
}

impl SettlementToken {
    pub fn from_record(record: OutcomeRecord) -> Result<Self> {
        record.validate()?;
        Ok(Self { record })
    }

    pub fn record(&self) -> &OutcomeRecord {
        &self.record
    }

    pub fn settle(&self, connector: &Connector) -> Result<Prepared> {
        let r = &self.record;
        settle_original(
            connector,
            &OriginalIdentity {
                server_id: &r.server_id,
                operation_id: &r.operation_id,
                revision: r.revision,
                generation: &r.generation,
                saved_connection: r.saved_connection.as_deref(),
            },
            r.phase,
        )
    }
}

/// Which retained records one reconciliation pass may settle.
pub enum ReconcileScope<'a> {
    /// Every retained record, on application launch.
    Launch,
    /// Only the exact original server, operation, revision and saved scope.
    Original {
        server: &'a str,
        id: &'a str,
        revision: u64,
        saved_connection: Option<&'a str>,
    },
}

impl ReconcileScope<'_> {
    fn admits(&self, record: &OutcomeRecord) -> bool {
        match self {
            Self::Launch => true,
            Self::Original {
                server,
                id,
                revision,
                saved_connection,
            } => {
                record.server_id == *server
                    && record.operation_id == *id
                    && record.revision == *revision
                    && record.saved_connection.as_deref() == *saved_connection
            }
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct ReconcileReport {
    pub settled: usize,
    pub retained: usize,
    pub skipped: usize,
}

impl Connector {
    pub fn outcome_store(&self) -> OutcomeStore {
        OutcomeStore::new(&self.root)
    }

    /// Replays retained exact outcomes into their original Installing
    /// journals. A record is retired only after the settle response echoes the
    /// recorded phase for the same identities; corrupt, foreign or rejected
    /// evidence is kept and reported as retained uncertainty.
    pub fn reconcile_retained_outcomes(
        &self,
        scope: &ReconcileScope<'_>,
        stopping: &dyn Fn() -> bool,
    ) -> ReconcileReport {
        let store = self.outcome_store();
        let mut report = ReconcileReport::default();
        let entries = match store.scan() {
            Ok(entries) => entries,
            Err(code) => {
                tracing::warn!(
                    operation = "desktop_update_reconcile",
                    state = "scan-failed",
                    ?code
                );
                return report;
            }
        };
        for entry in entries {
            if stopping() {
                tracing::info!(operation = "desktop_update_reconcile", state = "stopped");
                break;
            }
            match entry {
                StoredEntry::Unreadable(name) => {
                    report.retained += 1;
                    tracing::warn!(
                        operation = "desktop_update_reconcile",
                        state = "evidence-unreadable-retained",
                        id = name.as_deref().unwrap_or("invalid-name")
                    );
                }
                StoredEntry::Record(record) if !scope.admits(&record) => report.skipped += 1,
                StoredEntry::Record(record) => {
                    let id = record.operation_id.clone();
                    let phase = record.phase;
                    let Ok(token) = SettlementToken::from_record(record) else {
                        report.retained += 1;
                        continue;
                    };
                    match token.settle(self) {
                        Ok(_) => match store.retire(token.record()) {
                            Ok(()) => {
                                report.settled += 1;
                                tracing::info!(
                                    operation = "desktop_update_reconcile",
                                    state = "settled",
                                    id,
                                    ?phase
                                );
                            }
                            Err(code) => {
                                report.settled += 1;
                                tracing::warn!(
                                    operation = "desktop_update_reconcile",
                                    state = "retire-failed",
                                    id,
                                    ?code
                                );
                            }
                        },
                        Err(code) => {
                            report.retained += 1;
                            tracing::warn!(
                                operation = "desktop_update_reconcile",
                                state = "settlement-uncertain-retained",
                                id,
                                ?phase,
                                ?code
                            );
                        }
                    }
                }
            }
        }
        report
    }

    pub fn begin_desktop_installation(
        &self,
        expected: Option<&SavedConnection>,
        mut request: DesktopUpdateRequest<'_>,
    ) -> Result<AcceptedInstallation> {
        request.action = "native-begin";
        request.outcome = None;
        let revision = request.revision;
        let prepared = self.desktop_update(expected, request)?;
        if prepared.phase != Phase::Installing {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(AcceptedInstallation {
            prepared,
            revision,
            saved: expected.cloned(),
            outcome: Mutex::new(None),
        })
    }
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
pub(crate) fn no_symlink_ancestors(path: &Path) -> bool {
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
    if !appimage_mount_matches(&mounts, &dir, &image, &exe) {
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

#[cfg(any(target_os = "linux", test))]
fn decode_mountinfo_path(field: &str) -> Option<PathBuf> {
    let mut decoded = Vec::with_capacity(field.len());
    let mut bytes = field.bytes();
    while let Some(byte) = bytes.next() {
        // Linux seq_path_root/seq_escape encode these characters as octal;
        // mount sources also escape '#'. Decode once so a literal "\\040"
        // remains distinct from a space. Reject every other escape.
        let byte = if byte == b'\\' {
            match [bytes.next()?, bytes.next()?, bytes.next()?] {
                [b'0', b'4', b'0'] => b' ',
                [b'0', b'1', b'1'] => b'\t',
                [b'0', b'1', b'2'] => b'\n',
                [b'1', b'3', b'4'] => b'\\',
                [b'0', b'4', b'3'] => b'#',
                _ => return None,
            }
        } else if byte == 0 {
            return None;
        } else {
            byte
        };
        decoded.push(byte);
    }
    Some(PathBuf::from(String::from_utf8(decoded).ok()?))
}

#[cfg(any(target_os = "linux", all(test, unix)))]
fn appimage_mount_matches(mounts: &str, dir: &Path, image: &Path, exe: &Path) -> bool {
    if mounts.len() > 1 << 20 || !image.is_absolute() || !dir.is_absolute() || !exe.starts_with(dir)
    {
        return false;
    }
    mounts.lines().any(|line| {
        let Some((mount, source)) = line.split_once(" - ") else {
            return false;
        };
        mount
            .split_ascii_whitespace()
            .nth(4)
            .and_then(decode_mountinfo_path)
            .is_some_and(|path| path == dir)
            && source
                .split_ascii_whitespace()
                .nth(1)
                .and_then(decode_mountinfo_path)
                .is_some_and(|path| path == image)
    })
}

#[cfg(test)]
mod tests {
    use sha2::{Digest, Sha256};

    use super::*;

    #[cfg(unix)]
    #[test]
    fn original_outcomes_settle_offline_after_quit_and_contention_without_reinstallation() {
        use std::os::unix::fs::PermissionsExt;
        for (phase, installer_panic) in [
            (Phase::Installed, false),
            (Phase::Failed, false),
            (Phase::Uncertain, false),
            (Phase::Uncertain, true),
        ] {
            let temp = tempfile::tempdir().unwrap();
            let sidecar = temp.path().join("sidecar");
            fs::write(
                &sidecar,
                "#!/bin/sh\nprintf x >> \"$2/invocations\"\nif [ ! -e \"$2/written\" ]; then \
                 touch \"$2/written\"; printf \
                 '{\"version\":1,\"error\":{\"code\":\"unavailable\"}}'; exit 1; fi\nexec \
                 /bin/cat \"$2/response.json\"\n",
            )
            .unwrap();
            fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
            let connector = Connector::new(sidecar, temp.path().to_path_buf()).unwrap();
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
            let extension = if cfg!(target_os = "macos") {
                ".dmg"
            } else {
                ".AppImage"
            };
            let descriptor = serde_json::json!({"version":1,"operation_id":id,"server_id":server,"generation":generation,"release_version":"0.2.0","target":target,"phase":phase,"artifact_path":connector.root.join("desktop-updates/downloads").join(format!("{}{}","a".repeat(64),extension)),"artifact_sha256":"a".repeat(64),"artifact_size":8,"manifest_sha256":"b".repeat(64)});
            fs::write(
                temp.path().join("response.json"),
                serde_json::to_vec(&serde_json::json!({"version":1,"result":descriptor})).unwrap(),
            )
            .unwrap();
            let mut installing: Prepared = serde_json::from_value(descriptor).unwrap();
            installing.phase = Phase::Installing;
            let accepted = AcceptedInstallation {
                prepared: installing,
                revision: 7,
                saved: None,
                outcome: Mutex::new(None),
            };
            let effects = std::sync::atomic::AtomicUsize::new(0);
            assert_eq!(
                accepted.install_with(false, || {
                    effects.fetch_add(1, Ordering::AcqRel);
                    if installer_panic {
                        panic!("original installer effect became uncertain");
                    }
                    phase
                }),
                phase
            );
            assert_eq!(
                accepted.install_with(false, || panic!("installation must not replay")),
                phase
            );
            assert_eq!(effects.load(Ordering::Acquire), 1);
            connector.exiting.store(true, Ordering::Release);
            let contention = connector.gate.lock().unwrap();
            assert_eq!(accepted.settle(&connector, phase).unwrap().phase, phase);
            assert_eq!(
                fs::read(temp.path().join("invocations")).unwrap(),
                b"xx",
                "lost reply retries only original offline writer"
            );
            assert_eq!(
                accepted.install(&connector),
                phase,
                "retained result forbids another installer effect"
            );
            assert!(matches!(
                accepted.settle(
                    &connector,
                    if phase == Phase::Failed {
                        Phase::Installed
                    } else {
                        Phase::Failed
                    }
                ),
                Err(NativeFailure::InvalidEvidence)
            ));
            assert_eq!(fs::read(temp.path().join("invocations")).unwrap(), b"xx");
            drop(contention);
            assert!(matches!(
                connector.desktop_update(
                    None,
                    DesktopUpdateRequest {
                        server: &server,
                        id: &id,
                        revision: 7,
                        generation: &generation,
                        action: "native-prepare",
                        outcome: None
                    }
                ),
                Err(NativeFailure::Stopped)
            ));
        }
    }

    #[test]
    fn mountinfo_decodes_kernel_path_escapes_once() {
        for (encoded, decoded) in [
            ("/home/user/DeliDev.AppImage", "/home/user/DeliDev.AppImage"),
            (
                r"/home/user/My\040Apps/DeliDev.AppImage",
                "/home/user/My Apps/DeliDev.AppImage",
            ),
            (
                r"/tmp/tab\011line\012slash\134hash\043",
                "/tmp/tab\tline\nslash\\hash#",
            ),
            (r"/tmp/literal\134040", r"/tmp/literal\040"),
            (
                "/home/Caf\u{e9}/DeliDev.AppImage",
                "/home/Caf\u{e9}/DeliDev.AppImage",
            ),
        ] {
            assert_eq!(decode_mountinfo_path(encoded), Some(PathBuf::from(decoded)));
        }
    }

    #[test]
    fn mountinfo_rejects_malformed_or_non_kernel_escapes() {
        for field in [
            "/tmp/trailing\\",
            r"/tmp/\0",
            r"/tmp/\04",
            r"/tmp/\08x",
            r"/tmp/\400",
            r"/tmp/\777",
            r"/tmp/\000",
            r"/tmp/\141",
            r"/tmp/\x20",
            "/tmp/nul\0",
        ] {
            assert!(decode_mountinfo_path(field).is_none());
        }
    }

    #[cfg(unix)]
    #[test]
    fn appimage_mount_matches_decoded_mount_and_source_in_the_same_record() {
        for (mount, source, dir, image) in [
            (
                "/tmp/.mount_DeliDev",
                "/home/user/DeliDev.AppImage",
                "/tmp/.mount_DeliDev",
                "/home/user/DeliDev.AppImage",
            ),
            (
                "/tmp/.mount_DeliDev",
                r"/home/user/My\040Apps/DeliDev.AppImage",
                "/tmp/.mount_DeliDev",
                "/home/user/My Apps/DeliDev.AppImage",
            ),
            (
                r"/tmp/mount\040with\011tab\012line\134slash",
                r"/home/user/tab\011line\012slash\134hash\043.AppImage",
                "/tmp/mount with\ttab\nline\\slash",
                "/home/user/tab\tline\nslash\\hash#.AppImage",
            ),
            (
                r"/tmp/literal\134040",
                r"/home/user/literal\134040.AppImage",
                r"/tmp/literal\040",
                r"/home/user/literal\040.AppImage",
            ),
        ] {
            let mounts =
                format!("36 35 0:42 / {mount} ro shared:7 unknown:1 - fuse.AppImage {source} ro\n");
            let dir = Path::new(dir);
            let image = Path::new(image);
            let exe = dir.join("usr/bin/delidev-desktop");
            assert!(appimage_mount_matches(&mounts, dir, image, &exe));
            assert!(!appimage_mount_matches(
                &mounts,
                Path::new("/tmp/other"),
                image,
                &exe
            ));
            assert!(!appimage_mount_matches(
                &mounts,
                dir,
                Path::new("/home/user/other.AppImage"),
                &exe
            ));
            assert!(!appimage_mount_matches(
                &mounts,
                dir,
                image,
                Path::new("/tmp/other/usr/bin/delidev-desktop")
            ));
        }
    }

    #[cfg(unix)]
    #[test]
    fn appimage_mount_rejects_invalid_inventory_and_unrelated_authority() {
        let dir = Path::new("/tmp/.mount_DeliDev");
        let image = Path::new("/home/user/My Apps/DeliDev.AppImage");
        let exe = dir.join("usr/bin/delidev-desktop");
        for mounts in [
            "",
            "36 35 0:42 / /tmp/.mount_DeliDev ro fuse.AppImage \
             /home/user/My\\040Apps/DeliDev.AppImage ro",
            "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage",
            "36 35 0:42 / /tmp/.mount_DeliDev\\04 ro - fuse.AppImage \
             /home/user/My\\040Apps/DeliDev.AppImage ro",
            "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage \
             /home/user/My\\04Apps/DeliDev.AppImage ro",
            "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage /home/user/other.AppImage ro\n37 \
             35 0:43 / /tmp/other ro - fuse.AppImage /home/user/My\\040Apps/DeliDev.AppImage ro",
        ] {
            assert!(!appimage_mount_matches(mounts, dir, image, &exe));
        }
        let mounts = "36 35 0:42 / /tmp/.mount_DeliDev ro - fuse.AppImage \
                      /home/user/My\\040Apps/DeliDev.AppImage ro\n";
        assert!(!appimage_mount_matches(
            mounts,
            dir,
            image,
            Path::new("/tmp/.mount_DeliDev-other/delidev-desktop")
        ));
        assert!(!appimage_mount_matches(
            mounts,
            Path::new("tmp/.mount_DeliDev"),
            image,
            &exe
        ));
        assert!(!appimage_mount_matches(
            mounts,
            dir,
            Path::new("DeliDev.AppImage"),
            &exe
        ));
        let oversized = format!("{mounts}{}", " ".repeat(1 << 20));
        assert!(!appimage_mount_matches(&oversized, dir, image, &exe));
    }

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

#[cfg(all(test, unix))]
mod retained_outcome_tests {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    const FIXTURE: &str = r#"#!/usr/bin/python3
import sys, json, os
root = sys.argv[2]
open(os.path.join(root, 'calls'), 'a').write('x')
args = sys.argv[3:]
def fail(code):
    print(json.dumps({'version': 1, 'error': {'code': code}}))
    sys.exit(1)
mode_path = os.path.join(root, 'mode')
mode = open(mode_path).read().strip() if os.path.exists(mode_path) else 'ok'
assert args[0:2] == ['update', 'native-outcome'], args
options = dict(zip(args[2::2], args[3::2]))
if mode == 'down':
    fail('unavailable')
journal_path = os.path.join(root, 'journal.json')
if not os.path.exists(journal_path):
    fail('recovery_required')
journal = json.load(open(journal_path))
if (options['--id'] != journal['operation_id']
        or options['--server-id'] != journal['server_id']
        or options['--revision'] != str(journal['revision'])
        or options['--native-generation'] != journal['generation']
        or options.get('--saved-connection') != journal.get('saved')):
    fail('recovery_required')
outcome = options['--outcome']
if journal['phase'] not in ('installing', outcome):
    fail('recovery_required')
journal['phase'] = outcome
json.dump(journal, open(journal_path, 'w'))
lost = os.path.join(root, 'lost-once')
if mode == 'lost-reply-once' and not os.path.exists(lost):
    open(lost, 'w').write('x')
    fail('unavailable')
descriptor = json.load(open(os.path.join(root, 'descriptor.json')))
descriptor['phase'] = outcome
print(json.dumps({'version': 1, 'result': descriptor}))
"#;

    struct Fixture {
        _temp: tempfile::TempDir,
        root: PathBuf,
        connector: Connector,
        server: String,
        id: String,
        generation: String,
        saved: Option<String>,
    }

    impl Fixture {
        fn new(saved: Option<&str>, journal: Option<&str>) -> Self {
            let temp = tempfile::tempdir().unwrap();
            let root = temp.path().canonicalize().unwrap();
            let sidecar = root.join("sidecar");
            fs::write(&sidecar, FIXTURE).unwrap();
            fs::set_permissions(&sidecar, fs::Permissions::from_mode(0o700)).unwrap();
            fs::create_dir_all(root.join("desktop-updates")).unwrap();
            fs::set_permissions(
                root.join("desktop-updates"),
                fs::Permissions::from_mode(0o700),
            )
            .unwrap();
            let connector = Connector::new(sidecar, root.clone()).unwrap();
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
            let extension = if cfg!(target_os = "macos") {
                ".dmg"
            } else {
                ".AppImage"
            };
            let descriptor = serde_json::json!({"version":1,"operation_id":id,"server_id":server,"generation":generation,"release_version":"0.2.0","target":target,"phase":"installing","artifact_path":root.join("desktop-updates/downloads").join(format!("{}{}","a".repeat(64),extension)),"artifact_sha256":"a".repeat(64),"artifact_size":8,"manifest_sha256":"b".repeat(64)});
            fs::write(
                root.join("descriptor.json"),
                serde_json::to_vec(&descriptor).unwrap(),
            )
            .unwrap();
            if let Some(phase) = journal {
                fs::write(
                    root.join("journal.json"),
                    serde_json::to_vec(&serde_json::json!({
                        "operation_id": id, "server_id": server, "revision": 7,
                        "generation": generation, "saved": saved, "phase": phase,
                    }))
                    .unwrap(),
                )
                .unwrap();
            }
            Self {
                _temp: temp,
                root,
                connector,
                server,
                id,
                generation,
                saved: saved.map(str::to_owned),
            }
        }

        fn record(&self, phase: Phase) -> OutcomeRecord {
            OutcomeRecord::outcome(
                &self.server,
                &self.id,
                7,
                &self.generation,
                self.saved.as_deref(),
                phase,
            )
            .unwrap()
        }

        fn store(&self) -> OutcomeStore {
            self.connector.outcome_store()
        }

        fn calls(&self) -> usize {
            fs::read(self.root.join("calls"))
                .map(|v| v.len())
                .unwrap_or(0)
        }

        fn journal(&self) -> serde_json::Value {
            serde_json::from_slice(&fs::read(self.root.join("journal.json")).unwrap()).unwrap()
        }

        fn marker(&self, id: &str) -> PathBuf {
            self.root
                .join(crate::updater_outcome::STORE_DIRECTORY)
                .join(format!("{id}.json"))
        }

        fn launch(&self) -> ReconcileReport {
            self.connector
                .reconcile_retained_outcomes(&ReconcileScope::Launch, &|| false)
        }
    }

    #[test]
    fn each_phase_replays_once_into_the_original_journal_and_retires() {
        for (phase, name) in [
            (Phase::Installed, "installed"),
            (Phase::Failed, "failed"),
            (Phase::Uncertain, "uncertain"),
        ] {
            let saved = uuid::Uuid::now_v7().to_string();
            let fixture = Fixture::new(Some(&saved), Some("installing"));
            fixture.store().persist(&fixture.record(phase)).unwrap();
            let marker = fixture.marker(&fixture.id);
            let mode = fs::metadata(&marker).unwrap().permissions().mode() & 0o777;
            assert_eq!(mode, 0o600, "marker is owner-only");
            let report = fixture.launch();
            assert_eq!(
                report,
                ReconcileReport {
                    settled: 1,
                    retained: 0,
                    skipped: 0
                }
            );
            assert!(!marker.exists(), "retired after matching settlement");
            assert_eq!(fixture.journal()["phase"], name);
            assert_eq!(fixture.journal()["generation"], fixture.generation);
            assert_eq!(fixture.calls(), 1, "one offline write and no installer");
            assert_eq!(fixture.launch(), ReconcileReport::default());
            assert_eq!(fixture.calls(), 1, "a retired record is never replayed");
        }
    }

    #[test]
    fn local_scope_without_saved_connection_replays() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.journal()["phase"], "installed");
    }

    #[test]
    fn lost_reply_retries_the_same_phase_and_retires() {
        let fixture = Fixture::new(None, Some("installing"));
        fs::write(fixture.root.join("mode"), "lost-reply-once").unwrap();
        fixture
            .store()
            .persist(&fixture.record(Phase::Failed))
            .unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.calls(), 2, "second same-phase write sees the first");
        assert_eq!(fixture.journal()["phase"], "failed");
        assert!(!fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn failed_settlement_retains_the_record_until_a_later_pass_succeeds() {
        let fixture = Fixture::new(None, Some("installing"));
        fs::write(fixture.root.join("mode"), "down").unwrap();
        let record = fixture.record(Phase::Installed);
        fixture.store().persist(&record).unwrap();
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (0, 1));
        assert!(fixture.marker(&fixture.id).exists());
        assert_eq!(fixture.journal()["phase"], "installing");
        assert_eq!(
            fixture.store().scan().unwrap(),
            vec![StoredEntry::Record(record)]
        );
        fs::write(fixture.root.join("mode"), "ok").unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.journal()["phase"], "installed");
        assert!(!fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn foreign_identity_or_scope_is_rejected_and_retained() {
        let saved = uuid::Uuid::now_v7().to_string();
        let other = uuid::Uuid::now_v7().to_string();
        for variant in ["saved", "no-saved", "server", "generation", "revision"] {
            let fixture = Fixture::new(Some(&saved), Some("installing"));
            let mut record = fixture.record(Phase::Installed);
            match variant {
                "saved" => record.saved_connection = Some(other.clone()),
                "no-saved" => record.saved_connection = None,
                "server" => record.server_id = other.clone(),
                "generation" => record.generation = other.clone(),
                _ => record.revision = 8,
            }
            fixture.store().persist(&record).unwrap();
            let report = fixture.launch();
            assert_eq!((report.settled, report.retained), (0, 1), "{variant}");
            assert_eq!(fixture.journal()["phase"], "installing", "{variant}");
            assert_eq!(
                fixture.store().scan().unwrap(),
                vec![StoredEntry::Record(record)],
                "{variant}"
            );
        }
    }

    #[test]
    fn original_scope_skips_other_records_without_contacting_the_sidecar() {
        let saved = uuid::Uuid::now_v7().to_string();
        let fixture = Fixture::new(Some(&saved), Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Failed))
            .unwrap();
        let other = uuid::Uuid::now_v7().to_string();
        let skipped = |server: &str, id: &str, revision: u64, scope: Option<&str>| {
            fixture.connector.reconcile_retained_outcomes(
                &ReconcileScope::Original {
                    server,
                    id,
                    revision,
                    saved_connection: scope,
                },
                &|| false,
            )
        };
        for report in [
            skipped(&other, &fixture.id, 7, Some(&saved)),
            skipped(&fixture.server, &other, 7, Some(&saved)),
            skipped(&fixture.server, &fixture.id, 8, Some(&saved)),
            skipped(&fixture.server, &fixture.id, 7, None),
            skipped(&fixture.server, &fixture.id, 7, Some(&other)),
        ] {
            assert_eq!(report.skipped, 1);
            assert_eq!(report.settled, 0);
        }
        assert_eq!(fixture.calls(), 0);
        let exact = skipped(&fixture.server, &fixture.id, 7, Some(&saved));
        assert_eq!(exact.settled, 1);
        assert_eq!(fixture.journal()["phase"], "failed");
    }

    #[test]
    fn corrupt_or_unparsable_evidence_is_retained_and_never_deleted() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let directory = fixture.root.join(crate::updater_outcome::STORE_DIRECTORY);
        let good = uuid::Uuid::now_v7().to_string();
        let mut files = vec![
            (format!("{good}.json"), b"{not json".to_vec()),
            (
                format!("{}.json", uuid::Uuid::now_v7()),
                vec![b' '; (4096 + 1) as usize],
            ),
            ("not-a-uuid.json".to_string(), b"{}".to_vec()),
        ];
        // A syntactically valid record stored under another operation's name.
        let misnamed = uuid::Uuid::now_v7().to_string();
        files.push((
            format!("{misnamed}.json"),
            serde_json::to_vec(&fixture.record(Phase::Installed)).unwrap(),
        ));
        // Unknown fields and a non-terminal phase are corrupt evidence too.
        let mut unknown = serde_json::to_value(fixture.record(Phase::Installed)).unwrap();
        unknown["install"] = serde_json::json!(true);
        let unknown_id = uuid::Uuid::now_v7().to_string();
        unknown["operation_id"] = serde_json::json!(unknown_id);
        files.push((
            format!("{unknown_id}.json"),
            serde_json::to_vec(&unknown).unwrap(),
        ));
        let mut prepared = serde_json::to_value(fixture.record(Phase::Installed)).unwrap();
        let prepared_id = uuid::Uuid::now_v7().to_string();
        prepared["operation_id"] = serde_json::json!(prepared_id);
        prepared["phase"] = serde_json::json!("prepared");
        files.push((
            format!("{prepared_id}.json"),
            serde_json::to_vec(&prepared).unwrap(),
        ));
        for (name, bytes) in &files {
            fs::write(directory.join(name), bytes).unwrap();
        }
        let report = fixture.launch();
        assert_eq!(report.settled, 1, "valid evidence still settles");
        assert_eq!(report.retained, files.len());
        assert_eq!(fixture.calls(), 1, "corrupt evidence never reaches Go");
        for (name, bytes) in files {
            assert_eq!(fs::read(directory.join(name)).unwrap(), bytes);
        }
    }

    #[test]
    fn absent_journal_never_infers_installed_and_keeps_the_record() {
        let fixture = Fixture::new(None, None);
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (0, 1));
        assert!(fixture.marker(&fixture.id).exists());
        assert!(!fixture.root.join("journal.json").exists());
    }

    #[test]
    fn conflicting_settled_phase_is_not_overwritten_and_keeps_the_record() {
        let fixture = Fixture::new(None, Some("failed"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let report = fixture.launch();
        assert_eq!((report.settled, report.retained), (0, 1));
        assert_eq!(fixture.journal()["phase"], "failed");
        assert!(fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn already_settled_same_phase_is_idempotent_and_retires() {
        let fixture = Fixture::new(None, Some("uncertain"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Uncertain))
            .unwrap();
        assert_eq!(fixture.launch().settled, 1);
        assert_eq!(fixture.journal()["phase"], "uncertain");
        assert!(!fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn stopping_prevents_new_settlements_and_keeps_records() {
        let fixture = Fixture::new(None, Some("installing"));
        fixture
            .store()
            .persist(&fixture.record(Phase::Installed))
            .unwrap();
        let report = fixture
            .connector
            .reconcile_retained_outcomes(&ReconcileScope::Launch, &|| true);
        assert_eq!(report, ReconcileReport::default());
        assert_eq!(fixture.calls(), 0);
        assert!(fixture.marker(&fixture.id).exists());
    }

    #[test]
    fn settlement_token_requires_a_terminal_valid_record() {
        let fixture = Fixture::new(None, Some("installing"));
        let mut record = fixture.record(Phase::Installed);
        record.phase = Phase::Prepared;
        assert!(matches!(
            SettlementToken::from_record(record),
            Err(NativeFailure::InvalidEvidence)
        ));
        let mut record = fixture.record(Phase::Installed);
        record.operation_id = "not-an-id".into();
        assert!(SettlementToken::from_record(record).is_err());
    }
}
