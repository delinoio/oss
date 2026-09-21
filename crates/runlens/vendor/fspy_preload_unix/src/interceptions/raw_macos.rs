//! Preserve Darwin's variadic syscall ABI while marking bypassed collection.
use crate::{client::global_client, macros::{InterposeEntry, intercept}};

#[unsafe(no_mangle)]
extern "C" fn runlens_note_raw_syscall() {
    // SAFETY: __error returns the current thread's errno storage. Reporting must
    // not alter errno before the original syscall (or fork) runs.
    unsafe {
        let saved = *libc::__error();
        if let Some(client) = global_client() { client.report_failure(); }
        *libc::__error() = saved;
    }
}

intercept!(fork: unsafe extern "C" fn() -> libc::pid_t);
unsafe extern "C" fn fork() -> libc::pid_t {
    // A forked macOS child can invoke kernel operations without going through
    // any interposable symbol. Classify before the fork, even if it later fails.
    runlens_note_raw_syscall();
    unsafe { fork::original()() }
}

unsafe extern "C" { fn runlens_raw_syscall(number: libc::c_int, ...) -> libc::c_int; }
#[used]
#[unsafe(link_section = "__DATA,__interpose")]
static mut SYSCALL: InterposeEntry = InterposeEntry {
    _new: runlens_raw_syscall as _, _old: libc::syscall as _,
};

// Do not read a guessed number of variadic arguments: callers legitimately pass
// zero or up to eight operands. Preserve all integer argument registers and the
// untouched caller stack, report loss, then tail-call libSystem's implementation.
// Darwin arm64 places variadic arguments on the stack; x64 uses six registers.
// Remove these ABI adapters when the backend supplies kernel lifecycle coverage.
#[cfg(target_arch = "aarch64")]
core::arch::global_asm!(r#"
.text
.p2align 2
.globl _runlens_raw_syscall
_runlens_raw_syscall:
    stp x29, x30, [sp, #-16]!
    mov x29, sp
    sub sp, sp, #80
    stp x0, x1, [sp, #0]
    stp x2, x3, [sp, #16]
    stp x4, x5, [sp, #32]
    stp x6, x7, [sp, #48]
    stp x8, x9, [sp, #64]
    bl _runlens_note_raw_syscall
    ldp x0, x1, [sp, #0]
    ldp x2, x3, [sp, #16]
    ldp x4, x5, [sp, #32]
    ldp x6, x7, [sp, #48]
    ldp x8, x9, [sp, #64]
    add sp, sp, #80
    ldp x29, x30, [sp], #16
    b _syscall
"#);
#[cfg(target_arch = "x86_64")]
core::arch::global_asm!(r#"
.text
.p2align 4
.globl _runlens_raw_syscall
_runlens_raw_syscall:
    push rax
    push rdi
    push rsi
    push rdx
    push rcx
    push r8
    push r9
    call _runlens_note_raw_syscall
    pop r9
    pop r8
    pop rcx
    pop rdx
    pop rsi
    pop rdi
    pop rax
    jmp _syscall
"#);
