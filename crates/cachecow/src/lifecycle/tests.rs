// SPDX-License-Identifier: Apache-2.0
//! The transaction tests use the real private record storage (`state`,
//! `lease::ensure_private_directory`) exactly as production does.

use std::fs;

use super::*;

struct Root {
    _temp: tempfile::TempDir,
    path: String,
}

/// Creates transaction storage as production does. Windows temporary
/// directories inherit grants for other principals and are not private.
fn private_root() -> Root {
    let temp = tempfile::tempdir().unwrap();
    let base = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    let path = paths::join2(&base, "state");
    crate::lease::ensure_private_directory(&path).unwrap();
    Root { _temp: temp, path }
}

fn plain_root() -> (tempfile::TempDir, String) {
    let temp = tempfile::tempdir().unwrap();
    let base = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    (temp, base)
}

fn tree(path: &str, contents: &str) {
    fs::create_dir_all(path).unwrap();
    fs::write(paths::join2(path, "data"), contents).unwrap();
}

fn content(path: &str, want: &str) {
    let got = fs::read_to_string(paths::join2(path, "data")).unwrap();
    assert_eq!(got, want, "{path}");
}

fn pair(source: &str, destination: &str) -> Pair {
    Pair {
        source: source.to_string(),
        destination: destination.to_string(),
    }
}

fn bg() -> Context {
    Context::background()
}

#[test]
fn replacement_and_isolation() {
    let d = private_root();
    let (src, dst) = (
        paths::join2(&d.path, "source"),
        paths::join2(&d.path, "target"),
    );
    tree(&src, "source");
    tree(&dst, "old");
    let p = [pair(&src, &dst)];
    let record = paths::join2(&d.path, "txn.json");
    let r = prepare(&bg(), &record, &p, false, true).unwrap();
    assert!(r.reused);
    content(&dst, "old");
    let r = prepare(&bg(), &record, &p, true, true).unwrap();
    assert!(!r.reused);
    content(&dst, "source");
    tree(&dst, "derived");
    content(&src, "source");
    assert!(fs::symlink_metadata(&record).is_err(), "journal retained");
}

#[cfg(unix)]
#[test]
fn failed_second_tree_preserves_both() {
    let d = private_root();
    let [a, b, c, f] = ["a", "b", "c", "f"].map(|n| paths::join2(&d.path, n));
    tree(&a, "new");
    tree(&b, "old-b");
    tree(&c, "new-c");
    tree(&f, "old-f");
    std::os::unix::fs::symlink(&a, paths::join2(&c, "unsafe")).unwrap();
    let record = paths::join2(&d.path, "txn.json");
    assert!(prepare(&bg(), &record, &[pair(&a, &b), pair(&c, &f)], true, true).is_err());
    content(&b, "old-b");
    content(&f, "old-f");
    for entry in fs::read_dir(&d.path).unwrap() {
        let name = entry.unwrap().file_name().into_string().unwrap();
        assert!(name.len() <= 20, "staging leaked {name}");
    }
}

#[test]
fn recover_partial_publication() {
    let d = private_root();
    let (src, dst) = (paths::join2(&d.path, "src"), paths::join2(&d.path, "dst"));
    tree(&src, "new");
    tree(&dst, "new");
    let id = uuid::Uuid::now_v7();
    let entry = Entry {
        source: src.clone(),
        destination: dst.clone(),
        stage: format!("{dst}.ccow-{id}.stage"),
        backup: format!("{dst}.ccow-{id}.backup"),
        existing: true,
    };
    tree(&entry.backup, "old");
    let record = paths::join2(&d.path, "txn.json");
    state::write(
        &record,
        &Journal {
            schema_version: 1,
            committed: false,
            entries: vec![entry],
        },
    )
    .unwrap();
    recover(&record, &[pair(&src, &dst)]).unwrap();
    content(&dst, "old");
    assert!(fs::symlink_metadata(&record).is_err());
}

#[test]
fn unknown_journal_preserved() {
    let d = private_root();
    let record = paths::join2(&d.path, "txn.json");
    state::write(
        &record,
        &Journal {
            schema_version: 99,
            ..Journal::default()
        },
    )
    .unwrap();
    assert!(recover(&record, &[]).is_err());
    assert!(fs::metadata(&record).is_ok());
}

