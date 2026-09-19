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
        let status = tokio::select! {
            result = self.child.wait() => Some(result?),
            _ = cancel.cancelled() => None,
            _ = &mut deadline => None,
        };
        if let Some(status) = status {
            // A finite command may leave descendants with inherited pipes. Reap the
            // group before awaiting EOF so those children cannot deadlock completion.
            self.kill_tree();
            Ok(ProcessExit {
                code: status.code().unwrap_or(1),
                cancelled: false,
            })
        } else {
            self.terminate().await?;
            Ok(ProcessExit {
                code: 130,
                cancelled: true,
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
    pub cancelled: bool,
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
    let mut child = OwnedProcess::spawn(directory, command, shell, Some(environment))?;
    let stdout = child.child.stdout.take().unwrap();
    let stderr = child.child.stderr.take().unwrap();
    let out = tokio::spawn(read_bounded(stdout));
    let err = tokio::spawn(read_bounded(stderr));
    let status = child.wait(cancel, Some(Duration::from_secs(120))).await?;
    let output = out.await??;
    let _ = err.await??;
    ensure!(
        status.code == 0 && !status.cancelled,
        "native command failed (exit {})",
        status.code
    );
    Ok(output)
}

pub async fn capture_task(
    root: &Path,
    directory: &Path,
    task: &crate::config::Task,
    command: &Command,
    environment: &BTreeMap<String, String>,
    cancel: &CancellationToken,
) -> Result<Vec<u8>> {
    if task.platform.executor == crate::config::Executor::Host {
        return capture_with_shell(
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
        &uuid::Uuid::now_v7().to_string(),
        cancel,
    )
    .await?;
    let result = capture_with_env(directory, &command, &environment, cancel).await;
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
