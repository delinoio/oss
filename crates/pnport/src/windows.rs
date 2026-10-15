// SPDX-License-Identifier: Apache-2.0
//! Native Windows process ownership. A suspended root never executes before
//! both job assignment and injection succeed; job handles are not inherited.

use std::{
    collections::HashMap,
    ffi::{OsStr, OsString},
    fs,
    os::windows::{io::AsRawHandle, process::CommandExt},
    path::Path,
    process::Command,
    ptr,
    sync::atomic::{AtomicU32, Ordering},
    time::{Duration, Instant},
};

use pnport::{
    diagnostic::{Code, Error, Result},
    launch::{windows_entry_state, EntryState},
    view::View,
};
use pnport_core::windows::{Handle, LaunchContext};
use windows_sys::Win32::System::{Console::*, JobObjects::*, Threading::*};

static CANCELLED: AtomicU32 = AtomicU32::new(0);
unsafe extern "system" fn control(event: u32) -> i32 {
    if matches!(
        event,
        CTRL_C_EVENT
            | CTRL_BREAK_EVENT
            | CTRL_CLOSE_EVENT
            | CTRL_LOGOFF_EVENT
            | CTRL_SHUTDOWN_EVENT
    ) {
        CANCELLED.store(
            if event == CTRL_BREAK_EVENT { 131 } else { 130 },
            Ordering::SeqCst,
        );
        1
    } else {
        0
    }
}
struct Console;
impl Drop for Console {
    fn drop(&mut self) {
        unsafe {
            SetConsoleCtrlHandler(Some(control), 0);
        }
    }
}
fn failed(code: Code, message: &'static str) -> Error {
    Error::new(code, message)
}
fn injection() -> Error {
    failed(
        Code::PnportInjectionFailed,
        "Windows native injection or ownership preparation failed.",
    )
}
fn native_failure(stage: &'static str, error: std::io::Error) -> Error {
    tracing::debug!(action = "windows_admission_failed", stage, native_code = error.raw_os_error(), kind = ?error.kind(), "Windows admission failed");
    injection()
}

pub fn supported() -> bool {
    let mut version: winapi::um::winnt::RTL_OSVERSIONINFOW = unsafe { std::mem::zeroed() };
    version.dwOSVersionInfoSize = std::mem::size_of_val(&version) as u32;
    unsafe {
        ntapi::ntrtl::RtlGetVersion(&mut version) >= 0
            && version.dwMajorVersion == 10
            && version.dwBuildNumber >= 19045
    }
}

