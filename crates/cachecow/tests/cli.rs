// SPDX-License-Identifier: Apache-2.0
//! These tests execute the actual `ccow` binary and native process ownership
//! on the current host. Cargo and rustc are deterministic fixtures, not
//! tool-floor acceptance.
#![cfg(unix)]

use std::{
    fs,
    io::{Read, Write},
    os::unix::fs::PermissionsExt,
    path::Path,
    process::{Child, Command, Output, Stdio},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

const BIN: &str = env!("CARGO_BIN_EXE_ccow");

fn write_file(path: &str, text: &str, mode: u32) {
    fs::create_dir_all(Path::new(path).parent().unwrap()).unwrap();
    fs::write(path, text).unwrap();
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).unwrap();
}

fn join(a: &str, b: &str) -> String {
    format!("{a}/{b}")
}

fn git(cwd: &str, args: &[&str]) {
    let out = Command::new("git")
        .arg("-C")
        .arg(cwd)
        .args(args)
        .output()
        .unwrap();
    assert!(
        out.status.success(),
        "git {args:?}: {}",
        String::from_utf8_lossy(&out.stderr)
    );
}

fn exists(path: &str) -> bool {
    fs::symlink_metadata(path).is_ok()
}

struct Fixture {
    temp: tempfile::TempDir,
    dir: String,
    bin: String,
    main: String,
    work: String,
    home: String,
    env: Vec<(String, String)>,
}

impl Drop for Fixture {
    fn drop(&mut self) {
        // Tests may leave read-only directories; restore owner access within
        // the fixture only so its own temporary directory can be
        // removed.
        restore_writable(self.temp.path().to_str().unwrap());
    }
}

fn restore_writable(path: &str) {
    let Ok(meta) = fs::symlink_metadata(path) else {
        return;
    };
    if meta.is_dir() {
        let _ = fs::set_permissions(path, fs::Permissions::from_mode(0o700));
        if let Ok(entries) = fs::read_dir(path) {
            for entry in entries.flatten() {
                restore_writable(entry.path().to_str().unwrap());
            }
        }
    }
}

const CARGO: &str = r#"#!/bin/sh
case "$1" in
 --version) echo 'cargo 1.99.1 (fixture)' ;;
 metadata) printf '{"version":1,"workspace_root":"%s","target_directory":"%s/target","build_directory":"%s/target"}\n' "$PWD" "$PWD" "$PWD" ;;
 clean) rm -rf target; printf 'clean:%s\n' "$*" ;;
 *) mkdir -p target; if [ -f scenario ]; then exec /bin/sh "$PWD/scenario" "$@"; fi; printf 'source artifact' > target/data ;;
esac
"#;

impl Fixture {
    fn new() -> Self {
        let temp = tempfile::tempdir().unwrap();
        let dir = fs::canonicalize(temp.path())
            .unwrap()
            .to_str()
            .unwrap()
            .to_string();
        let f = Self {
            bin: join(&dir, "bin"),
            main: join(&dir, "main"),
            work: join(&dir, "work 한 space"),
            home: join(&dir, "home"),
            dir,
            temp,
            env: Vec::new(),
        };
        for p in [&f.main, &f.home, &f.bin] {
            fs::create_dir_all(p).unwrap();
            fs::set_permissions(p, fs::Permissions::from_mode(0o700)).unwrap();
        }
        write_file(&join(&f.bin, "cargo"), CARGO, 0o700);
        write_file(
            &join(&f.bin, "rustc"),
            "#!/bin/sh\nprintf 'rustc 1.99.1 (fixture)\\nhost: aarch64-apple-darwin\\nrelease: \
             1.99.1\\n'\n",
            0o700,
        );
        write_file(
            &join(&f.main, "Cargo.toml"),
            "[package]\nname='fixture'\nversion='0.1.0'\n",
            0o600,
        );
        write_file(&join(&f.main, ".gitignore"), "target/\n", 0o600);
        git(&f.main, &["init"]);
        git(&f.main, &["add", "."]);
        git(
            &f.main,
            &[
                "-c",
                "user.name=Fixture",
                "-c",
                "user.email=fixture@example.invalid",
                "commit",
                "-m",
                "fixture",
            ],
        );
        git(&f.main, &["worktree", "add", "-b", "derived", &f.work]);
        let mut f = f;
        for (key, value) in std::env::vars() {
            // ZDOTDIR must not leak: shell setup tests would otherwise rewrite
            // the real zsh profile.
            match key.as_str() {
                "HOME"
                | "XDG_CONFIG_HOME"
                | "CARGO_HOME"
                | "PATH"
                | "RUSTC"
                | "RUSTC_WRAPPER"
                | "RUSTC_WORKSPACE_WRAPPER"
                | "CCOW_SESSION"
                | "ZDOTDIR"
                | "NO_COLOR" => continue,
                _ => f.env.push((key, value)),
            }
        }
        let path = format!("{}:{}", f.bin, std::env::var("PATH").unwrap_or_default());
        f.set_env("HOME", &f.home.clone());
        f.set_env("XDG_CONFIG_HOME", &join(&f.home, "config"));
        f.set_env("CARGO_HOME", &join(&f.home, "cargo"));
        f.set_env("PATH", &path);
        f.set_env("NO_COLOR", "1");
        f
    }

    fn set_env(&mut self, key: &str, value: &str) {
        self.env.retain(|(k, _)| k != key);
        self.env.push((key.to_string(), value.to_string()));
    }

    fn state_base(&self) -> String {
        let mac = join(&self.home, "Library/Application Support/cachecow");
        if exists(&mac) {
            mac
        } else {
            join(&self.home, "config/cachecow")
        }
    }

