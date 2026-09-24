#[cfg(target_os = "linux")]
mod linux {
    use std::{
        ffi::c_void,
        fs::{self, File},
        io::{self, Read as _, Write as _},
        os::{
            fd::{AsRawFd as _, FromRawFd as _, OwnedFd},
            unix::process::CommandExt as _,
        },
        path::Path,
        process::{Child, Command, Stdio},
        thread,
        time::{Duration, Instant},
    };

    fn min_repro_command(root: &Path, bundle: &Path, script: &str) -> Command {
        let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
        command.current_dir(root).args([
            "fspy",
            "min-repro",
            "--include",
            "input",
            "--bundle-dir",
            bundle.to_str().unwrap(),
            "--expect-exit",
            "7",
            "--expect-stderr",
            "failure",
            "--json",
            "--",
            "/bin/sh",
            "-c",
            script,
        ]);
        command
    }

    #[test]
    fn min_repro_publishes_only_verified_observed_inputs() {
        let root = tempfile::tempdir().unwrap();
        let output = tempfile::tempdir().unwrap();
        let bundle = output.path().join("repro");
        fs::write(root.path().join("input"), b"needed").unwrap();
        fs::write(root.path().join("unused"), b"omit").unwrap();
        let result = min_repro_command(
            root.path(),
            &bundle,
            "cat input >/dev/null; printf failure >&2; exit 7",
        )
        .output()
        .unwrap();
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(fs::read(bundle.join("input")).unwrap(), b"needed");
        assert!(!bundle.join("unused").exists());
        let metadata = fs::read_dir(&bundle)
            .unwrap()
            .map(|entry| entry.unwrap().path())
            .find(|path| {
                path.file_name()
                    .unwrap()
                    .to_string_lossy()
                    .starts_with(".clibox-fspy-bundle-")
            })
            .unwrap();
        let manifest = fs::read_to_string(metadata.join("manifest.json")).unwrap();
        assert!(manifest.contains("external_dependencies"));
        assert!(!manifest.contains("failure"), "stderr predicate leaked");
    }

    #[test]
    fn min_repro_rejects_original_tree_access_and_preserves_existing_bundle() {
        let root = tempfile::tempdir().unwrap();
        let output = tempfile::tempdir().unwrap();
        let bundle = output.path().join("repro");
        let input = root.path().join("input");
        fs::write(&input, b"needed").unwrap();
        let script = format!(
            "cat '{}' >/dev/null; printf failure >&2; exit 7",
            input.display()
        );
        let result = min_repro_command(root.path(), &bundle, &script)
            .output()
            .unwrap();
        assert_eq!(result.status.code(), Some(1));
        assert!(String::from_utf8_lossy(&result.stderr).contains("original_tree_access"));
        assert!(!bundle.exists());

        fs::create_dir(&bundle).unwrap();
        fs::write(bundle.join("keep"), b"unchanged").unwrap();
        let result = min_repro_command(root.path(), &bundle, "touch launched; exit 7")
            .output()
            .unwrap();
        assert_eq!(result.status.code(), Some(2));
        assert_eq!(fs::read(bundle.join("keep")).unwrap(), b"unchanged");
        assert!(!root.path().join("launched").exists());
    }

    #[test]
    fn min_repro_preserves_internal_links_and_blocks_sensitive_inputs() {
        use std::os::unix::fs::symlink;

        let root = tempfile::tempdir().unwrap();
        let output = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("assets")).unwrap();
        fs::write(root.path().join("assets/data"), b"needed").unwrap();
        symlink("assets/data", root.path().join("input")).unwrap();
        let bundle = output.path().join("linked");
        let result = min_repro_command(
            root.path(),
            &bundle,
            "cat input >/dev/null; printf failure >&2; exit 7",
        )
        .output()
        .unwrap();
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(
            fs::read_link(bundle.join("input")).unwrap(),
            Path::new("assets/data")
        );
        assert_eq!(fs::read(bundle.join("assets/data")).unwrap(), b"needed");

        fs::write(root.path().join(".env"), b"secret").unwrap();
        let blocked_bundle = output.path().join("blocked");
        let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
        command.current_dir(root.path()).args([
            "fspy",
            "min-repro",
            "--include",
            ".env",
            "--bundle-dir",
            blocked_bundle.to_str().unwrap(),
            "--expect-exit",
            "7",
            "--expect-stderr",
            "failure",
            "--",
            "/bin/sh",
            "-c",
            "cat .env >/dev/null; printf failure >&2; exit 7",
        ]);
        let result = command.output().unwrap();
        assert_eq!(result.status.code(), Some(1));
        assert!(String::from_utf8_lossy(&result.stderr).contains("blocked_input"));
        assert!(!blocked_bundle.exists());

