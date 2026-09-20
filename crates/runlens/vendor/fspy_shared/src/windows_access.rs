//! Pure classification shared with the Windows hooks and portable regressions.
use crate::ipc::AccessMode;

// NT disposition values and FILE_DELETE_ON_CLOSE are part of the native ABI:
// https://learn.microsoft.com/en-us/windows-hardware/drivers/ddi/ntifs/nf-ntifs-ntcreatefile
pub const fn creation_mode(mode: AccessMode, disposition: u32, options: u32) -> AccessMode {
    let mode = match disposition {
        1 => mode, // FILE_OPEN is the only non-creating disposition.
        0 | 2..=5 => mode.union(AccessMode::WRITE),
        _ => mode.union(AccessMode::UNSUPPORTED),
    };
    if options & 0x0000_1000 != 0 { mode.union(AccessMode::WRITE) } else { mode }
}
