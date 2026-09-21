use std::{collections::BTreeMap, path::Path, process::Stdio, time::Duration};

use anyhow::{ensure, Context, Result};
use tokio::{
    io::{AsyncRead, AsyncReadExt},
    process::Child,
};
use tokio_util::sync::CancellationToken;

use crate::config::Command;

tokio::task_local! {
    pub static CANCELLATION: CancellationToken;
    pub(crate) static DEADLINE: Option<tokio::time::Instant>;
}

pub(crate) fn remaining_timeout(limit: Option<Duration>) -> Option<Duration> {
    let remaining = DEADLINE
        .try_with(|deadline| {
            deadline.map(|d| d.saturating_duration_since(tokio::time::Instant::now()))
        })
        .ok()
        .flatten();
    match (limit, remaining) {
        (Some(limit), Some(remaining)) => Some(limit.min(remaining)),
        (limit, remaining) => limit.or(remaining),
    }
}

#[derive(Debug)]
pub(crate) struct TimedOut;
impl std::fmt::Display for TimedOut {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("task timeout elapsed")
    }
}
impl std::error::Error for TimedOut {}

pub struct OwnedProcess {
    pub child: Child,
    pid: u32,
    cleaned: bool,
    #[cfg(unix)]
    supervisor: Option<tempfile::TempDir>,
    #[cfg(unix)]
    lease: Option<std::os::unix::net::UnixStream>,
    #[cfg(windows)]
    job: usize,
    #[cfg(windows)]
    job_cleanup: windows::Cleanup,
}