        let external = tempfile::tempdir().unwrap();
        fs::write(external.path().join("outside"), b"secret").unwrap();
        symlink(external.path().join("outside"), root.path().join("escape")).unwrap();
        let escaped_bundle = output.path().join("escaped");
        let result = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .current_dir(root.path())
            .args([
                "fspy",
                "min-repro",
                "--include",
                "escape",
                "--bundle-dir",
                escaped_bundle.to_str().unwrap(),
                "--expect-exit",
                "7",
                "--expect-stderr",
                "failure",
                "--",
                "/bin/sh",
                "-c",
                "cat escape >/dev/null; printf failure >&2; exit 7",
            ])
            .output()
            .unwrap();
        assert_eq!(result.status.code(), Some(1));
        assert!(String::from_utf8_lossy(&result.stderr).contains("escaping_symlink"));
        assert!(!escaped_bundle.exists());
    }

    #[test]
    fn record_and_compare_require_complete_result_aware_traces() {
        let root = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        fs::write(root.path().join("input"), b"one").unwrap();
        fs::write(root.path().join("other"), b"two").unwrap();
        let before = outside.path().join("before.ndjson");
        let after = outside.path().join("after.ndjson");
        for (trace, script) in [
            (&before, "cat input >/dev/null"),
            (&after, "cat input other >/dev/null"),
        ] {
            let result = Command::new(env!("CARGO_BIN_EXE_clibox"))
                .current_dir(root.path())
                .args([
                    "fspy",
                    "record",
                    "--output",
                    trace.to_str().unwrap(),
                    "--",
                    "/bin/sh",
                    "-c",
                    script,
                ])
                .output()
                .unwrap();
            assert!(
                result.status.success(),
                "{}",
                String::from_utf8_lossy(&result.stderr)
            );
            let events: Vec<serde_json::Value> = fs::read_to_string(trace)
                .unwrap()
                .lines()
                .map(|line| serde_json::from_str(line).unwrap())
                .collect();
            assert!(events
                .iter()
                .any(|event| event["type"] == "operation-completion"
                    && event["bytes"].as_u64().is_some_and(|bytes| bytes > 0)));
            assert_eq!(events.last().unwrap()["type"], "summary");
            assert_eq!(events.last().unwrap()["complete"], true);
        }
        let comparison = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args([
                "fspy",
                "compare",
                before.to_str().unwrap(),
                after.to_str().unwrap(),
                "--fail-on-change",
                "--json",
            ])
            .output()
            .unwrap();
        assert_eq!(comparison.status.code(), Some(1));
        let report: serde_json::Value = serde_json::from_slice(&comparison.stdout).unwrap();
        assert!(!report["added"].as_array().unwrap().is_empty());

        let incomplete = outside.path().join("incomplete.ndjson");
        let valid = fs::read_to_string(&before).unwrap();
        fs::write(
            &incomplete,
            valid
                .lines()
                .take(valid.lines().count() - 1)
                .collect::<Vec<_>>()
                .join("\n"),
        )
        .unwrap();
        let result = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args([
                "fspy",
                "compare",
                incomplete.to_str().unwrap(),
                after.to_str().unwrap(),
            ])
            .output()
            .unwrap();
        assert_eq!(result.status.code(), Some(1));
        assert!(result.stdout.is_empty());
    }

    #[test]
    fn assetcov_counts_content_reads_and_initial_empty_eof() {
        let root = tempfile::tempdir().unwrap();
        fs::create_dir(root.path().join("assets")).unwrap();
        fs::write(root.path().join("assets/used"), b"used").unwrap();
        fs::write(root.path().join("assets/empty"), b"").unwrap();
        fs::write(root.path().join("assets/missed"), b"missed").unwrap();
        let result = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .current_dir(root.path())
            .args([
                "fspy",
                "assetcov",
                "--include",
                "assets/**",
                "--fail-under",
                "90",
                "--json",
                "--",
                "/bin/sh",
                "-c",
                "cat assets/used assets/empty >/dev/null",
            ])
            .output()
            .unwrap();
        assert_eq!(result.status.code(), Some(1));
        let report: serde_json::Value = serde_json::from_slice(&result.stdout).unwrap();
        assert_eq!(report["total_count"], 3);
        assert_eq!(report["covered_count"], 2);
    }

    #[test]
    fn latencylab_runs_equal_tracing_and_observes_injected_delay() {
        let root = tempfile::tempdir().unwrap();
        fs::write(root.path().join("input"), b"content").unwrap();
        let result = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .current_dir(root.path())
            .args([
                "fspy",
                "latencylab",
                "--include",
                "input",
                "--delay",
                "2ms",
                "--runs",
                "1",
                "--json",
                "--",
                "/bin/cat",
                "input",
            ])
            .output()
            .unwrap();
        assert!(
            result.status.success(),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        let report: serde_json::Value = serde_json::from_slice(&result.stdout).unwrap();
        let runs = report["runs"].as_array().unwrap();
        assert_eq!(runs.len(), 2);
        assert_eq!(runs[0]["condition"], "baseline");
        assert_eq!(runs[1]["condition"], "delayed");
        assert_eq!(runs[0]["observed_injected_delay_ns"], 0);
        assert!(runs[1]["observed_injected_delay_ns"].as_u64().unwrap() >= 2_000_000);
    }

    struct PtySession {
        child: Child,
        master: File,
    }

    impl Drop for PtySession {
        fn drop(&mut self) {
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
    }

    struct OwnedChild(Child);

    impl Drop for OwnedChild {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }

    impl PtySession {
        fn launch(root: &Path, input: &Path) -> io::Result<Self> {
            let mut master = -1;
            let mut slave = -1;
            // SAFETY: openpty initializes both descriptor outputs or returns -1.
            if unsafe {
                libc::openpty(
                    &raw mut master,
                    &raw mut slave,
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                    std::ptr::null_mut(),
                )
            } == -1
            {
                return Err(io::Error::last_os_error());
            }
            // SAFETY: both descriptors were initialized by successful openpty.
            let mut master = unsafe { File::from_raw_fd(master) };
            // SAFETY: the slave remains owned through the child spawn.
            let slave = unsafe { OwnedFd::from_raw_fd(slave) };
            // SAFETY: F_GETFL and F_SETFL use a live master descriptor.
            let flags = unsafe { libc::fcntl(master.as_raw_fd(), libc::F_GETFL) };
            if flags == -1
                || unsafe {
                    libc::fcntl(master.as_raw_fd(), libc::F_SETFL, flags | libc::O_NONBLOCK)
                } == -1
            {
                return Err(io::Error::last_os_error());
            }
            let duplicated = || -> io::Result<OwnedFd> {
                // SAFETY: dup returns a new independently owned descriptor.
                let fd = unsafe { libc::dup(slave.as_raw_fd()) };
                if fd == -1 {
                    Err(io::Error::last_os_error())
                } else {
                    // SAFETY: dup returned a fresh descriptor above.
                    Ok(unsafe { OwnedFd::from_raw_fd(fd) })
                }
            };
            let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
            command.args([
                "fspy",
                "fbreak",
                "--root",
                root.to_str().expect("test path UTF-8"),
                "--include",
                "input",
                "--",
                "/bin/cat",
                input.to_str().expect("test path UTF-8"),
            ]);
            command.stdin(Stdio::from(duplicated()?));
            command.stdout(Stdio::from(duplicated()?));
            command.stderr(Stdio::from(duplicated()?));
            let slave_fd = slave.as_raw_fd();
            // SAFETY: the child creates a new session before claiming this
            // test-owned slave PTY as its control terminal.
            unsafe {
                command.pre_exec(move || {
                    if libc::setsid() == -1
                        || libc::ioctl(slave_fd, libc::TIOCSCTTY, 0 as *mut c_void) == -1
                    {
                        return Err(io::Error::last_os_error());
                    }
                    Ok(())
                });
            }
            let child = command.spawn()?;
            drop(slave);
            // Keep the master mutable for deterministic terminal control.
            master.flush()?;
            Ok(Self { child, master })
        }

        fn read_until(&mut self, needle: &str, timeout: Duration) -> String {
            let deadline = Instant::now() + timeout;
            let mut output = Vec::new();
            while Instant::now() < deadline {
                let mut chunk = [0u8; 4096];
                match self.master.read(&mut chunk) {
                    Ok(size) if size > 0 => output.extend_from_slice(&chunk[..size]),
                    Ok(_) => {}
                    Err(error) if error.kind() == io::ErrorKind::WouldBlock => {}
                    Err(error) if error.raw_os_error() == Some(libc::EIO) => break,
                    Err(error) => panic!("PTY read failed: {error}"),
                }
                let text = String::from_utf8_lossy(&output);
                if text.contains(needle) {
                    return text.into_owned();
                }
                if self.child.try_wait().expect("poll fbreak").is_some() {
                    break;
                }
                thread::sleep(Duration::from_millis(10));
            }
            panic!("fbreak did not show expected terminal output: {needle}");
        }
    }

    #[test]
    fn fbreak_stops_before_read_and_next_then_continue_release_callers() {
        let root = tempfile::tempdir().unwrap();
        let input = root.path().join("input");
        fs::write(&input, b"content").unwrap();
        let mut session = PtySession::launch(root.path(), &input).unwrap();
        let first = session.read_until("break: input Read", Duration::from_secs(20));
        assert!(!first.contains("content"), "read executed before control");
        session.master.write_all(b"n").unwrap();
        let second = session.read_until("break: input Read", Duration::from_secs(20));
        assert!(second.contains("content"), "first read was not released");
        session.master.write_all(b"c").unwrap();
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if let Some(status) = session.child.try_wait().unwrap() {
                assert!(status.success(), "fbreak exited with {status}");
                return;
            }
            thread::sleep(Duration::from_millis(10));
        }
        panic!("fbreak did not finish after continue");
    }

    #[test]
    fn fbreak_requires_terminal_before_child_launch() {
        let root = tempfile::tempdir().unwrap();
        let marker = root.path().join("launched");
        let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
        command.args([
            "fspy",
            "fbreak",
            "--root",
            root.path().to_str().unwrap(),
            "--include",
            "input",
            "--",
            "/bin/touch",
            marker.to_str().unwrap(),
        ]);
        command.stdin(Stdio::null());
        // SAFETY: setsid is async-signal-safe and leaves the test child with
        // no controlling terminal, regardless of the test runner's TTY.
        unsafe {
            command.pre_exec(|| {
                if libc::setsid() == -1 {
                    Err(io::Error::last_os_error())
                } else {
                    Ok(())
                }
            });
        }
        let output = command.output().unwrap();
        assert_eq!(output.status.code(), Some(1));
        assert!(String::from_utf8_lossy(&output.stderr).contains("control_tty_unavailable"));
        assert!(!marker.exists());
    }

    #[test]
    fn autowatch_reruns_on_input_change_and_ignores_write_only_output() {
        let root = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let input = root.path().join("input");
        let output = root.path().join("output");
        let runs = outside.path().join("runs");
        fs::write(&input, b"first").unwrap();
        let mut command = Command::new(env!("CARGO_BIN_EXE_clibox"));
        command.args([
            "fspy",
            "autowatch",
            "--root",
            root.path().to_str().unwrap(),
            "--include",
            "input",
            "--debounce",
            "100ms",
            "--",
            "/bin/sh",
            "-c",
            "cat \"$1\" > \"$2\"; printf x >> \"$3\"",
            "sh",
            input.to_str().unwrap(),
            output.to_str().unwrap(),
            runs.to_str().unwrap(),
        ]);
        command.stdout(Stdio::null()).stderr(Stdio::null());
        let mut child = OwnedChild(command.spawn().unwrap());
        let wait_for_runs = |child: &mut OwnedChild, minimum: usize| {
            let deadline = Instant::now() + Duration::from_secs(20);
            while Instant::now() < deadline {
                let count = fs::read(&runs).map_or(0, |bytes| bytes.len());
                if count >= minimum {
                    return;
                }
                assert!(
                    child.0.try_wait().unwrap().is_none(),
                    "autowatch exited early"
                );
                thread::sleep(Duration::from_millis(10));
            }
            panic!("autowatch did not reach run {minimum}");
        };
        wait_for_runs(&mut child, 1);
        thread::sleep(Duration::from_millis(350));
        assert_eq!(
            fs::read(&runs).unwrap().len(),
            1,
            "self-write caused a rerun"
        );
        fs::write(&input, b"second").unwrap();
        wait_for_runs(&mut child, 2);
        assert_eq!(fs::read(&output).unwrap(), b"second");
        // SAFETY: this signal targets only the test-owned watcher process.
        assert_eq!(
            unsafe { libc::kill(i32::try_from(child.0.id()).unwrap(), libc::SIGINT) },
            0
        );
        let deadline = Instant::now() + Duration::from_secs(20);
        while Instant::now() < deadline {
            if let Some(status) = child.0.try_wait().unwrap() {
                assert_eq!(status.code(), Some(130));
                return;
            }
            thread::sleep(Duration::from_millis(10));
        }
        panic!("autowatch did not handle cancellation");
    }
}
