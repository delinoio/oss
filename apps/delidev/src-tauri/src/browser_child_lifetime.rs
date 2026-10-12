//! Exact receipts for one raw CEF child. Native destruction is not a close
//! acknowledgment.

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ChildIdentity {
    pub browser_id: i32,
    pub generation: u64,
    pub child: usize,
    pub parent: usize,
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
enum Phase {
    #[default]
    AwaitingCreation,
    Live,
    Queued,
    Destroying,
    Destroyed,
    Closed,
}

#[derive(Default)]
pub struct ChildLifetime {
    identity: Option<ChildIdentity>,
    phase: Phase,
}

impl ChildLifetime {
    pub fn created(&mut self, identity: ChildIdentity) -> bool {
        if self.phase != Phase::AwaitingCreation {
            return false;
        }
        self.identity = Some(identity);
        self.phase = Phase::Live;
        true
    }

    pub fn queue(&mut self, browser_id: i32, generation: u64) -> Option<ChildIdentity> {
        let identity = self.identity?;
        if self.phase != Phase::Live
            || identity.browser_id != browser_id
            || identity.generation != generation
        {
            return None;
        }
        self.phase = Phase::Queued;
        Some(identity)
    }

    pub fn dispatch_failed(&mut self, identity: ChildIdentity) {
        if self.identity == Some(identity) && self.phase == Phase::Queued {
            self.phase = Phase::Live;
        }
    }

    pub fn begin_destroy(&mut self, identity: ChildIdentity) -> bool {
        if self.identity != Some(identity) || self.phase != Phase::Queued {
            return false;
        }
        self.phase = Phase::Destroying;
        true
    }

    pub fn finish_destroy(&mut self, identity: ChildIdentity, success: bool) {
        // Native destruction can synchronously reenter on_before_close. Never
        // reopen an acknowledged receipt when the native call returns.
        if self.identity == Some(identity) && self.phase == Phase::Destroying {
            self.phase = if success {
                Phase::Destroyed
            } else {
                Phase::Live
            };
        }
    }

    pub fn closed(&mut self, browser_id: i32, generation: u64) -> bool {
        if self.phase == Phase::Closed
            || self.identity.is_none_or(|identity| {
                identity.browser_id != browser_id || identity.generation != generation
            })
        {
            return false;
        }
        self.phase = Phase::Closed;
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn child() -> ChildIdentity {
        ChildIdentity {
            browser_id: 41,
            generation: 7,
            child: 200,
            parent: 100,
        }
    }

    #[test]
    fn duplicate_callbacks_share_one_destruction_and_one_acknowledgment() {
        let mut receipt = ChildLifetime::default();
        let original = child();
        assert!(receipt.created(original));
        assert!(!receipt.created(original));
        assert_eq!(receipt.queue(41, 7), Some(original));
        assert_eq!(receipt.queue(41, 7), None);
        assert!(receipt.begin_destroy(original));
        assert!(!receipt.begin_destroy(original));
        receipt.finish_destroy(original, true);
        assert_eq!(receipt.queue(41, 7), None);
        assert!(receipt.closed(41, 7));
        assert!(!receipt.closed(41, 7));
    }

    #[test]
    fn old_callbacks_cannot_claim_replacement_or_sibling_identity() {
        let mut receipt = ChildLifetime::default();
        let original = child();
        receipt.created(original);
        assert_eq!(receipt.queue(42, 7), None);
        assert_eq!(receipt.queue(41, 8), None);
        assert!(!receipt.closed(42, 7));
        assert!(!receipt.closed(41, 8));
        receipt.queue(41, 7);
        for foreign in [
            ChildIdentity {
                browser_id: 42,
                ..original
            },
            ChildIdentity {
                generation: 8,
                ..original
            },
            ChildIdentity {
                child: 201,
                ..original
            },
            ChildIdentity {
                parent: 101,
                ..original
            },
        ] {
            assert!(!receipt.begin_destroy(foreign));
            receipt.dispatch_failed(foreign);
            receipt.finish_destroy(foreign, true);
        }
        assert!(receipt.begin_destroy(original));
    }

    #[test]
    fn failed_dispatch_and_destruction_preserve_pending_accounting() {
        let mut receipt = ChildLifetime::default();
        let original = child();
        receipt.created(original);
        receipt.queue(41, 7);
        receipt.dispatch_failed(original);
        assert_eq!(receipt.queue(41, 7), Some(original));
        assert!(receipt.begin_destroy(original));
        receipt.finish_destroy(original, false);
        assert_eq!(receipt.queue(41, 7), Some(original));
        assert!(receipt.begin_destroy(original));
        receipt.finish_destroy(original, true);
        assert!(receipt.closed(41, 7));
    }

    #[test]
    fn before_close_cancels_queued_task_and_survives_native_reentrancy() {
        for during_destruction in [false, true] {
            let mut receipt = ChildLifetime::default();
            let original = child();
            receipt.created(original);
            receipt.queue(41, 7);
            if during_destruction {
                assert!(receipt.begin_destroy(original));
            }
            assert!(receipt.closed(41, 7));
            receipt.finish_destroy(original, false);
            receipt.dispatch_failed(original);
            assert!(!receipt.begin_destroy(original));
            assert_eq!(receipt.queue(41, 7), None);
            assert!(!receipt.closed(41, 7));
        }
    }

    #[test]
    fn linux_default_native_close_and_pending_creation_acknowledge_only_original() {
        let mut receipt = ChildLifetime::default();
        assert!(!receipt.closed(41, 7));
        receipt.created(child());
        assert!(receipt.closed(41, 7));
        assert!(!receipt.closed(41, 7));
        assert_eq!(receipt.queue(41, 7), None);
    }
}
