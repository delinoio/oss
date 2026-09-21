use std::{
    fs,
    io::Write,
    path::Path,
    process::{Command, Output, Stdio},
    thread,
    time::{Duration, Instant},
};

const CLI: &str = env!("CARGO_BIN_EXE_clibox");
const ABC_SHA256: &str = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";

fn run(cwd: &Path, args: &[&str], input: &[u8]) -> Output {
    let mut child = Command::new(CLI)
        .args(args)
        .current_dir(cwd)
        .env("RUST_LOG", "off")
        .env("NO_COLOR", "1")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdin = child.stdin.take().unwrap();
    let input = input.to_vec();
    let writer = thread::spawn(move || {
        let _ = stdin.write_all(&input);
    });
    let output = child.wait_with_output().unwrap();
    writer.join().unwrap();
    output
}

#[test]
fn stdout_selectors_and_literal_dash_files_agree_for_every_file_output_command() {
    let dir = tempfile::tempdir().unwrap();
    let cases: &[(&[&str], &[u8])] = &[
        (&["dotenv", "list", "--input", "-"], b"A=1"),
        (&["dotenv", "merge", "-"], b"A=1"),
        (&["yaml", "normalize"], b"a: 1"),
        (&["text", "replace", "a", "z", "--text", "abc"], b""),
        (&["base64", "encode", "--text", "abc"], b""),
        (&["base64", "decode", "--text", "YWJj"], b""),
        (&["hash", "compute", "--text", "abc"], b""),
        (&["hash", "verify", ABC_SHA256, "--text", "abc"], b""),
    ];
    for (args, input) in cases {
        let implicit = run(dir.path(), args, input);
        assert!(implicit.status.success(), "{args:?}");
        let mut explicit_args = args.to_vec();
        explicit_args.extend(["--output", "-"]);
        let explicit = run(dir.path(), &explicit_args, input);
        assert!(explicit.status.success(), "{args:?}");
        assert_eq!(explicit.stdout, implicit.stdout, "{args:?}");
        assert!(explicit.stderr.is_empty());
        assert!(!dir.path().join("-").exists());
        *explicit_args.last_mut().unwrap() = "./-";
        let file = run(dir.path(), &explicit_args, input);
        assert!(file.status.success(), "{args:?}");
        assert!(file.stdout.is_empty());
        assert_eq!(fs::read(dir.path().join("-")).unwrap(), implicit.stdout);
        fs::remove_file(dir.path().join("-")).unwrap();
    }
}

#[test]
fn force_without_a_file_fails_before_reading_open_stdin() {
    let dir = tempfile::tempdir().unwrap();
    for args in [
        vec!["dotenv", "list"],
        vec!["dotenv", "merge", "-"],
        vec!["yaml", "normalize"],
        vec!["text", "replace", "a", "b"],
        vec!["base64", "encode"],
        vec!["base64", "decode"],
        vec!["hash", "compute"],
        vec!["hash", "verify", ABC_SHA256],
    ] {
        for output in [vec![], vec!["--output", "-"]] {
            let mut child = Command::new(CLI)
                .args(&args)
                .args(output)
                .arg("--force")
                .current_dir(dir.path())
                .env("RUST_LOG", "off")
                .stdin(Stdio::piped())
                .stdout(Stdio::piped())
                .stderr(Stdio::piped())
                .spawn()
                .unwrap();
            let stdin = child.stdin.take().unwrap();
            let deadline = Instant::now() + Duration::from_secs(5);
            while child.try_wait().unwrap().is_none() {
                if Instant::now() >= deadline {
                    child.kill().unwrap();
                    child.wait().unwrap();
                    panic!("invalid force waited for stdin: {args:?}");
                }
                thread::sleep(Duration::from_millis(10));
            }
            let result = child.wait_with_output().unwrap();
            drop(stdin);
            assert_eq!(result.status.code(), Some(2), "{args:?}");
            assert!(result.stdout.is_empty());
            assert!(String::from_utf8_lossy(&result.stderr).contains("error:"));
            assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 0);
        }
    }
}

