// SPDX-License-Identifier: Apache-2.0
#![allow(
    non_upper_case_globals,
    reason = "NT API constants retain their upstream names"
)]
//! Native directory records for merged dependency/cache views. Enumeration
//! cursors are shared by duplicated handles, as on the native open object.

use std::os::windows::{
    fs::{MetadataExt, OpenOptionsExt},
    io::AsRawHandle,
};

use winapi::um::{
    fileapi::{GetFileInformationByHandle, BY_HANDLE_FILE_INFORMATION},
    winbase::FILE_FLAG_BACKUP_SEMANTICS,
};

use super::*;
use crate::directory_query::QueryState;

/// Byte offset of `FileId` in the ID-bearing record classes. Offsets follow
/// `FILE_ID_BOTH_DIR_INFORMATION` (a 94-byte structure padded to the next
/// 8-byte boundary) and `FILE_ID_FULL_DIR_INFORMATION` (`EaSize` then padding).
const BOTH_FILE_ID_OFFSET: usize = 96;
const FULL_FILE_ID_OFFSET: usize = 72;

/// File identifier reported when the backing object cannot be opened for its
/// identity. Zero means no identifier.
const UNAVAILABLE_FILE_ID: u64 = 0;

/// The directory-record fields read for one entry.
struct Attributes {
    creation_time: u64,
    last_access_time: u64,
    last_write_time: u64,
    size: u64,
    file_attributes: u32,
}
impl Attributes {
    fn from_metadata(metadata: &fs::Metadata) -> Self {
        Self {
            creation_time: metadata.creation_time(),
            last_access_time: metadata.last_access_time(),
            last_write_time: metadata.last_write_time(),
            size: metadata.len(),
            file_attributes: metadata.file_attributes(),
        }
    }
}

struct Item {
    name: Vec<u16>,
    attributes: Attributes,
    /// Native file reference number of the backing object that `attributes`
    /// describes, or `UNAVAILABLE_FILE_ID`. Hard links to one file share it.
    file_id: u64,
}
pub(super) struct Listing {
    items: Vec<Item>,
    index: usize,
    pattern: Vec<u16>,
    query_state: QueryState,
}

unsafe extern "system" {
    fn RtlIsNameInExpression(
        expression: PUNICODE_STRING,
        name: PUNICODE_STRING,
        ignore_case: BOOLEAN,
        table: *mut u16,
    ) -> BOOLEAN;
}

fn matching(pattern: &[u16], name: &[u16]) -> bool {
    let mut pattern = pattern.to_vec();
    // RtlIsNameInExpression expects an uppercase expression for IgnoreCase.
    // Upcase through Windows' invariant Unicode table, not Unicode expansion.
    for unit in &mut pattern {
        *unit = unsafe { ntapi::ntrtl::RtlUpcaseUnicodeChar(*unit) };
    }
    let mut name = name.to_vec();
    let mut expression = UNICODE_STRING {
        Length: (pattern.len() * 2) as u16,
        MaximumLength: (pattern.len() * 2) as u16,
        Buffer: pattern.as_mut_ptr(),
    };
    let mut value = UNICODE_STRING {
        Length: (name.len() * 2) as u16,
        MaximumLength: (name.len() * 2) as u16,
        Buffer: name.as_mut_ptr(),
    };
    unsafe { RtlIsNameInExpression(&mut expression, &mut value, 1, ptr::null_mut()) != 0 }
}

fn filetime(time: &winapi::shared::minwindef::FILETIME) -> u64 {
    (u64::from(time.dwHighDateTime) << 32) | u64::from(time.dwLowDateTime)
}

