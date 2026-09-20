use std::{
    fs,
    io::Write,
    path::Path,
    process::{Command, Output, Stdio},
    thread,
};

use base64::{engine::general_purpose::STANDARD, Engine};
use tempfile::tempdir;

fn run(args: &[&str], input: &[u8]) -> Output {
    run_in(args, input, None)
}

fn run_in(args: &[&str], input: &[u8], cwd: Option<&Path>) -> Output {
    let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
    command
        .args(args)
        .env("NO_COLOR", "1")
        .env_remove("RUST_LOG")
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    if let Some(cwd) = cwd {
        command.current_dir(cwd);
    }
    let mut child = command.spawn().unwrap();
    let mut stdin = child.stdin.take().unwrap();
    let input = input.to_vec();
    let writer = thread::spawn(move || {
        let _ = stdin.write_all(&input);
    });
    let output = child.wait_with_output().unwrap();
    writer.join().unwrap();
    output
}

fn success(args: &[&str], input: &[u8], expected: &[u8]) {
    let output = run(args, input);
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert_eq!(output.stdout, expected);
    assert!(
        output.stderr.is_empty(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}

#[test]
fn parser_rejects_conflicts_and_malformed_values() {
    for args in [
        vec!["text", "replace", "x"],
        vec!["text", "replace", "x", "y", "--input", "-", "--text", "x"],
        vec!["text", "replace", "x", "y", "--in-place"],
        vec!["text", "replace", "x", "y", "--in-place", "--input", "-"],
        vec![
            "text",
            "replace",
            "x",
            "y",
            "--in-place",
            "--input",
            "a",
            "--output",
            "b",
        ],
        vec!["time", "format", "--from", "unix-s", "--input-format", "%s"],
        vec!["time", "format", "--to", "unix-s", "--format", "%s"],
        vec!["time", "add"],
        vec!["time", "add", "--years", "1.5"],
        vec!["hash", "encode", "--algorithm", "md5"],
        vec!["hash", "encode", "--format", "checksum", "--text", "x"],
        vec!["hash", "verify"],
        vec!["hash", "verify", "--check", "-", "--text", "x"],
        vec!["hash", "verify", "--check", "-", "--format", "hex"],
        vec!["hash", "verify", "--check", "-", "--quiet", "--json"],
        vec!["hash", "verify", "--check", "-", "--quiet", "--output", "a"],
    ] {
        let output = run(&args, b"");
        assert_eq!(
            output.status.code(),
            Some(2),
            "{args:?}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(output.stdout.is_empty());
    }
}

#[test]
fn literal_text_preserves_unicode_and_line_endings() {
    success(
        &["text", "replace", ".", "$1", "--text", "hé.\r\n.\n"],
        b"ignored",
        "hé$1\r\n$1\n".as_bytes(),
    );
    success(
        &["text", "replace", "a", "", "--first"],
        b"aaa\r\n",
        b"aa\r\n",
    );
    success(
        &["text", "replace", "missing", "value"],
        b"unchanged",
        b"unchanged",
    );
    success(
        &["text", "replace", "x", "y", "--text", ""],
        b"ignored",
        b"",
    );
    assert_eq!(
        run(&["text", "replace", "z", "", "--require-match"], b"x")
            .status
            .code(),
        Some(1)
    );
    assert_eq!(
        run(&["text", "replace", "", "x"], b"").status.code(),
        Some(2)
    );
    assert_eq!(
        run(&["text", "replace", "x", "y"], b"\xff").status.code(),
        Some(1)
    );
}

#[test]
fn regex_replacements_follow_capture_and_zero_width_semantics() {
    success(
        &[
            "text",
            "replace",
            "(?<word>x)",
            concat!("$", "{word}"),
            "--regex",
        ],
        b"x",
        b"x",
    );
    success(
        &["text", "replace", "(?m)^", ">", "--regex"],
        b"a\nb",
        b">a\n>b",
    );
    success(
        &[
            "text",
            "replace",
            "(?<word>é)(x)?",
            "$word:$2:$$",
            "--regex",
        ],
        "é éx".as_bytes(),
        "é::$ é:x:$".as_bytes(),
    );
    success(
        &["text", "replace", "(?m)^", ">", "--regex", "--first"],
        b"a\nb\n",
        b">a\nb\n",
    );
    success(&["text", "replace", "(?i)a", "b", "--regex"], b"aA", b"bb");
    success(&["text", "replace", "(x)", "$0$1", "--regex"], b"x", b"xx");
    success(&["text", "replace", "x", "$!", "--regex"], b"x", b"$!");
    for replacement in ["$2", "$missing", "$99999999999999999999999"] {
        assert_eq!(
            run(
                &["text", "replace", "(x)", replacement, "--regex"],
                b"no match"
            )
            .status
            .code(),
            Some(2)
        );
    }
    for pattern in ["(", "(?=x)", r"(x)\1"] {
        assert_eq!(
            run(&["text", "replace", pattern, "", "--regex"], b"x")
                .status
                .code(),
            Some(2)
        );
    }
}

#[test]
fn base64_known_vectors_and_strict_modes() {
    for (plain, encoded) in [
        ("", ""),
        ("f", "Zg=="),
        ("fo", "Zm8="),
        ("foo", "Zm9v"),
        ("foobar", "Zm9vYmFy"),
    ] {
        success(
            &["base64", "encode", "--text", plain],
            b"ignored",
            encoded.as_bytes(),
        );
        success(
            &["base64", "decode", "--text", encoded],
            b"ignored",
            plain.as_bytes(),
        );
        success(
            &["base64", "encode", "--no-padding", "--text", plain],
            b"",
            encoded.trim_end_matches('=').as_bytes(),
        );
        success(
            &[
                "base64",
                "decode",
                "--no-padding",
                "--text",
                encoded.trim_end_matches('='),
            ],
            b"",
            plain.as_bytes(),
        );
    }
    success(&["base64", "encode", "--url-safe"], &[251, 255], b"-_8=");
    success(&["base64", "decode", "--url-safe"], b"-_8=", &[251, 255]);
    success(&["base64", "decode"], b" Z m\t8=\r\n\x0b\x0c", b"fo");
    for encoded in [
        "Zg",
        "Zh==",
        "Zg=",
        "Zg===",
        "Zg==Zg==",
        "A",
        "====",
        "-_8=",
        "Zm9v!",
        "Zg==\u{a0}",
    ] {
        assert_eq!(
            run(&["base64", "decode", "--text", encoded], b"")
                .status
                .code(),
            Some(1),
            "{encoded}"
        );
    }
    for encoded in ["Zg==", "Zh", "A", "Zm9="] {
        assert_eq!(
            run(
                &["base64", "decode", "--no-padding", "--text", encoded],
                b""
            )
            .status
            .code(),
            Some(1)
        );
    }
}

#[test]
fn large_binary_streams_cross_chunk_boundaries() {
    let input: Vec<u8> = (0..4 * 1024 * 1024 + 2)
        .map(|index| (index % 256) as u8)
        .collect();
    let expected = STANDARD.encode(&input);
    success(&["base64", "encode"], &input, expected.as_bytes());
    success(&["base64", "decode"], expected.as_bytes(), &input);
    for size in [65533, 65534, 65535, 65536, 65537, 65538] {
        let expected = STANDARD.encode(&input[..size]);
        success(&["base64", "decode"], expected.as_bytes(), &input[..size]);
    }
    let output = run(&["hash", "encode"], &input);
    use sha2::{Digest, Sha256};
    assert_eq!(
        String::from_utf8(output.stdout).unwrap(),
        format!("{:x}\n", Sha256::digest(&input))
    );
}

#[test]
fn time_formats_epochs_custom_inputs_and_nanoseconds() {
    for (args, expected) in [
        (
            vec!["time", "format", "-1", "--from", "unix-ms"],
            "1969-12-31T23:59:59.999Z\n",
        ),
        (
            vec![
                "time", "format", "-1", "--from", "unix-ms", "--to", "unix-s",
            ],
            "-1\n",
        ),
        (
            vec![
                "time",
                "format",
                "1969-12-31T23:59:59.999999999Z",
                "--to",
                "unix-ms",
            ],
            "-1\n",
        ),
        (
            vec!["time", "format", "2024-02-29T12:34:56.1200+02:00"],
            "2024-02-29T10:34:56.1200Z\n",
        ),
        (
            vec![
                "time",
                "format",
                "2024-02-29",
                "--from",
                "date",
                "--timezone",
                "Asia/Seoul",
            ],
            "2024-02-29T00:00:00+09:00\n",
        ),
        (
            vec![
                "time",
                "format",
                "2024/02/29 12:34:56.12",
                "--input-format",
                "%Y/%m/%d %H:%M:%S%.f",
            ],
            "2024-02-29T12:34:56.12Z\n",
        ),
        (
            vec!["time", "format", "2024-02-29", "--input-format", "%F"],
            "2024-02-29T00:00:00Z\n",
        ),
        (
            vec![
                "time",
                "format",
                "2024-02-29",
                "--from",
                "date",
                "--format",
                "%a %b %d",
            ],
            "Thu Feb 29\n",
        ),
        (
            vec!["time", "format", "0001-01-01", "--from", "date"],
            "0001-01-01T00:00:00Z\n",
        ),
        (
            vec!["time", "format", "9999-12-31", "--from", "date"],
            "9999-12-31T00:00:00Z\n",
        ),
    ] {
        success(&args, b"", expected.as_bytes());
    }
}

#[test]
fn calendar_arithmetic_clamps_then_applies_days_and_elapsed_time() {
    for (args, expected) in [
        (
            vec!["2024-01-31", "--from", "date", "--months", "1"],
            "2024-02-29T00:00:00Z\n",
        ),
        (
            vec!["2024-02-29", "--from", "date", "--years", "1"],
            "2025-02-28T00:00:00Z\n",
        ),
        (
            vec![
                "2024-03-31",
                "--from",
                "date",
                "--months",
                "-1",
                "--days",
                "1",
            ],
            "2024-03-01T00:00:00Z\n",
        ),
        (
            vec![
                "2024-01-31",
                "--from",
                "date",
                "--years",
                "1",
                "--months",
                "-11",
                "--weeks",
                "1",
                "--days",
                "-6",
                "--hours",
                "25",
            ],
            "2024-03-02T01:00:00Z\n",
        ),
        (
            vec![
                "2024-03-09T12:00:00-05:00",
                "--timezone",
                "America/New_York",
                "--days",
                "1",
            ],
            "2024-03-10T12:00:00-04:00\n",
        ),
        (
            vec![
                "2024-03-09T12:00:00-05:00",
                "--timezone",
                "America/New_York",
                "--hours",
                "24",
            ],
            "2024-03-10T13:00:00-04:00\n",
        ),
        (
            vec![
                "2024-11-03T01:30:00-04:00",
                "--timezone",
                "America/New_York",
                "--days",
                "0",
            ],
            "2024-11-03T01:30:00-04:00\n",
        ),
    ] {
        let mut all = vec!["time", "add"];
        all.extend(args);
        success(&all, b"", expected.as_bytes());
    }
}

#[test]
fn time_rejects_invalid_ambiguous_and_out_of_range_values() {
    assert_eq!(
        run(&["time", "format", "0000-12-31T23:00:00-02:00"], b"")
            .status
            .code(),
        Some(2)
    );
    assert_eq!(
        run(
            &[
                "time",
                "format",
                "0000-12-31 23:00 -0200",
                "--input-format",
                "%F %H:%M %z"
            ],
            b""
        )
        .status
        .code(),
        Some(2)
    );
    for value in [
        "0",
        "2023-02-29T00:00:00Z",
        "2024-01-01T00:00:60Z",
        "0000-01-01T00:00:00Z",
        "2024-01-01T00:00:00.1234567890Z",
    ] {
        assert_eq!(run(&["time", "format", value], b"").status.code(), Some(2));
    }
    for value in ["2024-03-10 02:30", "2024-11-03 01:30"] {
        assert_eq!(
            run(
                &[
                    "time",
                    "format",
                    value,
                    "--input-format",
                    "%F %H:%M",
                    "--timezone",
                    "America/New_York"
                ],
                b""
            )
            .status
            .code(),
            Some(2)
        );
    }
    for format in ["%Q", "%", "%#z"] {
        assert_eq!(
            run(&["time", "format", "--format", format], b"")
                .status
                .code(),
            Some(2)
        );
    }
    assert_eq!(
        run(&["time", "format", "--timezone", "Not/AZone"], b"")
            .status
            .code(),
        Some(2)
    );
    assert_eq!(
        run(
            &[
                "time",
                "format",
                "2024-01-01 UTC",
                "--input-format",
                "%F %Z"
            ],
            b""
        )
        .status
        .code(),
        Some(2)
    );
    for args in [
        vec!["9999-12-31", "--from", "date", "--days", "1"],
        vec!["0001-01-01", "--from", "date", "--seconds", "-1"],
        vec![
            "2024-01-01",
            "--from",
            "date",
            "--years",
            "9223372036854775807",
        ],
        vec![
            "2024-03-09T02:30:00-05:00",
            "--timezone",
            "America/New_York",
            "--days",
            "1",
        ],
        vec![
            "2024-11-02T01:30:00-04:00",
            "--timezone",
            "America/New_York",
            "--days",
            "1",
        ],
    ] {
        let mut all = vec!["time", "add"];
        all.extend(args);
        assert_eq!(run(&all, b"").status.code(), Some(1));
    }
}

#[test]
fn bundled_timezones_ignore_host_environment() {
    let args = [
        "time",
        "format",
        "2024-07-01",
        "--from",
        "date",
        "--timezone",
        "America/New_York",
    ];
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(args)
        .env("TZ", "Asia/Tokyo")
        .env("TZDIR", "/does/not/exist")
        .env("LC_ALL", "fr_FR.UTF-8")
        .output()
        .unwrap();
    assert!(output.status.success());
    assert_eq!(output.stdout, b"2024-07-01T00:00:00-04:00\n");
}

#[test]
fn known_hashes_and_direct_verification_do_not_echo_digests_or_text() {
    for (algorithm, expected) in [
        ("sha256", "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"),
        ("sha512", "ddaf35a193617abacc417349ae20413112e6fa4e89a97ea20a9eeee64b55d39a2192992a274fc1a836ba3c23a3feebbd454d4423643ce80e2a9ac94fa54ca49f"),
        ("blake3", "6437b3ac38465133ffb63b75273a8db548c558465d79db03fd359c6cd5bd9d85"),
    ] {
        success(&["hash", "encode", "--algorithm", algorithm, "--text", "abc"], b"ignored", format!("{expected}\n").as_bytes());
        success(&["hash", "verify", &expected.to_uppercase(), "--algorithm", algorithm, "--text", "abc"], b"", b"text: ok\n");
        let encoded = run(&["hash", "encode", "--algorithm", algorithm, "--format", "base64", "--text", "abc"], b"");
        let encoded = String::from_utf8(encoded.stdout).unwrap();
        success(&["hash", "verify", encoded.trim(), "--algorithm", algorithm, "--format", "base64", "--quiet", "--text", "abc"], b"", b"");
        let output = run(&["hash", "verify", expected, "--algorithm", algorithm, "--text", "different", "--json"], b"");
        assert_eq!(output.status.code(), Some(1));
        let json: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(json["results"][0]["status"], "mismatch");
        assert!(!String::from_utf8(output.stdout).unwrap().contains(expected));
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn manifests_resolve_relative_files_and_report_every_error() {
    let dir = tempdir().unwrap();
    fs::write(dir.path().join("good file"), b"abc").unwrap();
    fs::write(dir.path().join("bad"), b"bad").unwrap();
    let digest = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad";
    let manifest = format!("{digest} *good file\nmalformed\n{digest}  bad\n{digest} *missing\n");
    fs::write(dir.path().join("sums"), &manifest).unwrap();
    let output = run(
        &[
            "hash",
            "verify",
            "--check",
            dir.path().join("sums").to_str().unwrap(),
            "--json",
        ],
        b"ignored",
    );
    assert_eq!(output.status.code(), Some(1));
    let json: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(json["results"][0]["status"], "ok");
    assert_eq!(json["results"][1]["status"], "mismatch");
    assert_eq!(json["results"][2]["code"], "read-failed");
    assert_eq!(json["errors"][0]["line"], 2);
    assert_eq!(json["errors"][0]["code"], "malformed-record");
    let stdin = run_in(
        &["hash", "verify", "--check", "-", "--json"],
        manifest.as_bytes(),
        Some(dir.path()),
    );
    assert_eq!(stdin.stdout, output.stdout);
    assert_eq!(
        run(&["hash", "verify", "--check", "-"], b"").status.code(),
        Some(1)
    );
}

#[test]
fn checksum_records_round_trip_escaped_filenames() {
    let dir = tempdir().unwrap();
    #[cfg(unix)]
    let filename = "file\\with\nnew\rline";
    #[cfg(windows)]
    let filename = "file with spaces";
    fs::write(dir.path().join(filename), b"\0\xff\r\n").unwrap();
    let encoded = run_in(
        &[
            "hash", "encode", "--input", filename, "--format", "checksum",
        ],
        b"",
        Some(dir.path()),
    );
    assert!(encoded.status.success());
    let checked = run_in(
        &["hash", "verify", "--check", "-", "--quiet"],
        &encoded.stdout,
        Some(dir.path()),
    );
    assert_eq!(checked.status.code(), Some(0));
    assert!(checked.stdout.is_empty());
}

#[test]
fn file_outputs_are_atomic_and_completed_failure_reports_are_published() {
    let dir = tempdir().unwrap();
    let path = dir.path().join("destination");
    let path = path.to_str().unwrap();
    fs::write(path, b"original").unwrap();
    assert_eq!(
        run(&["text", "replace", "", "x", "--output", path], b"")
            .status
            .code(),
        Some(2)
    );
    assert_eq!(
        run(&["hash", "verify", "invalid", "--output", path], b"")
            .status
            .code(),
        Some(2)
    );
    assert_eq!(
        run(&["base64", "encode", "--text", "x", "--output", path], b"")
            .status
            .code(),
        Some(1)
    );
    assert_eq!(fs::read(path).unwrap(), b"original");
    assert_eq!(
        run(
            &["base64", "decode", "--text", "invalid!", "--output", path, "--force"],
            b""
        )
        .status
        .code(),
        Some(1)
    );
    assert_eq!(fs::read(path).unwrap(), b"original");
    let output = run(
        &[
            "base64", "encode", "--text", "x", "--output", path, "--force",
        ],
        b"",
    );
    assert_eq!(
        output.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(output.stdout.is_empty());
    assert_eq!(fs::read(path).unwrap(), b"eA==");
    let output = run(
        &[
            "hash",
            "verify",
            &"0".repeat(64),
            "--text",
            "x",
            "--output",
            path,
            "--force",
        ],
        b"",
    );
    assert_eq!(output.status.code(), Some(1));
    assert!(output.stdout.is_empty());
    assert_eq!(fs::read(path).unwrap(), b"text: mismatch\n");
    assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
}

#[test]
fn inplace_preserves_input_on_validation_failure_and_keeps_access_mode() {
    let dir = tempdir().unwrap();
    let path = dir.path().join("input");
    let input = path.to_str().unwrap();
    fs::write(&path, b"\xff x").unwrap();
    assert_eq!(
        run(
            &["text", "replace", "x", "y", "--input", input, "--in-place"],
            b""
        )
        .status
        .code(),
        Some(1)
    );
    assert_eq!(fs::read(&path).unwrap(), b"\xff x");
    fs::write(&path, b"x\r\n").unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&path, fs::Permissions::from_mode(0o640)).unwrap();
    }
    success(
        &["text", "replace", "x", "y", "--input", input, "--in-place"],
        b"",
        b"",
    );
    assert_eq!(fs::read(&path).unwrap(), b"y\r\n");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o640
        );
    }
}

#[test]
fn replacement_rejects_hardlinks_and_nonregular_files() {
    let dir = tempdir().unwrap();
    let first = dir.path().join("first");
    let second = dir.path().join("second");
    fs::write(&first, b"safe").unwrap();
    fs::hard_link(&first, &second).unwrap();
    for path in [&first, &second, dir.path()] {
        let output = run(
            &[
                "base64",
                "encode",
                "--text",
                "x",
                "--output",
                path.to_str().unwrap(),
                "--force",
            ],
            b"",
        );
        assert_eq!(output.status.code(), Some(1));
    }
    assert_eq!(fs::read(&first).unwrap(), b"safe");
    #[cfg(unix)]
    {
        let link = dir.path().join("link");
        std::os::unix::fs::symlink(&first, &link).unwrap();
        assert_eq!(
            run(
                &[
                    "base64",
                    "encode",
                    "--text",
                    "x",
                    "--output",
                    link.to_str().unwrap(),
                    "--force"
                ],
                b""
            )
            .status
            .code(),
            Some(1)
        );
    }
}

#[test]
fn explicit_input_finishes_without_reading_an_open_stdin() {
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["base64", "encode", "--text", "x"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let _keep_stdin_open = child.stdin.take().unwrap();
    for _ in 0..100 {
        if child.try_wait().unwrap().is_some() {
            assert_eq!(child.wait_with_output().unwrap().stdout, b"eA==");
            return;
        }
        thread::sleep(std::time::Duration::from_millis(20));
    }
    child.kill().unwrap();
    child.wait().unwrap();
    panic!("explicit input consumed stdin");
}

#[test]
fn diagnostics_never_contain_sensitive_arguments_or_paths() {
    let secret = "CLIBOX_SECRET_MARKER";
    for args in [
        vec!["--CLIBOX_SECRET_MARKER"],
        vec!["hash", "encode", "--algorithm", secret],
        vec!["hash", "verify", secret, "--text", secret],
        vec![
            "text",
            "replace",
            "(CLIBOX_SECRET_MARKER",
            secret,
            "--regex",
            "--text",
            secret,
        ],
        vec!["text", "replace", "(x)", "$CLIBOX_SECRET_MARKER", "--regex"],
        vec!["base64", "decode", "--text", secret],
        vec!["base64", "encode", "--input", secret],
        vec!["time", "format", secret],
        vec!["time", "format", "--timezone", secret],
        vec!["time", "format", "--format", "%QCLIBOX_SECRET_MARKER"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(args)
            .env("RUST_LOG", "trace")
            .env("NO_COLOR", "1")
            .stdin(Stdio::null())
            .output()
            .unwrap();
        assert!(!output.status.success());
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert!(!stderr.contains(secret), "{stderr}");
        assert!(!stderr.contains("\u{1b}"));
    }
}

#[cfg(unix)]
#[test]
fn cancellation_interrupts_open_stdin_and_removes_unpublished_output() {
    use std::time::{Duration, Instant};
    let dir = tempdir().unwrap();
    let path = dir.path().join("output");
    fs::write(&path, b"original").unwrap();
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args([
            "base64",
            "encode",
            "--output",
            path.to_str().unwrap(),
            "--force",
        ])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdin = child.stdin.take().unwrap();
    stdin.write_all(b"waiting for EOF").unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    while fs::read_dir(dir.path()).unwrap().count() < 2 {
        assert!(
            Instant::now() < deadline,
            "temporary output was not prepared"
        );
        thread::sleep(Duration::from_millis(10));
    }
    assert_eq!(unsafe { libc::kill(child.id() as i32, libc::SIGINT) }, 0);
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            child.kill().unwrap();
            child.wait().unwrap();
            panic!("cancellation waited for stdin EOF");
        }
        thread::sleep(Duration::from_millis(10));
    }
    let output = child.wait_with_output().unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(output.stdout.is_empty());
    assert_eq!(fs::read(&path).unwrap(), b"original");
    assert_eq!(fs::read_dir(dir.path()).unwrap().count(), 1);
    drop(stdin);
}

#[test]
fn streaming_stdout_can_be_partial_before_decode_failure() {
    use std::io::Read;
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["base64", "decode"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let mut stdin = child.stdin.take().unwrap();
    stdin.write_all(&b"Zm9v".repeat(2048)).unwrap();
    let mut prefix = [0; 3];
    child
        .stdout
        .as_mut()
        .unwrap()
        .read_exact(&mut prefix)
        .unwrap();
    assert_eq!(&prefix, b"foo");
    stdin.write_all(b"!").unwrap();
    drop(stdin);
    let output = child.wait_with_output().unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(output.stdout.chunks_exact(3).all(|chunk| chunk == b"foo"));
}

#[test]
fn closed_stdout_is_a_runtime_failure() {
    use std::time::{Duration, Instant};
    let dir = tempdir().unwrap();
    let path = dir.path().join("large");
    fs::write(&path, vec![42; 1024 * 1024]).unwrap();
    let mut child = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .args(["base64", "encode", "--input", path.to_str().unwrap()])
        .stdout(Stdio::piped())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    drop(child.stdout.take());
    let deadline = Instant::now() + Duration::from_secs(5);
    while child.try_wait().unwrap().is_none() {
        if Instant::now() >= deadline {
            child.kill().unwrap();
            child.wait().unwrap();
            panic!("broken stdout did not terminate the operation");
        }
        thread::sleep(Duration::from_millis(10));
    }
    assert_eq!(child.wait_with_output().unwrap().status.code(), Some(1));
}

#[cfg(target_os = "macos")]
#[test]
fn macos_extended_acl_survives_replacement() {
    use std::{ffi::CStr, os::fd::AsRawFd};
    unsafe extern "C" {
        fn acl_get_fd_np(fd: libc::c_int, kind: libc::c_int) -> *mut libc::c_void;
        fn acl_to_text(acl: *mut libc::c_void, length: *mut libc::ssize_t) -> *mut libc::c_char;
        fn acl_free(acl: *mut libc::c_void) -> libc::c_int;
    }
    fn acl(path: &Path) -> Vec<u8> {
        let file = fs::File::open(path).unwrap();
        unsafe {
            let acl = acl_get_fd_np(file.as_raw_fd(), 0x100);
            assert!(!acl.is_null());
            let text = acl_to_text(acl, std::ptr::null_mut());
            assert!(!text.is_null());
            let bytes = CStr::from_ptr(text).to_bytes().to_vec();
            acl_free(text.cast());
            acl_free(acl);
            bytes
        }
    }
    let dir = tempdir().unwrap();
    let path = dir.path().join("acl");
    fs::write(&path, b"before").unwrap();
    // chmod is only a fixture setup tool, never a clibox runtime dependency.
    assert!(Command::new("/bin/chmod")
        .args(["+a", "everyone allow read"])
        .arg(&path)
        .status()
        .unwrap()
        .success());
    let before = acl(&path);
    success(
        &[
            "text",
            "replace",
            "before",
            "after",
            "--input",
            path.to_str().unwrap(),
            "--in-place",
        ],
        b"",
        b"",
    );
    assert_eq!(acl(&path), before);
    assert_eq!(fs::read(&path).unwrap(), b"after");
}
