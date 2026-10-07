use std::{thread, time::Instant};

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
        let connector = fixture_connector(executable, temporary.path().join("state")).unwrap();
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
    let connector = fixture_connector(executable, root.clone()).unwrap();
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
    let binary = explicit_sidecar();
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().join("server");
    let connector = Connector::new(binary.clone(), root.clone()).unwrap();
    let original = connector.connect().unwrap();
    let generation = original.runtime_generation.clone();
    for _ in 0..8 {
        let current = connector.observe_launch(&original).unwrap();
        assert_eq!(current.runtime_generation, generation);
        assert_eq!(current.token, original.token);
    }
    let before = fs::read(root.join("desktop-client/device.json")).unwrap();
    external_runtime(
        &connector,
        &[
            "device",
            "revoke",
            "--id",
            &original.device_id,
            "--revision",
            "1",
        ],
    );
    // File inspection retains identity; authenticated product verification and
    // this explicit registration read enforce revocation independently.
    assert_eq!(
        connector.observe_launch(&original).unwrap().device_id,
        original.device_id
    );
    let inspected = connector.inspect_desktop_registration().unwrap();
    assert_eq!(inspected.state, DesktopRegistrationState::Revoked);
    let request = uuid::Uuid::now_v7().to_string();
    let recovered = connector
        .recover_desktop_registration(&inspected.device_id, &inspected.revision, &request)
        .unwrap();
    assert_ne!(recovered.device_id, original.device_id);
    assert_eq!(recovered.server_id, original.server_id);
    assert_ne!(
        before,
        fs::read(root.join("desktop-client/device.json")).unwrap()
    );
    let retained = fs::read(root.join("desktop-client/device.json")).unwrap();
    connector.shutdown_owned().unwrap();
    let fresh = Connector::new(binary, root.clone()).unwrap();
    let again = fresh.connect().unwrap();
    assert_eq!(again.device_id, recovered.device_id);
    assert_eq!(again.token, recovered.token);
    assert_ne!(again.runtime_generation, generation);
    assert_eq!(
        fs::read(root.join("desktop-client/device.json")).unwrap(),
        retained
    );
    fresh.shutdown_owned().unwrap();
}

#[test]
#[ignore = "requires an explicitly built Go sidecar; kills only its own temporary foreground server"]
fn real_supervisor_recovers_crash_respects_stop_and_quit_joins_owned_server() {
    let temporary = tempfile::tempdir().unwrap();
    let connector = Connector::new(explicit_sidecar(), temporary.path().join("server")).unwrap();
    let original = connector.connect().unwrap();
    connector.session.crash_for_test().unwrap();
    assert_eq!(connector.ensure().unwrap(), LocalServerState::Ready);
    let replaced = connector.observe_launch(&original).unwrap();
    assert_eq!(replaced.endpoint, original.endpoint);
    assert_eq!(replaced.device_id, original.device_id);
    assert_eq!(replaced.token, original.token);
    assert_ne!(replaced.runtime_generation, original.runtime_generation);
    external_runtime(&connector, &["server", "stop"]);
    let deadline = Instant::now() + Duration::from_secs(10);
    while connector.ensure().unwrap() != LocalServerState::Stopped {
        assert!(Instant::now() < deadline);
        thread::sleep(Duration::from_millis(25));
    }
    assert_eq!(
        connector.runtime().unwrap().generation,
        replaced.runtime_generation.clone().unwrap()
    );
    assert!(matches!(
        connector.retry_launch(),
        Err(NativeFailure::Stopped)
    ));
    let reopened = connector.connect().unwrap();
    assert_eq!(reopened.runtime_generation, replaced.runtime_generation);
    let endpoint = reopened.endpoint.clone();
    connector.shutdown_owned().unwrap();
    let address = endpoint.trim_start_matches("http://");
    let _listener = std::net::TcpListener::bind(address).unwrap();
}

#[test]
fn worker_proof_requires_a_distinct_canonical_machine_identity() {
    let mut value = metadata();
    value.kind = DeviceType::Worker;
    assert!(verified_connection(&document(&value), &value, DeviceType::Worker).is_err());
    value.machine_id = uuid::Uuid::now_v7().to_string();
    assert!(verified_connection(&document(&value), &value, DeviceType::Worker).is_ok());
    assert!(connection_from_bytes(&document(&value), &value).is_err());
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
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("server");
    let binary = explicit_sidecar();
    let connector = Connector::new(binary.clone(), root.clone()).unwrap();
    connector.connect().unwrap();
    let registered = connector
        .local_worker(LocalWorkerAction::Register, None)
        .unwrap();
    let started = connector
        .local_worker(LocalWorkerAction::Start, None)
        .unwrap();
    assert_eq!(started.state, LocalWorkerState::Running);
    let generation = started.generation.clone().unwrap();
    struct WorkerCleanup(PathBuf, PathBuf, String);
    impl Drop for WorkerCleanup {
        fn drop(&mut self) {
            let _ = Command::new(&self.0)
                .arg("--data-dir")
                .arg(&self.1)
                .args(["worker", "stop", "--generation", &self.2])
                .stdout(Stdio::null())
                .stderr(Stdio::null())
                .status();
        }
    }
    let _cleanup = WorkerCleanup(binary.clone(), root.clone(), generation.clone());
    let observer = connector.browser_observer().unwrap();
    assert!(std::sync::Arc::ptr_eq(
        &observer.session,
        &connector.session
    ));
    assert_eq!(
        observer
            .local_worker(LocalWorkerAction::Status, None)
            .unwrap()
            .generation,
        started.generation
    );
    let credential = fs::read(root.join("worker/device.json")).unwrap();
    connector.shutdown_owned().unwrap();
    let offline = external_local(&binary, &root, &["worker", "status"]);
    assert_eq!(offline["state"], "running");
    let fresh = Connector::new(binary, root.clone()).unwrap();
    fresh.connect().unwrap();
    let current = fresh.local_worker(LocalWorkerAction::Status, None).unwrap();
    assert_eq!(current.machine_id, registered.machine_id);
    assert_eq!(current.generation, started.generation);
    assert_eq!(
        fs::read(root.join("worker/device.json")).unwrap(),
        credential
    );
    assert_eq!(
        fresh
            .local_worker(LocalWorkerAction::Stop, Some(&generation))
            .unwrap()
            .state,
        LocalWorkerState::Exited
    );
    fresh.shutdown_owned().unwrap();
}

