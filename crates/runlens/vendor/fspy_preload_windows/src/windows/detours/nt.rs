use std::mem::{offset_of, size_of};

use fspy_shared::ipc::{AccessMode, IpcPath, PathAccess};
use ntapi::{
    ntioapi::{
        FILE_INFORMATION_CLASS, NtQueryDirectoryFile, NtQueryFullAttributesFile,
        NtQueryInformationByName, PFILE_BASIC_INFORMATION, PFILE_NETWORK_OPEN_INFORMATION,
        PIO_APC_ROUTINE, PIO_STATUS_BLOCK,
    },
    ntpsapi::{
        NtCreateUserProcess, PPS_ATTRIBUTE_LIST, PPS_CREATE_INFO, PS_ATTRIBUTE,
        PS_ATTRIBUTE_IMAGE_NAME, PS_ATTRIBUTE_LIST,
    },
};
use winapi::{
    shared::{
        minwindef::HFILE,
        ntdef::{
            BOOLEAN, HANDLE, NTSTATUS, PHANDLE, PLARGE_INTEGER, POBJECT_ATTRIBUTES,
            PUNICODE_STRING, PVOID, ULONG,
        },
    },
    um::winnt::{ACCESS_MASK, GENERIC_READ},
};

use crate::windows::{
    client::global_client,
    convert::{ToAbsolutePath, ToAccessMode},
    detour::{Detour, DetourAny},
};

// CreateProcess ultimately asks NtCreateUserProcess to open the executable image. Some Windows
// versions perform that open entirely inside the syscall, so it never reaches the NtCreateFile and
// query functions hooked below. PS_ATTRIBUTE_IMAGE_NAME is the kernel-facing path that identifies
// the image to open; RTL_USER_PROCESS_PARAMETERS.ImagePathName is only metadata for the child PEB
// and can intentionally name a different file.
//
// Record the image before forwarding the syscall, matching the attempted-access semantics of the
// other NT hooks in this module. This is important for missing executables: the failed lookup is
// still an input access even though NtCreateUserProcess returns an error.
static DETOUR_NT_CREATE_USER_PROCESS: Detour<
    unsafe extern "system" fn(
        process_handle: PHANDLE,
        thread_handle: PHANDLE,
        process_desired_access: ACCESS_MASK,
        thread_desired_access: ACCESS_MASK,
        process_object_attributes: POBJECT_ATTRIBUTES,
        thread_object_attributes: POBJECT_ATTRIBUTES,
        process_flags: ULONG,
        thread_flags: ULONG,
        process_parameters: PVOID,
        create_info: PPS_CREATE_INFO,
        attribute_list: PPS_ATTRIBUTE_LIST,
    ) -> NTSTATUS,
> =
    // SAFETY: initializing Detour with the real NtCreateUserProcess function pointer
    unsafe {
        Detour::new(c"NtCreateUserProcess", NtCreateUserProcess, {
            unsafe extern "system" fn new_fn(
                process_handle: PHANDLE,
                thread_handle: PHANDLE,
                process_desired_access: ACCESS_MASK,
                thread_desired_access: ACCESS_MASK,
                process_object_attributes: POBJECT_ATTRIBUTES,
                thread_object_attributes: POBJECT_ATTRIBUTES,
                process_flags: ULONG,
                thread_flags: ULONG,
                process_parameters: PVOID,
                create_info: PPS_CREATE_INFO,
                attribute_list: PPS_ATTRIBUTE_LIST,
            ) -> NTSTATUS {
                // Direct native creation bypasses suspended injection in the
                // CreateProcess wrappers. The child must still run unchanged,
                // but its unobserved accesses cannot certify complete collection.
                if !super::create_process::injection_owned() {
                    crate::windows::client::report_global_failure();
                }
                // SAFETY: observing caller memory without changing the forwarded arguments
                unsafe { handle_process_image(attribute_list) };

                // SAFETY: calling the original NtCreateUserProcess with all original arguments
                unsafe {
                    (DETOUR_NT_CREATE_USER_PROCESS.real())(
                        process_handle,
                        thread_handle,
                        process_desired_access,
                        thread_desired_access,
                        process_object_attributes,
                        thread_object_attributes,
                        process_flags,
                        thread_flags,
                        process_parameters,
                        create_info,
                        attribute_list,
                    )
                }
            }
            new_fn
        })
    };

