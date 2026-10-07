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

fn retire_replaced_attempt(
    retained: &mut Option<Attempt>,
    server: &str,
    generation: &str,
) -> Result<()> {
    if let Some(attempt) = retained.as_ref() {
        if attempt.server != server {
            return Err(NativeFailure::InvalidEvidence);
        }
        if attempt.generation != generation {
            // The caller has already matched this generation to the live
            // resident runtime, which proves the prior child was replaced.
            *retained = None;
        }
    }
    Ok(())
}

fn validate_retry_target(attempt: &Attempt, expected_attempt_id: Option<&str>) -> Result<()> {
    let expected_attempt_id = expected_attempt_id.ok_or(NativeFailure::InvalidEvidence)?;
    canonical_id(expected_attempt_id)?;
    if attempt.id != expected_attempt_id {
        return Err(NativeFailure::InvalidEvidence);
    }
    if attempt
        .last
        .as_ref()
        .is_none_or(|result| result.state != CredentialAccessState::Failed)
    {
        return Err(NativeFailure::Busy);
    }
    Ok(())
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
        expected_attempt_id: Option<&str>,
    ) -> Result<CredentialAccessResult> {
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        canonical_id(server)?;
        canonical_id(generation)?;
        match (action, expected_attempt_id) {
            (CredentialAccessAction::Retry, Some(expected)) => canonical_id(expected)?,
            (CredentialAccessAction::Retry, None) | (_, Some(_)) => {
                return Err(NativeFailure::InvalidEvidence);
            }
            _ => {}
        }
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
        retire_replaced_attempt(&mut retained, server, generation)?;
        if action == CredentialAccessAction::Retry {
            validate_retry_target(
                retained.as_ref().ok_or(NativeFailure::InvalidEvidence)?,
                expected_attempt_id,
            )?;
        }
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
        // Replacement or Quit while the reply was in flight cannot publish
        // an old server's access observation into the current desktop.
        if self.exiting.load(Ordering::Acquire)
            || self.runtime()?.generation != generation
            || self.oauth_server_identity()? != server
        {
            return Err(NativeFailure::InvalidEvidence);
        }
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
    fn only_a_confirmed_runtime_generation_retires_the_attempt() {
        let server = uuid::Uuid::now_v7().to_string();
        let original_generation = uuid::Uuid::now_v7().to_string();
        let replacement_generation = uuid::Uuid::now_v7().to_string();
        let mut retained = Some(Attempt {
            id: uuid::Uuid::now_v7().to_string(),
            previous: String::new(),
            server: server.clone(),
            generation: original_generation.clone(),
            last: None,
        });

        assert!(retire_replaced_attempt(&mut retained, &server, &original_generation).is_ok());
        assert!(retained.is_some());
        assert!(retire_replaced_attempt(&mut retained, &server, &replacement_generation).is_ok());
        assert!(retained.is_none());
    }

    #[test]
    fn a_replacement_server_cannot_retire_an_attempt() {
        let original_server = uuid::Uuid::now_v7().to_string();
        let replacement_server = uuid::Uuid::now_v7().to_string();
        let generation = uuid::Uuid::now_v7().to_string();
        let mut retained = Some(Attempt {
            id: uuid::Uuid::now_v7().to_string(),
            previous: String::new(),
            server: original_server,
            generation: uuid::Uuid::now_v7().to_string(),
            last: None,
        });

        assert!(retire_replaced_attempt(&mut retained, &replacement_server, &generation).is_err());
        assert!(retained.is_some());
    }

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

    #[test]
    fn retry_must_name_the_failed_attempt_still_retained() {
        let current_id = uuid::Uuid::now_v7().to_string();
        let attempt = Attempt {
            id: current_id.clone(),
            previous: String::new(),
            server: uuid::Uuid::now_v7().to_string(),
            generation: uuid::Uuid::now_v7().to_string(),
            last: Some(CredentialAccessResult {
                attempt_id: current_id.clone(),
                state: CredentialAccessState::Failed,
                issue: Some(CredentialAccessIssue::Unavailable),
            }),
        };

        assert!(validate_retry_target(&attempt, Some(&current_id)).is_ok());
        assert!(validate_retry_target(&attempt, Some(&uuid::Uuid::now_v7().to_string())).is_err());
        assert!(validate_retry_target(&attempt, None).is_err());
    }
}
