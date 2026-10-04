use std::{
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
    sync::atomic::{AtomicI32, Ordering},
};
#[cfg(not(target_os = "linux"))]
use std::{
    process::{Command, ExitStatus},
    time::{Duration, Instant},
};

#[cfg(not(target_os = "linux"))]
use pnport::graph::Input;
use pnport::{
    diagnostic::{Code, Error, Result},
    view::View,
};

pub fn platform() -> Result<()> {
    if !cfg!(all(
        any(
            target_os = "macos",
            all(target_os = "linux", target_env = "gnu")
        ),
        any(target_arch = "x86_64", target_arch = "aarch64")
    )) {
        return Err(Error::new(
            Code::PnportUnsupportedOperation,
            "This release supports macOS 15+ and glibc Linux on x64/arm64. Windows support is \
             planned for pnport 0.2.0.",
        ));
    }
    #[cfg(target_os = "macos")]
    {
        let mut host = std::mem::MaybeUninit::<libc::utsname>::zeroed();
        let supported = unsafe {
            libc::uname(host.as_mut_ptr()) == 0
                && std::ffi::CStr::from_ptr(host.assume_init_ref().release.as_ptr())
                    .to_str()
                    .is_ok_and(macos_kernel_supported)
        };
        if !supported {
            return Err(Error::new(
                Code::PnportUnsupportedOperation,
                "macOS 15 or newer is required by this release.",
            ));
        }
    }
    Ok(())
}

#[cfg(any(target_os = "macos", test))]
fn macos_kernel_supported(release: &str) -> bool {
    // Darwin 24 is the macOS 15 kernel. Use the native kernel identity without
    // launching an external command or reading inherited environment values.
    let parts: Vec<_> = release.split('.').collect();
    parts.len() == 3
        && parts
            .iter()
            .all(|part| !part.is_empty() && part.bytes().all(|b| b.is_ascii_digit()))
        && parts[0].len() <= 3
        && parts[0].parse::<u16>().is_ok_and(|major| major >= 24)
}
pub fn artifact() -> Result<PathBuf> {
    platform()?;
    let directory = std::env::current_exe()
        .ok()
        .and_then(|p| p.parent().map(Path::to_owned))
        .ok_or_else(injection_error)?;
    let packaged = directory.join(if cfg!(target_os = "macos") {
        "libpnport_preload.dylib"
    } else {
        "libpnport_preload.so"
    });
    // Development uses the fork's crate name; release packaging keeps the
    // stable pnport companion filename checked by the existing installer.
    let development = directory.join("libfspy_preload_unix.dylib");
    let artifact = if cfg!(target_os = "macos") && development.is_file() {
        development
    } else {
        packaged
    };
    let bytes = fs::read(&artifact).map_err(|_| injection_error())?;
    #[cfg(target_os = "linux")]
    {
        let machine = if cfg!(target_arch = "aarch64") {
            183
        } else {
            62
        };
        if bytes.len() < 64
            || &bytes[..4] != b"\x7fELF"
            || bytes[4] != 2
            || bytes[5] != 1
            || u16::from_le_bytes([bytes[16], bytes[17]]) != 3
            || u16::from_le_bytes([bytes[18], bytes[19]]) != machine
        {
            return Err(injection_error());
        }
    }
    #[cfg(target_os = "macos")]
    {
        // Release companions are native single-slice 64-bit Mach-O dylibs.
        // A marker in another CPU slice or non-library must not report ready.
        let cpu = if cfg!(target_arch = "aarch64") {
            0x0100_000c
        } else {
            0x0100_0007
        };
        if bytes.len() < 32
            || &bytes[..4] != b"\xcf\xfa\xed\xfe"
            || u32::from_le_bytes(bytes[4..8].try_into().unwrap()) != cpu
            || u32::from_le_bytes(bytes[12..16].try_into().unwrap()) != 6
        {
            return Err(injection_error());
        }
    }
    let marker = if cfg!(target_os = "macos") {
        b"PNPORT_PRELOAD_0.1.0_FORMAT_3_READY"
    } else {
        b"PNPORT_PRELOAD_0.1.0_FORMAT_1_READY"
    };
    if !bytes.windows(35).any(|window| window == marker) {
        return Err(injection_error());
    }
    Ok(artifact)
}
fn injection_error() -> Error {
    Error::new(
        Code::PnportInjectionFailed,
        "The matching native injection artifact is unavailable; reinstall the complete native \
         distribution.",
    )
}

