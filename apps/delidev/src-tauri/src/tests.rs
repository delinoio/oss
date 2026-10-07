use super::*;

#[test]
fn sidecar_lookup_preserves_absolute_paths_without_relative_fallback() {
    let temporary = tempfile::tempdir().unwrap();
    let selected = temporary.path().join("native-bin");
    let input = std::env::join_paths([
        PathBuf::from("relative"),
        selected.clone(),
        PathBuf::new(),
        selected.clone(),
    ])
    .unwrap();
    let result = sidecar_lookup_path(Some(&input));
    let paths: Vec<_> = std::env::split_paths(&result).collect();
    assert!(paths.iter().all(|path| path.is_absolute()));
    assert_eq!(paths.iter().filter(|path| *path == &selected).count(), 1);
    #[cfg(target_os = "macos")]
    for required in ["/opt/homebrew/bin", "/usr/local/bin"] {
        assert!(paths.contains(&PathBuf::from(required)));
    }
    let oversized = OsString::from("x".repeat(32769));
    assert_eq!(
        sidecar_lookup_path(Some(&oversized)),
        sidecar_lookup_path(None)
    );
}

#[test]
#[cfg(unix)]
fn github_presentation_uses_closed_sidecar_and_checks_acknowledgment() {
    use std::os::unix::fs::PermissionsExt;
    let temporary = tempfile::tempdir().unwrap();
    for (index, response) in [
        r#"{"dispatched":true}"#,
        r#"{"dispatched":false}"#,
        r#"{"dispatched":true,"token":"unexpected"}"#,
    ]
    .iter()
    .enumerate()
    {
        let executable = temporary.path().join(format!("sidecar-{index}"));
        fs::write(&executable, format!("#!/bin/sh\n[ \"$3\" = presentation ] && [ \"$4\" = open-github ] && [ \"$5\" = --url-stdin ] && [ \"$#\" = 5 ] || exit 2\naddress=$(/bin/cat)\n[ \"$address\" = https://github.com/owner/repo/pull/1 ] || exit 3\nprintf '%s' '{{\"version\":1,\"result\":{response}}}'\n")).unwrap();
        fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
        let connector = Connector::new(executable, temporary.path().join("state")).unwrap();
        assert_eq!(
            connector
                .open_github("https://github.com/owner/repo/pull/1")
                .is_ok(),
            index == 0
        );
        assert_eq!(
            connector.open_github("file:///tmp/unsafe"),
            Err(NativeFailure::InvalidInput)
        );
        assert!(
            !connector.root.exists(),
            "presentation cannot bootstrap server state"
        );
    }
}

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
    serde_json::to_vec(&serde_json::json!({"version":value.version, "type":value.kind, "endpoint":value.endpoint, "server_id":value.server_id, "device_id":value.device_id, "pairing_id":value.pairing_id, "machine_id":value.machine_id, "token":URL_SAFE_NO_PAD.encode([7;32])})).unwrap()
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
#[cfg(unix)]
fn advanced_start_preserves_native_service_ownership_before_pairing() {
    use std::os::unix::fs::PermissionsExt;
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("private");
    fs::create_dir(&root).unwrap();
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
    fs::write(root.join("registration"), "original service ownership").unwrap();
    let executable = temporary.path().join("sidecar");
    // Model the Go admission boundary: desktop Start preserves a registration;
    // ordinary explicit CLI Start has independent, unchanged semantics.
    let script = r#"#!/bin/sh
case "$3:$4" in
  server:desktop-host)
    printf '%s' '{"version":1,"result":{"state":"service-managed"}}' ;;
  server:start)
    printf '%s' 'spawned' > "$2/competitor"
    printf '%s' '{"version":1,"error":{"code":"unavailable"}}' ;;
  *) exit 2 ;;
