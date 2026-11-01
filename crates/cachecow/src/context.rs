// SPDX-License-Identifier: Apache-2.0
//! Cancellation contexts for blocking, thread-based operations.
//!
//! A [`Context`] mirrors the parts of Go's `context.Context` that CacheCow
//! uses: cancellation, deadlines, interruptible sleeps, and `AfterFunc`
//! callbacks. Cancelling a parent cancels every derived context.

use std::{
    sync::{Arc, Condvar, Mutex, Weak},
    time::{Duration, Instant},
};

/// Why a context finished.
#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum CancelError {
    #[error("context canceled")]
    Canceled,
    #[error("context deadline exceeded")]
    DeadlineExceeded,
}

type Callback = Box<dyn FnOnce() + Send>;

struct State {
    done: Option<CancelError>,
    next_id: u64,
    callbacks: Vec<(u64, Callback)>,
    children: Vec<Weak<Inner>>,
}

struct Inner {
    state: Mutex<State>,
    changed: Condvar,
}

impl Inner {
    fn new() -> Arc<Self> {
        Arc::new(Self {
            state: Mutex::new(State {
                done: None,
                next_id: 0,
                callbacks: Vec::new(),
                children: Vec::new(),
            }),
            changed: Condvar::new(),
        })
    }

    fn finish(self: &Arc<Self>, reason: CancelError) {
        let (callbacks, children) = {
            let mut s = self
                .state
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if s.done.is_some() {
                return;
            }
            s.done = Some(reason);
            (
                std::mem::take(&mut s.callbacks),
                std::mem::take(&mut s.children),
            )
        };
        self.changed.notify_all();
        for (_, f) in callbacks {
            std::thread::spawn(f);
        }
        for child in children {
            if let Some(child) = child.upgrade() {
                child.finish(reason);
            }
        }
    }

    fn done(&self) -> Option<CancelError> {
        self.state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner)
            .done
    }
}

/// A cancellable context. Cloning shares the same cancellation state.
#[derive(Clone)]
pub struct Context {
    inner: Arc<Inner>,
}

impl Default for Context {
    fn default() -> Self {
        Self::background()
    }
}

impl std::fmt::Debug for Context {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("Context")
            .field("done", &self.inner.done())
            .finish()
    }
}

/// Cancels the context it was created with. Dropping it does not cancel.
#[derive(Clone)]
pub struct CancelHandle {
    inner: Arc<Inner>,
}

impl CancelHandle {
    pub fn cancel(&self) {
        self.inner.finish(CancelError::Canceled);
    }
}

/// Registration returned by [`Context::after_func`]. Dropping it, or calling
/// [`AfterFunc::stop`], unregisters a callback that has not started yet.
pub struct AfterFunc {
    inner: Weak<Inner>,
    id: u64,
}

impl AfterFunc {
    /// Returns true when the callback was unregistered before it started.
    pub fn stop(&self) -> bool {
        let Some(inner) = self.inner.upgrade() else {
            return false;
        };
        let mut s = inner
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let before = s.callbacks.len();
        s.callbacks.retain(|(id, _)| *id != self.id);
        before != s.callbacks.len()
    }
}

impl Drop for AfterFunc {
    fn drop(&mut self) {
        self.stop();
    }
}

impl Context {
    /// A context that is never cancelled.
    pub fn background() -> Self {
        Self {
            inner: Inner::new(),
        }
    }

    fn child(&self) -> Self {
        let child = Inner::new();
        let parent_done = {
            let mut s = self
                .inner
                .state
                .lock()
                .unwrap_or_else(std::sync::PoisonError::into_inner);
            if s.done.is_none() {
                s.children.retain(|w| w.strong_count() > 0);
                s.children.push(Arc::downgrade(&child));
            }
            s.done
        };
        if let Some(reason) = parent_done {
            child.finish(reason);
        }
        Self { inner: child }
    }

    /// Derives a context cancelled by the returned handle or by this context.
    pub fn with_cancel(&self) -> (Self, CancelHandle) {
        let child = self.child();
        let handle = CancelHandle {
            inner: child.inner.clone(),
        };
        (child, handle)
    }