    fn command(&self, program: &str, cwd: &str, args: &[&str]) -> Command {
        let mut c = Command::new(program);
        c.args(args)
            .current_dir(cwd)
            .env_clear()
            .envs(self.env.iter().map(|(k, v)| (k, v)));
        c
    }

    fn cmd(&self, cwd: &str, args: &[&str]) -> Command {
        self.command(BIN, cwd, args)
    }

    fn run(&self, cwd: &str, want: i32, args: &[&str]) -> (String, String) {
        let out = output(self.cmd(cwd, args));
        let code = out.status.code().unwrap_or(-1);
        let (stdout, stderr) = (
            String::from_utf8_lossy(&out.stdout).into_owned(),
            String::from_utf8_lossy(&out.stderr).into_owned(),
        );
        assert_eq!(
            code, want,
            "ccow {args:?}\nstdout={stdout}\nstderr={stderr}"
        );
        (stdout, stderr)
    }
}

/// Runs a command with captured output and a 60-second bound.
fn output(mut c: Command) -> Output {
    c.stdin(Stdio::null())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let child = c.spawn().unwrap();
    let pid = child.id();
    let (tx, rx) = mpsc::channel();
    thread::spawn(move || {
        let _ = tx.send(child.wait_with_output());
    });
    match rx.recv_timeout(Duration::from_secs(60)) {
        Ok(out) => out.unwrap(),
        Err(_) => {
            // SAFETY: signalling a child process ID owned by this test.
            unsafe { libc::kill(pid as i32, libc::SIGKILL) };
            panic!("command timed out");
        }
    }
}

fn wait_file(path: &str) {
    let deadline = Instant::now() + Duration::from_secs(20);
    while Instant::now() < deadline {
        if exists(path) {
            return;
        }
        thread::sleep(Duration::from_millis(20));
    }
    panic!("timed out waiting for {path}");
}

/// Kills a background child when dropped.
struct Background(Child);

impl Drop for Background {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

fn spawn_quiet(mut c: Command) -> Background {
    c.stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::piped());
    Background(c.spawn().unwrap())
}

fn read_stderr(child: &mut Child) -> String {
    let mut s = String::new();
    if let Some(mut e) = child.stderr.take() {
        let _ = e.read_to_string(&mut s);
    }
    s
}

#[test]
fn executable_output_exit_and_cwd() {
    let f = Fixture::new();
    let (out, _) = f.run(&f.main, 0, &["--version"]);
    assert_eq!(out, format!("ccow {}\n", env!("CARGO_PKG_VERSION")));
    write_file(
        &join(&f.main, "scenario"),
        "printf 'OUT:%s|%s' \"$PWD\" \"$*\"; printf 'ERR' >&2; exit 37\n",
        0o600,
    );
    let (out, err) = f.run(
        &f.main,
        37,
        &["run", "--", "cargo", "build", "space arg", "--json"],
    );
    assert_eq!(out, format!("OUT:{}|build space arg --json", f.main));
    assert_eq!(err, "ERR");
}

#[test]
fn executable_preparation_replacement_and_failure() {
    let f = Fixture::new();
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    f.run(
        &f.work,
        0,
        &["prepare", "--allow-copy", "--", "cargo", "build"],
    );
    let p = join(&f.work, "target/data");
    write_file(&p, "local", 0o600);
    f.run(&f.work, 0, &["prepare", "--", "cargo", "build"]);
    assert_eq!(
        fs::read_to_string(&p).unwrap(),
        "local",
        "replaced warm cache"
    );
    f.run(
        &f.work,
        0,
        &[
            "prepare",
            "--replace",
            "--allow-copy",
            "--",
            "cargo",
            "build",
        ],
    );
    assert_eq!(fs::read_to_string(&p).unwrap(), "source artifact");
    write_file(&p, "preserve", 0o600);
    std::os::unix::fs::symlink(&f.home, join(&f.main, "target/bad-link")).unwrap();
    f.run(
        &f.work,
        125,
        &[
            "prepare",
            "--replace",
            "--allow-copy",
            "--",
            "cargo",
            "build",
        ],
    );
    assert_eq!(
        fs::read_to_string(&p).unwrap(),
        "preserve",
        "failed clone destroyed cache"
    );
    for entry in fs::read_dir(&f.work).unwrap() {
        let name = entry.unwrap().file_name().into_string().unwrap();
        assert!(!name.contains(".ccow-"), "staging leaked {name}");
    }
}

#[test]
fn executable_noninteractive_missing_and_settings() {
    let f = Fixture::new();
    let (out, _) = f.run(&f.work, 125, &["prepare", "--json"]);
    assert!(
        out.contains("\"schemaVersion\":1") && out.contains("\"success\":false"),
        "{out}"
    );
    f.run(&f.work, 0, &["config", "set", "source", &f.main]);
    let (out, _) = f.run(&f.work, 0, &["config", "get", "source", "--json"]);
    assert!(out.contains(&f.main), "{out}");
    f.run(&f.work, 0, &["config", "unset", "source"]);
    assert!(
        !exists(&join(&f.work, "target")),
        "prepare invented a build"
    );
}

/// Parses a management JSON error envelope and checks the usage-error fields.
fn require_usage_envelope(stdout: &str, what: &str) {
    let v: serde_json::Value = serde_json::from_str(stdout).unwrap_or_else(|e| {
        panic!("{what}: stdout is not JSON ({e}): {stdout:?}");
    });
    assert_eq!(v["schemaVersion"], 1, "{what}: {stdout}");
    assert_eq!(v["success"], false, "{what}: {stdout}");
    assert_eq!(v["error"]["code"], 2, "{what}: {stdout}");
    for field in ["cause", "remediation"] {
        assert!(
            v["error"][field].as_str().is_some_and(|t| !t.is_empty()),
            "{what}: empty {field}: {stdout}"
        );
    }
}

