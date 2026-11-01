// SPDX-License-Identifier: Apache-2.0
//! Native ownership of a command tree, independent of the calling CLI
//! (Go `internal/process`). Callers retain their durable cache claim until
//! [`run`] or [`reconcile`] proves completion. Losing the CLI or supervisor is
//! never completion evidence.
//!
//! The calling CLI creates a private journal directory and launches a detached
//! native supervisor (this executable with [`SUPERVISOR_ARGUMENT`]). The two
//! exchange newline-delimited JSON frames over a private control channel:
//! the supervisor reports the ready scope, the CLI runs its durable `owned`
//! hook and sends the command, and the supervisor reports the finished scope
//! after the whole tree drained. Closing the channel requests cancellation.
//!
//! Journal version 2 (#4582) records a native identity that survives the
//! supervisor: the boot identity on Linux and macOS, and the supervisor's
//! creation time on Windows. Version 1 journals stay readable; incomplete ones
//! remain protected because they carry no such identity.

use std::{
    fs::{self, File},
    io::Write,
    sync::{Arc, Mutex, mpsc},
    time::{Duration, Instant},
};

use anyhow::{Result, anyhow, bail};
use serde::{Deserialize, Serialize};

use crate::{Context, ipc, jsonx, paths};

#[cfg(target_os = "linux")]
mod linux;
#[cfg(target_os = "linux")]
use linux as platform;
#[cfg(target_os = "macos")]
mod macos;
#[cfg(target_os = "macos")]
use macos as platform;
#[cfg(unix)]
mod unix;
#[cfg(windows)]
mod windows;
#[cfg(windows)]
use windows as platform;

pub const SUPERVISOR_ARGUMENT: &str = "__ccow_process_supervisor_v1";
pub const GRACE_PERIOD: Duration = Duration::from_secs(5);

/// Journal version written by this implementation.
pub const JOURNAL_VERSION: i64 = 2;

#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum ProcessError {
    #[error(
        "cannot prove the owned process tree has exited; retain the cache claim and retry recovery"
    )]
    Reconciliation,
    #[error("native process ownership is unavailable; command was not started")]
    Unavailable,
}

/// `ProcessError::Unavailable` with detail: "<unavailable>: <detail>".
pub fn infrastructure_error(detail: &str) -> anyhow::Error {
    anyhow::Error::new(ProcessError::Unavailable)
        .context(format!("{}: {detail}", ProcessError::Unavailable))
}

fn reconciliation() -> anyhow::Error {
    anyhow::Error::new(ProcessError::Reconciliation)
}

fn unavailable() -> anyhow::Error {
    anyhow::Error::new(ProcessError::Unavailable)
}

/// A resolved command. `args` includes argv\[0\]; `env` is explicit.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Command {
    /// Runs supervisor hooks without launching user code (prepare and clean).
    #[serde(rename = "Noop")]
    pub noop: bool,
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Args")]
    pub args: Vec<String>,
    #[serde(rename = "Dir")]
    pub dir: String,
    #[serde(rename = "Env")]
    pub env: Vec<String>,
}

/// Real inherited descriptors; `None` inherits the corresponding CLI stream.
#[derive(Default)]
pub struct Streams {
    pub stdin: Option<File>,
    pub stdout: Option<File>,
    pub stderr: Option<File>,
}

/// Fully populated streams (missing entries duplicated from this process).
struct ResolvedStreams {
    stdin: File,
    stdout: File,
    stderr: File,
}

fn duplicate_std(kind: u8) -> Result<File> {
    #[cfg(unix)]
    {
        use std::os::fd::AsFd;
        let owned = match kind {
            0 => std::io::stdin().as_fd().try_clone_to_owned()?,
            1 => std::io::stdout().as_fd().try_clone_to_owned()?,
            _ => std::io::stderr().as_fd().try_clone_to_owned()?,
        };
        Ok(File::from(owned))
    }
    #[cfg(windows)]
    {
        use std::os::windows::io::AsHandle;
        let owned = match kind {
            0 => std::io::stdin().as_handle().try_clone_to_owned()?,
            1 => std::io::stdout().as_handle().try_clone_to_owned()?,
            _ => std::io::stderr().as_handle().try_clone_to_owned()?,
        };
        Ok(File::from(owned))
    }
}

