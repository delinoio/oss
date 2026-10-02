#![cfg(unix)]

use std::{
    fs,
    io::{BufRead, BufReader, Read},
    os::unix::{ffi::OsStrExt, fs::symlink},
    path::Path,
    process::{Child, Command, Stdio},
    sync::mpsc::{self, Receiver},
    thread,
    time::{Duration, Instant},
};

struct WatchChild {
    child: Child,
    runs: Receiver<()>,
    errors: Option<thread::JoinHandle<Vec<u8>>>,
}

impl WatchChild {
    fn start(root: &Path, reader: &Path, input: &Path) -> Self {
        let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(["fspy", "autowatch", "--root"])
            .arg(root)
            .args(["--include", "**", "--debounce", "50ms", "--"])
            .arg(reader)
            .arg(input)
            .current_dir(root)
            .env_remove("RUST_LOG")
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let stdout = child.stdout.take().unwrap();
        let mut stderr = child.stderr.take().unwrap();
        let (tx, runs) = mpsc::channel();
        thread::spawn(move || {
            for line in BufReader::new(stdout).lines() {
                if line.unwrap() == "DONE" {
                    let _ = tx.send(());
                }
            }
        });
        let errors = thread::spawn(move || {
            let mut bytes = Vec::new();
            stderr.read_to_end(&mut bytes).unwrap();
            bytes
        });
        Self {
            child,
            runs,
            errors: Some(errors),
        }
    }

    fn wait_for_run(&mut self) {
        if self.runs.recv_timeout(Duration::from_secs(30)).is_err() {
            self.stop();
            let errors = self.errors.take().unwrap().join().unwrap();
            panic!(
                "autowatch missed input change: {}",
                String::from_utf8_lossy(&errors)
            );
        }
    }

    fn settle(&self) {
        // A run prints before discovery finishes. Wait for quiet output before
        // making the next controlled edit, including after dependency handoff.
        let deadline = Instant::now() + Duration::from_secs(10);
        while self.runs.recv_timeout(Duration::from_secs(1)).is_ok() {
            assert!(Instant::now() < deadline, "watcher did not settle");
        }
    }

    fn stop(&mut self) -> std::process::ExitStatus {
        if let Some(status) = self.child.try_wait().unwrap() {
            return status;
        }
        // SAFETY: this PID belongs to the test-owned watcher process.
        unsafe { libc::kill(self.child.id() as i32, libc::SIGINT) };
        let deadline = Instant::now() + Duration::from_secs(15);
        loop {
            if let Some(status) = self.child.try_wait().unwrap() {
                return status;
            }
            if Instant::now() >= deadline {
                self.child.kill().unwrap();
                return self.child.wait().unwrap();
            }
            thread::sleep(Duration::from_millis(20));
        }
    }
}

impl Drop for WatchChild {
    fn drop(&mut self) {
        self.stop();
    }
}

