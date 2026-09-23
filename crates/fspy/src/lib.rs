pub mod error;

#[cfg(not(target_env = "musl"))]
mod ipc;

#[cfg(unix)]
#[path = "./unix/mod.rs"]
mod os_impl;

#[cfg(target_os = "windows")]
#[path = "./windows/mod.rs"]
mod os_impl;

#[cfg(unix)]
mod arena;
mod command;

use std::{io, process::ExitStatus, sync::LazyLock};

pub use command::Command;
pub use error::TrackingIncomplete;
pub use fspy_shared::ipc::{AccessMode, PathAccess};
use futures_util::future::BoxFuture;
pub use os_impl::PathAccessIterable;
use os_impl::SpyImpl;
use tempfile::TempDir;
use tokio::process::{ChildStderr, ChildStdin, ChildStdout};

/// The result of a tracked child process upon its termination.
pub struct ChildTermination {
    /// The exit status of the child process.
    pub status: ExitStatus,
    /// The path accesses captured from the child process, or the reason
    /// they cannot be trusted to be all of them.
    pub path_accesses: Result<PathAccessIterable, TrackingIncomplete>,
}

pub struct TrackedChild {
    /// The handle for writing to the child's standard input (stdin), if it has
    /// been captured.
    pub stdin: Option<ChildStdin>,

    /// The handle for reading from the child's standard output (stdout), if it
    /// has been captured.
    pub stdout: Option<ChildStdout>,

    /// The handle for reading from the child's standard error (stderr), if it
    /// has been captured.
    pub stderr: Option<ChildStderr>,

    /// The future that resolves to exit status and path accesses when the
    /// process exits.
    pub wait_handle: BoxFuture<'static, io::Result<ChildTermination>>,

    /// A duplicated process handle of the child, captured before the tokio
    /// `Child` is moved into the background wait task. This is an
    /// independently owned handle (via `DuplicateHandle`) so it remains
    /// valid even after tokio closes its copy. Callers can use this to
    /// assign the process to a Win32 Job Object.
    #[cfg(windows)]
    pub process_handle: std::os::windows::io::OwnedHandle,
}

pub(crate) struct GlobalSpy {
    pub(crate) spy: SpyImpl,
    // The random 0700 directory must outlive every process using its preload.
    _dir: TempDir,
}

fn private_preload_dir() -> io::Result<TempDir> {
    let parent = std::fs::canonicalize(std::env::temp_dir())?;
    let mut builder = tempfile::Builder::new();
    builder.prefix("fspy-");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        builder.permissions(std::fs::Permissions::from_mode(0o700));
    }
    builder.tempdir_in(parent)
}

pub(crate) static SPY_IMPL: LazyLock<GlobalSpy> = LazyLock::new(|| {
    let dir = private_preload_dir().expect("Failed to create private preload directory");
    let spy = SpyImpl::init_in(dir.path()).expect("Failed to initialize global spy");
    GlobalSpy { spy, _dir: dir }
});

#[cfg(all(test, unix))]
mod tests {
    use std::os::unix::fs::PermissionsExt;
    #[cfg(target_os = "linux")]
    use std::{fs, process::Stdio, time::Duration};

    #[cfg(target_os = "linux")]
    use tokio::io::AsyncReadExt;
    #[cfg(target_os = "linux")]
    use tokio_util::sync::CancellationToken;

    use super::private_preload_dir;

    #[test]
    fn preload_directory_is_private() {
        let dir = private_preload_dir().expect("create private preload directory");
        let mode = dir
            .path()
            .metadata()
            .expect("inspect directory")
            .permissions()
            .mode();
        assert_eq!(mode & 0o777, 0o700);
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn filtered_descendant_continues_after_root_trace_seals() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("survivor.c");
        let executable = directory.path().join("survivor");
        fs::write(
            &source,
            r#"#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>
int main(void) {
  if (fork() == 0) {
    usleep(500000);
    int fd = open("/dev/null", O_RDONLY);
    puts(fd >= 0 ? "continued" : "blocked");
    if (fd >= 0) close(fd);
    return 0;
  }
  return 0;
}
"#,
        )
        .expect("write fixture");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile static fixture")
                .success()
        );

        let mut command = super::Command::new(&executable);
        command.stdout(Stdio::piped());
        let mut child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let mut stdout = child.stdout.take().expect("capture descendant output");
        let termination = child.wait_handle.await.expect("wait for tracked root");
        assert!(termination.status.success());
        let mut output = String::new();
        tokio::time::timeout(Duration::from_secs(5), stdout.read_to_string(&mut output))
            .await
            .expect("descendant did not finish")
            .expect("read descendant output");
        assert_eq!(output, "continued\n");
    }

    #[cfg(all(target_os = "linux", target_arch = "x86_64"))]
    #[tokio::test]
    async fn seccomp_creat_records_an_output() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("create.c");
        let executable = directory.path().join("create");
        let output = directory.path().join("output");
        fs::write(
            &source,
            r#"#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  int fd = syscall(SYS_creat, argv[1], 0600);
  if (fd < 0) return 3;
  close(fd);
  return 0;
}
"#,
        )
        .expect("write fixture");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile static fixture")
                .success()
        );

        let mut command = super::Command::new(&executable);
        command.arg(&output);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success());
        assert!(output.exists());
        let accesses = termination.path_accesses.expect("complete trace");
        assert!(accesses.iter().any(|access| {
            access.mode.contains(super::AccessMode::WRITE)
                && access.path.strip_path_prefix(&output, |path| {
                    path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                })
        }));
    }
}