pub fn run(
    view: &mut View,
    artifact: &Path,
    prepared: &pnport::executable::Prepared,
    node_options: &OsStr,
) -> Result<i32> {
    let mut context = LaunchContext {
        format: 1,
        session: view.session.clone(),
        cache: view.cache.root.clone(),
        artifact: artifact.to_owned(),
        launch: format!("pnport-{}", uuid::Uuid::now_v7()),
        debug: tracing::enabled!(tracing::Level::DEBUG),
    };
    context.artifact = fs::canonicalize(&context.artifact).map_err(|_| injection())?;
    crate::windows_launch::dll_path(&context.artifact).map_err(|_| injection())?;
    for name in ["pending", "launch-starting", "ready", "active"] {
        pnport::cache::private_dir(&view.session.join(name))?;
    }
    let job = Handle(unsafe { CreateJobObjectW(ptr::null(), ptr::null()) });
    if job.0.is_null() {
        return Err(native_failure(
            "create_job",
            std::io::Error::last_os_error(),
        ));
    }
    let mut limits = JOBOBJECT_EXTENDED_LIMIT_INFORMATION::default();
    limits.BasicLimitInformation.LimitFlags = JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE;
    if unsafe {
        SetInformationJobObject(
            job.0,
            JobObjectExtendedLimitInformation,
            (&raw const limits).cast(),
            std::mem::size_of_val(&limits) as u32,
        )
    } == 0
    {
        return Err(native_failure(
            "configure_job",
            std::io::Error::last_os_error(),
        ));
    }
    CANCELLED.store(0, Ordering::SeqCst);
    if unsafe { SetConsoleCtrlHandler(Some(control), 1) } == 0 {
        let error = std::io::Error::last_os_error();
        // Detached tools and CI consumers can have inherited pipes without a
        // console. Job ownership still provides cancellation/crash containment.
        if error.raw_os_error() != Some(6) {
            return Err(native_failure("console_handler", error));
        }
    }
    let _console = Console;
    fs::write(
        view.session.join("pending").join(&context.launch),
        b"pnport launch\n",
    )
    .map_err(|_| injection())?;
    tracing::debug!(
        action = "windows_spawn_suspended",
        "Preparing the owned native process"
    );
    let mut child = Command::new(&prepared.program)
        .args(&prepared.args)
        .env("NODE_OPTIONS", node_options)
        .creation_flags(CREATE_SUSPENDED | CREATE_NEW_PROCESS_GROUP)
        .spawn()
        .map_err(|error| {
            failed(
                if error.kind() == std::io::ErrorKind::NotFound {
                    Code::PnportCommandNotFound
                } else {
                    Code::PnportCommandNotExecutable
                },
                "Windows could not start the executable.",
            )
        })?;
    let process = child.as_raw_handle();
    let admitted = (|| {
        if unsafe { AssignProcessToJobObject(job.0, process) } == 0 {
            return Err(native_failure(
                "assign_job",
                std::io::Error::last_os_error(),
            ));
        }
        crate::windows_launch::inject(process.cast(), &context)
            .map_err(|error| native_failure("inject", error))?;
        if unsafe { ntapi::ntpsapi::NtResumeProcess(process.cast()) } < 0 {
            return Err(injection());
        }
        Ok(())
    })();
    if let Err(error) = admitted {
        // Job assignment itself may have failed, so terminate the exact
        // retained root handle as well as closing the job. Never signal
        // a recycled PID.
        let _ = child.kill();
        let _ = child.wait();
        return Err(error);
    }
    tracing::debug!(
        action = "windows_tree_admitted",
        "Resumed injected process in the private job"
    );
    let mut watch = crate::input_watch::InputWatch::default();
    let mut active = std::collections::HashSet::new();
    let mut pending: HashMap<OsString, Instant> = HashMap::new();
    let ready = view.session.join("ready").join(&context.launch);
    let result = (|| loop {
        let cancelled = CANCELLED.load(Ordering::SeqCst);
        if cancelled != 0 {
            return Ok(cancelled as i32);
        }
        if let Some(error) = crate::supervisor::runtime_failure(&view.session)? {
            return Err(error);
        }
        view.graph.check_conflicts()?;
        for input in &view.graph.snapshot.inputs {
            watch.register(input)?;
        }
        for entry in fs::read_dir(view.session.join("active")).map_err(|_| injection())? {
            let entry = entry.map_err(|_| injection())?;
            if !entry.file_name().to_string_lossy().starts_with('.') && active.insert(entry.path())
            {
                let input =
                    serde_json::from_slice(&fs::read(entry.path()).map_err(|_| injection())?)
                        .map_err(|_| injection())?;
                watch.register(&input)?;
            }
        }
        watch.poll()?;
        let mut outstanding = false;
        for entry in fs::read_dir(view.session.join("pending")).map_err(|_| injection())? {
            let entry = entry.map_err(|_| injection())?;
            let since = pending
                .entry(entry.file_name())
                .or_insert_with(Instant::now);
            let state = match windows_entry_state(
                &entry.path(),
                &view.session.join("launch-starting").join(entry.file_name()),
            ) {
                Ok(state) => state,
                Err(_) if !entry.path().exists() => continue,
                Err(_) => return Err(injection()),
            };
            if state == EntryState::Abandoned
                || (state == EntryState::Missing && since.elapsed() >= Duration::from_secs(5))
            {
                return Err(injection());
            }
            outstanding = true;
        }
        if let Some(status) = child.try_wait().map_err(|_| injection())? {
            if let Some(error) = crate::supervisor::runtime_failure(&view.session)? {
                return Err(error);
            }
            if !ready.is_file() {
                return Err(injection());
            }
            if !outstanding {
                return Ok(status.code().unwrap_or(125));
            }
        }
        std::thread::sleep(Duration::from_millis(25));
    })();
    // Ctrl+Break addresses only this owned console group; no console is a
    // legitimate detached/noninteractive state. Job termination remains the
    // authoritative cleanup path after the grace interval.
    unsafe {
        GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, child.id());
    }
    let deadline = Instant::now() + Duration::from_secs(5);
    loop {
        let mut accounting = JOBOBJECT_BASIC_ACCOUNTING_INFORMATION::default();
        if unsafe {
            QueryInformationJobObject(
                job.0,
                JobObjectBasicAccountingInformation,
                (&raw mut accounting).cast(),
                std::mem::size_of_val(&accounting) as u32,
                ptr::null_mut(),
            )
        } == 0
        {
            return Err(failed(
                Code::PnportCleanupFailed,
                "Could not inspect the owned Windows job.",
            ));
        }
        if accounting.ActiveProcesses == 0 {
            break;
        }
        if Instant::now() >= deadline {
            if unsafe { TerminateJobObject(job.0, 125) } == 0 {
                return Err(failed(
                    Code::PnportCleanupFailed,
                    "Could not terminate the owned Windows job.",
                ));
            }
            break;
        }
        std::thread::sleep(Duration::from_millis(25));
    }
    child.wait().map_err(|_| {
        failed(
            Code::PnportCleanupFailed,
            "Could not reap the owned Windows root.",
        )
    })?;
    tracing::debug!(
        action = "windows_tree_stopped",
        "Stopped the owned Windows job"
    );
    result
}
