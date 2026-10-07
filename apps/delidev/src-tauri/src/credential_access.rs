// SPDX-License-Identifier: Apache-2.0
use std::sync::atomic::Ordering;

use serde::{Deserialize, Serialize};
use zeroize::Zeroizing;

use crate::{Connector, NativeFailure, Result, canonical_id};

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum CredentialAccessAction {
    Observe,
    Retry,
    Skip,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "lowercase")]
pub enum CredentialAccessState {
    Checking,
    Succeeded,
    Failed,
    Skipped,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum CredentialAccessIssue {
    ConfirmationRequired,
    Unavailable,
    RecoveryRequired,
    ExecutableChanged,
    ExecutableInvalid,
    PermissionDenied,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CredentialAccessResult {
    pub attempt_id: String,
    pub state: CredentialAccessState,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub issue: Option<CredentialAccessIssue>,
}

pub(crate) struct Attempt {
    id: String,
    previous: String,
    server: String,
    generation: String,
    last: Option<CredentialAccessResult>,
}

fn validate_result(value: CredentialAccessResult, id: &str) -> Result<CredentialAccessResult> {
    canonical_id(&value.attempt_id)?;
    if value.attempt_id != id
        || (value.state == CredentialAccessState::Failed) != value.issue.is_some()
    {
        return Err(NativeFailure::InvalidEvidence);
    }
    Ok(value)
}

impl Connector {
    // One attempt belongs to the native main process, independently of every
    // renderer/window mount. Repeated Begin with its same ID is an observation,
    // including after an unknown reply; only explicit Retry replaces that ID.
    pub fn credential_access(
        &self,
        action: CredentialAccessAction,
        server: &str,
        generation: &str,
    ) -> Result<CredentialAccessResult> {
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        canonical_id(server)?;
        canonical_id(generation)?;
        if self.oauth_server_identity()? != server {
            return Err(NativeFailure::InvalidEvidence);
        }
        let runtime = self.runtime()?;
        if runtime.generation != generation {
            return Err(NativeFailure::InvalidEvidence);
        }
        let device = self
            .oauth_identity
            .lock()
            .map_err(|_| NativeFailure::Busy)?
            .as_ref()
            .ok_or(NativeFailure::CredentialUnavailable)?
            .device_id
            .clone();
        let mut retained = self
            .credential_access
            .lock()
            .map_err(|_| NativeFailure::Busy)?;
        let attempt = retained.get_or_insert_with(|| Attempt {
            id: uuid::Uuid::now_v7().to_string(),
            previous: String::new(),
            server: server.to_owned(),
            generation: generation.to_owned(),
            last: None,
        });
        if attempt.server != server || attempt.generation != generation {
            return Err(NativeFailure::InvalidEvidence);
        }
        if action == CredentialAccessAction::Retry {
            if attempt
                .last
                .as_ref()
                .is_none_or(|v| v.state != CredentialAccessState::Failed)
            {
                return Err(NativeFailure::Busy);
            }
            attempt.previous = attempt.id.clone();
            attempt.id = uuid::Uuid::now_v7().to_string();
            attempt.last = None;
        } else if action == CredentialAccessAction::Observe
            && attempt.last.as_ref().is_some_and(|v| {
                matches!(
                    v.state,
                    CredentialAccessState::Succeeded | CredentialAccessState::Skipped
                )
            })
        {
            return Ok(attempt.last.as_ref().unwrap().clone());
        }
        if !cfg!(target_os = "macos") {
            let result = CredentialAccessResult {
                attempt_id: attempt.id.clone(),
                state: CredentialAccessState::Skipped,
                issue: None,
            };
            attempt.last = Some(result.clone());
            return Ok(result);
        }
        let operation = if action == CredentialAccessAction::Skip {
            "skip"
        } else if attempt.previous.is_empty() {
            "begin"
        } else {
            "retry"
        };
        let input = Zeroizing::new(
            serde_json::to_vec(&serde_json::json!({
                "action": operation, "attempt_id": attempt.id, "previous_id": attempt.previous,
                "server_id": server, "generation": generation, "device_id": device,
            }))
            .map_err(|_| NativeFailure::InvalidInput)?,
        );
        let value = self.resident_request(
            &["runtime".into(), "credentials".into()],
            Some(input),
            self.command_timeout,
        )?;
        let result = validate_result(
            serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?,
            &attempt.id,
        )?;
        attempt.last = Some(result.clone());
        Ok(result)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn result_requires_original_attempt_and_closed_failure() {
        let id = uuid::Uuid::now_v7().to_string();
        let value = CredentialAccessResult {
            attempt_id: id.clone(),
            state: CredentialAccessState::Failed,
            issue: Some(CredentialAccessIssue::ConfirmationRequired),
        };
        assert!(validate_result(value.clone(), &id).is_ok());
        assert!(validate_result(value.clone(), &uuid::Uuid::now_v7().to_string()).is_err());
        assert!(
            validate_result(
                CredentialAccessResult {
                    issue: None,
                    ..value
                },
                &id
            )
            .is_err()
        );
        assert!(
            serde_json::from_value::<CredentialAccessResult>(
                serde_json::json!({"attempt_id": id, "state": "succeeded", "secret": "forbidden"})
            )
            .is_err()
        );
    }
}
