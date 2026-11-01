// SPDX-License-Identifier: Apache-2.0
//! Native ownership of a command tree, independent of the calling CLI
//! (Go `internal/process`). Callers retain their durable cache claim until
//! [`run`] or [`reconcile`] proves completion. Losing the CLI or supervisor is
//! never completion evidence.

use std::{fs::File, sync::Arc, time::Duration};

use anyhow::Result;
use serde::{Deserialize, Serialize};

use crate::Context;

pub const SUPERVISOR_ARGUMENT: &str = "__ccow_process_supervisor_v1";
pub const GRACE_PERIOD: Duration = Duration::from_secs(5);

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
    let _ = (ctx, state_root, command, streams, hooks);
    todo!("process::run")
}

/// Reads recovery metadata from an ownership directory in a durable lease.
pub fn load_scope(directory: &str) -> Result<Scope> {
    let _ = directory;
    todo!("process::load_scope")
}

/// Accepts durable completion or positive native whole-tree evidence, marking
/// recovered journals complete with exit code 125. A dead PID, elapsed time or
/// unknown journal version never permits release.
pub fn reconcile(scope: &Scope) -> Result<()> {
    let _ = scope;
    todo!("process::reconcile")
}

/// Why `reconcile` refuses an incomplete journal (doctor diagnostics).
pub fn retention(scope: &Scope) -> Retention {
    let _ = scope;
    todo!("process::retention")
}

/// Verifies that `pid` (from kernel-authenticated IPC) is a live descendant
/// owned by this supervisor scope, excluding the supervisor itself.
pub fn contains(scope: &Scope, pid: i64) -> bool {
    let _ = (scope, pid);
    todo!("process::contains")
}

/// Asks the native supervisor `owner_pid` to drain its whole session
/// (SIGTERM on Unix, CTRL_BREAK on Windows).
pub fn request_termination(owner_pid: i64) -> Result<()> {
    let _ = owner_pid;
    todo!("process::request_termination")
}

/// Builds an unstarted command. Path and Dir must be absolute and Args must
/// include argv\[0\]. Windows batch wrappers run through `cmd.exe` with strict
/// argument validation.
pub fn new_exec_command(command: &Command) -> Result<std::process::Command> {
    let _ = command;
    todo!("process::new_exec_command")
}

/// Exit code of a finished child; `128 + signal` for Unix signal deaths.
pub fn exit_code(status: std::process::ExitStatus) -> i32 {
    let _ = status;
    todo!("process::exit_code")
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
    pub ready: Option<Box<dyn FnMut(&Scope) -> Result<()>>>,
    pub before_start: Option<Box<dyn FnMut(&Scope, &mut Command) -> Result<()>>>,
    pub quiesce: Option<Box<dyn FnMut(&Scope) -> Result<()>>>,
    pub drained: Option<Box<dyn FnMut(&Scope) -> Result<()>>>,
}

/// Dispatches the supervisor when this process was started as one. Must run
/// first in `main`; returns the exit code to use immediately.
pub fn supervisor_main(hooks: SupervisorHooks) -> Option<i32> {
    let _ = hooks;
    todo!("process::supervisor_main")
}
