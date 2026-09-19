use runlens::{config, entries::Entries, model::*, privacy::Redactor, snapshot};
#[test]
fn executable_identity_rejects_path_replacement_with_matching_metadata() {
    use std::io::Write;
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("executable");
    std::fs::write(&path, b"first-image").unwrap();
    let cancel = tokio_util::sync::CancellationToken::new();
    let identity = runlens::platform::executable_identity(&path, &cancel).unwrap();
    assert!(identity.revalidate(&path).is_ok());
    let identity = runlens::platform::executable_identity(&path, &cancel).unwrap();
    let original_metadata = std::fs::metadata(&path).unwrap();
    let modified = original_metadata.modified().unwrap();
    let mut replacement = tempfile::NamedTempFile::new_in(root.path()).unwrap();
    replacement.write_all(b"other-image").unwrap();
    replacement
        .as_file()
        .set_times(std::fs::FileTimes::new().set_modified(modified))
        .unwrap();
    // Windows persist cannot overwrite an open destination. Move the original
    // aside without closing its inspected handle, then replace the pathname.
    // This extra rename can go if persist supports replacing open Windows files.
    std::fs::rename(&path, root.path().join("retained-executable")).unwrap();
    replacement.persist(&path).unwrap();
    let replacement_metadata = std::fs::metadata(&path).unwrap();
    assert_eq!(replacement_metadata.len(), original_metadata.len());
    assert_eq!(replacement_metadata.modified().unwrap(), modified);
    assert_eq!(
        identity.revalidate(&path).unwrap_err().code,
        runlens::error::ErrorCode::Incomplete
    );
    let identity = runlens::platform::executable_identity(&path, &cancel).unwrap();
    std::fs::write(&path, b"changed-size").unwrap();
    assert_eq!(
        identity.revalidate(&path).unwrap_err().code,
        runlens::error::ErrorCode::Incomplete
    );
}
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
fn path_patterns_use_the_observed_platform_case_rules() {
    let patterns = vec!["private/**".into(), "output.bin".into()];
    for os in ["windows", "linux", "macos"] {
        let matcher = config::patterns_for_os(&patterns, os).unwrap();
        assert!(matcher.is_match("private/file"));
        assert_eq!(matcher.is_match("Private"), os == "windows");
        assert_eq!(matcher.is_match("Private/File"), os == "windows");
        assert_eq!(matcher.is_match("OUTPUT.BIN"), os == "windows");
    }
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("Private");
    std::fs::create_dir(&path).unwrap();
    let matcher = config::patterns(&patterns).unwrap();
    assert_eq!(
        snapshot::excluded(&path, root.path(), &matcher, &[]),
        cfg!(windows)
    );
}
#[test]
fn final_snapshot_record_and_directory_member_obey_byte_budget() {
    let root = tempfile::tempdir().unwrap();
    let root = root.path().canonicalize().unwrap();
    for index in 0..7 {
        std::fs::write(root.join(format!("{index}{}", "a".repeat(199))), "content").unwrap();
    }
    let redactor = Redactor::new(&root, &[], &config::Redaction::default()).unwrap();
    let cancel = tokio_util::sync::CancellationToken::new();
    let mut limits = config::Limits::default();
    let complete = snapshot::take(&root, &[], &[], &redactor, &limits, &cancel).unwrap();
    assert!(complete.complete);
    // One byte below the complete charge means only the final walked record
    // overruns, regardless of filesystem directory ordering.
    limits.total_bytes = (complete.bytes - 1) * 3;
    limits.validate().unwrap();
    let limited = snapshot::take(&root, &[], &[], &redactor, &limits, &cancel).unwrap();
    assert!(!limited.complete);
    assert!(limited.bytes <= limits.total_bytes / 3);
    assert_eq!(limited.entries.len() + 1, complete.entries.len());

    // Seven 200-byte names exceed 4096/3 only at the last directory member.
    limits.total_bytes = 4096;
    let limited = snapshot::take(&root, &[], &[], &redactor, &limits, &cancel).unwrap();
    assert!(!limited.complete);
    let directory = limited.entries.get("${workspace}").unwrap().unwrap();
    assert_eq!(directory.knowledge, Knowledge::Unknown);
    assert_eq!(directory.reason, Some(ObservationIssue::CollectionLimit));
}
#[test]
fn clean_environment_reserves_git_controls_with_platform_case_rules() {
    let root = tempfile::tempdir().unwrap();
    for (index, name) in [
        "GIT_CONFIG_GLOBAL",
        "GIT_CONFIG_COUNT",
        "GIT_CONFIG_KEY_0",
        "GIT_DIR",
        "GIT_WORK_TREE",
        "git_config_global",
        "Git_Config_Count",
        "git_config_key_0",
        "Git_Dir",
        "git_work_tree",
    ]
    .iter()
    .enumerate()
    {
        let result = runlens::clean::isolated_environment(
            &root.path().join(index.to_string()),
            &[(*name).into()],
        );
        let reserved = cfg!(windows) || *name == name.to_ascii_uppercase();
        assert_eq!(result.is_err(), reserved, "{name}");
        if let Err(error) = result {
            assert_eq!(error.code, runlens::error::ErrorCode::InvalidInput);
        }
    }
    assert!(
        runlens::clean::isolated_environment(
            &root.path().join("allowed"),
            &["RUNLENS_ALLOWED_CONTEXT".into()]
        )
        .is_ok()
    );
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
fn path_normalization_preserves_similar_external_roots() {
    use std::path::Path;
    let root = if cfg!(windows) {
        "C:/Runlens/project"
    } else {
        "/runlens/project"
    };
    let temporary = if cfg!(windows) {
        "C:/Runlens/temporary"
    } else {
        "/runlens/temporary"
    };
    let redactor = Redactor::new(
        Path::new(root),
        &[Path::new(temporary)],
        &config::Redaction::default(),
    )
    .unwrap();
    assert_eq!(redactor.path(Path::new(root)), "${workspace}");
    assert_eq!(
        redactor.path(Path::new(&format!("{root}/input"))),
        "${workspace}/input"
    );
    for sibling in [
        format!("{root}-cache/file"),
        format!("{temporary}-cache/file"),
    ] {
        assert_eq!(redactor.path(Path::new(&sibling)), sibling);
    }
    assert_eq!(
        redactor.text(&format!("--file={root}/input --copy={root}-cache/input")),
        format!("--file=${{workspace}}/input --copy={root}-cache/input")
    );
    assert_eq!(
        redactor.text(&format!("prefix{root}/input")),
        format!("prefix{root}/input")
    );
    #[cfg(windows)]
    assert_eq!(
        redactor.text(r"--file=C:\Runlens\project\input"),
        r"--file=${workspace}\input"
    );
    let volume = if cfg!(windows) { "C:/" } else { "/" };
    let redactor = Redactor::new(Path::new(volume), &[], &config::Redaction::default()).unwrap();
    assert_eq!(
        redactor.path(Path::new(&format!("{volume}file"))),
        "${workspace}/file"
    );
}
#[test]
fn hidden_sensitive_flags_still_redact_their_values() {
    let root = tempfile::tempdir().unwrap();
    for (args, indices, expected) in [
        (
            vec!["tool", "--token", "--password", "plain-canary"],
            vec![],
            vec!["tool", "--token", "[redacted]", "[redacted]"],
        ),
        (
            vec!["tool", "--password", "plain-canary"],
            vec![1],
            vec!["tool", "[redacted]", "[redacted]"],
        ),
        (
            vec!["tool", "--token", "--password=plain-canary", "public"],
            vec![],
            vec!["tool", "--token", "[redacted]", "public"],
        ),
    ] {
        let redactor = Redactor::new(
            root.path(),
            &[],
            &config::Redaction {
                argument_indices: indices,
                ..Default::default()
            },
        )
        .unwrap();
        let args = args.into_iter().map(str::to_owned).collect::<Vec<_>>();
        assert_eq!(redactor.argv(&args), expected);
    }
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