esac
"#;
    // A concurrent fork can retain a parent-authored script's writable file
    // description even after fs::write returns, causing Linux ETXTBSY at exec.
    // Keep the writer in a joined child whose descriptors cannot reach sibling
    // fixture children. Remove this isolation only with another lifetime proof.
    let written = Command::new("/bin/sh")
        .env_clear()
        .args([
            "-c",
            r#"umask 077; printf '%s' "$2" > "$1""#,
            "sidecar-fixture",
        ])
        .arg(&executable)
        .arg(script)
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .unwrap();
    assert!(
        written.success(),
        "sidecar fixture writer failed: {written}"
    );
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    let connector = Connector::new(executable, root.clone()).unwrap();
    assert_eq!(
        connector.connect().err(),
        Some(NativeFailure::ServiceManaged)
    );
    assert!(!root.join("competitor").exists());
    assert!(!root.join("desktop-client").exists());
    assert_eq!(
        fs::read_to_string(root.join("registration")).unwrap(),
        "original service ownership"
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
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    connector.listen = listener.local_addr().unwrap().to_string();
    drop(listener);
    // Always stop this private fixture server, including after assertion
    // failure.
    struct Stop<'a>(&'a Connector);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            let _ = self.0.run(&["server".into(), "stop".into()]);
        }
    }
    let _stop = Stop(&connector);
    let first = connector.connect().unwrap();
    assert_eq!(connector.ensure().unwrap(), LocalServerState::Ready);
    let second = connector.connect().unwrap();
    assert_eq!(first.device_id, second.device_id);
    assert_eq!(first.token, second.token);
    // A client/connector exit has no server stop side effect.
    let replacement = Connector::new(connector.executable.clone(), connector.root.clone()).unwrap();
    // The production authority cannot adopt this fixture's alternate listener.
    assert!(matches!(
        replacement.connect(),
        Err(NativeFailure::Incompatible)
    ));
    let mut same_authority =
        Connector::new(connector.executable.clone(), connector.root.clone()).unwrap();
    same_authority.listen = connector.listen.clone();
    let reopened = same_authority.connect().unwrap();
    assert_eq!(reopened.server_id, first.server_id);
    let owner: serde_json::Value =
        serde_json::from_slice(&fs::read(connector.root.join("owner.json")).unwrap()).unwrap();
    assert_ne!(first.token, owner["token"].as_str().unwrap());
    assert!(connector.local_worker_proof().is_err());
    let worker_root = connector.root.join("worker");
    assert!(!worker_root.exists(), "proof reads cannot pair a Worker");
    let grant = connector
        .run(&[
            "device".into(),
            "create-pairing".into(),
            "--type".into(),
            "worker".into(),
            "--name".into(),
            "private proof fixture".into(),
        ])
        .unwrap();
    let code = Zeroizing::new(fs::read(grant["code_file"].as_str().unwrap()).unwrap());
    let mut pair = Command::new(&connector.executable)
        .arg("--data-dir")
        .arg(&connector.root)
        .args(["worker", "pair", "--worker-dir"])
        .arg(&worker_root)
        .arg("--code-stdin")
        .stdin(Stdio::piped())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    {
        use std::io::Write;
        pair.stdin.take().unwrap().write_all(&code).unwrap();
    }
    assert!(pair.wait().unwrap().success());
    let proof = connector.local_worker_proof().unwrap();
    assert_eq!(proof.server_id, first.server_id);
    assert_eq!(proof.endpoint, first.endpoint);
    assert_ne!(proof.token, first.token);
    assert_ne!(proof.token, owner["token"].as_str().unwrap());
    let path = worker_root.join("device.json");
    let original = Zeroizing::new(fs::read(&path).unwrap());
    let mut foreign: serde_json::Value = serde_json::from_slice(&original).unwrap();
    foreign["server_id"] = uuid::Uuid::now_v7().to_string().into();
    fs::write(&path, serde_json::to_vec(&foreign).unwrap()).unwrap();
    assert!(matches!(
        connector.local_worker_proof(),
        Err(NativeFailure::InvalidEvidence)
    ));
    fs::write(&path, &original).unwrap();
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
    let inspected = connector.inspect_desktop_registration().unwrap();
    assert_eq!(inspected.state, DesktopRegistrationState::Revoked);
    assert_eq!(inspected.device_id, first.device_id);
    let request = uuid::Uuid::now_v7().to_string();
    // A production connector cannot recover this fixture's alternate endpoint.
    // Failure must precede new registrations and fixed credential publication.
    let old_credential =
        Zeroizing::new(fs::read(connector.root.join("desktop-client/device.json")).unwrap());
    assert!(matches!(
        replacement.recover_desktop_registration(&first.device_id, &inspected.revision, &request),
        Err(NativeFailure::Incompatible)
    ));
    assert!(!connector.root.join("desktop-recovery.json").exists());
    assert!(!connector.root.join("desktop-recoveries").exists());
    assert_eq!(
        *old_credential,
        fs::read(connector.root.join("desktop-client/device.json")).unwrap()
    );
    let devices = connector.run(&["device".into(), "list".into()]).unwrap();
    assert_eq!(devices["resources"].as_array().unwrap().len(), 2);
    let recovered = connector
        .recover_desktop_registration(&first.device_id, &inspected.revision, &request)
        .unwrap();
    let retried = connector
        .recover_desktop_registration(&first.device_id, &inspected.revision, &request)
        .unwrap();
    assert_ne!(recovered.device_id, first.device_id);
    assert_ne!(recovered.token, first.token);
    assert_eq!(recovered.server_id, first.server_id);
    assert_eq!(recovered.device_id, retried.device_id);
    assert_eq!(recovered.token, retried.token);
    assert_eq!(connector.connect().unwrap().device_id, recovered.device_id);
    assert_eq!(connector.local_worker_proof().unwrap().token, proof.token);
}

