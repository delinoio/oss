//! Bounded presentation data only. Go and the direct Connect client own all
//! product observations; this module neither reads credentials nor calls RPCs.
use serde::{Deserialize, Serialize};

use crate::NativeFailure;

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum TrayDestination {
    Sessions,
    Inbox,
    Usage,
    Settings,
}

#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TraySummary {
    pub overview: Option<TrayOverview>,
    pub usage: Option<TrayUsage>,
    pub accounts: Option<TrayAccounts>,
}
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TrayOverview {
    pub observed_at: String,
    pub stale: bool,
    pub active_sessions: String,
    pub pending_interactions: String,
    pub registered_workers: String,
    pub connected_workers: String,
}
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TrayUsage {
    pub known_tokens: Option<String>,
    pub incomplete: bool,
}
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TrayAccounts {
    pub entries: Vec<TrayAccount>,
    pub more: bool,
}
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TrayAccount {
    pub alias: String,
    pub windows: Vec<TrayQuota>,
    pub more: bool,
}
#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum QuotaState {
    Observed,
    Unknown,
    Stale,
    Failed,
    Unsupported,
}
#[derive(Clone, Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TrayQuota {
    pub state: QuotaState,
    pub remaining_basis_points: Option<u16>,
    pub observed_at: Option<String>,
    pub reset_at: Option<String>,
}

fn decimal(value: &str, limit: usize) -> bool {
    !value.is_empty()
        && value.len() <= limit
        && value.bytes().all(|b| b.is_ascii_digit())
        && (value == "0" || !value.starts_with('0'))
}
fn timestamp(value: &str) -> bool {
    (20..=40).contains(&value.len())
        && value.contains('T')
        && value
            .bytes()
            .all(|b| b.is_ascii_digit() || b"-:+.TZ".contains(&b))
}
impl TraySummary {
    pub fn validate(&self) -> Result<(), NativeFailure> {
        if let Some(v) = &self.overview
            && (!timestamp(&v.observed_at)
                || [
                    &v.active_sessions,
                    &v.pending_interactions,
                    &v.registered_workers,
                    &v.connected_workers,
                ]
                .iter()
                .any(|v| !decimal(v, 20) || v.parse::<u64>().is_err())
                || v.connected_workers.parse::<u64>().ok()
                    > v.registered_workers.parse::<u64>().ok())
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        if let Some(v) = &self.usage
            && v.known_tokens.as_deref().is_some_and(|v| !decimal(v, 80))
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        if let Some(v) = &self.accounts {
            if v.entries.len() > 20 {
                return Err(NativeFailure::InvalidEvidence);
            }
            for account in &v.entries {
                if account.alias.is_empty()
                    || account.alias.len() > 256
                    || account.windows.len() > 8
                {
                    return Err(NativeFailure::InvalidEvidence);
                }
                for window in &account.windows {
                    if window.remaining_basis_points.is_some_and(|v| v > 10000)
                        || [&window.observed_at, &window.reset_at]
                            .iter()
                            .any(|v| v.as_deref().is_some_and(|v| !timestamp(v)))
                    {
                        return Err(NativeFailure::InvalidEvidence);
                    }
                }
            }
        }
        Ok(())
    }
}

// Menu accelerators and control characters are presentation syntax, never part
// of an account alias. Email-shaped aliases also stay private in native UI.
pub fn menu_alias(value: &str) -> String {
    if value.contains('@') {
        return "Account alias hidden".to_owned();
    }
    value
        .chars()
        .map(|c| if c.is_control() { ' ' } else { c })
        .collect::<String>()
        .replace('&', "&&")
}
impl TrayQuota {
    pub fn label(&self) -> String {
        let state = match self.state {
            QuotaState::Observed => "observed",
            QuotaState::Unknown => "unknown",
            QuotaState::Stale => "stale",
            QuotaState::Failed => "failed",
            QuotaState::Unsupported => "unsupported",
        };
        match self.remaining_basis_points {
            Some(value) if matches!(self.state, QuotaState::Observed | QuotaState::Stale) => {
                format!("{}.{:02}% remaining · {state}", value / 100, value % 100)
            }
            _ => format!("Remaining quota {state}"),
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn presentation_rejects_unknown_authority_and_preserves_large_counts() {
        let raw = r#"{"overview":{"observed_at":"2026-09-27T00:00:00Z","stale":false,"active_sessions":"9007199254740993","pending_interactions":"0","registered_workers":"2","connected_workers":"1"},"usage":{"known_tokens":"90071992547409930000","incomplete":true},"accounts":null}"#;
        let mut value: TraySummary = serde_json::from_str(raw).unwrap();
        value.validate().unwrap();
        assert!(
            serde_json::from_str::<TraySummary>(
                &raw.replace("\"overview\":", "\"token\":\"private\",\"overview\":")
            )
            .is_err()
        );
        value.overview.as_mut().unwrap().connected_workers = "3".into();
        assert!(value.validate().is_err());
        value.overview.as_mut().unwrap().connected_workers = "01".into();
        assert!(value.validate().is_err());
    }
    #[test]
    fn aliases_and_quota_never_invent_capacity() {
        assert_eq!(menu_alias("A&B\taccount"), "A&&B account");
        assert_eq!(menu_alias("owner@example.test"), "Account alias hidden");
        let mut value = TrayQuota {
            state: QuotaState::Unknown,
            remaining_basis_points: None,
            observed_at: None,
            reset_at: None,
        };
        assert_eq!(value.label(), "Remaining quota unknown");
        value.state = QuotaState::Observed;
        value.remaining_basis_points = Some(0);
        assert_eq!(value.label(), "0.00% remaining · observed");
        value.state = QuotaState::Stale;
        assert_eq!(value.label(), "0.00% remaining · stale");
    }
}
