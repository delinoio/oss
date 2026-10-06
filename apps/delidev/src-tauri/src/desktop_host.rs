// SPDX-License-Identifier: Apache-2.0
use std::{
    io::{BufRead, BufReader, Read, Write},
    process::{Child, ChildStdin},
    sync::mpsc,
    thread,
    time::{Duration, Instant},
};

use crate::{
    CliEnvelope, Connector, NativeFailure, OUTPUT_LIMIT, Result, canonical_id, read_bounded,
};

const SHUTDOWN_TIMEOUT: Duration = Duration::from_secs(35);
const MAX_HOST_CHILDREN: usize = 8;
const STOP: &[u8] = b"{\"version\":1,\"action\":\"stop\"}\n";

// Retain the original child rather than deriving termination authority from a
// server PID or endpoint. Even a launch without a ready reply remains owned.
pub(crate) struct DesktopChild {
    child: Child,
    input: Option<ChildStdin>,
    output: thread::JoinHandle<Result<Vec<u8>>>,
    diagnostic: thread::JoinHandle<Result<Vec<u8>>>,
}

impl DesktopChild {
    fn join(self) {
        let _ = self.output.join();
        let _ = self.diagnostic.join();
    }
}

impl Connector {
    pub(crate) fn run_desktop_host(&self, action: &str) -> Result<serde_json::Value> {
        if self.exiting.load(std::sync::atomic::Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        let mode = match action {
            "desktop-launch" => "launch",
            "desktop-retry" => "retry",
            "ensure" => "ensure",
            _ => return Err(NativeFailure::InvalidInput),
        };
        let mut args = self.server_arguments("desktop-host");
        args.extend(["--mode".into(), mode.into()]);
        {
            let mut hosted = self.hosted.lock().unwrap_or_else(|e| e.into_inner());
            let mut i = 0;
            while i < hosted.len() {
                if matches!(hosted[i].child.try_wait(), Ok(Some(_))) {
                    hosted.remove(i).join();
                } else {
                    i += 1;
                }
            }
            if hosted.len() >= MAX_HOST_CHILDREN {
                return Err(NativeFailure::Busy);
            }
        }
        let mut command = self.sidecar_command(&args, true)?;
        // Isolate development terminal/process-group signals too. No
        // kill-on-parent-exit job or EOF shutdown is installed.
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            command.process_group(0);
        }
        #[cfg(windows)]
        {
            use std::os::windows::process::CommandExt;
            command.creation_flags(0x0000_0208); // NEW_PROCESS_GROUP | DETACHED_PROCESS
        }
        let mut child = command.spawn().map_err(|_| NativeFailure::SidecarFailed)?;
        let input = child.stdin.take();
        let stdout = child.stdout.take().ok_or(NativeFailure::SidecarFailed)?;
        let stderr = child.stderr.take().ok_or(NativeFailure::SidecarFailed)?;
        let (send, receive) = mpsc::sync_channel(1);
        let output = thread::spawn(move || {
            let mut reader = BufReader::new(stdout);
            let mut first = Vec::new();
            let result = reader
                .by_ref()
                .take(OUTPUT_LIMIT + 1)
                .read_until(b'\n', &mut first)
                .map_err(|_| NativeFailure::SidecarFailed)
                .and_then(|_| {
                    if first.len() as u64 > OUTPUT_LIMIT {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                    host_result(&first)
                });
            let _ = send.send(result);
            read_bounded(reader, OUTPUT_LIMIT)
        });
        let diagnostic = thread::spawn(move || read_bounded(stderr, OUTPUT_LIMIT));
        {
            let mut hosted = self.hosted.lock().unwrap_or_else(|e| e.into_inner());
            hosted.push(DesktopChild {
                child,
                input,
                output,
                diagnostic,
            });
        }
        let started = Instant::now();
        loop {
            if self.exiting.load(std::sync::atomic::Ordering::Acquire) {
                return Err(NativeFailure::Stopped);
            }
            match receive.recv_timeout(Duration::from_millis(25)) {
                Ok(result) => return result,
                Err(mpsc::RecvTimeoutError::Disconnected) => {
                    return Err(NativeFailure::SidecarFailed);
                }
                Err(mpsc::RecvTimeoutError::Timeout)
                    if started.elapsed() < self.command_timeout => {}
                Err(_) => return Err(NativeFailure::TimedOut),
            }
        }
    }

    pub fn shutdown_owned(&self) -> Result<()> {
        self.shutdown_owned_with_timeout(SHUTDOWN_TIMEOUT)
    }

    fn shutdown_owned_with_timeout(&self, timeout: Duration) -> Result<()> {
        self.exiting
            .store(true, std::sync::atomic::Ordering::Release);
        // Join commands admitted before Quit before taking the complete set.
        // New commands cannot cross their exiting checks after this gate.
        let _gate = self.gate.lock().unwrap_or_else(|e| e.into_inner());
        let mut children =
            std::mem::take(&mut *self.hosted.lock().unwrap_or_else(|e| e.into_inner()));
        let deadline = Instant::now() + timeout;
        for owned in &mut children {
            if let Some(mut input) = owned.input.take() {
                let _ = input.write_all(STOP);
            }
            tracing::info!(operation = "desktop_sidecar_shutdown", phase = "requested");
        }
        let mut children = children.into_iter();
        while let Some(mut owned) = children.next() {
            loop {
                match owned.child.try_wait() {
                    Ok(Some(status)) => {
                        tracing::info!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "process-exit-confirmed",
                            forced = false,
                            successful = status.success()
                        );
                        break;
                    }
                    Ok(None) | Err(_) if Instant::now() < deadline => {
                        thread::sleep(Duration::from_millis(25))
                    }
                    _ => {
                        tracing::warn!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "force-requested",
                            native_cleanup = "unconfirmed"
                        );
                        if owned.child.kill().is_err() {
                            // An exit may race Kill. Recheck the retained
                            // child; never fall
                            // back to signaling a discovered PID.
                            if !matches!(owned.child.try_wait(), Ok(Some(_))) {
                                tracing::error!(
                                    operation = "desktop_sidecar_shutdown",
                                    phase = "process-exit-unconfirmed"
                                );
                                // Preserve handles when termination is
                                // uncertain.
                                let mut retained =
                                    self.hosted.lock().unwrap_or_else(|e| e.into_inner());
                                retained.push(owned);
                                retained.extend(children);
                                return Err(NativeFailure::SidecarFailed);
                            }
                        }
                        if owned.child.wait().is_err() {
                            let mut retained =
                                self.hosted.lock().unwrap_or_else(|e| e.into_inner());
                            retained.push(owned);
                            retained.extend(children);
                            return Err(NativeFailure::SidecarFailed);
                        }
                        tracing::info!(
                            operation = "desktop_sidecar_shutdown",
                            phase = "process-exit-confirmed",
                            forced = true,
                            native_cleanup = "unconfirmed"
                        );
                        break;
                    }
                }
            }
            // Join drains only after observed exit. They cannot inherit server
            // business logs or keep the native UI loop waiting for this
            // process.
            if owned.child.try_wait().is_ok_and(|s| s.is_some()) {
                owned.join();
            }
        }
        Ok(())
    }
}

