#![cfg(target_os = "macos")]

use std::{
    fs,
    io::{BufRead, BufReader},
    os::unix::fs::{symlink, MetadataExt},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    sync::mpsc,
    time::{Duration, Instant},
};

use clibox_fspy::record::{self, FileIdentity, NativePath, Operation, PathClass};

struct Fixture {
    directory: tempfile::TempDir,
    root: PathBuf,
    executable: PathBuf,
}

impl Fixture {
    fn new() -> Self {
        let directory = tempfile::tempdir().unwrap();
        let base = directory.path().canonicalize().unwrap();
        let root = base.join("project");
        fs::create_dir(&root).unwrap();
        fs::write(root.join("data"), b"fixture").unwrap();
        fs::write(base.join("external"), b"external bytes").unwrap();
        for (name, target) in [
            ("self-link", "self-link"),
            ("dangling", "absent"),
            ("link-in", "data"),
            ("link-out", "../external"),
        ] {
            symlink(target, root.join(name)).unwrap();
        }
        let source = base.join("nofollow.c");
        let executable = base.join("nofollow");
        fs::write(
            &source,
            r#"
#include <errno.h>
#include <fcntl.h>
#include <limits.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
static void probe(int operation, int dirfd, const char *path) {
    char output[64]; struct stat st;
    errno = EDOM;
    long result = operation == 0 ? readlink(path, output, sizeof output)
        : operation == 1 ? lstat(path, &st)
        : operation == 2 ? readlinkat(dirfd, path, output, sizeof output)
        : fstatat(dirfd, path, &st, AT_SYMLINK_NOFOLLOW);
    int error = errno;
    printf("%d %s %ld %d\n", operation, path, result, error);
    fflush(stdout);
}
int main(int argc, char **argv) {
    if (argc != 2) return 2;
    if (!strcmp(argv[1], "matrix")) {
        const char *links[] = {"self-link", "dangling", "link-in", "link-out"};
        int dirfd = open(".", O_RDONLY);
        if (dirfd < 0) return 3;
        for (int i = 0; i < 4; ++i)
            for (int op = 0; op < 4; ++op) probe(op, dirfd, links[i]);
        probe(0, dirfd, "data");
        probe(1, dirfd, "missing");
        probe(2, INT_MAX, "link-in");
        probe(3, INT_MAX, "link-in");
        struct stat st;
        if (stat("link-in", &st) != 0 || fstatat(dirfd, "link-in", &st, 0) != 0) return 3;
        int fd = open("link-in", O_RDONLY);
        char bytes[7];
        if (fd < 0 || read(fd, bytes, sizeof bytes) != 7 || memcmp(bytes, "fixture", 7)) return 3;
        close(fd); close(dirfd);
        return 0;
    }
    probe(0, AT_FDCWD, "link-out");
    if (!strcmp(argv[1], "fail")) { fprintf(stderr, "fixture failure\n"); return 3; }
    return 0;
}
"#,
        )
        .unwrap();
        let compile = Command::new("cc")
            .args(["-O2", "-o"])
            .arg(&executable)
            .arg(&source)
            .output()
            .unwrap();
        assert!(compile.status.success(), "{compile:?}");
        Self {
            directory,
            root,
            executable,
        }
    }

    fn cli(&self, workflow: &str) -> Command {
        let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
        command
            .args(["fspy", workflow, "--root"])
            .arg(&self.root)
            .args(["--timeout", "30s"])
            .current_dir(&self.root);
        command
    }
}

