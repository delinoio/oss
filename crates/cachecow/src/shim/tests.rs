// SPDX-License-Identifier: Apache-2.0

use std::fs;

use super::{
    install::{MANIFEST_NAME, digest, filename, lock, read_manifest, save_manifest},
    *,
};
use crate::paths;

fn write(p: &str, s: &str) {
    fs::write(p, s).unwrap();
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(p, fs::Permissions::from_mode(0o700)).unwrap();
    }
}

fn temp() -> (tempfile::TempDir, String) {
    let t = tempfile::tempdir().unwrap();
    let p = paths::path_string(t.path()).unwrap();
    (t, p)
}

fn private() -> (tempfile::TempDir, String) {
    let (t, base) = temp();
    let p = paths::join2(&base, "개인 shims");
    super::private::private_dir(&p).unwrap();
    (t, p)
}

fn strings(v: &[&str]) -> Vec<String> {
    v.iter().map(|s| (*s).to_string()).collect()
}

#[test]
fn classification() {
    use Mode::{Clean as C, Managed as M, Passthrough as P};
    use Reason::{CacheOperation as CO, CleanOperation as CL, Query as Q, Unknown as U};
    use Tool::{Cargo, Rsbuild, Rspack};
    let cases: Vec<(Tool, Vec<&str>, Mode, Reason)> = vec![
        (
            Cargo,
            vec![
                "+stable",
                "--color",
                "always",
                "--config",
                "path = '한 글'",
                "build",
                "--target-dir",
                "a b",
                "--",
                "--help",
            ],
            M,
            CO,
        ),
        (
            Cargo,
            vec!["+nightly", "-vv", "clean", "--target-dir=a b", "-p", "x"],
            C,
            CL,
        ),
        (Cargo, vec!["build", "--help"], P, Q),
        (Cargo, vec!["-V"], P, Q),
        (Cargo, vec!["metadata", "--format-version", "1"], P, Q),
        (Cargo, vec!["locate-project"], P, Q),
        (Cargo, vec!["tree"], P, Q),
        (Cargo, vec!["custom", "build"], P, U),
        (Cargo, vec!["--unknown", "build"], P, U),
        (Cargo, vec!["--config"], P, U),
        (Cargo, vec![], P, Q),
        (Cargo, vec!["run", "--", "--version"], M, CO),
        (Cargo, vec!["package"], M, CO),
        (Cargo, vec!["package", "--no-verify"], M, CO),
        (Cargo, vec!["package", "--allow-dirty", "-p", "x"], M, CO),
        (
            Cargo,
            vec!["+stable", "-v", "package", "--target-dir", "a b"],
            M,
            CO,
        ),
        (Cargo, vec!["publish"], M, CO),
        (Cargo, vec!["publish", "--dry-run"], M, CO),
        (
            Cargo,
            vec!["publish", "--no-verify", "--registry", "r"],
            M,
            CO,
        ),
        (Cargo, vec!["publish", "--list"], M, CO),
        (Cargo, vec!["package", "--", "--list"], M, CO),
        (Cargo, vec!["package", "--list"], P, Q),
        (Cargo, vec!["package", "-l"], P, Q),
        (Cargo, vec!["package", "-p", "x", "--list"], P, Q),
        (Cargo, vec!["package", "--help"], P, Q),
        (Cargo, vec!["publish", "-h"], P, Q),
        (Cargo, vec!["install", "--path", "."], P, U),
        (Rspack, vec![], M, CO),
        (Rspack, vec!["--watch", "--config", "한 글.js"], M, CO),
        (Rsbuild, vec!["--config=x.js", "dev"], M, CO),
        (Rspack, vec!["build", "--config", "a b.js"], M, CO),
        (Rspack, vec!["serve"], M, CO),
        (Rsbuild, vec!["dev"], M, CO),
        (Rsbuild, vec!["preview"], P, Q),
        (Rsbuild, vec!["inspect"], P, Q),
        (Rsbuild, vec!["custom"], P, U),
        (Rsbuild, vec![], M, CO),
        (Rsbuild, vec!["--port", "3000"], M, CO),
        (Rsbuild, vec!["--port=3000"], M, CO),
        (Rsbuild, vec!["--open"], M, CO),
        (Rsbuild, vec!["-c", "rsbuild.config.ts"], M, CO),
        (Rsbuild, vec!["--config=rsbuild.config.ts"], M, CO),
        (Rsbuild, vec!["--host", "0.0.0.0"], M, CO),
        (Rsbuild, vec!["--help"], P, Q),
        (Rsbuild, vec!["-h"], P, Q),
        (Rsbuild, vec!["--version"], P, Q),
        (Rsbuild, vec!["--port", "3000", "--help"], P, Q),
        (Rsbuild, vec!["--port"], P, U),
        (Rspack, vec!["server"], M, CO),
        (Rspack, vec!["s"], M, CO),
        (Rspack, vec!["--config", "x.js", "server"], M, CO),
        (Rspack, vec!["bundle"], M, CO),
        (Rspack, vec!["b"], M, CO),
        (Rspack, vec!["--mode=production", "bundle"], M, CO),
        (Rspack, vec!["b", "--watch"], M, CO),
        (Rspack, vec!["dev"], M, CO),
        (Rspack, vec!["watch"], M, CO),
        (Rspack, vec!["p"], P, Q),
        (Rspack, vec!["p", "dist"], P, Q),
        (Rspack, vec!["preview"], P, Q),
        (Rspack, vec!["s", "--help"], P, Q),
        (Rspack, vec!["bundle", "--version"], M, CO),
        (Rspack, vec!["--version", "build"], M, CO),
        (Rspack, vec!["b", "-v"], M, CO),
        (Rspack, vec!["-v", "build"], M, CO),
        (Rspack, vec!["serve", "--version"], M, CO),
        (Rsbuild, vec!["build", "--version"], M, CO),
        (Rsbuild, vec!["dev", "-v"], M, CO),
        (Rsbuild, vec!["-v", "dev"], M, CO),
        (Rsbuild, vec!["build", "-v"], M, CO),
        (Rspack, vec!["--version"], P, Q),
        (Rspack, vec!["-v"], P, Q),
        (Rsbuild, vec!["-v"], P, Q),
        (Rsbuild, vec!["--port", "3000", "--version"], P, Q),
        (Rspack, vec!["--mode=production", "-v"], P, Q),
        (Rspack, vec!["-V"], P, Q),
        (Rsbuild, vec!["build", "-V"], P, Q),
        (Rspack, vec!["bogus", "-v"], P, Q),
        (Rsbuild, vec!["bogus", "--version"], P, Q),
        (Rspack, vec!["build", "--help"], P, Q),
        (Rsbuild, vec!["-v", "-h"], P, Q),
        (Rsbuild, vec!["build", "--", "--help"], M, CO),
        (Rsbuild, vec!["custom", "--", "-v"], P, U),
        (Cargo, vec!["-v", "build"], M, CO),
        (Cargo, vec!["--version"], P, Q),
        (Cargo, vec!["-V"], P, Q),
        (Cargo, vec!["build", "--version"], P, Q),
        (Cargo, vec!["run", "--", "--version"], M, CO),
        (Rspack, vec!["custom"], P, U),
        (Rsbuild, vec!["server"], P, U),
        (Rsbuild, vec!["s"], P, U),
        (Rsbuild, vec!["bundle"], P, U),
        (Rsbuild, vec!["b"], P, U),
        (Rsbuild, vec!["p"], P, U),
    ];
    for (tool, args, mode, reason) in cases {
        let args = strings(&args);
        let got = classify(tool, &args, true, "");
        assert_eq!((got.mode, got.reason), (mode, reason), "{tool} {args:?}");
    }
    for tool in Tool::ALL {
        for args in [
            vec!["build"],
            vec!["clean"],
            vec!["--version"],
            vec!["-v"],
            vec!["s"],
            vec!["package"],
            vec!["publish"],
            vec![],
        ] {
            let args = strings(&args);
            let got = classify(tool, &args, false, "");
            assert_eq!(
                (got.mode, got.reason),
                (Mode::Passthrough, Reason::OutsideGit)
            );
            let got = classify(tool, &args, true, "1");
            assert_eq!((got.mode, got.reason), (Mode::Passthrough, Reason::Bypass));
        }
    }
    assert_eq!(
        classify(Cargo, &strings(&["build"]), true, "true").mode,
        Mode::Managed
    );
    for tool in Tool::ALL {
        assert_eq!(
            from_argv0(&paths::join2("a b", &format!("{tool}.exe"))),
            Some(tool)
        );
        assert_eq!(from_argv0(&paths::join2("a b", tool.as_str())), Some(tool));
    }
    assert_eq!(from_argv0("ccow"), None);
}

