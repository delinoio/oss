// SPDX-License-Identifier: Apache-2.0
//! Ported from `cmds/cachecow/internal/repository/repository_test.go`.

#![cfg(unix)]

use std::{fs, os::unix::fs::symlink};

use super::*;

fn ctx() -> Context {
    Context::background()
}

fn s(p: &std::path::Path) -> String {
    p.to_str().unwrap().to_string()
}

pub(crate) fn run_git(dir: &str, args: &[&str]) {
    let out = std::process::Command::new("git")
        .arg("-C")
        .arg(dir)
        .args(args)
        .env("GIT_CONFIG_NOSYSTEM", "1")
        .env("GIT_CONFIG_GLOBAL", "/dev/null")
        .output()
        .unwrap();
    assert!(
        out.status.success(),
        "git {args:?}: {}",
        String::from_utf8_lossy(&out.stderr)
    );
}

/// Returns a temporary directory and a committed repository inside it.
fn fixture() -> (tempfile::TempDir, String) {
    let t = tempfile::tempdir().unwrap();
    let p = t.path().join("primary 한 글");
    fs::create_dir(&p).unwrap();
    let p = s(&p);
    run_git(&p, &["init"]);
    run_git(
        &p,
        &[
            "-c",
            "user.name=Test",
            "-c",
            "user.email=test@example.invalid",
            "commit",
            "--allow-empty",
            "-m",
            "init",
        ],
    );
    (t, p)
}

/// Runs `f` with a replacement Git environment on this thread only.
fn with_git_env<T>(extra: &[(&str, &str)], f: impl FnOnce() -> T) -> T {
    let mut env: Vec<(String, String)> = std::env::vars().collect();
    env.extend(
        extra
            .iter()
            .map(|(k, v)| ((*k).to_string(), (*v).to_string())),
    );
    ENV_OVERRIDE.with(|v| *v.borrow_mut() = Some(env));
    let out = f();
    ENV_OVERRIDE.with(|v| *v.borrow_mut() = None);
    out
}

#[test]
fn discovery_and_source_precedence() {
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let w = s(&t2.path().join("linked space 한"));
    run_git(&p, &["worktree", "add", "-b", "linked", &w]);
    let r = discover(&ctx(), &w).unwrap();
    let primary = discover(&ctx(), &p).unwrap();
    assert_eq!(r.common_dir, primary.common_dir);
    assert_eq!(r.primary, primary.root);
    assert_ne!(r.id, primary.id);
    let sub = paths::join2(&w, "sub");
    fs::create_dir(&sub).unwrap();
    assert_eq!(discover(&ctx(), &sub).unwrap().id, r.id);
    for (explicit, saved, origin, is_self) in [
        ("", "", SourceOrigin::Primary, false),
        ("", w.as_str(), SourceOrigin::Saved, true),
        (p.as_str(), w.as_str(), SourceOrigin::Invocation, false),
    ] {
        let src = select_source(&ctx(), &r, explicit, saved).unwrap();
        assert_eq!((src.origin, src.is_self), (origin, is_self));
    }
    let (_o, other) = fixture();
    assert!(select_source(&ctx(), &r, &other, &p).is_err());
    assert!(select_source(&ctx(), &r, &paths::join2(&w, "missing"), &p).is_err());
    let actual = with_git_env(&[("GIT_DIR", &primary.git_dir)], || discover(&ctx(), &w)).unwrap();
    assert_eq!(actual.id, r.id, "ambient Git override");
}