/// Opens the object at `path` once, following reparse points like a metadata
/// read, and returns its attributes together with its native 64-bit file
/// reference number. Both come from the same handle, so a concurrent
/// replacement of the entry cannot pair one object's attributes with another
/// object's identity. Like `fs::metadata`, the open requests no access beyond
/// the implicit attribute read and does not conflict with other users.
fn opened(path: &Path) -> std::io::Result<(Attributes, u64)> {
    let file = fs::OpenOptions::new()
        .access_mode(0)
        .custom_flags(FILE_FLAG_BACKUP_SEMANTICS)
        .open(path)?;
    let mut information = std::mem::MaybeUninit::<BY_HANDLE_FILE_INFORMATION>::zeroed();
    if unsafe { GetFileInformationByHandle(file.as_raw_handle().cast(), information.as_mut_ptr()) }
        == 0
    {
        return Err(std::io::Error::last_os_error());
    }
    let information = unsafe { information.assume_init() };
    let attributes = Attributes {
        creation_time: filetime(&information.ftCreationTime),
        last_access_time: filetime(&information.ftLastAccessTime),
        last_write_time: filetime(&information.ftLastWriteTime),
        size: (u64::from(information.nFileSizeHigh) << 32) | u64::from(information.nFileSizeLow),
        file_attributes: information.dwFileAttributes,
    };
    let file_id =
        (u64::from(information.nFileIndexHigh) << 32) | u64::from(information.nFileIndexLow);
    Ok((attributes, file_id))
}

/// Reads the attributes and file reference number of one listed entry.
///
/// Enumeration never requires per-entry open access: an entry the caller can
/// list but not open (for example because of its ACL) keeps the attributes
/// `fs::metadata` reads from the parent directory and reports
/// `UNAVAILABLE_FILE_ID` instead of failing the whole listing or pairing those
/// attributes with an identity from a second lookup. Only an entry whose
/// attributes cannot be read at all returns `failure`.
fn item(name: Vec<u16>, path: &Path, failure: NTSTATUS) -> std::result::Result<Item, NTSTATUS> {
    let (attributes, file_id) = match opened(path) {
        Ok(value) => value,
        Err(error) => {
            tracing::debug!(
                action = "windows_directory_file_id_unavailable",
                path = %path.display(),
                %error,
                "Could not open a merged directory entry for its file reference number"
            );
            let metadata = fs::metadata(path).map_err(|error| {
                tracing::debug!(
                    action = "windows_directory_metadata_failed",
                    path = %path.display(),
                    %error,
                    "Could not read metadata for a merged directory entry"
                );
                failure
            })?;
            (Attributes::from_metadata(&metadata), UNAVAILABLE_FILE_ID)
        }
    };
    Ok(Item {
        name,
        attributes,
        file_id,
    })
}

fn listing(file: &OpenFile, pattern: Vec<u16>) -> std::result::Result<Listing, NTSTATUS> {
    let mut items = Vec::new();
    let physical = &file.translation.physical;
    for (name, path) in [
        (".", physical.as_path()),
        ("..", physical.parent().unwrap_or(physical)),
    ] {
        let name: Vec<_> = name.encode_utf16().collect();
        if matching(&pattern, &name) {
            items.push(item(name, path, IO_ERROR)?);
        }
    }
    for entry in fs::read_dir(physical).map_err(|_| MISSING)? {
        let entry = entry.map_err(|_| IO_ERROR)?;
        let name: Vec<_> = entry.file_name().encode_wide().collect();
        if matching(&pattern, &name) {
            items.push(item(name, &entry.path(), IO_ERROR)?);
        }
    }
    let additions = {
        let runtime = RUNTIME
            .get()
            .ok_or(IO_ERROR)?
            .lock()
            .map_err(|_| IO_ERROR)?;
        runtime
            .view
            .directory_entries(&file.translation.logical, physical)
    }
    .map_err(|error| error_status(error.code))?;
    for entry in additions {
        let name: Vec<_> = entry.name.encode_utf16().collect();
        if items.iter().any(|item| item.name == name) || !matching(&pattern, &name) {
            continue;
        }
        let translation = translate(&file.translation.logical.join(&entry.name), false)?;
        items.push(item(name, &translation.physical, MISSING)?);
    }
    Ok(Listing {
        items,
        index: 0,
        pattern,
        query_state: QueryState::new(),
    })
}

