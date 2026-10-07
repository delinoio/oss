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
    #[serde(skip_serializing_if = "Option::is_none")]
    pub management: Option<crate::LocalWorkerManagement>,
    #[serde(skip)]
    pub(crate) desired_stopped: bool,
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
    #[serde(default)]
    desired: Option<WorkerDesiredState>,
}

#[derive(Clone, Copy, PartialEq, Eq, Deserialize)]
#[serde(rename_all = "lowercase")]
enum WorkerDesiredState {
    Running,
    Stopped,
}

impl Connector {
    pub fn local_worker(
        &self,
        action: LocalWorkerAction,
        generation: Option<&str>,
    ) -> Result<LocalWorkerStatus> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        validate_action(action, generation)?;
        if self.exiting.load(Ordering::Acquire) {
            return Err(NativeFailure::Stopped);
        }
        if self.worker_auto_enabled.load(Ordering::Acquire) {
            if matches!(action, LocalWorkerAction::Start) {
                self.worker_launch_pending.store(false, Ordering::Release);
                *self
                    .worker_pause_generation
                    .lock()
                    .unwrap_or_else(|e| e.into_inner()) = None;
                let result =
                    self.start_managed_worker(crate::worker_supervision::WorkerHostMode::Launch);
                if let Err(error) = &result {
                    self.worker_management_failure(*error);
                }
                return result;
            }
            if matches!(action, LocalWorkerAction::Stop) {
                let current = self.local_worker_inner(LocalWorkerAction::Status, None)?;
                if current.generation.as_deref() != generation {
                    return Err(NativeFailure::InvalidEvidence);
                }
                // Fence recovery before the Stop controller can lose its reply.
                // This latch is tied only to the confirmed original generation.
                self.worker_launch_pending.store(false, Ordering::Release);
                *self
                    .worker_pause_generation
                    .lock()
                    .unwrap_or_else(|e| e.into_inner()) = current.generation;
            }
            let result = self.local_worker_inner(action, generation);
            if matches!(action, LocalWorkerAction::Stop)
                && result.as_ref().is_ok_and(|status| status.desired_stopped)
            {
                self.worker_launch_pending.store(false, Ordering::Release);
            }
            return match result {
                Ok(status) => Ok(self.managed_worker_status(status)),
                Err(error) if matches!(action, LocalWorkerAction::Status) => {
                    self.worker_management_failure(error);
                    Ok(self.unavailable_worker_status())
                }
                Err(error) => Err(error),
            };
        }
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

    pub(crate) fn local_worker_inner(
        &self,
        action: LocalWorkerAction,
        generation: Option<&str>,
    ) -> Result<LocalWorkerStatus> {
        validate_action(action, generation)?;
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
        // no renderer-selected machine, path, endpoint or credential is
        // accepted.
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
        worker_status(
            self.run(&["worker".into(), "status".into()])?,
            &proof.server_id,
            &proof.endpoint,
            &proof.machine_id,
        )
    }

    // Retained Workers have independent authority after the client is
    // forgotten. Return only lifecycle metadata; this boundary never
    // reads/delivers a token.
    pub fn retained_worker(
        &self,
        id: &str,
        action: LocalWorkerAction,
        generation: Option<&str>,
    ) -> Result<LocalWorkerStatus> {
        canonical_id(id)?;
        validate_action(action, generation)?;
        if matches!(action, LocalWorkerAction::Register) {
            return Err(NativeFailure::PermissionDenied);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        tracing::info!(operation = "retained_worker", ?action, phase = "start");
        let result = (|| {
            let profile: SavedConnection = serde_json::from_value(self.run(&[
                "connection".into(),
                "inspect".into(),
                "--id".into(),
                id.into(),
            ])?)
            .map_err(|_| NativeFailure::InvalidEvidence)?;
            profile.validate()?;
            if profile.id != id
                || !matches!(
                    profile.state,
                    SavedConnectionState::Removing | SavedConnectionState::Removed
                )
            {
                return Err(NativeFailure::PermissionDenied);
            }
            let metadata: DeviceMetadata = serde_json::from_value(self.run(&[
                "connection".into(),
                "worker-inspect".into(),
                "--id".into(),
                id.into(),
            ])?)
            .map_err(|_| NativeFailure::InvalidEvidence)?;
            if metadata.kind != DeviceType::Worker
                || metadata.endpoint != profile.endpoint
                || metadata.server_id != profile.server_id
            {
                return Err(NativeFailure::InvalidEvidence);
            }
            canonical_id(&metadata.machine_id)?;
            let command = match action {
                LocalWorkerAction::Status => "worker-status",
                LocalWorkerAction::Start => "worker-start",
                LocalWorkerAction::Stop => "worker-stop",
                LocalWorkerAction::Register => unreachable!(),
            };
            let mut args = vec![
                "connection".into(),
                command.into(),
                "--id".into(),
                id.into(),
            ];
            if let Some(generation) = generation {
                args.extend(["--generation".into(), generation.into()]);
            }
            worker_status(
                self.run(&args)?,
                &metadata.server_id,
                &metadata.endpoint,
                &metadata.machine_id,
            )
        })();
        if let Err(code) = &result {
            tracing::warn!(
                operation = "retained_worker",
                ?action,
                phase = "failed",
                ?code
            );
        }
        result
    }

    pub fn saved_worker_proof(&self, expected: &SavedConnection) -> Result<LocalWorkerProof> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        self.saved_worker_proof_inner(expected)
    }

