// SPDX-License-Identifier: Apache-2.0
//! Typed product destination for a new product window. The renderer supplies
//! closed route enums and opaque IDs only; this module validates them
//! independently and serializes the one initial-route query that the new
//! renderer reads before its first route read. It never carries URLs, titles,
//! drafts, search text or credentials.
use std::{
    collections::BTreeSet,
    sync::{Arc, Mutex},
};

use serde::Deserialize;

use crate::{NativeFailure, Result, canonical_id};

/// Closed phases of one destination-window operation. Logs record only the
/// phase and a failure code, never the route, IDs, titles or connection data.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DestinationPhase {
    Requested,
    Authorized,
    Created,
    Failed,
}

/// Admits at most one pending destination-window operation per source window
/// instance, so a repeated activation cannot create a second window.
#[derive(Default)]
pub struct PendingDestinations {
    keys: Arc<Mutex<BTreeSet<String>>>,
}

/// Releases its source window slot when the operation ends or is dropped.
pub struct DestinationAdmission {
    keys: Arc<Mutex<BTreeSet<String>>>,
    key: String,
}

impl PendingDestinations {
    /// `key` identifies one registered source window instance.
    pub fn admit(&self, key: &str) -> Result<DestinationAdmission> {
        let mut keys = self.keys.lock().unwrap_or_else(|e| e.into_inner());
        if !keys.insert(key.to_owned()) {
            return Err(NativeFailure::Busy);
        }
        Ok(DestinationAdmission {
            keys: Arc::clone(&self.keys),
            key: key.to_owned(),
        })
    }
}

impl Drop for DestinationAdmission {
    fn drop(&mut self) {
        self.keys
            .lock()
            .unwrap_or_else(|e| e.into_inner())
            .remove(&self.key);
    }
}

/// Destination screens that can open in a new window. Creation screens (new
/// Session, new General chat) are action controls and are not destinations.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum DestinationScreen {
    Sessions,
    PullRequests,
    Usage,
    Schedules,
    Inbox,
    Search,
    Settings,
}

impl DestinationScreen {
    fn as_str(self) -> &'static str {
        match self {
            Self::Sessions => "sessions",
            Self::PullRequests => "pull-requests",
            Self::Usage => "usage",
            Self::Schedules => "schedules",
            Self::Inbox => "inbox",
            Self::Search => "search",
            Self::Settings => "settings",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum DestinationCategory {
    Appearance,
    Browser,
    KeyboardShortcuts,
    SubscriptionAccounts,
    ApiAccounts,
    Providers,
    AgentWorkers,
    Instructions,
    ProjectDefaults,
    Projects,
    Repositories,
    ExecutionWorkers,
    PairedDevices,
    Tailscale,
    ServerPreferences,
    GitWorkflow,
    Integrations,
    Diagnostics,
    Notifications,
    Transfer,
    Backups,
    AppInformation,
}

impl DestinationCategory {
    fn as_str(self) -> &'static str {
        match self {
            Self::Appearance => "appearance",
            Self::Browser => "browser",
            Self::KeyboardShortcuts => "keyboard-shortcuts",
            Self::SubscriptionAccounts => "subscription-accounts",
            Self::ApiAccounts => "api-accounts",
            Self::Providers => "providers",
            Self::AgentWorkers => "agent-workers",
            Self::Instructions => "instructions",
            Self::ProjectDefaults => "project-defaults",
            Self::Projects => "projects",
            Self::Repositories => "repositories",
            Self::ExecutionWorkers => "execution-workers",
            Self::PairedDevices => "paired-devices",
            Self::Tailscale => "tailscale",
            Self::ServerPreferences => "server-preferences",
            Self::GitWorkflow => "git-workflow",
            Self::Integrations => "integrations",
            Self::Diagnostics => "diagnostics",
            Self::Notifications => "notifications",
            Self::Transfer => "transfer",
            Self::Backups => "backups",
            Self::AppInformation => "app-information",
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "kebab-case")]
pub enum DestinationTab {
    Overview,
    History,
    Prices,
}

impl DestinationTab {
    fn as_str(self) -> &'static str {
        match self {
            Self::Overview => "overview",
            Self::History => "history",
            Self::Prices => "prices",
        }
    }
}

/// One product route. Each slot is accepted only for the screen that owns it;
/// any other combination, unknown field or non-canonical ID is rejected.
#[derive(Clone, Debug, PartialEq, Eq, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DestinationRoute {
    pub screen: DestinationScreen,
    #[serde(default)]
    pub session: Option<String>,
    #[serde(default)]
    pub sidechat: Option<String>,
    #[serde(default)]
    pub item: Option<String>,
    #[serde(default)]
    pub category: Option<DestinationCategory>,
    #[serde(default)]
    pub tab: Option<DestinationTab>,
}

