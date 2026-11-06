// SPDX-License-Identifier: Apache-2.0
//! Environment name matching for discovery. Not Unix-gated: the Windows
//! variants run in the native Windows CI job, the shared logic everywhere.

use super::*;

fn strings(v: &[&str]) -> Vec<String> {
    v.iter().map(|s| (*s).to_string()).collect()
}

#[test]
fn env_value_matches_names_case_insensitively_when_requested() {
    let env = strings(&["Path=C:\\x"]);
    assert_eq!(env_value_with(&env, "PATH", true), "C:\\x");
    assert_eq!(env_value_with(&env, "PATH", false), "");
}

#[test]
fn env_value_returns_the_last_matching_entry_in_either_mode() {
    let env = strings(&["Path=first", "PATH=second", "path=third", "OTHER=x"]);
    assert_eq!(env_value_with(&env, "PATH", true), "third");
    assert_eq!(env_value_with(&env, "PATH", false), "second");
}

#[test]
fn env_value_ignores_entries_without_a_separator_or_partial_names() {
    let env = strings(&["PATH", "PATHEXT=.EXE", "XPATH=y"]);
    assert_eq!(env_value_with(&env, "PATH", true), "");
    assert_eq!(env_value_with(&env, "PATH", false), "");
}

#[test]
fn env_value_keeps_equals_signs_inside_values() {
    let env = strings(&["Path=a=b"]);
    assert_eq!(env_value_with(&env, "PATH", true), "a=b");
}

#[test]
fn set_env_replaces_names_differing_only_by_case_when_requested() {
    let env = strings(&["Cargo_Target_Dir=a", "KEEP=1"]);
    assert_eq!(
        set_env_with(&env, "CARGO_TARGET_DIR", "b", true),
        strings(&["KEEP=1", "CARGO_TARGET_DIR=b"]),
    );
    assert_eq!(
        set_env_with(&env, "CARGO_TARGET_DIR", "b", false),
        strings(&["Cargo_Target_Dir=a", "KEEP=1", "CARGO_TARGET_DIR=b"]),
    );
}

#[test]
fn set_env_keeps_entries_without_a_separator_and_longer_names() {
    let env = strings(&[
        "CARGO_TARGET_DIR",
        "CARGO_TARGET_DIR_X=1",
        "CARGO_TARGET_DIR=old",
    ]);
    assert_eq!(
        set_env_with(&env, "CARGO_TARGET_DIR", "new", true),
        strings(&[
            "CARGO_TARGET_DIR",
            "CARGO_TARGET_DIR_X=1",
            "CARGO_TARGET_DIR=new"
        ]),
    );
}

#[cfg(not(windows))]
#[test]
fn platform_matching_stays_case_sensitive_outside_windows() {
    let env = strings(&["Path=/x"]);
    assert_eq!(env_value(&env, "PATH"), "");
    assert_eq!(
        set_env(&strings(&["Cargo_Target_Dir=a"]), "CARGO_TARGET_DIR", "b"),
        strings(&["Cargo_Target_Dir=a", "CARGO_TARGET_DIR=b"]),
    );
}

#[cfg(windows)]
#[test]
fn platform_matching_ignores_case_on_windows() {
    assert_eq!(env_value(&strings(&["Path=C:\\x"]), "PATH"), "C:\\x");
    assert_eq!(
        set_env(&strings(&["Cargo_Target_Dir=a"]), "CARGO_TARGET_DIR", "b"),
        strings(&["CARGO_TARGET_DIR=b"]),
    );
}

#[cfg(windows)]
#[test]
fn exec_runner_resolves_executables_from_a_path_spelled_path() {
    let root = std::env::var("SystemRoot").expect("SystemRoot is set on Windows");
    let system32 = format!("{root}\\System32");
    let ctx = Context::background();
    let out = ExecRunner
        .output(
            &ctx,
            &system32,
            &[format!("Path={system32}")],
            &strings(&["cmd", "/c", "ver"]),
        )
        .expect("cmd.exe is found through Path=");
    assert!(
        String::from_utf8_lossy(&out).contains("Windows"),
        "unexpected ver output: {out:?}"
    );
}

#[cfg(windows)]
#[test]
fn cargo_discovery_rejects_a_mixed_case_compiler_wrapper() {
    let ctx = Context::background();
    let req = Resolved {
        cwd: std::env::temp_dir().to_string_lossy().into_owned(),
        argv: strings(&["cargo", "build"]),
        env: strings(&["Rustc_Wrapper=x"]),
        protected: Vec::new(),
        runner: &ExecRunner,
    };
    let err = cargo::discover(&ctx, &req, &mut PresetResult::default())
        .expect_err("wrapper must be rejected");
    assert!(
        err.to_string().contains("custom compiler wrappers"),
        "unexpected error: {err}"
    );
}

#[test]
fn without_shim_dir_removes_only_the_shim_entry_and_keeps_order() {
    let shim = "/state/shims";
    for (path, expected) in [
        ("/state/shims:/a:/b", "/a:/b"),
        ("/a:/state/shims:/b", "/a:/b"),
        ("/a:/b:/state/shims", "/a:/b"),
        ("/a::/state/shims:/b", "/a::/b"),
        ("/state/shims", ""),
    ] {
        let env = strings(&["KEEP=1", &format!("PATH={path}")]);
        assert_eq!(
            without_shim_dir(&env, shim, false),
            strings(&["KEEP=1", &format!("PATH={expected}")]),
            "PATH={path}"
        );
    }
}

#[test]
fn without_shim_dir_leaves_other_environments_unchanged() {
    let shim = "/state/shims";
    let env = strings(&[
        "PATH=/a:/state/shims-other:/state/shims/bin",
        "OTHER=/state/shims",
        "NO_SEPARATOR",
    ]);
    assert_eq!(without_shim_dir(&env, shim, false), env);
    assert_eq!(without_shim_dir(&env, "", false), env);
    let with_shim = strings(&["PATH=/state/shims:/a"]);
    assert_eq!(without_shim_dir(&with_shim, "", false), with_shim);
}

#[test]
fn without_shim_dir_matches_the_path_name_like_the_platform() {
    let shim = "/state/shims";
    let env = strings(&["Path=/state/shims:/a"]);
    assert_eq!(without_shim_dir(&env, shim, true), strings(&["Path=/a"]));
    assert_eq!(without_shim_dir(&env, shim, false), env);
    let upper = strings(&["PATH=/STATE/SHIMS:/a"]);
    assert_eq!(without_shim_dir(&upper, shim, true), strings(&["PATH=/a"]));
    assert_eq!(without_shim_dir(&upper, shim, false), upper);
}

#[cfg(unix)]
#[test]
fn without_shim_dir_matches_a_symlinked_alias_of_the_shim_directory() {
    let dir = tempfile::tempdir().unwrap();
    let real = dir.path().join("shims");
    std::fs::create_dir(&real).unwrap();
    let alias = dir.path().join("alias");
    std::os::unix::fs::symlink(&real, &alias).unwrap();
    let shim = real.to_string_lossy().into_owned();
    let env = strings(&[&format!("PATH=/a:{}:/b", alias.to_string_lossy())]);
    assert_eq!(
        without_shim_dir(&env, &shim, false),
        strings(&["PATH=/a:/b"])
    );
}
