// SPDX-License-Identifier: Apache-2.0
//! Owned CreateProcess propagation with an injected payload independent of the
//! caller's replacement environment. Unsupported native creation never runs.

use ntapi::ntpsapi::{PPS_ATTRIBUTE_LIST, PPS_CREATE_INFO};
use winapi::{
    shared::minwindef::LPVOID,
    um::{
        errhandlingapi::{GetLastError, SetLastError},
        handleapi::CloseHandle,
        minwinbase::LPSECURITY_ATTRIBUTES,
        processenv::GetEnvironmentVariableW,
        processthreadsapi::*,
        stringapiset::MultiByteToWideChar,
        winbase::{
            CREATE_BREAKAWAY_FROM_JOB, CREATE_SUSPENDED, CREATE_UNICODE_ENVIRONMENT,
            STARTUPINFOEXA, STARTUPINFOEXW,
        },
        winnls::CP_ACP,
        winnt::{LPCSTR, LPCWSTR, LPSTR, LPWSTR},
    },
};

use super::*;

thread_local! { static CREATING: Cell<bool> = const { Cell::new(false) }; }
struct Creating;
impl Drop for Creating {
    fn drop(&mut self) {
        CREATING.with(|v| v.set(false));
    }
}

unsafe fn string(value: LPCWSTR) -> std::io::Result<Vec<u16>> {
    if value.is_null() {
        return Ok(Vec::new());
    }
    for length in 0..32768 {
        if *value.add(length) == 0 {
            return Ok(std::slice::from_raw_parts(value, length).to_vec());
        }
    }
    Err(std::io::ErrorKind::InvalidInput.into())
}
unsafe fn ansi(value: LPCSTR) -> std::io::Result<Vec<u16>> {
    if value.is_null() {
        return Ok(Vec::new());
    }
    let mut length = 0;
    while length < 32768 && *value.add(length) != 0 {
        length += 1;
    }
    if length >= 32768 {
        return Err(std::io::ErrorKind::InvalidInput.into());
    }
    if length == 0 {
        return Ok(Vec::new());
    }
    let required = MultiByteToWideChar(CP_ACP, 0, value, length as i32, ptr::null_mut(), 0);
    if required <= 0 {
        return Err(std::io::Error::last_os_error());
    }
    let mut buffer = vec![0; required as usize];
    if MultiByteToWideChar(
        CP_ACP,
        0,
        value,
        length as i32,
        buffer.as_mut_ptr(),
        required,
    ) != required
    {
        return Err(std::io::Error::last_os_error());
    }
    Ok(buffer)
}

fn reserved(entry: &[u16], key: &str) -> bool {
    let key: Vec<_> = key.encode_utf16().collect();
    entry.len() > key.len()
        && entry[key.len()] == b'=' as u16
        && entry[..key.len()].iter().zip(key).all(|(a, b)| unsafe {
            ntapi::ntrtl::RtlUpcaseUnicodeChar(*a) == ntapi::ntrtl::RtlUpcaseUnicodeChar(b)
        })
}

unsafe fn environment(
    input: LPVOID,
    flags: DWORD,
    loader: &pnport_core::node::Loader,
    cwd: &Path,
) -> std::io::Result<Vec<u16>> {
    let mut entries = Vec::new();
    if input.is_null() {
        for (key, value) in std::env::vars_os() {
            let mut entry: Vec<_> = key.encode_wide().collect();
            entry.push(61);
            entry.extend(value.encode_wide());
            entries.push(entry);
        }
    } else {
        let mut offset = 0;
        while offset < 32768 * 32 {
            let value = if flags & CREATE_UNICODE_ENVIRONMENT != 0 {
                string(input.cast::<u16>().add(offset))?
            } else {
                ansi(input.cast::<i8>().add(offset))?
            };
            if value.is_empty() {
                break;
            }
            if flags & CREATE_UNICODE_ENVIRONMENT != 0 {
                offset += value.len() + 1;
            } else {
                let mut bytes = 0;
                while *input.cast::<u8>().add(offset + bytes) != 0 {
                    bytes += 1;
                    if bytes > 32768 {
                        return Err(std::io::ErrorKind::InvalidInput.into());
                    }
                }
                offset += bytes + 1;
            }
            entries.push(value);
        }
        if offset >= 32768 * 32 {
            return Err(std::io::ErrorKind::InvalidInput.into());
        }
    }
    let inherited = entries
        .iter()
        .find(|entry| reserved(entry, "NODE_OPTIONS"))
        .map(|entry| OsString::from_wide(&entry[13..]));
    let options = loader
        .options_in(inherited.as_deref(), Some(cwd))
        .map_err(|_| std::io::ErrorKind::InvalidData)?;
    entries.retain(|entry| !reserved(entry, "NODE_OPTIONS"));
    let mut option: Vec<_> = "NODE_OPTIONS=".encode_utf16().collect();
    option.extend(options.encode_wide());
    entries.push(option);
    entries.sort_by(|a, b| {
        a.iter()
            .map(|unit| ntapi::ntrtl::RtlUpcaseUnicodeChar(*unit))
            .cmp(
                b.iter()
                    .map(|unit| ntapi::ntrtl::RtlUpcaseUnicodeChar(*unit)),
            )
    });
    let mut buffer = Vec::new();
    for entry in entries {
        buffer.extend(entry);
        buffer.push(0);
    }
    buffer.push(0);
    Ok(buffer)
}

