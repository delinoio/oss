use std::io;
use fspy_seccomp_unotify::supervisor::handler::arg::{Caller, Ignored};
use super::SyscallHandler;

impl SyscallHandler {
    // io_uring executes filesystem operations without their ordinary syscalls.
    // Setup must count too: SQPOLL can submit work without io_uring_enter.
    // Preserve every operand/result; remove this conservative loss only when
    // ring operations and their asynchronous lifetimes are fully observed.
    pub(super) fn io_uring_setup(&mut self, _: Caller, _: (Ignored,)) -> io::Result<()> {
        Err(io::Error::other("unsupported io_uring collection"))
    }
    pub(super) fn io_uring_enter(&mut self, _: Caller, _: (Ignored,)) -> io::Result<()> {
        Err(io::Error::other("unsupported io_uring collection"))
    }
    pub(super) fn io_uring_register(&mut self, _: Caller, _: (Ignored,)) -> io::Result<()> {
        Err(io::Error::other("unsupported io_uring collection"))
    }
}
