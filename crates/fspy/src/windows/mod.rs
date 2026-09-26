use std::{
    ffi::{CStr, c_char},
    io,
    os::windows::{
        ffi::OsStrExt,
        io::{AsRawHandle, BorrowedHandle},
    },
    path::Path,
    ptr,
    sync::Arc,
};

use fspy_detours_sys::{DetourCopyPayloadToProcess, DetourUpdateProcessWithDll};
use fspy_shared::{
    ipc::{AccessMode, IpcPath, PathAccess, channel::channel},
    windows::{PAYLOAD_ID, Payload},
};
use futures_util::FutureExt;
use materialized_artifact::{Artifact, artifact};
use ntapi::{ntpsapi::NtResumeProcess, ntrtl::RtlNtStatusToDosError};
use tokio_util::sync::CancellationToken;
use winapi::{
    shared::{minwindef::TRUE, ntdef::NT_SUCCESS},
    um::{
        fileapi::GetShortPathNameW,
        stringapiset::{MultiByteToWideChar, WideCharToMultiByte},
        winbase::CREATE_SUSPENDED,
        winnls::CP_ACP,
    },
};

use crate::{
    ChildTermination, TrackedChild, command::Command, error::SpawnError, ipc::ChannelAccesses,
};

const INTERPOSE_CDYLIB: Artifact =
    artifact!("fspy_preload", "CARGO_CDYLIB_FILE_FSPY_PRELOAD_WINDOWS");

pub struct PathAccessIterable {
    ipc_accesses: ChannelAccesses,
    resolution_accesses: Vec<Vec<u16>>,
}

impl PathAccessIterable {
    pub fn iter(&self) -> impl Iterator<Item = PathAccess<'_>> {
        self.ipc_accesses
            .iter_path_accesses()
            .chain(self.resolution_accesses.iter().map(|path| PathAccess {
                mode: AccessMode::READ,
                path: IpcPath::from_wide(path),
            }))
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
        let wide_dll_path = dll_path.as_os_str().encode_wide().collect::<Vec<u16>>();
        let ansi_dll_path_with_nul = match encode_ansi_path(&wide_dll_path)? {
            Some(path) => path,
            None => {
                // Detours accepts an ANSI DLL path even from its wide process
                // APIs. A short alias can name the same file without losing
                // Unicode characters under the process ANSI code page.
                let short_path = short_path(&dll_path)?;
                encode_ansi_path(&short_path)?.ok_or_else(|| {
                    io::Error::new(
                        io::ErrorKind::Unsupported,
                        "the preload DLL has no lossless ANSI or short path for Detours",
                    )
                })?
            }
        };
        Ok(Self {
            ansi_dll_path_with_nul,
        })
    }

    pub(crate) fn spawn(
        &self,
        command: Command,
        cancellation_token: CancellationToken,
    ) -> std::future::Ready<Result<TrackedChild, SpawnError>> {
        std::future::ready(self.spawn_inner(command, cancellation_token))
    }

    fn spawn_inner(
        &self,
        mut command: Command,
        cancellation_token: CancellationToken,
    ) -> Result<TrackedChild, SpawnError> {
        let ansi_dll_path_with_nul = &self.ansi_dll_path_with_nul;
        let resolution_accesses = command
            .resolution_accesses
            .iter()
            .map(|path| path.as_os_str().encode_wide().collect::<Vec<_>>())
            .collect::<Vec<_>>();
        command.env("FSPY", "1");

        let receiver = channel(crate::ipc::shm_capacity(), allocator_api2::alloc::Global)
            .map_err(SpawnError::ChannelCreation)?;

        let payload = Payload {
            channel_conf: receiver.conf(),
            ansi_dll_path_with_nul: ansi_dll_path_with_nul.to_bytes(),
        };
        let payload_bytes = wincode::serialize(&payload).unwrap();
        let payload_len = payload_bytes.len().try_into().unwrap();

        let mut command = command.into_tokio_command();
        command.creation_flags(CREATE_SUSPENDED);
        let mut child = command.spawn().map_err(SpawnError::OsSpawn)?;

        let preparation = (|| {
            // Duplicate the process handle before the child is moved into the background
            // task so it stays valid after Tokio closes its copy when the process exits.
            // SAFETY: the child owns this handle and is not waited on during this borrow.
            let process = unsafe { BorrowedHandle::borrow_raw(child.raw_handle().unwrap()) };
            let process_handle = process.try_clone_to_owned().map_err(SpawnError::OsSpawn)?;
            let raw_process = process_handle
                .as_raw_handle()
                .cast::<winapi::ctypes::c_void>();
            let mut dll_paths = ansi_dll_path_with_nul.as_ptr().cast::<c_char>();
            // SAFETY: raw_process is a valid handle to the suspended child process,
            // dll_paths points to a valid null-terminated ANSI string.
            let success = unsafe { DetourUpdateProcessWithDll(raw_process, &raw mut dll_paths, 1) };
            if success != TRUE {
                return Err(SpawnError::Injection(io::Error::last_os_error()));
            }

            // SAFETY: raw_process is valid, PAYLOAD_ID is a static GUID,
            // payload_bytes is a valid buffer with the correct length.
            let success = unsafe {
                DetourCopyPayloadToProcess(
                    raw_process,
                    &PAYLOAD_ID,
                    payload_bytes.as_ptr().cast(),
                    payload_len,
                )
            };
            if success != TRUE {
                return Err(SpawnError::Injection(io::Error::last_os_error()));
            }

            // Resume using the process handle, without the nightly main-thread handle API.
            // SAFETY: raw_process is a valid child process handle with
            // PROCESS_SUSPEND_RESUME access.
            let status = unsafe { NtResumeProcess(raw_process) };
            if !NT_SUCCESS(status) {
                // SAFETY: RtlNtStatusToDosError accepts any NTSTATUS value. Native APIs
                // return their status directly; GetLastError would report a stale error.
                let error = unsafe { RtlNtStatusToDosError(status) };
                return Err(SpawnError::Injection(io::Error::from_raw_os_error(
                    error.cast_signed(),
                )));
            }

            Ok(process_handle)
        })();

        let process_handle = preparation.inspect_err(|_| {
            // Do not leave a suspended process behind if tracking initialization fails.
            let _ = child.start_kill();
        })?;

        Ok(TrackedChild {
            root_pid: child
                .id()
                .ok_or_else(|| SpawnError::OsSpawn(io::Error::other("child_pid_unavailable")))?,
            stdin: child.stdin.take(),
            stdout: child.stdout.take(),
            stderr: child.stderr.take(),
            process_handle,
            // Keep polling for the child to exit in the background even if `wait_handle` is not
            // awaited, because we need to stop the supervisor and close the channel as
            // soon as the child exits.
            wait_handle: tokio::spawn(async move {
                let status = tokio::select! {
                    status = child.wait() => status?,
                    () = cancellation_token.cancelled() => {
                        child.start_kill()?;
                        child.wait().await?
                    }
                };
                // Close the ipc channel after the child has exited.
                // We are not interested in path accesses from descendants after the main child
                // has exited.
                let path_accesses =
                    ChannelAccesses::try_from(receiver).map(|ipc_accesses| PathAccessIterable {
                        ipc_accesses,
                        resolution_accesses,
                    });

                io::Result::Ok(ChildTermination {
                    status,
                    path_accesses,
                })
            })
            .map(|f| f?) // flatten JoinError and io::Result
            .boxed(),
        })
    }
}