fn resolve_streams(s: Streams) -> Result<ResolvedStreams> {
    Ok(ResolvedStreams {
        stdin: match s.stdin {
            Some(f) => f,
            None => duplicate_std(0)?,
        },
        stdout: match s.stdout {
            Some(f) => f,
            None => duplicate_std(1)?,
        },
        stderr: match s.stderr {
            Some(f) => f,
            None => duplicate_std(2)?,
        },
    })
}

/// Process journal `scope.json`. Contains no arguments, environment or output.
#[derive(Debug, Clone, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Scope {
    /// Supervisor lifetime context; cancelled when the scope drains.
    #[serde(skip)]
    pub context: Context,
    /// The user's stderr as received by the supervisor, for notices that must
    /// reach the terminal (#4582). `None` outside the supervisor.
    #[serde(skip)]
    pub stderr: Option<Arc<File>>,
    pub version: i64,
    pub id: String,
    pub directory: String,
    pub owner_pid: i64,
    #[serde(default, skip_serializing_if = "is_zero_u64")]
    pub native_id: u64,
    /// Linux `/proc/sys/kernel/random/boot_id` or macOS `kern.bootsessionuuid`
    /// (journal version 2, #4582).
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub boot_id: String,
    /// Windows FILETIME of the supervisor's creation (journal version 2).
    #[serde(default, skip_serializing_if = "is_zero_i64")]
    pub owner_created: i64,
    pub started: bool,
    pub complete: bool,
    pub exit_code: i32,
}

fn is_zero_u64(v: &u64) -> bool {
    *v == 0
}

fn is_zero_i64(v: &i64) -> bool {
    *v == 0
}

/// Why an incomplete journal still protects its leases (#4582).
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
pub enum Retention {
    #[serde(rename = "owner running")]
    OwnerRunning,
    #[serde(rename = "incomplete journal without native identity (version 1)")]
    NoNativeIdentity,
    #[serde(rename = "same boot; descendants may survive")]
    SameBoot,
    #[serde(rename = "journal unreadable")]
    Unreadable,
}

impl Retention {
    pub const fn as_str(self) -> &'static str {
        match self {
            Self::OwnerRunning => "owner running",
            Self::NoNativeIdentity => "incomplete journal without native identity (version 1)",
            Self::SameBoot => "same boot; descendants may survive",
            Self::Unreadable => "journal unreadable",
        }
    }
}

/// Durable ownership callbacks run in the calling CLI. `owned` must durably
/// associate the scope with all held claims before returning; `drained` runs
/// only after durable whole-tree completion.
pub struct Hooks<'a> {
    pub owned: &'a mut dyn FnMut(&Scope) -> Result<()>,
    pub drained: &'a mut dyn FnMut(&Scope) -> Result<()>,
}

#[derive(Debug, Clone, Default)]
pub struct RunResult {
    pub exit_code: i32,
    pub scope: Scope,
}

/// One control-channel message. Field names match the Go encoding.
#[derive(Debug, Default, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
struct Frame {
    #[serde(rename = "Scope", default)]
    scope: Option<Scope>,
    #[serde(rename = "Command", default)]
    command: Option<Command>,
}

/// Runs a cleanup closure when dropped.
struct Defer(Option<Box<dyn FnOnce()>>);

impl Drop for Defer {
    fn drop(&mut self) {
        if let Some(f) = self.0.take() {
            f();
        }
    }
}

/// Launches the detached native supervisor and runs `command` under it.
/// Returns the exact child status separately from infrastructure errors; the
/// result's scope is populated as far as the run progressed, even on error.
pub fn run(
    ctx: &Context,
    state_root: &str,
    command: Command,
    streams: Streams,
    hooks: Hooks<'_>,
) -> (RunResult, Result<()>) {
    let mut result = RunResult {
        exit_code: 125,
        scope: Scope::default(),
    };
    let outcome = run_inner(ctx, state_root, command, streams, hooks, &mut result);
    if let Err(e) = &outcome {
        tracing::debug!(error = %e, scope_id = %result.scope.id, "process run failed");
    }
    (result, outcome)
}