/// Read the caller's remembered directory for `drive` (the hidden `=X:`
/// environment record) through the native API. Portable environment accessors
/// may reject names that start with `=`, so this must not use them.
fn drive_directory(drive: u16) -> Option<Vec<u16>> {
    let name = pnport_core::application::drive_directory_variable(drive);
    pnport_core::application::read_variable(|buffer| unsafe {
        GetEnvironmentVariableW(name.as_ptr(), buffer.as_mut_ptr(), buffer.len() as u32)
    })
}

unsafe fn discard(info: LPPROCESS_INFORMATION) {
    let error = GetLastError();
    TerminateProcess((*info).hProcess, 125);
    CloseHandle((*info).hThread);
    CloseHandle((*info).hProcess);
    ptr::write(info, std::mem::zeroed());
    SetLastError(error);
}

#[allow(
    clippy::too_many_arguments,
    reason = "Retain the native CreateProcess parameter contract"
)]
unsafe fn create(
    application: LPCWSTR,
    command: LPWSTR,
    process_attrs: LPSECURITY_ATTRIBUTES,
    thread_attrs: LPSECURITY_ATTRIBUTES,
    inherit: BOOL,
    flags: DWORD,
    env: LPVOID,
    cwd: LPCWSTR,
    startup: LPSTARTUPINFOW,
    info: LPPROCESS_INFORMATION,
) -> BOOL {
    let Some(_guard) = Guard::enter() else {
        return (CREATE_W.real())(
            application,
            command,
            process_attrs,
            thread_attrs,
            inherit,
            flags,
            env,
            cwd,
            startup,
            info,
        );
    };
    if flags & (CREATE_BREAKAWAY_FROM_JOB | 0x00040000) != 0 || info.is_null() {
        SetLastError(50);
        return 0;
    }
    let prepared = (|| -> std::io::Result<_> {
        let runtime = RUNTIME
            .get()
            .ok_or(std::io::ErrorKind::Other)?
            .lock()
            .map_err(|_| std::io::ErrorKind::Other)?;
        if inherit != 0 {
            // Cross-process managed directory cursors and peer provenance do
            // not yet have a shared native representation. Refuse transfer
            // before birth instead of silently exposing physical cache names.
            for (handle, file) in &runtime.handles {
                if file.translation.readonly
                    || file.translation.logical != file.translation.physical
                {
                    let mut attributes = 0;
                    if winapi::um::handleapi::GetHandleInformation(
                        *handle as HANDLE,
                        &mut attributes,
                    ) != 0
                        && attributes & 1 != 0
                    {
                        tracing::debug!(
                            action = "windows_child_rejected",
                            reason = "managed_handle_inheritance",
                            "Rejected unsupported inherited managed handles"
                        );
                        return Err(std::io::ErrorKind::Unsupported.into());
                    }
                }
            }
        }
        let mut context = runtime.context.clone();
        context.launch = format!("pnport-{}", uuid::Uuid::now_v7());
        let loader = pnport_core::node::Loader::from_snapshot(&runtime.view.graph.snapshot);
        drop(runtime);
        let cwd_value = string(cwd)?;
        // The requested child directory only seeds the child's environment
        // and loader options. Native process creation resolves a partial
        // application path against the caller's own directory state.
        let caller = std::env::current_dir()?;
        let cwd = if cwd_value.is_empty() {
            caller.clone()
        } else {
            PathBuf::from(OsString::from_wide(&cwd_value))
        };
        let environment = environment(env, flags, &loader, &cwd)?;
        let image = string(application)?;
        let image = if image.is_empty() {
            None
        } else {
            let caller_wide: Vec<u16> = caller.as_os_str().encode_wide().collect();
            let resolved = pnport_core::application::resolve_application(
                &image,
                &caller_wide,
                drive_directory,
            )
            .ok_or(std::io::ErrorKind::Unsupported)?;
            let path = PathBuf::from(OsString::from_wide(&resolved));
            tracing::debug!(
                action = "windows_application_resolved",
                requested = %String::from_utf16_lossy(&image),
                caller = %caller.display(),
                resolved = %path.display(),
                "Resolved the application against the caller directory"
            );
            let translation =
                translate(&path, false).map_err(|_| std::io::ErrorKind::Unsupported)?;
            let mut wide: Vec<_> = translation.physical.as_os_str().encode_wide().collect();
            wide.push(0);
            Some(wide)
        };
        fs::write(
            context.session.join("pending").join(&context.launch),
            b"pnport launch\n",
        )?;
        Ok((context, environment, image))
    })();
    let (context, mut environment, image) = match prepared {
        Ok(value) => value,
        Err(_) => {
            SetLastError(50);
            return 0;
        }
    };
    CREATING.with(|value| value.set(true));
    let creating = Creating;
    let created = (CREATE_W.real())(
        image.as_ref().map_or(application, |value| value.as_ptr()),
        command,
        process_attrs,
        thread_attrs,
        inherit,
        flags | CREATE_SUSPENDED | CREATE_UNICODE_ENVIRONMENT,
        environment.as_mut_ptr().cast(),
        cwd,
        startup,
        info,
    );
    drop(creating);
    if created == 0 {
        let error = GetLastError();
        let _ = fs::remove_file(context.session.join("pending").join(&context.launch));
        SetLastError(error);
        return 0;
    }
    if crate::windows_launch::inject((*info).hProcess, &context).is_err() {
        publish_failure(&context.session, Code::PnportInjectionFailed);
        SetLastError(50);
        discard(info);
        let _ = fs::remove_file(context.session.join("pending").join(&context.launch));
        return 0;
    }
    if flags & CREATE_SUSPENDED == 0 && ResumeThread((*info).hThread) == u32::MAX {
        discard(info);
        return 0;
    }
    1
}

