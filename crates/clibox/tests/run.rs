#![cfg(unix)]

#[cfg(target_os = "linux")]
use std::os::fd::AsRawFd;
#[cfg(unix)]
use std::os::fd::FromRawFd;
use std::{
    collections::HashMap,
    fs,
    io::{Read, Write},
    net::TcpListener,
    os::unix::process::CommandExt,
    path::{Path, PathBuf},
    process::{Child, Command, ExitStatus, Stdio},
    sync::{
        atomic::{AtomicBool, Ordering},
        Arc,
    },
    thread,
    time::{Duration, Instant},
};

fn command(home: &std::path::Path, args: &[&str]) -> Command {
    let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
    command.args(args).env("HOME", home);
    #[cfg(target_os = "linux")]
    command.env("XDG_STATE_HOME", home.join("state"));
    #[cfg(target_os = "macos")]
    command.env_remove("XDG_STATE_HOME");
    command
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
#[derive(Clone, Copy)]
struct ProcessRecord {
    parent: i32,
    group: i32,
    state_is_zombie: bool,
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn process_snapshot() -> HashMap<i32, ProcessRecord> {
    let output = Command::new("ps")
        .args(["-axo", "pid=,ppid=,pgid=,stat="])
        .output()
        .expect("ps is required for Unix process ownership tests");
    assert!(output.status.success(), "could not inspect test processes");
    String::from_utf8(output.stdout)
        .expect("ps output should be UTF-8")
        .lines()
        .filter_map(|line| {
            let mut fields = line.split_whitespace();
            let pid = fields.next()?.parse().ok()?;
            let parent = fields.next()?.parse().ok()?;
            let group = fields.next()?.parse().ok()?;
            let state_is_zombie = fields.next()?.starts_with('Z');
            Some((
                pid,
                ProcessRecord {
                    parent,
                    group,
                    state_is_zombie,
                },
            ))
        })
        .collect()
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn wrapper_flags(timeout: &str, kill_after: &str) -> Vec<String> {
    [
        "run",
        "with-timeout",
        "--timeout",
        timeout,
        "--kill-after",
        kill_after,
        "--",
    ]
    .into_iter()
    .map(str::to_owned)
    .collect()
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn nested_native_chain_args(depth: usize, outer_timeout: &str) -> Vec<String> {
    assert!(depth >= 1);
    let mut workload = vec![
        "sh".to_owned(),
        "-c".to_owned(),
        "trap '' TERM; printf '%s %s\\n' \"$$\" \"$(ps -o pgid= -p $$)\" > \"$MARKER\"; exec \
         sleep 30"
            .to_owned(),
    ];
    for _ in 1..depth {
        let mut nested = vec![env!("CARGO_BIN_EXE_clibox").to_owned()];
        nested.extend(wrapper_flags("30s", "10s"));
        nested.extend(workload);
        workload = nested;
    }
    let mut args = wrapper_flags(outer_timeout, "0");
    args.extend(workload);
    args
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
struct NestedProcessFixture {
    child: Child,
    marker: PathBuf,
    stderr: PathBuf,
    additional_owned_groups: Vec<i32>,
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
impl NestedProcessFixture {
    fn new(mut command: Command, marker: &Path) -> Self {
        let stderr = marker.with_extension("stderr");
        let stderr_file = fs::File::create(&stderr).expect("could not create fixture log");
        Self {
            child: command
                .stdout(Stdio::null())
                .stderr(Stdio::from(stderr_file))
                .spawn()
                .expect("could not start nested wrapper fixture"),
            marker: marker.to_owned(),
            stderr,
            additional_owned_groups: Vec::new(),
        }
    }

    fn stderr(&self) -> String {
        fs::read_to_string(&self.stderr).unwrap_or_default()
    }

    fn workload(&mut self, timeout: Duration) -> Option<(i32, i32)> {
        let deadline = Instant::now() + timeout;
        while Instant::now() < deadline {
            if let Ok(contents) = fs::read_to_string(&self.marker) {
                let mut fields = contents.split_whitespace();
                if let (Some(pid), Some(group)) = (fields.next(), fields.next()) {
                    if let (Ok(pid), Ok(group)) = (pid.parse(), group.parse()) {
                        return Some((pid, group));
                    }
                }
            }
            if self.child.try_wait().ok().flatten().is_some() {
                return None;
            }
            thread::sleep(Duration::from_millis(5));
        }
        None
    }

    fn wait(&mut self, timeout: Duration) -> Option<ExitStatus> {
        let deadline = Instant::now() + timeout;
        loop {
            if let Some(status) = self.child.try_wait().expect("could not wait for wrapper") {
                return Some(status);
            }
            if Instant::now() >= deadline {
                return None;
            }
            thread::sleep(Duration::from_millis(10));
        }
    }
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
impl Drop for NestedProcessFixture {
    fn drop(&mut self) {
        let mut groups = self.additional_owned_groups.clone();
        if let Ok(contents) = fs::read_to_string(&self.marker) {
            if let Some(group) = contents
                .split_whitespace()
                .nth(1)
                .and_then(|group| group.parse::<i32>().ok())
            {
                groups.push(group);
            }
        }
        // Also find the root wrapper's direct child group when setup failed
        // before the workload could publish its PID and process-group marker.
        if let Ok(output) = Command::new("ps")
            .args(["-axo", "pid=,ppid=,pgid="])
            .output()
        {
            let root = self.child.id() as i32;
            for line in String::from_utf8_lossy(&output.stdout).lines() {
                let mut fields = line.split_whitespace();
                let Some(_pid) = fields.next() else { continue };
                let Some(parent) = fields.next().and_then(|value| value.parse::<i32>().ok()) else {
                    continue;
                };
                let Some(group) = fields.next().and_then(|value| value.parse::<i32>().ok()) else {
                    continue;
                };
                if parent == root {
                    groups.push(group);
                }
            }
        }
        groups.sort_unstable();
        groups.dedup();
        for group in groups.into_iter().filter(|group| *group > 0) {
            // Every collected group is rooted in this fixture's wrapper.
            unsafe { libc::kill(-group, libc::SIGKILL) };
        }
        if self.child.try_wait().ok().flatten().is_none() {
            let _ = self.child.kill();
        }
        let _ = self.child.wait();
    }
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn assert_process_chain_in_group(root: u32, workload: i32, expected_group: i32) {
    let root = i32::try_from(root).expect("test process id should fit pid_t");
    let processes = process_snapshot();
    let mut current = workload;
    let mut ancestors = 0;
    while current != root {
        let record = processes
            .get(&current)
            .unwrap_or_else(|| panic!("process {current} disappeared before ownership check"));
        assert_eq!(
            record.group, expected_group,
            "process {current} escaped the root-owned process group"
        );
        current = record.parent;
        ancestors += 1;
        assert!(ancestors <= 64, "process ancestry exceeded its test bound");
    }
    assert!(
        ancestors >= 2,
        "fixture did not include a nested wrapper chain"
    );
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
fn assert_process_group_stopped(group: i32) {
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        let live: Vec<_> = process_snapshot()
            .into_iter()
            .filter_map(|(pid, process)| {
                (process.group == group && !process.state_is_zombie).then_some(pid)
            })
            .collect();
        if live.is_empty() {
            return;
        }
        if Instant::now() >= deadline {
            panic!("fixture processes remain live in group {group}: {live:?}");
        }
        thread::sleep(Duration::from_millis(20));
    }
}

#[cfg(target_os = "linux")]
fn terminal_command(home: &std::path::Path, args: &[&str]) -> (Command, fs::File) {
    terminal_command_with_stdio(home, args, false)
}

#[cfg(target_os = "linux")]
fn terminal_command_with_redirected_stdin(
    home: &std::path::Path,
    args: &[&str],
) -> (Command, fs::File) {
    terminal_command_with_stdio(home, args, true)
}

#[cfg(target_os = "linux")]
fn terminal_command_with_stdio(
    home: &std::path::Path,
    args: &[&str],
    redirected_stdin: bool,
) -> (Command, fs::File) {
    terminal_process_command(command(home, args), redirected_stdin)
}

#[cfg(target_os = "linux")]
fn terminal_process_command(mut command: Command, redirected_stdin: bool) -> (Command, fs::File) {
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
    command
        .stdin(if redirected_stdin {
            Stdio::null()
        } else {
            Stdio::from(slave.try_clone().unwrap())
        })
        .stdout(Stdio::from(slave.try_clone().unwrap()))
        .stderr(Stdio::from(slave));
    let controlling_descriptor = if redirected_stdin {
        libc::STDOUT_FILENO
    } else {
        libc::STDIN_FILENO
    };
    unsafe {
        command.pre_exec(move || {
            if libc::setsid() == -1
                || libc::ioctl(controlling_descriptor, libc::TIOCSCTTY as _, 0) == -1
            {
                Err(std::io::Error::last_os_error())
            } else {
                Ok(())
            }
        });
    }
    (command, master)
}

#[cfg(target_os = "linux")]
fn spawn_terminal(mut command: Command) -> Child {
    // `Command` retains the parent copies of the slave descriptors after
    // spawning. Consume it here so those copies close before a test reads the
    // PTY master and waits for end-of-file.
    command.spawn().unwrap()
}

#[cfg(target_os = "linux")]
fn read_terminal(mut terminal: fs::File) -> String {
    let mut output = Vec::new();
    let mut buffer = [0; 1024];
    loop {
        match terminal.read(&mut buffer) {
            Ok(0) => break,
            Ok(count) => output.extend_from_slice(&buffer[..count]),
            Err(error) if error.raw_os_error() == Some(libc::EIO) => break,
            Err(error) => panic!("could not read the test terminal: {error}"),
        }
    }
    String::from_utf8(output).unwrap()
}

#[cfg(target_os = "linux")]
fn terminal_foreground_group(terminal: &fs::File) -> libc::pid_t {
    let group = unsafe { libc::tcgetpgrp(std::os::fd::AsRawFd::as_raw_fd(terminal)) };
    assert_ne!(group, -1, "could not read the foreground terminal group");
    group
}

#[cfg(target_os = "linux")]
fn enable_terminal_tostop(terminal: &fs::File) {
    let mut settings = std::mem::MaybeUninit::<libc::termios>::uninit();
    assert_eq!(
        unsafe { libc::tcgetattr(terminal.as_raw_fd(), settings.as_mut_ptr()) },
        0
    );
    let mut settings = unsafe { settings.assume_init() };
    settings.c_lflag |= libc::TOSTOP;
    assert_eq!(
        unsafe { libc::tcsetattr(terminal.as_raw_fd(), libc::TCSANOW, &settings) },
        0
    );
}

#[test]
fn rate_limit_uses_shared_hashed_state_and_zero_wait_is_immediate() {
    let home = tempfile::tempdir().unwrap();
    let first = command(
        home.path(),
        &[
            "run",
            "with-rate-limit",
            "--name",
            "shared.bucket",
            "--limit",
            "1",
            "--period",
            "1m",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(first.status.success());
    let second = command(
        home.path(),
        &[
            "run",
            "with-rate-limit",
            "--name",
            "shared.bucket",
            "--limit",
            "1",
            "--period",
            "1m",
            "--wait-timeout",
            "0",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(second.status.code(), Some(124));
    #[cfg(target_os = "linux")]
    let state = home.path().join("state/clibox/run/buckets");
    #[cfg(target_os = "macos")]
    let state = home
        .path()
        .join("Library/Application Support/clibox/run/buckets");
    if state.exists() {
        for entry in fs::read_dir(state).unwrap() {
            assert!(!entry
                .unwrap()
                .file_name()
                .to_string_lossy()
                .contains("shared.bucket"));
        }
    }
}

#[test]
fn restrictive_umask_keeps_new_lock_state_usable() {
    let home = tempfile::tempdir().unwrap();
    for _ in 0..2 {
        let mut invocation = command(
            home.path(),
            &[
                "run",
                "with-lock",
                "--name",
                "restrictive-umask",
                "--",
                "sh",
                "-c",
                "exit 0",
            ],
        );
        unsafe {
            invocation.pre_exec(|| {
                libc::umask(0o777);
                Ok(())
            });
        }
        assert!(invocation.output().unwrap().status.success());
    }
    for _ in 0..2 {
        let mut invocation = command(
            home.path(),
            &[
                "run",
                "with-rate-limit",
                "--name",
                "restrictive-umask-rate",
                "--limit",
                "2",
                "--period",
                "1m",
                "--burst",
                "2",
                "--",
                "sh",
                "-c",
                "exit 0",
            ],
        );
        unsafe {
            invocation.pre_exec(|| {
                libc::umask(0o777);
                Ok(())
            });
        }
        assert!(invocation.output().unwrap().status.success());
    }
}

#[test]
fn rate_limit_rejects_bursts_larger_than_exact_token_storage() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-rate-limit",
            "--name",
            "shared.bucket",
            "--limit",
            "1",
            "--period",
            "1m",
            "--burst",
            "9007199254740993",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(2));
}

#[test]
fn lock_fail_never_starts_a_second_workload() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("lock-owner-ready");
    let assignment = format!("MARKER={}", marker.display());
    let mut owner = command(
        home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "migration",
            &assignment,
            "--",
            "sh",
            "-c",
            "printf ready > \"$MARKER\"; sleep 1",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    for _ in 0..50 {
        if marker.is_file() {
            break;
        }
        thread::sleep(Duration::from_millis(20));
    }
    assert!(marker.is_file(), "lock owner did not start its workload");
    let contender = command(
        home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "migration",
            "--on-locked",
            "fail",
            "--",
            "sh",
            "-c",
            "exit 99",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(contender.status.code(), Some(75));
    assert!(owner.wait().unwrap().success());
}

#[test]
fn retry_preserves_the_last_child_status_after_exhaustion() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("attempts");
    let script = format!(
        "test -f '{}' && exit 7; : > '{}'; exit 7",
        marker.display(),
        marker.display()
    );
    let output = command(
        home.path(),
        &[
            "run",
            "with-retry",
            "--max-attempts",
            "2",
            "--delay",
            "1ms",
            "--jitter",
            "none",
            "--",
            "sh",
            "-c",
            &script,
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(7));
    assert!(marker.is_file());
}

#[test]
fn timeout_terminates_a_silent_workload() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "50ms",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "sleep 1",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
}

#[test]
fn timeout_forwards_shutdown_output_before_returning() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "50ms",
            "--kill-after",
            "100ms",
            "--",
            "sh",
            "-c",
            "trap 'printf timed-workload-shutdown >&2; exit 0' TERM; while :; do sleep 1; done",
        ],
    )
    .output()
    .unwrap();

    assert_eq!(output.status.code(), Some(124), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("timed-workload-shutdown"),
        "timed workload shutdown output was not forwarded: {output:?}"
    );
}

#[test]
fn idle_timeout_uses_the_output_read_time_before_completion() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--idle-timeout",
            "1ms",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "printf output; sleep 0.005",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
}

#[test]
fn unwritable_wrapper_output_returns_a_runtime_failure() {
    let home = tempfile::tempdir().unwrap();
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--idle-timeout",
            "1s",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "printf output",
        ],
    );
    let mut output_pipe = [0; 2];
    assert_eq!(unsafe { libc::pipe(output_pipe.as_mut_ptr()) }, 0);
    let reader = unsafe { fs::File::from_raw_fd(output_pipe[0]) };
    let writer = unsafe { fs::File::from_raw_fd(output_pipe[1]) };
    drop(reader);
    wrapper.stdout(Stdio::from(writer));
    let mut wrapper = wrapper.spawn().unwrap();

    assert_eq!(wrapper.wait().unwrap().code(), Some(1));
}

#[test]
#[cfg(target_os = "linux")]
fn timeout_does_not_block_on_a_stalled_output_consumer() {
    let home = tempfile::tempdir().unwrap();
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--idle-timeout",
            "100ms",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "head -c 8192 /dev/zero",
        ],
    );
    let mut output_pipe = [0; 2];
    assert_eq!(unsafe { libc::pipe(output_pipe.as_mut_ptr()) }, 0);
    let reader = unsafe { fs::File::from_raw_fd(output_pipe[0]) };
    let writer = unsafe { fs::File::from_raw_fd(output_pipe[1]) };
    assert_ne!(
        unsafe { libc::fcntl(writer.as_raw_fd(), libc::F_SETPIPE_SZ, 4_096) },
        -1
    );
    wrapper.stdout(Stdio::from(writer));
    let mut wrapper = wrapper.spawn().unwrap();

    let started = std::time::Instant::now();
    assert_eq!(wrapper.wait().unwrap().code(), Some(124));
    assert!(
        started.elapsed() < Duration::from_secs(2),
        "the wrapper waited for a blocked output writer"
    );
    drop(reader);
}

#[test]
#[cfg(target_os = "linux")]
fn interactive_workload_keeps_foreground_terminal_access() {
    struct Fixture {
        child: Child,
        terminal: fs::File,
        output: Vec<u8>,
    }
    impl Fixture {
        fn drain(&mut self) {
            let mut buffer = [0; 1024];
            loop {
                match self.terminal.read(&mut buffer) {
                    Ok(0) => break,
                    Ok(count) => self.output.extend_from_slice(&buffer[..count]),
                    Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => break,
                    Err(error) if error.raw_os_error() == Some(libc::EIO) => break,
                    Err(error) => panic!("could not read fixture terminal: {error}"),
                }
            }
        }

        fn wait(&mut self, timeout: Duration) -> Option<ExitStatus> {
            let deadline = Instant::now() + timeout;
            loop {
                self.drain();
                if let Some(status) = self.child.try_wait().unwrap() {
                    self.drain();
                    return Some(status);
                }
                if Instant::now() >= deadline {
                    return None;
                }
                thread::sleep(Duration::from_millis(10));
            }
        }
    }
    impl Drop for Fixture {
        fn drop(&mut self) {
            if self.child.try_wait().ok().flatten().is_some() {
                return;
            }
            // The retained wrapper owns cleanup of its workload on SIGTERM.
            unsafe { libc::kill(self.child.id() as i32, libc::SIGTERM) };
            let deadline = Instant::now() + Duration::from_secs(5);
            while self.child.try_wait().ok().flatten().is_none() {
                if Instant::now() >= deadline {
                    // Force and reap only the retained wrapper. Dropping
                    // this fixture's PTY master then hangs up its original
                    // controlling session and the simple shell workload.
                    let _ = self.child.kill();
                    let _ = self.child.wait();
                    break;
                }
                thread::sleep(Duration::from_millis(10));
            }
        }
    }
    let home = tempfile::tempdir().unwrap();
    let (wrapper, terminal) = terminal_command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "printf 'terminal-ready\\n'; read value; printf 'reply=%s\\n' \"$value\"",
        ],
    );
    use std::os::fd::AsRawFd;
    let flags = unsafe { libc::fcntl(terminal.as_raw_fd(), libc::F_GETFL) };
    assert_ne!(flags, -1);
    assert_ne!(
        unsafe {
            libc::fcntl(
                terminal.as_raw_fd(),
                libc::F_SETFL,
                flags | libc::O_NONBLOCK,
            )
        },
        -1
    );
    let mut fixture = Fixture {
        child: spawn_terminal(wrapper),
        terminal,
        output: Vec::new(),
    };
    // Input sent before the child starts cannot prove it retained foreground
    // terminal access. Wait for output and the native ownership handoff first;
    // the product timeout is only an outer guard for this interactive fixture.
    let deadline = Instant::now() + Duration::from_secs(10);
    let group = loop {
        fixture.drain();
        let group = unsafe { libc::tcgetpgrp(fixture.terminal.as_raw_fd()) };
        if String::from_utf8_lossy(&fixture.output)
            .lines()
            .any(|line| line == "terminal-ready")
            && group > 0
            && group != fixture.child.id() as i32
        {
            break group;
        }
        let status = fixture.child.try_wait().unwrap();
        assert!(
            status.is_none() && Instant::now() < deadline,
            "interactive workload was not ready: status={status:?}, output={:?}",
            String::from_utf8_lossy(&fixture.output)
        );
        thread::sleep(Duration::from_millis(10));
    };
    // This fixture has one direct shell, unlike the nested-chain controls.
    // The admitted foreground leader must still be that live owned child.
    let processes = process_snapshot();
    let workload = processes
        .get(&group)
        .expect("foreground shell disappeared before terminal input");
    assert_eq!(workload.parent, fixture.child.id() as i32);
    assert_eq!(workload.group, group);
    assert!(!workload.state_is_zombie);
    fixture.terminal.write_all(b"answer\n").unwrap();
    let status = fixture.wait(Duration::from_secs(10));
    assert!(
        status.is_some_and(|status| status.success()),
        "interactive workload failed: status={status:?}, output={:?}",
        String::from_utf8_lossy(&fixture.output)
    );
    assert!(String::from_utf8_lossy(&fixture.output)
        .lines()
        .any(|line| line == "reply=answer"));
}