fn run_inner(
    ctx: &Context,
    state_root: &str,
    mut command: Command,
    streams: Streams,
    hooks: Hooks<'_>,
    result: &mut RunResult,
) -> Result<()> {
    if !paths::is_abs(state_root) {
        bail!("absolute private process state root is required");
    }
    if !command.noop
        && (command.path.is_empty()
            || !paths::is_abs(&command.path)
            || command.args.is_empty()
            || !paths::is_abs(&command.dir))
    {
        bail!("resolved absolute executable, cwd, and argv are required");
    }
    ctx.err()?;
    let info = fs::symlink_metadata(state_root)
        .map_err(|_| anyhow!("private process state directory is required"))?;
    if !info.is_dir() || info.file_type().is_symlink() {
        bail!("private process state directory is required");
    }
    platform::private_root(state_root, &info)?;
    let id = uuid::Uuid::now_v7().to_string();
    let dir = paths::join2(state_root, &id);
    create_private_dir(&dir)?;
    let mut scope = Scope {
        version: JOURNAL_VERSION,
        id: id.clone(),
        directory: dir,
        exit_code: 125,
        ..Scope::default()
    };
    result.scope = scope.clone();
    save(&scope)?;
    let (listener, endpoint) = ipc::Listener::bind(control_prefix())?;
    let streams = resolve_streams(streams)?;
    let cleanup = match platform::launch(&scope, &endpoint, &streams) {
        Ok(c) => c,
        Err(e) => {
            tracing::debug!(error = %e, "supervisor launch failed");
            return Err(unavailable());
        }
    };
    let _cleanup = Defer(Some(cleanup));
    // Closing the listener also bounds startup when the native launcher cannot
    // start the helper.
    let (startup, _startup_cancel) = ctx.with_timeout(Duration::from_secs(15));
    let listener_closer = listener.closer();
    let _startup_guard = startup.after_func(move || listener_closer.close());
    let Ok(mut conn) = listener.accept() else {
        return Err(reconciliation());
    };
    // Cancellation must also interrupt stream transfer and command encoding.
    let conn_closer = conn.closer();
    let _cancel_guard = ctx.after_func(move || conn_closer.close());
    if send_streams(&conn, &streams).is_err() {
        return Err(reconciliation());
    }
    let _ = conn.set_deadline(Some(Instant::now() + Duration::from_secs(15)));
    let ready = match conn.recv::<Frame>(ipc::DEFAULT_LIMIT) {
        Ok(Frame { scope: Some(s), .. }) => s,
        _ => return Err(unavailable()),
    };
    let saved = match read(&scope) {
        Ok(s) if s.owner_pid == ready.owner_pid && s.owner_pid > 0 => s,
        _ => return Err(reconciliation()),
    };
    scope = saved;
    result.scope = scope.clone();
    let _ = conn.set_deadline(None);
    if let Err(e) = (hooks.owned)(&scope) {
        conn.close();
        let _ = wait_for_completion(&scope);
        return Err(e);
    }
    if let Err(cancelled) = ctx.err() {
        conn.close();
        if wait_for_completion(&scope)
            && let Ok(completed) = read(&scope)
        {
            result.scope = completed.clone();
            (hooks.drained)(&completed)?;
        }
        return Err(cancelled.into());
    }
    if conn
        .send(&Frame {
            scope: None,
            command: Some(std::mem::take(&mut command)),
        })
        .is_err()
    {
        return Err(reconciliation());
    }
    // End of stream requests cancellation but keeps the helper alive to drain.
    if conn.recv::<Frame>(ipc::DEFAULT_LIMIT).is_err() {
        // Cancellation closes control immediately; the helper independently
        // grants five seconds, escalates and writes completion. Bound the
        // caller's wait, retaining ownership if completion is not yet proven.
        if ctx.err().is_ok() {
            return Err(reconciliation());
        }
        let deadline = Instant::now() + GRACE_PERIOD + Duration::from_secs(5);
        while Instant::now() < deadline {
            if reconcile(&scope).is_ok() {
                break;
            }
            std::thread::sleep(Duration::from_millis(20));
        }
    }
    reconcile(&scope)?;
    let scope = read(&scope).map_err(|_| reconciliation())?;
    *result = RunResult {
        exit_code: scope.exit_code,
        scope: scope.clone(),
    };
    (hooks.drained)(&scope)?;
    Ok(())
}

const fn control_prefix() -> &'static str {
    if cfg!(windows) { "ccow-" } else { "ccow-p-" }
}

#[cfg(unix)]
fn send_streams(conn: &ipc::Connection, s: &ResolvedStreams) -> Result<()> {
    use std::os::fd::AsFd;
    conn.send_streams([s.stdin.as_fd(), s.stdout.as_fd(), s.stderr.as_fd()])
}

#[cfg(windows)]
fn send_streams(_conn: &ipc::Connection, _s: &ResolvedStreams) -> Result<()> {
    // Windows passes the streams as the supervisor's inherited handles.
    Ok(())
}

