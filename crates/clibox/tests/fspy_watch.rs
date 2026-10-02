#![cfg(any(target_os = "linux", target_os = "macos"))]

use std::{
    fs,
    io::{BufRead, BufReader, Read},
    os::unix::fs::symlink,
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    sync::{
        mpsc::{self, Receiver},
        Mutex, OnceLock,
    },
    thread,
    time::{Duration, Instant},
};

// Fresh native test images also pass through platform injection admission;
// allow startup under loaded hosts without changing production watch timing.
const RUN_TIMEOUT: Duration = Duration::from_secs(30);

static WATCH_TEST_LOCK: OnceLock<Mutex<()>> = OnceLock::new();

fn watch_test_guard() -> std::sync::MutexGuard<'static, ()> {
    WATCH_TEST_LOCK
        .get_or_init(|| Mutex::new(()))
        .lock()
        .unwrap()
}

struct Watcher {
    child: Child,
    lines: Receiver<String>,
    stderr: Option<thread::JoinHandle<String>>,
    runs: usize,
    active: bool,
}

impl Watcher {
    fn start(root: &Path, input: &Path) -> Self {
        let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(["fspy", "autowatch", "--root"])
            .arg(root)
            .args(["--include", "**/input", "--debounce", "50ms", "--"])
            .arg(std::env::current_exe().unwrap())
            .args(["--exact", "watch_worker", "--nocapture"])
            .env("CLIBOX_FSPY_ANCESTOR_INPUT", input)
            .current_dir(root)
            .stdout(Stdio::piped())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        let (tx, lines) = mpsc::channel();
        let stdout = child.stdout.take().unwrap();
        thread::spawn(move || {
            for line in BufReader::new(stdout).lines() {
                if tx.send(line.unwrap()).is_err() {
                    break;
                }
            }
        });
        let mut stderr = child.stderr.take().unwrap();
        let stderr = thread::spawn(move || {
            let mut text = String::new();
            stderr.read_to_string(&mut text).unwrap();
            text
        });
        Self {
            child,
            lines,
            stderr: Some(stderr),
            runs: 0,
            active: false,
        }
    }

    fn observe(&mut self, duration: Duration) {
        let deadline = Instant::now() + duration;
        while Instant::now() < deadline {
            if let Ok(line) = self.lines.recv_timeout(Duration::from_millis(10)) {
                if line.contains("FSPY_WATCH_START") {
                    assert!(!self.active, "watch runs overlapped");
                    self.active = true;
                }
                if line.contains("FSPY_WATCH_DONE") {
                    assert!(self.active, "watch completion without a start");
                    self.active = false;
                    self.runs += 1;
                }
            }
            if let Some(status) = self.child.try_wait().unwrap() {
                let stderr = self.stderr.take().unwrap().join().unwrap();
                panic!("watcher exited unexpectedly: {status}: {stderr}");
            }
        }
    }

    fn wait_for_run(&mut self, before: usize) {
        let deadline = Instant::now() + RUN_TIMEOUT;
        while self.runs <= before && Instant::now() < deadline {
            self.observe(Duration::from_millis(20));
        }
        assert!(self.runs > before, "watcher missed the input change");
        self.observe(Duration::from_millis(700));
        assert!(!self.active);
    }

    fn remains_idle(&mut self, before: usize) {
        self.observe(Duration::from_millis(800));
        assert_eq!(self.runs, before, "unrelated input triggered a rerun");
        assert!(!self.active);
    }

    fn cancel(mut self) {
        // SAFETY: this PID still belongs to the live test-owned child.
        assert_eq!(
            unsafe { libc::kill(self.child.id() as i32, libc::SIGINT) },
            0
        );
        let deadline = Instant::now() + Duration::from_secs(10);
        loop {
            if let Some(status) = self.child.try_wait().unwrap() {
                assert_eq!(status.code(), Some(130));
                assert!(self.stderr.take().unwrap().join().unwrap().is_empty());
                break;
            }
            assert!(Instant::now() < deadline, "watch cancellation timed out");
            thread::sleep(Duration::from_millis(10));
        }
    }
}

impl Drop for Watcher {
    fn drop(&mut self) {
        if self.child.try_wait().unwrap().is_none() {
            // SAFETY: only the unreaped child owned by this fixture is signalled.
            unsafe { libc::kill(self.child.id() as i32, libc::SIGINT) };
            let deadline = Instant::now() + Duration::from_secs(10);
            while self.child.try_wait().unwrap().is_none() {
                if Instant::now() >= deadline {
                    let _ = self.child.kill();
                    let _ = self.child.wait();
                    break;
                }
                thread::sleep(Duration::from_millis(10));
            }
        }
    }
}

fn replace_link(root: &Path, link: &str, target: &str) {
    let replacement = root.join("replacement");
    symlink(target, &replacement).unwrap();
    fs::rename(replacement, root.join(link)).unwrap();
}

#[test]
fn watch_worker() {
    let Some(input) = std::env::var_os("CLIBOX_FSPY_ANCESTOR_INPUT") else {
        return;
    };
    println!("FSPY_WATCH_START");
    let result = fs::read(PathBuf::from(input));
    if let Some(root) = std::env::var_os("CLIBOX_FSPY_ANCESTOR_WRITE") {
        let root = PathBuf::from(root);
        symlink("two", root.join("child-replacement")).unwrap();
        fs::rename(root.join("child-replacement"), root.join("current")).unwrap();
        println!("FSPY_WATCH_WRITTEN");
        // Leave an interval for the parent's overlapping external replacement.
        thread::sleep(Duration::from_millis(500));
    }
    thread::sleep(Duration::from_millis(80));
    println!("FSPY_WATCH_DONE");
    if result.is_err() {
        std::process::exit(3);
    }
}