#[test]
fn equivalent_prefix_alias_replacement_discards_the_obsolete_target() {
    let directory = tempfile::tempdir().unwrap();
    let base = directory.path().canonicalize().unwrap();
    let source = base.join("reader.c");
    let reader = base.join("reader");
    fs::write(
        &source,
        br#"
#include <fcntl.h>
#include <stdio.h>
#include <unistd.h>
int main(int argc, char **argv) {
    if (argc != 2) return 2;
    int fd = open(argv[1], O_RDONLY);
    if (fd < 0) return 3;
    char byte;
    while (read(fd, &byte, 1) > 0) {}
    close(fd);
    puts("DONE");
    fflush(stdout);
    return 0;
}
"#,
    )
    .unwrap();
    let compiled = Command::new("cc")
        .args(["-O2", "-o"])
        .arg(&reader)
        .arg(&source)
        .output()
        .unwrap();
    assert!(
        compiled.status.success(),
        "{}",
        String::from_utf8_lossy(&compiled.stderr)
    );
    let root = base.join("root");
    fs::create_dir(&root).unwrap();
    symlink("root", base.join("outer")).unwrap();
    symlink("outer", base.join("nested")).unwrap();
    symlink(&base, base.join("parent-prefix")).unwrap();
    let mut cases = vec![
        (root.clone(), root.clone()),
        (root.clone(), base.join("outer")),
        (base.join("outer"), base.join("outer")),
        (root.clone(), base.join("nested")),
        (root.clone(), base.join("parent-prefix/root")),
    ];
    #[cfg(target_os = "macos")]
    {
        // Use the OS-provided /tmp -> /private/tmp alias explicitly.
        let tmp = tempfile::Builder::new()
            .prefix("clibox-watch-")
            .tempdir_in("/tmp")
            .unwrap();
        let canonical = tmp.path().canonicalize().unwrap();
        exercise_alias_replacement(&canonical, tmp.path(), &reader, &base);
    }
    for (selected_root, logical_prefix) in cases.drain(..) {
        exercise_alias_replacement(&selected_root, &logical_prefix, &reader, &base);
    }
    fs::create_dir(base.join("outside")).unwrap();
    fs::write(base.join("outside/input"), b"outside").unwrap();
    symlink("outside", base.join("outside-prefix")).unwrap();
    let mut watcher = WatchChild::start(&root, &reader, &base.join("outside-prefix/input"));
    watcher.wait_for_run();
    let deadline = Instant::now() + Duration::from_secs(30);
    let status = loop {
        if let Some(status) = watcher.child.try_wait().unwrap() {
            break status;
        }
        assert!(
            Instant::now() < deadline,
            "outside access gained watch authority"
        );
        thread::sleep(Duration::from_millis(20));
    };
    assert_eq!(status.code(), Some(1));
    let errors = watcher.errors.take().unwrap().join().unwrap();
    assert!(String::from_utf8_lossy(&errors).contains("no_watchable_input"));
}

fn exercise_alias_replacement(root: &Path, prefix: &Path, reader: &Path, base: &Path) {
    fs::write(root.join("one"), b"one").unwrap();
    fs::write(root.join("two"), b"two").unwrap();
    let alias = root.join("current");
    if alias.is_symlink() {
        fs::remove_file(&alias).unwrap();
    }
    symlink("one", &alias).unwrap();
    let input = prefix.join("current");

    // Check the real native record as well as watch behavior. Prefix
    // normalization must not rewrite the lossless caller path in that record.
    let record = base.join("record.ndjson");
    if record.exists() {
        fs::remove_file(&record).unwrap();
    }
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["fspy", "record", "--root"])
        .arg(root)
        .args(["--output"])
        .arg(&record)
        .arg("--")
        .arg(reader)
        .arg(&input)
        .current_dir(root)
        .env_remove("RUST_LOG")
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let raw = fs::read(&record).unwrap();
    use clibox_fspy::record::{parse, NativePath, DEFAULT_BYTE_LIMIT, DEFAULT_EVENT_LIMIT};
    let captured = parse(raw.as_slice(), DEFAULT_EVENT_LIMIT, DEFAULT_BYTE_LIMIT).unwrap();
    let expected = NativePath::UnixBytes(input.as_os_str().as_bytes().to_vec());
    assert!(
        captured.operations.iter().any(|pair| pair
            .start
            .paths
            .iter()
            .any(|path| path.logical == expected)),
        "native record lost the caller's logical path"
    );

    let mut watcher = WatchChild::start(root, reader, &input);
    watcher.wait_for_run();
    watcher.settle();
    symlink("two", root.join("replacement")).unwrap();
    fs::rename(root.join("replacement"), &alias).unwrap();
    watcher.wait_for_run();
    watcher.settle();
    fs::write(root.join("one"), b"old target changed").unwrap();
    assert!(
        watcher.runs.recv_timeout(Duration::from_secs(2)).is_err(),
        "obsolete target still watched"
    );
    assert!(
        watcher.child.try_wait().unwrap().is_none(),
        "watcher exited before cancellation"
    );
    fs::write(root.join("two"), b"new target changed").unwrap();
    watcher.wait_for_run();
    watcher.settle();
    let status = watcher.stop();
    let errors = watcher.errors.take().unwrap().join().unwrap();
    assert_eq!(
        status.code(),
        Some(130),
        "{}",
        String::from_utf8_lossy(&errors)
    );
}