#[test]
#[cfg(target_os = "linux")]
fn terminal_interrupt_cancels_a_foreground_workload_and_its_wrapper() {
    let home = tempfile::tempdir().unwrap();
    let (wrapper, terminal) = terminal_command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "trap '' INT; while :; do :; done",
        ],
    );
    let mut wrapper = spawn_terminal(wrapper);
    let wrapper_group = wrapper.id() as libc::pid_t;
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    let child_group = loop {
        let group = terminal_foreground_group(&terminal);
        if group != wrapper_group {
            break group;
        }
        assert!(
            std::time::Instant::now() < deadline,
            "workload did not receive terminal ownership"
        );
        thread::sleep(Duration::from_millis(20));
    };

    assert_eq!(unsafe { libc::kill(-child_group, libc::SIGINT) }, 0);
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    let status = loop {
        if let Some(status) = wrapper.try_wait().unwrap() {
            break status;
        }
        if std::time::Instant::now() >= deadline {
            let _ = unsafe { libc::kill(-wrapper_group, libc::SIGKILL) };
            let _ = unsafe { libc::kill(-child_group, libc::SIGKILL) };
            let _ = wrapper.wait();
            panic!("foreground terminal interrupt did not cancel the wrapper");
        }
        thread::sleep(Duration::from_millis(20));
    };

    assert_eq!(status.code(), Some(130));
}

