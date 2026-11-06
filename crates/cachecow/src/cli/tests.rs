// SPDX-License-Identifier: Apache-2.0

use super::{
    managed::{Claims, Provenance, detect_preset, metadata_path, paths_of, records},
    *,
};
use crate::{
    lease,
    preset::{PresetResult, Root, RootKind},
    repository::Info,
    session::{Action, Plan},
};

fn strings(values: &[&str]) -> Vec<String> {
    values.iter().map(|v| (*v).to_string()).collect()
}

#[test]
fn json_request_scan() {
    for (args, want) in [
        (&["status", "--json"][..], true),
        (&["status", "--json", "--bogus"], true),
        (&["status", "--bogus", "--json"], true),
        (&["status", "--json=x"], true),
        (&["status", "--bogus"], false),
        (&["run", "--", "tool", "--json"], false),
        (&["run", "--preset", "bogus", "--", "tool", "--json"], false),
        (&["status", "--source", "--json"], false),
        (&["status", "--preset=--json"], false),
        (&["status", "--source", "--json", "--json"], true),
        (&["status", "--source=a", "--json"], true),
        (&[], false),
    ] {
        assert_eq!(requests_json(&strings(args)), want, "{args:?}");
    }
}

#[test]
fn argument_errors() {
    for args in [
        &["run", "--wat"][..],
        &["run", "--source"],
        &["run", "--replace=true"],
        &["--preset", "unknown"],
        &["run", "--preset="],
    ] {
        let e = parse(&strings(args)).expect_err(&format!("{args:?}"));
        assert_eq!(error::find::<Failure>(&e).map(|f| f.code), Some(2));
    }
    let o = parse(&strings(&[
        "run",
        "--preset=cargo",
        "--allow-copy",
        "--",
        "cargo",
        "--json",
        "-x",
    ]))
    .unwrap();
    assert_eq!(o.preset, "cargo");
    assert!(o.allow_copy && !o.json);
    assert_eq!(o.args, strings(&["run"]));
    assert_eq!(o.command, strings(&["cargo", "--json", "-x"]));
}

#[test]
fn failure_mapping() {
    assert_eq!(fail_with(false, &anyhow!("plain")), 125);
    assert_eq!(fail_with(false, &bad("usage")), 2);
    let busy = anyhow::Error::new(LeaseError::RegistryBusy).context("records");
    assert_eq!(fail_with(false, &busy), 125);
    let resolve = anyhow::Error::new(shim::ResolveError {
        tool: shim::Tool::Cargo,
        denied: false,
    });
    assert_eq!(fail_with(false, &resolve), 127);
}

#[test]
fn config_record_error_classification() {
    let code = |e: anyhow::Error| fail_with(false, &e);
    let io = anyhow::Error::new(std::io::Error::from(std::io::ErrorKind::PermissionDenied))
        .context("write settings");
    assert_eq!(code(config_record_error(io)), 125);
    let busy = anyhow::Error::new(LeaseError::RegistryBusy).context("records");
    assert_eq!(code(config_record_error(busy)), 125);
    let version = anyhow!("unsupported settings schemaVersion");
    assert_eq!(code(config_record_error(version)), 2);
    assert_eq!(code(config_failure("bad source")), 2);
}

fn root(path: &str) -> Root {
    Root {
        path: path.to_string(),
        kinds: vec![RootKind::Target],
    }
}

fn result(roots: &[&str]) -> PresetResult {
    PresetResult {
        roots: roots.iter().map(|p| root(p)).collect(),
        ..PresetResult::default()
    }
}

/// Private state storage as production creates it. Windows temporary
/// directories inherit grants for other principals.
fn private_state_root() -> (tempfile::TempDir, String) {
    let temp = tempfile::tempdir().unwrap();
    let base = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    let root = paths::join2(&base, "state");
    lease::ensure_private_directory(&root).unwrap();
    (temp, root)
}

fn claims_plan(root: &str, action: Action, current: &PresetResult) -> Plan {
    Plan {
        root: root.to_string(),
        action,
        claims: serde_json::to_value(Claims {
            current: current.clone(),
            ..Claims::default()
        })
        .unwrap(),
        ..Plan::default()
    }
}

