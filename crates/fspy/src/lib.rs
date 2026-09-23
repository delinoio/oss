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
    use std::{fs, os::unix::fs::PermissionsExt};
    #[cfg(target_os = "linux")]
    use std::{process::Stdio, time::Duration};

    #[cfg(target_os = "linux")]
    use tokio::io::AsyncReadExt;
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
    async fn null_preload_paths_keep_native_efault() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("null-path.c");
        let executable = directory.path().join("null-path");
        let input = directory.path().join("input");
        fs::write(&input, b"input").expect("write input");
        fs::write(
            &source,
            r"#include <errno.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  volatile unsigned long zero = 0;
  const char *path = (const char *)zero;
  struct stat st;
  if (open(path, O_RDONLY) != -1 || errno != EFAULT) return 3;
  if (openat(AT_FDCWD, path, O_RDONLY) != -1 || errno != EFAULT) return 4;
  if (stat(path, &st) != -1 || errno != EFAULT) return 6;
  if (lstat(path, &st) != -1 || errno != EFAULT) return 7;
  if (fstatat(AT_FDCWD, path, &st, 0) != -1 || errno != EFAULT) return 8;
  if (access(path, F_OK) != -1 || errno != EFAULT) return 9;
  if (faccessat(AT_FDCWD, path, F_OK, 0) != -1 || errno != EFAULT) return 10;
  int fd = open(argv[1], O_RDONLY);
  if (fd < 0) return 5;
  close(fd);
  return 0;
}
",
        )
        .expect("write fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile fixture")
                .success()
        );
        let mut command = super::Command::new(&executable);
        command.arg(&input);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success(), "{:?}", termination.status);
        assert!(termination.path_accesses.is_ok());
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn null_and_empty_exec_vectors_run_shebang() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("exec-vectors.c");
        let executable = directory.path().join("exec-vectors");
        let script = directory.path().join("script");
        fs::write(
            &source,
            r"#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2 && argc != 3) return 2;
  char *empty[] = {NULL};
  execve(argv[1], argc == 2 ? NULL : empty, NULL);
  return 3;
}
",
        )
        .expect("write fixture");
        fs::write(&script, "#!/bin/sh\nexit 23\n").expect("write script");
        fs::set_permissions(&script, fs::Permissions::from_mode(0o755))
            .expect("make script executable");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile fixture")
                .success()
        );
        for empty_array in [false, true] {
            let mut command = super::Command::new(&executable);
            command.arg(&script);
            if empty_array {
                command.arg("empty-array");
            }
            let child = command
                .spawn(CancellationToken::new())
                .await
                .expect("spawn fixture");
            let termination = child.wait_handle.await.expect("wait for fixture");
            assert_eq!(termination.status.code(), Some(23));
            assert!(termination.path_accesses.is_ok());
        }
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn preload_records_path_mutations_as_writes() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("preload-mutate.c");
        let executable = directory.path().join("preload-mutate");
        let old = directory.path().join("old");
        let new = directory.path().join("new");
        let symbolic = directory.path().join("symbolic");
        let created_dir = directory.path().join("created");
        let hard = directory.path().join("hard");
        fs::write(&old, b"input").expect("write old image");
        fs::write(
            &source,
            r"#include <stdio.h>
#include <sys/stat.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 6) return 2;
  if (mkdir(argv[4], 0700)) return 3;
  if (rename(argv[1], argv[2])) return 4;
  if (symlink(argv[2], argv[3])) return 5;
  if (link(argv[2], argv[5])) return 6;
  if (unlink(argv[2])) return 7;
  return 0;
}
",
        )
        .expect("write mutation fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile mutation fixture")
                .success()
        );
        let mut command = super::Command::new(&executable);
        command.args([&old, &new, &symbolic, &created_dir, &hard]);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success(), "{:?}", termination.status);
        let accesses = termination.path_accesses.expect("complete trace");
        for expected in [&old, &new, &symbolic, &created_dir, &hard] {
            assert!(
                accesses.iter().any(|access| {
                    access.mode.contains(super::AccessMode::WRITE)
                        && access.path.strip_path_prefix(expected, |path| {
                            path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                        })
                }),
                "missing write for {}",
                expected.display()
            );
        }
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn preload_records_symbolic_link_reads() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("preload-readlink.c");
        let executable = directory.path().join("preload-readlink");
        let direct = directory.path().join("direct-link");
        let relative = directory.path().join("relative-link");
        let relative_from_fd = directory
            .path()
            .canonicalize()
            .expect("resolve descriptor base")
            .join("relative-link");
        std::os::unix::fs::symlink("direct-target", &direct).expect("create direct link");
        std::os::unix::fs::symlink("relative-target", &relative).expect("create relative link");
        fs::write(
            &source,
            r#"#include <fcntl.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 3) return 2;
  char output[256];
  if (readlink(argv[1], output, sizeof(output)) < 0) return 3;
  int fd = open(argv[2], O_RDONLY);
  if (fd < 0) return 4;
  if (readlinkat(fd, "relative-link", output, sizeof(output)) < 0) return 5;
  close(fd);
  return 0;
}
"#,
        )
        .expect("write link fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile link fixture")
                .success()
        );
        let mut command = super::Command::new(&executable);
        command.args([&direct, directory.path()]);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success(), "{:?}", termination.status);
        let accesses = termination.path_accesses.expect("complete trace");
        for expected in [&direct, &relative_from_fd] {
            assert!(
                accesses.iter().any(|access| {
                    access.mode.contains(super::AccessMode::READ)
                        && access.path.strip_path_prefix(expected, |path| {
                            path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                        })
                }),
                "missing link read for {}",
                expected.display()
            );
        }
    }

    #[cfg(unix)]
    #[tokio::test]
    async fn preload_records_working_directory_alias() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("preload-chdir.c");
        let executable = directory.path().join("preload-chdir");
        let actual = directory.path().join("actual");
        let alias = directory.path().join("alias");
        fs::create_dir(&actual).expect("create target directory");
        fs::write(actual.join("input"), b"input").expect("write input");
        std::os::unix::fs::symlink(&actual, &alias).expect("create directory alias");
        fs::write(
            &source,
            r#"#include <fcntl.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  if (chdir(argv[1])) return 3;
  int input = open("input", O_RDONLY);
  if (input < 0) return 4;
  close(input);
  int directory = open(argv[1], O_RDONLY);
  if (directory < 0) return 5;
  if (fchdir(directory)) return 6;
  close(directory);
  return 0;
}
"#,
        )
        .expect("write chdir fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile chdir fixture")
                .success()
        );
        let mut command = super::Command::new(&executable);
        command.arg(&alias);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success(), "{:?}", termination.status);
        let accesses = termination.path_accesses.expect("complete trace");
        assert!(accesses.iter().any(|access| {
            access.mode.contains(super::AccessMode::READ)
                && access.path.strip_path_prefix(&alias, |path| {
                    path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                })
        }));
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn late_seccomp_filter_continues_after_root_trace_seals() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let static_source = directory.path().join("late-static.c");
        let static_executable = directory.path().join("late-static");
        fs::write(
            &static_source,
            r#"#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>
int main(void) {
  int fd = open("/dev/null", O_RDONLY);
  puts(fd >= 0 ? "late-continued" : "late-blocked");
  if (fd >= 0) close(fd);
  return fd >= 0 ? 0 : 1;
}
"#,
        )
        .expect("write static fixture");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&static_source)
                .arg("-o")
                .arg(&static_executable)
                .status()
                .expect("compile static fixture")
                .success()
        );

        let source = directory.path().join("late-parent.c");
        let executable = directory.path().join("late-parent");
        fs::write(
            &source,
            r"#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  if (fork() == 0) {
    usleep(500000);
    execl(argv[1], argv[1], (char *)0);
    return 42;
  }
  return 0;
}
",
        )
        .expect("write dynamic fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile dynamic fixture")
                .success()
        );

        let mut command = super::Command::new(&executable);
        command.arg(&static_executable).stdout(Stdio::piped());
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
            .expect("late static descendant did not finish")
            .expect("read descendant output");
        assert_eq!(output, "late-continued\n");
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
            r"#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  int fd = syscall(SYS_creat, argv[1], 0600);
  if (fd < 0) return 3;
  close(fd);
  return 0;
}
",
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

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn seccomp_records_path_mutations_as_writes() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("mutate.c");
        let executable = directory.path().join("mutate");
        let old = directory.path().join("old");
        let new = directory.path().join("new");
        let link = directory.path().join("link");
        let created_dir = directory.path().join("created");
        fs::write(&old, b"input").expect("write old image");
        fs::write(
            &source,
            r"#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 5) return 2;
  if (syscall(SYS_mkdirat, AT_FDCWD, argv[4], 0700)) return 3;
  if (syscall(SYS_renameat2, AT_FDCWD, argv[1], AT_FDCWD, argv[2], 0)) return 4;
  if (syscall(SYS_symlinkat, argv[2], AT_FDCWD, argv[3])) return 5;
  if (syscall(SYS_unlinkat, AT_FDCWD, argv[2], 0)) return 6;
  return 0;
}
",
        )
        .expect("write mutation fixture");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile mutation fixture")
                .success()
        );
        let mut command = super::Command::new(&executable);
        command.args([&old, &new, &link, &created_dir]);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success());
        let accesses = termination.path_accesses.expect("complete trace");
        for expected in [&old, &new, &link, &created_dir] {
            assert!(
                accesses.iter().any(|access| {
                    access.mode.contains(super::AccessMode::WRITE)
                        && access.path.strip_path_prefix(expected, |path| {
                            path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                        })
                }),
                "missing write for {}",
                expected.display()
            );
        }
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn seccomp_records_working_directory_alias() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("static-chdir.c");
        let executable = directory.path().join("static-chdir");
        let actual = directory.path().join("actual");
        let alias = directory.path().join("alias");
        fs::create_dir(&actual).expect("create target directory");
        fs::write(actual.join("input"), b"input").expect("write input");
        std::os::unix::fs::symlink(&actual, &alias).expect("create directory alias");
        fs::write(
            &source,
            r#"#include <fcntl.h>
#include <sys/syscall.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 3) return 2;
  if (syscall(SYS_chdir, argv[1])) return 3;
  int input = syscall(SYS_openat, AT_FDCWD, "input", O_RDONLY);
  if (input < 0) return 4;
  close(input);
  int directory = syscall(SYS_openat, AT_FDCWD, argv[2], O_RDONLY);
  if (directory < 0) return 5;
  if (syscall(SYS_fchdir, directory)) return 6;
  close(directory);
  return 0;
}
"#,
        )
        .expect("write chdir fixture");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile static chdir fixture")
                .success()
        );
        let mut command = super::Command::new(&executable);
        command.args([&alias, &actual]);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success(), "{:?}", termination.status);
        let accesses = termination.path_accesses.expect("complete trace");
        assert!(accesses.iter().any(|access| {
            access.mode.contains(super::AccessMode::READ)
                && access.path.strip_path_prefix(&alias, |path| {
                    path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                })
        }));
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn seccomp_exec_records_script_interpreter() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("launcher.c");
        let executable = directory.path().join("launcher");
        let interpreter = directory.path().join("interpreter");
        let script = directory.path().join("script");
        fs::write(
            &source,
            "#include <unistd.h>\nint main(int argc, char **argv) { if (argc != 2) return 2; \
             execl(argv[1], argv[1], (char *)0); return 127; }\n",
        )
        .expect("write launcher");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile launcher")
                .success()
        );
        std::os::unix::fs::symlink("/bin/sh", &interpreter).expect("link interpreter");
        fs::write(&script, "#!interpreter\nexit 0\n").expect("write script");
        fs::set_permissions(&script, fs::Permissions::from_mode(0o755))
            .expect("make script executable");

        let mut command = super::Command::new(&executable);
        command.arg("script").current_dir(directory.path());
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked launcher");
        let termination = child.wait_handle.await.expect("wait for launcher");
        assert!(termination.status.success());
        let accesses = termination.path_accesses.expect("complete trace");
        assert!(accesses.iter().any(|access| {
            access.path.strip_path_prefix(&interpreter, |path| {
                path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
            })
        }));
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn spawn_chdir_action_preserves_relative_program() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("launcher.c");
        let executable = directory.path().join("launcher");
        let child_dir = directory.path().join("child-dir");
        fs::create_dir(&child_dir).expect("create child directory");
        let child = child_dir.join("child");
        fs::write(&child, "#!/bin/sh\nexit 0\n").expect("write child");
        fs::set_permissions(&child, fs::Permissions::from_mode(0o755))
            .expect("make child executable");
        fs::write(
            &source,
            r#"#define _GNU_SOURCE
#include <spawn.h>
#include <sys/wait.h>
extern char **environ;
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  posix_spawn_file_actions_t actions;
  if (posix_spawn_file_actions_init(&actions)) return 3;
  if (posix_spawn_file_actions_addchdir_np(&actions, argv[1])) return 4;
  pid_t child;
  char *args[] = {"./child", 0};
  if (posix_spawn(&child, "./child", &actions, 0, args, environ)) return 5;
  int status;
  if (waitpid(child, &status, 0) != child) return 6;
  return WIFEXITED(status) ? WEXITSTATUS(status) : 7;
}
"#,
        )
        .expect("write launcher");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile launcher")
                .success()
        );

        let mut command = super::Command::new(&executable);
        command.arg(&child_dir).current_dir(directory.path());
        let spawned = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked launcher");
        let termination = spawned.wait_handle.await.expect("wait for launcher");
        assert!(termination.status.success());
        assert!(termination.path_accesses.is_err());
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn execute_only_image_runs_with_seccomp_tracking() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("execute-only.c");
        let image = directory.path().join("execute-only");
        let output = directory.path().join("ran");
        fs::write(
            &source,
            r#"#include <fcntl.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  int fd = open(argv[1], O_WRONLY | O_CREAT | O_TRUNC, 0600);
  if (fd < 0) return 3;
  if (write(fd, "ran", 3) != 3) return 4;
  close(fd);
  return 0;
}
"#,
        )
        .expect("write executable source");
        assert!(
            std::process::Command::new("cc")
                .arg("-static")
                .arg(&source)
                .arg("-o")
                .arg(&image)
                .status()
                .expect("compile executable")
                .success()
        );
        fs::set_permissions(&image, fs::Permissions::from_mode(0o111))
            .expect("make image execute-only");
        // Privileged test runners can read mode-0111 files regardless of mode.
        if fs::File::open(&image).is_ok() {
            return;
        }

        let mut command = super::Command::new(&image);
        command.arg(&output);
        let child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn execute-only image");
        // The seccomp handler cannot inspect an unreadable script line, so
        // collection fails closed even though the kernel ran the image.
        assert!(child.wait_handle.await.is_err());
        assert_eq!(fs::read(&output).expect("image ran"), b"ran");
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn path_search_exec_runs_plain_text_through_tracked_shell() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("shell-fallback.c");
        let executable = directory.path().join("shell-fallback");
        let script = directory.path().join("plain");
        let script_input = directory.path().join("plain.data");
        fs::write(
            &source,
            r#"#include <string.h>
#include <unistd.h>
int main(int argc, char **argv) {
  if (argc != 2) return 2;
  if (strcmp(argv[1], "execvp") == 0) {
    char *args[] = {"plain", "proof", 0};
    execvp("plain", args);
  } else {
    execlp("plain", "plain", "proof", (char *)0);
  }
  return 42;
}
"#,
        )
        .expect("write fixture");
        fs::write(
            &script,
            "IFS= read -r value < \"$0.data\"\nprintf 'fallback:%s:%s\\n' \"$1\" \"$value\"\n",
        )
        .expect("write plain text script");
        fs::write(&script_input, "tracked\n").expect("write script input");
        fs::set_permissions(&script, fs::Permissions::from_mode(0o755))
            .expect("make script executable");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile fixture")
                .success()
        );

        for method in ["execvp", "execlp"] {
            let mut command = super::Command::new(&executable);
            command.arg(method).env("PATH", directory.path());
            command.stdout(Stdio::piped());
            let mut child = command
                .spawn(CancellationToken::new())
                .await
                .expect("spawn tracked fixture");
            let mut stdout = child.stdout.take().expect("capture script output");
            let termination = child.wait_handle.await.expect("wait for fixture");
            assert!(termination.status.success(), "{method} failed");
            let mut output = String::new();
            stdout
                .read_to_string(&mut output)
                .await
                .expect("read script output");
            assert_eq!(output, "fallback:proof:tracked\n", "{method} output");
            let accesses = termination.path_accesses.expect("complete trace");
            assert!(accesses.iter().any(|access| {
                access.mode.contains(super::AccessMode::READ)
                    && access.path.strip_path_prefix(&script_input, |path| {
                        path.is_ok_and(|remaining| remaining.as_os_str().is_empty())
                    })
            }));
        }
    }

    #[cfg(target_os = "linux")]
    #[tokio::test]
    async fn execl_accepts_more_than_stack_argument_capacity() {
        let directory = tempfile::tempdir().expect("create fixture directory");
        let source = directory.path().join("long-execl.c");
        let executable = directory.path().join("long-execl");
        let arguments = std::iter::repeat_n("\"x\"", 32)
            .collect::<Vec<_>>()
            .join(", ");
        fs::write(
            &source,
            format!(
                "#include <unistd.h>\nint main(void) {{ execl(\"/bin/echo\", \"echo\", \
                 {arguments}, (char *)0); return 42; }}\n"
            ),
        )
        .expect("write fixture");
        assert!(
            std::process::Command::new("cc")
                .arg(&source)
                .arg("-o")
                .arg(&executable)
                .status()
                .expect("compile fixture")
                .success()
        );

        let mut command = super::Command::new(&executable);
        command.stdout(Stdio::piped());
        let mut child = command
            .spawn(CancellationToken::new())
            .await
            .expect("spawn tracked fixture");
        let mut stdout = child.stdout.take().expect("capture exec output");
        let termination = child.wait_handle.await.expect("wait for fixture");
        assert!(termination.status.success());
        let mut output = String::new();
        stdout
            .read_to_string(&mut output)
            .await
            .expect("read exec output");
        assert_eq!(output.split_whitespace().count(), 32);
    }
}