#[cfg(unix)]
fn receive_streams(conn: &ipc::Connection) -> Result<ResolvedStreams> {
    let [stdin, stdout, stderr] = conn.recv_streams()?;
    Ok(ResolvedStreams {
        stdin: stdin.into(),
        stdout: stdout.into(),
        stderr: stderr.into(),
    })
}

#[cfg(windows)]
fn receive_streams(_conn: &ipc::Connection) -> Result<ResolvedStreams> {
    resolve_streams(Streams::default())
}

fn create_private_dir(dir: &str) -> Result<()> {
    #[cfg(unix)]
    let builder = {
        use std::os::unix::fs::DirBuilderExt;
        let mut builder = fs::DirBuilder::new();
        builder.mode(0o700);
        builder
    };
    #[cfg(not(unix))]
    let builder = fs::DirBuilder::new();
    builder.create(dir)?;
    Ok(())
}

/// The timeout only bounds the caller's wait. It never establishes completion.
fn wait_for_completion(scope: &Scope) -> bool {
    let deadline = Instant::now() + GRACE_PERIOD + Duration::from_secs(5);
    while Instant::now() < deadline {
        if reconcile(scope).is_ok() {
            return true;
        }
        std::thread::sleep(Duration::from_millis(20));
    }
    false
}

/// Reads recovery metadata from an ownership directory in a durable lease.
pub fn load_scope(directory: &str) -> Result<Scope> {
    if !paths::is_abs(directory) || paths::clean(directory) != directory {
        return Err(reconciliation());
    }
    read(&Scope {
        id: paths::base(directory),
        directory: directory.to_string(),
        ..Scope::default()
    })
}

/// Accepts durable completion or positive native whole-tree evidence, marking
/// recovered journals complete with exit code 125. A dead PID, elapsed time or
/// unknown journal version never permits release.
pub fn reconcile(scope: &Scope) -> Result<()> {
    let mut s = match read(scope) {
        Ok(s) if s.owner_pid > 0 => s,
        _ => return Err(reconciliation()),
    };
    if !s.complete {
        if !native_completion(&s) {
            return Err(reconciliation());
        }
        // Native recovery proves the supervisor and every descendant exited.
        // No IPC worker remains to acquire resources or start another command.
        s.complete = true;
        s.exit_code = 125;
        if save(&s).is_err() {
            return Err(reconciliation());
        }
        tracing::debug!(scope_id = %s.id, "process scope recovered from native evidence");
    }
    Ok(())
}

/// Why `reconcile` refuses an incomplete journal (doctor diagnostics).
pub fn retention(scope: &Scope) -> Retention {
    match read(scope) {
        Ok(s) => platform::retention(&s),
        Err(_) => Retention::Unreadable,
    }
}

fn native_completion(s: &Scope) -> bool {
    if s.version < 2 {
        // Version 1 journals keep their original evidence rules.
        return platform::legacy_completion(s);
    }
    platform::native_completion(s)
}

/// Verifies that `pid` (from kernel-authenticated IPC) is a live descendant
/// owned by this supervisor scope, excluding the supervisor itself.
pub fn contains(scope: &Scope, pid: i64) -> bool {
    platform::contains(scope, pid)
}

/// Asks the native supervisor `owner_pid` to drain its whole session
/// (SIGTERM on Unix, CTRL_BREAK on Windows).
pub fn request_termination(owner_pid: i64) -> Result<()> {
    platform::request_termination(owner_pid)
}

/// Builds an unstarted command. Path and Dir must be absolute and Args must
/// include argv\[0\]. Windows batch wrappers run through `cmd.exe` with strict
/// argument validation.
pub fn new_exec_command(command: &Command) -> Result<std::process::Command> {
    if command.noop
        || !paths::is_abs(&command.path)
        || !paths::is_abs(&command.dir)
        || command.args.is_empty()
    {
        bail!("resolved absolute executable, cwd, and argv are required");
    }
    let mut cmd = platform::base_command(command)?;
    cmd.current_dir(&command.dir);
    cmd.env_clear();
    for entry in &command.env {
        // Windows keeps per-drive entries such as "=C:=C:\dir": the key ends at
        // the first '=' after the first character.
        if let Some(i) = entry
            .get(1..)
            .and_then(|rest| rest.find('='))
            .map(|i| i + 1)
        {
            cmd.env(&entry[..i], &entry[i + 1..]);
        }
    }
    #[cfg(windows)]
    {
        let has_root = command.env.iter().any(|e| {
            e.split_once('=')
                .is_some_and(|(k, _)| k.eq_ignore_ascii_case("SYSTEMROOT"))
        });
        if !has_root && let Ok(root) = std::env::var("SYSTEMROOT") {
            cmd.env("SYSTEMROOT", root);
        }
    }
    Ok(cmd)
}

