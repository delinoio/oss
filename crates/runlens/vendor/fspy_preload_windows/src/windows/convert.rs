use std::fmt::Debug;

use fspy_shared::ipc::AccessMode;
use widestring::{U16CStr, U16CString, U16Str};
use winapi::{
    shared::ntdef::{HANDLE, OBJECT_ATTRIBUTES, POBJECT_ATTRIBUTES, UNICODE_STRING},
    um::winnt::ACCESS_MASK,
};

use crate::windows::winapi_utils::{
    access_mask_to_mode, combine_paths, get_path_name, is_non_filesystem_handle,
};

pub trait ToAccessMode: Debug {
    unsafe fn to_access_mode(self) -> AccessMode;
}

impl ToAccessMode for AccessMode {
    unsafe fn to_access_mode(self) -> AccessMode {
        self
    }
}

impl ToAccessMode for ACCESS_MASK {
    unsafe fn to_access_mode(self) -> AccessMode {
        access_mask_to_mode(self)
    }
}

pub trait ToAbsolutePath {
    unsafe fn to_absolute_path<R, F: FnOnce(Option<&U16Str>) -> winsafe::SysResult<R>>(
        self,
        f: F,
    ) -> winsafe::SysResult<R>;
}

impl ToAbsolutePath for HANDLE {
    unsafe fn to_absolute_path<R, F: FnOnce(Option<&U16Str>) -> winsafe::SysResult<R>>(
        self,
        f: F,
    ) -> winsafe::SysResult<R> {
        // SAFETY: get_path_name performs FFI call with this HANDLE to retrieve the file path
        let resolved = unsafe { get_path_name(self) }.ok();
        let resolved = resolved.as_ref().map(|p| U16Str::from_slice(p));
        f(resolved)
    }
}

impl ToAbsolutePath for POBJECT_ATTRIBUTES {
    unsafe fn to_absolute_path<R, F: FnOnce(Option<&U16Str>) -> winsafe::SysResult<R>>(
        self,
        f: F,
    ) -> winsafe::SysResult<R> {
        // Caller pointers may be inaccessible or concurrently unmapped. Copy
        // through the kernel before parsing; never dereference them in the hook.
        let Ok((root, name)) = copy_object_name(self) else {
            crate::windows::client::report_global_failure();
            return f(None);
        };
        let fname_str = U16Str::from_slice(&name);
        let fname_slice = fname_str.as_slice();
        let is_absolute = fname_slice.first() == Some(&b'\\'.into()) // \...
        || fname_slice.get(1) == Some(&b':'.into()); // C:...

        if is_absolute {
            f(Some(fname_str))
        } else {
            // CreatePipe can open an endpoint relative to a pipe handle.
            // Its lack of a DOS path is not a lost filesystem observation.
            if unsafe { is_non_filesystem_handle(root) } {
                return f(None);
            }
            // SAFETY: the kernel validates the copied handle value.
            let Ok(mut root_dir) = (unsafe { get_path_name(root) }) else {
                // A valid NT operation can outlive failed normalized-name lookup
                // (for example, denied SMB traversal). Never silently drop it.
                crate::windows::client::report_global_failure();
                return f(None);
            };
            // If filename is empty, just use root_dir directly
            if fname_str.is_empty() {
                let root_dir_str = U16Str::from_slice(&root_dir);
                return f(Some(root_dir_str));
            }
            let root_dir_cstr = {
                root_dir.push(0);
                // SAFETY: we just pushed a null terminator, so the buffer is null-terminated
                unsafe { U16CStr::from_ptr_str(root_dir.as_ptr()) }
            };
            let fname_cstring = U16CString::from_ustr_truncate(fname_str);
            let Ok(abs_path) = combine_paths(root_dir_cstr, fname_cstring.as_ucstr()) else {
                crate::windows::client::report_global_failure();
                return f(None);
            };
            f(Some(abs_path.to_u16_str()))
        }
    }
}