#[test]
#[ignore = "requires an explicitly built Go sidecar; kills only its own temporary foreground server"]
fn real_supervisor_recovers_crash_respects_stop_and_quit_joins_owned_server() {
    let binary =
        PathBuf::from(std::env::var_os("DELIDEV_TEST_SIDECAR").expect("explicit sidecar required"));
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("server");
    let mut connector = Connector::new(binary.clone(), root.clone()).unwrap();
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap().to_string();
    drop(listener);
    connector.listen = address.clone();
    let connector = std::sync::Arc::new(connector);
    let mut process = Command::new(binary)
        .args(["--data-dir"])
        .arg(&root)
        .args([
            "server",
            "run",
            "--listen",
            &address,
            "--allowed-origins",
            ORIGINS,
        ])
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    struct ChildCleanup<'a>(&'a mut std::process::Child);
    impl Drop for ChildCleanup<'_> {
        fn drop(&mut self) {
            let _ = self.0.kill();
            let _ = self.0.wait();
        }
    }
    let process = ChildCleanup(&mut process);
    let deadline = Instant::now() + Duration::from_secs(10);
    while !root.join("server.json").exists() {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(25));
    }
    let connection = connector.connect().unwrap();
    process.0.kill().unwrap();
    process.0.wait().unwrap();
    let supervision = Supervision::new(std::sync::Arc::clone(&connector));
    let cleanup = Connector::new(connector.executable.clone(), root).unwrap();
    struct Stop<'a>(&'a Connector);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            let _ = self.0.run(&["server".into(), "stop".into()]);
            let deadline = Instant::now() + Duration::from_secs(5);
            while self.0.root.join("server.json").exists() && Instant::now() < deadline {
                thread::sleep(Duration::from_millis(25));
            }
        }
    }
    let _stop = Stop(&cleanup);
    let deadline = Instant::now() + Duration::from_secs(15);
    while supervision.status().state != LocalServerState::Ready {
        assert!(
            Instant::now() < deadline,
            "supervisor did not recover: {:?}",
            supervision.status()
        );
        thread::sleep(Duration::from_millis(25));
    }
    let reopened = connector.connect().unwrap();
    assert_eq!(reopened.server_id, connection.server_id);
    assert_eq!(reopened.device_id, connection.device_id);
    cleanup.run(&["server".into(), "stop".into()]).unwrap();
    supervision.refresh();
    let deadline = Instant::now() + Duration::from_secs(10);
    while supervision.status().state != LocalServerState::Stopped {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(25));
    }
    while cleanup.root.join("server.json").exists() {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(25));
    }
    assert_eq!(connector.ensure().unwrap(), LocalServerState::Stopped);
    // Stopping observation alone leaves the admitted process alive. Normal
    // native Quit additionally joins only this connector's original children.
    connector.connect().unwrap();
    let started = Instant::now();
    drop(supervision);
    assert!(started.elapsed() < Duration::from_secs(2));
    assert!(cleanup.run(&["server".into(), "status".into()]).is_ok());
    connector.shutdown_owned().unwrap();
    assert!(cleanup.run(&["server".into(), "status".into()]).is_err());
    assert!(cleanup.root.join("desktop-client/device.json").exists());
}

#[test]
fn worker_proof_requires_a_distinct_canonical_machine_identity() {
    let mut value = metadata();
    value.kind = DeviceType::Worker;
    assert!(verified_connection(&document(&value), &value, DeviceType::Worker).is_err());
    value.machine_id = uuid::Uuid::now_v7().to_string();
    assert!(verified_connection(&document(&value), &value, DeviceType::Worker).is_ok());
    assert!(connection_from_bytes(&document(&value), &value).is_ok());
    for id in [
        "",
        "not-an-identity",
        "00000000-0000-4000-8000-000000000000",
    ] {
        value.machine_id = id.into();
        assert!(verified_connection(&document(&value), &value, DeviceType::Worker).is_err());
    }
}

#[test]
#[ignore = "requires an explicitly built Go sidecar; starts only its owned private Worker"]
fn real_local_worker_registration_start_status_and_offline_stop() {
    let binary =
        PathBuf::from(std::env::var_os("DELIDEV_TEST_SIDECAR").expect("explicit sidecar required"));
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("server");
    let mut connector = Connector::new(binary, root).unwrap();
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    connector.listen = listener.local_addr().unwrap().to_string();
    drop(listener);
    struct Stop<'a>(&'a Connector);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            if let Ok(status) = self.0.local_worker(LocalWorkerAction::Status, None)
                && let Some(generation) = status.generation
            {
                let _ = self
                    .0
                    .local_worker(LocalWorkerAction::Stop, Some(&generation));
            }
            let _ = self.0.run(&["server".into(), "stop".into()]);
        }
    }
    let _stop = Stop(&connector);
    connector.connect().unwrap();
    assert!(
        connector
            .local_worker(LocalWorkerAction::Status, None)
            .is_err()
    );
    assert!(!connector.root.join("worker").exists());
    let registered = connector
        .local_worker(LocalWorkerAction::Register, None)
        .unwrap();
    assert_eq!(registered.state, LocalWorkerState::NotStarted);
    let retry = connector
        .local_worker(LocalWorkerAction::Register, None)
        .unwrap();
    assert_eq!(retry.machine_id, registered.machine_id);
    let started = connector
        .local_worker(LocalWorkerAction::Start, None)
        .unwrap();
    assert_eq!(started.state, LocalWorkerState::Running);
    let generation = started.generation.as_deref().unwrap();
    assert_eq!(started.machine_id, registered.machine_id);
    assert_eq!(
        connector
            .local_worker(LocalWorkerAction::Start, None)
            .unwrap()
            .generation,
        started.generation
    );
    assert!(
        connector
            .local_worker(LocalWorkerAction::Stop, None)
            .is_err()
    );
    assert!(
        connector
            .local_worker(
                LocalWorkerAction::Stop,
                Some(&uuid::Uuid::now_v7().to_string())
            )
            .is_err()
    );
    assert_eq!(
        connector
            .local_worker(LocalWorkerAction::Status, None)
            .unwrap()
            .state,
        LocalWorkerState::Running
    );
    // A second native controller reads the same process; dropping it leaves the
    // detached Worker running without changing its scope or process generation.
    let mut observer =
        Connector::new(connector.executable.clone(), connector.root.clone()).unwrap();
    observer.listen = connector.listen.clone();
    assert_eq!(
        observer
            .local_worker(LocalWorkerAction::Status, None)
            .unwrap()
            .generation,
        started.generation
    );
    drop(observer);
    connector.run(&["server".into(), "stop".into()]).unwrap();
    let deadline = Instant::now() + Duration::from_secs(5);
    while connector.root.join("server.json").exists() {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(25));
    }
    let stopped = connector
        .local_worker(LocalWorkerAction::Stop, Some(generation))
        .unwrap();
    assert_eq!(stopped.state, LocalWorkerState::Exited);
    assert!(!stopped.controller_active);
    assert_eq!(
        connector.local_worker_proof().unwrap().machine_id,
        registered.machine_id
    );
}

