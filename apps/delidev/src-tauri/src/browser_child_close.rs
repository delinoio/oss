// SPDX-License-Identifier: Apache-2.0
//! Each raw CEF client retains one original child lifetime across handler
//! getters.
use std::sync::Mutex;

#[derive(Default)]
struct Lifetime {
    browser: Option<i32>,
    scheduled: bool,
    completed: bool,
}

#[derive(Default)]
pub(crate) struct ChildClose(Mutex<Lifetime>);

impl ChildClose {
    pub(crate) fn created(&self, browser: i32) -> bool {
        let Ok(mut lifetime) = self.0.lock() else {
            return false;
        };
        if lifetime.browser.is_some() {
            return false;
        }
        lifetime.browser = Some(browser);
        true
    }

    pub(crate) fn schedule(&self, browser: i32) -> bool {
        let Ok(mut lifetime) = self.0.lock() else {
            return false;
        };
        if lifetime.browser != Some(browser) || lifetime.scheduled || lifetime.completed {
            return false;
        }
        lifetime.scheduled = true;
        true
    }

    pub(crate) fn should_destroy(&self, browser: i32) -> bool {
        self.0.lock().is_ok_and(|lifetime| {
            lifetime.browser == Some(browser) && lifetime.scheduled && !lifetime.completed
        })
    }

    pub(crate) fn completed(&self, browser: i32) -> bool {
        let Ok(mut lifetime) = self.0.lock() else {
            return false;
        };
        if lifetime.browser != Some(browser) || lifetime.completed {
            return false;
        }
        lifetime.completed = true;
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn original_child_close_is_scheduled_and_accounted_once() {
        let close = ChildClose::default();
        assert!(!close.schedule(1));
        assert!(close.created(1));
        assert!(!close.created(1));
        assert!(close.schedule(1));
        assert!(!close.schedule(1));
        assert!(close.should_destroy(1));
        assert!(close.completed(1));
        assert!(!close.completed(1));
        assert!(!close.should_destroy(1));
        assert!(!close.schedule(1));
    }

    #[test]
    fn late_original_callbacks_cannot_close_replacement_or_sibling() {
        let original = ChildClose::default();
        let replacement = ChildClose::default();
        let sibling = ChildClose::default();
        assert!(original.created(1));
        assert!(replacement.created(2));
        assert!(sibling.created(3));
        assert!(original.schedule(1));
        assert!(!original.schedule(2));
        assert!(!original.should_destroy(2));
        assert!(!original.completed(2));
        assert!(original.completed(1));
        assert!(!original.should_destroy(1));
        assert!(replacement.schedule(2));
        assert!(sibling.schedule(3));
        assert!(replacement.completed(2));
        assert!(sibling.completed(3));
    }

    #[test]
    fn native_completion_before_queued_destruction_fences_stale_handle() {
        let close = ChildClose::default();
        assert!(close.created(1));
        assert!(close.schedule(1));
        assert!(close.completed(1));
        assert!(!close.should_destroy(1));
        assert!(!close.created(2));
        assert!(!close.completed(2));
    }
}
