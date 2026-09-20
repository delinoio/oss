mod ansi_path;

use std::{
    ffi::{CStr, c_char},
    io,
    os::windows::{io::AsRawHandle, process::ChildExt as _},
    path::Path,
    sync::Arc,
};

use fspy_detours_sys::{DetourCopyPayloadToProcess, DetourUpdateProcessWithDll};
use fspy_shared::{
    ipc::{PathAccess, channel::channel},
    windows::{PAYLOAD_ID, Payload},
};
use futures_util::FutureExt;
use materialized_artifact::{Artifact, artifact};
use tokio_util::sync::CancellationToken;
use winapi::{
    shared::minwindef::TRUE,
    um::{processthreadsapi::ResumeThread, winbase::CREATE_SUSPENDED},
};

use crate::{
    ChildTermination, TrackedChild, command::Command, error::SpawnError, ipc::ChannelAccesses,
};

const INTERPOSE_CDYLIB: Artifact =
    artifact!("fspy_preload", "CARGO_CDYLIB_FILE_FSPY_PRELOAD_WINDOWS");

pub struct PathAccessIterable {
    ipc_accesses: ChannelAccesses,
}

impl PathAccessIterable {
    pub fn incomplete(&self) -> bool { self.ipc_accesses.incomplete() }
    pub fn attached(&self) -> bool { self.iter().any(|a| a.mode.contains(fspy_shared::ipc::AccessMode::ATTACHED)) }
    pub fn iter(&self) -> impl Iterator<Item = PathAccess<'_>> {
        self.ipc_accesses.iter_path_accesses()
    }
}

// pub struct TracedProcess {
//     pub child: Child,
//     pub path_access_stream: PathAccessIter,
// }

#[derive(Debug, Clone)]
pub struct SpyImpl {
    ansi_dll_path_with_nul: Arc<CStr>,
}

impl SpyImpl {
    pub fn init_in(path: &Path) -> io::Result<Self> {
        let dll_path = INTERPOSE_CDYLIB.materialize().suffix(".dll").at(path)?;

        let ansi_dll_path_with_nul = ansi_path::encode_path(&dll_path)?;
        Ok(Self { ansi_dll_path_with_nul: ansi_dll_path_with_nul.into() })
    }

    pub(crate) fn spawn(
        &self,
        command: Command,
        directory: &Path,
        capacity: usize,
        max_paths: usize,
        cancellation_token: CancellationToken,
    ) -> std::future::Ready<Result<TrackedChild, SpawnError>> {
        std::future::ready(self.spawn_inner(command, directory, capacity, max_paths, cancellation_token))
    }

    fn spawn_inner(
        &self,
        command: Command,
        directory: &Path,
        capacity: usize,
        max_paths: usize,
        cancellation_token: CancellationToken,
    ) -> Result<TrackedChild, SpawnError> {
        let ansi_dll_path_with_nul = Arc::clone(&self.ansi_dll_path_with_nul);
        let mut command = command.into_tokio_command();

        command.creation_flags(CREATE_SUSPENDED | 0x00000200);
        command.kill_on_drop(true);
        let job = crate::lifecycle::Job::new().map_err(SpawnError::OsSpawn)?;

        let receiver = channel(directory, capacity, max_paths, allocator_api2::alloc::Global)
            .map_err(SpawnError::ChannelCreation)?;

        let mut spawn_success = false;
        let spawn_success = &mut spawn_success;
        let mut child = command
            .spawn_with(|std_command| {
                let mut std_child = std_command.spawn()?;
                if let Err(error) = job.assign(std_child.as_raw_handle()) { let _ = std_child.kill(); let _ = std_child.wait(); return Err(error); }
                *spawn_success = true;

                let mut dll_paths = ansi_dll_path_with_nul.as_ptr().cast::<c_char>();
                let process_handle = std_child.as_raw_handle().cast::<winapi::ctypes::c_void>();
                // SAFETY: process_handle is a valid handle to the just-spawned child process,
                // dll_paths points to a valid null-terminated ANSI string
                let success =
                    unsafe { DetourUpdateProcessWithDll(process_handle, &raw mut dll_paths, 1) };
                if success != TRUE {
                    let error = io::Error::last_os_error();
                    let _ = std_child.kill(); let _ = std_child.wait();
                    return Err(error);
                }

                let payload = Payload {
                    channel_conf: receiver.conf(),
                    ansi_dll_path_with_nul: ansi_dll_path_with_nul.to_bytes_with_nul(),
                };
                let payload_bytes = wincode::serialize(&payload).unwrap();
                // SAFETY: process_handle is valid, PAYLOAD_ID is a static GUID,
                // payload_bytes is a valid buffer with correct length
                let success = unsafe {
                    DetourCopyPayloadToProcess(
                                    &PAYLOAD_ID,
                        payload_bytes.as_ptr().cast(),
                        payload_bytes.len().try_into().unwrap(),
                    )
                };
                if success != TRUE {
                    let error = io::Error::last_os_error();
                    let _ = std_child.kill(); let _ = std_child.wait();
                    return Err(error);
                }

                let main_thread_handle = std_child.main_thread_handle();
                // SAFETY: main_thread_handle is a valid thread handle from the spawned child
                let resume_thread_ret =
                    unsafe { ResumeThread(main_thread_handle.as_raw_handle().cast()) }
                        .cast_signed();

                if resume_thread_ret == -1 {
                    let error = io::Error::last_os_error();
                    let _ = std_child.kill(); let _ = std_child.wait();
                    return Err(error);
                }

                Ok(std_child)
            })
            .map_err(|err| {
                if *spawn_success { SpawnError::OsSpawn(err) } else { SpawnError::Injection(err) }
            })?;

        // Job ownership was established before ResumeThread. Do not introduce
        // fallible setup here: the child can already have observable effects.
        // The wait task owns the original handle; no duplicate is needed.
        Ok(TrackedChild {
            stdin: child.stdin.take(),
            stdout: child.stdout.take(),
            stderr: child.stderr.take(),
            // Keep polling for the child to exit in the background even if `wait_handle` is not awaited,
            // because we need to stop the supervisor and close the channel as soon as the child exits.
            wait_handle: tokio::spawn(async move {
                let (status, lifecycle_incomplete) = job.wait(&mut child, cancellation_token).await?;
                // Close collection only after the complete owned job is reaped.
                let path_accesses = ChannelAccesses::try_from(receiver)
                    .map(|ipc_accesses| PathAccessIterable { ipc_accesses });

                io::Result::Ok(ChildTermination { status, path_accesses, lifecycle_incomplete })
            })
            .map(|f| f?) // flatten JoinError and io::Result
            .boxed(),
        })
    }
}