fn host_result(bytes: &[u8]) -> Result<serde_json::Value> {
    let envelope: CliEnvelope =
        serde_json::from_slice(bytes).map_err(|_| NativeFailure::InvalidEvidence)?;
    if envelope.version != 1 {
        return Err(NativeFailure::Incompatible);
    }
    if let Some(error) = envelope.error {
        return Err(match error.code.as_str() {
            "unsupported" => NativeFailure::Incompatible,
            "invalid_argument" | "missing_input" => NativeFailure::InvalidInput,
            "unauthenticated" => NativeFailure::CredentialUnavailable,
            "permission_denied" => NativeFailure::PermissionDenied,
            "conflict" => NativeFailure::Busy,
            "recovery_required" => NativeFailure::InvalidEvidence,
            _ => NativeFailure::SidecarFailed,
        });
    }
    let result = envelope.result.ok_or(NativeFailure::InvalidEvidence)?;
    if result.get("started").and_then(|v| v.as_bool()) == Some(true) {
        canonical_id(
            result
                .get("generation")
                .and_then(|v| v.as_str())
                .ok_or(NativeFailure::InvalidEvidence)?,
        )?;
    }
    Ok(result)
}

#[cfg(all(test, unix))]
mod tests {
    use std::{
        os::unix::fs::PermissionsExt,
        process::{Command, Stdio},
        sync::Arc,
    };

    use super::*;

    fn fixture(script: &str) -> (tempfile::TempDir, Arc<Connector>) {
        let root = tempfile::tempdir().unwrap();
        let executable = root.path().join("sidecar");
        // A joined writer child prevents sibling fixture forks inheriting an
        // open writable executable description (Linux ETXTBSY).
        let mut writer = Command::new("/bin/sh")
            .env_clear()
            .args(["-c", "umask 077; /bin/cat > \"$1\"", "fixture"])
            .arg(&executable)
            .stdin(Stdio::piped())
            .stdout(Stdio::null())
            .stderr(Stdio::null())
            .spawn()
            .unwrap();
        writer
            .stdin
            .take()
            .unwrap()
            .write_all(script.as_bytes())
            .unwrap();
        assert!(writer.wait().unwrap().success());
        std::fs::set_permissions(&executable, std::fs::Permissions::from_mode(0o700)).unwrap();
        let connector = Arc::new(Connector::new(executable, root.path().to_path_buf()).unwrap());
        (root, connector)
    }