pub(crate) fn runtime_failure(session: &Path) -> Result<Option<Error>> {
    let recorded = match fs::read(session.join("failure")) {
        Ok(recorded) => recorded,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        Err(_) => return Err(injection_error()),
    };
    let recorded_code = [
        Code::PnportManifestMissing,
        Code::PnportManifestInvalid,
        Code::PnportFilesystemConflict,
        Code::PnportUnsupportedOperation,
        Code::PnportInjectionFailed,
        Code::PnportArchiveCorrupt,
        Code::PnportCacheFailed,
        Code::PnportGraphChanged,
        Code::PnportCleanupFailed,
        Code::PnportCommandNotFound,
        Code::PnportCommandNotExecutable,
    ]
    .into_iter()
    .find(|code| code.as_str().as_bytes() == recorded);
    let code = recorded_code.unwrap_or(Code::PnportInjectionFailed);
    let initialization_stage = fs::read(session.join("initialization-failure"))
        .ok()
        .and_then(|bytes| {
            serde_json::from_slice::<pnport::diagnostic::InitializationStage>(&bytes).ok()
        });
    let group_operation = fs::read(session.join("process-group-failure"))
        .ok()
        .and_then(|bytes| {
            serde_json::from_slice::<pnport::diagnostic::ProcessGroupOperation>(&bytes).ok()
        });
    tracing::debug!(
        action = "native_failure_record",
        code = code.as_str(),
        recognized = recorded_code.is_some(),
        observed_initialization_failure_stage = ?initialization_stage,
        observed_process_group_failure = ?group_operation,
        "Read the native interception failure record"
    );
    Ok(Some(Error::new(
        code,
        if code == Code::PnportInjectionFailed && group_operation.is_some() {
            "The macOS process owner did not acknowledge a native group or session change; owned \
             processes were stopped."
        } else {
            "Native filesystem interception reported a runtime failure; the process tree has been \
             stopped."
        },
    )))
}

#[cfg(not(target_os = "linux"))]
#[derive(Default)]
struct PendingLaunches {
    observed: std::collections::HashMap<PathBuf, (Option<(u64, u64)>, Instant)>,
}

#[cfg(not(target_os = "linux"))]
fn marker_identity(metadata: &fs::Metadata) -> Option<(u64, u64)> {
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        Some((metadata.dev(), metadata.ino()))
    }
    #[cfg(not(unix))]
    {
        let _ = metadata;
        None
    }
}

#[cfg(not(target_os = "linux"))]
fn pending_entry_state(
    session: &Path,
    path: &Path,
    identity: Option<(u64, u64)>,
) -> Result<Option<pnport::launch::EntryState>> {
    let current = match fs::symlink_metadata(path) {
        Ok(current) if current.is_file() => current,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(None),
        _ => return Err(injection_error()),
    };
    if marker_identity(&current) != identity {
        return Ok(None);
    }
    #[cfg(unix)]
    {
        let token = path.file_name().ok_or_else(injection_error)?;
        pnport::launch::entry_state(&session.join("launch-starting").join(token), &current)
            .map(Some)
            .map_err(|_| injection_error())
    }
    #[cfg(not(unix))]
    {
        let _ = session;
        Ok(Some(pnport::launch::EntryState::Missing))
    }
}

#[cfg(not(target_os = "linux"))]
impl PendingLaunches {
    #[cfg(target_os = "macos")]
    fn pause(&mut self, duration: Duration) {
        for (_, observed) in self.observed.values_mut() {
            *observed += duration;
        }
    }

    fn observe(&mut self, session: &Path) -> Result<bool> {
        let entries = match fs::read_dir(session.join("pending")) {
            Ok(entries) => entries,
            Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
                self.observed.clear();
                return Ok(false);
            }
            Err(_) => return Err(injection_error()),
        };
        let mut present = std::collections::HashSet::new();
        for entry in entries {
            let entry = entry.map_err(|_| injection_error())?;
            let token = entry.file_name();
            if !token.to_str().is_some_and(pnport::launch::valid_token) {
                return Err(injection_error());
            }
            let path = entry.path();
            let metadata = match fs::symlink_metadata(&path) {
                Ok(metadata) if metadata.is_file() => metadata,
                Err(error) if error.kind() == std::io::ErrorKind::NotFound => continue,
                _ => return Err(injection_error()),
            };
            let identity = marker_identity(&metadata);
            let observed = self
                .observed
                .entry(path.clone())
                .or_insert_with(|| (identity, Instant::now()));
            if observed.0 != identity {
                *observed = (identity, Instant::now());
            }
            present.insert(path.clone());
            #[cfg(unix)]
            let state = pnport::launch::entry_state(
                &session.join("launch-starting").join(token),
                &metadata,
            )
            .map_err(|_| injection_error())?;
            #[cfg(not(unix))]
            let state = pnport::launch::EntryState::Missing;
            let abandoned = state == pnport::launch::EntryState::Abandoned;
            if abandoned
                || state == pnport::launch::EntryState::Missing
                    && observed.1.elapsed() > Duration::from_secs(5)
            {
                // Completion can remove the pending inode, or the constructor
                // can enter after this scan's first observation. Recheck both
                // pending identity and its current entry/lease before failure.
                let abandoned = match pending_entry_state(session, &path, identity)? {
                    None => {
                        present.remove(&path);
                        continue;
                    }
                    Some(pnport::launch::EntryState::Abandoned) => true,
                    Some(pnport::launch::EntryState::Missing)
                        if observed.1.elapsed() > Duration::from_secs(5) =>
                    {
                        false
                    }
                    Some(_) => continue,
                };
                // Failed constructors publish before releasing their lease.
                // Re-read now so a failure after the loop's earlier read still
                // retains its first code and initialization-stage debug log.
                if let Some(error) = runtime_failure(session)? {
                    return Err(error);
                }
                tracing::debug!(
                    action = "descendant_injection_deadline",
                    constructor_entered = abandoned,
                    initializer_lease_released = abandoned,
                    "A pending native image did not complete initialization"
                );
                return Err(Error::new(
                    Code::PnportInjectionFailed,
                    if abandoned {
                        "A child executable exited during native initialization; execution was \
                         stopped."
                    } else {
                        "A child executable did not acknowledge native injection; execution was \
                         stopped."
                    },
                ));
            }
        }
        self.observed.retain(|path, _| present.contains(path));
        Ok(!present.is_empty())
    }
}