    pub(crate) fn saved_worker_proof_inner(
        &self,
        expected: &SavedConnection,
    ) -> Result<LocalWorkerProof> {
        self.check_saved_profile(expected)?;
        let metadata: DeviceMetadata = serde_json::from_value(self.run(&[
            "connection".into(),
            "worker-inspect".into(),
            "--id".into(),
            expected.id.as_str().into(),
        ])?)
        .map_err(|_| NativeFailure::InvalidEvidence)?;
        if metadata.kind != DeviceType::Worker
            || metadata.endpoint != expected.endpoint
            || metadata.server_id != expected.server_id
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        let path = self
            .root
            .join("connections")
            .join(&expected.id)
            .join("worker/device.json");
        let file = fs::symlink_metadata(&path).map_err(|_| NativeFailure::CredentialUnavailable)?;
        if !file.is_file() || file.len() > 16 << 10 {
            return Err(NativeFailure::InvalidEvidence);
        }
        let bytes = Zeroizing::new(read_bounded(
            File::open(path).map_err(|_| NativeFailure::CredentialUnavailable)?,
            16 << 10,
        )?);
        let verified = validated_connection(&bytes, &metadata, DeviceType::Worker, true)?;
        self.check_saved_profile(expected)?;
        Ok(LocalWorkerProof {
            endpoint: verified.endpoint.clone(),
            server_id: verified.server_id.clone(),
            machine_id: metadata.machine_id,
            token: verified.token.clone(),
        })
    }

    pub fn saved_worker(
        &self,
        expected: &SavedConnection,
        action: LocalWorkerAction,
        generation: Option<&str>,
    ) -> Result<LocalWorkerStatus> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        validate_action(action, generation)?;
        self.check_saved_profile(expected)?;
        tracing::info!(operation = "saved_worker", ?action, phase = "start");
        let result = (|| {
            let command = match action {
                LocalWorkerAction::Register => "worker-register",
                LocalWorkerAction::Start => "worker-start",
                LocalWorkerAction::Stop => "worker-stop",
                LocalWorkerAction::Status => "worker-status",
            };
            let mut args = vec![
                "connection".into(),
                command.into(),
                "--id".into(),
                expected.id.as_str().into(),
            ];
            if let Some(id) = generation {
                args.extend(["--generation".into(), id.into()]);
            }
            let observed = self.run(&args)?;
            let proof = self.saved_worker_proof_inner(expected)?;
            let status = if matches!(action, LocalWorkerAction::Register) {
                self.run(&[
                    "connection".into(),
                    "worker-status".into(),
                    "--id".into(),
                    expected.id.as_str().into(),
                ])?
            } else {
                observed
            };
            worker_status(status, &proof.server_id, &proof.endpoint, &proof.machine_id)
        })();
        match &result {
            Ok(status) => {
                tracing::info!(operation = "saved_worker", ?action, phase = "observed", state = ?status.state)
            }
            Err(code) => {
                tracing::warn!(operation = "saved_worker", ?action, phase = "failed", ?code)
            }
        }
        result
    }
}

fn validate_action(action: LocalWorkerAction, generation: Option<&str>) -> Result<()> {
    match (action, generation) {
        (LocalWorkerAction::Stop, Some(id)) => {
            let parsed = uuid::Uuid::parse_str(id).map_err(|_| NativeFailure::InvalidEvidence)?;
            if parsed.get_version_num() != 7 || parsed.to_string() != id {
                return Err(NativeFailure::InvalidEvidence);
            }
        }
        (LocalWorkerAction::Stop, None) | (_, Some(_)) => {
            return Err(NativeFailure::InvalidEvidence);
        }
        _ => {}
    }
    Ok(())
}

pub(crate) fn worker_status(
    value: serde_json::Value,
    server_id: &str,
    endpoint: &str,
    machine_id: &str,
) -> Result<LocalWorkerStatus> {
    let status: RuntimeStatus =
        serde_json::from_value(value).map_err(|_| NativeFailure::InvalidEvidence)?;
    let generation = if status.lifecycle.version == 0 {
        if status.state != LocalWorkerState::NotStarted || status.controller_active {
            return Err(NativeFailure::InvalidEvidence);
        }
        None
    } else {
        let value = &status.lifecycle;
        let parsed =
            uuid::Uuid::parse_str(&value.generation).map_err(|_| NativeFailure::InvalidEvidence)?;
        if value.version != 1
            || parsed.get_version_num() != 7
            || parsed.to_string() != value.generation
            || value.server_id != server_id
            || value.endpoint != endpoint
            || value.machine_id != machine_id
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Some(value.generation.clone())
    };
    Ok(LocalWorkerStatus {
        state: status.state,
        machine_id: machine_id.to_owned(),
        generation,
        controller_active: status.controller_active,
        management: None,
        desired_stopped: status.lifecycle.desired == Some(WorkerDesiredState::Stopped),
    })
}