#[test]
fn install_ownership_and_recovery() {
    let (_t, dir) = private();
    let (_s, src_dir) = temp();
    let source = paths::join2(&src_dir, "ccow");
    write(&source, "native v1");
    install(&dir, &source).unwrap();
    install(&dir, &source).unwrap();
    for e in list(&dir).unwrap() {
        assert_eq!(e.state, State::Owned, "{e:?}");
    }
    // Simulate interruption between journal publication and replacing all
    // copies.
    let mut m = read_manifest(&dir).unwrap();
    for tool in Tool::ALL {
        m.hashes
            .insert(tool, vec![digest(b"native v1"), digest(b"native v2")]);
    }
    save_manifest(&dir, &m).unwrap();
    write(&paths::join2(&dir, &filename(Tool::Cargo)), "native v2");
    write(&source, "native v2");
    install(&dir, &source).unwrap();
    write(&paths::join2(&dir, "unrelated"), "retain me");
    let cargo = paths::join2(&dir, &filename(Tool::Cargo));
    write(&cargo, "modified native file");
    assert!(install(&dir, &source).is_err(), "overwrote modified shim");
    assert!(uninstall(&dir).is_err(), "removed modified shim");
    assert_eq!(fs::read_to_string(&cargo).unwrap(), "modified native file");
    write(&cargo, "native v2");
    uninstall(&dir).unwrap();
    uninstall(&dir).unwrap();
    assert_eq!(
        fs::read_to_string(paths::join2(&dir, "unrelated")).unwrap(),
        "retain me"
    );
}