#[test]
fn argument_errors_use_json_envelope() {
    let f = Fixture::new();
    for args in [
        &["status", "--json", "--bogus"][..],
        &["status", "--json", "--preset", "bogus"],
        &["status", "--bogus", "--json"],
        &["status", "--json", "--preset"],
        &["doctor", "--json", "--source"],
        &["status", "--json=yes"],
    ] {
        let (out, _) = f.run(&f.work, 2, args);
        require_usage_envelope(&out, &format!("{args:?}"));
    }
}

#[test]
fn argument_errors_stay_text_without_json_option() {
    let f = Fixture::new();
    for args in [
        &["status", "--bogus"][..],
        &["run", "--preset", "bogus", "--", "cargo", "--json"],
        // The value of --source is consumed by the option, not read as a flag.
        &["status", "--source", "--json", "--bogus"],
    ] {
        let (out, err) = f.run(&f.work, 2, args);
        assert!(out.is_empty(), "{args:?}: stdout={out:?}");
        assert!(err.contains("error:"), "{args:?}: stderr={err:?}");
    }
}

#[test]
fn non_utf8_argument_honors_json_option() {
    use std::os::unix::ffi::OsStrExt;
    let f = Fixture::new();
    let bad = std::ffi::OsStr::from_bytes(b"bad\xff");
    let out = output({
        let mut c = f.cmd(&f.work, &["status", "--json"]);
        c.arg(bad);
        c
    });
    assert_eq!(out.status.code(), Some(2));
    require_usage_envelope(
        &String::from_utf8_lossy(&out.stdout),
        "non-UTF-8 with --json",
    );
    let out = output({
        let mut c = f.cmd(&f.work, &["status"]);
        c.arg(bad);
        c
    });
    assert_eq!(out.status.code(), Some(2));
    assert!(out.stdout.is_empty());
    assert!(String::from_utf8_lossy(&out.stderr).contains("error:"));
}

#[test]
fn executable_contention_and_parent_death() {
    let f = Fixture::new();
    write_file(
        &join(&f.main, "scenario"),
        "trap '' TERM\nprintf '%s' $$ > target/pid\ntouch target/ready\nwhile :; do sleep 1; \
         done\n",
        0o600,
    );
    let mut owner = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    wait_file(&join(&f.main, "target/ready"));
    f.run(&f.main, 125, &["clean", "--preset", "cargo"]);
    let start = Instant::now();
    owner.0.kill().unwrap();
    let _ = owner.0.wait();
    // A second writer must wait through the first supervisor's five-second
    // drain.
    write_file(&join(&f.main, "scenario"), "printf 'after'\n", 0o600);
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    assert!(
        start.elapsed() >= Duration::from_secs(4),
        "released lease before descendant shutdown"
    );
    let pid: i32 = fs::read_to_string(join(&f.main, "target/pid"))
        .unwrap()
        .trim()
        .parse()
        .unwrap();
    // SAFETY: signal 0 only checks for existence.
    assert!(
        pid <= 1 || unsafe { libc::kill(pid, 0) } != 0,
        "writer survived parent death"
    );
}

#[test]
fn executable_nested_same_and_different_cache() {
    let f = Fixture::new();
    // A project in the same primary checkout is an independently managed cache.
    let sub = join(&f.main, "member");
    write_file(
        &join(&sub, "Cargo.toml"),
        "[package]\nname='member'\nversion='0.1.0'\n",
        0o600,
    );
    write_file(&join(&sub, "scenario"), "printf 'different-cache'\n", 0o600);
    write_file(
        &join(&f.main, "scenario"),
        &format!(
            "if [ \"$2\" = inner ]; then printf 'same-cache|'; exit 0; fi\n'{BIN}' run -- cargo \
             build inner || exit $?\ncd ./member || exit 88\nexec '{BIN}' run -- cargo build\n"
        ),
        0o600,
    );
    let (out, _) = f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    assert_eq!(out, "same-cache|different-cache");
}

#[test]
fn executable_forged_session_rejected() {
    let mut f = Fixture::new();
    f.set_env(
        "CCOW_SESSION",
        &format!("/tmp/not-a-session|{}", "a".repeat(64)),
    );
    f.run(&f.main, 125, &["run", "--", "cargo", "build"]);
}

fn install_shims(f: &mut Fixture) -> String {
    f.run(&f.main, 0, &["shim", "install"]);
    let shims = join(&f.state_base(), "shims");
    let path = format!(
        "{shims}:{}:{}",
        f.bin,
        std::env::var("PATH").unwrap_or_default()
    );
    f.set_env("PATH", &path);
    shims
}

#[test]
fn executable_passthrough_and_cargo_clean() {
    let mut f = Fixture::new();
    let shims = install_shims(&mut f);
    let cargo = join(&shims, "cargo");
    let out = output(f.command(&cargo, &f.work, &["--version"]));
    assert!(out.status.success());
    assert_eq!(
        String::from_utf8_lossy(&out.stdout),
        "cargo 1.99.1 (fixture)\n"
    );
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    let out = output(f.command(&cargo, &f.main, &["clean", "--release"]));
    assert!(
        out.status.success(),
        "{}",
        String::from_utf8_lossy(&out.stderr)
    );
    assert_eq!(
        String::from_utf8_lossy(&out.stdout),
        "clean:clean --release\n"
    );
    assert!(
        !exists(&join(&f.main, "target")),
        "cargo clean did not retain its semantics"
    );
}

/// A pseudo-terminal whose secondary side is used as the child's stdin.
struct Terminal {
    primary: fs::File,
    secondary: fs::File,
}