unsafe fn handle_process_image(attribute_list: PPS_ATTRIBUTE_LIST) {
    match read_process_image_attribute(attribute_list) {
        Ok(image_path) => {
            // SAFETY: the client is initialized before hooks are installed.
            unsafe { global_client() }.send(PathAccess {
                mode: AccessMode::READ, path: IpcPath::from_wide(&image_path),
            });
        }
        Err(()) => crate::windows::client::report_global_failure(),
    }
}

// The intercepted NT call may intentionally contain invalid user pointers. Do
// not form Rust references or slices into that memory before the kernel checks
// it. Copy bounded bytes through ReadProcessMemory, which fails on unreadable
// pages, then parse only owned storage. The original syscall is always forwarded.
fn read_process_image_attribute(attribute_list: PPS_ATTRIBUTE_LIST) -> Result<Vec<u16>, ()> {
    const WORD: usize = size_of::<usize>();
    const MAX_ATTRIBUTES: usize = 64;
    const MAX_IMAGE_BYTES: usize = 65534;
    const _: () = assert!(size_of::<PS_ATTRIBUTE>() == 4 * WORD);
    let address = attribute_list as usize;
    let offset = offset_of!(PS_ATTRIBUTE_LIST, Attributes);
    if address == 0 || address % std::mem::align_of::<PS_ATTRIBUTE>() != 0 { return Err(()); }
    let header = copy_process_bytes(address, WORD)?;
    let total = usize::from_ne_bytes(header.try_into().map_err(|_| ())?);
    let bytes = total.checked_sub(offset).ok_or(())?;
    if bytes == 0 || bytes % size_of::<PS_ATTRIBUTE>() != 0
        || bytes / size_of::<PS_ATTRIBUTE>() > MAX_ATTRIBUTES { return Err(()); }
    address.checked_add(total).ok_or(())?;
    let owned = copy_process_bytes(address, total)?;
    if usize::from_ne_bytes(owned[..WORD].try_into().map_err(|_| ())?) != total { return Err(()); }
    let mut image = None;
    for attribute in owned[offset..].chunks_exact(size_of::<PS_ATTRIBUTE>()) {
        let word = |index: usize| usize::from_ne_bytes(attribute[index * WORD..(index + 1) * WORD].try_into().unwrap());
        if word(0) != PS_ATTRIBUTE_IMAGE_NAME { continue; }
        let size = word(1);
        let pointer = word(2);
        if image.is_some() || size == 0 || size > MAX_IMAGE_BYTES || size % 2 != 0
            || pointer == 0 || pointer % std::mem::align_of::<u16>() != 0 { return Err(()); }
        pointer.checked_add(size).ok_or(())?;
        let bytes = copy_process_bytes(pointer, size)?;
        image = Some(bytes.chunks_exact(2).map(|pair| u16::from_ne_bytes([pair[0], pair[1]])).collect());
    }
    image.ok_or(())
}

fn copy_process_bytes(address: usize, size: usize) -> Result<Vec<u8>, ()> {
    use winapi::um::{memoryapi::ReadProcessMemory, processthreadsapi::GetCurrentProcess};
    let mut bytes = vec![0u8; size];
    let mut copied = 0;
    // SAFETY: the destination is initialized owned storage of exactly size
    // bytes. The kernel validates the untrusted source address without Rust
    // dereferencing it. The pseudo process handle requires no close.
    let result = unsafe { ReadProcessMemory(GetCurrentProcess(), address as *const _,
        bytes.as_mut_ptr().cast(), size, &mut copied) };
    if result == 0 || copied != size { return Err(()); }
    Ok(bytes)
}

#[cfg(test)]
mod process_attribute_tests {
    use super::*;

