// SPDX-License-Identifier: Apache-2.0
//! A stable native lock that can be released by both `pthread_atfork`
//! callbacks.

use std::{
    cell::UnsafeCell,
    marker::PhantomData,
    mem::MaybeUninit,
    ops::{Deref, DerefMut},
};

pub(super) struct ForkMutex<T> {
    native: UnsafeCell<MaybeUninit<libc::pthread_mutex_t>>,
    value: UnsafeCell<T>,
}

// SAFETY: The native mutex protects every access to value. The allocation stays
// at one address after initialization, including across a copied fork image.
unsafe impl<T: Send> Sync for ForkMutex<T> {}
// SAFETY: The native allocation may be transferred before being published;
// moving the box never moves its initialized mutex or its protected value.
unsafe impl<T: Send> Send for ForkMutex<T> {}

impl<T> ForkMutex<T> {
    pub(super) fn new(value: T) -> Result<Box<Self>, i32> {
        let lock = Box::new(Self {
            native: UnsafeCell::new(MaybeUninit::uninit()),
            value: UnsafeCell::new(value),
        });
        let mut attributes = MaybeUninit::uninit();
        // SAFETY: Initialize native objects at their final addresses before any
        // publication. Only initialized attributes are destroyed below.
        unsafe {
            let result = libc::pthread_mutexattr_init(attributes.as_mut_ptr());
            if result != 0 {
                return Err(result);
            }
            let mut result = libc::pthread_mutexattr_settype(
                attributes.as_mut_ptr(),
                libc::PTHREAD_MUTEX_NORMAL,
            );
            if result == 0 {
                result = libc::pthread_mutex_init(lock.raw(), attributes.as_ptr());
            }
            libc::pthread_mutexattr_destroy(attributes.as_mut_ptr());
            if result != 0 {
                return Err(result);
            }
        }
        Ok(lock)
    }

    const fn raw(&self) -> *mut libc::pthread_mutex_t {
        self.native.get().cast()
    }

    pub(super) fn lock(&self) -> Result<Guard<'_, T>, i32> {
        self.prepare()?;
        Ok(Guard {
            lock: self,
            // Like std::sync::MutexGuard, a native guard cannot cross threads.
            thread: PhantomData,
        })
    }

    pub(super) fn prepare(&self) -> Result<(), i32> {
        // SAFETY: new initialized this stable allocation before publication.
        match unsafe { libc::pthread_mutex_lock(self.raw()) } {
            0 => Ok(()),
            code => Err(code),
        }
    }

    pub(super) unsafe fn release(&self) {
        // SAFETY: Only a current guard or a matching parent/child atfork callback may
        // release this normal mutex. No Rust guard is copied into the child.
        if unsafe { libc::pthread_mutex_unlock(self.raw()) } != 0 {
            // SAFETY: Immediate process termination avoids using corrupt state.
            unsafe { libc::_exit(125) };
        }
    }
}

pub(super) struct Guard<'a, T> {
    lock: &'a ForkMutex<T>,
    thread: PhantomData<*mut ()>,
}

impl<T> Deref for Guard<'_, T> {
    type Target = T;

    fn deref(&self) -> &T {
        // SAFETY: The guard owns the lock for the entire reference lifetime.
        unsafe { &*self.lock.value.get() }
    }
}

impl<T> DerefMut for Guard<'_, T> {
    fn deref_mut(&mut self) -> &mut T {
        // SAFETY: A unique guard owns the lock, preventing concurrent references.
        unsafe { &mut *self.lock.value.get() }
    }
}

impl<T> Drop for Guard<'_, T> {
    fn drop(&mut self) {
        // SAFETY: This guard still owns the native lock on its creating thread.
        unsafe { self.lock.release() };
    }
}
