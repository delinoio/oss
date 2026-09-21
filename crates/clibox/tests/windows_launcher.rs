#![cfg(windows)]

use std::{
    fs,
    io::{BufRead, BufReader, Write},
    os::windows::process::CommandExt,
    path::PathBuf,
    process::{Command, Stdio},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

use windows_sys::Win32::System::{
    Console::{GenerateConsoleCtrlEvent, SetConsoleCtrlHandler, CTRL_BREAK_EVENT, CTRL_C_EVENT},
    Threading::CREATE_NEW_CONSOLE,
};

#[test]
fn npm_launcher_console_events_preserve_native_cleanup_and_status() {
    let launcher =
        PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../../packages/clibox/src/launcher.cjs");
    if !launcher.is_file() {
        // Standalone Cargo packages do not include the npm workspace. Repository
        // Windows CI and release runners provide Node and run this integration.
        return;
    }
    let output = Command::new(std::env::current_exe().unwrap())
        .args(["--exact", "windows_launcher_console_helper", "--nocapture"])
        .env("CLIBOX_TEST_LAUNCHER", launcher)
        .creation_flags(CREATE_NEW_CONSOLE)
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "stdout: {}\nstderr: {}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
}

#[test]
fn windows_launcher_console_helper() {
    let Some(launcher) = std::env::var_os("CLIBOX_TEST_LAUNCHER") else {
        return;
    };
    unsafe extern "system" fn ignore(_: u32) -> i32 {
        1
    }
    // Reset Git Bash's inherited ignore attribute only in this disposable
    // console. A real handler protects the helper without disabling its child.
    assert_ne!(unsafe { SetConsoleCtrlHandler(None, 0) }, 0);
    assert_ne!(unsafe { SetConsoleCtrlHandler(Some(ignore), 1) }, 0);
    for via_npm in [false, true] {
        for event in [CTRL_C_EVENT, CTRL_BREAK_EVENT] {
            for (args, expected) in [
                (vec!["yaml", "normalize", "--input", "-"], 130),
                (vec!["base64", "encode"], 130),
            ] {
                let dir = tempfile::tempdir().unwrap();
                let output = dir.path().join("output");
                fs::write(&output, b"original").unwrap();
                let mut command = if via_npm {
                    let mut node = Command::new("node");
                    node.args([
                        "-e",
                        r#"const {launch} = require(process.env.CLIBOX_TEST_LAUNCHER);
launch(process.env.CLIBOX_TEST_BINARY, process.argv.slice(1)).then(({code, signal}) => {
  process.exitCode = signal ? 1 : (code ?? 1);
}).catch(() => { process.exitCode = 99; });
process.stderr.write('launcher_ready\n');"#,
                        "--",
                    ]);
                    node
                } else {
                    Command::new(env!("CARGO_BIN_EXE_clibox"))
                };
                let is_transform = args[0] == "base64";
                let mut child = command
                    .args(args)
                    .arg("--output")
                    .arg(&output)
                    .arg("--force")
                    .env("CLIBOX_TEST_LAUNCHER", &launcher)
                    .env("CLIBOX_TEST_BINARY", env!("CARGO_BIN_EXE_clibox"))
                    .env("RUST_LOG", "clibox=debug")
                    .env("NO_COLOR", "1")
                    .stdin(Stdio::piped())
                    .stdout(Stdio::piped())
                    .stderr(Stdio::piped())
                    .spawn()
                    .unwrap();
                let mut input = child.stdin.take().unwrap();
                input.write_all(b"waiting for EOF").unwrap();
                let stderr = child.stderr.take().unwrap();
                let (tx, rx) = mpsc::channel();
                let reader = thread::spawn(move || {
                    let mut messages = String::new();
                    for line in BufReader::new(stderr).lines() {
                        let line = line.unwrap();
                        messages.push_str(&line);
                        messages.push('\n');
                        let _ = tx.send(line);
                    }
                    messages
                });
                let deadline = Instant::now() + Duration::from_secs(15);
                let (mut launcher_ready, mut native_ready) = (!via_npm, false);
                loop {
                    if let Ok(line) = rx.recv_timeout(Duration::from_millis(20)) {
                        launcher_ready |= line.contains("launcher_ready");
                        native_ready |= !is_transform && line.contains("operation_started");
                    }
                    if is_transform {
                        native_ready = fs::read_dir(dir.path()).unwrap().any(|entry| {
                            entry
                                .unwrap()
                                .file_name()
                                .to_string_lossy()
                                .starts_with(".clibox-")
                        });
                    }
                    if launcher_ready && native_ready {
                        break;
                    }
                    if child.try_wait().unwrap().is_some() || Instant::now() >= deadline {
                        let _ = child.kill();
                        drop(input);
                        child.wait().unwrap();
                        panic!("launcher did not become ready: {}", reader.join().unwrap());
                    }
                }
                // This console belongs only to the disposable helper and its Node
                // and native children, so no event can reach the CI runner.
                assert_ne!(unsafe { GenerateConsoleCtrlEvent(event, 0) }, 0);
                while child.try_wait().unwrap().is_none() {
                    if Instant::now() >= deadline {
                        child.kill().unwrap();
                        drop(input);
                        child.wait().unwrap();
                        panic!("launcher cancellation blocked: {}", reader.join().unwrap());
                    }
                    thread::sleep(Duration::from_millis(20));
                }
                drop(input);
                let result = child.wait_with_output().unwrap();
                let diagnostics = reader.join().unwrap();
                assert_eq!(result.status.code(), Some(expected), "{diagnostics}");
                assert!(result.stdout.is_empty());
                assert_eq!(fs::read(&output).unwrap(), b"original");
                assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
            }
        }
    }
}
