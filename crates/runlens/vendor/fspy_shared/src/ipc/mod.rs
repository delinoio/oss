#[cfg(not(target_env = "musl"))]
pub mod channel;
mod ipc_path;
use std::fmt::Debug;

use bitflags::bitflags;
pub use fspy_ipc_str::IpcStr;
pub use ipc_path::IpcPath;
use wincode::{SchemaRead, SchemaWrite};

#[derive(SchemaWrite, SchemaRead, PartialEq, Eq, PartialOrd, Ord, Hash, Clone, Copy)]
pub struct AccessMode(u8);

bitflags! {
    impl AccessMode: u8 {
        const READ = 1;
        const WRITE = 1 << 1;
        const READ_DIR = 1 << 2;
        // Runlens: an observed child cannot be injected without changing it.
        const UNSUPPORTED = 1 << 3;
        const ATTACHED = 1 << 4;
        // Private native image identity; never a filesystem access.
        const IMAGE = 1 << 5;
        // Private path replacement/removal attempt, used to invalidate ancestry.
        const PATH_MUTATION = 1 << 6;
    }
}

impl Debug for AccessMode {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        struct InternalAccessMode(AccessMode);
        impl Debug for InternalAccessMode {
            fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                bitflags::parser::to_writer(&self.0, f)
            }
        }
        f.debug_tuple("AccessMode").field(&InternalAccessMode(*self)).finish()
    }
}

#[derive(SchemaWrite, SchemaRead, Debug, Clone, Copy, PartialEq, Eq)]
pub struct PathAccess<'a> {
    pub mode: AccessMode,
    pub path: &'a IpcPath,
    // TODO: add follow_symlinks (O_NOFOLLOW)
}

impl<'a> PathAccess<'a> {
    pub fn read(path: impl Into<&'a IpcPath>) -> Self {
        Self { mode: AccessMode::READ, path: path.into() }
    }

    pub fn read_dir(path: impl Into<&'a IpcPath>) -> Self {
        Self { mode: AccessMode::READ_DIR, path: path.into() }
    }
}
