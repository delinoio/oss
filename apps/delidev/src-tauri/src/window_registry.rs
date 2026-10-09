// SPDX-License-Identifier: Apache-2.0
//! Process-local product window admission. This stores no renderer state or
//! credentials.
use std::collections::BTreeMap;

use crate::{NativeFailure, Result};

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum Role {
    Local,
    Saved(String),
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Phase {
    Preparing,
    Ready,
    Closing,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub struct Entry {
    pub label: String,
    pub instance: String,
    pub role: Role,
    pub number: u64,
    pub phase: Phase,
    focused: u64,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Close {
    Hide,
    Destroy,
}

#[derive(Default)]
pub struct Registry {
    entries: BTreeMap<String, Entry>,
    sequence: u64,
    focus_sequence: u64,
    stopping: bool,
    pub local_revision: u64,
    local_identity: Option<[u8; 32]>,
    local_preference_scope: Option<(u64, crate::session_creation_preferences::Scope)>,
}

impl Registry {
    pub fn reserve(&mut self, role: Role, initial: bool) -> Result<Entry> {
        if self.stopping {
            return Err(NativeFailure::Stopped);
        }
        self.sequence = self.sequence.checked_add(1).ok_or(NativeFailure::Busy)?;
        let instance = uuid::Uuid::now_v7().to_string();
        let label = match &role {
            Role::Local if initial && self.sequence == 1 => "main".into(),
            Role::Local => format!("local-{instance}"),
            Role::Saved(id) => format!("server-{id}-{instance}"),
        };
        let entry = Entry {
            label: label.clone(),
            instance,
            role,
            number: self.sequence,
            phase: Phase::Preparing,
            focused: 0,
        };
        self.entries.insert(label, entry.clone());
        Ok(entry)
    }

    pub fn ready(&mut self, entry: &Entry) -> Result<()> {
        if self.stopping {
            return Err(NativeFailure::Stopped);
        }
        let current = self
            .entries
            .get_mut(&entry.label)
            .filter(|v| v.instance == entry.instance && v.phase == Phase::Preparing)
            .ok_or(NativeFailure::InvalidEvidence)?;
        current.phase = Phase::Ready;
        self.focus(&entry.label);
        Ok(())
    }

    pub fn admitted(&self, label: &str) -> Result<Entry> {
        if self.stopping {
            return Err(NativeFailure::Stopped);
        }
        self.entries
            .get(label)
            .filter(|v| v.phase != Phase::Closing)
            .cloned()
            .ok_or(NativeFailure::PermissionDenied)
    }

    pub fn focus(&mut self, label: &str) {
        if let Some(value) = self
            .entries
            .get_mut(label)
            .filter(|v| v.phase == Phase::Ready)
        {
            // Saturation preserves deterministic creation-order fallback.
            self.focus_sequence = self.focus_sequence.saturating_add(1);
            value.focused = self.focus_sequence;
        }
    }

    pub fn recent(&self, role: Option<&Role>) -> Option<Entry> {
        if self.stopping {
            return None;
        }
        self.entries
            .values()
            .filter(|v| v.phase == Phase::Ready && role.is_none_or(|role| &v.role == role))
            .max_by_key(|v| (v.focused, v.number))
            .cloned()
    }

    pub fn oldest(&self, role: &Role) -> Option<Entry> {
        self.entries
            .values()
            .filter(|v| v.phase == Phase::Ready && &v.role == role)
            .min_by_key(|v| v.number)
            .cloned()
    }

    pub fn begin_close(&mut self, label: &str, tray: bool) -> Result<Close> {
        let current = self
            .entries
            .get(label)
            .ok_or(NativeFailure::PermissionDenied)?;
        if current.phase == Phase::Closing {
            return Ok(Close::Destroy);
        }
        let others = self
            .entries
            .values()
            .any(|v| v.label != label && v.phase == Phase::Ready);
        if tray && !self.stopping && !others {
            return Ok(Close::Hide);
        }
        self.entries.get_mut(label).unwrap().phase = Phase::Closing;
        Ok(Close::Destroy)
    }

    pub fn cancel_close(&mut self, label: &str) {
        if !self.stopping
            && let Some(value) = self.entries.get_mut(label)
        {
            value.phase = Phase::Ready;
        }
    }

    pub fn remove(&mut self, label: &str, instance: &str) {
        if self
            .entries
            .get(label)
            .is_some_and(|v| v.instance == instance)
        {
            self.entries.remove(label);
        }
    }

    pub fn entries(&self) -> Vec<Entry> {
        self.entries.values().cloned().collect()
    }

    pub fn adopt_local(&mut self, identity: [u8; 32]) -> Result<bool> {
        if self.stopping {
            return Err(NativeFailure::Stopped);
        }
        if self.local_identity.is_none() {
            self.local_identity = Some(identity);
            return Ok(false);
        }
        if self.local_identity == Some(identity) {
            return Ok(false);
        }
        self.local_revision = self
            .local_revision
            .checked_add(1)
            .ok_or(NativeFailure::Busy)?;
        self.local_identity = Some(identity);
        Ok(true)
    }

    pub fn adopt_local_at(&mut self, identity: [u8; 32], expected: u64) -> Result<bool> {
        if self.local_revision != expected && self.local_identity != Some(identity) {
            return Err(NativeFailure::InvalidEvidence);
        }
        self.adopt_local(identity)
    }

    // The caller holds the registry lock through both publications. A late
    // observer cannot label its old scope with a replacement's revision.
    pub fn adopt_local_scope_at(
        &mut self,
        identity: [u8; 32],
        expected: u64,
        scope: crate::session_creation_preferences::Scope,
    ) -> Result<(bool, u64)> {
        if !scope.valid() {
            return Err(NativeFailure::InvalidEvidence);
        }
        let changed = self.adopt_local_at(identity, expected)?;
        let revision = self.local_revision;
        self.local_preference_scope = Some((revision, scope));
        Ok((changed, revision))
    }

    pub fn local_preference_scope_at(
        &self,
        expected: u64,
    ) -> Result<crate::session_creation_preferences::Scope> {
        let (revision, scope) = self
            .local_preference_scope
            .as_ref()
            .ok_or(NativeFailure::InvalidEvidence)?;
        if *revision != expected || self.local_revision != expected {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(scope.clone())
    }

    pub fn stop(&mut self) {
        self.stopping = true;
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn open(r: &mut Registry, role: Role) -> Entry {
        let value = r.reserve(role, r.entries.is_empty()).unwrap();
        r.ready(&value).unwrap();
        value
    }
    #[test]
    fn local_windows_survive_initial_close_without_reusing_labels() {
        let mut r = Registry::default();
        let first = open(&mut r, Role::Local);
        let second = open(&mut r, Role::Local);
        assert_eq!(first.label, "main");
        assert_eq!(r.begin_close(&first.label, true).unwrap(), Close::Destroy);
        r.remove(&first.label, &first.instance);
        assert_eq!(r.recent(None).unwrap().label, second.label);
        assert_eq!(r.begin_close(&second.label, true).unwrap(), Close::Hide);
        assert!(r.admitted(&first.label).is_err());
        let third = open(&mut r, Role::Local);
        assert_ne!(first.label, third.label);
        assert_ne!(second.instance, third.instance);
    }
    #[test]
    fn concurrent_close_reservations_retain_last_window_and_ignore_preparation() {
        let mut r = Registry::default();
        let a = open(&mut r, Role::Local);
        let b = open(&mut r, Role::Saved("profile".into()));
        let _pending = r.reserve(Role::Local, false).unwrap();
        assert_eq!(r.begin_close(&a.label, true).unwrap(), Close::Destroy);
        assert_eq!(r.begin_close(&b.label, true).unwrap(), Close::Hide);
        assert_eq!(r.begin_close(&b.label, false).unwrap(), Close::Destroy);
    }
    #[test]
    fn exact_instance_cleanup_and_widget_writer_handoff() {
        let mut r = Registry::default();
        let role = Role::Saved("profile".into());
        let a = open(&mut r, role.clone());
        let b = open(&mut r, role.clone());
        r.remove(&a.label, &b.instance);
        assert_eq!(r.oldest(&role).unwrap().instance, a.instance);
        r.focus(&a.label);
        assert_eq!(r.recent(Some(&role)).unwrap().instance, a.instance);
        r.begin_close(&a.label, false).unwrap();
        assert_eq!(r.oldest(&role).unwrap().instance, b.instance);
        r.remove(&a.label, &a.instance);
        assert_eq!(r.entries().len(), 1);
    }
    #[test]
    fn stale_observers_cannot_restore_an_old_local_authority() {
        let mut r = Registry::default();
        assert!(!r.adopt_local_at([1; 32], 0).unwrap());
        assert!(r.adopt_local_at([2; 32], 0).unwrap());
        assert_eq!(
            r.adopt_local_at([1; 32], 0),
            Err(NativeFailure::InvalidEvidence)
        );
        assert!(!r.adopt_local_at([2; 32], 0).unwrap());
        assert_eq!(r.local_revision, 1);
    }
    #[test]
    fn late_older_adoption_cannot_publish_scope_under_replacement_revision() {
        use crate::session_creation_preferences::Scope;
        let old = Scope {
            server_id: "11111111-1111-4111-8111-111111111111".into(),
            device_id: "22222222-2222-4222-8222-222222222222".into(),
        };
        let replacement = Scope {
            server_id: "33333333-3333-4333-8333-333333333333".into(),
            device_id: "44444444-4444-4444-8444-444444444444".into(),
        };
        let mut r = Registry::default();
        assert_eq!(
            r.adopt_local_scope_at([1; 32], 0, old.clone()),
            Ok((false, 0))
        );
        assert_eq!(
            r.adopt_local_scope_at([2; 32], 0, replacement.clone()),
            Ok((true, 1))
        );
        // An earlier command resumes after replacement. Its captured revision
        // cannot authorize publication or preference access on the new scope.
        assert_eq!(
            r.adopt_local_scope_at([1; 32], 0, old),
            Err(NativeFailure::InvalidEvidence)
        );
        assert_eq!(
            r.local_preference_scope_at(0),
            Err(NativeFailure::InvalidEvidence)
        );
        assert_eq!(r.local_preference_scope_at(1), Ok(replacement));
    }
    #[test]
    fn shutdown_rejects_pending_creation_and_authority_publication() {
        let mut r = Registry::default();
        let a = r.reserve(Role::Local, true).unwrap();
        assert!(!r.adopt_local([1; 32]).unwrap());
        assert!(!r.adopt_local([1; 32]).unwrap());
        assert!(r.adopt_local([2; 32]).unwrap());
        assert_eq!(r.local_revision, 1);
        r.stop();
        assert!(r.ready(&a).is_err());
        assert!(r.reserve(Role::Local, false).is_err());
        assert!(r.admitted(&a.label).is_err());
        assert!(r.adopt_local([3; 32]).is_err());
    }
}