static SIGNAL: AtomicI32 = AtomicI32::new(0);
#[cfg(target_os = "linux")]
pub(crate) fn handled_signal() -> i32 {
    SIGNAL.load(Ordering::SeqCst)
}
#[cfg(unix)]
fn install_signal_handlers() {
    SIGNAL.store(0, Ordering::SeqCst);
    unsafe {
        for signal in [libc::SIGINT, libc::SIGTERM, libc::SIGHUP] {
            libc::signal(signal, signal_handler as *const () as usize);
        }
    }
}
#[cfg(unix)]
extern "C" fn signal_handler(signal: i32) {
    SIGNAL.store(signal, Ordering::SeqCst);
}

pub fn run(view: &mut View, artifact: &Path, executable: &Path, args: &[OsString]) -> Result<i32> {
    let node_loader = pnport::node::Loader::from_snapshot(&view.graph.snapshot);
    let node_options = node_loader.options(std::env::var_os("NODE_OPTIONS").as_deref())?;
    tracing::debug!(
        action = "node_runtime_prepared",
        esm = node_loader.has_esm(),
        "Prepared automatic Yarn runtime activation for Node workloads"
    );
    let prepared =
        pnport::executable::prepare(view, executable, args, std::env::var_os("PATH").as_deref())?;
    tracing::debug!(
        action = "command_prepared",
        "Prepared the owned command image"
    );
    #[cfg(unix)]
    install_signal_handlers();
    #[cfg(target_os = "linux")]
    {
        let _ = artifact;
        crate::linux::run_traced(view, &prepared, &node_options)
    }
    #[cfg(not(target_os = "linux"))]
    {
        #[cfg(target_os = "macos")]
        let admission =
            fspy_shared_unix::spawn::admit_pnport_program(&prepared.program).map_err(|_| {
                Error::new(
                    Code::PnportUnsupportedOperation,
                    "The executable cannot accept macOS filesystem injection.",
                )
            })?;
        #[cfg(target_os = "macos")]
        tracing::debug!(
            action = "native_image_admitted",
            "Admitted the native command image"
        );
        #[cfg(target_os = "macos")]
        let admitted_program = &admission.path;
        #[cfg(not(target_os = "macos"))]
        let admitted_program = prepared.program;
        let mut command = Command::new(admitted_program);
        command
            .args(&prepared.args)
            .env("PNPORT_SESSION", &view.session)
            .env("PNPORT_CACHE", &view.cache.root)
            .env("NODE_OPTIONS", &node_options)
            .env_remove("PNPORT_LAUNCH_TOKEN");
        let variable = if cfg!(target_os = "macos") {
            "DYLD_INSERT_LIBRARIES"
        } else {
            "LD_PRELOAD"
        };
        if std::env::var_os(variable).is_some() {
            return Err(Error::new(
                Code::PnportUnsupportedOperation,
                "An existing native preload may conflict with pnport; start from an environment \
                 without another interception library.",
            ));
        }
        #[cfg(target_os = "macos")]
        command.env(variable, artifact);
        #[cfg(not(target_os = "macos"))]
        command.env(variable, artifact);
        #[cfg(target_os = "macos")]
        let mut owner = crate::macos_owner::Owner::start(&view.session)?;
        #[cfg(unix)]
        {
            use std::os::unix::process::CommandExt;
            #[cfg(target_os = "macos")]
            command
                .process_group(owner.group())
                .env("PNPORT_MACOS_GROUP", owner.group().to_string())
                .env("PNPORT_MACOS_OWNER_KEY", owner.verification_key());
            #[cfg(not(target_os = "macos"))]
            command.process_group(0);
        }
        tracing::debug!(action = "spawn", "Starting the owned process tree");
        #[cfg(target_os = "macos")]
        let mut job = crate::macos_job::Job::start(owner.group(), &mut command)?;
        #[cfg(target_os = "macos")]
        admission.verify_at_launch()?;
        let mut child = command.spawn().map_err(|e| {
            Error::new(
                if e.kind() == std::io::ErrorKind::NotFound {
                    Code::PnportCommandNotFound
                } else {
                    Code::PnportCommandNotExecutable
                },
                "The operating system could not start the executable.",
            )
        })?;
        let pid = child.id() as i32;
        #[cfg(target_os = "macos")]
        owner.admit_root(pid)?;
        let start = Instant::now();
        #[cfg(target_os = "macos")]
        let mut start = start;
        let mut status = None;
        let mut root_exited_at = None;
        let mut watch = crate::input_watch::InputWatch::default();
        let mut active_markers = std::collections::HashSet::new();
        let starting = view.session.join("starting").join(pid.to_string());
        let ready = view.session.join("ready").join(pid.to_string());
        let mut initialization_started = false;
        let mut pending_launches = PendingLaunches::default();
        let result = (|| loop {
            #[cfg(target_os = "macos")]
            owner.check()?;
            if SIGNAL.load(Ordering::SeqCst) != 0 {
                return Ok(128 + SIGNAL.load(Ordering::SeqCst));
            }
            #[cfg(target_os = "macos")]
            if status.is_none() {
                if let Some(paused) = job.poll_stop(pid, &mut owner)? {
                    start += paused;
                    pending_launches.pause(paused);
                    owner.resume(job.group())?;
                }
            }
            for input in &view.graph.snapshot.inputs {
                watch.register(input)?;
            }
            view.graph.check_conflicts()?;
            if let Some(error) = runtime_failure(&view.session)? {
                return Err(error);
            }
            if let Ok(entries) = fs::read_dir(view.session.join("active")) {
                for entry in entries {
                    let entry = entry.map_err(|_| injection_error())?;
                    if entry.file_name().to_string_lossy().starts_with('.')
                        || !active_markers.insert(entry.path())
                    {
                        continue;
                    }
                    let input: Input = serde_json::from_slice(
                        &fs::read(entry.path()).map_err(|_| injection_error())?,
                    )
                    .map_err(|_| injection_error())?;
                    watch.register(&input)?;
                }
            }
            watch.poll()?;
            // Observe descendant launches while the root is still running.
            // Constructor entry permits legitimate cache coordination; only
            // removal after readiness acknowledges a complete native image.
            pending_launches.observe(&view.session)?;
            if !initialization_started && starting.is_file() {
                initialization_started = true;
                tracing::debug!(
                    action = "initialization_started",
                    "Native preload entered initialization"
                );
            }
            if let Some(exit) = child.try_wait().map_err(|_| injection_error())? {
                // The child may publish its admission/runtime failure after
                // this loop's first read and then exit before try_wait. Read
                // again after observing exit so success cannot hide that code.
                if let Some(error) = runtime_failure(&view.session)? {
                    return Err(error);
                }
                if status.is_none() {
                    status = Some(exit);
                    root_exited_at = Some(Instant::now());
                    tracing::debug!(action="child_exit", status=?exit, "Child exited");
                }
                if !ready.is_file() {
                    return Err(Error::new(
                        Code::PnportInjectionFailed,
                        "The executable did not initialize native interception; its result is not \
                         a virtualized run.",
                    ));
                }
                // A launch can be published after the running-root scan and
                // before exit. Check again before accepting the final result.
                if pending_launches.observe(&view.session)? {
                    if root_exited_at.is_some_and(|at| at.elapsed() > Duration::from_secs(5)) {
                        return Err(Error::new(
                            Code::PnportInjectionFailed,
                            "A child executable did not acknowledge native injection; its trace \
                             is incomplete.",
                        ));
                    }
                    std::thread::sleep(Duration::from_millis(100));
                    continue;
                }
                return Ok(exit_code(exit));
            }
            if start.elapsed() > Duration::from_secs(5)
                && !initialization_started
                // Entry can be published after this loop's earlier observation.
                // Recheck at rejection, as for pending descendant images, so a
                // legitimate constructor/cache wait retains its admission.
                && !starting.is_file()
                && !ready.is_file()
            {
                if let Some(error) = runtime_failure(&view.session)? {
                    return Err(error);
                }
                tracing::debug!(
                    action = "root_injection_deadline",
                    constructor_entered = false,
                    acknowledged = false,
                    "Root native initialization did not enter before its active deadline"
                );
                return Err(Error::new(
                    Code::PnportInjectionFailed,
                    "The executable did not acknowledge native injection; execution was stopped.",
                ));
            }
            std::thread::sleep(Duration::from_millis(100));
        })();
        if let Err(error) = &result {
            tracing::debug!(
                action = "supervisor_failed",
                code = error.code.as_str(),
                initialization_started,
                ready = ready.is_file(),
                "Owned execution failed before cleanup"
            );
        }
        #[cfg(target_os = "macos")]
        let cleanup = {
            use std::os::unix::process::ExitStatusExt;
            let terminal = job.restore_owned(&mut owner);
            let signal = match SIGNAL.load(Ordering::SeqCst) {
                signal @ (libc::SIGINT | libc::SIGTERM | libc::SIGHUP) => signal,
                _ => match status.and_then(|exit| exit.signal()) {
                    Some(signal @ (libc::SIGINT | libc::SIGTERM | libc::SIGHUP)) => signal,
                    _ => libc::SIGTERM,
                },
            };
            owner.stop(signal).and(terminal)
        };
        #[cfg(all(unix, not(target_os = "macos")))]
        unsafe {
            libc::kill(-pid, libc::SIGTERM);
            let deadline = Instant::now() + Duration::from_secs(5);
            while libc::kill(-pid, 0) == 0 && Instant::now() < deadline {
                if status.is_none() {
                    status = child.try_wait().ok().flatten();
                }
                std::thread::sleep(Duration::from_millis(25));
            }
            libc::kill(-pid, libc::SIGKILL);
        }
        if status.is_none() {
            child.wait().map_err(|_| {
                Error::new(
                    Code::PnportCleanupFailed,
                    "Could not reap the owned executable.",
                )
            })?;
        }
        #[cfg(target_os = "macos")]
        cleanup?;
        tracing::debug!(action = "cleanup", "Owned process group stopped");
        result
    }
}
#[cfg(not(target_os = "linux"))]
fn exit_code(status: ExitStatus) -> i32 {
    #[cfg(unix)]
    {
        use std::os::unix::process::ExitStatusExt;
        status
            .code()
            .unwrap_or_else(|| 128 + status.signal().unwrap_or(0))
    }
    #[cfg(not(unix))]
    {
        status.code().unwrap_or(125)
    }
}