    #[test]
    fn process_image_attributes_reject_malformed_memory_without_dereferencing() {
        assert!(read_process_image_attribute(std::ptr::null_mut()).is_err());
        assert!(read_process_image_attribute(1usize as PPS_ATTRIBUTE_LIST).is_err());
        // An aligned but inaccessible user address reaches ReadProcessMemory.
        assert!(read_process_image_attribute(16usize as PPS_ATTRIBUTE_LIST).is_err());
        let path = [b'C' as u16, b':' as u16, b'/' as u16, b'x' as u16];
        let total = offset_of!(PS_ATTRIBUTE_LIST, Attributes) + size_of::<PS_ATTRIBUTE>();
        let valid = [total, PS_ATTRIBUTE_IMAGE_NAME, path.len() * 2, path.as_ptr() as usize, 0];
        let read = |words: &[usize]| read_process_image_attribute(words.as_ptr() as PPS_ATTRIBUTE_LIST);
        assert_eq!(read(&valid).unwrap(), path);
        for length in [0, 1, total - 1, usize::MAX, total + 64 * size_of::<PS_ATTRIBUTE>()] {
            let mut words = valid;
            words[0] = length;
            assert!(read(&words).is_err(), "length={length}");
        }
        for size in [0, 1, 65536, usize::MAX] {
            let mut words = valid;
            words[2] = size;
            assert!(read(&words).is_err(), "image size={size}");
        }
        for pointer in [0, 1, 16, usize::MAX - 1] {
            let mut words = valid;
            words[3] = pointer;
            assert!(read(&words).is_err(), "image pointer={pointer}");
        }
    }
}


static DETOUR_NT_CREATE_FILE: Detour<
    unsafe extern "system" fn(
        file_handle: PHANDLE,
        desired_access: ACCESS_MASK,
        object_attributes: POBJECT_ATTRIBUTES,
        io_status_block: PIO_STATUS_BLOCK,
        allocation_size: PLARGE_INTEGER,
        file_attributes: ULONG,
        share_access: ULONG,
        create_disposition: ULONG,
        create_options: ULONG,
        ea_buffer: PVOID,
        ea_length: ULONG,
    ) -> HFILE,
> =
    // SAFETY: initializing Detour with the real NtCreateFile function pointer and our replacement
    unsafe {
        Detour::new(c"NtCreateFile", ntapi::ntioapi::NtCreateFile, {
            unsafe extern "system" fn new_nt_create_file(
                file_handle: PHANDLE,
                desired_access: ACCESS_MASK,
                object_attributes: POBJECT_ATTRIBUTES,
                io_status_block: PIO_STATUS_BLOCK,
                allocation_size: PLARGE_INTEGER,
                file_attributes: ULONG,
                share_access: ULONG,
                create_disposition: ULONG,
                create_options: ULONG,
                ea_buffer: PVOID,
                ea_length: ULONG,
            ) -> HFILE {
                // SAFETY: intercepting file open to record access before forwarding to real function
                unsafe {
                    handle_open(
                        fspy_shared::windows_access::creation_mode(
                            crate::windows::winapi_utils::access_mask_to_mode(desired_access),
                            create_disposition,
                            create_options,
                        ),
                        object_attributes,
                    )
                };

                // SAFETY: calling the original NtCreateFile with all original arguments
                unsafe {
                    (DETOUR_NT_CREATE_FILE.real())(
                        file_handle,
                        desired_access,
                        object_attributes,
                        io_status_block,
                        allocation_size,
                        file_attributes,
                        share_access,
                        create_disposition,
                        create_options,
                        ea_buffer,
                        ea_length,
                    )
                }
            }
            new_nt_create_file
        })
    };

static DETOUR_NT_OPEN_FILE: Detour<
    unsafe extern "system" fn(
        file_handle: PHANDLE,
        desired_access: ACCESS_MASK,
        object_attributes: POBJECT_ATTRIBUTES,
        io_status_block: PIO_STATUS_BLOCK,
        share_access: ULONG,
        open_options: ULONG,
    ) -> HFILE,
> =
    // SAFETY: initializing Detour with the real NtOpenFile function pointer and our replacement
    unsafe {
        Detour::new(c"NtOpenFile", ntapi::ntioapi::NtOpenFile, {
            unsafe extern "system" fn new_nt_open_file(
                file_handle: PHANDLE,
                desired_access: ACCESS_MASK,
                object_attributes: POBJECT_ATTRIBUTES,
                io_status_block: PIO_STATUS_BLOCK,
                share_access: ULONG,
                open_options: ULONG,
            ) -> HFILE {
                // SAFETY: intercepting file open to record access before forwarding to real function
                unsafe {
                    handle_open(
                        fspy_shared::windows_access::creation_mode(
                            crate::windows::winapi_utils::access_mask_to_mode(desired_access),
                            1, // NtOpenFile has FILE_OPEN semantics.
                            open_options,
                        ),
                        object_attributes,
                    );
                }

                // SAFETY: calling the original NtOpenFile with all original arguments
                unsafe {
                    (DETOUR_NT_OPEN_FILE.real())(
                        file_handle,
                        desired_access,
                        object_attributes,
                        io_status_block,
                        share_access,
                        open_options,
                    )
                }
            }
            new_nt_open_file
        })
    };