#[test]
#[cfg(target_os = "linux")]
fn foreground_completion_reaps_the_interrupt_relay_without_grace_delay() {
    let home = tempfile::tempdir().unwrap();
    let (wrapper, _terminal) = terminal_command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    );
    let started = std::time::Instant::now();
    let status = spawn_terminal(wrapper).wait().unwrap();

    assert!(status.success());
    assert!(
        started.elapsed() < Duration::from_secs(2),
        "interrupt relay completion waited for the default cleanup grace"
    );
}

#[test]
#[cfg(target_os = "linux")]
fn node_launcher_terminal_service_interrupt_obeys_acknowledgement_window() {
    let home = tempfile::tempdir().unwrap();
    let service_started = home.path().join("node-launcher-service-started");
    let observations = home.path().join("launcher-interrupt-events");
    let service_marker = format!("SERVICE_STARTED={}", service_started.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    drop(listener);
    let launcher = home.path().join("launcher.cjs");
    let clibox_launcher = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../packages/clibox/src/launcher.cjs")
        .canonicalize()
        .unwrap();
    fs::write(
        &launcher,
        concat!(
            "const { launch } = require(process.env.CLIBOX_LAUNCHER);\n",
            "const { spawn } = require('node:child_process');\n",
            "const { appendFileSync } = require('node:fs');\n",
            "const observe = event => appendFileSync(process.env.CLIBOX_TEST_EVENTS, event + \
             '\\n');\n",
            "process.on('SIGINT', () => observe('interrupt'));\n",
            "const spawnChild = (...args) => {\n",
            "  const child = spawn(...args);\n",
            "  child.stdio[3].on('data', bytes => { if (bytes.length) observe('ack'); });\n",
            "  const kill = child.kill.bind(child);\n",
            "  child.kill = signal => { if (signal === 'SIGINT') observe('forward'); return \
             kill(signal); };\n",
            "  return child;\n",
            "};\n",
            "launch(process.env.CLIBOX_TEST_BINARY, process.argv.slice(2), { spawnChild \
             }).then(({ code }) => {\n",
            "  process.exitCode = code ?? 1;\n",
            "});\n",
        ),
    )
    .unwrap();
    let mut node = Command::new("node");
    node.arg(&launcher)
        .args([
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--kill-after",
            "500ms",
            "--service",
            &service_marker,
            "sh",
            "-c",
            "trap '' TERM; printf '%s %s\\n' \"$$\" \"$(ps -o pgid= -p $$)\" > \
             \"$SERVICE_STARTED\"; while :; do :; done",
            "--",
            "sh",
            "-c",
            "exit 0",
        ])
        .env("HOME", home.path())
        .env("XDG_STATE_HOME", home.path().join("state"))
        .env("CLIBOX_TEST_EVENTS", &observations)
        .env("CLIBOX_LAUNCHER", clibox_launcher)
        .env("CLIBOX_TEST_BINARY", env!("CARGO_BIN_EXE_clibox"));
    let (launcher, mut terminal) = terminal_process_command(node, false);
    let mut launcher = spawn_terminal(launcher);
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    let (service_group, early_status) = loop {
        let service_group = fs::read_to_string(&service_started)
            .ok()
            .and_then(|identity| {
                if !identity.ends_with('\n') {
                    return None;
                }
                let mut fields = identity.split_whitespace();
                let pid = fields.next()?.parse::<i32>().ok()?;
                let group = fields.next()?.parse::<i32>().ok()?;
                (pid > 0 && group > 0 && fields.next().is_none()).then_some(group)
            });
        if service_group.is_some() {
            break (service_group, None);
        }
        if let Some(status) = launcher.try_wait().unwrap() {
            break (None, Some(status));
        }
        if std::time::Instant::now() >= deadline {
            break (None, None);
        }
        thread::sleep(Duration::from_millis(10));
    };
    if service_group.is_none() {
        let status = early_status.unwrap_or_else(|| {
            let _ = unsafe { libc::kill(-(launcher.id() as libc::pid_t), libc::SIGKILL) };
            launcher.wait().unwrap()
        });
        panic!(
            "managed service did not start through the Node launcher: {status:?}; terminal: {}",
            read_terminal(terminal)
        );
    }

    let service_group = service_group.unwrap();
    let interrupted_at = std::time::Instant::now();
    terminal.write_all(&[3]).unwrap();
    let status = loop {
        if let Some(status) = launcher.try_wait().unwrap() {
            break status;
        }
        if interrupted_at.elapsed() >= Duration::from_secs(5) {
            // Both process groups belong solely to this fixture. Bound failure
            // cleanup as well as the successful acknowledgement branches.
            unsafe {
                libc::kill(-(launcher.id() as libc::pid_t), libc::SIGKILL);
                libc::kill(-service_group, libc::SIGKILL);
            }
            launcher.wait().unwrap();
            assert_process_group_stopped(service_group);
            panic!("terminal interrupt did not finish within the fixture budget");
        }
        thread::sleep(Duration::from_millis(10));
    };

    assert_eq!(status.code(), Some(130), "{status:?}");
    // Kernel terminal delivery reaches Node and the native handler
    // independently. The ACK may arrive before Node opens its 10ms pending
    // window, or after its timer expires. Both are intentionally uncredited:
    // a recorded fallback forwards SIGINT and may skip native cleanup grace.
    // Never require scheduling order or reintroduce stale ACK credits here.
    let events = fs::read_to_string(&observations).unwrap();
    let events: Vec<_> = events.lines().collect();
    assert_eq!(
        events.iter().filter(|event| **event == "interrupt").count(),
        1,
        "{events:?}"
    );
    let interrupt = events
        .iter()
        .position(|event| *event == "interrupt")
        .unwrap();
    if let Some(forward) = events.iter().position(|event| *event == "forward") {
        assert!(
            forward > interrupt,
            "fallback preceded the Node interrupt: {events:?}"
        );
        assert!(
            !events[interrupt + 1..forward].contains(&"ack"),
            "an ACK inside the pending window was not credited: {events:?}"
        );
        assert_eq!(
            events.iter().filter(|event| **event == "forward").count(),
            1,
            "{events:?}"
        );
    } else {
        assert!(
            events[interrupt + 1..].contains(&"ack"),
            "missing early ACK: {events:?}"
        );
        assert!(
            interrupted_at.elapsed() >= Duration::from_millis(350),
            "acknowledged interrupt skipped service cleanup grace: {events:?}"
        );
    }
    assert!(
        interrupted_at.elapsed() < Duration::from_secs(5),
        "cleanup exceeded fixture budget: {events:?}"
    );
    assert_process_group_stopped(service_group);
}

#[test]
#[cfg(target_os = "linux")]
fn interactive_output_forwarding_survives_tostop() {
    let home = tempfile::tempdir().unwrap();
    let (wrapper, terminal) = terminal_command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "100ms",
            "--idle-timeout",
            "30s",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "printf forwarded-output; sleep 30",
        ],
    );
    enable_terminal_tostop(&terminal);
    let mut wrapper = spawn_terminal(wrapper);
    let wrapper_group = wrapper.id() as libc::pid_t;

    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    let status = loop {
        if let Some(status) = wrapper.try_wait().unwrap() {
            break status;
        }
        if std::time::Instant::now() >= deadline {
            let _ = unsafe { libc::kill(-wrapper_group, libc::SIGKILL) };
            let foreground_group = unsafe { libc::tcgetpgrp(terminal.as_raw_fd()) };
            if foreground_group > 0 && foreground_group != wrapper_group {
                let _ = unsafe { libc::kill(-foreground_group, libc::SIGKILL) };
            }
            let _ = wrapper.wait();
            panic!("TOSTOP stopped the wrapper while it was forwarding output");
        }
        thread::sleep(Duration::from_millis(20));
    };
    assert_eq!(status.code(), Some(124));
    assert!(read_terminal(terminal).contains("forwarded-output"));
}