fn layout(class: FILE_INFORMATION_CLASS) -> Option<usize> {
    match class {
        FileDirectoryInformation => Some(64),
        FileFullDirectoryInformation => Some(68),
        FileBothDirectoryInformation => Some(94),
        FileNamesInformation => Some(12),
        FileIdBothDirectoryInformation => Some(104),
        FileIdFullDirectoryInformation => Some(80),
        _ => None,
    }
}
fn put32(buffer: &mut [u8], offset: usize, value: u32) {
    buffer[offset..offset + 4].copy_from_slice(&value.to_le_bytes());
}
fn put64(buffer: &mut [u8], offset: usize, value: u64) {
    buffer[offset..offset + 8].copy_from_slice(&value.to_le_bytes());
}

fn write_record(buffer: &mut [u8], item: &Item, class: FILE_INFORMATION_CLASS, header: usize) {
    buffer.fill(0);
    if class != FileNamesInformation {
        let attributes = &item.attributes;
        put64(buffer, 8, attributes.creation_time);
        put64(buffer, 16, attributes.last_access_time);
        put64(buffer, 24, attributes.last_write_time);
        put64(buffer, 32, attributes.last_write_time);
        put64(buffer, 40, attributes.size);
        put64(buffer, 48, attributes.size.saturating_add(4095) & !4095);
        put32(buffer, 56, attributes.file_attributes);
        put32(buffer, 60, (item.name.len() * 2) as u32);
        match class {
            FileIdBothDirectoryInformation => put64(buffer, BOTH_FILE_ID_OFFSET, item.file_id),
            FileIdFullDirectoryInformation => put64(buffer, FULL_FILE_ID_OFFSET, item.file_id),
            _ => {}
        }
    } else {
        put32(buffer, 8, (item.name.len() * 2) as u32);
    }
    for (i, unit) in item
        .name
        .iter()
        .take((buffer.len() - header) / 2)
        .enumerate()
    {
        buffer[header + i * 2..header + i * 2 + 2].copy_from_slice(&unit.to_le_bytes());
    }
}

#[allow(
    clippy::too_many_arguments,
    reason = "Retain the native directory query parameter contract"
)]
unsafe fn query(
    file: HANDLE,
    event: HANDLE,
    apc: PIO_APC_ROUTINE,
    context: PVOID,
    ios: PIO_STATUS_BLOCK,
    buffer: PVOID,
    length: ULONG,
    class: FILE_INFORMATION_CLASS,
    single: bool,
    pattern: PUNICODE_STRING,
    restart: bool,
) -> Option<NTSTATUS> {
    let owned = handle(file)?;
    let additions = {
        let runtime = RUNTIME.get()?.lock().ok()?;
        runtime
            .view
            .directory_entries(&owned.translation.logical, &owned.translation.physical)
    };
    match additions {
        Ok(entries) if entries.is_empty() => return None,
        Err(error) => return Some(error_status(error.code)),
        _ => {}
    }
    // Do not claim completion-port/APC semantics with a synchronous emulation.
    // Native-backed directories with no merged entries use the native syscall.
    // `NtQueryDirectoryFileEx` accepts a null context for a handle associated
    // with a completion port, so the association is tracked per open object.
    // Hold the association state for the whole emulation: a concurrent
    // association request waits for this query instead of installing a port
    // while a packet-less result is being produced.
    let association = owned.completion_port();
    if apc.is_some() || !context.is_null() || *association {
        tracing::debug!(
            action = "windows_directory_query_rejected",
            callback = apc.is_some() || !context.is_null(),
            "Rejected a merged directory query that needs APC or completion-port delivery"
        );
        return Some(fail(Code::PnportUnsupportedOperation));
    }
    let Some(header) = layout(class) else {
        return Some(fail(Code::PnportUnsupportedOperation));
    };
    if ios.is_null() || buffer.is_null() || (length as usize) < header {
        return Some(INVALID);
    }
    let mut cursor = match owned.listing.lock() {
        Ok(value) => value,
        Err(_) => return Some(IO_ERROR),
    };
    if restart || cursor.is_none() {
        let query_state = cursor
            .as_ref()
            .map_or_else(QueryState::new, |value| value.query_state);
        let pattern = if let Some(previous) = cursor.as_ref() {
            previous.pattern.clone()
        } else if pattern.is_null() {
            vec![b'*' as u16]
        } else {
            match unicode(pattern) {
                Ok(value) => value,
                Err(status) => return Some(status),
            }
        };
        *cursor = match listing(&owned, pattern) {
            Ok(mut value) => {
                value.query_state = query_state;
                Some(value)
            }
            Err(status) => return Some(status),
        };
    }
    let cursor = cursor.as_mut().unwrap();
    let bytes = std::slice::from_raw_parts_mut(buffer.cast::<u8>(), length as usize);
    let mut used = 0;
    let mut previous = None;
    let mut partial = false;
    while let Some(item) = cursor.items.get(cursor.index) {
        let needed = header + item.name.len() * 2;
        let aligned = (needed + 7) & !7;
        if used + needed > bytes.len() {
            if let Some(name_bytes) = cursor.query_state.partial_name_bytes(
                header,
                item.name.len() * 2,
                bytes.len(),
                used,
            ) {
                used = header + name_bytes;
                write_record(&mut bytes[..used], item, class, header);
                partial = true;
            }
            break;
        }
        write_record(&mut bytes[used..used + needed], item, class, header);
        if let Some(previous) = previous {
            put32(bytes, previous, (used - previous) as u32);
        }
        previous = Some(used);
        cursor.index += 1;
        used += if used + aligned <= bytes.len() {
            aligned
        } else {
            needed
        };
        if single {
            break;
        }
    }
    let status = cursor
        .query_state
        .finish(used, cursor.index < cursor.items.len(), partial);
    (*ios).Information = used;
    (*ios).u.Status = status;
    if !event.is_null() {
        SetEvent(event);
    }
    Some(status)
}

