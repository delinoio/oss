mod create_process;
mod exit_process;
mod nt;

use constcat::concat_slices;

use super::detour::DetourAny;

pub const DETOURS: &[DetourAny] = concat_slices!([DetourAny]:
    create_process::DETOURS,
    exit_process::DETOURS,
    nt::DETOURS,
);