#[test]
fn foreign_journal_paths_are_refused() {
    let d = private_root();
    let dst = paths::join2(&d.path, "dst");
    let elsewhere = paths::join2(&d.path, "elsewhere");
    tree(&elsewhere, "keep");
    let record = paths::join2(&d.path, "txn.json");
    let id = uuid::Uuid::now_v7();
    state::write(
        &record,
        &Journal {
            schema_version: 1,
            committed: true,
            entries: vec![Entry {
                source: String::new(),
                destination: dst.clone(),
                stage: format!("{dst}.ccow-{id}.stage"),
                backup: elsewhere.clone(),
                existing: true,
            }],
        },
    )
    .unwrap();
    assert!(recover(&record, &[pair(&dst, &dst)]).is_err());
    content(&elsewhere, "keep");
    assert!(fs::metadata(&record).is_ok());
}

#[test]
fn missing_journal_is_clean() {
    let d = private_root();
    recover(&paths::join2(&d.path, "absent.json"), &[]).unwrap();
}

#[test]
fn journal_json_matches_go_layout() {
    let j = Journal {
        schema_version: 1,
        committed: true,
        entries: vec![Entry {
            source: "/s".into(),
            destination: "/d".into(),
            stage: "/d.ccow-x.stage".into(),
            backup: "/d.ccow-x.backup".into(),
            existing: false,
        }],
    };
    assert_eq!(
        serde_json::to_string(&j).unwrap(),
        r#"{"schemaVersion":1,"committed":true,"entries":[{"source":"/s","destination":"/d","stage":"/d.ccow-x.stage","backup":"/d.ccow-x.backup","existing":false}]}"#
    );
    let go: Journal = crate::jsonx::from_slice_strict(
        br#"{"schemaVersion":99,"committed":false,"entries":null}"#,
    )
    .unwrap();
    assert_eq!(go.schema_version, 99);
    assert!(go.entries.is_empty());
}

#[test]
fn exists_rejects_files_and_symlinks() {
    let (_temp, base) = plain_root();
    assert!(!exists(&paths::join2(&base, "absent")).unwrap());
    let dir = paths::join2(&base, "dir");
    fs::create_dir(&dir).unwrap();
    assert!(exists(&dir).unwrap());
    let file = paths::join2(&base, "file");
    fs::write(&file, b"x").unwrap();
    assert!(
        exists(&file)
            .unwrap_err()
            .to_string()
            .contains("is not a directory")
    );
    #[cfg(unix)]
    {
        let link = paths::join2(&base, "link");
        std::os::unix::fs::symlink(&dir, &link).unwrap();
        assert!(exists(&link).is_err());
    }
}

#[cfg(unix)]
fn set_mode(path: &str, mode: u32) {
    use std::os::unix::fs::PermissionsExt;
    fs::set_permissions(path, fs::Permissions::from_mode(mode)).unwrap();
}

#[cfg(unix)]
fn mode(path: &str) -> u32 {
    use std::os::unix::fs::PermissionsExt;
    fs::symlink_metadata(path).unwrap().permissions().mode() & 0o777
}

#[cfg(unix)]
#[test]
fn remove_tree_restores_read_only_directories() {
    let (_temp, base) = plain_root();
    let cache = paths::join2(&base, "cache");
    let sub = paths::join2(&cache, "ro/sub");
    fs::create_dir_all(&sub).unwrap();
    let file = paths::join2(&sub, "f");
    fs::write(&file, b"x").unwrap();
    set_mode(&file, 0o444);
    set_mode(&sub, 0o555);
    set_mode(&paths::join2(&cache, "ro"), 0o555);
    remove_tree(&cache).unwrap();
    assert!(fs::symlink_metadata(&cache).is_err());
}