#[test]
fn missing_or_non_directory_source_reports_the_cause() {
    let (_t, p) = fixture();
    let current = discover(&ctx(), &p).unwrap();
    let message = |source: &str| match select_source(&ctx(), &current, source, "") {
        Ok(_) => panic!("accepted {source}"),
        Err(e) => format!("{e:#}"),
    };
    // Absolute and relative paths that do not exist, including one below a
    // root-owned directory, must not be reported as an ownership problem.
    let absolute = if cfg!(windows) {
        "C:\\ccow-missing-source-fixture\\nope".to_string()
    } else {
        "/ccow-missing-source-fixture/nope".to_string()
    };
    for missing in [absolute.as_str(), "../deleted-worktree"] {
        let text = message(missing);
        assert!(text.contains("does not exist"), "{missing}: {text}");
        assert!(text.contains("--source"), "{missing}: {text}");
        assert!(!text.contains("not owned"), "{missing}: {text}");
    }
    // A saved source that disappeared gets the same message and recovery hint.
    let gone = paths::join2(&p, "gone worktree 한");
    let text = match select_source(&ctx(), &current, "", &gone) {
        Ok(_) => panic!("accepted a deleted saved source"),
        Err(e) => format!("{e:#}"),
    };
    assert!(text.contains("does not exist") && text.contains("config unset source"));
    // A regular file is not a worktree.
    let file = paths::join2(&p, "plain-file");
    fs::write(&file, "x").unwrap();
    let text = message(&file);
    assert!(text.contains("is not a directory"), "{text}");
}

#[test]
fn bare_and_protected_paths() {
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let bare = s(&t2.path().join("bare.git"));
    run_git(&p, &["clone", "--bare", &p, &bare]);
    let w = s(&t2.path().join("worktree"));
    run_git(&bare, &["worktree", "add", &w]);
    let r = discover(&ctx(), &w).unwrap();
    assert!(r.primary.is_empty(), "invented primary");
    assert!(select_source(&ctx(), &r, "", "").is_err());
    select_source(&ctx(), &r, &w, "").unwrap();
    let b = discover(&ctx(), &bare).unwrap();
    assert_eq!(b.kind, Kind::Bare);
    let tracked = paths::join2(&w, "source.txt");
    fs::write(&tracked, "source").unwrap();
    run_git(&w, &["add", "source.txt"]);
    let settings = paths::join2(&w, ".ccow");
    fs::create_dir(&settings).unwrap();
    let protected = vec![settings.clone()];
    for bad in [
        w.clone(),
        paths::dir(&w),
        r.common_dir.clone(),
        r.git_dir.clone(),
        paths::join2(&w, ".git"),
        tracked.clone(),
        settings.clone(),
    ] {
        assert!(
            r.validate_cache(&ctx(), &bad, &protected).is_err(),
            "accepted protected {bad}"
        );
    }
    r.validate_cache(&ctx(), &paths::join2(&w, "target"), &protected)
        .unwrap();
    let alias = paths::join2(&w, "alias");
    symlink(&settings, &alias).unwrap();
    assert!(
        r.validate_cache(&ctx(), &paths::join2(&alias, "nested"), &protected)
            .is_err(),
        "accepted symlink escape"
    );
}

#[test]
fn canonical_and_ownership() {
    let t = tempfile::tempdir().unwrap();
    let p = s(t.path());
    let link = paths::join2(&p, "link");
    symlink(&p, &link).unwrap();
    let a = canonical(&paths::join(&[link.as_str(), "missing", "한"])).unwrap();
    let b = canonical(&paths::join(&[p.as_str(), "missing", "한"])).unwrap();
    assert_eq!(a, b, "aliases differ");
    let dangling = paths::join2(&p, "dangling");
    symlink(paths::join2(&p, "absent"), &dangling).unwrap();
    assert!(canonical(&dangling).is_err(), "accepted dangling link");
    check_owner(&paths::join2(&p, "absent")).unwrap();
    // SAFETY: geteuid has no preconditions.
    if unsafe { libc::geteuid() } != 0 {
        assert!(check_owner("/").is_err(), "accepted other user's root");
    }
}