fn encode_ansi_path(wide_path: &[u16]) -> io::Result<Option<Arc<CStr>>> {
    let wide_len = i32::try_from(wide_path.len())
        .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "preload DLL path is too long"))?;
    // SAFETY: the input slice is valid for `wide_len` code units. Null output
    // and default-character pointers request sizing without substitution
    // parameters, which also supports a UTF-8 active ANSI code page.
    let byte_len = unsafe {
        WideCharToMultiByte(
            CP_ACP,
            0,
            wide_path.as_ptr(),
            wide_len,
            ptr::null_mut(),
            0,
            ptr::null(),
            ptr::null_mut(),
        )
    };
    if byte_len == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut bytes = vec![0; usize::try_from(byte_len).expect("positive Windows length")];
    // SAFETY: `bytes` has exactly the size returned by the preceding call.
    let written = unsafe {
        WideCharToMultiByte(
            CP_ACP,
            0,
            wide_path.as_ptr(),
            wide_len,
            bytes.as_mut_ptr().cast(),
            byte_len,
            ptr::null(),
            ptr::null_mut(),
        )
    };
    if written != byte_len {
        return Err(io::Error::last_os_error());
    }
    // SAFETY: `bytes` is valid for `byte_len` bytes; a null output buffer
    // requests the number of UTF-16 code units needed for the round trip.
    let decoded_len = unsafe {
        MultiByteToWideChar(
            CP_ACP,
            0,
            bytes.as_ptr().cast(),
            byte_len,
            ptr::null_mut(),
            0,
        )
    };
    if decoded_len == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut decoded = vec![0; usize::try_from(decoded_len).expect("positive Windows length")];
    // SAFETY: `decoded` has the capacity returned by the preceding call.
    let decoded_written = unsafe {
        MultiByteToWideChar(
            CP_ACP,
            0,
            bytes.as_ptr().cast(),
            byte_len,
            decoded.as_mut_ptr(),
            decoded_len,
        )
    };
    if decoded_written != decoded_len {
        return Err(io::Error::last_os_error());
    }
    if decoded != wide_path {
        return Ok(None);
    }
    bytes.push(0);
    let path = CStr::from_bytes_with_nul(&bytes).map_err(|_| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "the preload DLL path contains NUL",
        )
    })?;
    Ok(Some(Arc::from(path)))
}

fn short_path(path: &Path) -> io::Result<Vec<u16>> {
    let mut wide = path.as_os_str().encode_wide().collect::<Vec<_>>();
    wide.push(0);
    // SAFETY: the path is NUL-terminated and GetShortPathNameW accepts a
    // null output pointer when querying the required buffer length.
    let needed = unsafe { GetShortPathNameW(wide.as_ptr(), ptr::null_mut(), 0) };
    if needed == 0 {
        return Err(io::Error::last_os_error());
    }
    let mut short = vec![0; usize::try_from(needed).expect("Windows path length fits usize")];
    // SAFETY: the input remains valid and the output buffer has `needed`
    // UTF-16 code units, including the terminator.
    let written = unsafe { GetShortPathNameW(wide.as_ptr(), short.as_mut_ptr(), needed) };
    if written == 0 {
        return Err(io::Error::last_os_error());
    }
    if written >= needed {
        return Err(io::Error::other(
            "the preload DLL short path changed during lookup",
        ));
    }
    short.truncate(usize::try_from(written).expect("Windows path length fits usize"));
    Ok(short)
}
