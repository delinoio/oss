//! Collector adapter process lifecycle: spawn, progress, cancellation.

use std::{
    path::{Path, PathBuf},
    process::Stdio,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

use serde::Deserialize;
use tokio::{
    io::{AsyncBufReadExt, AsyncReadExt, AsyncWriteExt, BufReader},
    sync::Notify,
};

use crate::{
    collect::{COLLECT_VERSION, Collection},
    detect::Selection,
    engine::ADAPTER_FILES,
    error::{BwError, BwResult, ErrorCode},
    model::RunStage,
};

/// Cancellation shared by Ctrl+C, the UI and the adapter supervisor.
#[derive(Default)]
pub struct CancelSignal {
    cancelled: AtomicBool,
    notify: Notify,
}

impl CancelSignal {
    pub fn cancel(&self) {
        self.cancelled.store(true, Ordering::SeqCst);
        self.notify.notify_waiters();
    }

    pub fn is_cancelled(&self) -> bool {
        self.cancelled.load(Ordering::SeqCst)
    }

    pub async fn wait(&self) {
        loop {
            let notified = self.notify.notified();
            if self.is_cancelled() {
                return;
            }
            notified.await;
        }
    }
}

#[derive(Debug, Clone)]
pub struct AdapterOptions {
    pub config_name: Option<String>,
    pub allow_unsupported_engine: bool,
    pub node: PathBuf,
}

/// Longest engine version string accepted from an adapter frame.
const MAX_ENGINE_VERSION_LEN: usize = 128;

/// Engine identity reported by the authenticated `engine` frame. The adapter
/// emits it right after resolving the engine package, before the version
/// check and before any config or build work, so it is known even when the
/// run later fails.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct DetectedEngine {
    pub package: String,
    pub version: String,
}

impl DetectedEngine {
    /// Accepts an engine frame only when it names the selected bundler's
    /// package and carries a bounded, printable, non-blank version string.
    /// The version is kept exactly as reported: trimming it here would record
    /// a padded manifest version as the matrix version, which the adapter's
    /// own exact comparison rejects.
    fn from_frame(expected_package: &str, package: &str, version: &str) -> Option<Self> {
        let valid_version = !version.trim().is_empty()
            && version.chars().count() <= MAX_ENGINE_VERSION_LEN
            && !version.chars().any(char::is_control);
        (package == expected_package && valid_version).then(|| Self {
            package: package.to_owned(),
            version: version.to_owned(),
        })
    }
}

#[derive(Debug, Deserialize)]
#[serde(tag = "event", rename_all = "camelCase")]
enum AdapterEvent {
    Progress {
        stage: String,
        message: String,
    },
    Engine {
        package: String,
        version: String,
    },
    Protection {
        root: String,
    },
    Error {
        code: String,
        message: String,
        recovery: String,
    },
    Done {},
}

/// How long buffered output is drained after the adapter exits.
const PIPE_DRAIN: Duration = Duration::from_secs(2);

/// Longest stdout line kept; longer project output is discarded while the
/// pipe keeps draining. Protocol frames are far shorter.
const MAX_STDOUT_LINE: usize = 1024 * 1024;

/// Cancel-safe line reader with a length bound. All state lives in the struct,
/// and bytes are consumed only after `fill_buf` returns, so dropping a pending
/// `next_line` future in `select!` loses nothing.
struct BoundedLines<R> {
    reader: BufReader<R>,
    line: Vec<u8>,
    discarding: bool,
}

impl<R: tokio::io::AsyncRead + Unpin> BoundedLines<R> {
    fn new(reader: R) -> Self {
        Self {
            reader: BufReader::new(reader),
            line: Vec::new(),
            discarding: false,
        }
    }

    /// Returns the next line that fits the bound; overlong lines are skipped.
    async fn next_line(&mut self) -> std::io::Result<Option<String>> {
        loop {
            let available = self.reader.fill_buf().await?;
            if available.is_empty() {
                if self.line.is_empty() || self.discarding {
                    return Ok(None);
                }
                let line = String::from_utf8_lossy(&std::mem::take(&mut self.line)).into_owned();
                return Ok(Some(line));
            }
            let (used, complete) = match available.iter().position(|byte| *byte == b'\n') {
                Some(index) => (index + 1, Some(index)),
                None => (available.len(), None),
            };
            if !self.discarding {
                let take = complete.unwrap_or(used);
                if self.line.len() + take > MAX_STDOUT_LINE {
                    self.discarding = true;
                    self.line.clear();
                } else {
                    self.line.extend_from_slice(&available[..take]);
                }
            }
            self.reader.consume(used);
            if complete.is_some() {
                if std::mem::take(&mut self.discarding) {
                    tracing::debug!("discarded an overlong adapter stdout line");
                    continue;
                }
                let mut line = std::mem::take(&mut self.line);
                if line.last() == Some(&b'\r') {
                    line.pop();
                }
                return Ok(Some(String::from_utf8_lossy(&line).into_owned()));
            }
        }
    }
}

/// Prefix of adapter protocol frames, followed by the per-run token.
pub const FRAME_PREFIX: &str = "BUNDLEWHY-FRAME";

pub fn cancelled_error() -> BwError {
    BwError::new(
        ErrorCode::RunCancelled,
        "The run was cancelled; results are incomplete",
        "Start a new run with `bundlewhy analyze` when you are ready.",
    )
}

