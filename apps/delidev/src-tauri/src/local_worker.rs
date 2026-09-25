use super::*;

#[derive(Clone, Copy, Debug, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum LocalWorkerAction {
    Register,
    Start,
    Stop,
    Status,
}
#[derive(Clone, Copy, Debug, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub enum LocalWorkerState {
    NotStarted,
    Starting,
    Running,
    Stopping,
    Exited,
    Uncertain,
}
#[derive(Debug, Serialize)]
pub struct LocalWorkerStatus {
    pub state: LocalWorkerState,
    pub machine_id: String,
    pub generation: Option<String>,
    pub controller_active: bool,
}
#[derive(Deserialize)]
struct RuntimeStatus {
    state: LocalWorkerState,
    controller_active: bool,
    lifecycle: Lifecycle,
}
#[derive(Deserialize)]
struct Lifecycle {
    version: u32,
    generation: String,
    server_id: String,
    machine_id: String,
    endpoint: String,
}

impl Connector {
    pub fn local_worker(
        &self,
        action: LocalWorkerAction,
        generation: Option<&str>,
    ) -> Result<LocalWorkerStatus> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        tracing::info!(operation = "local_worker", ?action, phase = "start");
        let result = self.local_worker_inner(action, generation);
        match &result {
            Ok(status) => {
                tracing::info!(operation = "local_worker", ?action, phase = "observed", state = ?status.state)
            }
            Err(code) => {
                tracing::warn!(operation = "local_worker", ?action, phase = "failed", ?code)
            }
        }
        result
    }

    fn local_worker_inner(
        &self,
        action: LocalWorkerAction,
        generation: Option<&str>,
    ) -> Result<LocalWorkerStatus> {
        match (action, generation) {
            (LocalWorkerAction::Stop, Some(id)) => {
                let parsed =
                    uuid::Uuid::parse_str(id).map_err(|_| NativeFailure::InvalidEvidence)?;
                if parsed.get_version_num() != 7 || parsed.to_string() != id {
                    return Err(NativeFailure::InvalidEvidence);
                }
            }
            (LocalWorkerAction::Stop, None) | (_, Some(_)) => {
                return Err(NativeFailure::InvalidEvidence);
            }
            _ => {}
        }
        let client: DeviceMetadata = serde_json::from_value(self.run(&[
            "device".into(),
            "inspect".into(),
            "--device-dir".into(),
            self.root.join("desktop-client").into_os_string(),
        ])?)
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        if client.kind != DeviceType::Client
            || !client.machine_id.is_empty()
            || client.endpoint != format!("http://{}", self.listen)
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        if matches!(action, LocalWorkerAction::Register) {
            self.run(&["worker".into(), "pair-local".into()])?;
        }
        // Fixed Go-owned scope only. Reuse the independent same-server check;
        // no renderer-selected machine, path, endpoint or credential is accepted.
        let proof = self.local_worker_proof_inner()?;
        if proof.endpoint != client.endpoint || proof.server_id != client.server_id {
            return Err(NativeFailure::InvalidEvidence);
        }
        match action {
            LocalWorkerAction::Start => {
                self.run(&["worker".into(), "start".into(), "--detach".into()])?;
            }
            LocalWorkerAction::Stop => {
                self.run(&[
                    "worker".into(),
                    "stop".into(),
                    "--generation".into(),
                    generation.unwrap().into(),
                ])?;
            }
            LocalWorkerAction::Status | LocalWorkerAction::Register => {}
        }
        let status: RuntimeStatus =
            serde_json::from_value(self.run(&["worker".into(), "status".into()])?)
                .map_err(|_| NativeFailure::InvalidEvidence)?;
        let generation = if status.lifecycle.version == 0 {
            if status.state != LocalWorkerState::NotStarted || status.controller_active {
                return Err(NativeFailure::InvalidEvidence);
            }
            None
        } else {
            let value = &status.lifecycle;
            let parsed = uuid::Uuid::parse_str(&value.generation)
                .map_err(|_| NativeFailure::InvalidEvidence)?;
            if value.version != 1
                || parsed.get_version_num() != 7
                || parsed.to_string() != value.generation
                || value.server_id != proof.server_id
                || value.endpoint != proof.endpoint
                || value.machine_id != proof.machine_id
            {
                return Err(NativeFailure::InvalidEvidence);
            }
            Some(value.generation.clone())
        };
        Ok(LocalWorkerStatus {
            state: status.state,
            machine_id: proof.machine_id.clone(),
            generation,
            controller_active: status.controller_active,
        })
    }
}
