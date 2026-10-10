// SPDX-License-Identifier: Apache-2.0

/// One raw CEF child's close ownership. Old generations remain independently
/// owned after the product view selects a replacement child.
pub(crate) struct ChildClose {
    generation: u64,
    browser: Option<i32>,
    phase: ClosePhase,
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum ClosePhase {
    Live,
    Queued,
    Destroying,
    Closed,
}

impl ChildClose {
    pub(crate) fn new(generation: u64) -> Self {
        Self {
            generation,
            browser: None,
            phase: ClosePhase::Live,
        }
    }

    pub(crate) fn bind(&mut self, browser: i32, generation: u64) -> bool {
        if browser <= 0 || generation != self.generation || self.browser.is_some() {
            return false;
        }
        self.browser = Some(browser);
        true
    }

    fn owns(&self, browser: i32, generation: u64) -> bool {
        self.browser == Some(browser) && self.generation == generation
    }

    pub(crate) fn queue(&mut self, browser: i32, generation: u64) -> bool {
        if !self.owns(browser, generation) || self.phase != ClosePhase::Live {
            return false;
        }
        self.phase = ClosePhase::Queued;
        true
    }

    pub(crate) fn begin_destroy(&mut self, browser: i32, generation: u64) -> bool {
        if !self.owns(browser, generation) || self.phase != ClosePhase::Queued {
            return false;
        }
        self.phase = ClosePhase::Destroying;
        true
    }

    pub(crate) fn retry(&mut self, browser: i32, generation: u64) {
        if self.owns(browser, generation) && self.phase != ClosePhase::Closed {
            self.phase = ClosePhase::Live;
        }
    }

    /// Only the original on_before_close callback releases live accounting.
    pub(crate) fn finish(&mut self, browser: i32, generation: u64) -> bool {
        if !self.owns(browser, generation) || self.phase == ClosePhase::Closed {
            return false;
        }
        self.phase = ClosePhase::Closed;
        true
    }
}

#[cfg(test)]
mod tests {
    use super::ChildClose;

    #[test]
    fn duplicate_close_requests_and_callbacks_release_only_once() {
        let mut close = ChildClose::new(7);
        assert!(close.bind(11, 7));
        assert!(!close.bind(11, 7));
        assert!(close.queue(11, 7));
        assert!(!close.queue(11, 7));
        assert!(close.begin_destroy(11, 7));
        assert!(!close.begin_destroy(11, 7));
        assert!(close.finish(11, 7));
        assert!(!close.finish(11, 7));
        assert!(!close.queue(11, 7));
    }

    #[test]
    fn old_child_destruction_cannot_claim_replacement_or_sibling() {
        let mut old = ChildClose::new(7);
        let mut replacement = ChildClose::new(8);
        let mut sibling = ChildClose::new(9);
        assert!(old.bind(11, 7));
        assert!(replacement.bind(12, 8));
        assert!(sibling.bind(13, 9));
        assert!(old.queue(11, 7));
        assert!(!old.begin_destroy(12, 8));
        assert!(!old.finish(13, 9));
        assert!(old.begin_destroy(11, 7));
        assert!(old.finish(11, 7));
        assert!(replacement.queue(12, 8));
        assert!(sibling.queue(13, 9));
    }

    #[test]
    fn completed_child_fences_an_already_queued_ui_callback() {
        let mut close = ChildClose::new(7);
        assert!(close.bind(11, 7));
        assert!(close.queue(11, 7));
        assert!(close.finish(11, 7));
        assert!(!close.begin_destroy(11, 7));
        close.retry(11, 7);
        assert!(!close.queue(11, 7));
    }

    #[test]
    fn dispatch_failure_preserves_original_close_for_retry() {
        let mut close = ChildClose::new(7);
        assert!(close.bind(11, 7));
        assert!(close.queue(11, 7));
        close.retry(12, 8);
        assert!(!close.queue(11, 7));
        close.retry(11, 7);
        assert!(close.queue(11, 7));
        assert!(close.begin_destroy(11, 7));
        close.retry(11, 7);
        assert!(close.queue(11, 7));
        assert!(close.begin_destroy(11, 7));
        assert!(close.finish(11, 7));
    }

    #[test]
    fn unbound_or_foreign_child_never_acquires_close_authority() {
        let mut close = ChildClose::new(7);
        assert!(!close.queue(11, 7));
        assert!(!close.finish(11, 7));
        assert!(!close.bind(0, 7));
        assert!(!close.bind(11, 8));
        assert!(close.bind(11, 7));
        assert!(!close.queue(11, 8));
        assert!(!close.queue(12, 7));
        assert!(!close.finish(12, 7));
        assert!(close.finish(11, 7));
    }
}
