use runlens::{config, entries::Entries, model::*, privacy::Redactor, snapshot};
#[test]
fn spill_preserves_sorted_records_and_roundtrip() {
    let mut entries = Entries::<u32>::new(32);
    for index in (0..300).rev() {
        entries.insert(format!("p{index:04}"), index).unwrap();
    }
    assert!(entries.spilled());
    assert_eq!(entries.len(), 300);
    assert_eq!(entries.get("p0021").unwrap(), Some(21));
    let encoded = serde_json::to_vec(&entries).unwrap();
    let decoded: Entries<u32> = serde_json::from_slice(&encoded).unwrap();
    assert_eq!(
        decoded.iter().map(|e| e.unwrap().1).collect::<Vec<_>>(),
        (0..300).collect::<Vec<_>>()
    );
    assert!(serde_json::from_str::<Entries<u32>>(r#"{"a":1,"a":2}"#).is_err());
}
#[test]
fn ignored_and_unknown_states_are_not_empty_files() {
    let root = tempfile::tempdir().unwrap();
    std::fs::write(root.path().join(".gitignore"), "ignored\n").unwrap();
    std::fs::write(root.path().join("ignored"), "before").unwrap();
    let root = root.path().canonicalize().unwrap();
    let redactor = Redactor::new(&root, &[], &config::Redaction::default()).unwrap();
    let cancel = tokio_util::sync::CancellationToken::new();
    let before = snapshot::take(
        &root,
        &[],
        &[],
        &redactor,
        &config::Limits::default(),
        &cancel,
    )
    .unwrap();
    std::fs::write(root.join("ignored"), "after").unwrap();
    let after = snapshot::take(
        &root,
        &[],
        &[],
        &redactor,
        &config::Limits::default(),
        &cancel,
    )
    .unwrap();
    let changes = snapshot::changes(&before.entries, &after.entries, true, true).unwrap();
    assert_eq!(
        changes.get("${workspace}/ignored").unwrap(),
        Some(ChangeKind::Modified)
    );
    assert_eq!(
        snapshot::difference(
            &FileState::unknown(ObservationIssue::PermissionDenied),
            &FileState::missing()
        ),
        Some(ChangeKind::Unknown)
    );
}
#[test]
fn argv_redaction_precedes_storage() {
    let root = tempfile::tempdir().unwrap();
    let redactor = Redactor::new(
        root.path(),
        &[],
        &config::Redaction {
            patterns: vec!["designated-canary".into()],
            ..Default::default()
        },
    )
    .unwrap();
    let argv = redactor.argv(&[
        "tool".into(),
        "--token".into(),
        "value-canary".into(),
        "--password=password-canary".into(),
        "designated-canary".into(),
    ]);
    let serialized = serde_json::to_string(&argv).unwrap();
    assert!(!serialized.contains("canary"));
    assert!(!serialized.contains("password-canary"));
}
