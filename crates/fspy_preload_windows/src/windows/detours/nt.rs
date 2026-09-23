use std::mem::{offset_of, size_of};

use fspy_shared::ipc::{AccessMode, IpcPath, PathAccess};
use ntapi::{
    ntioapi::{
        FILE_CREATE, FILE_INFORMATION_CLASS, FILE_OPEN_IF, FILE_OVERWRITE, FILE_OVERWRITE_IF,
        FILE_SUPERSEDE, NtQueryDirectoryFile, NtQueryFullAttributesFile, NtQueryInformationByName,
        PFILE_BASIC_INFORMATION, PFILE_NETWORK_OPEN_INFORMATION, PIO_APC_ROUTINE, PIO_STATUS_BLOCK,
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
            BOOLEAN, HANDLE, NT_SUCCESS, NTSTATUS, PHANDLE, PLARGE_INTEGER, POBJECT_ATTRIBUTES,
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

// CreateProcess ultimately asks NtCreateUserProcess to open the executable
// image. Some Windows versions perform that open entirely inside the syscall,
// so it never reaches the NtCreateFile and query functions hooked below.
// PS_ATTRIBUTE_IMAGE_NAME is the kernel-facing path that identifies
// the image to open; RTL_USER_PROCESS_PARAMETERS.ImagePathName is only metadata
// for the child PEB and can intentionally name a different file.
//
// Record the image before forwarding the syscall, matching the attempted-access
// semantics of the other NT hooks in this module. This is important for missing
// executables: the failed lookup is still an input access even though
// NtCreateUserProcess returns an error.
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
                // SAFETY: observing caller memory without changing the forwarded arguments
                unsafe { handle_process_image(attribute_list) };

                // SAFETY: calling the original NtCreateUserProcess with all original arguments
                let status = unsafe {
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
                };
                if NT_SUCCESS(status) && !super::create_process::is_hooking_create_process() {
                    // Direct NT creation bypasses the CreateProcess callbacks
                    // that copy the payload and inject the DLL. Its child can
                    // run, but this trace cannot claim to cover that child.
                    // SAFETY: the DLL client was initialized before detours.
                    unsafe { global_client() }.mark_incomplete();
                }
                status
            }
            new_fn
        })
    };

unsafe fn handle_process_image(attribute_list: PPS_ATTRIBUTE_LIST) {
    // SAFETY: NtCreateUserProcess requires its attribute list to remain valid for
    // this call.
    if let Some(image_path) = unsafe { read_process_image_attribute(attribute_list) } {
        // Sender serialization completes before this call returns, so IpcPath does not
        // retain the borrowed PS_ATTRIBUTE_IMAGE_NAME buffer past the
        // NtCreateUserProcess call. SAFETY: accessing the global client which
        // was initialized during DLL_PROCESS_ATTACH
        unsafe { global_client() }.send(PathAccess {
            mode: AccessMode::READ,
            path: IpcPath::from_wide(image_path),
        });
    }
}

/// Find the kernel-facing executable name in an `NtCreateUserProcess` attribute
/// list.
///
/// `PS_ATTRIBUTE_LIST` is a variable-length structure: `TotalLength` covers a
/// fixed-size header followed by contiguous `PS_ATTRIBUTE` entries. Its Rust
/// definition contains one placeholder element, so the actual entry count must
/// be derived from `TotalLength`, not from the array type.
///
/// The returned slice borrows the caller's `PS_ATTRIBUTE_IMAGE_NAME` buffer and
/// is valid only while the intercepted `NtCreateUserProcess` call is active.
///
/// # Safety
///
/// `attribute_list` and the image-name buffer it references must remain valid
/// for the duration of the intercepted call, as required by
/// `NtCreateUserProcess`.
unsafe fn read_process_image_attribute<'a>(
    attribute_list: PPS_ATTRIBUTE_LIST,
) -> Option<&'a [u16]> {
    if attribute_list.is_null() {
        return None;
    }
    // Read only the fixed header before trusting TotalLength. In particular,
    // do not create a reference to the trailing placeholder entry when the
    // caller supplied a short or malformed list.
    let total_length = unsafe { std::ptr::addr_of!((*attribute_list).TotalLength).read() };
    let attribute_count = checked_attribute_count(total_length)?;
    // SAFETY: a native attribute list with the validated length contains this
    // many contiguous entries after its header. The caller owns the allocation
    // for the duration of NtCreateUserProcess.
    let attributes: &[PS_ATTRIBUTE] = unsafe {
        std::slice::from_raw_parts(
            attribute_list
                .cast::<u8>()
                .add(offset_of!(PS_ATTRIBUTE_LIST, Attributes))
                .cast::<PS_ATTRIBUTE>(),
            attribute_count,
        )
    };
    for attribute in attributes {
        if attribute.Attribute != PS_ATTRIBUTE_IMAGE_NAME {
            continue;
        }

        // Unlike a UNICODE_STRING, PS_ATTRIBUTE_IMAGE_NAME stores the path buffer
        // directly in ValuePtr and stores its byte length in Size. It is the
        // image path consumed by the kernel, so do not fall back to the
        // separately spoofable process-parameter path.
        // SAFETY: PS_ATTRIBUTE_IMAGE_NAME stores a valid UTF-16 pointer in ValuePtr for
        // this call; a null pointer is parsed as None.
        let image_path = unsafe { attribute.u.ValuePtr.cast::<u16>().as_ref()? };
        // SAFETY: the attribute contract guarantees a valid UTF-16 buffer of Size bytes
        // for this call. Size is the counted string length, so no
        // NUL-terminator parsing is needed.
        return Some(unsafe {
            std::slice::from_raw_parts(
                std::ptr::from_ref(image_path),
                attribute.Size / size_of::<u16>(),
            )
        });
    }

    None
}