#[test]
#[cfg(target_os = "linux")]
fn inherited_output_keeps_foreground_terminal_when_stdin_is_redirected() {
    let home = tempfile::tempdir().unwrap();
    let (wrapper, terminal) = terminal_command_with_redirected_stdin(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "100ms",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "printf inherited-output; sleep 30",
        ],
    );
    enable_terminal_tostop(&terminal);
    let mut wrapper = spawn_terminal(wrapper);
    let wrapper_group = wrapper.id() as libc::pid_t;

    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    let status = loop {
        if let Some(status) = wrapper.try_wait().unwrap() {
            break status;
        }
        if std::time::Instant::now() >= deadline {
            let _ = unsafe { libc::kill(-wrapper_group, libc::SIGKILL) };
            let foreground_group = unsafe { libc::tcgetpgrp(terminal.as_raw_fd()) };
            if foreground_group > 0 && foreground_group != wrapper_group {
                let _ = unsafe { libc::kill(-foreground_group, libc::SIGKILL) };
            }
            let _ = wrapper.wait();
            panic!("TOSTOP stopped inherited output after stdin was redirected");
        }
        thread::sleep(Duration::from_millis(20));
    };
    assert_eq!(status.code(), Some(124));
    assert!(read_terminal(terminal).contains("inherited-output"));
}

#[test]
#[cfg(target_os = "linux")]
fn interactive_workload_stop_suspends_and_resumes_the_wrapper_job() {
    let home = tempfile::tempdir().unwrap();
    let (wrapper, mut terminal) = terminal_command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "5s",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "read value; printf 'reply=%s\\n' \"$value\"",
        ],
    );
    let mut wrapper = spawn_terminal(wrapper);
    let wrapper_group = wrapper.id() as libc::pid_t;
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    let child_group = loop {
        let group = terminal_foreground_group(&terminal);
        if group != wrapper_group {
            break group;
        }
        assert!(
            std::time::Instant::now() < deadline,
            "workload did not receive terminal ownership"
        );
        thread::sleep(Duration::from_millis(20));
    };
    assert_eq!(unsafe { libc::kill(-child_group, libc::SIGTSTP) }, 0);

    let mut stopped = 0;
    let deadline = std::time::Instant::now() + Duration::from_secs(2);
    loop {
        let observed =
            unsafe { libc::waitpid(wrapper_group, &mut stopped, libc::WUNTRACED | libc::WNOHANG) };
        if observed == wrapper_group {
            assert!(libc::WIFSTOPPED(stopped));
            assert_eq!(terminal_foreground_group(&terminal), wrapper_group);
            break;
        }
        assert_eq!(observed, 0, "wrapper exited before it suspended");
        assert!(
            std::time::Instant::now() < deadline,
            "wrapper did not suspend after the workload stopped"
        );
        thread::sleep(Duration::from_millis(20));
    }

    assert_eq!(unsafe { libc::kill(wrapper_group, libc::SIGCONT) }, 0);
    terminal.write_all(b"answer\n").unwrap();
    assert!(wrapper.wait().unwrap().success());
    assert!(read_terminal(terminal).contains("reply=answer"));
}

#[test]
fn timeout_terminates_owned_descendants() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("descendant-pid");
    let assignment = format!("MARKER={}", marker.display());
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "50ms",
            "--kill-after",
            "0",
            &assignment,
            "--",
            "sh",
            "-c",
            "sleep 30 & echo $! > \"$MARKER\"; wait",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("timeout left an owned descendant running");
}

#[test]
fn caller_marker_cannot_disable_root_descendant_cleanup() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("caller-marker-descendant-pid");
    let assignment = format!("MARKER={}", marker.display());
    let mut command = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "0",
            &assignment,
            "--",
            "sh",
            "-c",
            "sleep 30 & pid=$!; printf '%s %s\\n' \"$pid\" \"$(ps -o pgid= -p \"$pid\")\" > \
             \"$MARKER\"; wait",
        ],
    );
    command.env("CLIBOX_RUN_PARENT_WRAPPER", "1");
    let mut fixture = NestedProcessFixture::new(command, &marker);
    // This tests ownership, not startup speed. Observe the live descendant
    // before triggering cleanup; a short overall deadline can legitimately
    // expire before a busy CI host schedules its PID publication.
    let (workload, group) = fixture.workload(Duration::from_secs(8)).unwrap_or_else(|| {
        panic!(
            "caller-marker workload did not start; wrapper status: {:?}; stderr: {}",
            fixture.child.try_wait(),
            fixture.stderr()
        )
    });
    assert_process_chain_in_group(fixture.child.id(), workload, group);
    assert_eq!(
        unsafe { libc::kill(fixture.child.id() as i32, libc::SIGTERM) },
        0,
        "could not cancel caller-marker wrapper"
    );
    let status = fixture
        .wait(Duration::from_secs(5))
        .expect("caller-marker wrapper did not return after SIGTERM");
    assert_eq!(status.code(), Some(143));
    assert_process_group_stopped(group);
}

#[test]
fn wrapper_preserves_explicit_parent_marker_workload_value() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "1s",
            "CLIBOX_RUN_PARENT_WRAPPER=workload-value",
            "--",
            "sh",
            "-c",
            "test \"$CLIBOX_RUN_PARENT_WRAPPER\" = workload-value",
        ],
    )
    .output()
    .unwrap();

    assert!(output.status.success(), "{output:?}");
}

#[test]
fn outer_timeout_terminates_descendants_of_a_nested_wrapper() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("nested-descendant-pid");
    let assignment = format!("MARKER={}", marker.display());
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "100ms",
            "--kill-after",
            "0",
            &assignment,
            "--",
            env!("CARGO_BIN_EXE_clibox"),
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "10s",
            "--",
            "sh",
            "-c",
            "sleep 30 & echo $! > \"$MARKER\"; wait",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("outer timeout left a nested wrapper descendant running");
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
#[test]
fn outer_timeout_cleans_sigterm_ignoring_workloads_in_deep_native_chains() {
    for depth in [3, 4] {
        let home = tempfile::tempdir().unwrap();
        let marker = home.path().join(format!("native-depth-{depth}-workload"));
        let args = nested_native_chain_args(depth, "5s");
        let references: Vec<_> = args.iter().map(String::as_str).collect();
        let mut command = command(home.path(), &references);
        command.env("MARKER", &marker);
        let mut fixture = NestedProcessFixture::new(command, &marker);

        let (workload, group) = fixture.workload(Duration::from_secs(8)).unwrap_or_else(|| {
            panic!(
                "native depth {depth} workload did not start; wrapper status: {:?}; stderr: {}",
                fixture.child.try_wait(),
                fixture.stderr()
            )
        });
        assert!(group > 0);
        assert_process_chain_in_group(fixture.child.id(), workload, group);

        let status = fixture
            .wait(Duration::from_secs(10))
            .unwrap_or_else(|| panic!("native depth {depth} outer timeout did not return"));
        assert_eq!(status.code(), Some(124), "native depth {depth}");
        assert_process_group_stopped(group);
    }
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
#[test]
fn outer_sigterm_cleans_a_deep_native_chain_before_return() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("native-cancellation-workload");
    let args = nested_native_chain_args(3, "30s");
    let references: Vec<_> = args.iter().map(String::as_str).collect();
    let mut command = command(home.path(), &references);
    command.env("MARKER", &marker);
    let mut fixture = NestedProcessFixture::new(command, &marker);

    let (workload, group) = fixture
        .workload(Duration::from_secs(8))
        .expect("native cancellation workload did not start");
    assert_process_chain_in_group(fixture.child.id(), workload, group);
    let signal_result = unsafe { libc::kill(fixture.child.id() as i32, libc::SIGTERM) };
    assert_eq!(signal_result, 0, "could not cancel outer wrapper");

    let status = fixture
        .wait(Duration::from_secs(5))
        .expect("outer wrapper did not return after SIGTERM");
    assert_eq!(status.code(), Some(143));
    assert_process_group_stopped(group);
}