impl DestinationRoute {
    /// Validates the route and returns its initial-route query ("?screen=...").
    pub fn query(&self) -> Result<String> {
        let mut query = url::form_urlencoded::Serializer::new(String::new());
        query.append_pair("screen", self.screen.as_str());
        let sessions = self.screen == DestinationScreen::Sessions;
        let items = matches!(
            self.screen,
            DestinationScreen::Inbox
                | DestinationScreen::Schedules
                | DestinationScreen::PullRequests
        );
        if (!sessions && (self.session.is_some() || self.sidechat.is_some()))
            || (!items && self.item.is_some())
            || (self.screen != DestinationScreen::Settings && self.category.is_some())
            || (self.screen != DestinationScreen::Usage && self.tab.is_some())
        {
            return Err(NativeFailure::InvalidInput);
        }
        let valid = |value: &str| canonical_id(value).map_err(|_| NativeFailure::InvalidInput);
        if let Some(session) = &self.session {
            valid(session)?;
            query.append_pair("session", session);
            if let Some(sidechat) = &self.sidechat {
                valid(sidechat)?;
                if sidechat == session {
                    return Err(NativeFailure::InvalidInput);
                }
                query.append_pair("sidechat", sidechat);
            }
        } else if self.sidechat.is_some() {
            return Err(NativeFailure::InvalidInput);
        }
        if let Some(item) = &self.item {
            valid(item)?;
            query.append_pair("item", item);
        }
        if let Some(category) = self.category {
            query.append_pair("category", category.as_str());
        }
        if let Some(tab) = self.tab {
            query.append_pair("tab", tab.as_str());
        }
        Ok(format!("?{}", query.finish()))
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const A: &str = "018f0000-0000-7000-8000-000000000001";
    const B: &str = "018f0000-0000-7000-8000-000000000002";

    fn parse(value: serde_json::Value) -> Option<DestinationRoute> {
        serde_json::from_value(value).ok()
    }

    #[test]
    fn every_screen_serializes_only_its_own_slots() {
        let cases = [
            (
                serde_json::json!({"screen": "sessions"}),
                "?screen=sessions".to_string(),
            ),
            (
                serde_json::json!({"screen": "sessions", "session": A}),
                format!("?screen=sessions&session={A}"),
            ),
            (
                serde_json::json!({"screen": "sessions", "session": A, "sidechat": B}),
                format!("?screen=sessions&session={A}&sidechat={B}"),
            ),
            (
                serde_json::json!({"screen": "inbox", "item": A}),
                format!("?screen=inbox&item={A}"),
            ),
            (
                serde_json::json!({"screen": "schedules", "item": A}),
                format!("?screen=schedules&item={A}"),
            ),
            (
                serde_json::json!({"screen": "pull-requests", "item": A}),
                format!("?screen=pull-requests&item={A}"),
            ),
            (
                serde_json::json!({"screen": "settings", "category": "app-information"}),
                "?screen=settings&category=app-information".to_string(),
            ),
            (
                serde_json::json!({"screen": "usage", "tab": "prices"}),
                "?screen=usage&tab=prices".to_string(),
            ),
            (
                serde_json::json!({"screen": "search"}),
                "?screen=search".to_string(),
            ),
        ];
        for (value, expected) in cases {
            assert_eq!(parse(value).unwrap().query().unwrap(), expected);
        }
    }

    #[test]
    fn pending_admission_is_per_source_window_and_released_on_drop() {
        let pending = PendingDestinations::default();
        let first = pending.admit("main:one").unwrap();
        assert_eq!(pending.admit("main:one").err(), Some(NativeFailure::Busy));
        let other = pending.admit("local-b:two").unwrap();
        drop(first);
        let again = pending.admit("main:one").unwrap();
        drop((other, again));
        assert!(pending.admit("local-b:two").is_ok());
    }

    #[test]
    fn unknown_screens_fields_and_urls_are_rejected() {
        for value in [
            serde_json::json!({"screen": "new-session"}),
            serde_json::json!({"screen": "new-general-chat"}),
            serde_json::json!({"screen": "https://example.invalid/"}),
            serde_json::json!({"screen": "sessions", "url": "https://example.invalid/"}),
            serde_json::json!({"screen": "settings", "category": "unknown"}),
            serde_json::json!({"screen": "usage", "tab": "unknown"}),
            serde_json::json!({}),
        ] {
            assert!(parse(value).is_none());
        }
    }

    #[test]
    fn slots_for_another_screen_are_rejected() {
        for value in [
            serde_json::json!({"screen": "inbox", "session": A}),
            serde_json::json!({"screen": "inbox", "sidechat": A}),
            serde_json::json!({"screen": "sessions", "item": A}),
            serde_json::json!({"screen": "usage", "category": "browser"}),
            serde_json::json!({"screen": "settings", "tab": "history"}),
            serde_json::json!({"screen": "search", "item": A}),
            serde_json::json!({"screen": "sessions", "sidechat": B}),
            serde_json::json!({"screen": "sessions", "session": A, "sidechat": A}),
        ] {
            let route = parse(value).unwrap();
            assert_eq!(route.query(), Err(NativeFailure::InvalidInput));
        }
    }

    #[test]
    fn non_canonical_ids_are_rejected_without_echoing_them() {
        for id in [
            "",
            "not-an-id",
            "018F0000-0000-7000-8000-000000000001",
            "018f0000-0000-4000-8000-000000000001",
            "018f0000-0000-7000-8000-000000000001&screen=settings",
            "018f0000-0000-7000-8000-000000000001/../x",
        ] {
            let route = parse(serde_json::json!({"screen": "inbox", "item": id})).unwrap();
            assert_eq!(route.query(), Err(NativeFailure::InvalidInput));
        }
    }
}