fn write_adapter_files(dir: &Path) -> BwResult<()> {
    std::fs::create_dir_all(dir)
        .and_then(|()| {
            for (name, content) in ADAPTER_FILES {
                std::fs::write(dir.join(name), content)?;
            }
            Ok(())
        })
        .map_err(|error| {
            BwError::new(
                ErrorCode::OutputDirInvalid,
                format!("Could not write adapter files: {error}"),
                "Check that the output directory is writable.",
            )
        })
}

#[cfg(unix)]
fn terminate_group(pid: u32, signal: libc::c_int) {
    if let Ok(pid) = libc::pid_t::try_from(pid) {
        // SAFETY: kill(2) with a negative PID signals the process group that
        // the child leads (spawned with process_group(0)); it does not
        // touch memory.
        unsafe {
            libc::kill(-pid, signal);
        }
    }
}

/// Paths cross into the JSON adapter request; JSON strings are UTF-8, so a
/// non-UTF-8 path is rejected with guidance instead of panicking.
pub fn utf8_path(path: &Path) -> BwResult<&str> {
    path.to_str().ok_or_else(|| {
        BwError::new(
            ErrorCode::Usage,
            format!("The path {} is not valid UTF-8", path.display()),
            "Move or rename the project, configuration or output directory to a UTF-8 path, then \
             start a new run.",
        )
    })
}

/// Oldest supported Node.js: 22.18 is the first 22.x release that strips
/// TypeScript types by default, which TypeScript Rspack configurations need.
pub const MIN_NODE: (u64, u64) = (22, 18);

/// Parses `v24.1.0` into (major, minor).
pub fn parse_node_version(text: &str) -> Option<(u64, u64)> {
    let mut parts = text.trim().strip_prefix('v')?.split('.');
    Some((parts.next()?.parse().ok()?, parts.next()?.parse().ok()?))
}

/// Probes `node --version` and rejects runtimes below the documented minimum
/// before any project code runs.
async fn check_node_version(node: &Path) -> BwResult<()> {
    let probe = tokio::time::timeout(
        Duration::from_secs(15),
        tokio::process::Command::new(node)
            .arg("--version")
            .stdin(Stdio::null())
            .output(),
    )
    .await;
    let output = match probe {
        Ok(Ok(output)) => output,
        Ok(Err(error)) if error.kind() == std::io::ErrorKind::NotFound => {
            return Err(BwError::new(
                ErrorCode::NodeNotFound,
                format!("Node.js executable {} was not found", node.display()),
                "Install Node.js 22.18 or newer and make `node` available on PATH, or set \
                 BUNDLEWHY_NODE.",
            ));
        }
        Ok(Err(error)) => {
            return Err(BwError::new(
                ErrorCode::NodeNotFound,
                format!("Could not run {}: {error}", node.display()),
                "Check the Node.js installation or BUNDLEWHY_NODE.",
            ));
        }
        Err(_) => {
            return Err(BwError::new(
                ErrorCode::NodeNotFound,
                format!("{} --version did not finish", node.display()),
                "Check the Node.js installation or BUNDLEWHY_NODE.",
            ));
        }
    };
    let text = String::from_utf8_lossy(&output.stdout).into_owned();
    match parse_node_version(&text) {
        Some(version) if version >= MIN_NODE => {
            tracing::debug!(node = %text.trim(), "node version accepted");
            Ok(())
        }
        _ => Err(BwError::new(
            ErrorCode::NodeVersionUnsupported,
            format!(
                "Node.js {} is not supported; bundlewhy requires 22.18 or newer",
                text.trim()
            ),
            "Install Node.js 22.18 or newer, or point BUNDLEWHY_NODE at a supported Node.js \
             executable.",
        )),
    }
}

async fn acknowledge_protection(
    stdin: &mut tokio::process::ChildStdin,
    protocol_token: &str,
) -> std::io::Result<()> {
    stdin
        .write_all(format!("BUNDLEWHY-PROTECTION-ACK {protocol_token}\n").as_bytes())
        .await?;
    stdin.flush().await
}

/// Processes frames that the adapter already wrote, after it exited or was
/// terminated. The pipe is read until EOF but never longer than `PIPE_DRAIN`,
/// because a descendant spawned by a plugin can keep it open. Used on every
/// terminal path (success, failure, crash and cancellation) so a frame that
/// was buffered or in flight when the run ended is never lost. Returns whether
/// the time bound was hit. No acknowledgement is sent: the adapter is gone.
async fn drain_frames<R: tokio::io::AsyncRead + Unpin>(
    lines: &mut BoundedLines<R>,
    reported_error: &mut Option<BwError>,
    handle_line: &mut impl FnMut(&str, &mut Option<BwError>) -> bool,
) -> bool {
    tokio::time::timeout(PIPE_DRAIN, async {
        while let Ok(Some(line)) = lines.next_line().await {
            handle_line(&line, reported_error);
        }
    })
    .await
    .is_err()
}

