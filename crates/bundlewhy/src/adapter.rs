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
    io::{AsyncBufReadExt, AsyncReadExt, BufReader},
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
    Error {
        code: String,
        message: String,
        recovery: String,
    },
    Done {},
}

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

/// Runs the adapter and returns its collection. `on_progress` receives stage
/// updates.
pub async fn run(
    run_dir: &Path,
    selection: &Selection,
    options: &AdapterOptions,
    cancel: Arc<CancelSignal>,
    mut on_progress: impl FnMut(RunStage, String),
) -> BwResult<Collection> {
    let adapter_dir = run_dir.join("adapter");
    write_adapter_files(&adapter_dir)?;
    let spec = selection.bundler.spec();
    let collect_path = run_dir.join("collect.json");
    // Protocol frames carry this token so that JSON printed to stdout by
    // project configuration or plugins is never taken for an adapter event.
    let protocol_token = crate::server::new_token();
    let frame_prefix = format!("{FRAME_PREFIX} {protocol_token} ");
    let request = serde_json::json!({
        "projectRoot": selection.project_root,
        "configFile": selection.config_file,
        "configName": options.config_name,
        "buildDir": run_dir.join("build"),
        "collectPath": collect_path,
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
        .stdin(Stdio::null())
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
    loop {
        tokio::select! {
            line = lines.next_line() => {
                match line {
                    Ok(Some(line)) => match line
                        .strip_prefix(&frame_prefix)
                        .ok_or(())
                        .and_then(|frame| serde_json::from_str::<AdapterEvent>(frame).map_err(drop))
                    {
                        Ok(AdapterEvent::Progress { stage, message }) => {
                            tracing::debug!(stage = %stage, %message, "adapter progress");
                            on_progress(RunStage::from_adapter(&stage), message);
                        }
                        Ok(AdapterEvent::Engine { package, version }) => {
                            tracing::info!(%package, %version, "engine detected");
                        }
                        Ok(AdapterEvent::Error { code, message, recovery }) => {
                            tracing::debug!(%code, "adapter reported error");
                            reported_error = Some(BwError::new(ErrorCode::from_adapter(&code), message, recovery));
                        }
                        Ok(AdapterEvent::Done {}) => tracing::debug!("adapter finished"),
                        // Bundler plugins may print to stdout; ignore non-protocol lines.
                        Err(_) => tracing::debug!(length = line.len(), "ignored non-protocol adapter output"),
                    },
                    Ok(None) | Err(_) => break,
                }
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
        let leader_exited = tokio::time::timeout(Duration::from_secs(3), child.wait())
            .await
            .is_ok();
        // The adapter may exit promptly while a plugin-spawned descendant in
        // the same group ignores SIGTERM: always finish with SIGKILL for the
        // whole group after the grace period.
        #[cfg(unix)]
        if let Some(pid) = pid {
            if leader_exited {
                tokio::time::sleep(Duration::from_millis(200)).await;
            }
            terminate_group(pid, libc::SIGKILL);
        }
        if !leader_exited {
            let _ = child.kill().await;
        }
        let _ = child.wait().await;
        return Err(cancelled_error());
    }

    let status = child.wait().await.map_err(|error| {
        BwError::new(
            ErrorCode::BuildFailed,
            format!("Adapter process failed: {error}"),
            "Start a new run.",
        )
    })?;
    let stderr_text = stderr_task.await.unwrap_or_default();
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
        let task = run(dir.path(), &selection, &options, cancel, |stage, _| {
            stages.push(stage);
            trigger.cancel();
        });
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
}
