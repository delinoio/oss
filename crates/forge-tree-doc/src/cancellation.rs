//! Cooperative cancellation scoped to one synchronous engine operation.
use std::{
    cell::RefCell,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
};

use crate::{ErrorCode, Result, error};

thread_local! {
    static ACTIVE: RefCell<Option<Arc<AtomicBool>>> = const { RefCell::new(None) };
}

/// Install a worker-owned flag without changing the existing engine APIs or
/// their default behavior. Nested scopes and unwinding restore the prior flag;
/// callers spawning another engine thread must explicitly scope that thread.
pub fn with_cancellation<T>(flag: Arc<AtomicBool>, work: impl FnOnce() -> T) -> T {
    struct Restore(Option<Arc<AtomicBool>>);
    impl Drop for Restore {
        fn drop(&mut self) {
            ACTIVE.with(|active| {
                active.replace(self.0.take());
            });
        }
    }
    let _restore = Restore(ACTIVE.with(|active| active.replace(Some(flag))));
    work()
}

pub fn checkpoint() -> Result<()> {
    if ACTIVE.with(|active| {
        active
            .borrow()
            .as_ref()
            .is_some_and(|flag| flag.load(Ordering::Acquire))
    }) {
        return error(
            ErrorCode::Cancelled,
            "",
            "The document operation was cancelled",
        );
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn scopes_restore_after_nested_work_and_unwinding_and_do_not_cross_threads() {
        let cancelled = Arc::new(AtomicBool::new(true));
        with_cancellation(cancelled.clone(), || {
            assert_eq!(checkpoint().unwrap_err().code, ErrorCode::Cancelled);
            with_cancellation(Arc::new(AtomicBool::new(false)), || checkpoint().unwrap());
            assert_eq!(checkpoint().unwrap_err().code, ErrorCode::Cancelled);
            std::thread::spawn(|| checkpoint().unwrap()).join().unwrap();
        });
        checkpoint().unwrap();
        let _ = std::panic::catch_unwind(|| with_cancellation(cancelled, || panic!("test unwind")));
        checkpoint().unwrap();
    }
}