/// Exit code of a finished child; `128 + signal` for Unix signal deaths.
pub fn exit_code(status: std::process::ExitStatus) -> i32 {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;
        if let Some(signal) = status.signal() {
            return 128 + signal;
        }
    }
    status.code().unwrap_or(125)
}

/// Go `executionCode`: 127 for missing executables, otherwise 126.
pub fn execution_code(error: &anyhow::Error) -> i32 {
    if crate::error::is_not_found(error) {
        127
    } else {
        126
    }
}

/// Supervisor-side hooks. They run inside the detached supervisor and must not
/// spawn subprocesses.
#[derive(Default)]
pub struct SupervisorHooks {
    pub ready: Option<ScopeHook>,
    pub before_start: Option<StartHook>,
    pub quiesce: Option<ScopeHook>,
    pub drained: Option<ScopeHook>,
}

/// Supervisor callback receiving the scope.
pub type ScopeHook = Box<dyn FnMut(&Scope) -> Result<()>>;
/// Supervisor callback that may adjust the command before it starts.
pub type StartHook = Box<dyn FnMut(&Scope, &mut Command) -> Result<()>>;

/// Dispatches the supervisor when this process was started as one. Must run
/// first in `main`; returns the exit code to use immediately.
pub fn supervisor_main(hooks: SupervisorHooks) -> Option<i32> {
    let args: Vec<std::ffi::OsString> = std::env::args_os().collect();
    if args.len() < 2 || args[1] != SUPERVISOR_ARGUMENT {
        return None;
    }
    if args.len() != 4 {
        return Some(125);
    }
    let (Some(dir), Some(endpoint)) = (args[2].to_str(), args[3].to_str()) else {
        return Some(125);
    };
    Some(supervise(dir, endpoint, hooks))
}

/// Native whole-tree owner for one journal.
trait Backend {
    /// Starts the root command; the receiver yields its exit code once.
    fn start(&mut self, command: std::process::Command) -> Result<mpsc::Receiver<i32>>;
    /// Signals owned descendants: graceful once per process, forced after the
    /// grace period.
    fn signal(&mut self, force: bool) -> Result<()>;
    /// Positive native evidence that no owned process remains.
    fn empty(&mut self, root_done: bool) -> Result<bool>;
}

struct Supervisor {
    scope: Scope,
    conn: ipc::Connection,
    hooks: SupervisorHooks,
    cancel: crate::context::CancelHandle,
    stop_control: Option<crate::context::AfterFunc>,
}

impl Supervisor {
    fn finish(&mut self) -> i32 {
        if let Some(stop) = self.stop_control.take() {
            stop.stop();
        }
        self.cancel.cancel();
        if let Some(quiesce) = self.hooks.quiesce.as_mut()
            && quiesce(&self.scope).is_err()
        {
            return 125;
        }
        self.scope.complete = true;
        if save(&self.scope).is_err() {
            return 125;
        }
        if let Some(drained) = self.hooks.drained.as_mut()
            && drained(&self.scope).is_err()
        {
            return 125;
        }
        let _ = self.conn.send(&Frame {
            scope: Some(self.scope.clone()),
            command: None,
        });
        0
    }
}