fn terminal() -> Terminal {
    // SAFETY: standard pseudo-terminal allocation; every return value is
    // checked.
    unsafe {
        let fd = libc::posix_openpt(libc::O_RDWR | libc::O_NOCTTY);
        assert!(fd >= 0, "posix_openpt");
        assert_eq!(libc::grantpt(fd), 0, "grantpt");
        assert_eq!(libc::unlockpt(fd), 0, "unlockpt");
        let name = libc::ptsname(fd);
        assert!(!name.is_null(), "ptsname");
        let name = std::ffi::CStr::from_ptr(name).to_str().unwrap().to_string();
        let primary = <fs::File as std::os::fd::FromRawFd>::from_raw_fd(fd);
        let secondary = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(name)
            .unwrap();
        Terminal { primary, secondary }
    }
}

/// Runs an interactive command with `answers` typed into its terminal stdin.
fn interactive(f: &Fixture, cwd: &str, args: &[&str], answers: &str) -> (i32, String, String) {
    let mut term = terminal();
    let mut c = f.cmd(cwd, args);
    c.stdin(term.secondary.try_clone().unwrap())
        .stdout(Stdio::piped())
        .stderr(Stdio::piped());
    let mut child = c.spawn().unwrap();
    term.primary.write_all(answers.as_bytes()).unwrap();
    // Drain terminal echo so the line discipline never blocks.
    let mut echo = term.primary.try_clone().unwrap();
    thread::spawn(move || {
        let mut buf = [0u8; 1024];
        while matches!(echo.read(&mut buf), Ok(n) if n > 0) {}
    });
    let pid = child.id();
    let (tx, rx) = mpsc::channel();
    let stdout = child.stdout.take();
    let captured = std::sync::Arc::new(std::sync::Mutex::new(Vec::new()));
    let stderr_reader = child.stderr.take().map(|mut s| {
        let captured = captured.clone();
        thread::spawn(move || {
            let mut buf = [0u8; 1024];
            while let Ok(n) = s.read(&mut buf) {
                if n == 0 {
                    break;
                }
                captured.lock().unwrap().extend_from_slice(&buf[..n]);
            }
        })
    });
    thread::spawn(move || {
        let mut out = String::new();
        if let Some(mut s) = stdout {
            let _ = s.read_to_string(&mut out);
        }
        if let Some(reader) = stderr_reader {
            let _ = reader.join();
        }
        let _ = tx.send(out);
    });
    let out = rx
        .recv_timeout(Duration::from_secs(60))
        .unwrap_or_else(|_| {
            // SAFETY: signalling a child process ID owned by this test.
            unsafe { libc::kill(pid as i32, libc::SIGKILL) };
            panic!(
                "interactive command timed out; stderr so far:\n{}",
                String::from_utf8_lossy(&captured.lock().unwrap())
            );
        });
    let err = String::from_utf8_lossy(&captured.lock().unwrap()).into_owned();
    let code = child.wait().unwrap().code().unwrap_or(-1);
    drop(term);
    (code, out, err)
}

#[test]
fn interactive_source_choices() {
    for choice in ["current", "source", "select", "cancel", "prepare"] {
        let f = Fixture::new();
        let missing = join(&f.dir, "missing");
        let mut args = vec!["run", "--allow-copy", "--", "cargo", "build"];
        let mut answers = "1\n".to_string();
        let mut want = 0;
        match choice {
            "source" => answers = "3\ny\n".into(),
            "select" => {
                f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
                args = vec![
                    "run",
                    "--source",
                    &missing,
                    "--allow-copy",
                    "--",
                    "cargo",
                    "build",
                ];
                answers = format!("2\n{}\n", f.main);
            }
            "cancel" => {
                answers = "4\n".into();
                want = 125;
            }
            "prepare" => {
                args = vec!["prepare", "--", "cargo", "build"];
                answers = "4\n".into();
                want = 125;
            }
            _ => {}
        }
        let (code, _, stderr) = interactive(&f, &f.work, &args, &answers);
        assert_eq!(code, want, "{choice}: {stderr}");
        if choice == "source" {
            assert!(
                stderr.contains("Exact requested argv") && stderr.contains(&f.main),
                "missing exact source-build confirmation: {stderr}"
            );
        }
        if choice == "prepare" {
            assert!(
                !stderr.contains("3 build source"),
                "prepare offered an invented build"
            );
        }
    }
}

#[test]
fn inherited_capability_cannot_impersonate_scope() {
    let mut f = Fixture::new();
    write_file(
        &join(&f.main, "scenario"),
        "printf '%s' \"$CCOW_SESSION\" > target/capability\ntrap 'exit 0' TERM\nwhile :; do sleep \
         1; done\n",
        0o600,
    );
    let _owner = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    let path = join(&f.main, "target/capability");
    wait_file(&path);
    let deadline = Instant::now() + Duration::from_secs(5);
    let mut capability = String::new();
    while capability.is_empty() && Instant::now() < deadline {
        capability = fs::read_to_string(&path).unwrap();
    }
    f.set_env("CCOW_SESSION", &capability);
    let (_, stderr) = f.run(&f.main, 125, &["run", "--", "cargo", "build"]);
    assert!(stderr.contains("authentication failed"), "{stderr}");
}

#[test]
fn prepare_cannot_certify_unknown_source() {
    let f = Fixture::new();
    write_file(&join(&f.main, "target/data"), "unrecorded", 0o600);
    f.run(&f.main, 0, &["prepare", "--", "cargo", "build"]);
    f.run(
        &f.work,
        125,
        &["prepare", "--allow-copy", "--", "cargo", "build"],
    );
}