#[test]
fn writer_invalidates_overlapping_provenance() {
    for nested in [false, true] {
        let (_temp, root) = private_state_root();
        let target = paths::join2(&root, "target");
        let old = result(&[&target, &paths::join2(&root, "build-a")]);
        let changed_target = if nested {
            paths::join2(&target, "nested")
        } else {
            target.clone()
        };
        let current = result(&[&changed_target, &paths::join2(&root, "build-b")]);
        let unrelated = result(&[&paths::join2(&root, "unrelated")]);
        for r in [&old, &unrelated] {
            state::write(
                &metadata_path(&root, r),
                &Provenance {
                    schema_version: 1,
                    paths: paths_of(r),
                    ..Provenance::default()
                },
            )
            .unwrap();
        }
        let p = claims_plan(&root, Action::Run, &current);
        let hooks = records();
        (hooks.before.unwrap())(&p).unwrap();
        (hooks.after.unwrap())(&p, 1).unwrap();
        assert!(
            !std::path::Path::new(&metadata_path(&root, &old)).exists(),
            "stale successful provenance survived"
        );
        assert!(std::path::Path::new(&metadata_path(&root, &unrelated)).exists());
    }
}

#[test]
fn prepare_requires_supervisor_clone_evidence() {
    let (_temp, root) = private_state_root();
    let current = result(&[&paths::join2(&root, "cache")]);
    let mut p = claims_plan(&root, Action::Prepare, &current);
    let after = records().after.unwrap();
    after(&p, 0).unwrap();
    assert!(
        !std::path::Path::new(&metadata_path(&root, &current)).exists(),
        "retained cache acquired provenance"
    );
    p.prepared_all = true;
    after(&p, 0).unwrap();
    assert!(std::path::Path::new(&metadata_path(&root, &current)).exists());
}

#[cfg(unix)]
#[test]
fn translates_every_worktree_root_occurrence() {
    let from = "/repo/wt";
    let to = "/repo/main";
    let values = strings(&[
        "PATH=/repo/wt/node_modules/.bin:/usr/bin",
        "INIT_CWD=/repo/wt",
        "OTHER=/repo/wt2/x",
        "NESTED=/x/repo/wt/y",
        "--config",
        "/repo/wt/rspack.config.js",
        "FOO=1",
    ]);
    assert_eq!(
        crate::preset::translate_worktree_root(&values, from, to),
        strings(&[
            "PATH=/repo/main/node_modules/.bin:/usr/bin",
            "INIT_CWD=/repo/main",
            "OTHER=/repo/main2/x",
            "NESTED=/x/repo/main/y",
            "--config",
            "/repo/main/rspack.config.js",
            "FOO=1",
        ])
    );
}

#[cfg(unix)]
#[test]
fn source_working_directory_uses_canonical_cwd() {
    let current = Info {
        cwd: "/private/tmp/r/wt/sub/dir".into(),
        root: "/private/tmp/r/wt".into(),
        ..Info::default()
    };
    assert_eq!(
        super::managed::source_working_directory(&current, "/private/tmp/r/main").unwrap(),
        "/private/tmp/r/main/sub/dir"
    );
    let root_cwd = Info {
        cwd: "/r/wt".into(),
        root: "/r/wt".into(),
        ..Info::default()
    };
    assert_eq!(
        super::managed::source_working_directory(&root_cwd, "/r/main").unwrap(),
        "/r/main"
    );
    let outside = Info {
        cwd: "/elsewhere".into(),
        root: "/r/wt".into(),
        ..Info::default()
    };
    assert!(super::managed::source_working_directory(&outside, "/r/main").is_err());
}

#[test]
fn detect_walks_canonical_directories() {
    let temp = tempfile::tempdir().unwrap();
    let base = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    let repo = paths::join2(&base, "repo");
    let sub = paths::join2(&repo, "sub");
    std::fs::create_dir_all(&sub).unwrap();
    let info = |cwd: &str| Info {
        cwd: cwd.to_string(),
        root: repo.clone(),
        ..Info::default()
    };
    let message = |e: anyhow::Error| error::find::<Failure>(&e).unwrap().cause.clone();
    assert!(message(detect_preset(&info(&sub)).unwrap_err()).contains("cannot detect a preset"));
    std::fs::write(paths::join2(&repo, "Cargo.toml"), "").unwrap();
    assert_eq!(detect_preset(&info(&sub)).unwrap(), "cargo");
    std::fs::write(paths::join2(&sub, "rspack.config.js"), "").unwrap();
    assert_eq!(detect_preset(&info(&sub)).unwrap(), "rspack");
    std::fs::write(paths::join2(&repo, "rsbuild.config.ts"), "").unwrap();
    assert!(message(detect_preset(&info(&repo)).unwrap_err()).contains("ambiguous preset"));
}

#[test]
fn quotes_shell_notice_lines() {
    let mut b = String::new();
    write_block_lines(&mut b, "+ ", b"one\r\ntwo\n");
    assert_eq!(b, "\n+ one\n+ two");
}