static DETOUR_NT_QUERY_ATTRIBUTES_FILE: Detour<
    unsafe extern "system" fn(
        object_attributes: POBJECT_ATTRIBUTES,
        file_information: PFILE_BASIC_INFORMATION,
    ) -> HFILE,
> =
    // SAFETY: initializing Detour with the real NtQueryAttributesFile function pointer and our replacement
    unsafe {
        Detour::new(c"NtQueryAttributesFile", ntapi::ntioapi::NtQueryAttributesFile, {
            unsafe extern "system" fn new_nt_query_attrs(
                object_attributes: POBJECT_ATTRIBUTES,
                file_information: PFILE_BASIC_INFORMATION,
            ) -> HFILE {
                // SAFETY: intercepting attribute query to record read access
                unsafe { handle_open(AccessMode::READ, object_attributes) };
                // SAFETY: calling the original NtQueryAttributesFile with all original arguments
                unsafe {
                    (DETOUR_NT_QUERY_ATTRIBUTES_FILE.real())(object_attributes, file_information)
                }
            }
            new_nt_query_attrs
        })
    };

unsafe fn handle_open(access_mode: impl ToAccessMode, path: impl ToAbsolutePath) {
    // SAFETY: accessing the global client which was initialized during DLL_PROCESS_ATTACH
    let client = unsafe { global_client() };
    // SAFETY: resolving path from Windows object attributes or handle for access tracking
    unsafe {
        path.to_absolute_path(|path| {
            let Some(path) = path else {
                return Ok(());
            };
            let path = path.as_slice();
            let path_access = path.iter().rposition(|c| *c == u16::from(b'*')).map_or_else(
                || {
                    // SAFETY: converting access mask to AccessMode via FFI-aware trait
                    PathAccess {
                        mode: access_mode.to_access_mode(),
                        path: IpcPath::from_wide(path),
                    }
                },
                |wildcard_pos| {
                    let path_before_wildcard = &path[..wildcard_pos];
                    let slash_pos = path_before_wildcard
                        .iter()
                        .rposition(|c| *c == u16::from(b'\\') || *c == u16::from(b'/'))
                        .unwrap_or(0);
                    PathAccess {
                        mode: AccessMode::READ_DIR,
                        path: IpcPath::from_wide(&path[..slash_pos]),
                    }
                },
            );
            client.send(path_access);
            Ok(())
        })
    }
    .unwrap();
}

static DETOUR_NT_FULL_QUERY_ATTRIBUTES_FILE: Detour<
    unsafe extern "system" fn(
        object_attributes: POBJECT_ATTRIBUTES,
        file_information: PFILE_NETWORK_OPEN_INFORMATION,
    ) -> HFILE,
> =
    // SAFETY: initializing Detour with the real NtQueryFullAttributesFile function pointer
    unsafe {
        Detour::new(c"NtQueryFullAttributesFile", NtQueryFullAttributesFile, {
            unsafe extern "system" fn new_fn(
                object_attributes: POBJECT_ATTRIBUTES,
                file_information: PFILE_NETWORK_OPEN_INFORMATION,
            ) -> HFILE {
                // SAFETY: intercepting attribute query to record read access
                unsafe { handle_open(GENERIC_READ, object_attributes) };
                // SAFETY: calling the original NtQueryFullAttributesFile
                unsafe {
                    (DETOUR_NT_FULL_QUERY_ATTRIBUTES_FILE.real())(
                        object_attributes,
                        file_information,
                    )
                }
            }
            new_fn
        })
    };

static DETOUR_NT_OPEN_SYMBOLIC_LINK_OBJECT: Detour<
    unsafe extern "system" fn(
        link_handle: PHANDLE,
        desired_access: ACCESS_MASK,
        object_attributes: POBJECT_ATTRIBUTES,
    ) -> HFILE,