fn checked_attribute_count(total_length: usize) -> Option<usize> {
    let tail_length = total_length.checked_sub(offset_of!(PS_ATTRIBUTE_LIST, Attributes))?;
    let entry_size = size_of::<PS_ATTRIBUTE>();
    if tail_length == 0 || tail_length % entry_size != 0 || total_length > isize::MAX as usize {
        return None;
    }
    Some(tail_length / entry_size)
}

#[cfg(test)]
mod attribute_list_tests {
    use super::*;

    #[test]
    fn rejects_short_and_partial_attribute_lists() {
        let offset = offset_of!(PS_ATTRIBUTE_LIST, Attributes);
        let entry = size_of::<PS_ATTRIBUTE>();
        assert_eq!(checked_attribute_count(offset - 1), None);
        assert_eq!(checked_attribute_count(offset), None);
        assert_eq!(checked_attribute_count(offset + entry - 1), None);
        assert_eq!(checked_attribute_count(offset + entry), Some(1));
        assert_eq!(checked_attribute_count(usize::MAX), None);
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
                // SAFETY: intercepting file open to record access before forwarding to real
                // function
                unsafe {
                    handle_open(
                        create_file_access_mode(desired_access, create_disposition),
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

fn create_file_access_mode(desired_access: ACCESS_MASK, disposition: ULONG) -> AccessMode {
    let mode = crate::windows::winapi_utils::access_mask_to_mode(desired_access);
    match disposition {
        FILE_SUPERSEDE | FILE_CREATE | FILE_OPEN_IF | FILE_OVERWRITE | FILE_OVERWRITE_IF => {
            mode.union(AccessMode::WRITE)
        }
        _ => mode,
    }
}

#[cfg(test)]
mod create_file_tests {
    use super::*;

    #[test]
    fn creating_or_replacing_with_read_access_is_a_write() {
        for disposition in [
            FILE_SUPERSEDE,
            FILE_CREATE,
            FILE_OPEN_IF,
            FILE_OVERWRITE,
            FILE_OVERWRITE_IF,
        ] {
            assert_eq!(
                create_file_access_mode(GENERIC_READ, disposition),
                AccessMode::READ.union(AccessMode::WRITE)
            );
        }
        assert_eq!(
            create_file_access_mode(GENERIC_READ, ntapi::ntioapi::FILE_OPEN),
            AccessMode::READ
        );
    }
}

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
                // SAFETY: intercepting file open to record access before forwarding to real
                // function
                unsafe {
                    handle_open(desired_access, object_attributes);
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
    // SAFETY: initializing Detour with the real NtQueryAttributesFile function pointer and our
    // replacement
    unsafe {
        Detour::new(
            c"NtQueryAttributesFile",
            ntapi::ntioapi::NtQueryAttributesFile,
            {
                unsafe extern "system" fn new_nt_query_attrs(
                    object_attributes: POBJECT_ATTRIBUTES,
                    file_information: PFILE_BASIC_INFORMATION,
                ) -> HFILE {
                    // SAFETY: intercepting attribute query to record read access
                    unsafe { handle_open(AccessMode::READ, object_attributes) };
                    // SAFETY: calling the original NtQueryAttributesFile with all original
                    // arguments
                    unsafe {
                        (DETOUR_NT_QUERY_ATTRIBUTES_FILE.real())(
                            object_attributes,
                            file_information,
                        )
                    }
                }
                new_nt_query_attrs
            },
        )
    };

unsafe fn handle_open(access_mode: impl ToAccessMode, path: impl ToAbsolutePath) {
    // SAFETY: accessing the global client which was initialized during
    // DLL_PROCESS_ATTACH
    let client = unsafe { global_client() };
    // SAFETY: resolving path from Windows object attributes or handle for access
    // tracking
    if unsafe {
        path.to_absolute_path(|path| {
            let Some(path) = path else {
                return Ok(());
            };
            let path = path.as_slice();
            let path_access = path
                .iter()
                .rposition(|c| *c == u16::from(b'*'))
                .map_or_else(
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
    .is_err()
    {
        // The native call still receives its original arguments. Its access
        // cannot be represented in the trace after path resolution fails.
        client.mark_incomplete();
    }
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
        Detour::new(
            c"NtOpenSymbolicLinkObject",
            ntapi::ntobapi::NtOpenSymbolicLinkObject,
            {
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
            },
        )
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

pub const DETOURS: &[DetourAny] = &[
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
