// SPDX-License-Identifier: Apache-2.0
//! The Git environment filter, on every platform.
//!
//! The repository tests are Unix-only, so the case-insensitive way Windows
//! reads environment names had no coverage (#4689).

use super::{Context, ENV_OVERRIDE, git, is_git_env};

#[test]
fn git_own_names_are_recognized() {
    for name in [
        "GIT_DIR",
        "GIT_WORK_TREE",
        "GIT_INDEX_FILE",
        "GIT_OBJECT_DIRECTORY",
    ] {
        assert!(is_git_env(name, false), "{name}");
        assert!(is_git_env(name, true), "{name}");
    }
}

#[test]
fn other_names_are_kept() {
    for name in ["PATH", "HOME", "GITHUB_TOKEN", "GIT", "GITX", "DIGIT"] {
        assert!(!is_git_env(name, false), "{name}");
        assert!(!is_git_env(name, true), "{name}");
    }
}

#[test]
fn the_name_is_matched_the_way_the_platform_reads_it() {
    // A differently cased prefix is the same variable as `GIT_` only where
    // the environment is case-insensitive: on Windows `Git_Dir` redirects
    // discovery, off Windows Git never reads it (#4689).
    for name in ["Git_Dir", "git_dir", "git_DIR", "GiT_dIr"] {
        assert!(is_git_env(name, true), "{name}");
        assert!(!is_git_env(name, false), "{name}");
    }
    // A name that keeps the prefix and changes only the rest is Git's own
    // either way.
    for name in ["GIT_dir", "GIT_WORK_tree"] {
        assert!(is_git_env(name, true), "{name}");
        assert!(is_git_env(name, false), "{name}");
    }
}

/// A mixed-case `Git_Dir` in the ambient environment must not redirect
/// discovery: on Windows it is the variable Git reads (#4689).
#[test]
fn a_mixed_case_git_dir_does_not_redirect_discovery() {
    let t = tempfile::tempdir().unwrap();
    let repo = t.path().join("repo");
    std::fs::create_dir(&repo).unwrap();
    let repo = repo.to_str().unwrap().to_string();

    let init = std::process::Command::new("git")
        .arg("-C")
        .arg(&repo)
        .arg("init")
        .output()
        .unwrap();
    assert!(
        init.status.success(),
        "git init: {}",
        String::from_utf8_lossy(&init.stderr)
    );

    // A directory that exists but is not a repository: were the redirection
    // to survive, Git would fail instead of finding the checkout.
    let elsewhere = t.path().join("elsewhere");
    std::fs::create_dir(&elsewhere).unwrap();
    let elsewhere = elsewhere.to_str().unwrap().to_string();

    let mut env: Vec<(String, String)> = std::env::vars().collect();
    env.push(("Git_Dir".to_string(), elsewhere));
    ENV_OVERRIDE.with(|v| *v.borrow_mut() = Some(env));

    let out = git(
        &Context::background(),
        &repo,
        &["rev-parse", "--show-toplevel"],
    );

    ENV_OVERRIDE.with(|v| *v.borrow_mut() = None);

    let out = out.expect("discovery followed the ambient Git_Dir");
    assert!(
        String::from_utf8_lossy(&out).contains("repo"),
        "{}",
        String::from_utf8_lossy(&out)
    );
}
