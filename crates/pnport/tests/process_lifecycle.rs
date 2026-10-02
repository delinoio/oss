// SPDX-License-Identifier: Apache-2.0
#![cfg(any(target_os = "macos", target_os = "linux"))]

use std::{
    fs,
    io::Write,
    os::unix::{
        fs::MetadataExt,
        process::{CommandExt, ExitStatusExt},
    },
    process::{Child, Command, Output, Stdio},
    time::{Duration, Instant},
};

use pnport::cache::{Cache, Operation, State};
use serde_json::json;

fn pnport_binary() -> std::ffi::OsString {
    // Repeat the same lifecycle conformance against an explicitly installed
    // native archive as well as Cargo's development executable.
    std::env::var_os("PNPORT_TEST_BINARY").unwrap_or_else(|| env!("CARGO_BIN_EXE_pnport").into())
}

struct Fixture {
    root: tempfile::TempDir,
    child: Option<Child>,
    pids: Vec<i32>,
}

impl Fixture {
    fn new(mode: &str, static_binary: bool) -> Self {
        let root = tempfile::tempdir().unwrap();
        let data = json!({
            "enableTopLevelFallback": false,
            "ignorePatternData": null,
            "dependencyTreeRoots": [{"name":"root", "reference":"workspace:."}],
            "fallbackPool": [], "fallbackExclusionList": [],
            "packageRegistryData": [
                [null, [[null, {"packageLocation":"./", "packageDependencies":[["dep","npm:1"]], "linkType":"SOFT", "discardFromLookup":true}]]],
                ["root", [["workspace:.", {"packageLocation":"./", "packageDependencies":[["dep","npm:1"]], "linkType":"SOFT"}]]],
                ["dep", [["npm:1", {"packageLocation":"./cache.zip/node_modules/dep/", "packageDependencies":[], "linkType":"HARD"}]]]
            ]
        });
        fs::write(
            root.path().join(".pnp.data.json"),
            serde_json::to_vec(&data).unwrap(),
        )
        .unwrap();
        fs::write(
            root.path().join(".pnp.cjs"),
            "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");",
        )
        .unwrap();
        let binary = root.path().join("tree");
        let mut compiler = Command::new("cc");
        if static_binary {
            compiler.arg("-static");
        }
        assert!(compiler
            .arg("-pthread")
            .arg(concat!(env!("CARGO_MANIFEST_DIR"), "/tests/process-tree.c"))
            .arg("-o")
            .arg(&binary)
            .status()
            .unwrap()
            .success());
        let mut archive =
            zip::ZipWriter::new(fs::File::create(root.path().join("cache.zip")).unwrap());
        archive
            .start_file(
                "node_modules/dep/file.txt",
                zip::write::SimpleFileOptions::default().unix_permissions(0o644),
            )
            .unwrap();
        archive.write_all(b"package bytes").unwrap();
        archive
            .start_file(
                "node_modules/dep/bin/tree",
                zip::write::SimpleFileOptions::default().unix_permissions(0o755),
            )
            .unwrap();
        archive.write_all(&fs::read(&binary).unwrap()).unwrap();
        archive.finish().unwrap();
        let child = Command::new(pnport_binary())
            .current_dir(root.path())
            .args(["--cache-dir"])
            .arg(root.path().join("store"))
            .args(["run", "--"])
            .arg(binary)
            .args(["root", mode])
            // Test-owned group isolation must not signal the Cargo harness.
            .process_group(0)
            .stdout(Stdio::null())
            .stderr(Stdio::piped())
            .spawn()
            .unwrap();
        Self {
            root,
            child: Some(child),
            pids: Vec::new(),
        }
    }

    fn ready(&mut self) {
        let deadline = Instant::now() + Duration::from_secs(15);
        for role in ["root", "middle", "leaf"] {
            loop {
                if let Ok(value) = fs::read_to_string(self.root.path().join(format!("{role}.pid")))
                {
                    if let Ok(pid) = value.parse::<i32>() {
                        self.pids.push(pid);
                        break;
                    }
                }
                if self.child.as_mut().unwrap().try_wait().unwrap().is_some() {
                    let output = self.child.take().unwrap().wait_with_output().unwrap();
                    panic!(
                        "fixture initialization failed: {}",
                        String::from_utf8_lossy(&output.stderr)
                    );
                }
                assert!(
                    Instant::now() < deadline,
                    "owned fixture did not become ready"
                );
                std::thread::sleep(Duration::from_millis(10));
            }
        }
    }

    fn signal(&self, signal: i32) {
        assert_eq!(
            unsafe { libc::kill(self.child.as_ref().unwrap().id() as i32, signal) },
            0
        );
    }

    fn stopped(&mut self) -> Output {
        let deadline = Instant::now() + Duration::from_secs(12);
        while self.child.as_mut().unwrap().try_wait().unwrap().is_none() {
            assert!(
                Instant::now() < deadline,
                "supervisor exceeded the cleanup bound"
            );
            std::thread::sleep(Duration::from_millis(10));
        }
        while self
            .pids
            .iter()
            .any(|pid| unsafe { libc::kill(*pid, 0) } == 0)
        {
            assert!(
                Instant::now() < deadline,
                "owned descendants survived cleanup or were not reaped"
            );
            std::thread::sleep(Duration::from_millis(10));
        }
        #[cfg(target_os = "macos")]
        while unsafe { libc::kill(-self.group(), 0) } == 0 {
            assert!(
                Instant::now() < deadline,
                "the private guardian survived cleanup"
            );
            std::thread::sleep(Duration::from_millis(10));
        }
        self.pids.clear();
        self.child.take().unwrap().wait_with_output().unwrap()
    }

    #[cfg(target_os = "macos")]
    fn group(&self) -> i32 {
        fs::read_to_string(self.root.path().join("root.group"))
            .unwrap()
            .parse()
            .unwrap()
    }

    fn cache(&self) -> Cache {
        Cache::open(self.root.path().join("store")).unwrap()
    }

    fn assert_active(&self) {
        let entries = self.cache().entries(Operation::Clean).unwrap();
        assert!(!entries.is_empty());
        assert!(
            entries
                .iter()
                .all(|entry| matches!(entry.state, State::Active)),
            "active execution lost its cache lease"
        );
    }

    fn assert_released(&self) {
        let entries = self.cache().entries(Operation::List).unwrap();
        assert!(!entries.is_empty());
        assert!(
            entries
                .iter()
                .all(|entry| matches!(entry.state, State::Complete)),
            "stopped execution retained a cache lease"
        );
        assert!(self
            .cache()
            .entries(Operation::Clean)
            .unwrap()
            .iter()
            .all(|entry| matches!(entry.state, State::Removed)));
        assert!(self.cache().entries(Operation::List).unwrap().is_empty());
        assert!(!self.root.path().join("node_modules").exists());
    }

    fn assert_signals(&self, signal: i32) {
        for role in ["root", "middle", "leaf"] {
            assert_eq!(
                fs::read(self.root.path().join(format!("{role}.signal"))).unwrap(),
                [signal as u8],
                "wrong descendant termination signal"
            );
        }
    }
}