#[test]
fn protect_other_worktree_and_untracked_source() {
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let w = s(&t2.path().join("other"));
    run_git(&p, &["worktree", "add", "-b", "other", &w]);
    fs::write(paths::join2(&p, "untracked.txt"), "keep").unwrap();
    let r = discover(&ctx(), &w).unwrap();
    for path in [
        paths::join2(&p, "untracked.txt"),
        paths::join2(&p, ".git"),
        paths::join2(&p, ".ccow"),
    ] {
        assert!(
            r.validate_cache(&ctx(), &path, &[]).is_err(),
            "accepted unrelated source {path}"
        );
    }
    // A build cache must be explicitly ignored so unrelated untracked source
    // is never silently promoted into deletable cache storage.
    fs::write(paths::join2(&w, ".gitignore"), "/target/\n").unwrap();
    fs::create_dir(paths::join2(&w, "target")).unwrap();
    fs::write(paths::join(&[w.as_str(), "target", "artifact"]), "cache").unwrap();
    r.validate_cache(&ctx(), &paths::join2(&w, "target"), &[])
        .unwrap();
}

#[test]
fn filesystem_only_verification() {
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let w = s(&t2.path().join("linked"));
    run_git(&p, &["worktree", "add", "-b", "linked", &w]);
    let r = discover(&ctx(), &w).unwrap();
    verify(&r).unwrap();
    let cache = canonical(&paths::join2(&w, "target")).unwrap();
    verify_cache_claim(&r, &cache, &[]).unwrap();
    assert!(
        verify_cache_claim(&r, &r.root, &[]).is_err(),
        "accepted checkout as cache"
    );
    let mut forged = r.clone();
    forged.common_dir.clone_from(&r.git_dir);
    assert!(
        verify(&forged).is_err(),
        "accepted changed common directory"
    );
    let mut forged = r.clone();
    forged.id = "forged".into();
    assert!(verify(&forged).is_err(), "accepted forged identity");
    fs::write(
        paths::join2(&r.git_dir, "gitdir"),
        format!("{}\n", paths::join2(&p, ".git")),
    )
    .unwrap();
    assert!(verify(&r).is_err(), "accepted stale backlink");
}

#[test]
fn snapshot_verification_without_git() {
    for change in ["unchanged", "index", "ignore", "new-source", "cache-write"] {
        let (_t, p) = fixture();
        fs::write(paths::join2(&p, ".gitignore"), "/target/\n").unwrap();
        let r = discover(&ctx(), &p).unwrap();
        let cache = paths::join2(&r.root, "target");
        let snap = capture_snapshot(&ctx(), &r, std::slice::from_ref(&cache), &[]).unwrap();
        match change {
            "index" => fs::write(paths::join2(&r.git_dir, "index"), "changed").unwrap(),
            "ignore" => fs::write(paths::join2(&p, ".gitignore"), "/other/\n").unwrap(),
            "new-source" => fs::write(paths::join2(&p, "new.go"), "source").unwrap(),
            "cache-write" => {
                fs::create_dir(&cache).unwrap();
                fs::write(paths::join2(&cache, "artifact"), "cache").unwrap();
            }
            _ => {}
        }
        let e = snap.verify();
        if change == "unchanged" || change == "cache-write" {
            e.unwrap();
        } else {
            assert!(e.is_err(), "{change}: accepted changed discovery evidence");
        }
    }
}

#[test]
fn snapshot_independent_ignored_churn() {
    for change in ["churn", "ignore", "index", "symlink", "tracked"] {
        let (_t, p) = fixture();
        let t2 = tempfile::tempdir().unwrap();
        let w = s(&t2.path().join("independent"));
        run_git(&p, &["worktree", "add", "-b", "independent", &w]);
        fs::write(paths::join2(&w, ".gitignore"), "target/\n").unwrap();
        let cache = paths::join2(&w, "target");
        fs::create_dir(&cache).unwrap();
        fs::write(paths::join2(&cache, "artifact"), "old").unwrap();
        if change == "tracked" {
            run_git(&w, &["add", "-f", "target/artifact"]);
        }
        let r = discover(&ctx(), &p).unwrap();
        let snap = capture_snapshot(&ctx(), &r, &[paths::join2(&r.root, "target")], &[]).unwrap();
        match change {
            "churn" | "tracked" => {
                fs::remove_file(paths::join2(&cache, "artifact")).unwrap();
                fs::write(paths::join2(&cache, "new-artifact"), "new").unwrap();
            }
            "ignore" => fs::write(paths::join2(&w, ".gitignore"), "other/\n").unwrap(),
            "index" => {
                fs::write(paths::join2(&snap.worktrees[1].git_dir, "index"), "changed").unwrap();
            }
            "symlink" => {
                fs::remove_dir_all(&cache).unwrap();
                let other = tempfile::tempdir().unwrap();
                symlink(other.path(), &cache).unwrap();
                std::mem::forget(other);
            }
            _ => {}
        }
        let e = snap.verify();
        if change == "churn" {
            e.unwrap();
        } else {
            assert!(e.is_err(), "{change}: accepted changed safety evidence");
        }
    }
}

