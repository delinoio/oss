// SPDX-License-Identifier: Apache-2.0
//! Direct Darwin directory reads use the shared open-file offset for overlays.

use std::{ffi::CStr, ptr};

use libc::{__error, EFAULT, EINVAL, EIO, ENAMETOOLONG, c_char, c_int, c_void, dirent, ssize_t};

use super::{
    DirectoryStream, Guard, RUNTIME, directory_cookie, directory_entry, errno, fail,
    mach_vm_read_overwrite, path_from, translate_lookup,
};

// Darwin's extended getdirentries64 ABI reserves the final u32 at this size.
// https://github.com/apple-oss-distributions/xnu/blob/main/bsd/sys/dirent_private.h
const EXTENDED_BUFFER_SIZE: usize = 1024;
const EXTENDED_EOF: u32 = 1;

#[derive(Clone, Copy)]
enum RecordLayout {
    Legacy,
    Inode64,
}

unsafe fn copyout(bytes: &[u8], output: *mut u8) -> std::result::Result<(), c_int> {
    let mut copied = 0;
    #[expect(deprecated, reason = "the injected client avoids a mach2 dependency")]
    let status = mach_vm_read_overwrite(
        libc::mach_task_self_,
        bytes.as_ptr() as u64,
        bytes.len() as u64,
        output as u64,
        &raw mut copied,
    );
    if status != libc::KERN_SUCCESS || copied != bytes.len() as u64 {
        return Err(EFAULT);
    }
    Ok(())
}

unsafe fn write_flags(
    buffer: *mut u8,
    capacity: usize,
    layout: RecordLayout,
    end: bool,
) -> std::result::Result<(), c_int> {
    if matches!(layout, RecordLayout::Inode64) && capacity >= EXTENDED_BUFFER_SIZE {
        let flags = if end { EXTENDED_EOF } else { 0 };
        copyout(&flags.to_ne_bytes(), buffer.wrapping_add(capacity - 4))?;
    }
    Ok(())
}

unsafe fn merged_entries(
    fd: c_int,
    buffer: *mut u8,
    capacity: usize,
    base: *mut i64,
    layout: RecordLayout,
    native: impl FnOnce() -> ssize_t,
) -> ssize_t {
    if capacity == 0 {
        return native();
    }
    let Some(_guard) = Guard::enter() else {
        return native();
    };
    let Some(runtime) = RUNTIME.get() else {
        return native();
    };
    let saved_errno = *__error();
    let Ok(mut runtime) = runtime.lock() else {
        errno(EIO);
        return -1;
    };
    let result = (|| -> std::result::Result<ssize_t, c_int> {
        // Validate the live descriptor and retain intercepted peer identity.
        // Unmanaged descriptors and native descriptor errors remain libc's.
        let Ok(logical) = path_from(c".".as_ptr(), fd, &runtime) else {
            errno(saved_errno);
            return Ok(native());
        };
        let translation =
            translate_lookup(&mut runtime, &logical, true).map_err(|error| fail(error.code))?;
        let entries = runtime
            .view
            .directory_entries(&translation.logical, &translation.physical)
            .map_err(|error| fail(error.code))?;
        if entries.is_empty() {
            errno(saved_errno);
            return Ok(native());
        }
        let current = libc::lseek(fd, 0, libc::SEEK_CUR);
        if current < 0 {
            errno(saved_errno);
            return Ok(native());
        }
        let begin = directory_cookie(0, entries.len());
        let position = if current >= begin {
            usize::try_from(current - begin).map_err(|_| EIO)?
        } else {
            errno(saved_errno);
            let returned = native();
            if returned != 0 {
                if returned > 0 {
                    // Native EOF can accompany the last physical block. Keep
                    // callers reading until all virtual entries are returned.
                    if libc::lseek(fd, 0, libc::SEEK_CUR) >= begin {
                        return Err(EIO);
                    }
                    write_flags(buffer, capacity, layout, false)?;
                    errno(saved_errno);
                }
                return Ok(returned);
            }
            0
        };
        let mut stream = DirectoryStream {
            logical: translation.logical,
            physical: translation.physical,
            position,
            virtual_end: position == entries.len(),
            entry: Box::new(std::mem::zeroed()),
        };
        let entry = directory_entry(&mut stream, &entries)?;
        let cookie = directory_cookie(position, entries.len());
        let length = write_record(entry, buffer, capacity, layout)?;
        copyout(&cookie.to_ne_bytes(), base.cast())?;
        write_flags(buffer, capacity, layout, stream.virtual_end)?;
        // Kernel offset publication shares progress with dup and fork, and
        // naturally resets after lseek, close or descriptor reuse. Never pass
        // reserved cookies to native getdirentries: APFS rejects them.
        let next = directory_cookie(stream.position, entries.len());
        if libc::lseek(fd, next, libc::SEEK_SET) != next {
            return Err(*__error());
        }
        errno(saved_errno);
        Ok(length.cast_signed())
    })();
    match result {
        Ok(value) => value,
        Err(code) => {
            errno(code);
            -1
        }
    }
}

