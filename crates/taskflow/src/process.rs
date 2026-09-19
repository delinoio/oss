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
}

pub struct OwnedProcess {
    pub child: Child,
    pid: u32,
    cleaned: bool,
    #[cfg(windows)]
    job: usize,
}

impl OwnedProcess {
    pub fn spawn(
        directory: &Path,
        command: &Command,
        shell: Option<&[String]>,
        environment: Option<&BTreeMap<String, String>>,
    ) -> Result<Self> {
        let args = argv(command, shell);
        let mut builder = tokio::process::Command::new(&args[0]);
        builder
            .args(&args[1..])
            .current_dir(directory)
            .stdin(Stdio::null())
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .kill_on_drop(true);
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
        let child = builder.spawn().context("failed to spawn command")?;
        let pid = child.id().context("spawned child has no process ID")?;
        let mut owned = Self {
            child,
            pid,
            cleaned: false,
            #[cfg(windows)]
            job: 0,
        };
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
            // A finite command may leave descendants with inherited pipes. Reap the
            // group before awaiting EOF so those children cannot deadlock completion.
            self.kill_tree();
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
        #[cfg(unix)]
        {
            let _ = nix::sys::signal::killpg(
                nix::unistd::Pid::from_raw(self.pid as i32),
                nix::sys::signal::Signal::SIGTERM,
            );
        }
        #[cfg(windows)]
        unsafe {
            windows_sys::Win32::System::Console::GenerateConsoleCtrlEvent(
                windows_sys::Win32::System::Console::CTRL_BREAK_EVENT,
                self.pid,
            );
        }
        let _ = tokio::time::timeout(Duration::from_secs(2), self.child.wait()).await;
        self.kill_tree();
        let _ = self.child.wait().await;
        tracing::debug!(
            pid = self.pid,
            outcome = "reaped",
            "Owned process tree cleanup completed"
        );
        Ok(())
    }

    fn kill_tree(&mut self) {
        if self.cleaned {
            return;
        }
        self.cleaned = true;
        #[cfg(unix)]
        {
            let _ = nix::sys::signal::killpg(
                nix::unistd::Pid::from_raw(self.pid as i32),
                nix::sys::signal::Signal::SIGKILL,
            );
        }
        #[cfg(windows)]
        if self.job != 0 {
            unsafe {
                windows_sys::Win32::System::JobObjects::TerminateJobObject(self.job as _, 130);
            }
        }
        let _ = self.child.start_kill();
    }
}
impl Drop for OwnedProcess {
    fn drop(&mut self) {
        self.kill_tree();
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
    let mut env: BTreeMap<String, String> = std::env::vars().collect();
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
    ensure!(
        status.code == 0 && !status.cancelled(),
        "native command failed (exit {})",
        status.code
    );
    Ok(output)
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
    let mut child = OwnedProcess::spawn(directory, command, shell, Some(environment))?;
    let stdout = child.child.stdout.take().unwrap();
    let stderr = child.child.stderr.take().unwrap();
    let out = tokio::spawn(read_bounded(stdout));
    let err = tokio::spawn(read_bounded(stderr));
    let status = child.wait(cancel, Some(Duration::from_secs(120))).await;
    let cleanup = if status.is_err() {
        child.terminate().await
    } else {
        Ok(())
    };
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
    let environment = crate::docker::host_environment(environment);
    let (command, mut container) = crate::docker::prepare(
        root,
        directory,
        &probe,
        &environment,
        overrides,
        &uuid::Uuid::now_v7().to_string(),
        cancel,
    )
    .await?;
    let result = capture_output(directory, &command, None, &environment, cancel).await;
    container.cleanup().await?;
    result
}
async fn read_bounded(mut reader: impl AsyncRead + Unpin) -> Result<Vec<u8>> {
    let mut bytes = vec![];
    let mut block = [0; 8192];
    let mut exceeded = false;
    loop {
        let n = reader.read(&mut block).await?;
        if n == 0 {
            break;
        }
        if bytes.len() + n <= 64 * 1024 * 1024 {
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
    use windows_sys::Win32::{
        Foundation::*,
        System::{Diagnostics::ToolHelp::*, JobObjects::*, Threading::*},
    };

    use super::*;

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
}