#[test]
fn unrelated_dangling_source_links() {
    for tracked in [false, true] {
        let (_t, p) = fixture();
        let t2 = tempfile::tempdir().unwrap();
        let other = s(&t2.path().join("other"));
        run_git(&p, &["worktree", "add", "--detach", &other]);
        fs::write(paths::join2(&p, ".gitignore"), "target/\n").unwrap();
        let link = paths::join2(&other, "dangling");
        symlink("missing", &link).unwrap();
        if tracked {
            run_git(&other, &["add", "dangling"]);
        }
        let r = discover(&ctx(), &p).unwrap();
        let cache = paths::join2(&p, "target");
        r.validate_cache(&ctx(), &cache, &[]).unwrap();
        fs::remove_file(&link).unwrap();
        symlink(paths::join2(&cache, "missing"), &link).unwrap();
        assert!(
            r.validate_cache(&ctx(), &cache, &[]).is_err(),
            "accepted source alias into cache"
        );
        fs::remove_file(&link).unwrap();
        symlink("missing", &cache).unwrap();
        assert!(
            r.validate_cache(&ctx(), &cache, &[]).is_err(),
            "accepted dangling cache alias"
        );
    }
}

#[test]
fn canonical_rejects_parent_traversal_before_cleaning() {
    let root = tempfile::tempdir().unwrap();
    let outside = tempfile::tempdir().unwrap();
    let child = outside.path().join("child");
    fs::create_dir(&child).unwrap();
    let link = s(&root.path().join("link"));
    symlink(&child, &link).unwrap();
    for input in [format!("{link}/../cache"), "link/../cache".to_string()] {
        assert!(canonical(&input).is_err(), "accepted ambiguous {input}");
    }
    canonical(&format!("{link}/cache")).unwrap();
}

#[test]
fn file_stamps_detect_changes_and_absence() {
    let t = tempfile::tempdir().unwrap();
    let file = s(&t.path().join("config.toml"));
    let absent = s(&t.path().join("absent"));
    let stamps = capture_file_stamps(&[file.clone(), absent.clone()]).unwrap();
    assert!(!stamps[0].exists && !stamps[1].exists);
    verify_file_stamps(&stamps).unwrap();
    fs::write(&file, "a").unwrap();
    assert!(verify_file_stamps(&stamps).is_err(), "accepted creation");
    let stamps = capture_file_stamps(std::slice::from_ref(&file)).unwrap();
    assert_eq!(stamps[0].digest, crate::jsonx::sha256_hex(b"a"));
    fs::write(&file, "b").unwrap();
    assert!(verify_file_stamps(&stamps).is_err(), "accepted change");
    let link = s(&t.path().join("link"));
    symlink(&file, &link).unwrap();
    let stamps = capture_file_stamps(std::slice::from_ref(&link)).unwrap();
    let mut expected = file.clone().into_bytes();
    expected.extend_from_slice(b"b");
    assert_eq!(stamps[0].digest, crate::jsonx::sha256_hex(&expected));
}