unsafe fn write_record(
    entry: *const dirent,
    buffer: *mut u8,
    capacity: usize,
    layout: RecordLayout,
) -> std::result::Result<usize, c_int> {
    if entry.is_null() {
        return Ok(0);
    }
    let entry = &*entry;
    let mut legacy = [0u8; 264];
    let bytes = match layout {
        RecordLayout::Inode64 => std::slice::from_raw_parts(
            ptr::from_ref(entry).cast::<u8>(),
            usize::from(entry.d_reclen),
        ),
        RecordLayout::Legacy => {
            let name = CStr::from_ptr(entry.d_name.as_ptr()).to_bytes();
            let name_length = u8::try_from(name.len()).map_err(|_| ENAMETOOLONG)?;
            let length = (8 + name.len() + 1).next_multiple_of(4);
            let record_length = u16::try_from(length).map_err(|_| ENAMETOOLONG)?;
            legacy[..4].copy_from_slice(&1u32.to_ne_bytes());
            legacy[4..6].copy_from_slice(&record_length.to_ne_bytes());
            legacy[6] = entry.d_type;
            legacy[7] = name_length;
            legacy[8..8 + name.len()].copy_from_slice(name);
            &legacy[..length]
        }
    };
    let available = if matches!(layout, RecordLayout::Inode64) && capacity >= EXTENDED_BUFFER_SIZE {
        capacity - 4
    } else {
        capacity
    };
    if bytes.len() > available {
        return Err(EINVAL);
    }
    copyout(bytes, buffer)?;
    Ok(bytes.len())
}

unsafe extern "C" fn pnport_getdirentries64(
    fd: c_int,
    buffer: *mut u8,
    capacity: usize,
    base: *mut i64,
) -> ssize_t {
    merged_entries(fd, buffer, capacity, base, RecordLayout::Inode64, || {
        crate::libc::__getdirentries64(fd, buffer, capacity, base)
    })
}

unsafe extern "C" fn pnport_getdirentries(
    fd: c_int,
    buffer: *mut c_char,
    capacity: c_int,
    base: *mut libc::c_long,
) -> c_int {
    let Ok(count) = usize::try_from(capacity) else {
        return crate::libc::getdirentries(fd, buffer, capacity, base);
    };
    let result = merged_entries(
        fd,
        buffer.cast(),
        count,
        base.cast(),
        RecordLayout::Legacy,
        || crate::libc::getdirentries(fd, buffer, capacity, base) as ssize_t,
    );
    c_int::try_from(result).expect("Directory reads cannot exceed the supplied c_int capacity")
}

const _: () = {
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut INODE64: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_getdirentries64 as *const c_void,
        _old: crate::libc::__getdirentries64 as *const c_void,
    };
    #[used]
    #[unsafe(link_section = "__DATA,__interpose")]
    static mut LEGACY: crate::macros::InterposeEntry = crate::macros::InterposeEntry {
        _new: pnport_getdirentries as *const c_void,
        _old: crate::libc::getdirentries as *const c_void,
    };
};
