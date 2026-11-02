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
