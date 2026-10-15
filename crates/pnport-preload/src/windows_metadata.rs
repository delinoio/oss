// SPDX-License-Identifier: Apache-2.0
#![allow(
    non_upper_case_globals,
    reason = "NT API constants retain their upstream names"
)]
//! Preserve logical names when native resolvers canonicalize translated
//! handles.

use winapi::um::errhandlingapi::SetLastError;

use super::*;

fn volume_relative(path: &Path) -> Vec<u16> {
    let mut value = PathBuf::from(r"\");
    for component in path.components() {
        if let std::path::Component::Normal(name) = component {
            value.push(name);
        }
    }
    value.as_os_str().encode_wide().collect()
}

hook!(INFO, ntapi::ntioapi::NtQueryInformationFile, c"NtQueryInformationFile", (file: HANDLE, ios: PIO_STATUS_BLOCK, buffer: PVOID, length: ULONG, class: FILE_INFORMATION_CLASS) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (INFO.real())(file,ios,buffer,length,class); };
    let Some(owned) = handle(file) else { return (INFO.real())(file,ios,buffer,length,class); };
    if owned.translation.logical == owned.translation.physical || !matches!(class, FileNameInformation | FileNormalizedNameInformation | FileAllInformation) { return (INFO.real())(file,ios,buffer,length,class); }
    let offset = if class == FileAllInformation { 96 } else { 0 };
    if ios.is_null() || buffer.is_null() || (length as usize) < offset + 4 { return INVALID; }
    if class == FileAllInformation {
        let status = (INFO.real())(file,ios,buffer,length,class);
        if status < 0 && status != 0x80000005u32 as i32 { return status; }
    }
    let name = volume_relative(&owned.translation.logical);
    let bytes = std::slice::from_raw_parts_mut(buffer.cast::<u8>(),length as usize);
    bytes[offset..offset+4].copy_from_slice(&((name.len()*2) as u32).to_le_bytes());
    let capacity = (bytes.len()-offset-4)/2;
    let count = name.len().min(capacity);
    for (i,unit) in name[..count].iter().enumerate() { bytes[offset+4+i*2..offset+6+i*2].copy_from_slice(&unit.to_le_bytes()); }
    (*ios).Information = offset + 4 + count * 2;
    let status = if count == name.len() { 0 } else { 0x80000005u32 as i32 };
    (*ios).u.Status = status;
    status
});

hook!(FINAL_W, winapi::um::fileapi::GetFinalPathNameByHandleW, c"GetFinalPathNameByHandleW", (file: HANDLE, buffer: *mut u16, capacity: DWORD, flags: DWORD) -> DWORD, {
    let Some(_guard) = Guard::enter() else { return (FINAL_W.real())(file,buffer,capacity,flags); };
    let Some(owned) = handle(file) else { return (FINAL_W.real())(file,buffer,capacity,flags); };
    if owned.translation.logical == owned.translation.physical { return (FINAL_W.real())(file,buffer,capacity,flags); }
    if flags & !12 != 0 { tracing::debug!(action="windows_final_path_rejected",flags,"Rejected an unsupported final-path volume mode"); SetLastError(50); return 0; }
    let value = if flags & 4 != 0 { volume_relative(&owned.translation.logical) } else {
        let name: Vec<_> = owned.translation.logical.as_os_str().encode_wide().collect();
        let mut value: Vec<_> = r"\\?\".encode_utf16().collect();
        if name.starts_with(&[92,92]) { value.extend("UNC\\".encode_utf16()); value.extend(&name[2..]); } else { value.extend(name); }
        value
    };
    if capacity as usize <= value.len() { return (value.len()+1) as u32; }
    if buffer.is_null() { SetLastError(87); return 0; }
    ptr::copy_nonoverlapping(value.as_ptr(),buffer,value.len()); *buffer.add(value.len()) = 0;
    value.len() as u32
});

hook!(SET_EA, ntapi::ntioapi::NtSetEaFile, c"NtSetEaFile", (file: HANDLE, ios: PIO_STATUS_BLOCK, buffer: PVOID, length: ULONG) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (SET_EA.real())(file,ios,buffer,length); };
    if !writable(file) { return DENIED; }
    (SET_EA.real())(file,ios,buffer,length)
});
hook!(SET_SECURITY, ntapi::ntobapi::NtSetSecurityObject, c"NtSetSecurityObject", (file: HANDLE, information: winapi::um::winnt::SECURITY_INFORMATION, descriptor: winapi::um::winnt::PSECURITY_DESCRIPTOR) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (SET_SECURITY.real())(file,information,descriptor); };
    if !writable(file) { return DENIED; }
    (SET_SECURITY.real())(file,information,descriptor)
});
hook!(FSCTL, ntapi::ntioapi::NtFsControlFile, c"NtFsControlFile", (file: HANDLE, event: HANDLE, apc: PIO_APC_ROUTINE, context: PVOID, ios: PIO_STATUS_BLOCK, code: ULONG, input: PVOID, input_size: ULONG, output: PVOID, output_size: ULONG) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (FSCTL.real())(file,event,apc,context,ios,code,input,input_size,output,output_size); };
    // Read-only queries retain native semantics. Unknown controls on managed
    // backing may mutate it even when their encoded access mask is FILE_ANY_ACCESS.
    if !writable(file) && !matches!(code, 0x000900a8 | 0x0009003c | 0x000900eb | 0x000940cf) { return DENIED; }
    (FSCTL.real())(file,event,apc,context,ios,code,input,input_size,output,output_size)
});

// Resolve this newer entry point dynamically so Windows 10 can load the DLL
// without importing a Windows 11-only ntdll export.
static INFO_BY_NAME: Detour<
    unsafe extern "system" fn(
        POBJECT_ATTRIBUTES,
        PIO_STATUS_BLOCK,
        PVOID,
        ULONG,
        FILE_INFORMATION_CLASS,
    ) -> NTSTATUS,
> = unsafe {
    Detour::dynamic(c"NtQueryInformationByName", {
        unsafe extern "system" fn replacement(
            attrs: POBJECT_ATTRIBUTES,
            ios: PIO_STATUS_BLOCK,
            buffer: PVOID,
            length: ULONG,
            class: FILE_INFORMATION_CLASS,
        ) -> NTSTATUS {
            let Some(_guard) = Guard::enter() else {
                return (INFO_BY_NAME.real())(attrs, ios, buffer, length, class);
            };
            mapped(attrs, false, |attrs, _| {
                (INFO_BY_NAME.real())(attrs, ios, buffer, length, class)
            })
        }
        replacement
    })
};

pub(super) const DETOURS: &[DetourAny] = &[
    INFO_BY_NAME.as_any(),
    INFO.as_any(),
    FINAL_W.as_any(),
    SET_EA.as_any(),
    SET_SECURITY.as_any(),
    FSCTL.as_any(),
];