#[test]
#[ignore = "requires an explicitly built Go sidecar; pairs only with an owned temporary server"]
fn real_saved_connection_keeps_owner_local_and_remote_authority_separate() {
    let binary =
        PathBuf::from(std::env::var_os("DELIDEV_TEST_SIDECAR").expect("explicit sidecar required"));
    let temporary = tempfile::tempdir().unwrap();
    let mut server = Connector::new(binary.clone(), temporary.path().join("server")).unwrap();
    server.listen = "127.0.0.1:0".into();
    struct Stop<'a>(&'a Connector);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            let _ = self.0.run(&["server".into(), "stop".into()]);
        }
    }
    let _stop = Stop(&server);
    let local = server.connect().unwrap();
    let grant = server
        .run(&[
            "device".into(),
            "create-pairing".into(),
            "--type".into(),
            "client".into(),
            "--name".into(),
            "saved fixture".into(),
        ])
        .unwrap();
    let bytes = Zeroizing::new(fs::read(grant["code_file"].as_str().unwrap()).unwrap());
    let client_root = temporary.path().join("separate-client");
    let client = Connector::new(binary, client_root.clone()).unwrap();
    assert!(client.saved_connections().unwrap().is_empty());
    assert!(!client_root.exists());
    let id = uuid::Uuid::now_v7().to_string();
    let paired = client.pair_saved(&id, "Saved fixture", bytes).unwrap();
    assert!(paired.state == SavedConnectionState::Paired);
    assert_eq!(paired.server_id, local.server_id);
    assert_ne!(paired.device_id, local.device_id);
    let connected = client.connect_saved(&paired).unwrap();
    assert_eq!(connected.device_id, paired.device_id);
    assert_ne!(connected.token, local.token);
    let owner: serde_json::Value =
        serde_json::from_slice(&fs::read(server.root.join("owner.json")).unwrap()).unwrap();
    assert_ne!(connected.token, owner["token"].as_str().unwrap());
    let inventory = client.saved_connections().unwrap();
    assert!(inventory == vec![paired.clone()]);
    let metadata = serde_json::to_string(&inventory).unwrap();
    assert!(!metadata.contains(&connected.token));
    assert!(!metadata.contains("\"code\""));
    assert!(client.retry_saved(&id).unwrap() == paired);
    let mut foreign = paired.clone();
    foreign.server_id = uuid::Uuid::now_v7().to_string();
    assert!(client.connect_saved(&foreign).is_err());
    for name in ["owner.json", "server.json", "state.sqlite", "worker"] {
        assert!(!client_root.join(name).exists());
    }
    assert!(client.saved_worker_proof(&paired).is_err());
    assert!(
        client
            .saved_worker(&paired, LocalWorkerAction::Status, None)
            .is_err()
    );
    let registered = client
        .saved_worker(&paired, LocalWorkerAction::Register, None)
        .unwrap();
    assert_eq!(registered.state, LocalWorkerState::NotStarted);
    assert_eq!(
        client
            .saved_worker(&paired, LocalWorkerAction::Register, None)
            .unwrap()
            .machine_id,
        registered.machine_id
    );
    let proof = client.saved_worker_proof(&paired).unwrap();
    assert_eq!(proof.machine_id, registered.machine_id);
    assert_eq!(proof.server_id, paired.server_id);
    assert_eq!(proof.endpoint, paired.endpoint);
    assert_ne!(proof.token, connected.token);
    assert_ne!(proof.token, local.token);
    assert!(client.saved_worker_proof(&foreign).is_err());
    assert!(
        client
            .saved_worker(&foreign, LocalWorkerAction::Start, None)
            .is_err()
    );
    let started = client
        .saved_worker(&paired, LocalWorkerAction::Start, None)
        .unwrap();
    let generation = started.generation.as_deref().unwrap();
    struct StopWorker<'a>(&'a Connector, &'a SavedConnection, &'a str);
    impl Drop for StopWorker<'_> {
        fn drop(&mut self) {
            let _ = self
                .0
                .saved_worker(self.1, LocalWorkerAction::Stop, Some(self.2))
                .or_else(|_| {
                    self.0
                        .retained_worker(&self.1.id, LocalWorkerAction::Stop, Some(self.2))
                });
        }
    }
    let _stop_worker = StopWorker(&client, &paired, generation);
    assert_eq!(started.state, LocalWorkerState::Running);
    assert!(
        client
            .saved_worker(
                &paired,
                LocalWorkerAction::Stop,
                Some(&uuid::Uuid::now_v7().to_string())
            )
            .is_err()
    );
    assert_eq!(
        client
            .saved_worker(&paired, LocalWorkerAction::Status, None)
            .unwrap()
            .generation,
        started.generation
    );
    let observer = Connector::new(client.executable.clone(), client.root.clone()).unwrap();
    assert_eq!(
        observer
            .saved_worker(&paired, LocalWorkerAction::Status, None)
            .unwrap()
            .generation,
        started.generation
    );
    drop(observer);
    let request = uuid::Uuid::now_v7().to_string();
    let renamed = client
        .rename_saved(&id, &request, paired.revision, "Renamed active Worker")
        .unwrap();
    assert_eq!(renamed.name, "Renamed active Worker");
    assert_eq!(renamed.revision, paired.revision + 1);
    assert!(renamed.same_authority(&paired));
    assert_eq!(
        client.connect_saved(&paired).unwrap().token,
        connected.token
    );
    assert_eq!(
        client.saved_worker_proof(&paired).unwrap().machine_id,
        registered.machine_id
    );
    assert_eq!(
        client
            .saved_worker(&paired, LocalWorkerAction::Status, None)
            .unwrap()
            .generation,
        started.generation
    );
    assert!(
        client
            .rename_saved(&id, &request, paired.revision, "Renamed active Worker")
            .unwrap()
            == renamed
    );
    assert!(
        client
            .rename_saved(&id, &request, paired.revision, "Changed retry")
            .is_err()
    );
    assert!(
        client
            .rename_saved(
                &id,
                &uuid::Uuid::now_v7().to_string(),
                paired.revision,
                "Stale"
            )
            .is_err()
    );
    server
        .run(&[
            "device".into(),
            "revoke".into(),
            "--id".into(),
            paired.device_id.clone().into(),
            "--revision".into(),
            "1".into(),
        ])
        .unwrap();
    assert!(matches!(
        client.connect_saved(&paired),
        Err(NativeFailure::CredentialUnavailable)
    ));
    assert!(client.inspect_saved(&id).unwrap() == renamed);
    let removal_request = uuid::Uuid::now_v7().to_string();
    let removed = client
        .remove_saved(&id, &removal_request, renamed.revision)
        .unwrap();
    assert!(removed.state == SavedConnectionState::Removed);
    assert!(client.saved_connections().unwrap().is_empty());
    assert!(client.removed_connections("").unwrap().connections == vec![removed.clone()]);
    assert!(
        client
            .remove_saved(&id, &removal_request, renamed.revision)
            .unwrap()
            == removed
    );
    assert!(client.connect_saved(&paired).is_err());
    assert!(client.saved_worker_proof(&paired).is_err());
    assert!(
        client
            .retained_worker(&id, LocalWorkerAction::Register, None)
            .is_err()
    );
    assert_eq!(
        client
            .retained_worker(&id, LocalWorkerAction::Status, None)
            .unwrap()
            .generation,
        started.generation
    );
    assert!(
        client
            .retained_worker(
                &id,
                LocalWorkerAction::Stop,
                Some(&uuid::Uuid::now_v7().to_string())
            )
            .is_err()
    );
    let stopped = client
        .retained_worker(&id, LocalWorkerAction::Stop, Some(generation))
        .unwrap();
    assert_eq!(stopped.state, LocalWorkerState::Exited);
    assert!(!stopped.controller_active);
    assert!(!client_root.join("worker").exists());
    assert!(
        !client_root
            .join("connections")
            .join(&id)
            .join("client/device.json")
            .exists()
    );
    drop(_stop_worker);
    assert!(client.retry_saved(&id).is_err());
    assert!(
        client
            .pair_saved("../escape", "invalid", Zeroizing::new(vec![]))
            .is_err()
    );
}

