// SPDX-License-Identifier: Apache-2.0
//! A focused native capture keeps its fence after admission expires. Restoring
//! accelerators under a suspended renderer would let a captured key close it.
use std::{
    collections::BTreeSet,
    time::{Duration, Instant},
};

pub const ADMISSION: Duration = Duration::from_secs(15);
#[derive(Default)]
pub struct CaptureGate {
    pub epoch: u64,
    owner: Option<(String, String, Instant)>,
    paused: bool,
    retired: BTreeSet<(String, String)>,
}
impl CaptureGate {
    pub fn begin(&mut self, owner: String, token: String, now: Instant) -> bool {
        if self.owner.is_some()
            || self.retired.len() >= 1024
            || self.retired.contains(&(owner.clone(), token.clone()))
        {
            return false;
        }
        self.epoch += 1;
        self.owner = Some((owner, token, now + ADMISSION));
        true
    }

    pub fn fenced(&self) -> bool {
        self.owner.is_some() && !self.paused
    }

    pub fn owned(&self) -> bool {
        self.owner.is_some()
    }

    pub fn pause(&mut self) {
        self.paused = true;
        if let Some((_, _, until)) = &mut self.owner {
            *until = Instant::now();
        }
        self.epoch += 1;
    }

    pub fn resume(&mut self) {
        self.paused = false;
        self.epoch += 1;
    }

    pub fn matches(&self, owner: &str, token: &str) -> bool {
        self.owner
            .as_ref()
            .is_some_and(|(o, t, _)| o == owner && t == token)
    }

    pub fn remaining(&self, now: Instant) -> u64 {
        self.owner.as_ref().map_or(0, |(_, _, until)| {
            until.saturating_duration_since(now).as_millis() as u64
        })
    }

    pub fn retire(&mut self, owner: String, token: String) -> bool {
        if self.retired.len() >= 1024 {
            return false;
        }
        self.retired.insert((owner, token));
        true
    }

    pub fn end(&mut self) {
        if let Some((owner, token, _)) = self.owner.take() {
            self.retired.insert((owner, token));
        }
        self.paused = false;
        self.epoch += 1;
    }

    pub fn queued_allowed(&self, epoch: u64) -> bool {
        !self.fenced() && self.epoch == epoch
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn expiry_never_restores_native_selectors_under_stale_capture() {
        let now = Instant::now();
        let mut gate = CaptureGate::default();
        assert!(gate.begin("window-generation-1".into(), "token-1".into(), now));
        assert_eq!(gate.remaining(now + ADMISSION), 0);
        assert!(gate.fenced());
        assert!(!gate.begin("window-generation-2".into(), "token-2".into(), now));
        assert!(!gate.matches("window-generation-2", "token-1"));
        assert!(!gate.matches("window-generation-1", "token-2"));
        gate.end();
        assert!(!gate.fenced());
    }
    #[test]
    fn suspended_renderer_focus_return_refences_without_new_admission() {
        let mut gate = CaptureGate::default();
        gate.begin("original".into(), "token".into(), Instant::now());
        gate.pause();
        assert!(!gate.fenced());
        assert!(gate.owned());
        assert_eq!(gate.remaining(Instant::now()), 0);
        assert!(!gate.begin("other".into(), "replacement".into(), Instant::now()));
        gate.resume();
        assert!(gate.fenced());
        assert_eq!(gate.remaining(Instant::now()), 0);
        gate.end();
        assert!(!gate.owned());
    }
    #[test]
    fn retirement_before_late_begin_cannot_create_an_orphan_fence() {
        let mut gate = CaptureGate::default();
        assert!(gate.retire("original".into(), "lost-ack".into()));
        assert!(!gate.begin("original".into(), "lost-ack".into(), Instant::now()));
        assert!(gate.begin("original".into(), "fresh-explicit".into(), Instant::now()));
    }
    #[test]
    fn queued_custom_menu_actions_cannot_survive_begin_and_end() {
        let mut gate = CaptureGate::default();
        let queued = gate.epoch;
        assert!(gate.queued_allowed(queued));
        gate.begin("original".into(), "token".into(), Instant::now());
        assert!(!gate.queued_allowed(queued));
        gate.end();
        assert!(!gate.queued_allowed(queued));
        assert!(gate.queued_allowed(gate.epoch));
    }
}