impl OwnedProcess {
    pub fn spawn(
        directory: &Path,
        command: &Command,
        shell: Option<&[String]>,
        environment: Option<&BTreeMap<String, String>>,
    ) -> Result<Self> {
        let args = argv(command, shell);
        #[cfg(unix)]
        let envelope = unix_owner::environment_envelope(environment)?;
        #[cfg(unix)]
        let prepared = unix_owner::prepare()?;
        #[cfg(unix)]
        let (lease, control) = std::os::unix::net::UnixStream::pair()?;
        #[cfg(unix)]
        lease.set_write_timeout(Some(Duration::from_secs(10)))?;
        #[cfg(unix)]
        let mut builder = {
            let mut command = tokio::process::Command::new(&prepared.executable);
            command.arg("run").arg(prepared.scope.path()).args(&args);
            command
                .stdin(Stdio::from(std::os::fd::OwnedFd::from(control)))
                .kill_on_drop(false)
                .env_clear()
                .env("PATH", "/usr/bin:/bin")
                .env("LC_ALL", "C");
            command
        };
        #[cfg(windows)]
        let mut builder = {
            let mut command = tokio::process::Command::new(&args[0]);
            command
                .args(&args[1..])
                .stdin(Stdio::null())
                .kill_on_drop(true);
            command
        };
        builder
            .current_dir(directory)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped());
        #[cfg(windows)]
        if let Some(environment) = environment {
            builder.env_clear().envs(environment);
        }
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            builder.as_std_mut().process_group(0);
        }
        #[cfg(windows)]
        {
            // Suspend the initial thread before assigning its Job Object. Without
            // this boundary an immediately spawned grandchild can escape ownership.
            builder.creation_flags(
                windows_sys::Win32::System::Threading::CREATE_SUSPENDED
                    | windows_sys::Win32::System::Threading::CREATE_NEW_PROCESS_GROUP,
            );
        }
        let mut child = builder.spawn().map_err(|error| {
            // Preserve safe OS diagnostics even when a caller logs only the
            // outer error. Never include argv or environment values here.
            let kind = error.kind();
            let os_error = error.raw_os_error();
            tracing::error!(
                ?kind,
                ?os_error,
                phase = "spawn",
                "Owned process launch failed"
            );
            anyhow::Error::new(error).context(format!(
                "failed to spawn command ({kind:?}, OS error {os_error:?})"
            ))
        })?;
        let _ = &mut child;
        let pid = child.id().context("spawned child has no process ID")?;
        let mut owned = Self {
            child,
            pid,
            cleaned: false,
            #[cfg(unix)]
            supervisor: Some(prepared.scope),
            #[cfg(unix)]
            lease: Some(lease),
            #[cfg(windows)]
            job: 0,
            #[cfg(windows)]
            job_cleanup: windows::Cleanup::default(),
        };
        #[cfg(unix)]
        {
            use std::io::Write;
            // Task loader variables must never reach the supervisor's own
            // exec. Send them as private bytes, then retain this separate EOF
            // lease (Tokio Child::wait closes a ChildStdin automatically).
            owned
                .lease
                .as_mut()
                .unwrap()
                .write_all(&envelope)
                .context("send private command environment")?;
        }
        #[cfg(windows)]
        {
            owned.job = windows::attach_and_resume(&owned.child, pid)?;
        }
        // Keep this binding mutable on Windows without platform-specific signatures.
        let _ = &mut owned;
        Ok(owned)
    }

    pub async fn wait(
        &mut self,
        cancel: &CancellationToken,
        timeout: Option<Duration>,
    ) -> Result<ProcessExit> {
        let deadline = tokio::time::sleep(timeout.unwrap_or(Duration::from_secs(365 * 86400)));
        tokio::pin!(deadline);
        let (status, reason) = tokio::select! {
            biased;
            _ = cancel.cancelled() => (None, ExitReason::Cancelled),
            result = self.child.wait() => (Some(result?), ExitReason::Completed),
            _ = &mut deadline => (None, ExitReason::TimedOut),
        };
        if let Some(status) = status {
            #[cfg(unix)]
            self.verify_cleanup(status)?;
            #[cfg(windows)]
            self.kill_tree().await?;
            Ok(ProcessExit {
                code: status.code().unwrap_or(1),
                reason,
            })
        } else {
            self.terminate().await?;
            Ok(ProcessExit {
                code: if reason == ExitReason::TimedOut {
                    124
                } else {
                    130
                },
                reason,
            })
        }
    }

    pub async fn terminate(&mut self) -> Result<()> {
        if self.cleaned {
            return Ok(());
        }
        #[cfg(unix)]
        {
            // Closing this lease, including when the caller dies, asks the
            // independent supervisor to terminate and prove its domain empty.
            drop(self.lease.take());
            let status = self.child.wait().await.context(CleanupFailure)?;
            self.verify_cleanup(status)?;
        }
        #[cfg(windows)]
        if self.job != 0 {
            self.job_cleanup.prepare(self.job)?;
        }
        #[cfg(windows)]
        unsafe {
            windows_sys::Win32::System::Console::GenerateConsoleCtrlEvent(
                windows_sys::Win32::System::Console::CTRL_BREAK_EVENT,
                self.pid,
            );
        }
        #[cfg(windows)]
        {
            let _ = tokio::time::timeout(Duration::from_secs(2), self.child.wait()).await;
            self.kill_tree().await?;
        }
        tracing::debug!(
            pid = self.pid,
            outcome = "reaped",
            "Owned process tree cleanup completed"
        );
        Ok(())
    }

    #[cfg(unix)]
    fn verify_cleanup(&mut self, status: std::process::ExitStatus) -> Result<()> {
        let valid = self.supervisor.as_ref().is_some_and(|scope| {
            status.code().is_some_and(|code| {
                std::fs::read_to_string(scope.path().join("complete")).ok()
                    == Some(format!("TFLOW_OWNER_V1 {code}\n"))
            })
        });
        if !valid {
            unix_owner::poison();
            return Err(CleanupFailure.into());
        }
        self.cleaned = true;
        tracing::debug!(
            pid = self.pid,
            outcome = "kernel-domain-empty",
            "Owned process cleanup verified"
        );
        Ok(())
    }

    #[cfg(windows)]
    async fn kill_tree(&mut self) -> Result<()> {
        if self.cleaned {
            return Ok(());
        }
        if self.job != 0 {
            self.job_cleanup.prepare(self.job)?;
            windows::terminate_job(self.job)?;
            let deadline = tokio::time::Instant::now() + windows::CLEANUP_TIMEOUT;
            while windows::active_processes(self.job)? != 0 || !self.job_cleanup.exited()? {
                if tokio::time::Instant::now() >= deadline {
                    return Err(CleanupFailure.into());
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        } else {
            // Attachment failed while the initial thread was still suspended;
            // only that unstarted direct process can exist outside a job.
            self.child.start_kill().context(CleanupFailure)?;
        }
        self.child.wait().await.context(CleanupFailure)?;
        self.cleaned = true;
        tracing::debug!(
            pid = self.pid,
            outcome = "job-empty-processes-signaled",
            "Owned process cleanup verified"
        );
        Ok(())
    }

    #[cfg(windows)]
    fn cleanup_on_drop(&mut self) -> Result<()> {
        if self.cleaned {
            return Ok(());
        }
        if self.job != 0 {
            self.job_cleanup.prepare(self.job)?;
            windows::terminate_job(self.job)?;
        } else {
            self.child.start_kill().context(CleanupFailure)?;
        }
        let deadline = std::time::Instant::now() + windows::CLEANUP_TIMEOUT;
        loop {
            let empty = self.job == 0
                || (windows::active_processes(self.job)? == 0 && self.job_cleanup.exited()?);
            if empty && self.child.try_wait().context(CleanupFailure)?.is_some() {
                self.cleaned = true;
                return Ok(());
            }
            if std::time::Instant::now() >= deadline {
                return Err(CleanupFailure.into());
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}
impl Drop for OwnedProcess {
    fn drop(&mut self) {
        #[cfg(unix)]
        if !self.cleaned {
            drop(self.lease.take());
            // Drop cannot await Tokio, but it still retains ownership until the
            // helper acknowledges cleanup. On exceptional helper failure keep
            // its private journal and reject further launches in this process.
            loop {
                match self.child.try_wait() {
                    Ok(Some(status)) => {
                        let _ = self.verify_cleanup(status);
                        break;
                    }
                    Ok(None) => std::thread::sleep(Duration::from_millis(10)),
                    Err(_) => {
                        unix_owner::poison();
                        break;
                    }
                }
            }
            if !self.cleaned {
                if let Some(scope) = self.supervisor.take() {
                    let _ = scope.keep();
                }
                tracing::error!(
                    pid = self.pid,
                    "Process ownership cleanup could not be verified; further launches disabled"
                );
            }
        }
        #[cfg(windows)]
        if let Err(error) = self.cleanup_on_drop() {
            // Keep kill-on-close as a final fallback, but never claim that an
            // earlier failed termination or accounting query proved completion.
            tracing::error!(pid = self.pid, error = %error, "Windows process ownership cleanup could not be verified");
        }
        #[cfg(windows)]
        if self.job != 0 {
            unsafe {
                windows_sys::Win32::Foundation::CloseHandle(self.job as _);
            }
        }
    }
}
#[derive(Debug, Clone, Copy)]
pub struct ProcessExit {
    pub code: i32,
    pub reason: ExitReason,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ExitReason {
    Completed,
    Cancelled,
    TimedOut,
}
impl ProcessExit {
    pub fn cancelled(self) -> bool {
        self.reason == ExitReason::Cancelled
    }
}

pub fn argv(command: &Command, shell: Option<&[String]>) -> Vec<String> {
    match command {
        Command::Argv(args) => {
            #[cfg(windows)]
            if args
                .first()
                .is_some_and(|p| matches!(p.as_str(), "pnpm" | "npm" | "npx"))
            {
                // Rust's Windows Command implementation owns the special batch
                // quoting rules. Do not construct a cmd expression from argv:
                // shell interpolation would corrupt literal %, &, and quotes.
                let mut args = args.clone();
                args[0].push_str(".cmd");
                return args;
            }
            args.clone()
        }
        Command::Shell(expression) => {
            let mut args = shell.map(<[String]>::to_vec).unwrap_or_else(|| {
                if cfg!(windows) {
                    vec!["cmd.exe".into(), "/D".into(), "/S".into(), "/C".into()]
                } else {
                    vec!["/bin/sh".into(), "-c".into()]
                }
            });
            args.push(expression.clone());
            args
        }
    }
}

pub async fn capture(
    directory: &Path,
    command: &Command,
    environment: &[(&str, &str)],
) -> Result<Vec<u8>> {
    let mut env = crate::environment::inherited()?;
    for (key, value) in environment {
        crate::environment::insert(&mut env, (*key).into(), (*value).into());
    }
    let cancel = CANCELLATION.try_with(Clone::clone).unwrap_or_default();
    capture_with_env(directory, command, &env, &cancel).await
}
pub async fn capture_with_env(
    directory: &Path,
    command: &Command,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    capture_with_shell(directory, command, None, environment, cancel).await
}

pub(crate) async fn capture_with_shell(
    directory: &Path,
    command: &Command,
    shell: Option<&[String]>,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    Ok(
        capture_output(directory, command, shell, environment, cancel)
            .await?
            .stdout,
    )
}

struct CapturedOutput {
    stdout: Vec<u8>,
    stderr: Vec<u8>,
}

async fn capture_output(
    directory: &Path,
    command: &Command,
    shell: Option<&[String]>,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<CapturedOutput> {
    let (output, status) = capture_owned(directory, command, shell, environment, cancel).await?;
    if status.cancelled() {
        return Err(Cancelled.into());
    }
    if status.reason == ExitReason::TimedOut {
        return Err(TimedOut.into());
    }
    ensure!(
        status.code == 0,
        "native command failed (exit {})",
        status.code
    );
    Ok(output)
}

/// A completed ownership cleanup whose command was cancelled by its operator.
#[derive(Debug)]
pub struct Cancelled;
impl std::fmt::Display for Cancelled {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("command cancelled")
    }
}
impl std::error::Error for Cancelled {}

pub(crate) fn check_cancelled(cancel: &CancellationToken) -> Result<()> {
    if cancel.is_cancelled() {
        return Err(Cancelled.into());
    }
    Ok(())
}

/// Preserve typed execution failures at public CLI and session boundaries.
pub fn error_exit_code(error: &anyhow::Error) -> i32 {
    if error.is::<CleanupFailure>()
        || error.is::<crate::docker::CleanupFailure>()
        || error.is::<crate::cache::PublicationRollbackFailure>()
    {
        1
    } else if error.is::<Cancelled>() {
        130
    } else if error.is::<TimedOut>() {
        124
    } else {
        1
    }
}

pub(crate) fn aborts_discovery(error: &anyhow::Error) -> bool {
    error.is::<Cancelled>() || error.is::<CleanupFailure>()
}

#[derive(Debug)]
pub(crate) struct CleanupFailure;
impl std::fmt::Display for CleanupFailure {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str("native command owners could not be completed")
    }
}
impl std::error::Error for CleanupFailure {}

pub(crate) async fn readiness_command(
    directory: &Path,
    command: &Command,
    shell: Option<&[String]>,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<bool> {
    let (_, status) = capture_owned(directory, command, shell, environment, cancel).await?;
    Ok(status.code == 0 && !status.cancelled())
}

async fn capture_owned(
    directory: &Path,
    command: &Command,
    shell: Option<&[String]>,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<(CapturedOutput, ProcessExit)> {
    if cancel.is_cancelled() {
        return Err(Cancelled.into());
    }
    if remaining_timeout(None).is_some_and(|remaining| remaining.is_zero()) {
        return Err(TimedOut.into());
    }
    let mut child = OwnedProcess::spawn(directory, command, shell, Some(environment))?;
    let stdout = child.child.stdout.take().unwrap();
    let stderr = child.child.stderr.take().unwrap();
    let out = tokio::spawn(read_bounded(stdout));
    let err = tokio::spawn(read_bounded(stderr));
    let status = child
        .wait(cancel, remaining_timeout(Some(Duration::from_secs(120))))
        .await;
    let cleanup = if status.is_err() {
        child.terminate().await
    } else {
        Ok(())
    };
    // Release the owner's final kill-on-close fallback before waiting for pipe
    // EOF when an explicit Windows job cleanup attempt failed.
    drop(child);
    // Await every owner before propagating a failure, including cancellation.
    let output = out.await;
    let errors = err.await;
    cleanup.context(CleanupFailure)?;
    let status = status.context(CleanupFailure)?;
    let output = output.context(CleanupFailure)?.context(CleanupFailure)?;
    let errors = errors.context(CleanupFailure)?.context(CleanupFailure)?;
    Ok((
        CapturedOutput {
            stdout: output,
            stderr: errors,
        },
        status,
    ))
}

pub(crate) async fn capture_task_process(
    root: &Path,
    directory: &Path,
    task: &crate::config::Task,
    command: &Command,
    environment: &BTreeMap<String, String>,
    overrides: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<(Vec<u8>, ProcessExit)> {
    if task.platform.executor == crate::config::Executor::Host {
        let (output, status) = capture_owned(
            directory,
            command,
            task.shell.as_deref(),
            environment,
            cancel,
        )
        .await?;
        return Ok((output.stdout, status));
    }
    let mut probe = task.clone();
    probe.command = command.clone();
    probe.platform.ports.clear();
    let (command, mut container) = crate::docker::prepare(
        root,
        directory,
        &probe,
        environment,
        overrides,
        &uuid::Uuid::now_v7().to_string(),
        cancel,
    )
    .await?;
    let result = capture_owned(
        directory,
        &command,
        None,
        container.host_environment(),
        cancel,
    )
    .await;
    let cleanup = container.cleanup().await;
    let (output, status) = result?;
    cleanup?;
    Ok((output.stdout, status))
}

pub async fn capture_task(
    root: &Path,
    directory: &Path,
    task: &crate::config::Task,
    command: &Command,
    environment: &BTreeMap<String, String>,
    overrides: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    Ok(capture_task_output(
        root,
        directory,
        task,
        command,
        environment,
        overrides,
        cancel,
    )
    .await?
    .stdout)
}

pub(crate) async fn tool_identity(
    root: &Path,
    directory: &Path,
    task: &crate::config::Task,
    command: &Command,
    environment: &BTreeMap<String, String>,
    overrides: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<String> {
    let output = capture_task_output(
        root,
        directory,
        task,
        command,
        environment,
        overrides,
        cancel,
    )
    .await?;
    // Hash separately before framing: moving bytes between stdout and stderr
    // must not produce the same identity. Metadata consumers still receive only
    // stdout.
    Ok(crate::files::digest(&serde_json::to_vec(&(
        crate::files::digest(&output.stdout),
        crate::files::digest(&output.stderr),
    ))?))
}

async fn capture_task_output(
    root: &Path,
    directory: &Path,
    task: &crate::config::Task,
    command: &Command,
    environment: &BTreeMap<String, String>,
    overrides: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<CapturedOutput> {
    if task.platform.executor == crate::config::Executor::Host {
        return capture_output(
            directory,
            command,
            task.shell.as_deref(),
            environment,
            cancel,
        )
        .await;
    }
    let mut probe = task.clone();
    probe.command = command.clone();
    probe.platform.ports.clear();
    let (command, mut container) = crate::docker::prepare(
        root,
        directory,
        &probe,
        environment,
        overrides,
        &uuid::Uuid::now_v7().to_string(),
        cancel,
    )
    .await?;
    let result = capture_output(
        directory,
        &command,
        None,
        container.host_environment(),
        cancel,
    )
    .await;
    container.cleanup().await?;
    result
}
pub(crate) const METADATA_LIMIT: u64 = 64 * 1024 * 1024;

async fn read_bounded(mut reader: impl AsyncRead + Unpin) -> Result<Vec<u8>> {
    let mut bytes = vec![];
    let mut block = [0; 8192];
    let mut exceeded = false;
    loop {
        let n = reader.read(&mut block).await?;
        if n == 0 {
            break;
        }
        if (bytes.len() + n) as u64 <= METADATA_LIMIT {
            bytes.extend_from_slice(&block[..n]);
        } else {
            exceeded = true;
        }
    }
    ensure!(!exceeded, "native metadata exceeded 64 MiB");
    Ok(bytes)
}

#[cfg(windows)]
mod windows {
    use std::os::windows::io::{AsRawHandle, FromRawHandle, OwnedHandle};

    use windows_sys::Win32::{
        Foundation::*,
        System::{Diagnostics::ToolHelp::*, JobObjects::*, Threading::*},
    };

    use super::*;

    pub const CLEANUP_TIMEOUT: Duration = Duration::from_secs(10);

    fn api_failure(operation: &'static str) -> anyhow::Error {
        let error = std::io::Error::last_os_error();
        tracing::error!(
            operation,
            os_error = error.raw_os_error(),
            "Windows job cleanup failed"
        );
        anyhow::Error::new(error).context(CleanupFailure)
    }

    #[derive(Default)]
    pub struct Cleanup {
        processes: BTreeMap<u32, OwnedHandle>,
        prepared: bool,
    }

    impl Cleanup {
        pub fn prepare(&mut self, job: usize) -> Result<()> {
            if self.prepared {
                return Ok(());
            }
            // ActiveProcesses can reach zero before process objects signal.
            // Retain exact kernel handles before either graceful or forced
            // termination, including across failed cleanup attempts and Drop.
            // First forbid new children: with at least one live parent, a limit
            // of one rejects every new association. Without this boundary a
            // child born after enumeration could escape the handle wait.
            // https://learn.microsoft.com/windows/win32/api/winnt/ns-winnt-jobobject_basic_limit_information
            let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = unsafe { std::mem::zeroed() };
            if unsafe {
                QueryInformationJobObject(
                    job as _,
                    JobObjectExtendedLimitInformation,
                    &mut limits as *mut _ as _,
                    std::mem::size_of_val(&limits) as u32,
                    std::ptr::null_mut(),
                )
            } == 0
            {
                return Err(api_failure("query-job-limits"));
            }
            limits.BasicLimitInformation.LimitFlags |= JOB_OBJECT_LIMIT_ACTIVE_PROCESS;
            limits.BasicLimitInformation.ActiveProcessLimit = 1;
            if unsafe {
                SetInformationJobObject(
                    job as _,
                    JobObjectExtendedLimitInformation,
                    &limits as *const _ as _,
                    std::mem::size_of_val(&limits) as u32,
                )
            } == 0
            {
                return Err(api_failure("seal-job-process-membership"));
            }
            for pid in process_ids(job)? {
                if self.processes.contains_key(&pid) {
                    continue;
                }
                let process = unsafe {
                    OpenProcess(
                        PROCESS_SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                        0,
                        pid,
                    )
                };
                if process.is_null() {
                    // A process that disappeared before OpenProcess no longer
                    // owns resources. Access denial is not evidence of exit.
                    if unsafe { GetLastError() } == ERROR_INVALID_PARAMETER {
                        continue;
                    }
                    return Err(api_failure("open-job-process"));
                }
                let process = unsafe { OwnedHandle::from_raw_handle(process) };
                let mut member = 0;
                if unsafe { IsProcessInJob(process.as_raw_handle(), job as _, &mut member) } == 0 {
                    return Err(api_failure("verify-job-process-membership"));
                }
                // PID reuse between enumeration and OpenProcess must not make
                // cleanup wait for an unrelated process.
                if member != 0 {
                    self.processes.insert(pid, process);
                }
            }
            self.prepared = true;
            tracing::debug!(
                processes = self.processes.len(),
                "Retained Windows cleanup process handles"
            );
            Ok(())
        }

        pub fn exited(&self) -> Result<bool> {
            for (pid, process) in &self.processes {
                match unsafe { WaitForSingleObject(process.as_raw_handle(), 0) } {
                    WAIT_OBJECT_0 => {}
                    WAIT_TIMEOUT => {
                        tracing::trace!(pid, "Awaiting Windows process termination signal");
                        return Ok(false);
                    }
                    _ => return Err(api_failure("wait-job-process")),
                }
            }
            Ok(true)
        }
    }

    fn process_ids(job: usize) -> Result<Vec<u32>> {
        // Use ULONG_PTR-aligned storage for the variable-length Win32 record;
        // grow on partial results instead of trusting the initial process count.
        let header = std::mem::offset_of!(JOBOBJECT_BASIC_PROCESS_ID_LIST, ProcessIdList)
            / std::mem::size_of::<usize>();
        let mut capacity = 64usize;
        let deadline = std::time::Instant::now() + CLEANUP_TIMEOUT;
        loop {
            let mut storage = Vec::<usize>::new();
            storage
                .try_reserve_exact(header + capacity)
                .context(CleanupFailure)?;
            storage.resize(header + capacity, 0);
            let bytes =
                u32::try_from(std::mem::size_of_val(storage.as_slice())).context(CleanupFailure)?;
            let record = storage
                .as_mut_ptr()
                .cast::<JOBOBJECT_BASIC_PROCESS_ID_LIST>();
            let success = unsafe {
                QueryInformationJobObject(
                    job as _,
                    JobObjectBasicProcessIdList,
                    record.cast(),
                    bytes,
                    std::ptr::null_mut(),
                )
            };
            if success == 0 && unsafe { GetLastError() } != ERROR_MORE_DATA {
                return Err(api_failure("enumerate-job-processes"));
            }
            let (assigned, listed) = unsafe {
                (
                    (*record).NumberOfAssignedProcesses as usize,
                    (*record).NumberOfProcessIdsInList as usize,
                )
            };
            if success != 0 && listed == assigned && listed <= capacity {
                return storage[header..header + listed]
                    .iter()
                    .map(|pid| u32::try_from(*pid).context(CleanupFailure))
                    .collect();
            }
            ensure!(std::time::Instant::now() < deadline, CleanupFailure);
            capacity = capacity
                .checked_mul(2)
                .context(CleanupFailure)?
                .max(assigned);
        }
    }

    pub fn terminate_job(job: usize) -> Result<()> {
        if unsafe { TerminateJobObject(job as _, 130) } == 0 {
            return Err(api_failure("terminate-job"));
        }
        Ok(())
    }

    pub fn active_processes(job: usize) -> Result<u32> {
        let mut accounting: JOBOBJECT_BASIC_ACCOUNTING_INFORMATION = unsafe { std::mem::zeroed() };
        if unsafe {
            QueryInformationJobObject(
                job as _,
                JobObjectBasicAccountingInformation,
                &mut accounting as *mut _ as _,
                std::mem::size_of_val(&accounting) as u32,
                std::ptr::null_mut(),
            )
        } == 0
        {
            return Err(api_failure("query-job-accounting"));
        }
        Ok(accounting.ActiveProcesses)
    }

    pub fn attach_and_resume(child: &Child, pid: u32) -> Result<usize> {
        unsafe {
            let job = CreateJobObjectW(std::ptr::null(), std::ptr::null());
            ensure!(!job.is_null(), "create process Job Object failed");
            let mut limits: JOBOBJECT_EXTENDED_LIMIT_INFORMATION = std::mem::zeroed();
            limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
            let process = child.raw_handle().context("child process handle missing")?;
            if SetInformationJobObject(
                job,
                JobObjectExtendedLimitInformation,
                &limits as *const _ as _,
                std::mem::size_of_val(&limits) as u32,
            ) == 0
                || AssignProcessToJobObject(job, process as _) == 0
            {
                CloseHandle(job);
                anyhow::bail!("assign process Job Object failed");
            }
            let snapshot = CreateToolhelp32Snapshot(TH32CS_SNAPTHREAD, 0);
            if snapshot == INVALID_HANDLE_VALUE {
                CloseHandle(job);
                anyhow::bail!("enumerate suspended process thread failed");
            }
            let mut entry: THREADENTRY32 = std::mem::zeroed();
            entry.dwSize = std::mem::size_of_val(&entry) as u32;
            let mut found = false;
            let mut available = Thread32First(snapshot, &mut entry);
            while available != 0 {
                if entry.th32OwnerProcessID == pid {
                    let thread = OpenThread(THREAD_SUSPEND_RESUME, 0, entry.th32ThreadID);
                    if !thread.is_null() {
                        found = ResumeThread(thread) != u32::MAX;
                        CloseHandle(thread);
                    }
                    break;
                }
                available = Thread32Next(snapshot, &mut entry);
            }
            CloseHandle(snapshot);
            if !found {
                CloseHandle(job);
                anyhow::bail!("resume owned process failed");
            }
            Ok(job as usize)
        }
    }
    #[cfg(test)]
    mod tests {
        use std::os::windows::io::{AsRawHandle, FromRawHandle, OwnedHandle};

        use windows_sys::Win32::System::SystemServices::{JOB_OBJECT_QUERY, JOB_OBJECT_TERMINATE};

        use super::*;

        #[test]
        // Deliberately outlive the direct parent: the regression must prove that
        // the Job Object, rather than a parent wait, owns the descendant.
        #[allow(clippy::zombie_processes)]
        fn windows_job_fixture() {
            let Ok(mode) = std::env::var("TFLOW_JOB_FIXTURE_MODE") else {
                return;
            };
            if mode == "unexpected" {
                std::fs::write("unexpected-child", b"ran").unwrap();
                return;
            }
            if mode == "child" {
                let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
                // Publish readiness only after binding, without releasing a
                // parent-reserved ephemeral port for another test to acquire.
                crate::files::atomic_write(
                    Path::new(&std::env::var("TFLOW_JOB_READY").unwrap()),
                    &serde_json::to_vec(&(std::process::id(), listener.local_addr().unwrap()))
                        .unwrap(),
                )
                .unwrap();
                std::thread::sleep(Duration::from_secs(60));
                return;
            }
            std::process::Command::new(std::env::current_exe().unwrap())
                .args([
                    "--exact",
                    "process::windows::tests::windows_job_fixture",
                    "--nocapture",
                ])
                .env("TFLOW_JOB_FIXTURE_MODE", "child")
                .stdin(Stdio::null())
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .spawn()
                .unwrap();
            let path = std::env::var("TFLOW_JOB_READY").unwrap();
            while !Path::new(&path).exists() {
                std::thread::sleep(Duration::from_millis(10));
            }
            if mode == "hold" {
                while !Path::new("probe-sealed-job").exists() {
                    std::thread::sleep(Duration::from_millis(10));
                }
                let result = std::process::Command::new(std::env::current_exe().unwrap())
                    .args([
                        "--exact",
                        "process::windows::tests::windows_job_fixture",
                        "--nocapture",
                    ])
                    .env("TFLOW_JOB_FIXTURE_MODE", "unexpected")
                    .stdin(Stdio::null())
                    .stdout(Stdio::null())
                    .stderr(Stdio::null())
                    .status();
                assert!(
                    result.is_err() || !result.unwrap().success(),
                    "sealed job admitted a new child"
                );
                std::fs::write("sealed-job-rejected-child", b"rejected").unwrap();
                std::thread::sleep(Duration::from_secs(60));
            }
        }

        #[tokio::test]
        async fn failed_job_cleanup_preserves_ownership_for_retry_and_drop() {
            for reason in [
                ExitReason::Completed,
                ExitReason::Cancelled,
                ExitReason::TimedOut,
            ] {
                for access in [JOB_OBJECT_QUERY, JOB_OBJECT_TERMINATE] {
                    let directory = tempfile::tempdir().unwrap();
                    let ready_file = directory.path().join("child.json");
                    let mut environment: BTreeMap<String, String> = std::env::vars().collect();
                    environment.insert(
                        "TFLOW_JOB_FIXTURE_MODE".into(),
                        if reason == ExitReason::Completed {
                            "exit"
                        } else {
                            "hold"
                        }
                        .into(),
                    );
                    environment.insert(
                        "TFLOW_JOB_READY".into(),
                        ready_file.to_str().unwrap().into(),
                    );
                    let command = Command::Argv(vec![
                        std::env::current_exe().unwrap().to_str().unwrap().into(),
                        "--exact".into(),
                        "process::windows::tests::windows_job_fixture".into(),
                        "--nocapture".into(),
                    ]);
                    let mut owner =
                        OwnedProcess::spawn(directory.path(), &command, None, Some(&environment))
                            .unwrap();
                    let deadline = tokio::time::Instant::now() + Duration::from_secs(20);
                    while !ready_file.exists() && tokio::time::Instant::now() < deadline {
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    }
                    assert!(
                        ready_file.exists(),
                        "{reason:?}, access={access}: owned descendant did not bind its socket"
                    );
                    let (pid, address): (u32, std::net::SocketAddr) =
                        serde_json::from_slice(&std::fs::read(&ready_file).unwrap()).unwrap();
                    let handle = unsafe {
                        OpenProcess(
                            PROCESS_SYNCHRONIZE | PROCESS_QUERY_LIMITED_INFORMATION,
                            0,
                            pid,
                        )
                    };
                    assert!(
                        !handle.is_null(),
                        "open descendant: {}",
                        std::io::Error::last_os_error()
                    );
                    // Retain the kernel object before cleanup; reopening a PID
                    // afterward could inspect a different, reused process ID.
                    let descendant = unsafe { OwnedHandle::from_raw_handle(handle) };
                    let mut in_job = 0;
                    assert_ne!(
                        unsafe { IsProcessInJob(handle, owner.job as _, &mut in_job) },
                        0
                    );
                    assert_ne!(in_job, 0, "fixture descendant escaped the owned job");
                    assert_eq!(unsafe { WaitForSingleObject(handle, 0) }, WAIT_TIMEOUT);
                    owner.job_cleanup.prepare(owner.job).unwrap();
                    assert!(owner.job_cleanup.processes.contains_key(&pid));
                    if reason != ExitReason::Completed {
                        assert!(owner.job_cleanup.processes.len() >= 2);
                        std::fs::write(directory.path().join("probe-sealed-job"), b"probe")
                            .unwrap();
                        let deadline = tokio::time::Instant::now() + Duration::from_secs(10);
                        while !directory.path().join("sealed-job-rejected-child").exists() {
                            assert!(
                                tokio::time::Instant::now() < deadline,
                                "owned parent did not confirm the spawn barrier"
                            );
                            tokio::time::sleep(Duration::from_millis(10)).await;
                        }
                        assert!(!directory.path().join("unexpected-child").exists());
                    }
                    let full_job = owner.job;
                    // Real access-denied faults exercise both API return values
                    // without invalid handles or process-global test hooks.
                    let mut restricted = std::ptr::null_mut();
                    assert_ne!(
                        unsafe {
                            DuplicateHandle(
                                GetCurrentProcess(),
                                full_job as _,
                                GetCurrentProcess(),
                                &mut restricted,
                                access,
                                0,
                                0,
                            )
                        },
                        0
                    );
                    owner.job = restricted as usize;
                    let cancel = CancellationToken::new();
                    if reason == ExitReason::Cancelled {
                        cancel.cancel();
                    }
                    let timeout = if reason == ExitReason::TimedOut {
                        Duration::from_millis(1)
                    } else {
                        Duration::from_secs(20)
                    };
                    let result = owner.wait(&cancel, Some(timeout)).await;
                    owner.job = full_job;
                    unsafe {
                        CloseHandle(restricted);
                    }
                    let error = result.unwrap_err();
                    assert!(
                        error.is::<CleanupFailure>(),
                        "{reason:?}, access={access}: {error:?}"
                    );
                    assert!(!owner.cleaned, "failed API must retain cleanup ownership");
                    if reason != ExitReason::Completed {
                        owner.terminate().await.unwrap();
                        assert!(owner.cleaned);
                        assert_eq!(active_processes(full_job).unwrap(), 0);
                    }
                    // Normal-root-exit faults take the destructor retry path;
                    // cancellation/deadline faults prove explicit retry first.
                    drop(owner);
                    // Require the exact descendant to be dead when cleanup
                    // returns. A port-rebind retry must never hide a live child.
                    assert_eq!(
                        unsafe { WaitForSingleObject(descendant.as_raw_handle(), 0) },
                        WAIT_OBJECT_0,
                        "{reason:?}, access={access}, pid={pid}: cleanup returned before \
                         descendant exit"
                    );
                    drop(descendant);
                    // Process exit and immediate Winsock address reuse are
                    // separate observations. Keep the resource assertion, but
                    // tolerate only bounded AddrInUse after proving exit above.
                    // Do not enable SO_REUSEADDR: it can bind over a live owner.
                    let started = tokio::time::Instant::now();
                    let mut attempts = 0;
                    let _rebound = loop {
                        attempts += 1;
                        match std::net::TcpListener::bind(address) {
                            Ok(listener) => break listener,
                            Err(error)
                                if error.kind() == std::io::ErrorKind::AddrInUse
                                    && started.elapsed() < Duration::from_secs(10) =>
                            {
                                tokio::time::sleep(Duration::from_millis(10)).await;
                            }
                            Err(error) => panic!(
                                "{reason:?}, access={access}, pid={pid}: socket not released \
                                 after {attempts} attempts: {error}"
                            ),
                        }
                    };
                    eprintln!(
                        "Windows cleanup fixture: reason={reason:?} access={access} pid={pid} \
                         rebind_attempts={attempts} rebind_elapsed_ms={}",
                        started.elapsed().as_millis()
                    );
                }
            }
        }
    }
}

#[cfg(unix)]
mod unix_owner {
    use std::{
        os::unix::fs::PermissionsExt,
        sync::atomic::{AtomicBool, Ordering},
    };

    use super::*;

    static POISONED: AtomicBool = AtomicBool::new(false);

    pub fn poison() {
        POISONED.store(true, Ordering::SeqCst);
    }

    pub fn environment_envelope(environment: Option<&BTreeMap<String, String>>) -> Result<Vec<u8>> {
        let inherited;
        let environment = match environment {
            Some(environment) => environment,
            None => {
                inherited = crate::environment::inherited()?;
                &inherited
            }
        };
        ensure!(
            environment.len() <= 65536,
            "command environment exceeds transport limit"
        );
        let mut bytes = (environment.len() as u32).to_ne_bytes().to_vec();
        for (name, value) in environment {
            ensure!(
                !name.is_empty() && !name.contains(['=', '\0']) && !value.contains('\0'),
                "invalid command environment entry"
            );
            let size = name
                .len()
                .checked_add(value.len())
                .and_then(|n| n.checked_add(1))
                .context("command environment exceeds transport limit")?;
            ensure!(
                size <= 16 * 1024 * 1024 && bytes.len() + size + 4 <= METADATA_LIMIT as usize,
                "command environment exceeds transport limit"
            );
            bytes.extend_from_slice(&(size as u32).to_ne_bytes());
            bytes.extend_from_slice(name.as_bytes());
            bytes.push(b'=');
            bytes.extend_from_slice(value.as_bytes());
        }
        Ok(bytes)
    }

    pub struct Prepared {
        pub scope: tempfile::TempDir,
        pub executable: std::path::PathBuf,
        #[cfg(target_os = "linux")]
        _image: std::fs::File,
    }

    #[cfg(target_os = "linux")]
    fn executable_image(bytes: &[u8]) -> Result<std::fs::File> {
        use std::{
            io::Write,
            os::fd::{AsRawFd, FromRawFd},
        };

        use nix::libc;
        let flags = libc::MFD_CLOEXEC | libc::MFD_ALLOW_SEALING;
        let mut fd =
            unsafe { libc::memfd_create(c"tflow-supervisor".as_ptr(), flags | libc::MFD_EXEC) };
        // MFD_EXEC was introduced after memfd itself. Only an unsupported flag
        // permits the legacy call; an explicit executable-memfd policy denial
        // must fail closed, never fall back to a filesystem executable.
        if fd < 0 && std::io::Error::last_os_error().raw_os_error() == Some(libc::EINVAL) {
            fd = unsafe { libc::memfd_create(c"tflow-supervisor".as_ptr(), flags) };
        }
        if fd < 0 {
            return Err(std::io::Error::last_os_error())
                .context("create executable native supervisor image");
        }
        let mut image = unsafe { std::fs::File::from_raw_fd(fd) };
        image.write_all(bytes)?;
        image.set_permissions(std::fs::Permissions::from_mode(0o500))?;
        let seals =
            libc::F_SEAL_WRITE | libc::F_SEAL_GROW | libc::F_SEAL_SHRINK | libc::F_SEAL_SEAL;
        if unsafe { libc::fcntl(image.as_raw_fd(), libc::F_ADD_SEALS, seals) } < 0 {
            return Err(std::io::Error::last_os_error()).context("seal native supervisor image");
        }
        Ok(image)
    }

    pub fn prepare() -> Result<Prepared> {
        ensure!(!POISONED.load(Ordering::SeqCst), CleanupFailure);
        // A short private path is required by macOS sockaddr_un. User TMPDIR
        // can exceed its limit before adding a single socket component.
        let scope = tempfile::Builder::new()
            .prefix("tflow-")
            .tempdir_in("/tmp")?;
        let bytes = include_bytes!(concat!(env!("OUT_DIR"), "/taskflow-supervisor"));
        #[cfg(target_os = "linux")]
        {
            use std::os::fd::AsRawFd;
            let image = executable_image(bytes)?;
            // Linux executes the sealed anonymous inode, so control/journal
            // storage may be mounted noexec. CLOEXEC closes it after loading.
            let executable = format!("/proc/self/fd/{}", image.as_raw_fd()).into();
            Ok(Prepared {
                scope,
                executable,
                _image: image,
            })
        }
        #[cfg(target_os = "macos")]
        {
            let executable = scope.path().join("supervisor");
            std::fs::write(&executable, bytes)?;
            std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o500))?;
            Ok(Prepared { scope, executable })
        }
    }

    #[cfg(all(test, target_os = "linux"))]
    mod linux_owner_tests {
        use std::{io::Write, os::fd::AsRawFd};

        use super::*;

        #[test]
        fn supervisor_image_is_an_immutable_anonymous_executable() {
            let prepared = prepare().unwrap();
            assert!(!prepared.scope.path().join("supervisor").exists());
            assert!(prepared.executable.starts_with("/proc/self/fd"));
            // The image is mode 0500. Reopening for write tests root's DAC bypass,
            // not memfd sealing, and fails on unprivileged CI runners. Duplicate the
            // writer retained from image creation to exercise the seal itself.
            let mut writable = prepared._image.try_clone().unwrap();
            assert_eq!(
                writable.metadata().unwrap().permissions().mode() & 0o777,
                0o500
            );
            assert_eq!(
                writable.write_all(b"changed").unwrap_err().raw_os_error(),
                Some(nix::libc::EPERM)
            );
            assert!(writable.set_len(0).is_err());
            let flags = unsafe { nix::libc::fcntl(writable.as_raw_fd(), nix::libc::F_GET_SEALS) };
            assert_ne!(flags & nix::libc::F_SEAL_SEAL, 0);
            assert!(!std::fs::read(&prepared.executable).unwrap().is_empty());
        }
    }
}