#[test]
fn autowatch_replaces_nested_ancestor_dependencies() {
    let _guard = watch_test_guard();
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().canonicalize().unwrap();
    for name in ["group-one", "group-two", "one", "two", "three"] {
        fs::create_dir(root.join(name)).unwrap();
    }
    for name in ["one", "two", "three"] {
        fs::write(root.join(name).join("input"), name).unwrap();
    }
    symlink("../one", root.join("group-one/nested")).unwrap();
    symlink("../two", root.join("group-two/nested")).unwrap();
    symlink("group-one", root.join("current")).unwrap();
    let mut watcher = Watcher::start(&root, &root.join("current/nested/input"));
    watcher.wait_for_run(0);

    let before = watcher.runs;
    for sibling in ["unrelated", "group-one/sibling", "one/sibling"] {
        fs::write(root.join(sibling), b"unrelated").unwrap();
    }
    watcher.remains_idle(before);

    replace_link(&root, "current", "group-two");
    watcher.wait_for_run(before);
    let before = watcher.runs;
    fs::write(root.join("one/input"), b"obsolete").unwrap();
    watcher.remains_idle(before);
    // A burst on the new target is debounced into one serial execution.
    for value in ["first", "second", "third"] {
        fs::write(root.join("two/input"), value).unwrap();
    }
    watcher.wait_for_run(before);
    assert_eq!(watcher.runs, before + 1);

    let before = watcher.runs;
    replace_link(&root, "group-two/nested", "../three");
    watcher.wait_for_run(before);
    let before = watcher.runs;
    fs::write(root.join("two/input"), b"obsolete").unwrap();
    watcher.remains_idle(before);
    fs::write(root.join("three/input"), b"current").unwrap();
    watcher.wait_for_run(before);

    let before = watcher.runs;
    fs::remove_file(root.join("current")).unwrap();
    watcher.wait_for_run(before);
    let before = watcher.runs;
    // A failed run must retain the last successful target dependency.
    fs::write(root.join("three/input"), b"retained after failure").unwrap();
    watcher.wait_for_run(before);
    let before = watcher.runs;
    symlink("group-one", root.join("current")).unwrap();
    watcher.wait_for_run(before);
    let before = watcher.runs;
    fs::write(root.join("three/input"), b"obsolete after recovery").unwrap();
    watcher.remains_idle(before);
    watcher.cancel();
}

#[test]
fn autowatch_missing_leaf_observes_ancestor_replacement() {
    let _guard = watch_test_guard();
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().canonicalize().unwrap();
    fs::create_dir(root.join("one")).unwrap();
    fs::create_dir(root.join("two")).unwrap();
    fs::write(root.join("two/input"), b"fixture").unwrap();
    symlink("one", root.join("current")).unwrap();
    let mut watcher = Watcher::start(&root, &root.join("current/input"));
    watcher.wait_for_run(0);
    let before = watcher.runs;
    replace_link(&root, "current", "two");
    watcher.wait_for_run(before);
    let before = watcher.runs;
    fs::write(root.join("one/input"), b"obsolete missing target").unwrap();
    watcher.remains_idle(before);
    fs::write(root.join("two/input"), b"updated").unwrap();
    watcher.wait_for_run(before);
    watcher.cancel();
}

#[test]
fn autowatch_direct_file_alias_control() {
    let _guard = watch_test_guard();
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().canonicalize().unwrap();
    for name in ["one", "two"] {
        fs::create_dir(root.join(name)).unwrap();
        fs::write(root.join(name).join("input"), b"fixture").unwrap();
    }
    symlink("one/input", root.join("current")).unwrap();
    let mut watcher = Watcher::start(&root, &root.join("current"));
    watcher.wait_for_run(0);
    let before = watcher.runs;
    replace_link(&root, "current", "two/input");
    watcher.wait_for_run(before);
    let before = watcher.runs;
    fs::write(root.join("one/input"), b"obsolete").unwrap();
    watcher.remains_idle(before);
    fs::write(root.join("two/input"), b"updated").unwrap();
    watcher.wait_for_run(before);
    watcher.cancel();
}

#[test]
fn autowatch_child_written_alias_with_external_change_fails_ambiguous() {
    let _guard = watch_test_guard();
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().canonicalize().unwrap();
    for name in ["one", "two", "three"] {
        fs::create_dir(root.join(name)).unwrap();
        fs::write(root.join(name).join("input"), b"fixture").unwrap();
    }
    symlink("one", root.join("current")).unwrap();
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["fspy", "autowatch", "--root"])
        .arg(&root)
        .args(["--include", "**/input", "--debounce", "50ms", "--"])
        .arg(std::env::current_exe().unwrap())
        .args(["--exact", "watch_worker", "--nocapture"])
        .env("CLIBOX_FSPY_ANCESTOR_INPUT", root.join("current/input"))
        .env("CLIBOX_FSPY_ANCESTOR_WRITE", &root)
        .current_dir(&root)
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let stdout = child.stdout.take().unwrap();
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        for line in BufReader::new(stdout).lines() {
            if line.unwrap().contains("FSPY_WATCH_WRITTEN") {
                let _ = tx.send(());
            }
        }
    });
    if rx.recv_timeout(RUN_TIMEOUT).is_err() {
        let _ = child.kill();
        let output = child.wait_with_output().unwrap();
        panic!(
            "child did not replace alias: {}",
            String::from_utf8_lossy(&output.stderr)
        );
    }
    replace_link(&root, "current", "three");
    let deadline = Instant::now() + RUN_TIMEOUT;
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            let _ = child.kill();
            let _ = child.wait();
            panic!("ambiguous watch did not terminate");
        }
        thread::sleep(Duration::from_millis(10));
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(String::from_utf8_lossy(&output.stderr).contains("self_write_ambiguity"));
}