#[test]
#[cfg(unix)]
fn host_launch_is_once_joined_and_never_publishes_ready_after_stop() {
    use std::{os::unix::fs::PermissionsExt, sync::Arc};
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("private");
    fs::create_dir_all(root.join("desktop-client")).unwrap();
    let value = metadata();
    let body = serde_json::to_string(&serde_json::json!({"version":value.version,"type":value.kind,"endpoint":value.endpoint,"server_id":value.server_id,"device_id":value.device_id,"pairing_id":value.pairing_id,"machine_id":""})).unwrap();
    fs::write(root.join("desktop-client/device.json"), document(&value)).unwrap();
    let executable = temporary.path().join("sidecar");
    let script = format!(
        r#"#!/bin/sh
printf '%s:%s:%s\n' "$3" "$4" "${{10}}" >> "$2/operations"
if [ "$3:$4:${{10}}" = server:desktop-host:launch ]; then
  while [ ! -f "$2/release" ]; do /bin/sleep .01; done
fi
if [ "$3" = server ]; then
  if [ -f "$2/stopped" ]; then
    printf '%s' '{{"version":1,"result":{{"state":"stopped"}}}}'
  else
    printf '%s' '{{"version":1,"result":{{"reused":true,"status":{{"version":"0.1.0","protocol_version":1,"listener":"http://127.0.0.1:46310"}}}}}}'
  fi
else
  printf '%s' '{{"version":1,"result":{body}}}'
fi
"#
    );
    fs::write(&executable, script).unwrap();
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    let connector = Arc::new(Connector::new(executable, root.clone()).unwrap());
    let runtime = Arc::new(Supervision::new(connector));
    for _ in 0..20 {
        assert!(runtime.launch_connection().unwrap().is_none());
    }
    fs::write(root.join("release"), "").unwrap();
    let limit = Instant::now() + COMMAND_TIMEOUT + Duration::from_secs(5);
    let first = loop {
        if let Some(value) = runtime.launch_connection().unwrap() {
            break value;
        }
        assert!(
            Instant::now() < limit,
            "bounded sidecar phases: {}",
            fs::read_to_string(root.join("operations")).unwrap_or_default()
        );
        thread::sleep(Duration::from_millis(10));
    };
    let readers: Vec<_> = (0..8)
        .map(|_| {
            let runtime = Arc::clone(&runtime);
            thread::spawn(move || runtime.launch_connection().unwrap().unwrap())
        })
        .collect();
    for reader in readers {
        let observed = reader.join().unwrap();
        assert_eq!(observed.device_id, first.device_id);
        assert_eq!(observed.token, first.token);
    }
    fs::write(root.join("stopped"), "").unwrap();
    assert!(matches!(
        runtime.launch_connection(),
        Err(NativeFailure::Stopped)
    ));
    assert!(matches!(
        runtime.retry_launch(),
        Err(NativeFailure::Stopped)
    ));
    runtime.refresh();
    runtime.stop();
    let actions = fs::read_to_string(root.join("operations")).unwrap();
    assert_eq!(
        actions
            .lines()
            .filter(|v| *v == "server:desktop-host:launch")
            .count(),
        1
    );
    assert_eq!(
        actions
            .lines()
            .filter(|v| *v == "device:pair-local:")
            .count(),
        1
    );
    assert!(
        !actions.lines().any(|v| v == "server:start:"
            || v == "server:stop:"
            || v == "server:desktop-host:retry")
    );
}