#[test]
fn in_place_force_is_redundant_and_checksum_stdout_keeps_supplied_paths() {
    let dir = tempfile::tempdir().unwrap();
    for (args, input, expected) in [
        (vec!["text", "replace", "a", "b"], "a", "b"),
        (vec!["yaml", "normalize"], "a: 1", "\"a\": 1\n"),
    ] {
        fs::write(dir.path().join("input"), input).unwrap();
        let mut args = args;
        args.extend(["--input", "input", "--in-place", "--force"]);
        let result = run(dir.path(), &args, b"");
        assert!(result.status.success());
        assert!(result.stdout.is_empty());
        assert_eq!(
            fs::read_to_string(dir.path().join("input")).unwrap(),
            expected
        );
    }
    fs::create_dir(dir.path().join("sub")).unwrap();
    fs::write(dir.path().join("sub/input"), "abc").unwrap();
    let args = [
        "hash",
        "compute",
        "--input",
        "./sub/input",
        "--format",
        "checksum",
        "--output",
        "-",
    ];
    let result = run(dir.path(), &args, b"");
    assert!(result.status.success());
    assert_eq!(
        result.stdout,
        format!("{ABC_SHA256} *./sub/input\n").as_bytes()
    );
    assert!(!dir.path().join("-").exists());
}

#[test]
fn removed_names_have_static_migration_guidance_and_parser_context_is_redacted() {
    let dir = tempfile::tempdir().unwrap();
    for (old, new) in [
        (["run", "env"], "clibox env run --help"),
        (["port", "which"], "clibox port list --help"),
        (["hash", "encode"], "clibox hash compute --help"),
    ] {
        let result = run(dir.path(), &[old[0], old[1], "PRIVATE-MARKER"], b"");
        assert_eq!(result.status.code(), Some(2));
        assert!(result.stdout.is_empty());
        let message = String::from_utf8(result.stderr).unwrap();
        assert!(message.contains(new));
        assert!(!message.contains("PRIVATE-MARKER"));
    }
    for args in [
        vec!["hash", "compute", "--algorithm", "PRIVATE-MARKER"],
        vec!["base64", "decode", "--PRIVATE-MARKER"],
        vec!["text", "replace", "PRIVATE-MARKER"],
    ] {
        let result = run(dir.path(), &args, b"");
        assert_eq!(result.status.code(), Some(2));
        let message = String::from_utf8(result.stderr).unwrap();
        assert!(message.contains(&format!("clibox {} {} --help", args[0], args[1])));
        assert!(!message.contains("PRIVATE-MARKER"));
        assert!(!message.contains("Wait"));
    }
}

#[test]
fn output_mode_conflicts_and_filtered_runtime_failures_remain_visible() {
    let dir = tempfile::tempdir().unwrap();
    for args in [
        vec!["port", "list", "80", "--pids", "--json"],
        vec!["port", "list", "80", "--pids", "--quiet"],
        vec!["port", "kill", "80", "--quiet", "--json"],
        vec!["port", "kill", "80", "--pids"],
        vec!["hash", "verify", ABC_SHA256, "--quiet", "--output", "-"],
    ] {
        let result = run(dir.path(), &args, b"");
        assert_eq!(result.status.code(), Some(2), "{args:?}");
        assert!(result.stdout.is_empty());
    }
    for args in [
        vec!["env", "run", "--", "./PRIVATE-MARKER-MISSING"],
        vec!["base64", "decode", "--text", "PRIVATE-MARKER!"],
    ] {
        let result = run(dir.path(), &args, b"");
        assert_eq!(result.status.code(), Some(1));
        let message = String::from_utf8(result.stderr).unwrap();
        assert!(message.contains("error:"));
        assert!(message.contains("operation="));
        assert!(!message.contains("PRIVATE-MARKER"));
    }
    let failed = run(
        dir.path(),
        &[
            "hash",
            "verify",
            ABC_SHA256,
            "--text",
            "PRIVATE-MARKER",
            "--quiet",
        ],
        b"",
    );
    assert_eq!(failed.status.code(), Some(1));
    assert!(failed.stdout.is_empty());
    let message = String::from_utf8(failed.stderr).unwrap();
    assert!(message.contains("Checksum verification failed"));
    assert!(!message.contains("PRIVATE-MARKER"));
    assert!(!message.contains(ABC_SHA256));
}