#[test]
fn nofollow_calls_keep_link_identity_and_native_results() {
    let fixture = Fixture::new();
    let baseline = Command::new(&fixture.executable)
        .arg("matrix")
        .current_dir(&fixture.root)
        .output()
        .unwrap();
    assert!(baseline.status.success(), "{baseline:?}");
    let report = fixture.directory.path().join("trace.ndjson");
    let traced = fixture
        .cli("record")
        .arg("--output")
        .arg(&report)
        .arg("--")
        .arg(&fixture.executable)
        .arg("matrix")
        .output()
        .unwrap();
    assert!(traced.status.success(), "{traced:?}");
    // Record forwards child stdout to stderr to reserve stdout for reports.
    assert_eq!(
        traced.stderr, baseline.stdout,
        "native result/errno changed"
    );
    let captured = record::parse(
        BufReader::new(fs::File::open(&report).unwrap()),
        record::DEFAULT_EVENT_LIMIT,
        record::DEFAULT_BYTE_LIMIT,
    )
    .unwrap();
    assert!(captured.summary.complete);
    for name in ["self-link", "dangling", "link-in", "link-out"] {
        let metadata = fs::symlink_metadata(fixture.root.join(name)).unwrap();
        let expected = FileIdentity::Inode {
            device: metadata.dev(),
            inode: metadata.ino(),
        };
        let relative = NativePath::UnixBytes(name.as_bytes().to_vec());
        let pairs = captured
            .operations
            .iter()
            .filter(|pair| {
                pair.start.operation == Operation::Metadata
                    && pair.start.paths.iter().any(|path| {
                        path.project_relative.as_ref() == Some(&relative)
                            && path.class == PathClass::Project
                            && path.identity == Some(expected)
                    })
                    && pair.completion.native_error.is_none()
            })
            .collect::<Vec<_>>();
        assert_eq!(
            pairs.len(),
            4,
            "all four nofollow APIs must describe {name}"
        );
    }
    for error in [libc::EINVAL, libc::ENOENT, libc::EBADF] {
        assert!(captured.operations.iter().any(|pair| {
            pair.start.operation == Operation::Metadata
                && pair.completion.native_error == Some(error)
                && pair.completion.native_result == -1
        }));
    }
    let metadata = fs::metadata(fixture.root.join("data")).unwrap();
    let target = FileIdentity::Inode {
        device: metadata.dev(),
        inode: metadata.ino(),
    };
    for operation in [Operation::Metadata, Operation::Open, Operation::Read] {
        assert!(captured.operations.iter().any(|pair| {
            pair.start.operation == operation
                && pair.completion.native_error.is_none()
                && pair.start.paths.iter().any(|path| {
                    path.project_relative == Some(NativePath::UnixBytes(b"data".to_vec()))
                        && path.identity == Some(target)
                })
        }));
    }
    assert!(!captured.operations.iter().any(|pair| {
        pair.start
            .paths
            .iter()
            .any(|path| path.logical == native(&fixture.root.parent().unwrap().join("external")))
    }));
}

fn native(path: &Path) -> NativePath {
    use std::os::unix::ffi::OsStrExt;
    NativePath::UnixBytes(path.as_os_str().as_bytes().to_vec())
}

#[test]
fn autowatch_reruns_when_external_target_link_entry_changes() {
    let fixture = Fixture::new();
    let mut child = fixture
        .cli("autowatch")
        .args(["--include", "**", "--debounce", "50ms", "--"])
        .arg(&fixture.executable)
        .arg("readlink")
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let (tx, rx) = mpsc::channel();
    let stdout = child.stdout.take().unwrap();
    let reader = std::thread::spawn(move || {
        for line in BufReader::new(stdout).lines() {
            if tx.send(line.unwrap()).is_err() {
                break;
            }
        }
    });
    let first = rx.recv_timeout(Duration::from_secs(30));
    // Allow the completed trace to install its idle watch before mutation.
    std::thread::sleep(Duration::from_millis(500));
    fs::write(
        fixture.root.parent().unwrap().join("external"),
        b"changed target bytes",
    )
    .unwrap();
    let target_changed = rx.recv_timeout(Duration::from_millis(700));
    symlink("../other-external", fixture.root.join("replacement")).unwrap();
    fs::rename(
        fixture.root.join("replacement"),
        fixture.root.join("link-out"),
    )
    .unwrap();
    let second = rx.recv_timeout(Duration::from_secs(30));
    // SAFETY: this is the live test-owned child and SIGTERM requests bounded
    // cleanup.
    unsafe { libc::kill(child.id() as i32, libc::SIGTERM) };
    let deadline = Instant::now() + Duration::from_secs(8);
    while child.try_wait().unwrap().is_none() && Instant::now() < deadline {
        std::thread::sleep(Duration::from_millis(20));
    }
    let cleanup_finished = child.try_wait().unwrap().is_some();
    if !cleanup_finished {
        child.kill().unwrap();
    }
    let output = child.wait_with_output().unwrap();
    reader.join().unwrap();
    assert!(cleanup_finished, "autowatch cleanup timed out: {output:?}");
    assert!(
        first.is_ok(),
        "initial dependency discovery failed: {output:?}"
    );
    assert!(
        target_changed.is_err(),
        "external target bytes became a dependency"
    );
    assert!(second.is_ok(), "link replacement did not rerun: {output:?}");
    assert_ne!(first.unwrap(), second.unwrap());
}

#[test]
fn min_repro_still_rejects_selected_escaping_link() {
    let fixture = Fixture::new();
    // Isolate containment rejection from unrelated cyclic-link snapshot errors.
    fs::remove_file(fixture.root.join("self-link")).unwrap();
    fs::remove_file(fixture.root.join("dangling")).unwrap();
    let bundle = fixture.directory.path().join("bundle");
    let output = fixture
        .cli("min-repro")
        .args([
            "--include",
            "link-out",
            "--expect-exit",
            "3",
            "--expect-stderr",
            "fixture failure",
            "--bundle-dir",
        ])
        .arg(&bundle)
        .arg("--")
        .arg(&fixture.executable)
        .arg("fail")
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(1), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("external_link"),
        "{output:?}"
    );
    assert!(!bundle.exists());
}