#[cfg(unix)]
#[test]
fn remove_tree_never_follows_symlinks() {
    let (_temp, base) = plain_root();
    let outside = paths::join2(&base, "outside");
    fs::create_dir(&outside).unwrap();
    fs::write(paths::join2(&outside, "keep"), b"keep").unwrap();
    set_mode(&outside, 0o555);
    // A symlink root removes only the link.
    let root_link = paths::join2(&base, "root-link");
    std::os::unix::fs::symlink(&outside, &root_link).unwrap();
    remove_tree(&root_link).unwrap();
    assert!(fs::symlink_metadata(&root_link).is_err());
    // A nested symlink is removed without touching its target.
    let cache = paths::join2(&base, "cache");
    fs::create_dir(&cache).unwrap();
    std::os::unix::fs::symlink(&outside, paths::join2(&cache, "link")).unwrap();
    set_mode(&cache, 0o555);
    remove_tree(&cache).unwrap();
    assert!(fs::symlink_metadata(&cache).is_err());
    assert_eq!(mode(&outside), 0o555, "followed a symlink");
    assert_eq!(fs::read(paths::join2(&outside, "keep")).unwrap(), b"keep");
    set_mode(&outside, 0o700);
}

#[test]
fn remove_tree_writable_and_missing() {
    let (_temp, base) = plain_root();
    let cache = paths::join2(&base, "cache");
    tree(&paths::join2(&cache, "nested"), "x");
    remove_tree(&cache).unwrap();
    assert!(fs::symlink_metadata(&cache).is_err());
    remove_tree(&cache).unwrap();
}

#[cfg(windows)]
#[test]
fn remove_tree_read_only_windows_files() {
    let (_temp, base) = plain_root();
    let cache = paths::join2(&base, "cache");
    tree(&cache, "x");
    let file = paths::join2(&cache, "data");
    let mut p = fs::metadata(&file).unwrap().permissions();
    p.set_readonly(true);
    fs::set_permissions(&file, p).unwrap();
    remove_tree(&cache).unwrap();
    assert!(fs::symlink_metadata(&cache).is_err());
}

#[cfg(windows)]
#[test]
fn prepare_read_only_windows_file() {
    let root = private_root();
    let (src, dst) = (
        paths::join2(&root.path, "source"),
        paths::join2(&root.path, "destination"),
    );
    fs::create_dir(&src).unwrap();
    let original = paths::join2(&src, "readonly");
    fs::write(&original, b"cache data").unwrap();
    let mut p = fs::metadata(&original).unwrap().permissions();
    p.set_readonly(true);
    fs::set_permissions(&original, p).unwrap();
    prepare(
        &bg(),
        &paths::join2(&root.path, "transaction.json"),
        &[pair(&src, &dst)],
        false,
        true,
    )
    .unwrap();
    for path in [original.clone(), paths::join2(&dst, "readonly")] {
        assert!(
            fs::metadata(&path).unwrap().permissions().readonly(),
            "read-only attribute lost: {path}"
        );
        assert_eq!(fs::read(&path).unwrap(), b"cache data");
    }
    for path in [original, paths::join2(&dst, "readonly")] {
        let _ = clone::set_owner_access(&path, false);
    }
}

#[cfg(unix)]
#[test]
fn prepare_dangling_internal_link_and_remove_without_following() {
    let root = private_root();
    let (src, dst) = (
        paths::join2(&root.path, "source"),
        paths::join2(&root.path, "destination"),
    );
    tree(&src, "source");
    std::os::unix::fs::symlink("absent-inside", paths::join2(&src, "dangling")).unwrap();
    // Removal must not reach files outside the tree.
    let sentinel = paths::join2(&root.path, "sentinel");
    fs::write(&sentinel, b"keep").unwrap();
    prepare(
        &bg(),
        &paths::join2(&root.path, "transaction.json"),
        &[pair(&src, &dst)],
        false,
        true,
    )
    .unwrap();
    let link = paths::join2(&dst, "dangling");
    assert!(
        fs::symlink_metadata(&link)
            .unwrap()
            .file_type()
            .is_symlink()
    );
    assert_eq!(
        fs::read_link(&link).unwrap().to_str().unwrap(),
        "absent-inside"
    );
    content(&dst, "source");
    remove_tree(&dst).unwrap();
    assert!(fs::symlink_metadata(&dst).is_err());
    assert_eq!(fs::read(&sentinel).unwrap(), b"keep");
    content(&src, "source");
}