fn supervise(dir: &str, endpoint: &str, hooks: SupervisorHooks) -> i32 {
    let expected = Scope {
        id: paths::base(dir),
        directory: dir.to_string(),
        ..Scope::default()
    };
    let mut scope = match read(&expected) {
        Ok(s) if !s.started && !s.complete => s,
        _ => return 125,
    };
    let Ok(conn) = ipc::Connection::dial(endpoint, Duration::from_secs(10)) else {
        return 125;
    };
    let Ok(streams) = receive_streams(&conn) else {
        return 125;
    };
    // The backend records the native identity before the save that precedes
    // the caller's durable `owned` hook. Failure keeps the journal unstarted.
    let Ok(mut backend) = platform::new_backend(&mut scope) else {
        return 125;
    };
    scope.owner_pid = i64::from(std::process::id());
    tracing::debug!(scope_id = %scope.id, owner_pid = scope.owner_pid, "process scope ready");
    if save(&scope).is_err() {
        return 125;
    }
    let (ctx, cancel) = Context::background().with_cancel();
    scope.context = ctx.clone();
    scope.stderr = streams.stderr.try_clone().ok().map(Arc::new);
    let _signals = crate::signals::notify(crate::signals::SignalSet::Supervisor, cancel.clone());
    let closer = conn.closer();
    let stop_control = ctx.after_func(move || closer.close());
    let mut sup = Supervisor {
        scope,
        conn,
        hooks,
        cancel: cancel.clone(),
        stop_control: Some(stop_control),
    };
    // Ready can allocate durable ownership even if the request never arrives.
    // Every subsequent no-child exit must quiesce IPC and persist completion.
    if let Some(ready) = sup.hooks.ready.as_mut()
        && ready(&sup.scope).is_err()
    {
        return sup.finish();
    }
    if sup
        .conn
        .send(&Frame {
            scope: Some(sup.scope.clone()),
            command: None,
        })
        .is_err()
    {
        return sup.finish();
    }
    let mut request = match sup.conn.recv::<Frame>(ipc::DEFAULT_LIMIT) {
        Ok(Frame {
            command: Some(c), ..
        }) => c,
        _ => return sup.finish(),
    };
    if let Ok(mut reader) = sup.conn.try_clone() {
        let cancel = cancel.clone();
        let _ = std::thread::Builder::new()
            .name("ccow-control".into())
            .spawn(move || {
                reader.wait_closed();
                cancel.cancel();
            });
    }
    sup.scope.started = true;
    if save(&sup.scope).is_err() {
        return 125;
    }
    if let Some(before) = sup.hooks.before_start.as_mut()
        && before(&sup.scope, &mut request).is_err()
    {
        sup.scope.exit_code = 125;
        return sup.finish();
    }
    if ctx.is_done() {
        sup.scope.exit_code = 125;
        return sup.finish();
    }
    if request.noop {
        sup.scope.exit_code = 0;
        return sup.finish();
    }
    let mut cmd = match new_exec_command(&request) {
        Ok(c) => c,
        Err(e) => {
            sup.scope.exit_code = execution_code(&e);
            return sup.finish();
        }
    };
    let stdio = (|| -> Result<_> {
        Ok((
            streams.stdin.try_clone()?,
            streams.stdout.try_clone()?,
            streams.stderr.try_clone()?,
        ))
    })();
    let Ok((stdin, stdout, stderr)) = stdio else {
        sup.scope.exit_code = 125;
        return sup.finish();
    };
    cmd.stdin(stdin).stdout(stdout).stderr(stderr);
    drop(request);
    let waited = match backend.start(cmd) {
        Ok(w) => w,
        Err(e) => {
            // start proves cleanup of partial native launches itself.
            if crate::error::is(&e, &ProcessError::Reconciliation) {
                return 125;
            }
            sup.scope.exit_code = execution_code(&e);
            return sup.finish();
        }
    };
    let mut root_done = false;
    loop {
        match waited.recv_timeout(Duration::from_millis(20)) {
            Ok(code) => {
                sup.scope.exit_code = code;
                root_done = true;
                break;
            }
            Err(mpsc::RecvTimeoutError::Disconnected) => {
                root_done = true;
                break;
            }
            Err(mpsc::RecvTimeoutError::Timeout) => {
                if ctx.is_done() {
                    break;
                }
            }
        }
    }
    tracing::debug!(scope_id = %sup.scope.id, grace_seconds = 5, "process scope draining");
    let deadline = Instant::now() + GRACE_PERIOD;
    loop {
        if !root_done {
            match waited.try_recv() {
                Ok(code) => {
                    sup.scope.exit_code = code;
                    root_done = true;
                }
                Err(mpsc::TryRecvError::Disconnected) => root_done = true,
                Err(mpsc::TryRecvError::Empty) => {}
            }
        }
        if root_done && matches!(backend.empty(root_done), Ok(true)) {
            break;
        }
        // Each new descendant gets a graceful signal once. Native ownership,
        // not this enumeration, determines when the tree is empty.
        let _ = backend.signal(Instant::now() > deadline);
        std::thread::sleep(Duration::from_millis(20));
    }
    tracing::debug!(scope_id = %sup.scope.id, exit_code = sup.scope.exit_code, "process scope drained");
    drop(backend);
    sup.finish()
}