impl Drop for Fixture {
    fn drop(&mut self) {
        // Verify the native image again before cleaning a failed fixture, so
        // a recycled PID cannot signal an unrelated process on the host.
        for pid in &self.pids {
            if fixture_image(*pid, &self.root.path().join("tree")) {
                unsafe {
                    libc::kill(*pid, libc::SIGKILL);
                }
            }
        }
        if let Some(mut child) = self.child.take() {
            let _ = child.kill();
            let _ = child.wait();
        }
    }
}

fn cancellation(signal: i32) {
    let mut fixture = Fixture::new("normal", false);
    fixture.ready();
    fixture.assert_active();
    fixture.signal(signal);
    let output = fixture.stopped();
    assert_eq!(output.status.code(), Some(128 + signal));
    fixture.assert_signals(signal);
    fixture.assert_released();
}

#[test]
fn interrupt_reaches_the_owned_tree_and_releases_leases() {
    cancellation(libc::SIGINT);
}
#[test]
fn termination_reaches_the_owned_tree_and_releases_leases() {
    cancellation(libc::SIGTERM);
}
#[test]
fn hangup_reaches_the_owned_tree_and_releases_leases() {
    cancellation(libc::SIGHUP);
}

#[test]
fn spawnp_searches_parent_virtual_path_and_restores_replacement_environment() {
    let mut fixture = Fixture::new("spawnp", false);
    fixture.ready();
    fixture.assert_active();
    fixture.signal(libc::SIGTERM);
    assert_eq!(fixture.stopped().status.code(), Some(143));
    fixture.assert_signals(libc::SIGTERM);
    fixture.assert_released();
}

#[test]
fn concurrent_fork_and_child_callbacks_preserve_the_virtual_view() {
    let mut fixture = Fixture::new("fork-stress", false);
    let output = fixture.stopped();
    assert!(
        output.status.success(),
        "{}: {}",
        output.status,
        String::from_utf8_lossy(&output.stderr)
    );
    fixture.assert_released();
}

#[test]
fn unresponsive_tree_is_killed_after_the_five_second_grace() {
    let mut fixture = Fixture::new("ignore", false);
    fixture.ready();
    fixture.assert_active();
    let start = Instant::now();
    fixture.signal(libc::SIGTERM);
    assert_eq!(fixture.stopped().status.code(), Some(143));
    assert!(
        start.elapsed() >= Duration::from_secs(5),
        "shutdown grace was shortened"
    );
    fixture.assert_released();
}

#[test]
fn normal_root_exit_stops_surviving_descendants_and_preserves_status() {
    let mut fixture = Fixture::new("exit", false);
    fixture.ready();
    assert_eq!(fixture.stopped().status.code(), Some(23));
    fixture.assert_released();
}

#[test]
fn killed_supervisor_stops_the_tree_and_releases_leases() {
    let mut fixture = Fixture::new("normal", false);
    fixture.ready();
    fixture.assert_active();
    fixture.signal(libc::SIGKILL);
    assert_eq!(fixture.stopped().status.signal(), Some(libc::SIGKILL));
    fixture.assert_released();
}