#[test]
fn nested_shell_wrapper_owns_its_descendants() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("nested-shell-descendant-pid");
    let assignment = format!("MARKER={}", marker.display());
    let script = format!(
        "exec \"{}\" run with-timeout --timeout 100ms --kill-after 0 -- sh -c 'sleep 30 & echo $! \
         > \"$MARKER\"; wait'",
        env!("CARGO_BIN_EXE_clibox")
    );
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "5s",
            "--kill-after",
            "0",
            &assignment,
            "--",
            "sh",
            "-c",
            &script,
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124), "{output:?}");
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("nested shell wrapper left a descendant running");
}

#[test]
fn outer_timeout_terminates_descendants_of_a_nested_npm_launcher() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("nested-npm-descendant-pid");
    let node_home = format!(
        "HOME={}",
        std::env::var("HOME").expect("the Node launcher test needs a host home directory")
    );
    let launcher = home.path().join("launcher.cjs");
    let clibox_launcher = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../packages/clibox/src/launcher.cjs")
        .canonicalize()
        .unwrap();
    fs::write(
        &launcher,
        concat!(
            "const { launch } = require(process.env.CLIBOX_LAUNCHER);\n",
            "launch(process.env.CLIBOX_TEST_BINARY, process.argv.slice(2)).then(({ code, signal \
             }) => {\n",
            "  if (signal) process.kill(process.pid, signal);\n",
            "  else process.exitCode = code ?? 1;\n",
            "});\n",
        ),
    )
    .unwrap();
    let assignment = format!("MARKER={}", marker.display());
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "5s",
            "--kill-after",
            "0",
            &node_home,
            &assignment,
            "--",
            "node",
            launcher.to_str().unwrap(),
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "10s",
            "--",
            "sh",
            "-c",
            "sleep 30 & echo $! > \"$MARKER\"; wait",
        ],
    )
    .env("CLIBOX_LAUNCHER", clibox_launcher)
    .env("CLIBOX_TEST_BINARY", env!("CARGO_BIN_EXE_clibox"))
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124));
    assert!(
        marker.is_file(),
        "nested npm workload did not start: {output:?}"
    );
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("outer timeout left a nested npm launcher descendant running");
}

#[cfg(any(target_os = "linux", target_os = "macos"))]
#[test]
fn outer_timeout_terminates_descendants_of_nested_npm_launcher_chain() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("nested-npm-chain-descendant-pid");
    let launcher = home.path().join("launcher.cjs");
    let clibox_launcher = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
        .join("../../packages/clibox/src/launcher.cjs")
        .canonicalize()
        .unwrap();
    fs::write(
        &launcher,
        concat!(
            "const { launch } = require(process.env.CLIBOX_LAUNCHER);\n",
            "launch(process.env.CLIBOX_TEST_BINARY, process.argv.slice(2)).then(({ code, signal \
             }) => {\n",
            "  if (signal) process.kill(process.pid, signal);\n",
            "  else process.exitCode = code ?? 1;\n",
            "});\n",
        ),
    )
    .unwrap();
    let launcher = launcher.to_str().unwrap();
    let mut node_2 = vec!["node".to_owned(), launcher.to_owned()];
    node_2.extend(wrapper_flags("30s", "10s"));
    node_2.extend([
        "sh".to_owned(),
        "-c".to_owned(),
        "trap '' TERM; printf '%s %s\\n' \"$$\" \"$(ps -o pgid= -p $$)\" > \"$MARKER\"; exec \
         sleep 30"
            .to_owned(),
    ]);
    let mut node_1 = vec!["node".to_owned(), launcher.to_owned()];
    node_1.extend(wrapper_flags("30s", "10s"));
    node_1.extend(node_2);

    let mut args = wrapper_flags("5s", "0");
    args.pop();
    args.push(format!(
        "HOME={}",
        std::env::var("HOME").expect("the Node launcher test needs a host home directory")
    ));
    args.push("--".to_owned());
    args.extend(node_1);
    let references: Vec<_> = args.iter().map(String::as_str).collect();
    let mut command = command(home.path(), &references);
    command
        .env("MARKER", &marker)
        .env("CLIBOX_LAUNCHER", clibox_launcher)
        .env("CLIBOX_TEST_BINARY", env!("CARGO_BIN_EXE_clibox"));
    let mut fixture = NestedProcessFixture::new(command, &marker);

    let (workload, group) = fixture
        .workload(Duration::from_secs(8))
        .expect("nested npm workload did not start");
    assert_process_chain_in_group(fixture.child.id(), workload, group);
    let status = fixture
        .wait(Duration::from_secs(8))
        .expect("outer timeout did not return for the nested npm chain");
    assert_eq!(status.code(), Some(124));
    assert_process_group_stopped(group);
}

#[test]
fn completed_workload_cleans_up_its_background_descendants() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("completed-descendant-pid");
    let started = home.path().join("completed-descendant-started");
    let assignment = format!("MARKER={}", marker.display());
    let started_assignment = format!("STARTED={}", started.display());
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--idle-timeout",
            "1s",
            "--kill-after",
            "4s",
            &assignment,
            &started_assignment,
            "--",
            "sh",
            "-c",
            // The descendant stays active longer than the idle interval so
            // an implementation that discards cleanup activity returns 124.
            // The one-second boundary absorbs slower CI scheduler turns.
            "sh -c 'on_term() { i=0; while [ \"$i\" -lt 100 ]; do printf \
             descendant-cleanup-output >&2; sleep 0.02; i=$((i + 1)); done; exit 0; }; trap \
             on_term TERM; : > \"$STARTED\"; while :; do sleep 30; done' & echo $! > \"$MARKER\"; \
             while [ ! -f \"$STARTED\" ]; do sleep 0.01; done; printf workload-output >&2",
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("descendant-cleanup-output"),
        "descendant cleanup output was not forwarded: {output:?}"
    );
    let pid = fs::read_to_string(marker)
        .unwrap()
        .trim()
        .parse::<i32>()
        .unwrap();
    for _ in 0..50 {
        if unsafe { libc::kill(pid, 0) } == -1
            && std::io::Error::last_os_error().raw_os_error() == Some(libc::ESRCH)
        {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("completed workload left an owned descendant running");
}

#[test]
fn completed_workload_descendant_cleanup_respects_the_overall_timeout() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "100ms",
            "--kill-after",
            "250ms",
            "--",
            "sh",
            "-c",
            "sh -c 'trap \"\" TERM; while :; do sleep 1; done' &",
        ],
    )
    .output()
    .unwrap();

    assert_eq!(output.status.code(), Some(124), "{output:?}");
}

#[test]
fn wrappers_preserve_a_leading_literal_workload_separator() {
    use std::os::unix::fs::PermissionsExt;

    let home = tempfile::tempdir().unwrap();
    let executable = home.path().join("tool=value");
    fs::write(&executable, "#!/bin/sh\nexit 0\n").unwrap();
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "1s",
            "--",
            executable.to_str().unwrap(),
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
}

#[test]
fn first_cancellation_honors_the_configured_cleanup_grace() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("graceful-cleanup");
    let ready = home.path().join("graceful-cleanup-ready");
    let assignment = format!("MARKER={}", marker.display());
    let ready_assignment = format!("READY={}", ready.display());
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "500ms",
            &assignment,
            &ready_assignment,
            "--",
            "sh",
            "-c",
            ": > \"$READY\"; trap 'sleep 0.1; : > \"$MARKER\"; exit 0' TERM; while :; do :; done",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    let ready_deadline = std::time::Instant::now() + Duration::from_secs(5);
    while !ready.is_file() {
        if wrapper.try_wait().unwrap().is_some() || std::time::Instant::now() >= ready_deadline {
            let _ = wrapper.kill();
            let _ = wrapper.wait();
            panic!("workload did not become ready for cancellation");
        }
        thread::sleep(Duration::from_millis(20));
    }
    assert_eq!(unsafe { libc::kill(wrapper.id() as i32, libc::SIGINT) }, 0);
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    while wrapper.try_wait().unwrap().is_none() {
        if std::time::Instant::now() >= deadline {
            let _ = wrapper.kill();
            let _ = wrapper.wait();
            panic!("first cancellation did not complete bounded cleanup");
        }
        thread::sleep(Duration::from_millis(20));
    }
    assert_eq!(wrapper.wait().unwrap().code(), Some(130));
    assert!(
        marker.is_file(),
        "first cancellation skipped the grace period"
    );
}