/// Test-only overrides; never set in production code paths.
static BOOT_IDENTITY_OVERRIDE: Mutex<Option<String>> = Mutex::new(None);

#[doc(hidden)]
pub mod testing {
    /// Replaces the current boot identity seen by recovery (`None` restores
    /// the native value). Only tests call this.
    pub fn set_boot_identity(value: Option<String>) {
        *super::BOOT_IDENTITY_OVERRIDE
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner) = value;
    }

    /// Writes a journal exactly as given (fixtures for recovery tests).
    pub fn save_scope(scope: &super::Scope) -> anyhow::Result<()> {
        super::save(scope)
    }

    /// The native boot identity of this host (Linux and macOS).
    pub fn current_boot_identity() -> anyhow::Result<String> {
        super::current_boot_identity()
    }
}

/// Current boot identity, honoring the test override.
#[cfg_attr(
    windows,
    allow(dead_code, reason = "Windows has no user-mode boot identity")
)]
fn current_boot_identity() -> Result<String> {
    if let Some(v) = BOOT_IDENTITY_OVERRIDE
        .lock()
        .unwrap_or_else(std::sync::PoisonError::into_inner)
        .clone()
    {
        return Ok(v);
    }
    platform::boot_identity()
}

fn read(expected: &Scope) -> Result<Scope> {
    let bytes = fs::read(paths::join2(&expected.directory, "scope.json"))?;
    let s: Scope = jsonx::from_slice_strict(&bytes)?;
    if !(s.version == 1 || s.version == JOURNAL_VERSION)
        || s.id != expected.id
        || s.directory != expected.directory
    {
        return Err(reconciliation());
    }
    if s.version == 1 && (!s.boot_id.is_empty() || s.owner_created != 0) {
        return Err(reconciliation());
    }
    Ok(s)
}

fn save(s: &Scope) -> Result<()> {
    let bytes = serde_json::to_vec(s)?;
    let temp = paths::join2(&s.directory, &format!(".scope-{}", uuid::Uuid::now_v7()));
    let write = || -> Result<()> {
        let mut options = fs::OpenOptions::new();
        options.write(true).create_new(true);
        #[cfg(unix)]
        {
            use std::os::unix::fs::OpenOptionsExt;
            options.mode(0o600);
        }
        let mut f = options.open(&temp)?;
        f.write_all(&bytes)?;
        f.sync_all()?;
        drop(f);
        fs::rename(&temp, paths::join2(&s.directory, "scope.json"))?;
        platform::sync_directory(&s.directory)?;
        Ok(())
    };
    let outcome = write();
    if outcome.is_err() {
        let _ = fs::remove_file(&temp);
    }
    outcome
}

#[cfg(test)]
mod tests {
    use super::*;

    fn fixture(dir: &tempfile::TempDir) -> Scope {
        let directory = paths::eval_symlinks(dir.path().to_str().unwrap()).unwrap();
        Scope {
            version: 1,
            id: paths::base(&directory),
            directory,
            owner_pid: 2_147_483_647,
            started: true,
            ..Scope::default()
        }
    }

    #[test]
    fn reconcile_requires_durable_completion() {
        let dir = tempfile::tempdir().unwrap();
        let mut s = fixture(&dir);
        save(&s).unwrap();
        assert!(crate::error::is(
            &reconcile(&s).unwrap_err(),
            &ProcessError::Reconciliation
        ));
        let loaded = load_scope(&s.directory).unwrap();
        assert_eq!(loaded.id, s.id);
        assert!(crate::error::is(
            &load_scope("relative").unwrap_err(),
            &ProcessError::Reconciliation
        ));
        s.complete = true;
        save(&s).unwrap();
        reconcile(&s).unwrap();
        s.version = 3;
        save(&s).unwrap();
        assert!(crate::error::is(
            &reconcile(&s).unwrap_err(),
            &ProcessError::Reconciliation
        ));
        let raw = fs::read_to_string(paths::join2(&s.directory, "scope.json")).unwrap();
        assert!(raw.contains("\"version\":3"), "unknown journal rewritten");
    }

    #[test]
    fn no_child_completion_and_unknown_native_ownership() {
        let dir = tempfile::tempdir().unwrap();
        let mut s = fixture(&dir);
        s.started = false;
        save(&s).unwrap();
        assert!(
            reconcile(&s).is_err(),
            "absent native identity proved completion"
        );
        assert_eq!(retention(&s), Retention::NoNativeIdentity);
        s.complete = true;
        save(&s).unwrap();
        reconcile(&s).unwrap();
    }