#[test]
fn source_read_lease_ends_before_derived_build() {
    let f = Fixture::new();
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    write_file(
        &join(&f.work, "scenario"),
        "touch target/ready\ntrap 'exit 0' TERM\nwhile :; do sleep 1; done\n",
        0o600,
    );
    let mut derived = spawn_quiet(f.cmd(&f.work, &["run", "--allow-copy", "--", "cargo", "build"]));
    wait_file(&join(&f.work, "target/ready"));
    // The source can build after cloning completes, while the derived writer
    // lives.
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    // SAFETY: signalling a child process ID owned by this test.
    unsafe { libc::kill(derived.0.id() as i32, libc::SIGTERM) };
    let status = derived.0.wait().unwrap();
    let stderr = read_stderr(&mut derived.0);
    assert!(status.success(), "derived shutdown: {status:?} {stderr}");
}

#[test]
fn cargo_clean_waits_without_preparing() {
    let mut f = Fixture::new();
    let shims = install_shims(&mut f);
    write_file(
        &join(&f.main, "scenario"),
        "touch target/ready\nsleep 4\n",
        0o600,
    );
    let _build = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    wait_file(&join(&f.main, "target/ready"));
    let start = Instant::now();
    let out = output(f.command(&join(&shims, "cargo"), &f.main, &["clean", "--release"]));
    let text = format!(
        "{}{}",
        String::from_utf8_lossy(&out.stdout),
        String::from_utf8_lossy(&out.stderr)
    );
    assert!(
        out.status.success() && text.contains("clean:clean --release"),
        "{text}"
    );
    assert!(
        start.elapsed() >= Duration::from_secs(2),
        "cargo clean did not wait"
    );
    assert!(
        !exists(&join(&f.main, "target")),
        "clean prepared or retained cache"
    );
}

#[test]
fn explicit_cargo_path_discovery() {
    let f = Fixture::new();
    // PATH Cargo is supported, but the requested executable is not. Querying
    // PATH would certify its target directory and launch this unrelated
    // wrapper.
    write_file(
        &join(&f.main, "custom-cargo"),
        "#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'cargo 1.98.0'; else touch \
         WRONG-TOOL-RAN; fi\n",
        0o700,
    );
    let (_, diagnostic) = f.run(
        &f.main,
        125,
        &["run", "--preset", "cargo", "--", "./custom-cargo", "build"],
    );
    assert!(diagnostic.contains("1.99.1"), "{diagnostic}");
    assert!(
        !exists(&join(&f.main, "WRONG-TOOL-RAN")),
        "launched unverified tool"
    );
}

#[test]
fn nested_cancellation_drains_outer_scope() {
    let f = Fixture::new();
    write_file(
        &join(&f.main, "scenario"),
        &format!(
            r#"if [ "$2" = inner ]; then
  trap 'touch target/graceful; trap "" TERM' TERM
  (trap '' TERM; while :; do sleep 1; done) &
  touch target/inner-ready
  while :; do sleep 1; done
fi
trap '' TERM
'{BIN}' run -- cargo build inner &
echo $! > target/nested-pid
wait
"#
        ),
        0o600,
    );
    let mut outer = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    wait_file(&join(&f.main, "target/inner-ready"));
    // The outer shell records the nested PID concurrently with the inner
    // command's readiness; wait for a complete value.
    let deadline = Instant::now() + Duration::from_secs(10);
    let pid: i32 = loop {
        if let Some(pid) = fs::read_to_string(join(&f.main, "target/nested-pid"))
            .ok()
            .and_then(|s| s.trim().parse().ok())
        {
            break pid;
        }
        assert!(Instant::now() < deadline, "nested PID was not recorded");
        thread::sleep(Duration::from_millis(20));
    };
    let start = Instant::now();
    // SAFETY: signalling the nested ccow started by this fixture.
    assert_eq!(unsafe { libc::kill(pid, libc::SIGTERM) }, 0);
    wait_file(&join(&f.main, "target/graceful"));
    // The child deliberately ignores subsequent TERM signals. Protection must
    // remain active during the graceful interval, before the forced drain.
    f.run(&f.main, 125, &["clean"]);
    let _ = outer.0.wait();
    let elapsed = start.elapsed();
    let diagnostics = read_stderr(&mut outer.0);
    assert!(
        elapsed >= Duration::from_secs(4) && elapsed <= Duration::from_secs(9),
        "nested cancellation took {elapsed:?}: {diagnostics}"
    );
    f.run(&f.main, 0, &["clean"]);
}

#[test]
fn prepare_waits_for_source_provenance() {
    let f = Fixture::new();
    write_file(
        &join(&f.main, "scenario"),
        "touch target/ready\nwhile [ ! -f target/release ]; do sleep 0.05; done\nprintf 'finished \
         source' > target/data\n",
        0o600,
    );
    let mut source = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    wait_file(&join(&f.main, "target/ready"));
    let mut waiter = spawn_quiet(f.cmd(
        &f.work,
        &[
            "prepare",
            "--allow-copy",
            "--preset",
            "cargo",
            "--",
            "cargo",
            "build",
        ],
    ));
    thread::sleep(Duration::from_millis(800));
    assert!(
        waiter.0.try_wait().unwrap().is_none(),
        "source waiter exited before build completion"
    );
    write_file(&join(&f.main, "target/release"), "release", 0o600);
    assert!(source.0.wait().unwrap().success());
    let status = waiter.0.wait().unwrap();
    let stderr = read_stderr(&mut waiter.0);
    assert!(status.success(), "waiter: {status:?} {stderr}");
    assert_eq!(
        fs::read_to_string(join(&f.work, "target/data")).unwrap(),
        "finished source",
        "incomplete source clone"
    );
}

fn operation_journals(f: &Fixture) -> usize {
    let processes = join(&f.state_base(), "processes");
    fs::read_dir(processes)
        .map(|entries| {
            entries
                .flatten()
                .filter(|e| e.path().join("operation.json").exists())
                .count()
        })
        .unwrap_or(0)
}