#[test]
fn cancellation_forwards_shutdown_output_before_returning() {
    let home = tempfile::tempdir().unwrap();
    let ready = home.path().join("cancellation-output-ready");
    let ready_assignment = format!("READY={}", ready.display());
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--idle-timeout",
            "30s",
            "--kill-after",
            "500ms",
            &ready_assignment,
            "--",
            "sh",
            "-c",
            ": > \"$READY\"; trap 'printf cancellation-shutdown >&2; exit 0' TERM; while :; do :; \
             done",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::piped())
    .spawn()
    .unwrap();
    let ready_deadline = std::time::Instant::now() + Duration::from_secs(5);
    while !ready.is_file() {
        if wrapper.try_wait().unwrap().is_some() || std::time::Instant::now() >= ready_deadline {
            let _ = wrapper.kill();
            let _ = wrapper.wait();
            panic!("workload did not become ready for cancellation");
        }
        thread::sleep(Duration::from_millis(20));
    }

    assert_eq!(unsafe { libc::kill(wrapper.id() as i32, libc::SIGINT) }, 0);
    let output = wrapper.wait_with_output().unwrap();

    assert_eq!(output.status.code(), Some(130), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("cancellation-shutdown"),
        "cancellation shutdown output was not forwarded: {output:?}"
    );
}

#[test]
fn cancellation_during_completed_descendant_cleanup_overrides_child_success() {
    let home = tempfile::tempdir().unwrap();
    let ready = home.path().join("descendant-cleanup-ready");
    let started = home.path().join("descendant-cleanup-started");
    let ready_assignment = format!("READY={}", ready.display());
    let started_assignment = format!("STARTED={}", started.display());
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "30s",
            "--kill-after",
            "500ms",
            &ready_assignment,
            &started_assignment,
            "--",
            "sh",
            "-c",
            "sh -c 'trap \": > \\\"$READY\\\"\" TERM; : > \"$STARTED\"; while :; do sleep 1; \
             done' & \\
             while [ ! -f \"$STARTED\" ]; do sleep 0.01; done",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    let ready_deadline = std::time::Instant::now() + Duration::from_secs(5);
    while !ready.is_file() {
        if wrapper.try_wait().unwrap().is_some() || std::time::Instant::now() >= ready_deadline {
            let _ = wrapper.kill();
            let _ = wrapper.wait();
            panic!("descendant cleanup did not begin");
        }
        thread::sleep(Duration::from_millis(20));
    }

    assert_eq!(unsafe { libc::kill(wrapper.id() as i32, libc::SIGINT) }, 0);
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    let status = loop {
        if let Some(status) = wrapper.try_wait().unwrap() {
            break status;
        }
        if std::time::Instant::now() >= deadline {
            let _ = wrapper.kill();
            let _ = wrapper.wait();
            panic!("cancellation did not complete descendant cleanup");
        }
        thread::sleep(Duration::from_millis(20));
    };

    assert_eq!(status.code(), Some(130));
}

#[test]
fn service_probe_ignores_custom_ca_override_variables() {
    let certificate = rcgen::generate_simple_self_signed(vec!["127.0.0.1".to_owned()]).unwrap();
    let home = tempfile::tempdir().unwrap();
    let ca = home.path().join("custom-ca.pem");
    let marker = home.path().join("custom-ca-workload");
    let assignment = format!("MARKER={}", marker.display());
    fs::write(&ca, certificate.cert.pem()).unwrap();
    let config = rustls::ServerConfig::builder_with_provider(std::sync::Arc::new(
        rustls::crypto::ring::default_provider(),
    ))
    .with_safe_default_protocol_versions()
    .unwrap()
    .with_no_client_auth()
    .with_single_cert(
        vec![certificate.cert.der().clone()],
        rustls::pki_types::PrivatePkcs8KeyDer::from(certificate.signing_key.serialize_der()).into(),
    )
    .unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let port = listener.local_addr().unwrap().port();
    let server = thread::spawn(move || {
        let (stream, _) = listener.accept().unwrap();
        stream
            .set_read_timeout(Some(Duration::from_secs(5)))
            .unwrap();
        let mut stream = rustls::StreamOwned::new(
            rustls::ServerConnection::new(std::sync::Arc::new(config)).unwrap(),
            stream,
        );
        let _ = stream.read(&mut [0; 4096]);
        let _ = stream.write_all(b"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n");
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("https://127.0.0.1:{port}/health"),
            "--ready-timeout",
            "1s",
            &assignment,
            "--",
            "sh",
            "-c",
            ": > \"$MARKER\"",
        ],
    )
    .env("SSL_CERT_FILE", &ca)
    .env("SSL_CERT_DIR", home.path())
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(!marker.exists());
    server.join().unwrap();
}

#[test]
fn external_service_is_observed_without_becoming_owned() {
    let home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = [0u8; 1024];
        let _ = stream.read(&mut request);
        stream
            .write_all(b"HTTP/1.1 204 No Content\r\nContent-Length: 0\r\n\r\n")
            .unwrap();
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success());
    server.join().unwrap();
}

fn read_request_headers(stream: &mut std::net::TcpStream) -> std::io::Result<bool> {
    // Darwin inherits the listener's nonblocking mode on accept. A reply
    // before complete request headers can reset the client connection and
    // turn an expected 503 into an unintended transport failure.
    stream.set_nonblocking(false)?;
    let deadline = Instant::now() + Duration::from_secs(3);
    stream.set_write_timeout(Some(Duration::from_secs(3)))?;
    let mut request = [0u8; 4096];
    let mut received = 0;
    loop {
        if received == request.len() {
            return Err(std::io::Error::new(
                std::io::ErrorKind::InvalidData,
                "readiness fixture request headers exceeded their bound",
            ));
        }
        let remaining = deadline.saturating_duration_since(Instant::now());
        if remaining.is_zero() {
            return Ok(false);
        }
        stream.set_read_timeout(Some(remaining))?;
        match stream.read(&mut request[received..]) {
            Ok(0) => return Ok(false),
            Ok(count) => received += count,
            Err(error) if error.kind() == std::io::ErrorKind::Interrupted => continue,
            Err(error)
                if matches!(
                    error.kind(),
                    std::io::ErrorKind::ConnectionReset
                        | std::io::ErrorKind::TimedOut
                        | std::io::ErrorKind::WouldBlock
                ) =>
            {
                return Ok(false);
            }
            Err(error) => return Err(error),
        }
        if request[..received]
            .windows(4)
            .any(|part| part == b"\r\n\r\n")
        {
            return Ok(true);
        }
    }
}

struct ReadinessFixture {
    worker: Option<thread::JoinHandle<std::io::Result<Vec<Instant>>>>,
    stopped: Arc<AtomicBool>,
}

impl ReadinessFixture {
    fn new(listener: TcpListener) -> Self {
        let stopped = Arc::new(AtomicBool::new(false));
        let worker_stop = stopped.clone();
        Self {
            worker: Some(thread::spawn(move || {
                serve_readiness_sequence(listener, worker_stop)
            })),
            stopped,
        }
    }

    fn finish(mut self) -> Vec<Instant> {
        self.worker.take().unwrap().join().unwrap().unwrap()
    }
}

impl Drop for ReadinessFixture {
    fn drop(&mut self) {
        self.stopped.store(true, Ordering::SeqCst);
        if let Some(worker) = self.worker.take() {
            // Accept polls cancellation; an accepted stream has bounded I/O.
            // Join even when a wrapper assertion or spawn fails.
            let _ = worker.join();
        }
    }
}

fn serve_readiness_sequence(
    listener: TcpListener,
    stopped: Arc<AtomicBool>,
) -> std::io::Result<Vec<Instant>> {
    listener.set_nonblocking(true)?;
    let deadline = Instant::now() + Duration::from_secs(10);
    let mut observed = Vec::new();
    for status in ["503 Service Unavailable", "204 No Content"] {
        let mut stream = loop {
            match listener.accept() {
                Ok((stream, _)) => break stream,
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    if stopped.load(Ordering::SeqCst) || Instant::now() >= deadline {
                        return Err(std::io::ErrorKind::TimedOut.into());
                    }
                    thread::sleep(Duration::from_millis(5));
                }
                Err(error) => return Err(error),
            }
        };
        observed.push(Instant::now());
        if !read_request_headers(&mut stream)? {
            return Err(std::io::ErrorKind::UnexpectedEof.into());
        }
        stream.write_all(
            format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                .as_bytes(),
        )?;
    }
    Ok(observed)
}

#[test]
fn external_service_waits_after_an_unready_preflight() {
    let home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = ReadinessFixture::new(listener);
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "100ms",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .env("RUST_LOG", "debug")
    .output()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
    let observed = server.finish();
    assert!(
        observed[1].saturating_duration_since(observed[0]) >= Duration::from_millis(80),
        "the second probe did not honor the configured interval: {observed:?}"
    );
}

