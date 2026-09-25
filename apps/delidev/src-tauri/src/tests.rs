use super::*;

fn metadata() -> DeviceMetadata {
    DeviceMetadata {
        version: 1,
        kind: DeviceType::Client,
        endpoint: "http://127.0.0.1:46310".into(),
        server_id: uuid::Uuid::now_v7().to_string(),
        device_id: uuid::Uuid::now_v7().to_string(),
        pairing_id: uuid::Uuid::now_v7().to_string(),
        machine_id: String::new(),
    }
}
fn document(value: &DeviceMetadata) -> Vec<u8> {
    serde_json::to_vec(&serde_json::json!({"version":value.version, "type":value.kind, "endpoint":value.endpoint, "server_id":value.server_id, "device_id":value.device_id, "pairing_id":value.pairing_id, "token":URL_SAFE_NO_PAD.encode([7;32])})).unwrap()
}
#[test]
fn client_credential_has_separate_exact_authority() {
    let value = metadata();
    let connection = connection_from_bytes(&document(&value), &value).unwrap();
    assert_eq!(connection.device_id, value.device_id);
    assert_eq!(connection.token, URL_SAFE_NO_PAD.encode([7; 32]));
    assert!(connection_from_bytes(&document(&value), &metadata()).is_err());
}
#[test]
fn reject_worker_foreign_fields_and_non_loopback_authority() {
    for (kind, endpoint, machine) in [
        ("worker", "http://127.0.0.1:46310", ""),
        ("client", "https://example.com", ""),
        ("client", "http://127.0.0.1:46310/path", ""),
        ("client", "http://127.0.0.1:0", ""),
        ("client", "http://127.0.0.1:46310", "foreign"),
    ] {
        let mut value = metadata();
        value.kind = if kind == "client" {
            DeviceType::Client
        } else {
            DeviceType::Worker
        };
        value.endpoint = endpoint.into();
        value.machine_id = machine.into();
        assert!(connection_from_bytes(&document(&value), &value).is_err());
    }
    let value = metadata();
    let mut body: serde_json::Value = serde_json::from_slice(&document(&value)).unwrap();
    body["owner_token"] = "must-not-expose".into();
    assert!(connection_from_bytes(&serde_json::to_vec(&body).unwrap(), &value).is_err());
}
#[test]
fn bounded_output_rejects_overflow() {
    assert_eq!(read_bounded(&b"123"[..], 3).unwrap(), b"123");
    assert_eq!(
        read_bounded(&b"1234"[..], 3),
        Err(NativeFailure::InvalidEvidence)
    );
}
#[test]
#[ignore = "requires an explicitly built Go sidecar; uses only a temporary private server scope"]
fn real_sidecar_connect_reuse_revocation_and_exit() {
    let binary =
        PathBuf::from(std::env::var_os("DELIDEV_TEST_SIDECAR").expect("explicit sidecar required"));
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("server");
    let mut connector = Connector::new(binary, root).unwrap();
    connector.listen = "127.0.0.1:0";
    // Always stop this private fixture server, including after assertion failure.
    struct Stop<'a>(&'a Connector);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            let _ = self.0.run(&["server".into(), "stop".into()]);
        }
    }
    let _stop = Stop(&connector);
    let first = connector.connect().unwrap();
    let second = connector.connect().unwrap();
    assert_eq!(first.device_id, second.device_id);
    assert_eq!(first.token, second.token);
    // A client/connector exit has no server stop side effect.
    let replacement = Connector::new(connector.executable.clone(), connector.root.clone()).unwrap();
    let reopened = replacement.connect().unwrap();
    assert_eq!(reopened.server_id, first.server_id);
    let owner: serde_json::Value =
        serde_json::from_slice(&fs::read(connector.root.join("owner.json")).unwrap()).unwrap();
    assert_ne!(first.token, owner["token"].as_str().unwrap());
    connector
        .run(&[
            "device".into(),
            "revoke".into(),
            "--id".into(),
            first.device_id.clone().into(),
            "--revision".into(),
            "1".into(),
        ])
        .unwrap();
    assert!(matches!(
        connector.connect(),
        Err(NativeFailure::CredentialUnavailable)
    ));
}