> =
    // SAFETY: initializing Detour with the real NtOpenSymbolicLinkObject function pointer
    unsafe {
        Detour::new(c"NtOpenSymbolicLinkObject", ntapi::ntobapi::NtOpenSymbolicLinkObject, {
            unsafe extern "system" fn new_fn(
                link_handle: PHANDLE,
                desired_access: ACCESS_MASK,
                object_attributes: POBJECT_ATTRIBUTES,
            ) -> HFILE {
                // SAFETY: intercepting symlink open to record access
                unsafe { handle_open(desired_access, object_attributes) };
                // SAFETY: calling the original NtOpenSymbolicLinkObject
                unsafe {
                    (DETOUR_NT_OPEN_SYMBOLIC_LINK_OBJECT.real())(
                        link_handle,
                        desired_access,
                        object_attributes,
                    )
                }
            }
            new_fn
        })
    };

static DETOUR_NT_QUERY_INFORMATION_BY_NAME: Detour<
    unsafe extern "system" fn(
        object_attributes: POBJECT_ATTRIBUTES,
        io_status_block: PIO_STATUS_BLOCK,
        file_information: PVOID,
        length: ULONG,
        file_information_class: FILE_INFORMATION_CLASS,
    ) -> HFILE,
> =
    // SAFETY: initializing Detour with the real NtQueryInformationByName function pointer
    unsafe {
        Detour::new(c"NtQueryInformationByName", NtQueryInformationByName, {
            unsafe extern "system" fn new_fn(
                object_attributes: POBJECT_ATTRIBUTES,
                io_status_block: PIO_STATUS_BLOCK,
                file_information: PVOID,
                length: ULONG,
                file_information_class: FILE_INFORMATION_CLASS,
            ) -> HFILE {
                // SAFETY: intercepting information query to record read access
                unsafe { handle_open(GENERIC_READ, object_attributes) };
                // SAFETY: calling the original NtQueryInformationByName
                unsafe {
                    (DETOUR_NT_QUERY_INFORMATION_BY_NAME.real())(
                        object_attributes,
                        io_status_block,
                        file_information,
                        length,
                        file_information_class,
                    )
                }
            }
            new_fn
        })
    };

static DETOUR_NT_QUERY_DIRECTORY_FILE: Detour<
    unsafe extern "system" fn(
        file_handle: HANDLE,
        event: HANDLE,
        apc_routine: PIO_APC_ROUTINE,
        apc_context: PVOID,
        io_status_block: PIO_STATUS_BLOCK,
        file_information: PVOID,
        length: ULONG,
        file_information_class: FILE_INFORMATION_CLASS,
        return_single_entry: BOOLEAN,
        file_name: PUNICODE_STRING,
        restart_scan: BOOLEAN,
    ) -> NTSTATUS,
> =
    // SAFETY: initializing Detour with the real NtQueryDirectoryFile function pointer
    unsafe {
        Detour::new(c"NtQueryDirectoryFile", NtQueryDirectoryFile, {
            unsafe extern "system" fn new_fn(
                file_handle: HANDLE,
                event: HANDLE,
                apc_routine: PIO_APC_ROUTINE,
                apc_context: PVOID,
                io_status_block: PIO_STATUS_BLOCK,
                file_information: PVOID,
                length: ULONG,
                file_information_class: FILE_INFORMATION_CLASS,
                return_single_entry: BOOLEAN,
                file_name: PUNICODE_STRING,
                restart_scan: BOOLEAN,
            ) -> NTSTATUS {
                // SAFETY: intercepting directory query to record directory read access
                unsafe { handle_open(AccessMode::READ_DIR, file_handle) };
                // SAFETY: calling the original NtQueryDirectoryFile
                unsafe {
                    (DETOUR_NT_QUERY_DIRECTORY_FILE.real())(
                        file_handle,
                        event,
                        apc_routine,
                        apc_context,
                        io_status_block,
                        file_information,
                        length,
                        file_information_class,
                        return_single_entry,
                        file_name,
                        restart_scan,
                    )
                }
            }
            new_fn
        })
    };