#[test]
fn managed_service_waits_after_an_unready_preflight() {
    let home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let (observed_tx, observed_rx) = std::sync::mpsc::sync_channel(1);
    let server = thread::spawn(move || {
        let mut observed = Vec::new();
        for status in ["503 Service Unavailable", "204 No Content"] {
            let (mut stream, _) = listener.accept().unwrap();
            observed.push(std::time::Instant::now());
            let mut request = [0u8; 1024];
            let _ = stream.read(&mut request);
            stream
                .write_all(format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\n\r\n").as_bytes())
                .unwrap();
        }
        observed_tx.send(observed).unwrap();
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "100ms",
            "--service",
            "sh",
            "-c",
            "sleep 30",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
    let observed = observed_rx.recv_timeout(Duration::from_secs(5)).unwrap();
    assert!(
        observed[1].saturating_duration_since(observed[0]) >= Duration::from_millis(80),
        "the first managed-service probe did not honor the configured interval: {observed:?}"
    );
    server.join().unwrap();
}

#[test]
fn managed_service_forwards_shutdown_output_before_success() {
    let home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        for status in ["503 Service Unavailable", "204 No Content"] {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0u8; 1024];
            let _ = stream.read(&mut request);
            stream
                .write_all(format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\n\r\n").as_bytes())
                .unwrap();
        }
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--service",
            "sh",
            "-c",
            "trap 'printf managed-service-shutdown >&2; exit 0' TERM; while :; do sleep 1; done",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();

    assert!(output.status.success(), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("managed-service-shutdown"),
        "managed service shutdown output was not forwarded: {output:?}"
    );
    server.join().unwrap();
}

#[test]
fn managed_service_forwards_shutdown_output_before_readiness_timeout() {
    fn respond_not_ready(mut stream: std::net::TcpStream) -> std::io::Result<bool> {
        if !read_request_headers(&mut stream)? {
            return Ok(false);
        }
        match stream.write_all(
            b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
        ) {
            Ok(()) => Ok(true),
            // The CLI can cancel the final probe at its readiness deadline.
            Err(error)
                if matches!(
                    error.kind(),
                    std::io::ErrorKind::BrokenPipe | std::io::ErrorKind::ConnectionReset
                ) =>
            {
                Ok(false)
            }
            Err(error) => Err(error),
        }
    }

    // This process fixture must install the service's TERM trap before its
    // readiness deadline expires. Native HTTP-client setup and child scheduling
    // can consume 300ms on a loaded macOS runner; that tests pre-spawn timeout
    // instead of shutdown-output forwarding. Keep a bounded startup allowance
    // here until this process fixture can control the readiness clock.
    let home = tempfile::tempdir().unwrap();
    let workload_started = home.path().join("workload-started");
    let workload_marker = format!("WORKLOAD_MARKER={}", workload_started.display());
    let service_started = home.path().join("managed-service-started");
    let service_marker = format!("SERVICE_STARTED={}", service_started.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    listener.set_nonblocking(true).unwrap();
    let address = listener.local_addr().unwrap();
    let finished = Arc::new(AtomicBool::new(false));
    let server_finished = Arc::clone(&finished);
    let server = thread::spawn(move || {
        // CLI startup can outlast its own readiness timeout on a loaded host.
        // Bound startup separately and keep serving 503 until the CLI exits.
        let preflight_deadline = std::time::Instant::now() + Duration::from_secs(10);
        let (stream, _) = loop {
            match listener.accept() {
                Ok(stream) => break stream,
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    assert!(
                        std::time::Instant::now() < preflight_deadline,
                        "readiness preflight did not arrive"
                    );
                    thread::sleep(Duration::from_millis(1));
                }
                Err(error) => panic!("readiness fixture could not accept a request: {error}"),
            }
        };
        assert!(
            respond_not_ready(stream).expect("readiness fixture preflight response failed"),
            "readiness preflight closed before receiving its response"
        );
        let service_deadline = std::time::Instant::now() + Duration::from_secs(5);
        while !service_started.exists() {
            assert!(
                std::time::Instant::now() < service_deadline,
                "managed service did not install its shutdown trap"
            );
            thread::sleep(Duration::from_millis(1));
        }
        let mut closed_response = false;
        while !server_finished.load(Ordering::Acquire) {
            match listener.accept() {
                Ok((mut stream, _)) => {
                    if !closed_response {
                        // Close once after complete headers without a response
                        // to prove temporary transport failures remain
                        // retryable.
                        closed_response = read_request_headers(&mut stream)
                            .expect("readiness fixture closed-connection control failed");
                        continue;
                    }
                    respond_not_ready(stream).expect("readiness fixture response failed");
                }
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(5));
                }
                Err(error) => panic!("readiness fixture could not accept a request: {error}"),
            }
        }
        assert!(
            closed_response,
            "the temporary closed-connection control did not run"
        );
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--ready-timeout",
            "5s",
            "--service",
            &service_marker,
            "sh",
            "-c",
            "trap 'printf managed-service-readiness-timeout >&2; exit 0' TERM; : > \
             \"$SERVICE_STARTED\"; while :; do sleep 1; done",
            "--",
            &workload_marker,
            "sh",
            "-c",
            ": > \"$WORKLOAD_MARKER\"",
        ],
    )
    .output()
    .unwrap();
    finished.store(true, Ordering::Release);
    server.join().unwrap();

    assert_eq!(output.status.code(), Some(124), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("managed-service-readiness-timeout"),
        "managed service shutdown output was not forwarded: {output:?}"
    );
    assert!(
        !workload_started.exists(),
        "workload started after readiness timed out: {output:?}"
    );
}

#[test]
fn managed_service_must_remain_alive_after_a_successful_probe() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("workload-started");
    let marker_assignment = format!("WORKLOAD_MARKER={}", marker.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        for (index, status) in ["503 Service Unavailable", "204 No Content"]
            .into_iter()
            .enumerate()
        {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0u8; 1024];
            let _ = stream.read(&mut request);
            if index == 1 {
                // Keep the successful request in flight until the managed
                // service has exited after the loop's pre-probe check.
                thread::sleep(Duration::from_millis(1_100));
            }
            stream
                .write_all(format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\n\r\n").as_bytes())
                .unwrap();
        }
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--ready-timeout",
            "3s",
            "--service",
            "sh",
            "-c",
            "sleep 1",
            "--",
            &marker_assignment,
            "sh",
            "-c",
            "echo started > \"$WORKLOAD_MARKER\"",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(1), "{output:?}");
    assert!(
        !marker.exists(),
        "workload started after its managed service exited: {output:?}"
    );
    server.join().unwrap();
}

#[test]
fn managed_service_failure_stops_the_workload_before_service_cleanup_finishes() {
    let home = tempfile::tempdir().unwrap();
    let stopped = home.path().join("workload-stopped");
    let workload_started = home.path().join("workload-started");
    let service_child_ready = home.path().join("service-child-ready");
    let service_exited = home.path().join("service-exited");
    let workload_marker = format!("WORKLOAD_MARKER={}", stopped.display());
    let workload_started_marker = format!("WORKLOAD_STARTED={}", workload_started.display());
    let service_child_ready_marker =
        format!("SERVICE_CHILD_READY={}", service_child_ready.display());
    let service_exited_marker = format!("SERVICE_EXITED={}", service_exited.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        for status in ["503 Service Unavailable", "204 No Content"] {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0u8; 1024];
            let _ = stream.read(&mut request);
            stream
                .write_all(format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\n\r\n").as_bytes())
                .unwrap();
        }
    });
    let wrapper = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--kill-after",
            "500ms",
            "--service",
            &workload_started_marker,
            &service_child_ready_marker,
            &service_exited_marker,
            "sh",
            "-c",
            // The service must fail only after both children are ready; a fixed
            // delay can let a busy runner end it before the workload is spawned.
            "sh -c 'trap \"\" TERM; : > \"$SERVICE_CHILD_READY\"; while :; do sleep 1; done' & \
             attempts=0; until [ -f \"$WORKLOAD_STARTED\" ] && [ -f \"$SERVICE_CHILD_READY\" ]; \
             do [ \"$attempts\" -lt 500 ] || exit 1; attempts=$((attempts + 1)); sleep 0.01; \
             done; : > \"$SERVICE_EXITED\"",
            "--",
            &workload_marker,
            &workload_started_marker,
            "sh",
            "-c",
            "trap ': > \"$WORKLOAD_MARKER\"; exit 0' TERM; : > \"$WORKLOAD_STARTED\"; while :; do \
             :; done",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    let start_deadline = std::time::Instant::now() + Duration::from_secs(5);
    while !workload_started.is_file() && std::time::Instant::now() < start_deadline {
        thread::sleep(Duration::from_millis(10));
    }
    let workload_started_before_service_exit = workload_started.is_file();
    while !service_exited.is_file() && std::time::Instant::now() < start_deadline {
        thread::sleep(Duration::from_millis(10));
    }
    let service_exited_before_timeout = service_exited.is_file();
    let deadline = std::time::Instant::now() + Duration::from_millis(400);
    while !stopped.is_file() && std::time::Instant::now() < deadline {
        thread::sleep(Duration::from_millis(10));
    }
    let stopped_before_service_cleanup_finished = stopped.is_file();
    let output = wrapper.wait_with_output().unwrap();

    assert_eq!(output.status.code(), Some(1), "{output:?}");
    assert!(
        workload_started_before_service_exit,
        "the workload did not start before service failure: {output:?}"
    );
    assert!(
        service_exited_before_timeout,
        "the managed service did not exit: {output:?}"
    );
    assert!(
        stopped_before_service_cleanup_finished,
        "the workload was not stopped before managed-service cleanup: {output:?}"
    );
    server.join().unwrap();
}

