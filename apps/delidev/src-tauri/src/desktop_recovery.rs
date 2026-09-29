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
        let result = self.run(&[
            "device".into(),
            "inspect-local".into(),
            "--expected-endpoint".into(),
            format!("http://{}", self.listen).into(),
        ]);
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
                // Go enforces the native-owned endpoint before any recovery
                // intent or pairing mutation, using the same client authority.
                "--expected-endpoint".into(),
                format!("http://{}", self.listen).into(),
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
    #[cfg(unix)]
    fn native_registration_commands_pin_the_endpoint_in_go() {
        use std::os::unix::fs::PermissionsExt;
        let temporary = tempfile::tempdir().unwrap();
        let executable = temporary.path().join("sidecar");
        fs::write(
            &executable,
            r#"#!/bin/sh
if [ "$4" = inspect-local ]; then
    [ "$#" = 6 ] && [ "$5" = --expected-endpoint ] && [ "$6" = http://127.0.0.1:46310 ] || exit 2
else
    [ "$#" = 12 ] && [ "$6" = recover-local ] && [ "${11}" = --expected-endpoint ] && [ "${12}" = http://127.0.0.1:46310 ] || exit 2
fi
printf '%s' '{"version":1,"error":{"code":"unsupported"}}'
exit 1
"#,
        )
        .unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let connector = Connector::new(executable, temporary.path().join("server")).unwrap();
        assert!(matches!(
            connector.inspect_desktop_registration(),
            Err(NativeFailure::Incompatible)
        ));
        assert!(matches!(
            connector.recover_desktop_registration(
                &uuid::Uuid::now_v7().to_string(),
                "2",
                &uuid::Uuid::now_v7().to_string()
            ),
            Err(NativeFailure::Incompatible)
        ));
        assert!(!connector.root.exists());
    }

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