hook!(QUERY, ntapi::ntioapi::NtQueryDirectoryFile, c"NtQueryDirectoryFile", (file: HANDLE, event: HANDLE, apc: PIO_APC_ROUTINE, context: PVOID, ios: PIO_STATUS_BLOCK, buffer: PVOID, length: ULONG, class: FILE_INFORMATION_CLASS, single: BOOLEAN, pattern: PUNICODE_STRING, restart: BOOLEAN) -> NTSTATUS, {
    let Some(_guard) = Guard::enter() else { return (QUERY.real())(file,event,apc,context,ios,buffer,length,class,single,pattern,restart); };
    query(file,event,apc,context,ios,buffer,length,class,single != 0,pattern,restart != 0).unwrap_or_else(|| (QUERY.real())(file,event,apc,context,ios,buffer,length,class,single,pattern,restart))
});

static QUERY_EX: Detour<
    unsafe extern "system" fn(
        HANDLE,
        HANDLE,
        PIO_APC_ROUTINE,
        PVOID,
        PIO_STATUS_BLOCK,
        PVOID,
        ULONG,
        FILE_INFORMATION_CLASS,
        ULONG,
        PUNICODE_STRING,
    ) -> NTSTATUS,
> = unsafe {
    Detour::dynamic(c"NtQueryDirectoryFileEx", {
        unsafe extern "system" fn replacement(
            file: HANDLE,
            event: HANDLE,
            apc: PIO_APC_ROUTINE,
            context: PVOID,
            ios: PIO_STATUS_BLOCK,
            buffer: PVOID,
            length: ULONG,
            class: FILE_INFORMATION_CLASS,
            flags: ULONG,
            pattern: PUNICODE_STRING,
        ) -> NTSTATUS {
            let Some(_guard) = Guard::enter() else {
                return (QUERY_EX.real())(
                    file, event, apc, context, ios, buffer, length, class, flags, pattern,
                );
            };
            if flags & !3 != 0 {
                return fail(Code::PnportUnsupportedOperation);
            }
            query(
                file,
                event,
                apc,
                context,
                ios,
                buffer,
                length,
                class,
                flags & 2 != 0,
                pattern,
                flags & 1 != 0,
            )
            .unwrap_or_else(|| {
                (QUERY_EX.real())(
                    file, event, apc, context, ios, buffer, length, class, flags, pattern,
                )
            })
        }
        replacement
    })
};

pub(super) const DETOURS: &[DetourAny] = &[QUERY.as_any(), QUERY_EX.as_any()];