#[test]
fn managed_service_cancellation_stops_service_before_workload_cleanup_finishes() {
    let home = tempfile::tempdir().unwrap();
    let service_stopped = home.path().join("service-stopped");
    let workload_started = home.path().join("workload-started");
    let service_marker = format!("SERVICE_STOPPED={}", service_stopped.display());
    let workload_marker = format!("WORKLOAD_STARTED={}", workload_started.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        for status in ["503 Service Unavailable", "204 No Content"] {
            let (mut stream, _) = listener.accept().unwrap();
            let mut request = [0u8; 1024];
            let _ = stream.read(&mut request);
            stream
                .write_all(format!("HTTP/1.1 {status}\r\nContent-Length: 0\r\n\r\n").as_bytes())
                .unwrap();
        }
    });
    let mut wrapper = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--kill-after",
            "500ms",
            "--service",
            &service_marker,
            "sh",
            "-c",
            "trap ': > \"$SERVICE_STOPPED\"; exit 0' TERM; while :; do :; done",
            "--",
            &workload_marker,
            "sh",
            "-c",
            ": > \"$WORKLOAD_STARTED\"; trap '' TERM; while :; do :; done",
        ],
    )
    .stdout(Stdio::null())
    .stderr(Stdio::null())
    .spawn()
    .unwrap();
    let start_deadline = std::time::Instant::now() + Duration::from_secs(5);
    while !workload_started.is_file() && std::time::Instant::now() < start_deadline {
        thread::sleep(Duration::from_millis(10));
    }
    assert!(
        workload_started.is_file(),
        "workload did not start before cancellation"
    );

    assert_eq!(unsafe { libc::kill(wrapper.id() as i32, libc::SIGINT) }, 0);
    let service_deadline = std::time::Instant::now() + Duration::from_millis(400);
    while !service_stopped.is_file() && std::time::Instant::now() < service_deadline {
        thread::sleep(Duration::from_millis(10));
    }
    let service_stopped_before_workload_cleanup_finished = service_stopped.is_file();
    let status = wrapper.wait().unwrap();

    assert_eq!(status.code(), Some(130), "{status:?}");
    assert!(
        service_stopped_before_workload_cleanup_finished,
        "managed service was not stopped before workload cleanup: {status:?}"
    );
    server.join().unwrap();
}

#[test]
fn second_cancellation_forces_both_managed_service_trees() {
    let home = tempfile::tempdir().unwrap();
    let service_started = home.path().join("second-cancellation-service-started");
    let workload_started = home.path().join("second-cancellation-workload-started");
    let service_marker = format!("SERVICE_STARTED={}", service_started.display());
    let workload_marker = format!("WORKLOAD_STARTED={}", workload_started.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let service_stopped = home.path().join("service-term-observed");
    let workload_stopped = home.path().join("workload-term-observed");
    let server = ReadinessFixture::new(listener);
    let mut wrapper_command = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--kill-after",
            "10s",
            "--service",
            &service_marker,
            "sh",
            "-c",
            "trap ': > \"$SERVICE_STOPPED\"' TERM; printf '%s %s\\n' \"$$\" \"$(ps -o pgid= -p \
             $$)\" > \"$SERVICE_STARTED\"; while :; do sleep 0.05; done",
            "--",
            &workload_marker,
            "sh",
            "-c",
            "trap ': > \"$WORKLOAD_STOPPED\"' TERM; printf '%s %s\\n' \"$$\" \"$(ps -o pgid= -p \
             $$)\" > \"$WORKLOAD_STARTED\"; while :; do sleep 0.05; done",
        ],
    );
    wrapper_command
        .env("SERVICE_STOPPED", &service_stopped)
        .env("WORKLOAD_STOPPED", &workload_stopped)
        .env("RUST_LOG", "debug");
    let mut wrapper = NestedProcessFixture::new(wrapper_command, &workload_started);
    let started_deadline = std::time::Instant::now() + Duration::from_secs(5);
    while (!service_started.is_file() || !workload_started.is_file())
        && std::time::Instant::now() < started_deadline
    {
        thread::sleep(Duration::from_millis(10));
    }
    assert!(
        service_started.is_file() && workload_started.is_file(),
        "managed service and workload did not start before cancellation"
    );

    let (workload_pid, workload_group) = wrapper.workload(Duration::from_secs(5)).unwrap();
    let workload_identity = process_snapshot()[&workload_pid];
    assert_eq!(workload_identity.parent, wrapper.child.id() as i32);
    assert_eq!(workload_identity.group, workload_group);
    assert!(!workload_identity.state_is_zombie);
    let identity_deadline = Instant::now() + Duration::from_secs(5);
    let (service_pid, service_group) = loop {
        if let Ok(identity) = fs::read_to_string(&service_started) {
            let mut fields = identity.split_whitespace();
            if let Some((pid, group)) = fields.next().zip(fields.next()) {
                if let (Ok(pid), Ok(group)) = (pid.parse(), group.parse()) {
                    break (pid, group);
                }
            }
        }
        assert!(
            Instant::now() < identity_deadline,
            "service did not publish complete process identity"
        );
        thread::sleep(Duration::from_millis(5));
    };
    let service_identity = process_snapshot()[&service_pid];
    assert_eq!(service_identity.parent, wrapper.child.id() as i32);
    assert_eq!(service_identity.group, service_group);
    assert!(!service_identity.state_is_zombie);
    // Preserve both admitted original groups for panic cleanup after the
    // wrapper has been reaped, when parent inspection can no longer find them.
    wrapper.additional_owned_groups.push(service_group);

    assert_eq!(
        unsafe { libc::kill(wrapper.child.id() as i32, libc::SIGINT) },
        0
    );
    // A delivered first interrupt is not evidence that cleanup has started.
    // Wait for both original children to acknowledge TERM before escalating.
    let acknowledged_deadline = Instant::now() + Duration::from_secs(5);
    while (!service_stopped.is_file() || !workload_stopped.is_file())
        && Instant::now() < acknowledged_deadline
    {
        thread::sleep(Duration::from_millis(5));
    }
    assert!(
        service_stopped.is_file() && workload_stopped.is_file(),
        "first cancellation did not reach both trees: {}",
        wrapper.stderr()
    );
    let forced_at = std::time::Instant::now();
    assert_eq!(
        unsafe { libc::kill(wrapper.child.id() as i32, libc::SIGINT) },
        0
    );
    let status = wrapper.wait(Duration::from_secs(5)).unwrap_or_else(|| {
        panic!(
            "second cancellation did not skip the remaining ten-second grace: {}",
            wrapper.stderr()
        )
    });

    assert_eq!(status.code(), Some(130), "{status:?}");
    assert!(
        forced_at.elapsed() < Duration::from_secs(5),
        "second cancellation waited for a new managed-service grace interval"
    );
    assert_process_group_stopped(workload_group);
    assert_process_group_stopped(service_group);
    server.finish();
}

#[test]
fn managed_service_does_not_start_after_the_preflight_exhausts_its_deadline() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("managed-service-started");
    let service_marker = format!("SERVICE_MARKER={}", marker.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = [0u8; 1024];
        let _ = stream.read(&mut request);
        stream
            .write_all(b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")
            .unwrap();
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "200ms",
            "--ready-timeout",
            "20ms",
            "--service",
            &service_marker,
            "sh",
            "-c",
            "echo started > \"$SERVICE_MARKER\"; sleep 30",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(124), "{output:?}");
    assert!(
        !marker.exists(),
        "managed service started after readiness deadline: {output:?}"
    );
    server.join().unwrap();
}

#[test]
fn external_service_waits_for_delayed_readiness() {
    let home = tempfile::tempdir().unwrap();
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        thread::sleep(Duration::from_millis(100));
        let (mut stream, _) = listener.accept().unwrap();
        let mut request = [0u8; 1024];
        let _ = stream.read(&mut request);
        stream
            .write_all(b"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n")
            .unwrap();
    });
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            &format!("http://{address}/health"),
            "--interval",
            "10ms",
            "--ready-timeout",
            "2s",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert!(output.status.success());
    server.join().unwrap();
}

#[test]
fn managed_service_delimiter_accepts_independent_workload_arguments() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-service",
            "http://127.0.0.1:9/health",
            "--ready-timeout",
            "50ms",
            "--service",
            "true",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    // The endpoint can time out while the short-lived service is still being
    // scheduled. The important parser boundary is that this reaches managed
    // service runtime handling, not a CLI usage error that treats the
    // workload as service arguments.
    assert_ne!(output.status.code(), Some(2));
}

#[test]
fn invalid_wrapper_arguments_stay_redacted() {
    let home = tempfile::tempdir().unwrap();
    let output = command(
        home.path(),
        &[
            "run",
            "with-lock",
            "--name",
            "PRIVATE-RUN-MARKER",
            "--scope",
            "user",
            "--project-dir",
            "PRIVATE-RUN-MARKER",
            "--",
            "sh",
            "-c",
            "exit 0",
        ],
    )
    .output()
    .unwrap();
    assert_eq!(output.status.code(), Some(2));
    assert!(!String::from_utf8_lossy(&output.stderr).contains("PRIVATE-RUN-MARKER"));
}