#[test]
fn missing_bundled_sidecar_is_a_retained_launch_failure() {
    use std::sync::Arc;
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("private");
    let connector =
        Arc::new(Connector::new(temporary.path().join("missing"), root.clone()).unwrap());
    let runtime = Supervision::new(connector);
    let limit = Instant::now() + Duration::from_secs(5);
    loop {
        match runtime.launch_connection() {
            Err(NativeFailure::SidecarMissing) => break,
            Ok(None) => {
                assert!(Instant::now() < limit);
                thread::sleep(Duration::from_millis(10));
            }
            _ => panic!("unexpected launch observation"),
        }
    }
    for _ in 0..10 {
        assert!(matches!(
            runtime.launch_connection(),
            Err(NativeFailure::SidecarMissing)
        ));
    }
    runtime.stop();
    assert!(!root.exists());
}

#[test]
#[ignore = "requires an explicitly built Go sidecar; auto-launches only temporary private scopes"]
fn real_fresh_hosts_share_identity_and_quit_only_their_owned_server() {
    use std::sync::Arc;
    let binary =
        PathBuf::from(std::env::var_os("DELIDEV_TEST_SIDECAR").expect("explicit sidecar required"));
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("server");
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap().to_string();
    drop(listener);
    let mut first = Connector::new(binary.clone(), root.clone()).unwrap();
    first.listen = address.clone();
    let first = Arc::new(first);
    let mut second = Connector::new(binary.clone(), root.clone()).unwrap();
    second.listen = address.clone();
    let second = Arc::new(second);
    let cleanup = Connector::new(binary, root.clone()).unwrap();
    struct Stop<'a>(&'a Connector);
    impl Drop for Stop<'_> {
        fn drop(&mut self) {
            let _ = self.0.run(&["server".into(), "stop".into()]);
        }
    }
    let _stop = Stop(&cleanup);
    let first_host = Supervision::new(Arc::clone(&first));
    fn ready(runtime: &Supervision) -> Connection {
        // Bootstrap owns three bounded commands; readers do not replay them.
        let deadline = Instant::now() + COMMAND_TIMEOUT * 3 + Duration::from_secs(5);
        loop {
            match runtime.launch_connection() {
                Ok(Some(value)) => return value,
                Ok(None) => {}
                Err(failure) => panic!("launch failure: {failure:?}"),
            }
            assert!(
                Instant::now() < deadline,
                "bounded native launch did not settle: {:?}",
                runtime.status()
            );
            thread::sleep(Duration::from_millis(25));
        }
    }
    let original = ready(&first_host);
    eprintln!("fixture phase: first host authenticated");
    // Establish the original owner before testing borrowed-host Quit. With
    // simultaneous launch either admitted host may legitimately own the child.
    let second_host = Supervision::new(Arc::clone(&second));
    let concurrent = ready(&second_host);
    eprintln!("fixture phase: concurrent host authenticated");
    assert_eq!(original.server_id, concurrent.server_id);
    assert_eq!(original.device_id, concurrent.device_id);
    assert_eq!(original.token, concurrent.token);
    for _ in 0..4 {
        assert_eq!(ready(&first_host).device_id, original.device_id);
    }
    second_host.stop();
    second.shutdown_owned().unwrap();
    eprintln!("fixture phase: second host joined");
    assert!(cleanup.run(&["server".into(), "status".into()]).is_ok());
    cleanup.run(&["server".into(), "stop".into()]).unwrap();
    assert!(matches!(
        first_host.launch_connection(),
        Err(NativeFailure::Stopped)
    ));
    assert!(matches!(
        first_host.retry_launch(),
        Err(NativeFailure::Stopped)
    ));
    first_host.stop();
    first.shutdown_owned().unwrap();
    eprintln!("fixture phase: stopped host joined");
    let deadline = Instant::now() + Duration::from_secs(10);
    while root.join("server.json").exists() {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(25));
    }
    let mut fresh = Connector::new(first.executable.clone(), root).unwrap();
    fresh.listen = address;
    let fresh = Arc::new(fresh);
    let fresh_host = Supervision::new(Arc::clone(&fresh));
    assert_eq!(ready(&fresh_host).server_id, original.server_id);
    eprintln!("fixture phase: fresh host reopened original server");
    fresh_host.stop();
    fresh.shutdown_owned().unwrap();
    assert!(cleanup.run(&["server".into(), "status".into()]).is_err());
    for name in ["owner.json", "state.sqlite", "desktop-client/device.json"] {
        assert!(cleanup.root.join(name).exists());
    }
}

