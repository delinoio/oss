// SPDX-License-Identifier: Apache-2.0
#![cfg(any(target_os = "macos", target_os = "linux"))]

use std::{
    fs,
    io::{Read, Write},
    os::{
        fd::{AsRawFd, FromRawFd},
        unix::{
            fs::MetadataExt,
            process::{CommandExt, ExitStatusExt},
        },
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
    fn project(static_binary: bool) -> tempfile::TempDir {
        Self::project_source(static_binary, "process-tree.c")
    }

    fn project_source(static_binary: bool, source: &str) -> tempfile::TempDir {
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
            .arg(
                std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
                    .join("tests")
                    .join(source)
            )
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
        for path in [
            "node_modules/dep/blocked/tree",
            "node_modules/dep/bin/noexec",
        ] {
            archive
                .start_file(
                    path,
                    zip::write::SimpleFileOptions::default().unix_permissions(0o644),
                )
                .unwrap();
            archive.write_all(b"not executable").unwrap();
        }
        archive.finish().unwrap();
        root
    }

    fn new(mode: &str, static_binary: bool) -> Self {
        let root = Self::project(static_binary);
        let binary = root.path().join("tree");
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

    fn terminal(mode: &str) -> (Self, fs::File) {
        let root = Self::project(false);
        // Controlled pre-launch failures exercise diagnostics before the
        // command group or preload exists. No native output is published.
        match mode {
            "missing-image" => fs::remove_file(root.path().join("tree")).unwrap(),
            "invalid-image" => {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(root.path().join("tree"), fs::Permissions::from_mode(0o600))
                    .unwrap();
            }
            _ => {}
        }
        let driver = root.path().join("terminal-driver");
        assert!(Command::new("cc")
            .arg(concat!(
                env!("CARGO_MANIFEST_DIR"),
                "/tests/terminal-driver.c"
            ))
            .arg("-o")
            .arg(&driver)
            .status()
            .unwrap()
            .success());
        let mut master = -1;
        let mut slave = -1;
        assert_eq!(
            unsafe {
                libc::openpty(
                    &mut master,
                    &mut slave,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                )
            },
            0
        );
        let master = unsafe { fs::File::from_raw_fd(master) };
        let slave = unsafe { fs::File::from_raw_fd(slave) };
        let mut settings = unsafe { std::mem::zeroed::<libc::termios>() };
        assert_eq!(
            unsafe { libc::tcgetattr(slave.as_raw_fd(), &mut settings) },
            0
        );
        settings.c_lflag |= libc::ICANON | libc::ISIG;
        settings.c_lflag &= !libc::ECHO;
        settings.c_cc[libc::VINTR] = 3;
        settings.c_cc[libc::VSUSP] = 26;
        assert_eq!(
            unsafe { libc::tcsetattr(slave.as_raw_fd(), libc::TCSANOW, &settings) },
            0
        );
        // The master is test-owned and must not keep the tty alive in the job.
        assert_eq!(
            unsafe { libc::fcntl(master.as_raw_fd(), libc::F_SETFD, libc::FD_CLOEXEC) },
            0
        );
        let mut command = Command::new(driver);
        if matches!(mode, "missing-image" | "invalid-image") {
            command.env("PNPORT_TEST_TERMINAL_DIAGNOSTICS", "1");
        }
        command
            .current_dir(root.path())
            .arg(pnport_binary())
            .arg(root.path().join("tree"))
            .arg(mode)
            .stdin(slave.try_clone().unwrap())
            .stdout(slave.try_clone().unwrap())
            .stderr(slave);
        unsafe {
            command.pre_exec(|| {
                #[cfg(target_os = "macos")]
                let request = libc::TIOCSCTTY as libc::c_ulong;
                #[cfg(target_os = "linux")]
                let request = libc::TIOCSCTTY;
                if libc::setsid() < 0 || libc::ioctl(0, request, 0) < 0 {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            });
        }
        let child = command.spawn().unwrap();
        (
            Self {
                root,
                child: Some(child),
                pids: Vec::new(),
            },
            master,
        )
    }

    fn wait_marker(&mut self, name: &str) {
        let deadline = Instant::now() + Duration::from_secs(15);
        while !self.root.path().join(name).is_file() {
            let status = self.child.as_mut().unwrap().try_wait().unwrap();
            assert!(
                status.is_none(),
                "terminal driver exited before {name}: {status:?}; diagnostics={}",
                self.terminal_diagnostics()
            );
            assert!(
                Instant::now() < deadline,
                "terminal job did not reach {name}; driver={}, launching={}, running={}, root={}, \
                 diagnostics={}",
                self.root.path().join("terminal.driver").is_file(),
                self.root.path().join("terminal.launching").is_file(),
                self.root.path().join("terminal.running").is_file(),
                self.root.path().join("root.pid").is_file(),
                self.terminal_diagnostics()
            );
            std::thread::sleep(Duration::from_millis(10));
        }
    }

    fn terminal_diagnostics(&self) -> serde_json::Value {
        let mut log = String::new();
        if let Ok(file) = fs::File::open(self.root.path().join("terminal.diagnostics")) {
            let _ = file.take(64 * 1024).read_to_string(&mut log);
        }
        let actions = [
            "command_prepared",
            "native_image_admitted",
            "macos_owner_started",
            "macos_root_admitted",
            "macos_owner_unavailable",
            "macos_owner_recovery",
            "spawn",
            "initialization_started",
            "root_injection_deadline",
            "macos_job_stopped",
            "macos_job_resumed",
            "macos_job_detached",
            "descendant_injection_deadline",
            "supervisor_failed",
            "native_failure_record",
            "macos_owner_cleanup_requested",
            "macos_owner_cleanup_acknowledged",
            "child_exit",
        ];
        let observed: Vec<_> = log
            .lines()
            .flat_map(|line| {
                actions
                    .iter()
                    .filter(move |action| line.contains(&format!("action=\"{action}\"")))
            })
            .take(128)
            .collect();
        let codes: Vec<_> = [
            "PNPORT_MANIFEST_MISSING",
            "PNPORT_MANIFEST_INVALID",
            "PNPORT_RESOLUTION_FAILED",
            "PNPORT_FILESYSTEM_CONFLICT",
            "PNPORT_INJECTION_FAILED",
            "PNPORT_CLEANUP_FAILED",
            "PNPORT_UNSUPPORTED_OPERATION",
            "PNPORT_GRAPH_CHANGED",
            "PNPORT_CACHE_FAILED",
            "PNPORT_ARCHIVE_CORRUPT",
            "PNPORT_COMMAND_NOT_FOUND",
            "PNPORT_COMMAND_NOT_EXECUTABLE",
        ]
        .into_iter()
        .filter(|code| log.contains(code))
        .collect();
        let stages = [
            "Starting",
            "ProjectLoaded",
            "ProjectValidated",
            "PlatformValidated",
            "CompanionValidated",
            "CacheOpened",
            "SessionPrepared",
            "CommandResolved",
        ];
        let observed_stages: Vec<_> = log
            .lines()
            .filter(|line| line.contains("action=\"execution_stage\""))
            .flat_map(|line| {
                stages
                    .iter()
                    .filter(move |stage| line.contains(&format!("stage={stage}")))
            })
            .take(128)
            .collect();
        #[cfg(target_os = "macos")]
        let stopped = fs::read_to_string(self.root.path().join("terminal.supervisor"))
            .ok()
            .and_then(|value| value.parse::<i32>().ok())
            .and_then(|pid| {
                let mut info = std::mem::MaybeUninit::<libc::proc_bsdinfo>::zeroed();
                let size = std::mem::size_of::<libc::proc_bsdinfo>() as i32;
                (unsafe {
                    libc::proc_pidinfo(
                        pid,
                        libc::PROC_PIDTBSDINFO,
                        1,
                        info.as_mut_ptr().cast(),
                        size,
                    )
                } == size)
                    .then(|| unsafe { info.assume_init() }.pbi_status == 4)
            });
        #[cfg(not(target_os = "macos"))]
        let stopped: Option<bool> = None;
        // Emit closed actions/codes and one state bit, never raw native output,
        // identifiers, paths, argv or environment values from the debug file.
        let wait: Vec<i32> = fs::read_to_string(self.root.path().join("terminal.wait"))
            .unwrap_or_default()
            .split_whitespace()
            .filter_map(|value| value.parse().ok())
            .take(3)
            .collect();
        json!({"actions": observed, "stages": observed_stages, "codes": codes, "supervisorStopped": stopped,
            "waitOutcome": wait})
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
                "wrong {role} termination signal"
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

#[cfg(target_os = "macos")]
fn group_boundary(modes: &[&str], changed: bool) {
    for mode in modes {
        let root = Fixture::project_source(false, "process-group.c");
        let executable = root.path().join("tree");
        let native = Command::new(&executable)
            .current_dir(root.path())
            .args([*mode, "native"])
            .process_group(0)
            .output()
            .unwrap();
        assert_eq!(native.status.code(), Some(0));
        if changed {
            assert!(root
                .path()
                .join(if mode.starts_with("spawn-") {
                    "spawn-created"
                } else {
                    "group-escaped"
                })
                .is_file());
        }
        for marker in ["spawn-created", "child-created", "group-escaped"] {
            let _ = fs::remove_file(root.path().join(marker));
        }
        let cache_path = root.path().join("store");
        let output = Command::new(pnport_binary())
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(&cache_path)
            .args(["--color", "never", "run", "--"])
            .arg(&executable)
            .args([*mode, "virtual"])
            .process_group(0)
            .output()
            .unwrap();
        assert_eq!(
            output.status.code(),
            Some(0),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        if changed {
            assert!(
                root.path()
                    .join(if mode.starts_with("spawn-") {
                        "spawn-created"
                    } else {
                        "group-escaped"
                    })
                    .is_file(),
                "native group operation did not execute"
            );
        }
        if mode.starts_with("spawn-") {
            assert_eq!(output.stdout, b"owned group control\n");
            assert!(root.path().join("child-created").is_file());
        } else if *mode == "join" {
            assert_eq!(output.stdout, b"owned group control\n");
        }
        let cache = Cache::open(cache_path).unwrap();
        let entries = cache.entries(Operation::List).unwrap();
        assert!(
            !entries.is_empty()
                && entries
                    .iter()
                    .all(|entry| matches!(entry.state, State::Complete))
        );
        assert!(cache
            .entries(Operation::Clean)
            .unwrap()
            .iter()
            .all(|entry| matches!(entry.state, State::Removed)));
        assert!(cache.entries(Operation::List).unwrap().is_empty());
        assert!(!root.path().join("node_modules").exists());
    }
}

#[cfg(target_os = "macos")]
#[test]
fn session_and_group_creation_retain_native_behavior() {
    group_boundary(&["setsid", "setpgid", "setpgrp", "spawn-new"], true);
}

#[cfg(target_os = "macos")]
#[test]
fn spawn_session_creation_retains_native_behavior() {
    group_boundary(&["spawn-session"], true);
}

#[cfg(target_os = "macos")]
#[test]
fn children_can_join_and_spawn_into_the_existing_owned_group() {
    group_boundary(&["join", "spawn-same"], false);
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
    assert_eq!(
        fs::read(fixture.root.path().join("spawn-output")).unwrap(),
        b"11"
    );
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

fn exec_fixture(mode: &str) -> Fixture {
    use std::os::unix::fs::PermissionsExt;

    let root = Fixture::project_source(false, "exec-replace.c");
    std::os::unix::fs::symlink("loop", root.path().join("loop")).unwrap();
    fs::write(
        root.path().join("bad-format"),
        b"printf 'unexpected' > shell.accepted\n",
    )
    .unwrap();
    fs::set_permissions(
        root.path().join("bad-format"),
        fs::Permissions::from_mode(0o700),
    )
    .unwrap();
    if mode == "privileged" {
        fs::create_dir(root.path().join("privileged")).unwrap();
        let privileged = root.path().join("privileged/tree");
        fs::copy(root.path().join("tree"), &privileged).unwrap();
        fs::set_permissions(privileged, fs::Permissions::from_mode(0o4755)).unwrap();
    }
    // Complete the negative control before launching an owned command. It
    // writes its own process group marker, which must never become authority
    // for cleanup of the subsequent virtualized fixture.
    let negative = Command::new(root.path().join("tree"))
        .current_dir(root.path())
        .args(["root", mode])
        .output()
        .unwrap();
    assert_eq!(negative.status.code(), Some(40), "unvirtualized {mode}");
    fs::remove_file(root.path().join("root.group")).unwrap();
    let child = Command::new(pnport_binary())
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(root.path().join("store"))
        .args(["run", "--"])
        .arg(root.path().join("tree"))
        .args(["root", mode])
        .process_group(0)
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    Fixture {
        root,
        child: Some(child),
        pids: Vec::new(),
    }
}

#[test]
fn vector_and_variadic_exec_preserve_nested_virtual_images_and_literal_arguments() {
    let modes = ["execve", "execv", "execl", "execle", "execvp", "execlp"];
    #[cfg(target_os = "macos")]
    let modes = modes.into_iter().chain(["execvP"]);
    for mode in modes {
        let mut fixture = exec_fixture(mode);
        let output = fixture.stopped();
        assert_eq!(
            output.status.code(),
            Some(23),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        fixture.assert_released();
    }
}

#[cfg(target_os = "macos")]
#[test]
fn protected_exec_and_shell_fallback_fail_without_starting_unmediated_images() {
    for mode in ["protected", "shell-fallback"] {
        let mut fixture = exec_fixture(mode);
        let output = fixture.stopped();
        assert_eq!(
            output.status.code(),
            Some(125),
            "{mode}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
        assert!(!fixture.root.path().join("exec.accepted").exists());
        assert!(!fixture.root.path().join("shell.accepted").exists());
        fixture.assert_released();
    }
}

#[cfg(target_os = "macos")]
#[test]
fn privileged_path_candidate_fails_before_a_later_executable_can_start() {
    let mut fixture = exec_fixture("privileged");
    let output = fixture.stopped();
    assert_eq!(
        output.status.code(),
        Some(126),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_COMMAND_NOT_EXECUTABLE"));
    assert!(!fixture.root.path().join("exec.accepted").exists());
    fixture.assert_released();
}

fn terminal_job(mode: &str) {
    let (mut fixture, mut terminal) = Fixture::terminal(mode);
    terminal.write_all(b"first\n").unwrap();
    fixture.wait_marker("terminal.first");
    fixture.pids.push(
        fs::read_to_string(fixture.root.path().join("root.pid"))
            .unwrap()
            .parse()
            .unwrap(),
    );
    fixture.assert_active();
    if mode == "interrupt" {
        terminal.write_all(&[3]).unwrap();
    } else {
        if mode == "stop" {
            terminal.write_all(&[26]).unwrap();
        }
        fixture.wait_marker("terminal.stopped");
        terminal.write_all(b"second\n").unwrap();
    }
    assert_eq!(
        fixture.stopped().status.code(),
        Some(if mode == "interrupt" { 130 } else { 23 })
    );
    assert!(fixture.root.path().join("terminal.restored").is_file());
    fixture.assert_released();
}

#[test]
fn controlling_terminal_reads_stop_resume_and_restore_the_caller_group() {
    terminal_job("stop");
}

#[test]
fn command_sigstop_stops_the_supervisor_until_foreground_resume() {
    terminal_job("self-stop");
}

#[test]
fn background_terminal_read_stops_then_foreground_resume_preserves_input() {
    terminal_job("background");
}

#[test]
fn controlling_terminal_interrupt_preserves_the_native_exit_status() {
    terminal_job("interrupt");
}

#[test]
fn redirected_input_keeps_the_callers_terminal_group() {
    let (mut fixture, _terminal) = Fixture::terminal("redirected");
    assert_eq!(fixture.stopped().status.code(), Some(23));
    assert!(fixture.root.path().join("terminal.restored").is_file());
    fixture.assert_released();
}

#[test]
fn terminal_startup_failures_retain_their_stage_and_exit_class() {
    for (mode, status, code) in [
        ("missing-image", 127, "PNPORT_COMMAND_NOT_FOUND"),
        ("invalid-image", 126, "PNPORT_COMMAND_NOT_EXECUTABLE"),
    ] {
        let (mut fixture, _terminal) = Fixture::terminal(mode);
        fixture.wait_marker("terminal.restored");
        let output = fixture.child.take().unwrap().wait_with_output().unwrap();
        assert_eq!(output.status.code(), Some(status));
        let diagnostics = fixture.terminal_diagnostics();
        assert_eq!(diagnostics["codes"], json!([code]));
        assert_eq!(
            diagnostics["stages"],
            json!([
                "Starting",
                "ProjectLoaded",
                "ProjectValidated",
                "PlatformValidated",
                "CompanionValidated",
                "CacheOpened",
                "SessionPrepared",
                "CommandResolved",
            ])
        );
        assert_eq!(diagnostics["actions"], json!([]));
        assert!(!fixture.root.path().join("root.pid").exists());
        assert!(fixture.root.path().join("terminal.restored").is_file());
    }
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
    for role in [None, Some("guardian"), Some("anchor"), Some("bootstrap")] {
        for inherited in [false, true] {
            let (_peer, socket) = UnixStream::pair().unwrap();
            let fd = socket.as_raw_fd();
            let mut command = Command::new(pnport_binary());
            command
                .arg("__pnport_macos_owner")
                .process_group(0)
                .env_remove("PNPORT_MACOS_OWNER_FD");
            if let Some(role) = role {
                command.arg(role);
            }
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
}

#[cfg(target_os = "macos")]
#[derive(Clone, Copy, Debug)]
enum OwnerFailure {
    Supervisor,
    Guardian,
}

#[cfg(target_os = "macos")]
struct Control(Child);

#[cfg(target_os = "macos")]
impl Drop for Control {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

#[cfg(target_os = "macos")]
fn stopped_group(mode: &str, failure: OwnerFailure) {
    let mut fixture = Fixture::new(mode, false);
    fixture.ready();
    fixture.assert_active();
    let group = fixture.group();
    assert!(unsafe { libc::getpgid(group) } > 0);
    assert_ne!(
        unsafe { libc::getpgid(group) },
        group,
        "guardian must run outside the command group"
    );
    // The unrelated control is a direct child in its own group. Cleanup must
    // leave it running and never use a host inventory as signalling authority.
    let mut unrelated = Control(
        Command::new("/bin/sleep")
            .arg("30")
            .process_group(0)
            .spawn()
            .unwrap(),
    );
    assert_eq!(unsafe { libc::kill(-group, libc::SIGSTOP) }, 0);
    let supervisor = fixture.child.as_ref().unwrap().id() as i32;
    for pid in fixture.pids.iter().chain(std::iter::once(&supervisor)) {
        let deadline = Instant::now() + Duration::from_secs(2);
        loop {
            let mut info = unsafe { std::mem::zeroed::<libc::proc_bsdinfo>() };
            let size = std::mem::size_of_val(&info) as i32;
            let count = unsafe {
                libc::proc_pidinfo(
                    *pid,
                    libc::PROC_PIDTBSDINFO,
                    1,
                    (&mut info as *mut libc::proc_bsdinfo).cast(),
                    size,
                )
            };
            assert_eq!(count, size);
            if info.pbi_status == 4 {
                // Darwin SSTOP, not merely a queued stop signal.
                break;
            }
            assert!(Instant::now() < deadline, "fixture member did not stop");
            std::thread::sleep(Duration::from_millis(10));
        }
    }
    let start = Instant::now();
    if matches!(failure, OwnerFailure::Supervisor) {
        fixture.signal(libc::SIGKILL);
    } else {
        assert_eq!(unsafe { libc::kill(group, libc::SIGKILL) }, 0);
        // Root stop propagation can also have parked the supervisor. Resume
        // that direct child so it can detect the failed guardian and clean up.
        fixture.signal(libc::SIGCONT);
    }
    let output = fixture.stopped();
    assert!(
        unrelated.0.try_wait().unwrap().is_none(),
        "cleanup signalled an unrelated group"
    );
    drop(unrelated);
    if matches!(failure, OwnerFailure::Supervisor) {
        assert_eq!(output.status.signal(), Some(libc::SIGKILL));
        if mode == "ignore" {
            assert!(start.elapsed() >= Duration::from_secs(5));
        } else {
            fixture.assert_signals(libc::SIGTERM);
        }
    } else {
        assert_eq!(output.status.code(), Some(125));
        assert!(String::from_utf8_lossy(&output.stderr).contains("PNPORT_CLEANUP_FAILED"));
    }
    fixture.assert_released();
}

#[cfg(target_os = "macos")]
#[test]
fn killed_supervisor_resumes_a_stopped_group_for_graceful_cleanup() {
    stopped_group("normal", OwnerFailure::Supervisor);
}

#[cfg(target_os = "macos")]
#[test]
fn killed_supervisor_escalates_a_stopped_unresponsive_group_after_grace() {
    stopped_group("ignore", OwnerFailure::Supervisor);
}

#[cfg(target_os = "macos")]
#[test]
fn failed_guardian_stops_a_stopped_group_before_reaping_its_identity() {
    stopped_group("normal", OwnerFailure::Guardian);
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

#[cfg(target_os = "macos")]
#[test]
fn detached_native_trees_retain_signals_and_cache_cleanup() {
    for mode in [
        "detached",
        "detached-group",
        "detached-spawn-group",
        "detached-spawn-session",
    ] {
        for signal in [libc::SIGINT, libc::SIGTERM, libc::SIGHUP] {
            let mut fixture = Fixture::new(mode, false);
            fixture.ready();
            fixture.assert_active();
            assert_ne!(
                fs::read(fixture.root.path().join("root.group")).unwrap(),
                fs::read(fixture.root.path().join("middle.group")).unwrap()
            );
            fixture.signal(signal);
            assert_eq!(fixture.stopped().status.code(), Some(128 + signal));
            fixture.assert_signals(signal);
            fixture.assert_released();
        }
    }
}

#[cfg(target_os = "macos")]
#[test]
fn detached_native_trees_survive_owner_loss_only_until_cleanup() {
    for mode in [
        "detached",
        "detached-group",
        "detached-spawn-group",
        "detached-spawn-session",
        "detached-ignore",
    ] {
        for failure in [OwnerFailure::Supervisor, OwnerFailure::Guardian] {
            // Closed fixture values only: retain the failing variant without
            // printing process identities, native content or environment state.
            eprintln!("fixture_mode={mode} owner_failure={failure:?}");
            let mut fixture = Fixture::new(mode, false);
            fixture.ready();
            let mut unrelated = Control(
                Command::new("/bin/sleep")
                    .arg("30")
                    .process_group(0)
                    .spawn()
                    .unwrap(),
            );
            // Verify kernel stop admission before injecting owner failure. A
            // queued SIGCONT before pnport observes the root stop can otherwise
            // be lost before its subsequent intentional supervisor SIGSTOP.
            for pid in &fixture.pids {
                assert_eq!(unsafe { libc::kill(*pid, libc::SIGSTOP) }, 0);
            }
            let supervisor = fixture.child.as_ref().unwrap().id() as i32;
            for pid in fixture.pids.iter().chain(std::iter::once(&supervisor)) {
                let deadline = Instant::now() + Duration::from_secs(5);
                loop {
                    let mut info = unsafe { std::mem::zeroed::<libc::proc_bsdinfo>() };
                    let size = std::mem::size_of_val(&info) as i32;
                    assert_eq!(
                        unsafe {
                            libc::proc_pidinfo(
                                *pid,
                                libc::PROC_PIDTBSDINFO,
                                1,
                                (&raw mut info).cast(),
                                size,
                            )
                        },
                        size
                    );
                    if info.pbi_status == 4 {
                        break;
                    }
                    assert!(
                        Instant::now() < deadline,
                        "{mode}: native stop was not admitted"
                    );
                    std::thread::sleep(Duration::from_millis(10));
                }
            }
            let start = Instant::now();
            if matches!(failure, OwnerFailure::Supervisor) {
                fixture.signal(libc::SIGKILL);
            } else {
                assert_eq!(unsafe { libc::kill(fixture.group(), libc::SIGKILL) }, 0);
                fixture.signal(libc::SIGCONT);
            }
            let output = fixture.stopped();
            if matches!(failure, OwnerFailure::Supervisor) {
                assert_eq!(output.status.signal(), Some(libc::SIGKILL));
            } else {
                assert_eq!(output.status.code(), Some(125));
            }
            if mode.ends_with("ignore") {
                assert!(start.elapsed() >= Duration::from_secs(5));
            } else {
                // Forced owner loss can orphan a stopped group. The paired
                // unvirtualized native control below proves XNU can deliver
                // SIGHUP/SIGCONT independently of pnport's queued SIGTERM.
                for role in ["root", "middle", "leaf"] {
                    let delivered =
                        fs::read(fixture.root.path().join(format!("{role}.signal"))).unwrap();
                    assert!(
                        delivered == [libc::SIGTERM as u8] || delivered == [libc::SIGHUP as u8],
                        "{mode}, {failure:?}: unexpected {role} termination signal: {delivered:?}"
                    );
                }
            }
            assert!(unrelated.0.try_wait().unwrap().is_none());
            fixture.assert_released();
        }
    }
}

#[cfg(target_os = "macos")]
#[test]
fn normal_root_exit_stops_detached_native_descendants() {
    let mut fixture = Fixture::new("detached-exit", false);
    fixture.ready();
    assert_eq!(fixture.stopped().status.code(), Some(23));
    fixture.assert_released();
}

#[cfg(target_os = "macos")]
#[test]
fn stopped_unregistered_orphan_is_owned_after_its_parent_exits() {
    for failure in [OwnerFailure::Supervisor, OwnerFailure::Guardian] {
        let mut fixture = Fixture::new("detached-orphan", false);
        for marker in ["root.pid", "middle.pid", "parked.pid"] {
            fixture.wait_marker(marker);
            let deadline = Instant::now() + Duration::from_secs(5);
            loop {
                if let Ok(pid) = fs::read_to_string(fixture.root.path().join(marker))
                    .unwrap()
                    .parse::<i32>()
                {
                    fixture.pids.push(pid);
                    break;
                }
                assert!(Instant::now() < deadline);
                std::thread::sleep(Duration::from_millis(10));
            }
        }
        fs::write(fixture.root.path().join("orphan-release"), b"1").unwrap();
        let middle = fixture.pids[1];
        let deadline = Instant::now() + Duration::from_secs(5);
        while pnport_core::macos_process::Identity::capture(middle)
            .is_ok_and(|identity| !identity.zombie)
        {
            assert!(Instant::now() < deadline);
            std::thread::sleep(Duration::from_millis(10));
        }
        // The parked child has never reached its own image constructor.
        assert!(!fixture.root.path().join("leaf.pid").exists());
        if matches!(failure, OwnerFailure::Supervisor) {
            fixture.signal(libc::SIGKILL);
        } else {
            assert_eq!(unsafe { libc::kill(fixture.group(), libc::SIGKILL) }, 0);
        }
        let output = fixture.stopped();
        if matches!(failure, OwnerFailure::Supervisor) {
            assert_eq!(output.status.signal(), Some(libc::SIGKILL));
        } else {
            assert_eq!(output.status.code(), Some(125));
        }
        fixture.assert_released();
    }
}

#[cfg(target_os = "macos")]
#[test]
fn terminal_group_changes_retain_foreground_stop_and_resume() {
    terminal_job("new-group");
}

#[cfg(target_os = "macos")]
#[test]
fn suspended_jobs_do_not_consume_pending_image_deadlines() {
    terminal_job("pending-pause");
}

#[cfg(target_os = "macos")]
#[test]
fn terminal_interrupt_stops_a_root_in_a_new_session() {
    let (mut fixture, mut terminal) = Fixture::terminal("detached-interrupt");
    fixture.ready();
    fixture.assert_active();
    let deadline = Instant::now() + Duration::from_secs(3);
    loop {
        let caller = fs::read_to_string(fixture.root.path().join("terminal.supervisor"))
            .unwrap()
            .parse::<i32>()
            .unwrap();
        if unsafe { libc::tcgetpgrp(terminal.as_raw_fd()) } == caller {
            break;
        }
        assert!(
            Instant::now() < deadline,
            "detached root retained terminal foreground"
        );
        std::thread::sleep(Duration::from_millis(10));
    }
    terminal.write_all(&[3]).unwrap();
    assert_eq!(fixture.stopped().status.code(), Some(130));
    fixture.assert_signals(libc::SIGINT);
    assert!(fixture.root.path().join("terminal.restored").is_file());
    fixture.assert_released();
}

#[cfg(target_os = "macos")]
#[test]
fn native_stopped_orphan_groups_receive_kernel_hangup() {
    let root = Fixture::project_source(false, "process-orphan.c");
    let child = Command::new(root.path().join("tree"))
        .current_dir(root.path())
        .process_group(0)
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut fixture = Fixture {
        root,
        child: Some(child),
        pids: Vec::new(),
    };
    fixture.ready();
    assert_eq!(fixture.stopped().status.code(), Some(23));
    for role in ["middle", "leaf"] {
        assert_eq!(
            fs::read(fixture.root.path().join(format!("{role}.signal"))).unwrap(),
            [libc::SIGHUP as u8]
        );
    }
}