    #[test]
    fn rejects_unknown_fields_and_trailing_data() {
        let dir = tempfile::tempdir().unwrap();
        let s = fixture(&dir);
        save(&s).unwrap();
        let path = paths::join2(&s.directory, "scope.json");
        let raw = fs::read_to_string(&path).unwrap();
        fs::write(&path, raw.replace("\"started\"", "\"extra\":1,\"started\"")).unwrap();
        assert!(read(&s).is_err());
        fs::write(&path, format!("{raw}{{}}")).unwrap();
        assert!(read(&s).is_err());
        let v1_with_identity = raw.replace("\"started\"", "\"boot_id\":\"x\",\"started\"");
        fs::write(&path, v1_with_identity).unwrap();
        assert!(read(&s).is_err(), "version 1 journals carry no identity");
        assert_eq!(retention(&s), Retention::Unreadable);
    }

    #[test]
    fn exec_command_preserves_invocation_on_unix() {
        if !cfg!(unix) {
            return;
        }
        let dir = tempfile::tempdir().unwrap();
        let dir = paths::eval_symlinks(dir.path().to_str().unwrap()).unwrap();
        let c = Command {
            path: "/bin/sh".into(),
            args: vec![
                "custom-argv0".into(),
                "-c".into(),
                r#"printf '%s|%s|%s' "$0" "$PWD" "${HOME-unset}""#.into(),
            ],
            dir: dir.clone(),
            ..Command::default()
        };
        let out = new_exec_command(&c).unwrap().output().unwrap();
        assert_eq!(
            String::from_utf8(out.stdout).unwrap(),
            format!("custom-argv0|{dir}|unset")
        );
        assert!(new_exec_command(&Command { noop: true, ..c }).is_err());
    }

    #[test]
    fn execution_codes() {
        let missing = anyhow::Error::new(std::io::Error::from(std::io::ErrorKind::NotFound));
        assert_eq!(execution_code(&missing), 127);
        assert_eq!(execution_code(&anyhow!("denied")), 126);
    }
}

#[cfg(all(test, any(target_os = "linux", target_os = "macos")))]
mod boot_tests {
    use super::*;

    // One test owns the process-global override to avoid interference.
    #[test]
    fn version_two_boot_identity_recovery() {
        let current = current_boot_identity().expect("native boot identity");
        assert!(!current.is_empty());
        let dir = tempfile::tempdir().unwrap();
        let directory = paths::eval_symlinks(dir.path().to_str().unwrap()).unwrap();
        let base = Scope {
            version: 2,
            id: paths::base(&directory),
            directory,
            owner_pid: 1,
            started: true,
            exit_code: 125,
            ..Scope::default()
        };

        // Same boot: retained, file unchanged.
        let same = Scope {
            boot_id: current.clone(),
            ..base.clone()
        };
        save(&same).unwrap();
        let before = fs::read(paths::join2(&same.directory, "scope.json")).unwrap();
        if cfg!(target_os = "linux") {
            assert!(crate::error::is(
                &reconcile(&same).unwrap_err(),
                &ProcessError::Reconciliation
            ));
            assert_eq!(
                fs::read(paths::join2(&same.directory, "scope.json")).unwrap(),
                before
            );
        }

        // Empty identity: retained even across a different boot.
        let empty = Scope {
            boot_id: String::new(),
            ..base.clone()
        };
        save(&empty).unwrap();
        testing::set_boot_identity(Some("11111111-1111-1111-1111-111111111111".into()));
        assert!(reconcile(&empty).is_err());
        assert_eq!(retention(&empty), Retention::NoNativeIdentity);

        // Version 1 never recovers from a boot change.
        let v1 = Scope {
            version: 1,
            boot_id: String::new(),
            ..base.clone()
        };
        save(&v1).unwrap();
        assert!(reconcile(&v1).is_err());

        // Different boot: recovered and marked complete with 125.
        let old = Scope {
            boot_id: "00000000-0000-0000-0000-000000000000".into(),
            ..base.clone()
        };
        save(&old).unwrap();
        reconcile(&old).unwrap();
        let recovered = load_scope(&old.directory).unwrap();
        assert!(recovered.complete);
        assert_eq!(recovered.exit_code, 125);
        testing::set_boot_identity(None);
    }
}