    /// Derives a context that also finishes with
    /// [`CancelError::DeadlineExceeded`] after `timeout`.
    pub fn with_timeout(&self, timeout: Duration) -> (Self, CancelHandle) {
        let (child, handle) = self.with_cancel();
        let weak = Arc::downgrade(&child.inner);
        let deadline = Instant::now() + timeout;
        std::thread::spawn(move || {
            loop {
                let Some(inner) = weak.upgrade() else { return };
                let s = inner
                    .state
                    .lock()
                    .unwrap_or_else(std::sync::PoisonError::into_inner);
                if s.done.is_some() {
                    return;
                }
                let now = Instant::now();
                if now >= deadline {
                    drop(s);
                    inner.finish(CancelError::DeadlineExceeded);
                    return;
                }
                // Wake periodically so a dropped context releases this thread.
                let wait = (deadline - now).min(Duration::from_millis(250));
                let _unused = inner
                    .changed
                    .wait_timeout(s, wait)
                    .unwrap_or_else(std::sync::PoisonError::into_inner);
            }
        });
        (child, handle)
    }

    /// Returns the cancellation reason once the context is done.
    pub fn err(&self) -> Result<(), CancelError> {
        self.inner.done().map_or(Ok(()), Err)
    }

    pub fn is_done(&self) -> bool {
        self.inner.done().is_some()
    }

    /// Sleeps for `duration` unless the context finishes first.
    pub fn sleep(&self, duration: Duration) -> Result<(), CancelError> {
        let deadline = Instant::now() + duration;
        let mut s = self
            .inner
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        loop {
            if let Some(reason) = s.done {
                return Err(reason);
            }
            let now = Instant::now();
            if now >= deadline {
                return Ok(());
            }
            s = self
                .inner
                .changed
                .wait_timeout(s, deadline - now)
                .unwrap_or_else(std::sync::PoisonError::into_inner)
                .0;
        }
    }

    /// Blocks until the context finishes.
    pub fn wait(&self) -> CancelError {
        let mut s = self
            .inner
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        loop {
            if let Some(reason) = s.done {
                return reason;
            }
            s = self
                .inner
                .changed
                .wait(s)
                .unwrap_or_else(std::sync::PoisonError::into_inner);
        }
    }

    /// Runs `f` on a new thread once the context finishes, like Go's
    /// `context.AfterFunc`. Runs immediately when the context is already done.
    pub fn after_func(&self, f: impl FnOnce() + Send + 'static) -> AfterFunc {
        let mut s = self
            .inner
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let id = s.next_id;
        s.next_id += 1;
        if s.done.is_some() {
            drop(s);
            std::thread::spawn(f);
        } else {
            s.callbacks.push((id, Box::new(f)));
        }
        AfterFunc {
            inner: Arc::downgrade(&self.inner),
            id,
        }
    }
}

#[cfg(test)]
mod tests {
    use std::sync::atomic::{AtomicBool, Ordering};

    use super::*;

    #[test]
    fn cancel_propagates_to_children_and_callbacks() {
        let root = Context::background();
        let (parent, cancel) = root.with_cancel();
        let (child, _keep) = parent.with_cancel();
        let fired = Arc::new(AtomicBool::new(false));
        let flag = fired.clone();
        let _registration = child.after_func(move || flag.store(true, Ordering::SeqCst));
        assert!(child.err().is_ok());
        cancel.cancel();
        assert_eq!(child.wait(), CancelError::Canceled);
        for _ in 0..100 {
            if fired.load(Ordering::SeqCst) {
                break;
            }
            std::thread::sleep(Duration::from_millis(10));
        }
        assert!(fired.load(Ordering::SeqCst));
        assert!(root.err().is_ok());
    }

    #[test]
    fn timeout_interrupts_sleep() {
        let (ctx, _cancel) = Context::background().with_timeout(Duration::from_millis(50));
        let started = Instant::now();
        assert_eq!(
            ctx.sleep(Duration::from_secs(5)),
            Err(CancelError::DeadlineExceeded)
        );
        assert!(started.elapsed() < Duration::from_secs(2));
    }

    #[test]
    fn stopped_after_func_does_not_run() {
        let (ctx, cancel) = Context::background().with_cancel();
        let fired = Arc::new(AtomicBool::new(false));
        let flag = fired.clone();
        let registration = ctx.after_func(move || flag.store(true, Ordering::SeqCst));
        assert!(registration.stop());
        cancel.cancel();
        std::thread::sleep(Duration::from_millis(50));
        assert!(!fired.load(Ordering::SeqCst));
    }
}