/// Registers a linked worktree in `t2`, optionally locks it, then deletes its
/// directory the way agent tools do, leaving the Git record behind.
fn stale_worktree(p: &str, t2: &tempfile::TempDir, lock: bool) -> String {
    let w = s(&t2.path().join("stale"));
    run_git(p, &["worktree", "add", "-b", "stale", &w]);
    if lock {
        run_git(p, &["worktree", "lock", "--reason", "usb", &w]);
    }
    fs::remove_dir_all(&w).unwrap();
    canonical(&w).unwrap()
}

#[test]
fn missing_worktree_is_skipped() {
    for lock in [false, true] {
        let (_t, p) = fixture();
        fs::write(paths::join2(&p, ".gitignore"), "/target/\n").unwrap();
        let t2 = tempfile::tempdir().unwrap();
        let _stale = stale_worktree(&p, &t2, lock);
        let r = discover(&ctx(), &p).unwrap();
        let cache = paths::join2(&r.root, "target");
        r.validate_cache(&ctx(), &cache, &[])
            .unwrap_or_else(|e| panic!("lock={lock}: validate_cache: {e:#}"));
        let snap = capture_snapshot(&ctx(), &r, std::slice::from_ref(&cache), &[])
            .unwrap_or_else(|e| panic!("lock={lock}: capture_snapshot: {e:#}"));
        assert_eq!(snap.worktrees.len(), 1, "lock={lock}");
        snap.verify().unwrap();
        // Pruning the record changes the worktree registry, which must
        // invalidate the snapshot.
        if !lock {
            run_git(&p, &["worktree", "prune"]);
            assert!(snap.verify().is_err(), "accepted pruned registry");
        }
    }
}

#[test]
fn restored_missing_worktree_invalidates_snapshot() {
    for lock in [false, true] {
        let (_t, p) = fixture();
        fs::write(paths::join2(&p, ".gitignore"), "/target/\n").unwrap();
        let t2 = tempfile::tempdir().unwrap();
        let stale = stale_worktree(&p, &t2, lock);
        let r = discover(&ctx(), &p).unwrap();
        let cache = paths::join2(&r.root, "target");
        let snap = capture_snapshot(&ctx(), &r, &[cache], &[]).unwrap();
        assert_eq!(
            snap.missing_worktrees.as_slice(),
            std::slice::from_ref(&stale),
            "lock={lock}"
        );
        snap.verify().unwrap();
        // Restoring the directory (or remounting its volume) leaves the
        // worktree registry unchanged, so only the skipped path can reveal it.
        fs::create_dir(&stale).unwrap();
        let e = snap.verify().unwrap_err();
        assert!(
            format!("{e:#}").contains("available again"),
            "lock={lock}: {e:#}"
        );
        fs::remove_dir(&stale).unwrap();
        snap.verify().unwrap();
    }
}

#[test]
fn repaired_missing_worktree_invalidates_snapshot() {
    let (_t, p) = fixture();
    fs::write(paths::join2(&p, ".gitignore"), "/target/\n").unwrap();
    let t2 = tempfile::tempdir().unwrap();
    let old = s(&t2.path().join("old"));
    run_git(&p, &["worktree", "add", "-b", "moved", &old]);
    // Move the worktree without telling Git: the registration keeps pointing
    // at the absent old path.
    let new = s(&t2.path().join("new"));
    fs::rename(&old, &new).unwrap();
    let new = canonical(&new).unwrap();
    let r = discover(&ctx(), &p).unwrap();
    let cache = paths::join2(&r.root, "target");
    let snap = capture_snapshot(&ctx(), &r, &[cache], &[]).unwrap();
    assert_eq!(snap.worktrees.len(), 1);
    assert_eq!(snap.missing_worktrees.len(), 1);
    snap.verify().unwrap();
    // `git worktree repair` rewrites the registration's `gitdir` backlink to
    // the new path without renaming the registration, and the old path stays
    // absent, so only the stamped backlink can reveal the new source tree.
    run_git(&p, &["worktree", "repair", &new]);
    let e = snap.verify().unwrap_err();
    assert!(
        format!("{e:#}").contains("repository metadata changed"),
        "{e:#}"
    );
}