/// Runs the adapter and returns its collection. `on_progress` receives stage
/// updates, `on_protection` receives the resolved project root before the
/// build starts and `on_engine` receives the detected engine identity as soon
/// as the adapter reports it, so a later failure can still name the engine.
pub async fn run(
    run_dir: &Path,
    selection: &Selection,
    options: &AdapterOptions,
    cancel: Arc<CancelSignal>,
    mut on_progress: impl FnMut(RunStage, String),
    mut on_protection: impl FnMut(PathBuf),
    mut on_engine: impl FnMut(DetectedEngine),
) -> BwResult<Collection> {
    let adapter_dir = run_dir.join("adapter");
    write_adapter_files(&adapter_dir)?;
    let spec = selection.bundler.spec();
    let collect_path = run_dir.join("collect.json");
    // Protocol frames carry this token so that JSON printed to stdout by
    // project configuration or plugins is never taken for an adapter event.
    let protocol_token = crate::server::new_token();
    let frame_prefix = format!("{FRAME_PREFIX} {protocol_token} ");
    let build_dir = run_dir.join("build");
    let request = serde_json::json!({
        "projectRoot": utf8_path(&selection.project_root)?,
        "configFile": selection.config_file.as_deref().map(utf8_path).transpose()?,
        "configName": options.config_name,
        "buildDir": utf8_path(&build_dir)?,
        "collectPath": utf8_path(&collect_path)?,
        "supportedVersion": spec.version,
        "protocolToken": protocol_token,
        "allowUnsupportedEngine": options.allow_unsupported_engine,
    });
    let request_path = run_dir.join("adapter-request.json");
    std::fs::write(&request_path, request.to_string()).map_err(|error| {
        BwError::new(
            ErrorCode::OutputDirInvalid,
            format!("Could not write adapter request: {error}"),
            "Check that the output directory is writable.",
        )
    })?;

    check_node_version(&options.node).await?;
    let mut command = tokio::process::Command::new(&options.node);
    command
        .arg(adapter_dir.join(spec.adapter_file))
        .arg(&request_path)
        .current_dir(&selection.project_root)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .kill_on_drop(true);
    #[cfg(unix)]
    command.process_group(0);
    let mut child = command.spawn().map_err(|error| {
        if error.kind() == std::io::ErrorKind::NotFound {
            BwError::new(
                ErrorCode::NodeNotFound,
                format!(
                    "Node.js executable {} was not found",
                    options.node.display()
                ),
                "Install Node.js 22.18 or newer and make `node` available on PATH, or set \
                 BUNDLEWHY_NODE.",
            )
        } else {
            BwError::new(
                ErrorCode::BuildFailed,
                format!("Could not start the adapter: {error}"),
                "Check the Node.js installation.",
            )
        }
    })?;
    let pid = child.id();
    tracing::info!(adapter = spec.adapter_file, pid, "adapter started");

    let mut stdin = child.stdin.take().expect("piped stdin");
    let stdout = child.stdout.take().expect("piped stdout");
    let mut stderr = child.stderr.take().expect("piped stderr");
    let stderr_task = tokio::spawn(async move {
        // Keep only the last 64 KiB: build tools print the actionable error
        // after preceding warnings and logs. Draining also keeps the child
        // from blocking on a full pipe.
        const TAIL_BYTES: usize = 64 * 1024;
        let mut tail: std::collections::VecDeque<u8> =
            std::collections::VecDeque::with_capacity(TAIL_BYTES);
        let mut chunk = [0u8; 8192];
        while let Ok(read) = stderr.read(&mut chunk).await {
            if read == 0 {
                break;
            }
            tail.extend(&chunk[..read]);
            let excess = tail.len().saturating_sub(TAIL_BYTES);
            tail.drain(..excess);
        }
        String::from_utf8_lossy(&Vec::from(tail)).into_owned()
    });

    let mut lines = BoundedLines::new(stdout);
    let mut reported_error: Option<BwError> = None;
    let mut cancelled = false;
    let mut exit_status: Option<std::io::Result<std::process::ExitStatus>> = None;
    // Only the first valid engine frame counts: the adapter reports the
    // engine once, so a later frame cannot replace the recorded identity.
    let mut reported_engine = false;
    let mut handle_line = |line: &str, reported_error: &mut Option<BwError>| -> bool {
        let mut protection = false;
        match line
            .strip_prefix(&frame_prefix)
            .ok_or(())
            .and_then(|frame| serde_json::from_str::<AdapterEvent>(frame).map_err(drop))
        {
            Ok(AdapterEvent::Progress { stage, message }) => {
                tracing::debug!(stage = %stage, %message, "adapter progress");
                on_progress(RunStage::from_adapter(&stage), message);
            }
            Ok(AdapterEvent::Engine { package, version }) => {
                match DetectedEngine::from_frame(spec.package, &package, &version) {
                    Some(_) if reported_engine => {
                        tracing::warn!(%package, "ignored duplicate engine frame");
                    }
                    Some(engine) => {
                        tracing::info!(%package, version = ?version, "engine detected");
                        reported_engine = true;
                        on_engine(engine);
                    }
                    None => tracing::warn!(
                        %package,
                        expected = spec.package,
                        length = version.len(),
                        "ignored engine frame with an unexpected package or invalid version"
                    ),
                }
            }
            Ok(AdapterEvent::Protection { root }) => {
                tracing::debug!(%root, "adapter resolved project root");
                on_protection(PathBuf::from(root));
                protection = true;
            }
            Ok(AdapterEvent::Error {
                code,
                message,
                recovery,
            }) => {
                tracing::debug!(%code, "adapter reported error");
                *reported_error = Some(BwError::new(
                    ErrorCode::from_adapter(&code),
                    message,
                    recovery,
                ));
            }
            Ok(AdapterEvent::Done {}) => tracing::debug!("adapter finished"),
            // Bundler plugins may print to stdout; ignore non-protocol lines.
            Err(()) => tracing::debug!(length = line.len(), "ignored non-protocol adapter output"),
        }
        protection
    };
    loop {
        tokio::select! {
            line = lines.next_line() => {
                match line {
                    Ok(Some(line)) => {
                        if handle_line(&line, &mut reported_error)
                            && acknowledge_protection(&mut stdin, &protocol_token)
                                .await
                                .is_err()
                        {
                            tracing::warn!("failed to acknowledge adapter protection snapshot");
                            break;
                        }
                    }
                    Ok(None) | Err(_) => break,
                }
            }
            // A descendant spawned by a plugin can keep the pipes open after
            // the adapter exits; the adapter's own exit ends the run.
            status = child.wait(), if exit_status.is_none() => {
                exit_status = Some(status);
                break;
            }
            () = cancel.wait() => {
                cancelled = true;
                break;
            }
        }
    }
    if cancelled {
        tracing::warn!(pid, "cancelling adapter");
        #[cfg(unix)]
        if let Some(pid) = pid {
            terminate_group(pid, libc::SIGTERM);
        }
        #[cfg(not(unix))]
        let _ = child.start_kill();
        const GRACE: Duration = Duration::from_secs(3);
        let grace_started = tokio::time::Instant::now();
        let leader_exited = tokio::time::timeout(GRACE, child.wait()).await.is_ok();
        // The adapter may exit promptly while a plugin-spawned descendant in
        // the same group ignores SIGTERM: always finish with SIGKILL for the
        // whole group after the grace period.
        #[cfg(unix)]
        if let Some(pid) = pid {
            // Descendants that honor SIGTERM get the full grace period even
            // when the adapter itself exits early.
            tokio::time::sleep_until(grace_started + GRACE).await;
            terminate_group(pid, libc::SIGKILL);
        }
        if !leader_exited {
            let _ = child.kill().await;
        }
        let _ = child.wait().await;
        // A frame the supervisor had already received (or that the adapter
        // wrote before it was terminated) still counts for the cancelled
        // result, for example the engine identity.
        if drain_frames(&mut lines, &mut reported_error, &mut handle_line).await {
            tracing::warn!("adapter output pipe still open after cancellation");
        }
        return Err(cancelled_error());
    }

    let status = match exit_status {
        Some(status) => status,
        None => child.wait().await,
    };
    // Read frames still buffered in the pipe, but never wait for EOF from
    // descendants indefinitely.
    if drain_frames(&mut lines, &mut reported_error, &mut handle_line).await {
        tracing::warn!(
            "adapter output pipe still open after exit; a build process may still be running"
        );
    }
    let status = status.map_err(|error| {
        BwError::new(
            ErrorCode::BuildFailed,
            format!("Adapter process failed: {error}"),
            "Start a new run.",
        )
    })?;
    // Bounded like stdout: a descendant may hold stderr open too.
    let stderr_text = tokio::time::timeout(PIPE_DRAIN, stderr_task)
        .await
        .ok()
        .and_then(Result::ok)
        .unwrap_or_default();
    if cancel.is_cancelled() {
        return Err(cancelled_error());
    }
    if let Some(error) = reported_error {
        return Err(error);
    }
    if !status.success() {
        let tail: String = stderr_text
            .chars()
            .rev()
            .take(2000)
            .collect::<Vec<_>>()
            .into_iter()
            .rev()
            .collect();
        return Err(BwError::new(
            ErrorCode::BuildFailed,
            format!("The adapter exited with {status}: {}", tail.trim()),
            "Fix the build error, confirm the production build succeeds in the project, then \
             start a new run.",
        ));
    }
    let bytes = std::fs::read(&collect_path).map_err(|error| {
        BwError::new(
            ErrorCode::CollectionFailed,
            format!("The adapter wrote no collection: {error}"),
            "Start a new run; report a bug if it persists.",
        )
    })?;
    let collection: Collection = serde_json::from_slice(&bytes).map_err(|error| {
        BwError::new(
            ErrorCode::CollectionFailed,
            format!("The collection is invalid: {error}"),
            "Start a new run; report a bug if it persists.",
        )
    })?;
    if collection.collect_version != COLLECT_VERSION {
        return Err(BwError::new(
            ErrorCode::CollectionFailed,
            format!(
                "Unsupported collection version {}",
                collection.collect_version
            ),
            "Reinstall bundlewhy so the binary and adapters match.",
        ));
    }
    Ok(collection)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn bounded_lines_skip_overlong_output_and_keep_frames() {
        let mut input = vec![b'x'; MAX_STDOUT_LINE + 10];
        input.extend_from_slice(b"\nframe\r\nlast");
        let mut lines = BoundedLines::new(&input[..]);
        assert_eq!(lines.next_line().await.unwrap().as_deref(), Some("frame"));
        assert_eq!(lines.next_line().await.unwrap().as_deref(), Some("last"));
        assert_eq!(lines.next_line().await.unwrap(), None);
    }

    #[cfg(unix)]
    #[test]
    fn non_utf8_paths_are_rejected_with_guidance() {
        use std::os::unix::ffi::OsStrExt;
        let path = Path::new(std::ffi::OsStr::from_bytes(b"/tmp/bad-\xff"));
        assert_eq!(utf8_path(path).unwrap_err().code, ErrorCode::Usage);
        assert_eq!(utf8_path(Path::new("/tmp/ok")).unwrap(), "/tmp/ok");
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn adapter_exit_ends_the_run_even_when_a_descendant_holds_the_pipes() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(&project).unwrap();
        let collect = dir.path().join("collect.json");
        let fake_node = dir.path().join("fake-node.sh");
        std::fs::write(
            &fake_node,
            format!(
                r#"#!/bin/sh
if [ "$1" = "--version" ]; then echo v24.0.0; exit 0; fi
token=$(sed -n 's/.*"protocolToken":"\([0-9a-f]*\)".*/\1/p' "$2")
echo '{{"collectVersion":1,"bundler":"vite"}}' > '{}'
sleep 30 &
echo "BUNDLEWHY-FRAME $token {{\"event\":\"done\"}}"
exit 0
"#,
                collect.display()
            ),
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&fake_node).unwrap().permissions();
        std::os::unix::fs::PermissionsExt::set_mode(&mut permissions, 0o755);
        std::fs::set_permissions(&fake_node, permissions).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "p".to_owned(),
            bundler: crate::engine::BundlerChoice::Vite,
            config_file: None,
        };
        let options = AdapterOptions {
            config_name: None,
            allow_unsupported_engine: false,
            node: fake_node,
        };
        let task = run(
            dir.path(),
            &selection,
            &options,
            Arc::new(CancelSignal::default()),
            |_, _| {},
            |_| {},
            |_| {},
        );
        let collection = tokio::time::timeout(Duration::from_secs(10), task)
            .await
            .expect("run must not hang")
            .unwrap();
        assert_eq!(collection.bundler, "vite");
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn protection_events_wait_for_the_supervisor_acknowledgement() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(&project).unwrap();
        let collect = dir.path().join("collect.json");
        let fake_node = dir.path().join("fake-node.sh");
        std::fs::write(
            &fake_node,
            format!(
                r#"#!/bin/sh
if [ "$1" = "--version" ]; then echo v24.0.0; exit 0; fi
token=$(sed -n 's/.*"protocolToken":"\([0-9a-f]*\)".*/\1/p' "$2")
echo "BUNDLEWHY-FRAME $token {{\"event\":\"protection\",\"root\":\"{}\"}}"
IFS= read -r acknowledgement
test "$acknowledgement" = "BUNDLEWHY-PROTECTION-ACK $token" || exit 1
echo '{{"collectVersion":1,"bundler":"vite"}}' > '{}'
echo "BUNDLEWHY-FRAME $token {{\"event\":\"done\"}}"
"#,
                project.display(),
                collect.display()
            ),
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&fake_node).unwrap().permissions();
        std::os::unix::fs::PermissionsExt::set_mode(&mut permissions, 0o755);
        std::fs::set_permissions(&fake_node, permissions).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "p".to_owned(),
            bundler: crate::engine::BundlerChoice::Vite,
            config_file: None,
        };
        let options = AdapterOptions {
            config_name: None,
            allow_unsupported_engine: false,
            node: fake_node,
        };
        let collection = run(
            dir.path(),
            &selection,
            &options,
            Arc::new(CancelSignal::default()),
            |_, _| {},
            |_| {},
            |_| {},
        )
        .await
        .unwrap();
        assert_eq!(collection.bundler, "vite");
    }

    #[test]
    fn node_versions_below_the_minimum_are_rejected() {
        assert_eq!(parse_node_version("v22.18.0\n"), Some((22, 18)));
        assert!(parse_node_version("v22.17.1").unwrap() < MIN_NODE);
        assert!(parse_node_version("v24.0.0").unwrap() >= MIN_NODE);
        assert_eq!(parse_node_version("garbage"), None);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn old_node_runtimes_fail_before_the_adapter_starts() {
        let dir = tempfile::tempdir().unwrap();
        let fake = dir.path().join("old-node.sh");
        std::fs::write(&fake, "#!/bin/sh\necho v20.11.0\n").unwrap();
        let mut permissions = std::fs::metadata(&fake).unwrap().permissions();
        std::os::unix::fs::PermissionsExt::set_mode(&mut permissions, 0o755);
        std::fs::set_permissions(&fake, permissions).unwrap();
        let error = check_node_version(&fake).await.unwrap_err();
        assert_eq!(error.code, ErrorCode::NodeVersionUnsupported);
        assert!(error.message.contains("v20.11.0"));
    }

    #[tokio::test]
    async fn cancel_signal_wakes_waiters_once_cancelled() {
        let signal = Arc::new(CancelSignal::default());
        let waiter = {
            let signal = signal.clone();
            tokio::spawn(async move { signal.wait().await })
        };
        tokio::task::yield_now().await;
        signal.cancel();
        tokio::time::timeout(Duration::from_secs(1), waiter)
            .await
            .unwrap()
            .unwrap();
        // Waiting after cancellation returns immediately.
        tokio::time::timeout(Duration::from_millis(50), signal.wait())
            .await
            .unwrap();
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn cancellation_terminates_the_adapter_process_group() {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(&project).unwrap();
        // A fake `node` that spawns a grandchild and sleeps; both must exit.
        let pid_file = dir.path().join("grandchild.pid");
        let fake_node = dir.path().join("fake-node.sh");
        std::fs::write(
            &fake_node,
            format!(
                r#"#!/bin/sh
if [ "$1" = "--version" ]; then echo v24.0.0; exit 0; fi
(trap '' TERM; exec sleep 30) &
echo $! > '{}'
token=$(sed -n 's/.*"protocolToken":"\([0-9a-f]*\)".*/\1/p' "$2")
# Unframed project output must be ignored even when it looks like an event.
echo '{{"event":"error","code":"BW_BUILD_FAILED","message":"spoofed","recovery":""}}'
echo "BUNDLEWHY-FRAME $token {{\"event\":\"progress\",\"stage\":\"building\",\"message\":\"x\"}}"
wait
"#,
                pid_file.display()
            ),
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&fake_node).unwrap().permissions();
        std::os::unix::fs::PermissionsExt::set_mode(&mut permissions, 0o755);
        std::fs::set_permissions(&fake_node, permissions).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "p".to_owned(),
            bundler: crate::engine::BundlerChoice::Vite,
            config_file: None,
        };
        let options = AdapterOptions {
            config_name: None,
            allow_unsupported_engine: false,
            node: fake_node,
        };
        let cancel = Arc::new(CancelSignal::default());
        let trigger = cancel.clone();
        let mut stages = Vec::new();
        let task = run(
            dir.path(),
            &selection,
            &options,
            cancel,
            |stage, _| {
                stages.push(stage);
                trigger.cancel();
            },
            |_| {},
            |_| {},
        );
        let error = tokio::time::timeout(Duration::from_secs(10), task)
            .await
            .unwrap()
            .unwrap_err();
        assert_eq!(error.code, ErrorCode::RunCancelled);
        assert_eq!(stages, vec![RunStage::Building]);
        tokio::time::sleep(Duration::from_millis(200)).await;
        let grandchild: i32 = std::fs::read_to_string(&pid_file)
            .unwrap()
            .trim()
            .parse()
            .unwrap();
        // SAFETY: signal 0 only checks whether the process exists.
        let alive = unsafe { libc::kill(grandchild, 0) } == 0;
        assert!(!alive, "grandchild process survived cancellation");
    }

    #[cfg(unix)]
    fn write_engine_node(dir: &Path, frames: &str) -> PathBuf {
        let fake_node = dir.join("fake-node.sh");
        std::fs::write(
            &fake_node,
            format!(
                r#"#!/bin/sh
if [ "$1" = "--version" ]; then echo v24.0.0; exit 0; fi
token=$(sed -n 's/.*"protocolToken":"\([0-9a-f]*\)".*/\1/p' "$2")
{frames}
exit 1
"#,
                frames = frames.replace("TOKEN", "$token")
            ),
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&fake_node).unwrap().permissions();
        std::os::unix::fs::PermissionsExt::set_mode(&mut permissions, 0o755);
        std::fs::set_permissions(&fake_node, permissions).unwrap();
        fake_node
    }

    #[cfg(unix)]
    async fn engines_reported_before_failure(frames: &str) -> (Vec<DetectedEngine>, ErrorCode) {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(&project).unwrap();
        let fake_node = write_engine_node(dir.path(), frames);
        let selection = Selection {
            project_root: project,
            project_name: "p".to_owned(),
            bundler: crate::engine::BundlerChoice::Vite,
            config_file: None,
        };
        let options = AdapterOptions {
            config_name: None,
            allow_unsupported_engine: false,
            node: fake_node,
        };
        let mut engines = Vec::new();
        let error = run(
            dir.path(),
            &selection,
            &options,
            Arc::new(CancelSignal::default()),
            |_, _| {},
            |_| {},
            |engine| engines.push(engine),
        )
        .await
        .unwrap_err();
        (engines, error.code)
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn engine_events_reach_the_callback_before_a_failure() {
        let (engines, code) = engines_reported_before_failure(
            r#"echo "BUNDLEWHY-FRAME TOKEN {\"event\":\"engine\",\"package\":\"vite\",\"version\":\"8.0.0\"}"
echo "BUNDLEWHY-FRAME TOKEN {\"event\":\"error\",\"code\":\"BW_ENGINE_VERSION_UNSUPPORTED\",\"message\":\"m\",\"recovery\":\"r\"}""#,
        )
        .await;
        assert_eq!(
            engines,
            vec![DetectedEngine {
                package: "vite".to_owned(),
                version: "8.0.0".to_owned(),
            }]
        );
        assert_eq!(code, ErrorCode::EngineVersionUnsupported);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn padded_engine_versions_reach_the_callback_unchanged() {
        let (engines, _) = engines_reported_before_failure(
            r#"echo "BUNDLEWHY-FRAME TOKEN {\"event\":\"engine\",\"package\":\"vite\",\"version\":\" 8.3.4 \"}""#,
        )
        .await;
        assert_eq!(
            engines,
            vec![DetectedEngine {
                package: "vite".to_owned(),
                version: " 8.3.4 ".to_owned(),
            }]
        );
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn invalid_or_foreign_engine_events_are_not_reported() {
        let (engines, _) = engines_reported_before_failure(
            r#"echo "BUNDLEWHY-FRAME TOKEN {\"event\":\"engine\",\"package\":\"webpack\",\"version\":\"5.0.0\"}"
echo "BUNDLEWHY-FRAME TOKEN {\"event\":\"engine\",\"package\":\"vite\",\"version\":\"\"}"
echo '{"event":"engine","package":"vite","version":"9.9.9"}'"#,
        )
        .await;
        assert!(engines.is_empty());
    }

    #[test]
    fn engine_frames_require_the_selected_package_and_a_bounded_version() {
        assert_eq!(
            DetectedEngine::from_frame("vite", "vite", "8.3.4"),
            Some(DetectedEngine {
                package: "vite".to_owned(),
                version: "8.3.4".to_owned(),
            })
        );
        // The reported value is preserved exactly, never trimmed.
        for padded in [" 8.3.4", "8.3.4 ", " 8.3.4 "] {
            assert_eq!(
                DetectedEngine::from_frame("vite", "vite", padded),
                Some(DetectedEngine {
                    package: "vite".to_owned(),
                    version: padded.to_owned(),
                })
            );
        }
        assert_eq!(DetectedEngine::from_frame("vite", "other", "8.3.4"), None);
        assert_eq!(DetectedEngine::from_frame("vite", "vite", "  "), None);
        assert_eq!(DetectedEngine::from_frame("vite", "vite", "1\n2"), None);
        let long = "1".repeat(MAX_ENGINE_VERSION_LEN + 1);
        assert_eq!(DetectedEngine::from_frame("vite", "vite", &long), None);
    }

    /// Shell line that writes one authenticated protocol frame.
    #[cfg(unix)]
    fn frame(json: &str) -> String {
        format!("echo 'BUNDLEWHY-FRAME '\"$token\"' {json}'")
    }

    #[cfg(unix)]
    fn engine_frame(package: &str, version: &str) -> String {
        frame(&format!(
            r#"{{"event":"engine","package":"{package}","version":"{version}"}}"#
        ))
    }

    #[cfg(unix)]
    fn error_frame() -> String {
        frame(r#"{"event":"error","code":"BW_BUILD_FAILED","message":"m","recovery":"r"}"#)
    }

    #[cfg(unix)]
    const DONE_FRAME: &str = r#"echo 'BUNDLEWHY-FRAME '"$token"' {"event":"done"}'"#;

    /// Runs a fake Node.js that executes `body` after the protocol token is
    /// known. `COLLECT` in the body is replaced with the collection path.
    /// With `cancel_on_progress`, the first progress frame cancels the run.
    #[cfg(unix)]
    async fn scripted_run(
        body: &str,
        cancel_on_progress: bool,
    ) -> (Vec<DetectedEngine>, BwResult<Collection>) {
        let dir = tempfile::tempdir().unwrap();
        let project = dir.path().join("project");
        std::fs::create_dir_all(&project).unwrap();
        let collect = dir.path().join("collect.json");
        let fake_node = dir.path().join("fake-node.sh");
        std::fs::write(
            &fake_node,
            format!(
                "#!/bin/sh\nif [ \"$1\" = \"--version\" ]; then echo v24.0.0; exit 0; \
                 fi\ntoken=$(sed -n 's/.*\"protocolToken\":\"\\([0-9a-f]*\\)\".*/\\1/p' \
                 \"$2\")\n{}\n",
                body.replace("COLLECT", &collect.display().to_string())
            ),
        )
        .unwrap();
        let mut permissions = std::fs::metadata(&fake_node).unwrap().permissions();
        std::os::unix::fs::PermissionsExt::set_mode(&mut permissions, 0o755);
        std::fs::set_permissions(&fake_node, permissions).unwrap();
        let selection = Selection {
            project_root: project,
            project_name: "p".to_owned(),
            bundler: crate::engine::BundlerChoice::Vite,
            config_file: None,
        };
        let options = AdapterOptions {
            config_name: None,
            allow_unsupported_engine: false,
            node: fake_node,
        };
        let cancel = Arc::new(CancelSignal::default());
        let trigger = cancel.clone();
        let mut engines = Vec::new();
        let task = run(
            dir.path(),
            &selection,
            &options,
            cancel,
            |_, _| {
                if cancel_on_progress {
                    trigger.cancel();
                }
            },
            |_| {},
            |engine| engines.push(engine),
        );
        let result = tokio::time::timeout(Duration::from_secs(20), task)
            .await
            .expect("run must not hang");
        (engines, result)
    }

    #[cfg(unix)]
    fn vite_engine(version: &str) -> DetectedEngine {
        DetectedEngine {
            package: "vite".to_owned(),
            version: version.to_owned(),
        }
    }

    #[cfg(unix)]
    fn code_of(result: &BwResult<Collection>) -> Option<ErrorCode> {
        result.as_ref().err().map(|error| error.code)
    }

    /// Every terminal path of a run reports the engine frame it received.
    #[cfg(unix)]
    #[tokio::test]
    async fn engine_events_are_reported_on_every_terminal_path() {
        let engine = engine_frame("vite", "8.3.4");
        let error = error_frame();
        let write_collect = r#"echo '{"collectVersion":1,"bundler":"vite"}' > 'COLLECT'"#;
        let cases: Vec<(&str, String, Option<ErrorCode>)> = vec![
            (
                "success",
                format!("{engine}\n{write_collect}\n{DONE_FRAME}\nexit 0"),
                None,
            ),
            (
                "adapter-reported failure",
                format!("{engine}\n{error}\nexit 1"),
                Some(ErrorCode::BuildFailed),
            ),
            (
                "crash with a nonzero exit",
                format!("{engine}\nexit 3"),
                Some(ErrorCode::BuildFailed),
            ),
            (
                "crash by signal",
                format!("{engine}\nkill -9 $$"),
                Some(ErrorCode::BuildFailed),
            ),
            (
                "success status without a collection",
                format!("{engine}\n{DONE_FRAME}\nexit 0"),
                Some(ErrorCode::CollectionFailed),
            ),
            (
                "failure while a descendant holds the pipes",
                format!("sleep 30 &\n{engine}\n{error}\nexit 1"),
                Some(ErrorCode::BuildFailed),
            ),
            (
                "failure after the supervisor stopped acknowledging",
                format!(
                    "exec <&-\n{}\nsleep 1\n{engine}\n{error}\nexit 1",
                    frame(r#"{"event":"protection","root":"/"}"#)
                ),
                Some(ErrorCode::BuildFailed),
            ),
        ];
        for (name, body, expected_code) in cases {
            let (engines, result) = scripted_run(&body, false).await;
            assert_eq!(engines, vec![vite_engine("8.3.4")], "{name}");
            assert_eq!(code_of(&result), expected_code, "{name}");
        }
    }

    /// Malformed, foreign, unauthenticated or blank engine frames never reach
    /// the callback, whichever way the run ends.
    #[cfg(unix)]
    #[tokio::test]
    async fn malformed_engine_events_are_ignored_on_every_terminal_path() {
        let malformed = [
            frame(r#"{"event":"engine","package":"vite"}"#),
            frame(r#"{"event":"engine","package":"vite","version":8}"#),
            frame(r#"{"event":"engine","package":"vite","version":null}"#),
            frame(r#"{"event":"engine","package":"vite","version":"   "}"#),
            frame(r#"{"event":"engine","package":"vite","version":"1\u00002"}"#),
            frame(r#"{"event":"engine","package":"vite","version":"8.3.4""#),
            engine_frame("webpack", "5.0.0"),
            "echo 'BUNDLEWHY-FRAME wrong-token \
             {\"event\":\"engine\",\"package\":\"vite\",\"version\":\"8.3.4\"}'"
                .to_owned(),
            "echo '{\"event\":\"engine\",\"package\":\"vite\",\"version\":\"8.3.4\"}'".to_owned(),
        ];
        let error = error_frame();
        for (index, line) in malformed.iter().enumerate() {
            let (engines, result) = scripted_run(&format!("{line}\n{error}\nexit 1"), false).await;
            assert!(engines.is_empty(), "failure case {index}");
            assert_eq!(code_of(&result), Some(ErrorCode::BuildFailed));
            let body = format!(
                "{line}\necho '{{\"collectVersion\":1,\"bundler\":\"vite\"}}' > \
                 'COLLECT'\n{DONE_FRAME}\nexit 0"
            );
            let (engines, result) = scripted_run(&body, false).await;
            assert!(engines.is_empty(), "success case {index}");
            assert!(result.is_ok());
        }
    }

    /// The first valid engine frame is the recorded identity; invalid frames
    /// before it and repeats after it change nothing.
    #[cfg(unix)]
    #[tokio::test]
    async fn only_the_first_valid_engine_event_is_reported() {
        let first = engine_frame("vite", "8.3.4");
        let other = engine_frame("vite", "9.0.0");
        let invalid = engine_frame("webpack", "5.0.0");
        let error = error_frame();
        let cases = [
            ("repeat", format!("{first}\n{first}"), "8.3.4"),
            ("conflicting repeat", format!("{first}\n{other}"), "8.3.4"),
            (
                "invalid then valid",
                format!("{invalid}\n{other}\n{first}"),
                "9.0.0",
            ),
        ];
        for (name, frames, expected) in cases {
            let (engines, _) = scripted_run(&format!("{frames}\n{error}\nexit 1"), false).await;
            assert_eq!(engines, vec![vite_engine(expected)], "{name}");
        }
    }

    /// A cancelled run keeps the engine frames the supervisor had already
    /// received. Both frames arrive in one write, so the second one is
    /// buffered when the first one (progress) triggers the cancellation.
    #[cfg(unix)]
    #[tokio::test]
    async fn cancellation_keeps_an_engine_event_that_was_already_buffered() {
        let progress = r#"{"event":"progress","stage":"building","message":"x"}"#;
        let engine = r#"{"event":"engine","package":"vite","version":"8.3.4"}"#;
        let body = format!(
            "printf 'BUNDLEWHY-FRAME %s {progress}\\nBUNDLEWHY-FRAME %s {engine}\\n' \"$token\" \
             \"$token\"\nsleep 30"
        );
        let (engines, result) = scripted_run(&body, true).await;
        assert_eq!(code_of(&result), Some(ErrorCode::RunCancelled));
        assert_eq!(engines, vec![vite_engine("8.3.4")]);
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn cancellation_reports_the_engine_that_preceded_it_and_invents_none() {
        let progress = frame(r#"{"event":"progress","stage":"building","message":"x"}"#);
        let engine = engine_frame("vite", "8.3.4");
        let (engines, result) =
            scripted_run(&format!("{engine}\n{progress}\nsleep 30"), true).await;
        assert_eq!(code_of(&result), Some(ErrorCode::RunCancelled));
        assert_eq!(engines, vec![vite_engine("8.3.4")]);

        let (engines, result) = scripted_run(&format!("{progress}\nsleep 30"), true).await;
        assert_eq!(code_of(&result), Some(ErrorCode::RunCancelled));
        assert!(engines.is_empty());
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn cancellation_ignores_malformed_and_duplicate_engine_events() {
        let progress = r#"{"event":"progress","stage":"building","message":"x"}"#;
        let valid = r#"{"event":"engine","package":"vite","version":"8.3.4"}"#;
        let foreign = r#"{"event":"engine","package":"webpack","version":"5.0.0"}"#;
        let other = r#"{"event":"engine","package":"vite","version":"9.0.0"}"#;
        let body = format!(
            "printf 'BUNDLEWHY-FRAME %s {progress}\\nBUNDLEWHY-FRAME %s \
             {foreign}\\nBUNDLEWHY-FRAME %s {valid}\\nBUNDLEWHY-FRAME %s \
             {other}\\nBUNDLEWHY-FRAME %s {{broken\\n' \"$token\" \"$token\" \"$token\" \
             \"$token\" \"$token\"\nsleep 30"
        );
        let (engines, result) = scripted_run(&body, true).await;
        assert_eq!(code_of(&result), Some(ErrorCode::RunCancelled));
        assert_eq!(engines, vec![vite_engine("8.3.4")]);
    }

    /// The post-exit drain handles buffered lines deterministically.
    #[tokio::test]
    async fn drain_frames_processes_every_buffered_line() {
        let mut lines = BoundedLines::new(&b"one\ntwo\r\nthree"[..]);
        let mut seen = Vec::new();
        let mut reported = None;
        let timed_out = drain_frames(&mut lines, &mut reported, &mut |line, _| {
            seen.push(line.to_owned());
            false
        })
        .await;
        assert!(!timed_out);
        assert_eq!(seen, ["one", "two", "three"]);
    }
}
