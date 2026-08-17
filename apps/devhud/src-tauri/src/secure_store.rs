use std::collections::BTreeSet;

use keyring::{Entry, Error};
use serde_json::Value;
use tracing::error;

const SERVICE: &str = "io.delino.devhud.secure-settings.v1";
const INDEX_ACCOUNT: &str = "__index__";

#[derive(Clone, Debug, Eq, Ord, PartialEq, PartialOrd)]
struct SettingRef {
    kind: String,
    profile_id: String,
}

impl SettingRef {
    fn account(&self) -> String {
        format!("{}:{}", self.kind, self.profile_id)
    }
}

pub fn handle(request: &Value) -> Result<Value, String> {
    match request.get("operation").and_then(Value::as_str) {
        Some("secure.read") => {
            let setting = parse_setting(request)?;
            match entry(&setting)?.get_password() {
                Ok(value) => Ok(serde_json::json!({ "kind": "secure-value", "value": value })),
                Err(Error::NoEntry) => {
                    Ok(serde_json::json!({ "kind": "secure-value", "value": null }))
                }
                Err(_) => Err("storage-failure".to_string()),
            }
        }
        Some("secure.write") => {
            let setting = parse_setting(request)?;
            let value = request
                .get("value")
                .and_then(Value::as_str)
                .ok_or("invalid-argument")?;
            entry(&setting)?
                .set_password(value)
                .map_err(|_| "storage-failure")?;
            let index_result = (|| {
                let mut index = read_index()?;
                index.insert(setting.clone());
                write_index(&index)
            })();
            if let Err(reason) = index_result {
                if delete(&setting).is_err() {
                    error!(event = "secure_store_write_rollback_failed");
                }
                return Err(reason);
            }
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.remove") => {
            let setting = parse_setting(request)?;
            delete(&setting)?;
            let mut index = read_index()?;
            index.remove(&setting);
            write_index(&index)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        Some("secure.purge") => {
            purge(request)?;
            Ok(serde_json::json!({ "kind": "ok" }))
        }
        _ => Err("invalid-argument".to_string()),
    }
}

fn purge(request: &Value) -> Result<(), String> {
    let scope = request
        .get("scope")
        .and_then(Value::as_str)
        .ok_or("invalid-argument")?;
    let profile = request.get("profileId").and_then(Value::as_str);
    if !matches!(scope, "logout" | "account-deletion" | "api-change")
        || (scope != "logout" && profile.is_none())
    {
        return Err("invalid-argument".to_string());
    }
    let mut index = read_index()?;
    let targets: Vec<_> = index
        .iter()
        .filter(|setting| should_remove(setting, scope, profile))
        .cloned()
        .collect();
    for setting in targets {
        delete(&setting)?;
        index.remove(&setting);
    }
    write_index(&index)
}

fn should_remove(setting: &SettingRef, scope: &str, profile: Option<&str>) -> bool {
    match scope {
        "logout" => true,
        "account-deletion" => {
            setting.kind != "logto-session" || profile != Some(setting.profile_id.as_str())
        }
        "api-change" => {
            setting.kind == "logto-session" && profile == Some(setting.profile_id.as_str())
        }
        _ => false,
    }
}

fn parse_setting(request: &Value) -> Result<SettingRef, String> {
    let setting = request.get("setting").ok_or("invalid-argument")?;
    Ok(SettingRef {
        kind: setting
            .get("kind")
            .and_then(Value::as_str)
            .ok_or("invalid-argument")?
            .to_string(),
        profile_id: setting
            .get("profileId")
            .and_then(Value::as_str)
            .ok_or("invalid-argument")?
            .to_string(),
    })
}

fn entry(setting: &SettingRef) -> Result<Entry, String> {
    Entry::new(SERVICE, &setting.account()).map_err(|_| "storage-failure".to_string())
}

fn delete(setting: &SettingRef) -> Result<(), String> {
    match entry(setting)?.delete_credential() {
        Ok(()) | Err(Error::NoEntry) => Ok(()),
        Err(_) => Err("storage-failure".to_string()),
    }
}

fn read_index() -> Result<BTreeSet<SettingRef>, String> {
    let entry = Entry::new(SERVICE, INDEX_ACCOUNT).map_err(|_| "storage-failure")?;
    let value = match entry.get_password() {
        Ok(value) => value,
        Err(Error::NoEntry) => return Ok(BTreeSet::new()),
        Err(_) => return Err("storage-failure".to_string()),
    };
    let tuples: Vec<(String, String)> =
        serde_json::from_str(&value).map_err(|_| "storage-failure")?;
    Ok(tuples
        .into_iter()
        .map(|(kind, profile_id)| SettingRef { kind, profile_id })
        .collect())
}

fn write_index(index: &BTreeSet<SettingRef>) -> Result<(), String> {
    let entry = Entry::new(SERVICE, INDEX_ACCOUNT).map_err(|_| "storage-failure")?;
    if index.is_empty() {
        return match entry.delete_credential() {
            Ok(()) | Err(Error::NoEntry) => Ok(()),
            Err(_) => Err("storage-failure".to_string()),
        };
    }
    let tuples: Vec<_> = index
        .iter()
        .map(|setting| (&setting.kind, &setting.profile_id))
        .collect();
    entry
        .set_password(&serde_json::to_string(&tuples).map_err(|_| "storage-failure")?)
        .map_err(|_| "storage-failure".to_string())
}

#[cfg(test)]
mod tests {
    use super::{SettingRef, should_remove};

    fn setting(kind: &str, profile_id: &str) -> SettingRef {
        SettingRef {
            kind: kind.to_string(),
            profile_id: profile_id.to_string(),
        }
    }

    #[test]
    fn account_deletion_retains_only_the_current_recovery_session() {
        assert!(!should_remove(
            &setting("logto-session", "current"),
            "account-deletion",
            Some("current")
        ));
        assert!(should_remove(
            &setting("logto-session", "old-api"),
            "account-deletion",
            Some("current")
        ));
        assert!(should_remove(
            &setting("github-pat", "current"),
            "account-deletion",
            Some("current")
        ));
        assert!(should_remove(
            &setting("r2-secret-access-key", "current"),
            "account-deletion",
            Some("current")
        ));
    }

    #[test]
    fn logout_removes_every_secret_and_api_change_only_its_session() {
        for kind in ["logto-session", "github-pat", "r2-access-key-id"] {
            assert!(should_remove(&setting(kind, "profile"), "logout", None));
        }
        assert!(should_remove(
            &setting("logto-session", "old-api"),
            "api-change",
            Some("old-api")
        ));
        assert!(!should_remove(
            &setting("github-pat", "old-api"),
            "api-change",
            Some("old-api")
        ));
    }
}
