use fspy_shared::ipc::AccessMode;
use smallvec::SmallVec;
use widestring::{U16CStr, U16Str};
use winapi::{
    ctypes::c_long,
    shared::{
        minwindef::{BOOL, FALSE, MAX_PATH},
        ntdef::{HANDLE, PWSTR},
        winerror::{NO_ERROR, S_OK},
    },
    um::{
        fileapi::GetFinalPathNameByHandleW,
        winnt::{
            ACCESS_MASK, DELETE, FILE_APPEND_DATA, FILE_READ_DATA, FILE_WRITE_DATA,
            FILE_WRITE_ATTRIBUTES, FILE_WRITE_EA, GENERIC_ALL, GENERIC_READ, GENERIC_WRITE,
        },
    },
};
use windows_sys::Win32::{
    Foundation::LocalFree,
    UI::Shell::{PATHCCH_ALLOW_LONG_PATHS, PathAllocCombine},
};
use winsafe::{GetLastError, co};

pub fn ck(b: BOOL) -> winsafe::SysResult<()> {
    if b == FALSE { Err(GetLastError()) } else { Ok(()) }
}

pub const fn ck_long(val: c_long) -> winsafe::SysResult<()> {
    // LONG APIs return their error code directly; consulting a constant or
    // GetLastError would hide failed hook transactions. See PATCHES.md.
    if val.cast_unsigned() == NO_ERROR {
        Ok(())
    } else {
        // SAFETY: creating an ERROR from the raw c_long value for the Windows error code
        Err(unsafe { winsafe::co::ERROR::from_raw(val.cast_unsigned()) })
    }
}

thread_local! {
    // GetFinalPathNameByHandleW itself queries NT file information. Suppress
    // only those collector-owned queries, including resolution in other hooks.
    static RESOLVING_PATH: std::cell::Cell<bool> = const { std::cell::Cell::new(false) };
}

pub fn resolving_path() -> bool { RESOLVING_PATH.get() }

struct PathResolutionGuard(bool);
impl Drop for PathResolutionGuard {
    fn drop(&mut self) { RESOLVING_PATH.set(self.0); }
}

pub unsafe fn is_non_filesystem_handle(handle: HANDLE) -> bool {
    // A pipe/character handle has no filesystem identity. Classify it before
    // normalized-name lookup, including relative NT opens during CreatePipe.
    // Unknown or invalid handles must still take the fail-closed path. Keep
    // collector-owned type queries out of evidence and preserve last-error.
    let _guard = PathResolutionGuard(RESOLVING_PATH.replace(true));
    let saved_error = unsafe { windows_sys::Win32::Foundation::GetLastError() };
    let kind = unsafe { winapi::um::fileapi::GetFileType(handle) };
    unsafe { windows_sys::Win32::Foundation::SetLastError(saved_error) };
    kind == winapi::um::winbase::FILE_TYPE_PIPE
        || kind == winapi::um::winbase::FILE_TYPE_CHAR
}

pub unsafe fn get_path_name(handle: HANDLE) -> winsafe::SysResult<SmallVec<u16, MAX_PATH>> {
    let _guard = PathResolutionGuard(RESOLVING_PATH.replace(true));
    let mut path = SmallVec::<u16, MAX_PATH>::new();
    // SAFETY: FFI call to GetFinalPathNameByHandleW to query the file path from a handle
    let len = unsafe {
        GetFinalPathNameByHandleW(
            handle,
            path.as_mut_ptr(),
            path.capacity().try_into().unwrap(),
            0, /*FILE_NAME_NORMALIZED*/
        )
    };
    if len == 0 {
        return Err(winsafe::GetLastError());
    }
    let len = usize::try_from(len).unwrap();
    if len <= path.capacity() {
        // SAFETY: GetFinalPathNameByHandleW wrote `len` u16 characters into the buffer
        unsafe { path.set_len(len) };
    } else {
        path.reserve_exact(len);
        // SAFETY: FFI call to GetFinalPathNameByHandleW with larger buffer after first call indicated needed size
        let len = unsafe {
            GetFinalPathNameByHandleW(
                handle,
                path.as_mut_ptr(),
                path.capacity().try_into().unwrap(),
                0, /*FILE_NAME_NORMALIZED*/
            )
        };
        let len = usize::try_from(len).unwrap();
        if len == 0 {
            return Err(winsafe::GetLastError());
        } else if len > path.capacity() {
            unreachable!()
        }
        // SAFETY: GetFinalPathNameByHandleW wrote `len` u16 characters into the buffer
        unsafe { path.set_len(len) };
    }
    Ok(path)
}