#[test]
fn oauth_polling_uses_original_verified_descriptor_without_sidecar_or_command_gate() {
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("state");
    fs::create_dir_all(root.join("desktop-client")).unwrap();
    let path = root.join("desktop-client/device.json");
    let original = metadata();
    fs::write(&path, document(&original)).unwrap();
    let connector = Connector::new(temporary.path().join("missing-sidecar"), root).unwrap();
    assert_eq!(
        connector.oauth_server_identity(),
        Err(NativeFailure::CredentialUnavailable)
    );
    *connector.oauth_identity.lock().unwrap() = Some(original.clone());
    let _unrelated_command = connector.gate.lock().unwrap();
    for _ in 0..20 {
        assert_eq!(
            connector.oauth_server_identity().unwrap(),
            original.server_id
        );
    }
    fs::write(&path, document(&metadata())).unwrap();
    assert_eq!(
        connector.oauth_server_identity(),
        Err(NativeFailure::InvalidEvidence)
    );
    fs::write(&path, document(&original)).unwrap();
    assert_eq!(
        connector.oauth_server_identity().unwrap(),
        original.server_id
    );
    connector.exiting.store(true, Ordering::Release);
    assert_eq!(
        connector.oauth_server_identity(),
        Err(NativeFailure::Stopped)
    );
}

#[cfg(unix)]
#[test]
fn automatic_worker_keeps_stop_and_quits_only_its_original_child() {
    use std::os::unix::fs::PermissionsExt;
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("state");
    fs::create_dir_all(root.join("worker")).unwrap();
    let client = metadata();
    let mut worker = client.clone();
    worker.kind = DeviceType::Worker;
    worker.device_id = uuid::Uuid::now_v7().to_string();
    worker.machine_id = uuid::Uuid::now_v7().to_string();
    let mut client_view: serde_json::Value = serde_json::from_slice(&document(&client)).unwrap();
    let mut worker_view: serde_json::Value = serde_json::from_slice(&document(&worker)).unwrap();
    client_view.as_object_mut().unwrap().remove("token");
    worker_view.as_object_mut().unwrap().remove("token");
    fs::write(root.join("worker/device.json"), document(&worker)).unwrap();
    let generation = uuid::Uuid::now_v7().to_string();
    let runtime = serde_json::json!({"state":"running", "controller_active":true, "lifecycle": {"version":1,"generation":generation,"server_id":worker.server_id,"machine_id":worker.machine_id,"endpoint":worker.endpoint,"desired":"running"}});
    fs::write(
        root.join("status.json"),
        serde_json::to_vec(&serde_json::json!({"version":1,"result":runtime})).unwrap(),
    )
    .unwrap();
    let executable = temporary.path().join("sidecar");
    let script = format!(
        r##"#!/bin/sh
case "$3:$4" in
device:inspect) printf '%s\n' '{client}' ;;
worker:inspect) printf '%s\n' '{worker}' ;;
worker:status) if [ -f '{root}/status-fail' ]; then exit 2; fi; /bin/cat '{root}/status.json' ;;
worker:stop) exit 2 ;;
worker:desktop-prepare) if [ -f '{root}/prepare-fail' ]; then exit 2; fi; printf '%s\n' '{{"version":1,"result":{{"executable":"{executable}"}}}}' ;;
worker:desktop-host)
  printf '%s\n' '{admission}'
  IFS= read -r action
  printf '%s\n' "$action" > '{root}/stop.json'
  ;;