#[test]
fn graph_invalidation_stops_the_tree_and_releases_leases() {
    let mut fixture = Fixture::new("normal", false);
    fixture.ready();
    fixture.assert_active();
    fs::write(fixture.root.path().join(".pnp.data.json"), b"{}").unwrap();
    let output = fixture.stopped();
    assert_eq!(output.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_GRAPH_CHANGED"));
    fixture.assert_signals(libc::SIGTERM);
    fixture.assert_released();
}

#[test]
fn archive_invalidation_stops_the_tree_and_releases_leases() {
    let mut fixture = Fixture::new("normal", false);
    fixture.ready();
    fixture.assert_active();
    fs::write(fixture.root.path().join("cache.zip"), b"changed").unwrap();
    let output = fixture.stopped();
    assert_eq!(output.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_GRAPH_CHANGED"));
    fixture.assert_signals(libc::SIGTERM);
    fixture.assert_released();
}

#[cfg(target_os = "linux")]
#[test]
fn detached_static_tree_is_stopped_and_reaped() {
    let mut fixture = Fixture::new("detached", true);
    fixture.ready();
    fixture.assert_active();
    fixture.signal(libc::SIGINT);
    assert_eq!(fixture.stopped().status.code(), Some(130));
    fixture.assert_signals(libc::SIGINT);
    fixture.assert_released();
}

#[cfg(target_os = "linux")]
#[test]
fn killed_supervisor_stops_detached_static_descendants() {
    let mut fixture = Fixture::new("detached", true);
    fixture.ready();
    fixture.assert_active();
    fixture.signal(libc::SIGKILL);
    assert_eq!(fixture.stopped().status.signal(), Some(libc::SIGKILL));
    fixture.assert_released();
}

fn fixture_image(pid: i32, expected: &std::path::Path) -> bool {
    #[cfg(target_os = "linux")]
    let actual = std::path::PathBuf::from(format!("/proc/{pid}/exe"));
    #[cfg(target_os = "macos")]
    let actual = {
        use std::os::unix::ffi::OsStrExt;
        let mut path = [0; libc::PROC_PIDPATHINFO_MAXSIZE as usize];
        if unsafe { libc::proc_pidpath(pid, path.as_mut_ptr().cast(), path.len() as u32) } <= 0 {
            return false;
        }
        let Some(length) = path.iter().position(|byte| *byte == 0) else {
            return false;
        };
        std::path::PathBuf::from(std::ffi::OsStr::from_bytes(&path[..length]))
    };
    match (fs::metadata(actual), fs::metadata(expected)) {
        (Ok(actual), Ok(expected)) => {
            actual.dev() == expected.dev() && actual.ino() == expected.ino()
        }
        _ => false,
    }
}

#[cfg(target_os = "macos")]
#[test]
fn private_owner_rejects_direct_invocation_and_an_untrusted_peer() {
    use std::os::{fd::AsRawFd, unix::net::UnixStream};
    for inherited in [false, true] {
        let (_peer, socket) = UnixStream::pair().unwrap();
        let fd = socket.as_raw_fd();
        let mut command = Command::new(pnport_binary());
        command
            .arg("__pnport_macos_owner")
            .process_group(0)
            .env_remove("PNPORT_MACOS_OWNER_FD");
        if inherited {
            command.env("PNPORT_MACOS_OWNER_FD", fd.to_string());
            unsafe {
                command.pre_exec(move || {
                    if libc::fcntl(fd, libc::F_SETFD, 0) < 0 {
                        return Err(std::io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
        }
        let mut child = command.spawn().unwrap();
        let deadline = Instant::now() + Duration::from_secs(2);
        loop {
            if let Some(status) = child.try_wait().unwrap() {
                assert_eq!(status.code(), Some(125));
                break;
            }
            if Instant::now() >= deadline {
                child.kill().unwrap();
                child.wait().unwrap();
                panic!("An untrusted private owner must fail promptly");
            }
            std::thread::sleep(Duration::from_millis(10));
        }
    }
}

#[cfg(target_os = "macos")]
#[test]
fn guardian_failure_stops_the_owned_tree_and_fails_closed() {
    let mut fixture = Fixture::new("normal", false);
    fixture.ready();
    fixture.assert_active();
    assert_eq!(unsafe { libc::kill(fixture.group(), libc::SIGKILL) }, 0);
    let output = fixture.stopped();
    assert_eq!(output.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_CLEANUP_FAILED"));
    fixture.assert_released();
}

#[cfg(target_os = "macos")]
#[test]
fn killed_supervisor_retains_grace_then_kills_unresponsive_descendants() {
    let mut fixture = Fixture::new("ignore", false);
    fixture.ready();
    fixture.assert_active();
    let start = Instant::now();
    fixture.signal(libc::SIGKILL);
    assert_eq!(fixture.stopped().status.signal(), Some(libc::SIGKILL));
    assert!(start.elapsed() >= Duration::from_secs(5));
    fixture.assert_released();
}