#[cfg(unix)]
#[test]
fn native_and_npm_cancellation_preserve_files_and_return_numeric_signal_codes() {
    use std::{
        io::{BufRead, BufReader},
        sync::mpsc,
    };
    let launcher =
        Path::new(env!("CARGO_MANIFEST_DIR")).join("../../packages/clibox/src/launcher.cjs");
    for via_npm in [false, true] {
        if via_npm && !launcher.is_file() {
            continue;
        }
        for (signal, expected) in [(libc::SIGINT, 130), (libc::SIGTERM, 143), (libc::SIGHUP, 1)] {
            for (args, file_output) in [
                (vec!["base64", "encode"], true),
                (vec!["yaml", "normalize"], true),
                (vec!["clipboard", "copy"], false),
                (vec!["wait", "file", "missing", "--quiet"], false),
            ] {
                if signal == libc::SIGHUP && args[0] != "base64" {
                    continue;
                }
                let dir = tempfile::tempdir().unwrap();
                let destination = dir.path().join("output");
                fs::write(&destination, b"original").unwrap();
                let mut command = if via_npm {
                    let mut node = Command::new("node");
                    node.args([
                        "-e",
                        r#"
const {launch} = require(process.env.CLIBOX_TEST_LAUNCHER);
launch(process.env.CLIBOX_TEST_BINARY, process.argv.slice(1)).then(({code, signal}) => {
  if (signal) process.kill(process.pid, signal);
  else process.exitCode = code ?? 1;
}).catch(() => { process.exitCode = 99; });
"#,
                        "--",
                    ])
                    .env("CLIBOX_TEST_LAUNCHER", &launcher)
                    .env("CLIBOX_TEST_BINARY", CLI);
                    node
                } else {
                    Command::new(CLI)
                };
                command.args(&args);
                if file_output {
                    command.arg("--output").arg(&destination).arg("--force");
                }
                let mut child = command
                    .current_dir(dir.path())
                    .stdin(Stdio::piped())
                    .stdout(Stdio::piped())
                    .stderr(Stdio::piped())
                    .env("RUST_LOG", "clibox=debug")
                    .env("NO_COLOR", "1")
                    .spawn()
                    .unwrap();
                let mut stdin = child.stdin.take().unwrap();
                stdin.write_all(b"waiting for EOF").unwrap();
                let stderr = child.stderr.take().unwrap();
                let (tx, rx) = mpsc::channel();
                let reader = thread::spawn(move || {
                    let mut messages = String::new();
                    for line in BufReader::new(stderr).lines() {
                        let line = line.unwrap();
                        messages.push_str(&line);
                        messages.push('\n');
                        if line.contains("operation_started") || line.contains("wait_attempt") {
                            let _ = tx.send(());
                        }
                    }
                    messages
                });
                let deadline = Instant::now() + Duration::from_secs(10);
                if rx.recv_timeout(Duration::from_secs(5)).is_err() {
                    let _ = child.kill();
                    let _ = child.wait();
                    panic!("operation not ready: {}", reader.join().unwrap());
                }
                // Streaming transforms stage before consuming the blocked input;
                // cancel after staging to exercise cleanup for both Unix signals.
                while args[0] == "base64" && fs::read_dir(dir.path()).unwrap().count() == 1 {
                    if child.try_wait().unwrap().is_some() || Instant::now() >= deadline {
                        let _ = child.kill();
                        let _ = child.wait();
                        panic!("staging not ready: {}", reader.join().unwrap());
                    }
                    thread::sleep(Duration::from_millis(10));
                }
                assert_eq!(unsafe { libc::kill(child.id() as i32, signal) }, 0);
                while child.try_wait().unwrap().is_none() {
                    if Instant::now() >= deadline {
                        child.kill().unwrap();
                        child.wait().unwrap();
                        panic!("cancellation waited for stdin");
                    }
                    thread::sleep(Duration::from_millis(10));
                }
                let result = child.wait_with_output().unwrap();
                drop(stdin);
                let messages = reader.join().unwrap();
                assert_eq!(
                    result.status.code(),
                    Some(expected),
                    "npm={via_npm} {args:?}: {messages}"
                );
                assert!(result.stdout.is_empty());
                assert!(messages.contains("error:"));
                assert_eq!(fs::read(&destination).unwrap(), b"original");
                assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
            }
        }
    }
}