// NtQueryDirectoryFileEx is not in ntapi crate, so we define it here.
// https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/nf-ntifs-ntquerydirectoryfileex
type NtQueryDirectoryFileExFn = unsafe extern "system" fn(
    file_handle: HANDLE,
    event: HANDLE,
    apc_routine: PIO_APC_ROUTINE,
    apc_context: PVOID,
    io_status_block: PIO_STATUS_BLOCK,
    file_information: PVOID,
    length: ULONG,
    file_information_class: FILE_INFORMATION_CLASS,
    query_flags: ULONG,
    file_name: PUNICODE_STRING,
) -> NTSTATUS;

static DETOUR_NT_QUERY_DIRECTORY_FILE_EX: Detour<NtQueryDirectoryFileExFn> =
    // SAFETY: initializing dynamic Detour for NtQueryDirectoryFileEx (resolved at attach time)
    unsafe {
        Detour::dynamic(c"NtQueryDirectoryFileEx", {
            unsafe extern "system" fn new_fn(
                file_handle: HANDLE,
                event: HANDLE,
                apc_routine: PIO_APC_ROUTINE,
                apc_context: PVOID,
                io_status_block: PIO_STATUS_BLOCK,
                file_information: PVOID,
                length: ULONG,
                file_information_class: FILE_INFORMATION_CLASS,
                query_flags: ULONG,
                file_name: PUNICODE_STRING,
            ) -> NTSTATUS {
                // SAFETY: intercepting directory query to record directory read access
                unsafe { handle_open(AccessMode::READ_DIR, file_handle) };
                // SAFETY: calling the original NtQueryDirectoryFileEx
                unsafe {
                    (DETOUR_NT_QUERY_DIRECTORY_FILE_EX.real())(
                        file_handle,
                        event,
                        apc_routine,
                        apc_context,
                        io_status_block,
                        file_information,
                        length,
                        file_information_class,
                        query_flags,
                        file_name,
                    )
                }
            }
            new_fn
        })
    };

static DETOUR_NT_SET_INFORMATION_FILE: Detour<
    unsafe extern "system" fn(HANDLE, PIO_STATUS_BLOCK, PVOID, ULONG, FILE_INFORMATION_CLASS) -> NTSTATUS,
> = unsafe {
    // SAFETY: the replacement has the exact NtSetInformationFile ABI.
    Detour::new(c"NtSetInformationFile", ntapi::ntioapi::NtSetInformationFile, {
        unsafe extern "system" fn set_information(
            handle: HANDLE,
            status: PIO_STATUS_BLOCK,
            information: PVOID,
            length: ULONG,
            class: FILE_INFORMATION_CLASS,
        ) -> NTSTATUS {
            use fspy_shared::windows_access::{information_mutation, InformationMutation};
            let mutation = information_mutation(class);
            if mutation != InformationMutation::HandleOnly {
                // Resolve before deletion/rename invalidates the old name. An
                // unresolved handle or destination must not silently disappear.
                let observed = unsafe { handle.to_absolute_path(|path| {
                    if let Some(path) = path {
                        global_client().send(PathAccess {
                            mode: AccessMode::WRITE,
                            path: IpcPath::from_wide(path.as_slice()),
                        });
                        Ok(true)
                    } else { Ok(false) }
                }) };
                if observed != Ok(true) || mutation == InformationMutation::Unresolved {
                    crate::windows::client::report_global_failure();
                }
            }
            // SAFETY: preserve every argument and result even if collection failed.
            unsafe { (DETOUR_NT_SET_INFORMATION_FILE.real())(handle, status, information, length, class) }
        }
        set_information
    })
};

pub const DETOURS: &[DetourAny] = &[
    DETOUR_NT_SET_INFORMATION_FILE.as_any(),
    DETOUR_NT_CREATE_USER_PROCESS.as_any(),
    DETOUR_NT_CREATE_FILE.as_any(),
    DETOUR_NT_OPEN_FILE.as_any(),
    DETOUR_NT_QUERY_ATTRIBUTES_FILE.as_any(),
    DETOUR_NT_FULL_QUERY_ATTRIBUTES_FILE.as_any(),
    DETOUR_NT_OPEN_SYMBOLIC_LINK_OBJECT.as_any(),
    DETOUR_NT_QUERY_INFORMATION_BY_NAME.as_any(),
    DETOUR_NT_QUERY_DIRECTORY_FILE.as_any(),
    DETOUR_NT_QUERY_DIRECTORY_FILE_EX.as_any(),
];