#[test]
fn waiting_command_rejects_changed_cargo_configuration() {
    let f = Fixture::new();
    let config = join(&f.main, ".cargo/config.toml");
    write_file(&config, "[build]\ntarget-dir='target'\n", 0o600);
    write_file(
        &join(&f.main, "scenario"),
        "touch target/ready\nwhile [ ! -f target/release ]; do sleep 0.05; done\n",
        0o600,
    );
    let mut owner = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    wait_file(&join(&f.main, "target/ready"));
    let mut waiter = spawn_quiet(f.cmd(&f.main, &["run", "--", "cargo", "build"]));
    // A second operation journal proves discovery finished and the supervisor
    // received the plan before the configuration changes.
    let deadline = Instant::now() + Duration::from_secs(10);
    while operation_journals(&f) < 2 {
        assert!(Instant::now() < deadline, "waiter did not finish discovery");
        thread::sleep(Duration::from_millis(20));
    }
    write_file(&config, "[build]\ntarget-dir='other'\n", 0o600);
    write_file(&join(&f.main, "target/release"), "release", 0o600);
    assert!(owner.0.wait().unwrap().success());
    let status = waiter.0.wait().unwrap();
    let stderr = read_stderr(&mut waiter.0);
    assert_eq!(
        status.code(),
        Some(125),
        "waiter did not reject changed configuration: {stderr}"
    );
    assert!(
        stderr.contains("cache-selection configuration changed"),
        "{stderr}"
    );
}

const SHELL_SENTINEL_PROFILE: &str =
    "export PATH=/usr/bin:$PATH\nexport SENTINEL_SECRET=c8-sentinel-value\n";
const SHELL_PROFILE_FRAGMENTS: [&str; 3] = [
    "c8-sentinel-value",
    "SENTINEL_SECRET",
    "export PATH=/usr/bin",
];

fn require_no_profile_leak(what: &str, streams: &[&str]) {
    for s in streams {
        for fragment in SHELL_PROFILE_FRAGMENTS {
            assert!(
                !s.contains(fragment),
                "{what} leaked profile content {fragment:?}:\n{s}"
            );
        }
    }
}

/// Every line of the shell init code appears in `notice` with `prefix`.
fn require_block_lines(notice: &str, prefix: &str, code: &str) {
    for line in code.trim_end_matches('\n').split('\n') {
        let needle = format!("\n{prefix}{line}");
        assert!(
            notice.contains(&format!("{needle}\n")) || notice.ends_with(&needle),
            "notice does not show {prefix:?} line {line:?}:\n{notice}"
        );
    }
}

#[test]
fn shell_setup_remove_show_only_owned_block() {
    for (shell, file, flag) in [
        ("zsh", ".zshrc", false),
        ("bash", ".bashrc", false),
        ("powershell", "profile.ps1", true),
    ] {
        let f = Fixture::new();
        let path = join(&f.home, file);
        write_file(&path, SHELL_SENTINEL_PROFILE, 0o600);
        let args = |action: &'static str| -> Vec<String> {
            let mut a = vec!["shell".to_string(), action.to_string(), shell.to_string()];
            if flag {
                a.push("--file".into());
                a.push(path.clone());
            }
            a.push("--yes".into());
            a
        };
        let run = |want: i32, a: Vec<String>| {
            let refs: Vec<&str> = a.iter().map(String::as_str).collect();
            f.run(&f.main, want, &refs)
        };
        let (code, _) = f.run(&f.main, 0, &["shell", "init", shell]);
        let (out, stderr) = run(0, args("setup"));
        require_no_profile_leak("setup", &[&out, &stderr]);
        assert!(out.contains("\"changed\": true"), "stdout {out:?}");
        for want in [
            format!("Shell configuration target: {path}"),
            "Action: add CacheCow block".into(),
            "+ # >>> CacheCow owned activation >>>".into(),
            "+ # <<< CacheCow owned activation <<<".into(),
        ] {
            assert!(
                stderr.contains(&want),
                "setup notice missing {want:?}:\n{stderr}"
            );
        }
        assert!(
            !stderr.contains("\n- ") && !stderr.contains("separator"),
            "{stderr}"
        );
        require_block_lines(&stderr, "+ ", &code);
        let with_block = fs::read_to_string(&path).unwrap();
        assert!(
            with_block.starts_with(&format!(
                "{SHELL_SENTINEL_PROFILE}# >>> CacheCow owned activation >>>\n"
            )),
            "profile {with_block:?}"
        );

        let (out, stderr) = run(0, args("setup"));
        require_no_profile_leak("repeated setup", &[&out, &stderr]);
        assert!(
            out.contains("\"changed\": false")
                && stderr.contains(&format!("Shell configuration target: {path}"))
                && stderr.contains("No change: CacheCow block already up to date."),
            "repeated setup out={out:?} stderr={stderr:?}"
        );
        assert!(
            !stderr.contains("CacheCow owned activation") && !stderr.contains("_ccow_"),
            "{stderr}"
        );
        assert_eq!(
            fs::read_to_string(&path).unwrap(),
            with_block,
            "no-change setup modified the profile"
        );

        let (out, stderr) = run(0, args("remove"));
        require_no_profile_leak("remove", &[&out, &stderr]);
        for want in [
            format!("Shell configuration target: {path}"),
            "Action: remove CacheCow block".into(),
            "- # >>> CacheCow owned activation >>>".into(),
            "- # <<< CacheCow owned activation <<<".into(),
        ] {
            assert!(
                stderr.contains(&want),
                "remove notice missing {want:?}:\n{stderr}"
            );
        }
        require_block_lines(&stderr, "- ", &code);
        assert!(
            !stderr.contains("\n+ ") && out.contains("\"changed\": true"),
            "remove out={out:?} stderr={stderr:?}"
        );
        assert_eq!(
            fs::read_to_string(&path).unwrap(),
            SHELL_SENTINEL_PROFILE,
            "round trip changed profile"
        );

        let (out, stderr) = run(0, args("remove"));
        require_no_profile_leak("repeated remove", &[&out, &stderr]);
        assert!(
            out.contains("\"changed\": false")
                && stderr.contains(&format!("Shell configuration target: {path}"))
                && stderr.contains("No change: no CacheCow block to remove."),
            "repeated remove out={out:?} stderr={stderr:?}"
        );
    }
}