#[cfg(test)]
mod failure_tests {
    use super::*;

    #[test]
    fn macos_floor_rejects_older_and_unknown_kernel_versions() {
        for release in ["22.6.0", "23.6.0", "", "unknown", "24", "24.0.0extra"] {
            assert!(!macos_kernel_supported(release));
        }
        for release in ["24.0.0", "25.1.0"] {
            assert!(macos_kernel_supported(release));
        }
    }

    #[test]
    fn recorded_runtime_code_is_reported_and_invalid_marker_fails_closed() {
        let session = tempfile::tempdir().unwrap();
        assert!(runtime_failure(session.path()).unwrap().is_none());
        fs::write(
            session.path().join("failure"),
            Code::PnportArchiveCorrupt.as_str(),
        )
        .unwrap();
        assert_eq!(
            runtime_failure(session.path()).unwrap().unwrap().code,
            Code::PnportArchiveCorrupt
        );
        fs::write(session.path().join("failure"), b"unexpected").unwrap();
        assert_eq!(
            runtime_failure(session.path()).unwrap().unwrap().code,
            Code::PnportInjectionFailed
        );
    }
}

#[cfg(all(test, target_os = "macos"))]
mod pending_launch_tests {
    use super::*;

    #[test]
    fn abandoned_entry_retains_a_failure_published_after_the_first_read() {
        const ISOLATED: &str = "PNPORT_TEST_PENDING_ABANDONED";
        if std::env::var_os(ISOLATED).as_deref() != Some(std::ffi::OsStr::new("1")) {
            // Parallel owner tests fork real native children. Their temporary
            // inherited open descriptions legitimately retain this lease until
            // exec, so an immediate abandoned assertion needs its own process.
            // Keep the parent suite parallel and the explicit inherited-lease
            // control in pnport-core rather than retrying a timing assertion.
            let output = Command::new(std::env::current_exe().unwrap())
                .args(["supervisor::pending_launch_tests::abandoned_entry_retains_a_failure_published_after_the_first_read", "--exact"])
                .env(ISOLATED, "1").output().unwrap();
            assert!(
                output.status.success(),
                "isolated pending failure control failed: {}{}",
                String::from_utf8_lossy(&output.stdout),
                String::from_utf8_lossy(&output.stderr)
            );
            return;
        }
        let session = tempfile::tempdir().unwrap();
        fs::create_dir(session.path().join("pending")).unwrap();
        fs::write(session.path().join("pending/pnport-test"), b"").unwrap();
        let entry = pnport::launch::Entry::begin(session.path(), "pnport-test").unwrap();
        assert!(runtime_failure(session.path()).unwrap().is_none());
        fs::write(
            session.path().join("initialization-failure"),
            serde_json::to_vec(&pnport::diagnostic::InitializationStage::ReadGraph).unwrap(),
        )
        .unwrap();
        fs::write(
            session.path().join("failure"),
            Code::PnportArchiveCorrupt.as_str(),
        )
        .unwrap();
        drop(entry);
        let mut launches = PendingLaunches::default();
        assert_eq!(
            launches.observe(session.path()).unwrap_err().code,
            Code::PnportArchiveCorrupt
        );
    }