#[cfg(unix)]
#[test]
fn path_lookup_drops_only_non_utf8_entries() {
    use std::{ffi::OsStr, os::unix::ffi::OsStrExt};
    assert_eq!(utf8_path_entries(OsStr::new("/a:/b")), "/a:/b");
    assert_eq!(utf8_path_entries(OsStr::new("")), "");
    assert_eq!(
        utf8_path_entries(OsStr::from_bytes(b"/a:/b\xff:/c")),
        "/a:/c"
    );
    assert_eq!(utf8_path_entries(OsStr::from_bytes(b"\xff")), "");
}

#[cfg(unix)]
#[test]
fn managed_argument_check_reports_child_argv_index() {
    use std::{ffi::OsString, os::unix::ffi::OsStringExt};
    let ok = [OsString::from("build"), OsString::from("--release")];
    assert!(check_managed_args(&ok, 1).is_ok());
    let args = [
        OsString::from("build"),
        OsString::from_vec(b"a\xffb".to_vec()),
    ];
    let error = check_managed_args(&args, 1).unwrap_err();
    let text = error_text(&error);
    assert_eq!(
        text,
        "managed commands require UTF-8 arguments and environment; ARG #2 is not valid UTF-8"
    );
    assert!(!text.contains('\u{fffd}'));
}

#[test]
fn lookup_env_reads_path_from_the_given_environment() {
    let env = strings(&["HOME=/h", "PATH=/a:/b", "PATH=/c:/d", "PATHEXT=.EXE;.CMD"]);
    let lookup = LookupEnv::from_env(&env);
    assert_eq!(lookup.path, "/c:/d", "last entry wins like the child sees");
    assert_eq!(lookup.path_ext, ".EXE;.CMD");
    let empty = LookupEnv::from_env(&strings(&["HOME=/h"]));
    assert_eq!(empty.path, "");
    assert_eq!(empty.path_ext, "");
}

#[test]
fn lookup_env_drops_entries_with_replacement_characters() {
    // PATH is split with the platform separator (`:` on Unix, `;` on Windows),
    // so the input and the expectation are built with `std::env::join_paths`.
    let join = |entries: &[&str]| {
        std::env::join_paths(entries)
            .unwrap()
            .into_string()
            .unwrap()
    };
    let env = strings(&[
        &format!("PATH={}", join(&["/a", "/b\u{fffd}", "/c"])),
        "PATHEXT=.EXE;.C\u{fffd}D;.BAT",
    ]);
    let lookup = LookupEnv::from_env(&env);
    assert_eq!(lookup.path, join(&["/a", "/c"]));
    assert_eq!(lookup.path_ext, ".EXE;.BAT");
}

#[cfg(unix)]
#[test]
fn look_path_searches_only_the_given_environment() {
    use std::os::unix::fs::PermissionsExt;
    let temp = tempfile::tempdir().unwrap();
    let root = std::fs::canonicalize(temp.path()).unwrap();
    let derived = root.join("derived/bin");
    let source = root.join("source/bin");
    for dir in [&derived, &source] {
        std::fs::create_dir_all(dir).unwrap();
        let tool = dir.join("fixture-tool");
        std::fs::write(&tool, "#!/bin/sh\n").unwrap();
        std::fs::set_permissions(&tool, std::fs::Permissions::from_mode(0o700)).unwrap();
    }
    let source_only = root.join("source/only");
    std::fs::create_dir_all(&source_only).unwrap();
    let marker = source_only.join("source-only-tool");
    std::fs::write(&marker, "#!/bin/sh\n").unwrap();
    std::fs::set_permissions(&marker, std::fs::Permissions::from_mode(0o700)).unwrap();

    let source_env = strings(&[&format!(
        "PATH={}:{}",
        source.display(),
        source_only.display()
    )]);
    let derived_env = strings(&[&format!("PATH={}", derived.display())]);
    let found = look_path("fixture-tool", &LookupEnv::from_env(&source_env)).unwrap();
    assert_eq!(found, source.join("fixture-tool").to_str().unwrap());
    let found = look_path("fixture-tool", &LookupEnv::from_env(&derived_env)).unwrap();
    assert_eq!(found, derived.join("fixture-tool").to_str().unwrap());
    // A tool that only the other environment provides is not found.
    assert!(look_path("source-only-tool", &LookupEnv::from_env(&derived_env)).is_err());
    assert!(look_path("source-only-tool", &LookupEnv::from_env(&source_env)).is_ok());
}

#[test]
fn passthrough_remediation_matches_the_failure() {
    // Exit code 2 is an argument rejection (Windows refuses arguments that are
    // not valid UTF-8). Its remedy must not talk about the executable.
    let rejected = passthrough_remediation(2);
    assert!(rejected.contains("valid UTF-8"), "{rejected}");
    assert!(!rejected.contains("real executable"), "{rejected}");
    // Any other code means the real tool could not start.
    for code in [125, 126, 127] {
        assert_eq!(
            passthrough_remediation(code),
            "Check the real executable and its permissions."
        );
    }
}