pub const fn access_mask_to_mode(desired_access: ACCESS_MASK) -> AccessMode {
    let has_write = (desired_access & (FILE_WRITE_DATA | FILE_APPEND_DATA | GENERIC_WRITE
        | FILE_WRITE_ATTRIBUTES | FILE_WRITE_EA | DELETE | GENERIC_ALL)) != 0;
    let has_read = (desired_access & (FILE_READ_DATA | GENERIC_READ)) != 0;
    if has_write {
        if has_read { AccessMode::READ.union(AccessMode::WRITE) } else { AccessMode::WRITE }
    } else {
        AccessMode::READ
    }
}

pub struct HeapPath(PWSTR);
impl HeapPath {
    #[must_use]
    pub fn to_u16_str(&self) -> &U16Str {
        // SAFETY: the PWSTR was allocated by PathAllocCombine and is a valid null-terminated wide string
        unsafe { U16CStr::from_ptr_str(self.0).as_ustr() }
    }
}
impl Drop for HeapPath {
    fn drop(&mut self) {
        // SAFETY: freeing the PWSTR allocated by PathAllocCombine via LocalFree
        unsafe { LocalFree(self.0.cast()) };
    }
}

pub fn combine_paths(path1: &U16CStr, path2: &U16CStr) -> winsafe::SysResult<HeapPath> {
    let mut out = std::ptr::null_mut();
    // SAFETY: FFI call to PathAllocCombine with valid null-terminated wide string pointers
    let hr = unsafe {
        PathAllocCombine(
            path1.as_ptr(),
            path2.as_ptr(),
            PATHCCH_ALLOW_LONG_PATHS, /*PATHCOMBINE_DEFAULT*/
            &raw mut out,
        )
    };
    if hr != S_OK {
        // SAFETY: creating an ERROR from the HRESULT value
        return Err(unsafe { co::ERROR::from_raw(hr.try_into().unwrap()) });
    }
    Ok(HeapPath(out))
}

#[cfg(test)]
mod tests {
    use std::{
        ffi::OsString,
        fs::File,
        os::windows::{ffi::OsStringExt, io::AsRawHandle},
        path::PathBuf,
    };

    use super::get_path_name;

    #[test]
    fn detours_long_results_preserve_setup_failures() {
        use super::ck_long;
        use winsafe::co;
        assert_eq!(ck_long(0), Ok(()));
        assert_eq!(ck_long(5), Err(co::ERROR::ACCESS_DENIED));
        assert_eq!(ck_long(6), Err(co::ERROR::INVALID_HANDLE));
        // SAFETY: Detours explicitly rejects a null detour before reading any
        // target pointer. This exercises a real setup error without patching code.
        let failed_attach = unsafe {
            fspy_detours_sys::DetourAttach(std::ptr::null_mut(), std::ptr::null_mut())
        };
        assert_eq!(failed_attach, 87);
        assert_eq!(ck_long(failed_attach), Err(co::ERROR::INVALID_PARAMETER));
    }

    fn test_get_path_name(filename: &str) {
        let tmpdir = tempfile::tempdir().unwrap();
        let path = tmpdir.path().canonicalize().unwrap().join(filename);
        let file = File::create(&path).unwrap();
        // SAFETY: passing a valid raw file handle to get_path_name
        let actual_path = unsafe { get_path_name(file.as_raw_handle().cast()) }.unwrap();
        let actual_path = PathBuf::from(OsString::from_wide(&actual_path));
        assert_eq!(path, actual_path);
    }

    #[test]
    fn test_get_path_name_short() {
        test_get_path_name("foo");
    }
    #[test]
    fn test_get_path_name_long() {
        test_get_path_name(str::repeat("a", 255).as_str());
    }

    #[test]
    fn test_combine_path() {
        use widestring::u16cstr;

        use super::combine_paths;

        let path1 = u16cstr!("C:\\foo");
        let path2 = u16cstr!("bar\\baz");
        let combined = combine_paths(path1, path2).unwrap();
        assert_eq!(combined.to_u16_str(), u16cstr!("C:\\foo\\bar\\baz"));
    }
}

pub(crate) fn copy_process_bytes(address: usize, size: usize) -> Result<Vec<u8>, ()> {
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