#[test]
#[ignore = "requires an explicitly built Go sidecar; pairs only with an owned temporary server"]
fn real_saved_connection_keeps_owner_local_and_remote_authority_separate() {
    let temporary = tempfile::tempdir().unwrap();
    let binary = explicit_sidecar();
    let server = Connector::new(binary.clone(), temporary.path().join("server")).unwrap();
    let local = server.connect().unwrap();
    let grant = external_runtime(
        &server,
        &[
            "device",
            "create-pairing",
            "--type",
            "client",
            "--name",
            "saved fixture",
        ],
    );
    let client = Connector::new(binary, temporary.path().join("saved-client")).unwrap();
    let id = uuid::Uuid::now_v7().to_string();
    let profile = client
        .pair_saved(
            &id,
            "Saved fixture",
            Zeroizing::new(fs::read(grant["code_file"].as_str().unwrap()).unwrap()),
        )
        .unwrap();
    let saved = client.connect_saved(&profile).unwrap();
    assert_eq!(saved.endpoint, local.endpoint);
    assert_eq!(saved.server_id, local.server_id);
    assert_ne!(saved.device_id, local.device_id);
    assert!(saved.runtime_generation.is_none());
    assert!(saved.runtime_key.is_none());
    assert!(!client.root.join("owner.json").exists());
    client.shutdown_owned().unwrap();
    assert!(server.observe_launch(&local).is_ok());
    server.shutdown_owned().unwrap();
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
    let connector = Arc::new(fixture_connector(executable, root.clone()).unwrap());
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
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().join("server");
    let binary = explicit_sidecar();
    let first = Connector::new(binary.clone(), root.clone()).unwrap();
    let original = first.connect().unwrap();
    let second = Connector::new(binary.clone(), root.clone()).unwrap();
    assert!(matches!(second.connect(), Err(NativeFailure::Busy)));
    second.shutdown_owned().unwrap();
    assert!(first.observe_launch(&original).is_ok());
    let credential = fs::read(root.join("desktop-client/device.json")).unwrap();
    first.shutdown_owned().unwrap();
    let fresh = Connector::new(binary, root.clone()).unwrap();
    let again = fresh.connect().unwrap();
    assert_eq!(again.server_id, original.server_id);
    assert_eq!(again.device_id, original.device_id);
    assert_eq!(again.token, original.token);
    assert_eq!(
        fs::read(root.join("desktop-client/device.json")).unwrap(),
        credential
    );
    fresh.shutdown_owned().unwrap();
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

// Unit operation fixtures share a framed resident adapter. Actual process,
// listener and credential ownership are covered separately by the Go binary.
#[cfg(unix)]
pub(crate) fn fixture_connector(executable: PathBuf, root: PathBuf) -> Result<Connector> {
    use std::os::unix::fs::PermissionsExt;
    let operation = executable.with_extension("operation");
    fs::rename(&executable, &operation).unwrap();
    let adapter = include_str!("resident_fixture.py").replace(
        "OPERATION_PATH",
        &serde_json::to_string(&operation.to_string_lossy()).unwrap(),
    );
    fs::write(&executable, adapter).unwrap();
    fs::set_permissions(&executable, fs::Permissions::from_mode(0o700)).unwrap();
    Connector::new(executable, root)
}

fn explicit_sidecar() -> PathBuf {
    PathBuf::from(std::env::var_os("DELIDEV_TEST_SIDECAR").expect("explicit sidecar required"))
}
fn external_local(binary: &Path, root: &Path, args: &[&str]) -> serde_json::Value {
    let output = Command::new(binary)
        .arg("--data-dir")
        .arg(root)
        .args(args)
        .stdin(Stdio::null())
        .stderr(Stdio::null())
        .output()
        .unwrap();
    let envelope: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(
        output.status.success(),
        "fixture operation failed: {}",
        envelope["error"]["code"]
    );
    envelope["result"].clone()
}
fn external_runtime(connector: &Connector, args: &[&str]) -> serde_json::Value {
    // This separate fixture client is external to native lifecycle ownership.
    // Pass owner authority only through private stdin, never process arguments.
    let owner: serde_json::Value =
        serde_json::from_slice(&fs::read(connector.root.join("owner.json")).unwrap()).unwrap();
    let endpoint = connector.runtime_endpoint().unwrap();
    let mut child = Command::new(&connector.executable)
        .arg("--data-dir")
        .arg(&connector.root)
        .args([
            "--server",
            &endpoint,
            "--token-stdin",
            "--request-id",
            &uuid::Uuid::now_v7().to_string(),
        ])
        .args(args)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    let mut input = child.stdin.take().unwrap();
    input
        .write_all(owner["token"].as_str().unwrap().as_bytes())
        .unwrap();
    drop(input);
    let output = child.wait_with_output().unwrap();
    let envelope: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
    assert!(
        output.status.success(),
        "fixture operation failed: {}",
        envelope["error"]["code"]
    );
    envelope["result"].clone()
}