#[test]
fn missing_worktree_is_skipped_from_linked_worktree() {
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let healthy = s(&t2.path().join("healthy"));
    run_git(&p, &["worktree", "add", "-b", "healthy", &healthy]);
    let _stale = stale_worktree(&p, &t2, false);
    fs::write(paths::join2(&healthy, ".gitignore"), "/target/\n").unwrap();
    let r = discover(&ctx(), &healthy).unwrap();
    let cache = paths::join2(&r.root, "target");
    r.validate_cache(&ctx(), &cache, &[]).unwrap();
    capture_snapshot(&ctx(), &r, &[cache], &[])
        .unwrap()
        .verify()
        .unwrap();
}

#[test]
fn missing_worktree_path_stays_reserved() {
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let stale = stale_worktree(&p, &t2, true);
    let r = discover(&ctx(), &p).unwrap();
    let e = r
        .validate_cache(&ctx(), &paths::join2(&stale, "target"), &[])
        .unwrap_err();
    assert!(
        format!("{e:#}").contains("git worktree prune"),
        "missing prune hint: {e:#}"
    );
    // The registered path itself is the cache: it is a cache that contains
    // the worktree.
    assert!(r.validate_cache(&ctx(), &stale, &[]).is_err());
    let parent = paths::dir(&stale);
    let e = r.validate_cache(&ctx(), &parent, &[]).unwrap_err();
    assert!(
        format!("{e:#}").contains("cache cannot contain a worktree"),
        "{e:#}"
    );
}

#[test]
fn unreadable_worktree_directory_still_fails() {
    use std::os::unix::fs::PermissionsExt;
    let (_t, p) = fixture();
    let t2 = tempfile::tempdir().unwrap();
    let w = s(&t2.path().join("locked-down"));
    run_git(&p, &["worktree", "add", "-b", "locked-down", &w]);
    let r = discover(&ctx(), &p).unwrap();
    fs::set_permissions(&w, fs::Permissions::from_mode(0o000)).unwrap();
    // Root bypasses directory permissions, so the negative control is moot.
    let bypassed = fs::read_dir(&w).is_ok();
    let res = r.validate_cache(&ctx(), &paths::join2(&p, "target"), &[]);
    fs::set_permissions(&w, fs::Permissions::from_mode(0o755)).unwrap();
    if !bypassed {
        assert!(res.is_err(), "accepted unreadable worktree directory");
    }
}

#[test]
fn source_location_parent_reuse_preserves_alias_checks() {
    let t = tempfile::tempdir().unwrap();
    let root = s(t.path());
    let real = paths::join2(&root, "real");
    fs::create_dir(&real).unwrap();
    for name in ["one", "two"] {
        fs::write(paths::join2(&real, name), "source").unwrap();
    }
    let alias = paths::join2(&root, "alias");
    symlink(&real, &alias).unwrap();
    let mut resolver = SourceLocations::default();
    for name in ["one", "two"] {
        let path = paths::join2(&alias, name);
        assert_eq!(resolver.resolve(&path).unwrap(), canonical(&path).unwrap());
    }
    assert_eq!(resolver.parents.len(), 1);
    // Replacing a leaf must still be observed even with a reused parent.
    let leaf = paths::join2(&alias, "two");
    fs::remove_file(&leaf).unwrap();
    let missing = paths::join2(&root, "cache/missing");
    symlink(&missing, &leaf).unwrap();
    assert_eq!(
        resolver.resolve(&leaf).unwrap(),
        canonical(&missing).unwrap()
    );
    // A new validation pass must resolve a retargeted parent from scratch.
    fs::remove_file(&alias).unwrap();
    let other = paths::join2(&root, "other");
    fs::create_dir(&other).unwrap();
    fs::write(paths::join2(&other, "one"), "other").unwrap();
    symlink(&other, &alias).unwrap();
    let path = paths::join2(&alias, "one");
    assert_eq!(
        SourceLocations::default().resolve(&path).unwrap(),
        canonical(&path).unwrap()
    );
}
