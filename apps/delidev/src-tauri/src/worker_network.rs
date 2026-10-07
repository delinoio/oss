// SPDX-License-Identifier: Apache-2.0
use super::*;

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "kebab-case")]
pub enum WorkerNetworkAction {
    Prepare,
    Import,
    Status,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Recipient {
    version: u32,
    authority: RecipientAuthority,
    key_id: String,
    recipient: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RecipientAuthority {
    server_id: String,
    endpoint: String,
    machine_id: String,
    device_id: String,
    pairing_id: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Imported {
    version: u32,
    key_id: String,
    route_id: String,
    generation: String,
    ciphertext_digest: String,
}

fn validate_input(action: WorkerNetworkAction, bytes: &[u8], digest: &str) -> Result<()> {
    match action {
        WorkerNetworkAction::Import
            if !bytes.is_empty()
                && bytes.len() <= 96 << 10
                && digest.len() == 64
                && digest
                    .bytes()
                    .all(|v| v.is_ascii_digit() || (b'a'..=b'f').contains(&v)) =>
        {
            Ok(())
        }
        WorkerNetworkAction::Prepare | WorkerNetworkAction::Status
            if bytes.is_empty() && digest.is_empty() =>
        {
            Ok(())
        }
        _ => Err(NativeFailure::InvalidInput),
    }
}
fn validate_result(
    value: &serde_json::Value,
    action: WorkerNetworkAction,
    proof: &LocalWorkerProof,
    digest: &str,
) -> Result<()> {
    if action == WorkerNetworkAction::Import {
        let result: Imported =
            serde_json::from_value(value.clone()).map_err(|_| NativeFailure::InvalidEvidence)?;
        canonical_id(&result.key_id)?;
        canonical_id(&result.route_id)?;
        let generation: u64 = result
            .generation
            .parse()
            .map_err(|_| NativeFailure::InvalidEvidence)?;
        if result.version != 1
            || generation == 0
            || generation >= 1 << 63
            || generation.to_string() != result.generation
            || result.ciphertext_digest != digest
        {
            return Err(NativeFailure::InvalidEvidence);
        }
    } else {
        let result: Recipient =
            serde_json::from_value(value.clone()).map_err(|_| NativeFailure::InvalidEvidence)?;
        for id in [
            &result.key_id,
            &result.authority.server_id,
            &result.authority.machine_id,
            &result.authority.device_id,
            &result.authority.pairing_id,
        ] {
            canonical_id(id)?;
        }
        if result.version != 1
            || result.authority.server_id != proof.server_id
            || result.authority.endpoint != proof.endpoint
            || result.authority.machine_id != proof.machine_id
            || !result.recipient.starts_with("age1")
            || result.recipient.len() != 62
        {
            return Err(NativeFailure::InvalidEvidence);
        }
    }
    Ok(())
}
impl Connector {
    // This closed infrastructure bridge accepts no path, endpoint, recipient
    // key or saved-profile selector. Go owns preparation/import and its vault.
    // Native window authority selects one existing same-computer Worker scope.
    pub fn worker_network(
        &self,
        expected: Option<&SavedConnection>,
        machine: &str,
        action: WorkerNetworkAction,
        bytes: Vec<u8>,
        digest: &str,
    ) -> Result<serde_json::Value> {
        canonical_id(machine)?;
        validate_input(action, &bytes, digest)?;
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let proof = if let Some(profile) = expected {
            self.check_saved_profile(profile)?;
            self.saved_worker_proof_inner(profile)?
        } else {
            self.local_worker_proof_inner()?
        };
        if proof.machine_id != machine {
            return Err(NativeFailure::InvalidEvidence);
        }
        let mut args: Vec<OsString> = if let Some(profile) = expected {
            vec![
                "connection".into(),
                match action {
                    WorkerNetworkAction::Prepare => "worker-network-prepare",
                    WorkerNetworkAction::Import => "worker-network-import",
                    WorkerNetworkAction::Status => "worker-network-status",
                }
                .into(),
                "--id".into(),
                profile.id.as_str().into(),
            ]
        } else {
            vec![
                "worker".into(),
                "network".into(),
                match action {
                    WorkerNetworkAction::Prepare => "prepare",
                    WorkerNetworkAction::Import => "import",
                    WorkerNetworkAction::Status => "status",
                }
                .into(),
            ]
        };
        if action == WorkerNetworkAction::Import {
            if expected.is_none() {
                args.extend(["--input".into(), "-".into()]);
            }
            args.extend(["--expected-ciphertext-digest".into(), digest.into()]);
        }
        tracing::info!(operation = "worker_network", ?action, phase = "start");
        let result = (|| {
            let value = self.run_with_input(
                &args,
                (action == WorkerNetworkAction::Import).then(|| Zeroizing::new(bytes)),
            )?;
            validate_result(&value, action, &proof, digest)?;
            Ok(value)
        })();
        match &result {
            Ok(_) => tracing::info!(operation = "worker_network", ?action, phase = "observed"),
            Err(code) => tracing::warn!(
                operation = "worker_network",
                ?action,
                phase = "failed",
                ?code
            ),
        }
        result
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn closed_network_input_and_public_result_cannot_admit_other_authority() {
        assert!(validate_input(WorkerNetworkAction::Import, &[1], &"a".repeat(64)).is_ok());
        assert!(validate_input(WorkerNetworkAction::Prepare, &[1], "").is_err());
        assert!(
            validate_input(
                WorkerNetworkAction::Import,
                &[1; 96 * 1024 + 1],
                &"a".repeat(64)
            )
            .is_err()
        );
        assert!(validate_input(WorkerNetworkAction::Import, &[1], &"A".repeat(64)).is_err());
        let proof = LocalWorkerProof {
            paired_endpoint: String::new(),
            runtime_generation: None,
            runtime_key: None,
            endpoint: "http://127.0.0.1:46310".into(),
            server_id: uuid::Uuid::now_v7().to_string(),
            machine_id: uuid::Uuid::now_v7().to_string(),
            token: String::new(),
        };
        let mut recipient = serde_json::json!({"version":1,"authority":{"server_id":proof.server_id,"endpoint":proof.endpoint,"machine_id":proof.machine_id,"device_id":uuid::Uuid::now_v7().to_string(),"pairing_id":uuid::Uuid::now_v7().to_string()},"key_id":uuid::Uuid::now_v7().to_string(),"recipient":format!("age1{}","a".repeat(58))});
        assert!(validate_result(&recipient, WorkerNetworkAction::Prepare, &proof, "").is_ok());
        recipient["authority"]["machine_id"] = uuid::Uuid::now_v7().to_string().into();
        assert!(validate_result(&recipient, WorkerNetworkAction::Prepare, &proof, "").is_err());
        recipient["authority"]["machine_id"] = proof.machine_id.clone().into();
        recipient["private_key"] = "must-not-cross".into();
        assert!(validate_result(&recipient, WorkerNetworkAction::Status, &proof, "").is_err());
        let mut imported = serde_json::json!({"version":1,"key_id":uuid::Uuid::now_v7().to_string(),"route_id":uuid::Uuid::now_v7().to_string(),"generation":"9007199254740993","ciphertext_digest":"a".repeat(64)});
        assert!(
            validate_result(
                &imported,
                WorkerNetworkAction::Import,
                &proof,
                &"a".repeat(64)
            )
            .is_ok()
        );
        imported["generation"] = "09007199254740993".into();
        assert!(
            validate_result(
                &imported,
                WorkerNetworkAction::Import,
                &proof,
                &"a".repeat(64)
            )
            .is_err()
        );
    }
}