#[test]
fn conflict_and_unknown_manifest() {
    let (_t, dir) = private();
    let (_s, src_dir) = temp();
    let source = paths::join2(&src_dir, "ccow");
    write(&source, "native");
    let conflict = paths::join2(&dir, &filename(Tool::Rspack));
    write(&conflict, "other tool");
    let err = install(&dir, &source).unwrap_err();
    assert!(err.to_string().starts_with("shim conflict at "), "{err}");
    assert!(
        fs::symlink_metadata(paths::join2(&dir, &filename(Tool::Cargo))).is_err(),
        "partial install before conflict check"
    );
    fs::remove_file(&conflict).unwrap();
    fs::write(
        paths::join2(&dir, MANIFEST_NAME),
        r#"{"version":2,"owner":"cachecow-native-shims","hashes":{}}"#,
    )
    .unwrap();
    assert!(install(&dir, &source).is_err(), "rewrote unknown version");
    assert!(uninstall(&dir).is_err(), "removed unknown version");
    for (raw, ok) in [
        ("{}", true),
        (r#"{"hashes":null}"#, false),
        (r#"{"version":1,"owner":"x","hashes":{}}"#, false),
        (r#"{"hashes":{"go":["00"]}}"#, false),
        (r#"{"hashes":{"cargo":[]}}"#, false),
        (r#"{"hashes":{"cargo":["zz"]}}"#, false),
    ] {
        fs::write(paths::join2(&dir, MANIFEST_NAME), raw).unwrap();
        assert_eq!(read_manifest(&dir).is_ok(), ok, "{raw}");
    }
}

/// Windows PATHEXT entries keep their configured case on a case-insensitive
/// filesystem.
fn same_path(a: &str, b: &str) -> bool {
    if cfg!(windows) {
        a.eq_ignore_ascii_case(b)
    } else {
        a == b
    }
}

#[test]
fn resolve_dynamic_aliases() {
    let (_t, dir) = private();
    let (_s, src_dir) = temp();
    let source = paths::join2(&src_dir, "ccow");
    write(&source, "ccow native");
    install(&dir, &source).unwrap();
    let (_ta, alias) = temp();
    let (_tb, a) = temp();
    let (_tc, b) = temp();
    let cargo = filename(Tool::Cargo);
    write(&paths::join2(&a, &cargo), "rustup proxy A");
    write(&paths::join2(&b, &cargo), "rustup proxy B");
    // Even a copied CacheCow binary outside its owned directory is excluded.
    write(&paths::join2(&alias, &cargo), "ccow native");
    let sep = paths::LIST_SEPARATOR.to_string();
    let mut options = ResolveOptions {
        path: [dir.as_str(), &alias, &a, &b].join(&sep),
        shim_dir: dir.clone(),
        ccow_executable: source.clone(),
        ..ResolveOptions::default()
    };
    let r = resolve(Tool::Cargo, &options).unwrap();
    assert!(same_path(&r.path, &paths::join2(&a, &cargo)), "{r:?}");
    assert!(r.candidates.len() >= 3);
    options.path = [b.as_str(), &a].join(&sep);
    let r = resolve(Tool::Cargo, &options).unwrap();
    assert!(same_path(&r.path, &paths::join2(&b, &cargo)), "{r:?}");
    options.path = [dir.as_str(), &alias].join(&sep);
    let failure = resolve(Tool::Cargo, &options).unwrap_err();
    let missing = crate::error::find::<ResolveError>(&failure.error).unwrap();
    assert_eq!(missing.exit_code(), 127);
    assert_eq!(failure.resolution.candidates.len(), 2);
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let aliased = paths::join2(&alias, &cargo);
        fs::remove_file(&aliased).unwrap();
        std::os::unix::fs::symlink(&source, &aliased).unwrap();
        options.path = [alias.as_str(), &a].join(&sep);
        let r = resolve(Tool::Cargo, &options).unwrap();
        assert_eq!(r.path, paths::join2(&a, &cargo), "symlink recursion");
        fs::remove_file(&aliased).unwrap();
        fs::hard_link(&source, &aliased).unwrap();
        let r = resolve(Tool::Cargo, &options).unwrap();
        assert_eq!(r.path, paths::join2(&a, &cargo), "hardlink recursion");
        fs::set_permissions(paths::join2(&a, &cargo), fs::Permissions::from_mode(0o600)).unwrap();
        options.path = a.clone();
        let failure = resolve(Tool::Cargo, &options).unwrap_err();
        let denied = crate::error::find::<ResolveError>(&failure.error).unwrap();
        assert_eq!(denied.exit_code(), 126);
        assert_eq!(
            failure.error.to_string(),
            "cargo exists but is not executable; correct its permissions or PATH"
        );
    }
}

#[test]
fn install_lock_and_symlink_conflict() {
    let (_t, dir) = private();
    let (_s, src_dir) = temp();
    let source = paths::join2(&src_dir, "ccow");
    write(&source, "native");
    let held = lock(&dir).unwrap();
    let err = install(&dir, &source).unwrap_err();
    assert!(
        err.to_string().starts_with("shim installation is locked: "),
        "{err}"
    );
    drop(held);
    #[cfg(unix)]
    {
        std::os::unix::fs::symlink(&source, paths::join2(&dir, &filename(Tool::Cargo))).unwrap();
        assert!(install(&dir, &source).is_err(), "claimed unowned symlink");
    }
}

#[cfg(unix)]
#[test]
fn private_dir_rejects_shared_permissions() {
    use std::os::unix::fs::PermissionsExt;
    let (_t, base) = temp();
    let dir = paths::join2(&base, "shared");
    fs::create_dir(&dir).unwrap();
    fs::set_permissions(&dir, fs::Permissions::from_mode(0o755)).unwrap();
    let err = super::private::private_dir(&dir).unwrap_err();
    assert!(
        err.to_string()
            .contains("must be owned by the current user with mode 0700"),
        "{err}"
    );
}

const ENTRY_ENV: &str = "CACHECOW_SHIM_ENTRY_TEST";
const ENTRY_TEST: &str = "shim::tests::installed_native_entry";

/// Runs through an installed copy of the test binary to check the argv0
/// contract. libtest treats arguments after `--` as exact-match filters, so the
/// real tool arguments do not select other tests.
#[test]
fn installed_native_entry() {
    if let Ok(out) = std::env::var(ENTRY_ENV) {
        let argv: Vec<String> = std::env::args().collect();
        let Some(tool) = from_argv0(&argv[0]) else {
            std::process::exit(90)
        };
        let start = argv
            .iter()
            .position(|a| a == "--")
            .map_or(argv.len(), |i| i + 1);
        let cwd = paths::current_dir().unwrap();
        let report = serde_json::json!({"Tool": tool, "Args": argv[start..], "Cwd": cwd});
        fs::write(out, serde_json::to_vec(&report).unwrap()).unwrap();
        std::process::exit(23);
    }
    let source = paths::path_string(&std::env::current_exe().unwrap()).unwrap();
    let (_t, dir) = private();
    install(&dir, &source).unwrap();
    let (_c, cwd) = temp();
    let (_o, out_dir) = temp();
    let args = strings(&[
        "+stable",
        "build",
        "--target-dir",
        "한 글/cache",
        "--",
        "a'b",
        "$HOME",
        "",
    ]);
    for tool in Tool::ALL {
        let out = paths::join2(&out_dir, tool.as_str());
        let status = std::process::Command::new(paths::join2(&dir, &filename(tool)))
            .args([ENTRY_TEST, "--exact", "--test-threads=1", "--"])
            .args(&args)
            .env(ENTRY_ENV, &out)
            .current_dir(&cwd)
            .stdout(std::process::Stdio::null())
            .status()
            .unwrap();
        assert_eq!(status.code(), Some(23), "{tool}");
        let got: serde_json::Value = serde_json::from_slice(&fs::read(&out).unwrap()).unwrap();
        assert_eq!(got["Tool"], tool.as_str());
        assert_eq!(got["Args"], serde_json::json!(args));
        let real = paths::eval_symlinks(&cwd).unwrap();
        let reported = paths::eval_symlinks(got["Cwd"].as_str().unwrap()).unwrap();
        assert_eq!(reported, real);
    }
}