    #[test]
    fn deadline_confirmation_observes_entry_published_after_an_initial_miss() {
        let session = tempfile::tempdir().unwrap();
        fs::create_dir(session.path().join("pending")).unwrap();
        let pending = session.path().join("pending/pnport-test");
        let entered = session.path().join("launch-starting/pnport-test");
        fs::write(&pending, b"").unwrap();
        let metadata = fs::metadata(&pending).unwrap();
        assert!(
            pnport::launch::entry_state(&entered, &metadata).unwrap()
                == pnport::launch::EntryState::Missing
        );
        // Reproduce constructor publication between the first missing-entry
        // observation and the supervisor's final pending-inode confirmation.
        let entry = pnport::launch::Entry::begin(session.path(), "pnport-test").unwrap();
        assert!(matches!(
            pending_entry_state(session.path(), &pending, marker_identity(&metadata)).unwrap(),
            Some(pnport::launch::EntryState::Initializing)
        ));
        entry.acknowledge().unwrap();
        assert!(
            pending_entry_state(session.path(), &pending, marker_identity(&metadata))
                .unwrap()
                .is_none()
        );
    }

    #[test]
    fn cache_wait_requires_entry_for_the_exact_pending_inode() {
        let session = tempfile::tempdir().unwrap();
        let pending = session.path().join("pending");
        let starting = session.path().join("launch-starting");
        fs::create_dir(&pending).unwrap();
        fs::create_dir(&starting).unwrap();
        let marker = pending.join("pnport-test");
        let entered = starting.join("pnport-test");
        fs::write(&marker, b"").unwrap();
        fs::hard_link(&marker, &entered).unwrap();
        let lease = fs::File::open(&marker).unwrap();
        fs2::FileExt::try_lock_exclusive(&lease).unwrap();
        let mut launches = PendingLaunches::default();
        launches.observed.insert(
            marker.clone(),
            (
                marker_identity(&fs::metadata(&marker).unwrap()),
                Instant::now() - Duration::from_secs(6),
            ),
        );
        assert!(launches.observe(session.path()).unwrap());
        fs::remove_file(&marker).unwrap();
        fs::write(&marker, b"").unwrap();
        // Reuse of a filename starts a fresh deadline and cannot reuse the
        // previous image's hard-linked constructor acknowledgement.
        assert!(launches.observe(session.path()).unwrap());
        launches.observed.get_mut(&marker).unwrap().1 = Instant::now() - Duration::from_secs(6);
        assert_eq!(
            launches.observe(session.path()).unwrap_err().code,
            Code::PnportInjectionFailed
        );
        fs::remove_file(&marker).unwrap();
        assert!(!launches.observe(session.path()).unwrap());
        assert!(launches.observed.is_empty());
    }
}

