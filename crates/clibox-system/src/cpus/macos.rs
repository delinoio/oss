use std::{ffi::c_void, io, mem};

use super::{invalid_count, query_error};
use crate::error::Result;

const KEY: &[u8] = b"hw.logicalcpu\0";

pub(super) fn logical() -> Result<usize> {
    query_with(|key, value, size| {
        if unsafe { libc::sysctlbyname(key, value, size, std::ptr::null_mut(), 0) } == 0 {
            Ok(())
        } else {
            Err(io::Error::last_os_error())
        }
    })
}

fn query_with(
    call: impl FnOnce(*const libc::c_char, *mut c_void, *mut libc::size_t) -> io::Result<()>,
) -> Result<usize> {
    let mut value: libc::c_int = 0;
    let mut size = mem::size_of_val(&value);
    call(
        KEY.as_ptr().cast(),
        (&mut value as *mut libc::c_int).cast(),
        &mut size,
    )
    .map_err(|error| query_error(&error))?;
    if size != mem::size_of_val(&value) || value <= 0 {
        return Err(invalid_count());
    }
    Ok(value as usize)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::error::Code;

    #[test]
    fn queries_hw_logicalcpu_and_rejects_invalid_values() {
        let count = query_with(|key, value, size| {
            assert_eq!(
                unsafe { std::ffi::CStr::from_ptr(key) }.to_bytes(),
                b"hw.logicalcpu"
            );
            unsafe {
                *(value as *mut libc::c_int) = 12;
                *size = mem::size_of::<libc::c_int>();
            }
            Ok(())
        })
        .unwrap();
        assert_eq!(count, 12);
        assert_eq!(
            query_with(|_, _, _| Ok(())).unwrap_err().code,
            Code::EnumerationFailed
        );
        assert_eq!(
            query_with(|_, _, _| Err(io::ErrorKind::PermissionDenied.into()))
                .unwrap_err()
                .code,
            Code::PermissionDenied
        );
    }
}
