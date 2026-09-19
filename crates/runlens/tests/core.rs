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

#[cfg(unix)]
#[test]
fn snapshots_cover_links_types_permissions_deletions_and_exclusions() {
    use std::os::unix::fs::{PermissionsExt, symlink};
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().canonicalize().unwrap();
    std::fs::write(root.join("delete"), "old").unwrap();
    std::fs::write(root.join("replace"), "old").unwrap();
    std::fs::write(root.join("executable"), "same contents").unwrap();
    std::fs::set_permissions(
        root.join("executable"),
        std::fs::Permissions::from_mode(0o644),
    )
    .unwrap();
    std::fs::create_dir(root.join("excluded")).unwrap();
    std::fs::write(root.join("excluded/private"), "never inspect").unwrap();
    symlink("replace", root.join("link")).unwrap();
    let redactor = Redactor::new(&root, &[], &config::Redaction::default()).unwrap();
    let cancel = tokio_util::sync::CancellationToken::new();
    let take = || {
        snapshot::take(
            &root,
            &["excluded".into()],
            &[],
            &redactor,
            &config::Limits::default(),
            &cancel,
        )
        .unwrap()
    };
    let before = take();
    assert!(
        before
            .entries
            .get("${workspace}/excluded/private")
            .unwrap()
            .is_none()
    );
    assert_eq!(
        before
            .entries
            .get("${workspace}/link")
            .unwrap()
            .unwrap()
            .link_target
            .as_deref(),
        Some("replace")
    );
    std::fs::remove_file(root.join("delete")).unwrap();
    std::fs::remove_file(root.join("replace")).unwrap();
    std::fs::create_dir(root.join("replace")).unwrap();
    std::fs::set_permissions(
        root.join("executable"),
        std::fs::Permissions::from_mode(0o755),
    )
    .unwrap();
    let after = take();
    let changes = snapshot::changes(&before.entries, &after.entries, true, true).unwrap();
    assert_eq!(
        changes.get("${workspace}/delete").unwrap(),
        Some(ChangeKind::Deleted)
    );
    assert_eq!(
        changes.get("${workspace}/replace").unwrap(),
        Some(ChangeKind::TypeChanged)
    );
    assert_eq!(
        changes.get("${workspace}/executable").unwrap(),
        Some(ChangeKind::Modified)
    );
    assert!(changes.get("${workspace}/link").unwrap().is_none());
    std::fs::write(root.join("literal\\name"), "literal backslash").unwrap();
    use std::os::unix::ffi::OsStringExt;
    symlink(
        std::ffi::OsString::from_vec(vec![0xff]),
        root.join("non-unicode-link"),
    )
    .unwrap();
    let invalid = take();
    assert!(!invalid.complete);
    assert!(
        invalid
            .entries
            .get("${workspace}/literal\\name")
            .unwrap()
            .is_some()
    );
    assert_eq!(
        invalid
            .entries
            .get("${workspace}/non-unicode-link")
            .unwrap()
            .unwrap()
            .knowledge,
        Knowledge::Unknown
    );
}

#[cfg(unix)]
#[test]
fn unreadable_files_preserve_unknown_instead_of_empty_content() {
    use std::os::unix::fs::PermissionsExt;
    if unsafe { libc::geteuid() } == 0 {
        return;
    }
    let temporary = tempfile::tempdir().unwrap();
    let root = temporary.path().canonicalize().unwrap();
    let path = root.join("unreadable");
    std::fs::write(&path, "not empty").unwrap();
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o000)).unwrap();
    let redactor = Redactor::new(&root, &[], &config::Redaction::default()).unwrap();
    let observed = snapshot::take(
        &root,
        &[],
        &[],
        &redactor,
        &config::Limits::default(),
        &tokio_util::sync::CancellationToken::new(),
    )
    .unwrap();
    std::fs::set_permissions(&path, std::fs::Permissions::from_mode(0o600)).unwrap();
    let state = observed
        .entries
        .get("${workspace}/unreadable")
        .unwrap()
        .unwrap();
    assert!(!observed.complete);
    assert_eq!(state.knowledge, Knowledge::Unknown);
    assert_eq!(state.reason, Some(ObservationIssue::PermissionDenied));
    assert!(state.sha256.is_none());
}