#[cfg(all(test, target_os = "macos"))]
mod tests {
    use std::sync::{atomic::AtomicBool, Arc};

    use fs2::FileExt;
    use pnport::{
        cache::{private_dir, Cache},
        graph::{Graph, Snapshot},
    };
    use serde_json::json;

    use super::*;

    #[derive(Clone, Copy)]
    enum Scenario {
        CacheContention,
        IncompleteInitialization,
        UnacknowledgedDescendant,
        LiveUnacknowledgedDescendant,
        DescendantCacheContention,
        AbandonedDescendantInitialization,
    }

    impl Scenario {
        fn name(self) -> &'static str {
            match self {
                Self::CacheContention => {
                    "cache_contention_after_initializer_entry_can_exceed_five_seconds"
                }
                Self::IncompleteInitialization => {
                    "initializer_entry_without_readiness_cannot_accept_a_child_result"
                }
                Self::UnacknowledgedDescendant => {
                    "unacknowledged_descendant_prevents_a_complete_result"
                }
                Self::LiveUnacknowledgedDescendant => {
                    "unacknowledged_descendant_stops_a_running_root"
                }
                Self::DescendantCacheContention => {
                    "descendant_constructor_entry_permits_prolonged_cache_wait"
                }
                Self::AbandonedDescendantInitialization => {
                    "killed_descendant_initializer_stops_a_running_root"
                }
            }
        }
    }

    fn run_in_fresh_process(scenario: Scenario) -> bool {
        let name = scenario.name();
        if std::env::var("PNPORT_SUPERVISOR_TEST_SCENARIO").as_deref() == Ok(name) {
            return false;
        }
        // A CLI process owns one supervisor and its process-wide signal state.
        // Parallel unit threads cannot model that ownership; keep each scenario
        // in its own process while the normal test runner remains parallel.
        let output = Command::new(std::env::current_exe().unwrap())
            .args([
                format!("supervisor::tests::{name}"),
                "--exact".into(),
                "--nocapture".into(),
            ])
            .env("PNPORT_SUPERVISOR_TEST_SCENARIO", name)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "Supervisor scenario {name} failed: {}\n{}",
            String::from_utf8_lossy(&output.stdout),
            String::from_utf8_lossy(&output.stderr)
        );
        true
    }

    fn record_outcome(scenario: Scenario, result: &Result<i32>) {
        eprintln!(
            "{}",
            json!({
                "event": "pnport_supervisor_test_result",
                "scenario": scenario.name(),
                "status": result.as_ref().ok(),
                "code": result.as_ref().err().map(|error| error.code.as_str()),
            })
        );
    }

    fn fixture() -> (tempfile::TempDir, View, PathBuf) {
        let root = tempfile::tempdir().unwrap();
        let path = fs::canonicalize(root.path()).unwrap();
        let session = path.join("session");
        private_dir(&session).unwrap();
        let graph = Graph::from_snapshot(Snapshot {
            schema_version: 1,
            manifest_path: path.join(".pnp.cjs"),
            data: json!({
                "enableTopLevelFallback": false, "ignorePatternData": null,
                "dependencyTreeRoots": [], "fallbackPool": [], "fallbackExclusionList": [],
                "packageRegistryData": [[null, [[null, {
                    "packageLocation": "./", "packageDependencies": [], "linkType": "SOFT"
                }]]]]
            }),
            inputs: vec![],
        })
        .unwrap();
        fs::write(
            session.join("graph.json"),
            serde_json::to_vec(&graph.snapshot).unwrap(),
        )
        .unwrap();
        let cache = Cache::open(path.join("cache")).unwrap();
        let source = path.join("probe.c");
        let executable = path.join("probe");
        fs::write(&source, "int main(void) { return 0; }\n").unwrap();
        assert!(Command::new("cc")
            .arg(&source)
            .arg("-o")
            .arg(&executable)
            .status()
            .unwrap()
            .success());
        (root, View::new(graph, cache, session), executable)
    }

    #[test]
    fn cache_contention_after_initializer_entry_can_exceed_five_seconds() {
        if run_in_fresh_process(Scenario::CacheContention) {
            return;
        }
        let (_root, mut view, executable) = fixture();
        let lock = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(view.cache.root.join(".lock"))
            .unwrap();
        lock.lock_exclusive().unwrap();
        let session = view.session.clone();
        let supervisor_finished = Arc::new(AtomicBool::new(false));
        let finished = Arc::clone(&supervisor_finished);
        let release = std::thread::spawn(move || {
            let deadline = Instant::now() + Duration::from_secs(15);
            loop {
                if fs::read_dir(session.join("starting"))
                    .ok()
                    .is_some_and(|mut entries| entries.next().is_some())
                {
                    break;
                }
                if finished.load(Ordering::SeqCst) || Instant::now() >= deadline {
                    return Err("The preload did not start before supervision ended or timed out.");
                }
                std::thread::sleep(Duration::from_millis(10));
            }
            // Begin the long wait only after actual constructor entry, so a
            // slow compiler/signature check cannot shorten cache contention.
            std::thread::sleep(Duration::from_secs(6));
            assert!(
                !session.join("ready").exists(),
                "Readiness must wait for cache coordination."
            );
            drop(lock);
            Ok(())
        });
        let result = run(&mut view, &artifact().unwrap(), &executable, &[]);
        record_outcome(Scenario::CacheContention, &result);
        supervisor_finished.store(true, Ordering::SeqCst);
        release.join().unwrap().unwrap();
        assert_eq!(result.unwrap(), 0);
        assert_eq!(fs::read_dir(view.session.join("ready")).unwrap().count(), 1);
    }

    #[test]
    fn initializer_entry_without_readiness_cannot_accept_a_child_result() {
        if run_in_fresh_process(Scenario::IncompleteInitialization) {
            return;
        }
        let (root, mut view, executable) = fixture();
        let source = root.path().join("incomplete.c");
        let library = root.path().join("incomplete.dylib");
        fs::write(
            &source,
            r#"
#include <fcntl.h>
#include <stdio.h>
#include <stdlib.h>
#include <sys/stat.h>
#include <unistd.h>
__attribute__((constructor)) static void start(void) {
    char path[4096];
    snprintf(path, sizeof(path), "%s/starting", getenv("PNPORT_SESSION"));
    mkdir(path, 0700);
    snprintf(path, sizeof(path), "%s/starting/%d", getenv("PNPORT_SESSION"), getpid());
    int fd = open(path, O_WRONLY | O_CREAT, 0600);
    if (fd >= 0) close(fd);
}
"#,
        )
        .unwrap();
        assert!(Command::new("cc")
            .arg("-dynamiclib")
            .arg(&source)
            .arg("-o")
            .arg(&library)
            .status()
            .unwrap()
            .success());
        // A saturated host can consume the five-second missing-injection
        // deadline before dyld enters the constructor. Retry that unobserved
        // launch, but accept this test only after entry is actually proven.
        for attempt in 0..3 {
            let result = run(&mut view, &library, &executable, &[]);
            record_outcome(Scenario::IncompleteInitialization, &result);
            assert_eq!(result.unwrap_err().code, Code::PnportInjectionFailed);
            let observed = fs::read_dir(view.session.join("starting"))
                .ok()
                .is_some_and(|mut entries| entries.next().is_some());
            if observed {
                assert_eq!(
                    fs::read_dir(view.session.join("starting")).unwrap().count(),
                    1
                );
                return;
            }
            tracing::debug!(
                attempt,
                "constructor entry was not observed before deadline"
            );
        }
        panic!("The preload constructor did not start in three launches");
    }

    #[test]
    fn unacknowledged_descendant_prevents_a_complete_result() {
        if run_in_fresh_process(Scenario::UnacknowledgedDescendant) {
            return;
        }
        let (_root, mut view, executable) = fixture();
        fs::create_dir(view.session.join("pending")).unwrap();
        fs::write(view.session.join("pending/pnport-unacknowledged"), b"").unwrap();
        let result = run(&mut view, &artifact().unwrap(), &executable, &[]);
        record_outcome(Scenario::UnacknowledgedDescendant, &result);
        assert_eq!(result.unwrap_err().code, Code::PnportInjectionFailed);
    }

    #[test]
    fn unacknowledged_descendant_stops_a_running_root() {
        if run_in_fresh_process(Scenario::LiveUnacknowledgedDescendant) {
            return;
        }
        let (root, mut view, executable) = fixture();
        fs::write(
            root.path().join("probe.c"),
            "#include <unistd.h>\n#include <fcntl.h>\nint main(int argc, char **argv) {\nif (argc \
             != 2) return 1;\nsleep(8);\nint fd = open(argv[1], O_CREAT | O_WRONLY, 0600);\nif \
             (fd < 0) return 2;\nclose(fd); return 0; }\n",
        )
        .unwrap();
        assert!(Command::new("cc")
            .arg(root.path().join("probe.c"))
            .arg("-o")
            .arg(&executable)
            .status()
            .unwrap()
            .success());
        fs::create_dir(view.session.join("pending")).unwrap();
        fs::write(view.session.join("pending/pnport-unacknowledged"), b"").unwrap();
        let finished = root.path().join("root-finished");
        let result = run(
            &mut view,
            &artifact().unwrap(),
            &executable,
            &[finished.clone().into_os_string()],
        );
        record_outcome(Scenario::LiveUnacknowledgedDescendant, &result);
        assert_eq!(result.unwrap_err().code, Code::PnportInjectionFailed);
        assert_eq!(fs::read_dir(view.session.join("ready")).unwrap().count(), 1);
        assert!(
            !finished.exists(),
            "The unacknowledged launch was ignored until the root exited."
        );
    }

    #[test]
    fn descendant_constructor_entry_permits_prolonged_cache_wait() {
        if run_in_fresh_process(Scenario::DescendantCacheContention) {
            return;
        }
        descendant_initialization(DescendantOutcome::Ready);
    }

    #[test]
    fn killed_descendant_initializer_stops_a_running_root() {
        if run_in_fresh_process(Scenario::AbandonedDescendantInitialization) {
            return;
        }
        descendant_initialization(DescendantOutcome::Killed);
    }

    #[derive(Clone, Copy)]
    enum DescendantOutcome {
        Ready,
        Killed,
    }

    fn descendant_initialization(outcome: DescendantOutcome) {
        let (root, mut view, executable) = fixture();
        fs::write(
            root.path().join("probe.c"),
            r#"
#include <fcntl.h>
#include <spawn.h>
#include <signal.h>
#include <sys/wait.h>
#include <unistd.h>
extern char **environ;
int main(int argc, char **argv) {
    alarm(20);
    if (argc == 2) return 0;
    if (argc != 6) return 1;
    int fd = open(argv[1], O_CREAT | O_WRONLY, 0600);
    if (fd < 0 || close(fd)) return 2;
    while (access(argv[2], F_OK)) usleep(10000);
    char *child_argv[] = {argv[0], "child", 0};
    pid_t child;
    if (posix_spawn(&child, argv[0], 0, 0, child_argv, environ)) return 3;
    if (argv[3][0]) {
        while (access(argv[3], F_OK)) usleep(10000);
        fd = open(argv[5], O_CREAT | O_WRONLY, 0600);
        if (fd < 0 || close(fd)) return 6;
        // The native parent retains its direct child unreaped through kill.
        if (kill(child, SIGKILL)) return 7;
    }
    int status;
    if (waitpid(child, &status, 0) != child) return 4;
    if (argv[3][0]) {
        if (!WIFSIGNALED(status) || WTERMSIG(status) != SIGKILL) return 8;
        sleep(8);
    }
    fd = open(argv[4], O_CREAT | O_WRONLY, 0600);
    if (fd < 0 || close(fd)) return 9;
    if (argv[3][0]) return 0;
    return WIFEXITED(status) ? WEXITSTATUS(status) : 5;
}
"#,
        )
        .unwrap();
        assert!(Command::new("cc")
            .arg(root.path().join("probe.c"))
            .arg("-o")
            .arg(&executable)
            .status()
            .unwrap()
            .success());
        let root_started = root.path().join("root-started");
        let allow_child = root.path().join("allow-child");
        let allow_kill = root.path().join("allow-kill");
        let killed = root.path().join("child-killed");
        let root_finished = root.path().join("root-finished");
        let lock = fs::OpenOptions::new()
            .read(true)
            .write(true)
            .open(view.cache.root.join(".lock"))
            .unwrap();
        let session = view.session.clone();
        let supervisor_finished = Arc::new(AtomicBool::new(false));
        let finished = Arc::clone(&supervisor_finished);
        let started = root_started.clone();
        let allow = allow_child.clone();
        let kill = allow_kill.clone();
        let release = std::thread::spawn(move || {
            let deadline = Instant::now() + Duration::from_secs(15);
            while !started.is_file() {
                if finished.load(Ordering::SeqCst) || Instant::now() >= deadline {
                    return Err("The root did not initialize before supervision ended.");
                }
                std::thread::sleep(Duration::from_millis(10));
            }
            lock.lock_exclusive().unwrap();
            fs::write(allow, b"").unwrap();
            while fs::read_dir(session.join("launch-starting"))
                .ok()
                .is_none_or(|mut entries| entries.next().is_none())
            {
                if finished.load(Ordering::SeqCst) || Instant::now() >= deadline {
                    return Err(
                        "The descendant constructor did not enter before supervision ended.",
                    );
                }
                std::thread::sleep(Duration::from_millis(10));
            }
            assert_eq!(fs::read_dir(session.join("ready")).unwrap().count(), 1);
            assert_eq!(fs::read_dir(session.join("pending")).unwrap().count(), 1);
            match outcome {
                DescendantOutcome::Ready => {
                    std::thread::sleep(Duration::from_secs(6));
                    assert!(!finished.load(Ordering::SeqCst));
                    assert_eq!(fs::read_dir(session.join("ready")).unwrap().count(), 1);
                }
                DescendantOutcome::Killed => {
                    fs::write(kill, b"").unwrap();
                    while !finished.load(Ordering::SeqCst) {
                        if Instant::now() >= deadline {
                            return Err("The abandoned constructor lease did not stop supervision.");
                        }
                        std::thread::sleep(Duration::from_millis(10));
                    }
                }
            }
            drop(lock);
            Ok(())
        });
        let result = run(
            &mut view,
            &artifact().unwrap(),
            &executable,
            &[
                root_started.into_os_string(),
                allow_child.into_os_string(),
                match outcome {
                    DescendantOutcome::Ready => OsString::new(),
                    DescendantOutcome::Killed => allow_kill.into_os_string(),
                },
                root_finished.clone().into_os_string(),
                killed.clone().into_os_string(),
            ],
        );
        record_outcome(
            match outcome {
                DescendantOutcome::Ready => Scenario::DescendantCacheContention,
                DescendantOutcome::Killed => Scenario::AbandonedDescendantInitialization,
            },
            &result,
        );
        supervisor_finished.store(true, Ordering::SeqCst);
        release.join().unwrap().unwrap();
        match outcome {
            DescendantOutcome::Ready => {
                assert_eq!(result.unwrap(), 0);
                assert_eq!(fs::read_dir(view.session.join("ready")).unwrap().count(), 2);
                assert_eq!(
                    fs::read_dir(view.session.join("pending")).unwrap().count(),
                    0
                );
            }
            DescendantOutcome::Killed => {
                let error = result.unwrap_err();
                assert_eq!(error.code, Code::PnportInjectionFailed);
                assert!(error
                    .message
                    .contains("exited during native initialization"));
                assert!(killed.is_file());
                assert!(!root_finished.exists());
            }
        }
    }
}