#[test]
fn shell_setup_without_confirmation_shows_only_owned_block() {
    let f = Fixture::new();
    let path = join(&f.home, ".zshrc");
    write_file(&path, SHELL_SENTINEL_PROFILE, 0o600);
    let (out, stderr) = f.run(&f.main, 125, &["shell", "setup", "zsh"]);
    require_no_profile_leak("unconfirmed setup", &[&out, &stderr]);
    assert!(
        out.is_empty()
            && stderr.contains("shell update requires confirmation")
            && stderr.contains(&format!("Shell configuration target: {path}"))
            && stderr.contains("+ # >>> CacheCow owned activation >>>"),
        "out={out:?} stderr={stderr:?}"
    );
    assert_eq!(
        fs::read_to_string(&path).unwrap(),
        SHELL_SENTINEL_PROFILE,
        "unconfirmed setup modified the profile"
    );
}

#[test]
fn shell_json_reports_separator_without_profile_content() {
    let f = Fixture::new();
    let path = join(&f.home, ".bashrc");
    let original = "export SENTINEL_SECRET=c8-sentinel-value";
    write_file(&path, original, 0o600);
    let (out, stderr) = f.run(&f.main, 0, &["shell", "setup", "bash", "--yes", "--json"]);
    require_no_profile_leak("json setup", &[&out, &stderr]);
    assert!(
        out.contains("\"schemaVersion\":1") && out.contains("\"changed\":true"),
        "json envelope {out:?}"
    );
    for want in [
        "Action: add CacheCow block",
        "+ # >>> CacheCow owned activation (joined) >>>",
        "A separator line break is added before the block.",
    ] {
        assert!(
            stderr.contains(want),
            "setup notice missing {want:?}:\n{stderr}"
        );
    }
    let (out, stderr) = f.run(&f.main, 0, &["shell", "remove", "bash", "--yes", "--json"]);
    require_no_profile_leak("json remove", &[&out, &stderr]);
    for want in [
        "Action: remove CacheCow block",
        "- # >>> CacheCow owned activation (joined) >>>",
        "The separator line break before the block is removed.",
    ] {
        assert!(
            stderr.contains(want),
            "remove notice missing {want:?}:\n{stderr}"
        );
    }
    assert_eq!(
        fs::read_to_string(&path).unwrap(),
        original,
        "round trip changed profile"
    );
}

#[test]
fn shell_interactive_confirmation_shows_only_owned_block() {
    let f = Fixture::new();
    let path = join(&f.home, ".zshrc");
    write_file(&path, SHELL_SENTINEL_PROFILE, 0o600);
    let (code, out, stderr) = interactive(&f, &f.main, &["shell", "setup", "zsh"], "y\n");
    require_no_profile_leak("interactive setup", &[&out, &stderr]);
    assert_eq!(code, 0, "{stderr}");
    let block = stderr.find("+ # >>> CacheCow owned activation >>>");
    let prompt = stderr.find("Apply this shell configuration change?");
    assert!(
        matches!((block, prompt), (Some(i), Some(j)) if j > i)
            && stderr.contains(&format!("Shell configuration target: {path}")),
        "setup notice must precede the prompt:\n{stderr}"
    );
    let with_block = fs::read_to_string(&path).unwrap();
    assert!(with_block.starts_with(&format!(
        "{SHELL_SENTINEL_PROFILE}# >>> CacheCow owned activation >>>"
    )));
    let (code, out, stderr) = interactive(&f, &f.main, &["shell", "remove", "zsh"], "n\n");
    require_no_profile_leak("interactive remove", &[&out, &stderr]);
    assert_eq!(code, 125, "{stderr}");
    let block = stderr.find("- # >>> CacheCow owned activation >>>");
    let prompt = stderr.find("Apply this shell configuration change?");
    assert!(
        matches!((block, prompt), (Some(i), Some(j)) if j > i)
            && stderr.contains("shell update cancelled"),
        "remove notice must precede the prompt:\n{stderr}"
    );
    assert_eq!(
        fs::read_to_string(&path).unwrap(),
        with_block,
        "cancelled removal modified the profile"
    );
}

/// #4575: detection depends on the directory, not on how its path is spelled.
#[test]
fn preset_detection_from_symlinked_working_directory() {
    let mut f = Fixture::new();
    let link = join(&f.dir, "link");
    std::os::unix::fs::symlink(&f.dir, &link).unwrap();
    let sub = join(&f.main, "sub");
    fs::create_dir_all(&sub).unwrap();
    for (canonical, spelled) in [
        (f.main.clone(), join(&link, "main")),
        (sub.clone(), join(&link, "main/sub")),
    ] {
        f.set_env("PWD", &spelled);
        let (out, _) = f.run(&spelled, 0, &["status"]);
        assert!(out.contains("\"preset\": \"cargo\""), "{canonical}: {out}");
    }
    write_file(&join(&sub, "rspack.config.js"), "", 0o600);
    write_file(
        &join(&sub, "Cargo.toml"),
        "[package]\nname='sub'\nversion='0.1.0'\n",
        0o600,
    );
    f.set_env("PWD", &join(&link, "main/sub"));
    let (_, stderr) = f.run(&join(&link, "main/sub"), 2, &["status"]);
    assert!(stderr.contains("ambiguous preset"), "{stderr}");
    let empty = join(&f.dir, "empty");
    fs::create_dir_all(&empty).unwrap();
    git(&empty, &["init"]);
    f.set_env("PWD", &join(&link, "empty"));
    let (_, stderr) = f.run(&join(&link, "empty"), 2, &["status"]);
    assert!(stderr.contains("cannot detect a preset"), "{stderr}");
}