    fn ready() -> String {
        format!(
            "printf '%s\\n' \
             '{{\"version\":1,\"result\":{{\"started\":true,\"generation\":\"{}\"}}}}'\n",
            uuid::Uuid::now_v7()
        )
    }

    #[test]
    fn quit_requests_original_child_and_joins_it_once() {
        let script = format!(
            "#!/bin/sh\n{}IFS= read -r control\n[ \"$control\" = \
             '{{\"version\":1,\"action\":\"stop\"}}' ] || exit 3\nprintf done > \"$2/joined\"\n",
            ready()
        );
        let (root, connector) = fixture(&script);
        assert_eq!(
            connector.run_desktop_host("desktop-launch").unwrap()["started"],
            true
        );
        connector.shutdown_owned().unwrap();
        assert_eq!(std::fs::read(root.path().join("joined")).unwrap(), b"done");
        assert!(connector.hosted.lock().unwrap().is_empty());
        connector.shutdown_owned().unwrap();
        assert_eq!(
            connector.run_desktop_host("ensure"),
            Err(NativeFailure::Stopped)
        );
    }

    #[test]
    fn hung_original_child_is_forced_after_the_grace_period() {
        let (root, connector) = fixture(&format!("#!/bin/sh\n{}while :; do :; done\n", ready()));
        connector.run_desktop_host("desktop-launch").unwrap();
        let started = Instant::now();
        connector
            .shutdown_owned_with_timeout(Duration::from_millis(75))
            .unwrap();
        assert!(started.elapsed() >= Duration::from_millis(75));
        assert!(started.elapsed() < Duration::from_secs(3));
        assert!(connector.hosted.lock().unwrap().is_empty());
        assert!(!root.path().join("joined").exists());
    }

    #[test]
    #[ignore = "Runs the production 35-second grace period against a hung process fixture."]
    fn production_quit_deadline_forces_and_joins_original_child() {
        let (_root, connector) = fixture(&format!("#!/bin/sh\n{}exec /bin/sleep 120\n", ready()));
        connector.run_desktop_host("desktop-launch").unwrap();
        let started = Instant::now();
        connector.shutdown_owned().unwrap();
        assert!(started.elapsed() >= SHUTDOWN_TIMEOUT);
        assert!(started.elapsed() < SHUTDOWN_TIMEOUT + Duration::from_secs(10));
        assert!(connector.hosted.lock().unwrap().is_empty());
    }

    #[test]
    fn quit_during_startup_retains_and_joins_the_unreported_child() {
        let (root, connector) = fixture(
            "#!/bin/sh\nprintf started > \"$2/started\"\nIFS= read -r control\nprintf stopped > \
             \"$2/stopped\"\n",
        );
        let launching = Arc::clone(&connector);
        let task = thread::spawn(move || {
            let _gate = launching.gate.lock().unwrap();
            launching.run_desktop_host("desktop-launch")
        });
        let limit = Instant::now() + Duration::from_secs(3);
        while !root.path().join("started").exists() {
            assert!(Instant::now() < limit);
            thread::sleep(Duration::from_millis(10));
        }
        connector.shutdown_owned().unwrap();
        assert_eq!(task.join().unwrap(), Err(NativeFailure::Stopped));
        assert!(root.path().join("stopped").exists());
        assert!(connector.hosted.lock().unwrap().is_empty());
    }

    #[test]
    fn control_eof_preserves_running_child_until_explicit_fixture_cleanup() {
        let script = format!(
            "#!/bin/sh\n{}if IFS= read -r control; then exit 0; fi\nprintf eof > \
             \"$2/eof\"\nwhile :; do :; done\n",
            ready()
        );
        let (root, connector) = fixture(&script);
        connector.run_desktop_host("desktop-launch").unwrap();
        connector.hosted.lock().unwrap()[0].input.take();
        let limit = Instant::now() + Duration::from_secs(3);
        while !root.path().join("eof").exists() {
            assert!(Instant::now() < limit);
            thread::sleep(Duration::from_millis(10));
        }
        assert!(
            connector.hosted.lock().unwrap()[0]
                .child
                .try_wait()
                .unwrap()
                .is_none()
        );
        connector
            .shutdown_owned_with_timeout(Duration::ZERO)
            .unwrap();
    }

    #[test]
    fn reused_controller_has_no_authority_over_an_external_server() {
        let (root, connector) =
            fixture("#!/bin/sh\nprintf '%s\\n' '{\"version\":1,\"result\":{\"reused\":true}}'\n");
        assert_eq!(
            connector.run_desktop_host("desktop-launch").unwrap()["reused"],
            true
        );
        connector.shutdown_owned().unwrap();
        assert!(!root.path().join("joined").exists());
    }
}