*) exit 2 ;;
esac
"##,
        client = serde_json::json!({"version":1,"result":client_view}),
        worker = serde_json::json!({"version":1,"result":worker_view}),
        admission = serde_json::json!({"version":1,"result":{"started":true,"generation":generation,"worker":runtime}}),
        root = root.display(),
        executable = executable.display(),
    );
    fs::write(&executable, script).unwrap();
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    let connector = Connector::new(executable, root.clone()).unwrap();
    connector.worker_auto_enabled.store(true, Ordering::Release);
    *connector.worker_client_id.lock().unwrap() = Some(client.device_id.clone());
    connector
        .worker_launch_pending
        .store(false, Ordering::Release);
    let borrowed = connector
        .local_worker(LocalWorkerAction::Status, None)
        .unwrap();
    assert!(!borrowed.management.unwrap().owned_by_app);
    connector.worker_management_failure(NativeFailure::CredentialUnavailable);
    assert_eq!(
        connector
            .local_worker(LocalWorkerAction::Status, None)
            .unwrap()
            .management
            .unwrap()
            .state,
        LocalWorkerManagementState::Blocked
    );
    assert!(
        connector
            .local_worker(LocalWorkerAction::Start, Some(&generation))
            .is_err()
    );
    fs::write(root.join("prepare-fail"), []).unwrap();
    assert!(
        connector
            .local_worker(LocalWorkerAction::Start, None)
            .is_err()
    );
    assert!(connector.worker_launch_pending.load(Ordering::Acquire));
    fs::remove_file(root.join("prepare-fail")).unwrap();
    connector.manage_worker();
    let owned = connector
        .local_worker(LocalWorkerAction::Status, None)
        .unwrap();
    assert!(owned.management.unwrap().owned_by_app);
    assert!(
        connector
            .local_worker(
                LocalWorkerAction::Stop,
                Some(&uuid::Uuid::now_v7().to_string())
            )
            .is_err()
    );
    assert!(connector.worker_pause_generation.lock().unwrap().is_none());
    fs::write(root.join("status-fail"), []).unwrap();
    assert!(
        connector
            .local_worker(LocalWorkerAction::Stop, Some(&generation))
            .is_err()
    );
    assert_eq!(
        *connector.worker_pause_generation.lock().unwrap(),
        Some(generation.clone())
    );
    fs::remove_file(root.join("status-fail")).unwrap();
    assert!(
        connector
            .local_worker(LocalWorkerAction::Stop, Some(&generation))
            .is_err()
    );
    let mut lost_stop = runtime.clone();
    lost_stop["state"] = "exited".into();
    lost_stop["controller_active"] = false.into();
    fs::write(
        root.join("status.json"),
        serde_json::to_vec(&serde_json::json!({"version":1,"result":lost_stop})).unwrap(),
    )
    .unwrap();
    connector.manage_worker();
    assert_eq!(
        connector.worker_management.lock().unwrap().state,
        LocalWorkerManagementState::Blocked
    );
    assert_eq!(
        connector.hosted.lock().unwrap().len(),
        1,
        "lost stop reply started another child"
    );
    let mut stopped = runtime.clone();
    stopped["state"] = "exited".into();
    stopped["controller_active"] = false.into();
    stopped["lifecycle"]["desired"] = "stopped".into();
    fs::write(
        root.join("status.json"),
        serde_json::to_vec(&serde_json::json!({"version":1,"result":stopped})).unwrap(),
    )
    .unwrap();
    connector.manage_worker();
    assert_eq!(
        connector.worker_management.lock().unwrap().state,
        LocalWorkerManagementState::Paused
    );
    assert_eq!(
        connector.hosted.lock().unwrap().len(),
        1,
        "paused observation started another child"
    );
    connector.shutdown_owned().unwrap();
    assert_eq!(
        fs::read_to_string(root.join("stop.json")).unwrap(),
        "{\"version\":1,\"action\":\"stop\"}\n"
    );
    assert!(connector.hosted.lock().unwrap().is_empty());
    assert!(matches!(
        connector.local_worker(LocalWorkerAction::Start, None),
        Err(NativeFailure::Stopped)
    ));
}