// All fields are decoded from owned bytes, including nested UNICODE_STRING
// storage. Bounds follow its 16-bit byte lengths; no caller allocation is trusted.
fn copy_object_name(attributes: POBJECT_ATTRIBUTES) -> Result<(HANDLE, Vec<u16>), ()> {
    use std::mem::{align_of, offset_of, size_of};
    use crate::windows::winapi_utils::copy_process_bytes;
    let address = attributes as usize;
    if address == 0 || address % align_of::<OBJECT_ATTRIBUTES>() != 0 { return Err(()); }
    address.checked_add(size_of::<OBJECT_ATTRIBUTES>()).ok_or(())?;
    let object = copy_process_bytes(address, size_of::<OBJECT_ATTRIBUTES>())?;
    let word = |bytes: &[u8], offset: usize| usize::from_ne_bytes(bytes[offset..offset + size_of::<usize>()].try_into().unwrap());
    let length = u32::from_ne_bytes(object[..4].try_into().unwrap());
    if length as usize != size_of::<OBJECT_ATTRIBUTES>() { return Err(()); }
    let root = word(&object, offset_of!(OBJECT_ATTRIBUTES, RootDirectory)) as HANDLE;
    let name = word(&object, offset_of!(OBJECT_ATTRIBUTES, ObjectName));
    if name == 0 { return Ok((root, Vec::new())); }
    if name % align_of::<UNICODE_STRING>() != 0 { return Err(()); }
    name.checked_add(size_of::<UNICODE_STRING>()).ok_or(())?;
    let string = copy_process_bytes(name, size_of::<UNICODE_STRING>())?;
    let length = usize::from(u16::from_ne_bytes(string[..2].try_into().unwrap()));
    let maximum = usize::from(u16::from_ne_bytes(string[2..4].try_into().unwrap()));
    if length % 2 != 0 || length > maximum { return Err(()); }
    if length == 0 { return Ok((root, Vec::new())); }
    let buffer = word(&string, offset_of!(UNICODE_STRING, Buffer));
    if buffer == 0 || buffer % align_of::<u16>() != 0 { return Err(()); }
    buffer.checked_add(length).ok_or(())?;
    let bytes = copy_process_bytes(buffer, length)?;
    let name: Vec<u16> = bytes.chunks_exact(2).map(|pair| u16::from_ne_bytes([pair[0], pair[1]])).collect();
    if name.contains(&0) { return Err(()); }
    Ok((root, name))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn object_attributes_reject_malformed_memory_without_dereferencing() {
        for pointer in [0, 1, 16, usize::MAX - 15] {
            assert!(copy_object_name(pointer as POBJECT_ATTRIBUTES).is_err());
        }
        let mut path = [b'C' as u16, b':' as u16, b'\\' as u16, b'x' as u16];
        let mut name = UNICODE_STRING { Length: 8, MaximumLength: 8, Buffer: path.as_mut_ptr() };
        // SAFETY: OBJECT_ATTRIBUTES contains only integers and raw pointers.
        let mut attributes: OBJECT_ATTRIBUTES = unsafe { std::mem::zeroed() };
        attributes.Length = std::mem::size_of::<OBJECT_ATTRIBUTES>() as u32;
        attributes.ObjectName = &mut name;
        assert_eq!(copy_object_name(&mut attributes).unwrap().1, path);
        for pointer in [1, 16, usize::MAX - 15] {
            attributes.ObjectName = pointer as *mut UNICODE_STRING;
            assert!(copy_object_name(&mut attributes).is_err());
        }
        attributes.ObjectName = &mut name;
        for pointer in [0, 1, 16, usize::MAX - 1] {
            name.Buffer = pointer as *mut u16;
            attributes.ObjectName = &mut name;
            assert!(copy_object_name(&mut attributes).is_err());
        }
        name.Buffer = path.as_mut_ptr();
        for length in [1, 9, u16::MAX] {
            name.Length = length;
            attributes.ObjectName = &mut name;
            assert!(copy_object_name(&mut attributes).is_err());
        }
        name.Length = 8;
        attributes.ObjectName = &mut name;
        attributes.Length = 0;
        assert!(copy_object_name(&mut attributes).is_err());
    }
}