/// #4576: clean removes caches whose clone preserved read-only directories.
#[test]
fn clean_removes_read_only_directories() {
    let f = Fixture::new();
    f.run(
        &f.main,
        0,
        &["run", "--preset", "cargo", "--", "cargo", "build"],
    );
    write_file(&join(&f.main, "target/ro/sub/f"), "x", 0o444);
    for dir in ["target/ro/sub", "target/ro"] {
        fs::set_permissions(join(&f.main, dir), fs::Permissions::from_mode(0o555)).unwrap();
    }
    f.run(
        &f.work,
        0,
        &[
            "prepare",
            "--preset",
            "cargo",
            "--replace",
            "--allow-copy",
            "--",
            "cargo",
            "build",
        ],
    );
    let mode = fs::metadata(join(&f.work, "target/ro"))
        .unwrap()
        .permissions()
        .mode()
        & 0o777;
    assert_eq!(mode, 0o555, "clone did not preserve directory permissions");
    f.run(&f.work, 0, &["clean", "--preset", "cargo"]);
    assert!(
        !exists(&join(&f.work, "target")),
        "clean left a partial cache"
    );
    // Missing caches remain a successful no-op.
    f.run(&f.work, 0, &["clean", "--preset", "cargo"]);
}

fn registry_path(f: &Fixture) -> String {
    join(&f.state_base(), "leases/leases.sqlite")
}

/// Holds the registry write lock from another connection for `hold`.
fn hold_registry(path: &str, hold: Duration) -> thread::JoinHandle<()> {
    let path = path.to_string();
    let (ready_tx, ready) = mpsc::channel();
    let handle = thread::spawn(move || {
        let db = rusqlite::Connection::open(path).unwrap();
        db.execute_batch("BEGIN IMMEDIATE").unwrap();
        ready_tx.send(()).unwrap();
        thread::sleep(hold);
        db.execute_batch("ROLLBACK").unwrap();
    });
    ready.recv().unwrap();
    handle
}

/// #4583: registry lock contention is not cache use.
#[test]
fn registry_contention_is_retried_and_reported_separately() {
    let f = Fixture::new();
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    let registry = registry_path(&f);
    let held = hold_registry(&registry, Duration::from_millis(200));
    f.run(&f.main, 0, &["status", "--preset", "cargo"]);
    held.join().unwrap();
    let held = hold_registry(&registry, Duration::from_millis(200));
    f.run(&f.main, 0, &["clean", "--preset", "cargo"]);
    held.join().unwrap();
    assert!(
        !exists(&join(&f.main, "target")),
        "clean kept an idle cache"
    );
    let held = hold_registry(&registry, Duration::from_secs(4));
    let (out, _) = f.run(&f.main, 125, &["status", "--preset", "cargo", "--json"]);
    held.join().unwrap();
    assert!(
        out.contains("lease registry is temporarily locked"),
        "{out}"
    );
    assert!(
        out.contains("Another ccow process held the shared lease registry"),
        "{out}"
    );
    // The supervisor reports clean's contention through its result record;
    // the client keeps the registry-busy remediation.
    let held = hold_registry(&registry, Duration::from_secs(4));
    let (out, _) = f.run(&f.main, 125, &["clean", "--preset", "cargo", "--json"]);
    held.join().unwrap();
    assert!(
        out.contains("lease registry is temporarily locked"),
        "{out}"
    );
    assert!(
        out.contains("Another ccow process held the shared lease registry"),
        "{out}"
    );
}

/// #4582: doctor reports why it keeps a lease.
#[test]
fn doctor_reports_retained_lease_reason() {
    let f = Fixture::new();
    f.run(&f.main, 0, &["run", "--", "cargo", "build"]);
    let journal = join(
        &f.state_base(),
        "processes/018f0000-0000-7000-8000-000000000001",
    );
    fs::create_dir_all(&journal).unwrap();
    fs::set_permissions(&journal, fs::Permissions::from_mode(0o700)).unwrap();
    let id = "018f0000-0000-7000-8000-000000000001";
    write_file(
        &join(&journal, "scope.json"),
        &format!(
            r#"{{"version":1,"id":"{id}","directory":"{journal}","owner_pid":1,"started":true,"complete":false,"exit_code":125}}"#
        ),
        0o600,
    );
    let record_id = uuid::Uuid::now_v7().to_string();
    let record = serde_json::json!({"version": 1, "id": record_id, "ownership": journal, "paths": [join(&f.main, "target")]});
    let db = rusqlite::Connection::open(registry_path(&f)).unwrap();
    db.execute(
        "INSERT INTO leases(id, record) VALUES(?1, ?2)",
        (&record_id, record.to_string()),
    )
    .unwrap();
    drop(db);
    let (out, _) = f.run(&f.main, 0, &["doctor", "--json"]);
    assert!(
        out.contains("incomplete journal without native identity (version 1)"),
        "{out}"
    );
    assert!(out.contains(&journal), "{out}");
    let (_, stderr) = f.run(&f.main, 125, &["clean", "--preset", "cargo"]);
    assert!(stderr.contains("cache is in use"), "{stderr}");
}