hook!(CREATE_W, winapi::um::processthreadsapi::CreateProcessW, c"CreateProcessW", (application: LPCWSTR, command: LPWSTR, process_attrs: LPSECURITY_ATTRIBUTES, thread_attrs: LPSECURITY_ATTRIBUTES, inherit: BOOL, flags: DWORD, env: LPVOID, cwd: LPCWSTR, startup: LPSTARTUPINFOW, info: LPPROCESS_INFORMATION) -> BOOL, {
    create(application,command,process_attrs,thread_attrs,inherit,flags,env,cwd,startup,info)
});
hook!(CREATE_A, winapi::um::processthreadsapi::CreateProcessA, c"CreateProcessA", (application: LPCSTR, command: LPSTR, process_attrs: LPSECURITY_ATTRIBUTES, thread_attrs: LPSECURITY_ATTRIBUTES, inherit: BOOL, flags: DWORD, env: LPVOID, cwd: LPCSTR, startup: LPSTARTUPINFOA, info: LPPROCESS_INFORMATION) -> BOOL, {
    if ENTERED.with(Cell::get) { return (CREATE_A.real())(application,command,process_attrs,thread_attrs,inherit,flags,env,cwd,startup,info); }
    // STARTUPINFOEX carries an opaque attribute list whose handle inheritance
    // must remain native; A/W structures have identical layout except strings.
    if startup.is_null() { SetLastError(87); return 0; }
    let convert = |value| ansi(value).map(|mut value| { value.push(0); value });
    let result = (|| -> std::io::Result<_> {
        Ok((convert(application)?,convert(command)?,convert(cwd)?,convert((*startup).lpDesktop)?,convert((*startup).lpTitle)?,convert((*startup).lpReserved)?))
    })();
    let (app,mut cmd,dir,mut desktop,mut title,mut reserved) = match result { Ok(value) => value, Err(_) => { SetLastError(87); return 0; } };
    let mut wide: STARTUPINFOEXW = std::mem::zeroed();
    ptr::copy_nonoverlapping(startup.cast::<u8>(),(&raw mut wide.StartupInfo).cast::<u8>(),std::mem::size_of::<STARTUPINFOW>());
    if flags & 0x00080000 != 0 { wide.lpAttributeList = (*startup.cast::<STARTUPINFOEXA>()).lpAttributeList; }
    wide.StartupInfo.lpDesktop = if (*startup).lpDesktop.is_null() { ptr::null_mut() } else { desktop.as_mut_ptr() };
    wide.StartupInfo.lpTitle = if (*startup).lpTitle.is_null() { ptr::null_mut() } else { title.as_mut_ptr() };
    wide.StartupInfo.lpReserved = if (*startup).lpReserved.is_null() { ptr::null_mut() } else { reserved.as_mut_ptr() };
    create(if application.is_null() { ptr::null() } else { app.as_ptr() },if command.is_null() { ptr::null_mut() } else { cmd.as_mut_ptr() },process_attrs,thread_attrs,inherit,flags,env,if cwd.is_null() { ptr::null() } else { dir.as_ptr() },&mut wide.StartupInfo,info)
});
hook!(CREATE_NATIVE, ntapi::ntpsapi::NtCreateUserProcess, c"NtCreateUserProcess", (process: PHANDLE, thread: PHANDLE, process_access: ACCESS_MASK, thread_access: ACCESS_MASK, process_attrs: POBJECT_ATTRIBUTES, thread_attrs: POBJECT_ATTRIBUTES, process_flags: ULONG, thread_flags: ULONG, parameters: PVOID, create_info: PPS_CREATE_INFO, attributes: PPS_ATTRIBUTE_LIST) -> NTSTATUS, {
    if !CREATING.with(Cell::get) { return UNSUPPORTED; }
    (CREATE_NATIVE.real())(process,thread,process_access,thread_access,process_attrs,thread_attrs,process_flags,thread_flags,parameters,create_info,attributes)
});

pub(super) const DETOURS: &[DetourAny] =
    &[CREATE_W.as_any(), CREATE_A.as_any(), CREATE_NATIVE.as_any()];
