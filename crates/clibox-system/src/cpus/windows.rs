use windows_sys::Win32::System::Threading::{GetActiveProcessorCount, ALL_PROCESSOR_GROUPS};

use super::{invalid_count, query_error};
use crate::error::Result;

pub(super) fn logical() -> Result<usize> {
    count_with(|groups| {
        let count = unsafe { GetActiveProcessorCount(groups) };
        if count == 0 {
            Err(std::io::Error::last_os_error())
        } else {
            Ok(count)
        }
    })
}

fn count_with(get: impl FnOnce(u16) -> std::io::Result<u32>) -> Result<usize> {
    let count = get(ALL_PROCESSOR_GROUPS).map_err(|error| query_error(&error))?;
    if count == 0 {
        return Err(invalid_count());
    }
    Ok(count as usize)
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::error::Code;

    #[test]
    fn includes_all_processor_groups_and_rejects_zero() {
        assert_eq!(
            count_with(|groups| {
                assert_eq!(groups, ALL_PROCESSOR_GROUPS);
                Ok(128)
            })
            .unwrap(),
            128
        );
        assert_eq!(
            count_with(|_| Ok(0)).unwrap_err().code,
            Code::EnumerationFailed
        );
        assert_eq!(
            count_with(|_| Err(std::io::ErrorKind::PermissionDenied.into()))
                .unwrap_err()
                .code,
            Code::PermissionDenied
        );
    }
}
