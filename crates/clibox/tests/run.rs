#![cfg(unix)]

#[cfg(target_os = "linux")]
use std::os::fd::AsRawFd;
#[cfg(unix)]
use std::os::fd::FromRawFd;
#[cfg(target_os = "linux")]
use std::process::Child;
use std::{
    fs,
    io::{Read, Write},
    net::TcpListener,
    os::unix::process::CommandExt,
    process::{Command, Stdio},
    thread,
    time::Duration,
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
    let home = tempfile::tempdir().unwrap();
    let (wrapper, mut terminal) = terminal_command(
        home.path(),
        &[
            "run",
            "with-timeout",
            "--timeout",
            "1s",
            "--kill-after",
            "0",
            "--",
            "sh",
            "-c",
            "read value; printf 'reply=%s\\n' \"$value\"",
        ],
    );
    let mut wrapper = spawn_terminal(wrapper);
    terminal.write_all(b"answer\n").unwrap();
    assert!(wrapper.wait().unwrap().success());
    assert!(read_terminal(terminal).contains("reply=answer"));
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
fn node_launcher_counts_terminal_service_startup_interrupt_once() {
    let home = tempfile::tempdir().unwrap();
    let service_started = home.path().join("node-launcher-service-started");
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
            "launch(process.env.CLIBOX_TEST_BINARY, process.argv.slice(2)).then(({ code }) => {\n",
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
            "trap '' TERM; : > \"$SERVICE_STARTED\"; while :; do :; done",
            "--",
            "sh",
            "-c",
            "exit 0",
        ])
        .env("HOME", home.path())
        .env("XDG_STATE_HOME", home.path().join("state"))
        .env("CLIBOX_LAUNCHER", clibox_launcher)
        .env("CLIBOX_TEST_BINARY", env!("CARGO_BIN_EXE_clibox"));
    let (launcher, mut terminal) = terminal_process_command(node, false);
    let mut launcher = spawn_terminal(launcher);
    let deadline = std::time::Instant::now() + Duration::from_secs(5);
    let early_status = loop {
        if service_started.is_file() {
            break None;
        }
        if let Some(status) = launcher.try_wait().unwrap() {
            break Some(status);
        }
        if std::time::Instant::now() >= deadline {
            break None;
        }
        thread::sleep(Duration::from_millis(10));
    };
    if !service_started.is_file() {
        let status = early_status.unwrap_or_else(|| {
            let _ = unsafe { libc::kill(-(launcher.id() as libc::pid_t), libc::SIGKILL) };
            launcher.wait().unwrap()
        });
        panic!(
            "managed service did not start through the Node launcher: {status:?}; terminal: {}",
            read_terminal(terminal)
        );
    }

    let interrupted_at = std::time::Instant::now();
    terminal.write_all(&[3]).unwrap();
    let status = launcher.wait().unwrap();

    assert_eq!(status.code(), Some(130), "{status:?}");
    assert!(
        interrupted_at.elapsed() >= Duration::from_millis(350),
        "one terminal Ctrl+C skipped the configured service cleanup grace"
    );
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
    .env("CLIBOX_RUN_PARENT_WRAPPER", "1")
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
    panic!("caller marker disabled owned descendant cleanup");
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

#[test]
fn outer_timeout_terminates_descendants_of_nested_npm_launcher_chain() {
    let home = tempfile::tempdir().unwrap();
    let marker = home.path().join("nested-npm-chain-descendant-pid");
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
    assert_eq!(output.status.code(), Some(124), "{output:?}");
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
    panic!("outer timeout left a nested npm launcher chain descendant running");
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

#[test]
fn external_service_waits_after_an_unready_preflight() {
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
        "the second probe did not honor the configured interval: {observed:?}"
    );
    server.join().unwrap();
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
    let home = tempfile::tempdir().unwrap();
    let workload_started = home.path().join("workload-started");
    let workload_marker = format!("WORKLOAD_MARKER={}", workload_started.display());
    let service_started = home.path().join("managed-service-started");
    let service_marker = format!("SERVICE_STARTED={}", service_started.display());
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    listener.set_nonblocking(true).unwrap();
    let address = listener.local_addr().unwrap();
    let server = thread::spawn(move || {
        let deadline = std::time::Instant::now() + Duration::from_secs(1);
        let (mut stream, _) = loop {
            match listener.accept() {
                Ok(stream) => break stream,
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    assert!(
                        std::time::Instant::now() < deadline,
                        "readiness preflight did not arrive"
                    );
                    thread::sleep(Duration::from_millis(1));
                }
                Err(error) => panic!("readiness fixture could not accept a request: {error}"),
            }
        };
        let mut request = [0u8; 1024];
        let _ = stream.read(&mut request);
        stream
            .write_all(b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")
            .unwrap();
        while !service_started.exists() {
            assert!(
                std::time::Instant::now() < deadline,
                "managed service did not install its shutdown trap"
            );
            thread::sleep(Duration::from_millis(1));
        }
        while std::time::Instant::now() < deadline {
            match listener.accept() {
                Ok((mut stream, _)) => {
                    let mut request = [0u8; 1024];
                    let _ = stream.read(&mut request);
                    stream
                        .write_all(b"HTTP/1.1 503 Service Unavailable\r\nContent-Length: 0\r\n\r\n")
                        .unwrap();
                }
                Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                    thread::sleep(Duration::from_millis(5));
                }
                Err(error) => panic!("readiness fixture could not accept a request: {error}"),
            }
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
            "300ms",
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

    assert_eq!(output.status.code(), Some(124), "{output:?}");
    assert!(
        String::from_utf8_lossy(&output.stderr).contains("managed-service-readiness-timeout"),
        "managed service shutdown output was not forwarded: {output:?}"
    );
    assert!(
        !workload_started.exists(),
        "workload started after readiness timed out: {output:?}"
    );
    server.join().unwrap();
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
    let service_exited = home.path().join("service-exited");
    let workload_marker = format!("WORKLOAD_MARKER={}", stopped.display());
    let workload_started_marker = format!("WORKLOAD_STARTED={}", workload_started.display());
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
            &service_exited_marker,
            "sh",
            "-c",
            "sh -c 'trap \"\" TERM; while :; do sleep 1; done' & sleep 0.2; : > \
             \"$SERVICE_EXITED\"",
            "--",
            &workload_marker,
            &workload_started_marker,
            "sh",
            "-c",
            ": > \"$WORKLOAD_STARTED\"; trap ': > \"$WORKLOAD_MARKER\"; exit 0' TERM; while :; do \
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
            "2s",
            "--service",
            &service_marker,
            "sh",
            "-c",
            ": > \"$SERVICE_STARTED\"; trap '' TERM; while :; do :; done",
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

    assert_eq!(unsafe { libc::kill(wrapper.id() as i32, libc::SIGINT) }, 0);
    thread::sleep(Duration::from_millis(100));
    let forced_at = std::time::Instant::now();
    assert_eq!(unsafe { libc::kill(wrapper.id() as i32, libc::SIGINT) }, 0);
    let status = wrapper.wait().unwrap();

    assert_eq!(status.code(), Some(130), "{status:?}");
    assert!(
        forced_at.elapsed() < Duration::from_secs(1),
        "second cancellation waited for a new managed-service grace interval"
    );
    server.join().unwrap();
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
    let reservation = TcpListener::bind("127.0.0.1:0").unwrap();
    let address = reservation.local_addr().unwrap();
    drop(reservation);
    let server = thread::spawn(move || {
        thread::sleep(Duration::from_millis(100));
        let listener = TcpListener::bind(address).unwrap();
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
