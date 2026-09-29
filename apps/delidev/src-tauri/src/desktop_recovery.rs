use super::*;

#[derive(Clone, Copy, Deserialize, Serialize, PartialEq, Eq, Debug)]
#[serde(rename_all = "lowercase")]
pub enum DesktopRegistrationState {
    Authorized,
    Revoked,
    Recovering,
}

#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct DesktopRegistration {
    pub state: DesktopRegistrationState,
    pub server_id: String,
    pub device_id: String,
    pub revision: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub request_id: Option<String>,
}

fn valid_revision(value: &str) -> bool {
    value
        .parse::<u64>()
        .is_ok_and(|revision| revision > 0 && revision.to_string() == value)
}

impl Connector {
    pub fn inspect_desktop_registration(&self) -> Result<DesktopRegistration> {
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let result = self.run(&["device".into(), "inspect-local".into()]);
        let value: DesktopRegistration =
            serde_json::from_value(result?).map_err(|_| NativeFailure::InvalidEvidence)?;
        if canonical_id(&value.server_id).is_err()
            || canonical_id(&value.device_id).is_err()
            || !valid_revision(&value.revision)
            || match (&value.state, &value.request_id) {
                (DesktopRegistrationState::Recovering, Some(id)) => canonical_id(id).is_err(),
                (DesktopRegistrationState::Recovering, None) => true,
                (_, Some(_)) => true,
                (_, None) => false,
            }
        {
            return Err(NativeFailure::InvalidEvidence);
        }
        Ok(value)
    }

    pub fn recover_desktop_registration(
        &self,
        device: &str,
        revision: &str,
        request: &str,
    ) -> Result<Connection> {
        if canonical_id(device).is_err()
            || canonical_id(request).is_err()
            || !valid_revision(revision)
        {
            return Err(NativeFailure::InvalidInput);
        }
        let _guard = self.gate.try_lock().map_err(|_| NativeFailure::Busy)?;
        let result = (|| {
            let metadata: DeviceMetadata = serde_json::from_value(self.run(&[
                "--request-id".into(),
                request.into(),
                "device".into(),
                "recover-local".into(),
                "--id".into(),
                device.into(),
                "--revision".into(),
                revision.into(),
            ])?)
            .map_err(|_| NativeFailure::InvalidEvidence)?;
            if metadata.device_id == device {
                return Err(NativeFailure::InvalidEvidence);
            }
            // Reinspect the fixed active files after Go's durable publication.
            // No owner token, candidate path or argv selector crosses this API.
            self.read_local_connection(metadata)
        })();
        match &result {
            Ok(_) => tracing::info!(
                operation = "desktop_recovery",
                phase = "ready",
                request_id = request
            ),
            Err(code) => tracing::warn!(
                operation = "desktop_recovery",
                phase = "failed",
                request_id = request,
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
    fn revisions_preserve_exact_uint64_values() {
        for value in ["1", "9007199254740993", "18446744073709551615"] {
            assert!(valid_revision(value));
        }
        for value in ["0", "01", "+1", "1.0", "18446744073709551616", "-1", ""] {
            assert!(!valid_revision(value));
        }
    }
}
