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
