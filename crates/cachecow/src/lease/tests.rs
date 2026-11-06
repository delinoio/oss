// SPDX-License-Identifier: Apache-2.0
use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    thread,
    time::{Duration, Instant},
};

use anyhow::anyhow;
use rusqlite::Connection;

use super::*;
use crate::context::CancelError;

struct Fixture {
    _temp: tempfile::TempDir,
    root: String,
    registry: Arc<Registry>,
}

impl Fixture {
    fn new() -> Self {
        let temp = tempfile::tempdir().unwrap();
        let root = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
        let registry = Registry::open(&Context::background(), &self_dir(&root)).unwrap();
        Self {
            _temp: temp,
            root,
            registry,
        }
    }

    fn path(&self, name: &str) -> String {
        paths::join2(&self.root, name)
    }

    fn dir(&self) -> String {
        self_dir(&self.root)
    }

    fn reopen(&self) -> Arc<Registry> {
        Registry::open(&Context::background(), &self.dir()).unwrap()
    }

    fn db(&self) -> Connection {
        Connection::open(paths::join2(&self.dir(), "leases.sqlite")).unwrap()
    }

    /// Holds the registry write lock from another connection, like a
    /// concurrent ccow transaction. Released after `release`, or never.
    fn hold(&self, release: Option<Duration>) -> Hold {
        let c = self.db();
        c.execute_batch("BEGIN IMMEDIATE").unwrap();
        let shared = Arc::new(Mutex::new(Some(c)));
        if let Some(delay) = release {
            let shared = shared.clone();
            thread::spawn(move || {
                thread::sleep(delay);
                if let Some(c) = shared.lock().unwrap().take() {
                    c.execute_batch("ROLLBACK").unwrap();
                }
            });
        }
        Hold(shared)
    }
}

fn self_dir(root: &str) -> String {
    paths::join2(root, "registry")
}

struct Hold(Arc<Mutex<Option<Connection>>>);

impl Drop for Hold {
    fn drop(&mut self) {
        if let Some(c) = self.0.lock().unwrap().take() {
            let _ignored = c.execute_batch("ROLLBACK");
        }
    }
}

fn bg() -> Context {
    Context::background()
}

fn session(r: &Arc<Registry>) -> Session {
    r.new_session("native-supervisor-test-boundary").unwrap()
}

fn dests(paths: &[&str]) -> Resources {
    Resources {
        sources: Vec::new(),
        destinations: paths.iter().map(|p| (*p).to_string()).collect(),
    }
}

fn acquire(s: &Session, paths: &[&str]) -> Grant {
    s.acquire(&bg(), &dests(paths), false).unwrap()
}

fn verified(_: &Context, _: &Record) -> anyhow::Result<()> {
    Ok(())
}

fn finish(s: &Session) {
    s.finish(&bg(), &verified).unwrap();
}

fn lease_err(result: anyhow::Result<impl std::fmt::Debug>) -> LeaseError {
    let e = result.expect_err("expected a lease error");
    *crate::error::find::<LeaseError>(&e).unwrap_or_else(|| panic!("not a lease error: {e:#}"))
}

fn cancel_err(result: anyhow::Result<impl std::fmt::Debug>) -> CancelError {
    let e = result.expect_err("expected cancellation");
    *crate::error::find::<CancelError>(&e).unwrap_or_else(|| panic!("not a cancel error: {e:#}"))
}

#[test]
fn complete_sets_and_independent_worktrees() {
    let f = Fixture::new();
    let (a, b) = (session(&f.registry), session(&f.registry));
    let source = f.path("primary/target");
    let dest = f.path("worktree-a/target");
    a.acquire(
        &bg(),
        &Resources {
            sources: vec![source.clone()],
            destinations: vec![dest.clone()],
        },
        false,
    )
    .unwrap();
    acquire(&b, &[&f.path("worktree-b/target")]);
    for p in [
        source.clone(),
        dest.clone(),
        paths::join2(&dest, "debug"),
        paths::dir(&dest),
    ] {
        assert_eq!(
            lease_err(session(&f.registry).acquire(&bg(), &dests(&[&p]), false)),
            LeaseError::Busy,
            "{p}"
        );
    }
    finish(&a);
    acquire(&session(&f.registry), &[&source, &dest]);
}

#[test]
fn wait_and_cancellation() {
    let f = Fixture::new();
    let path = f.path("cache");
    let s = session(&f.registry);
    acquire(&s, &[&path]);
    let (c, _cancel) = bg().with_timeout(Duration::from_millis(60));
    let sources = Resources {
        sources: vec![path.clone()],
        destinations: Vec::new(),
    };
    assert_eq!(
        cancel_err(session(&f.registry).acquire(&c, &sources, true)),
        CancelError::DeadlineExceeded
    );
    let registry = f.registry.clone();
    let p = path.clone();
    let waiter = thread::spawn(move || session(&registry).acquire(&bg(), &dests(&[&p]), true));
    thread::sleep(Duration::from_millis(40));
    assert!(!waiter.is_finished(), "early acquisition");
    finish(&s);
    let deadline = Instant::now() + Duration::from_secs(3);
    while !waiter.is_finished() {
        assert!(Instant::now() < deadline, "waiter did not resume");
        thread::sleep(Duration::from_millis(10));
    }
    waiter.join().unwrap().unwrap();
}

#[test]
fn nested_borrow_and_cycle() {
    let f = Fixture::new();
    let (a, b) = (session(&f.registry), session(&f.registry));
    let (x, y) = (f.path("x"), f.path("y"));
    assert!(!acquire(&a, &[&x]).borrowed, "first acquisition borrowed");
    acquire(&b, &[&y]);
    assert!(
        acquire(&a, &[&x, &paths::join2(&x, "child")]).borrowed,
        "nested must skip preparation"
    );
    for (s, p) in [(&a, &y), (&b, &x)] {
        assert_eq!(
            lease_err(s.acquire(&bg(), &dests(&[p]), true)),
            LeaseError::NestedConflict
        );
    }
    assert_eq!(
        lease_err(session(&f.registry).acquire(&bg(), &dests(&[&x]), false)),
        LeaseError::Busy
    );
    let z = f.path("z");
    assert!(!acquire(&a, &[&z]).borrowed, "new cache borrowed");
    finish(&a);
    acquire(&session(&f.registry), &[&x, &z]);
}

#[cfg(unix)]
#[test]
fn aliases_and_absent_cache() {
    let f = Fixture::new();
    let actual = f.path("actual");
    std::fs::create_dir(&actual).unwrap();
    let alias = f.path("alias");
    std::os::unix::fs::symlink(&actual, &alias).unwrap();
    acquire(
        &session(&f.registry),
        &[&paths::join2(&actual, "absent/cache")],
    );
    let aliased = paths::join2(&alias, "absent");
    assert_eq!(
        lease_err(session(&f.registry).acquire(&bg(), &dests(&[&aliased]), false)),
        LeaseError::Busy
    );
    assert!(
        std::fs::symlink_metadata(paths::join2(&actual, "absent")).is_err(),
        "acquisition created cache"
    );
}

#[test]
fn durable_owner_loss_and_reconciliation() {
    let f = Fixture::new();
    let s = session(&f.registry);
    let path = f.path("cache");
    acquire(&s, &[&path]);
    let refuse =
        |_: &Context, _: &Record| -> anyhow::Result<()> { Err(anyhow!("surviving descendants")) };
    assert_eq!(lease_err(s.finish(&bg(), &refuse)), LeaseError::Unverified);
    drop(s);
    let reopened = f.reopen();
    assert_eq!(
        lease_err(session(&reopened).acquire(&bg(), &dests(&[&path]), false)),
        LeaseError::Busy
    );
    let all = reopened.records(&bg()).unwrap();
    assert_eq!(all.len(), 1);
    let e = reopened.reconcile(&bg(), &all[0], &refuse).unwrap_err();
    assert!(crate::error::is(&e, &LeaseError::Unverified));
    assert!(e.to_string().contains("surviving descendants"), "{e}");
    reopened.reconcile(&bg(), &all[0], &verified).unwrap();
    acquire(&session(&reopened), &[&path]);
}

#[test]
fn malformed_and_unknown_records() {
    for raw in [
        r"{",
        r#"{"version":2}"#,
        r#"{"version":1,"id":"not-uuid"}"#,
        r#"{"version":1,"unexpected":true}"#,
    ] {
        let f = Fixture::new();
        f.db()
            .execute("INSERT INTO leases VALUES('bad',?1)", [raw])
            .unwrap();
        assert!(
            session(&f.registry)
                .acquire(&bg(), &dests(&[&f.path("cache")]), false)
                .is_err(),
            "{raw}"
        );
        let got: String = f
            .db()
            .query_row("SELECT record FROM leases", [], |r| r.get(0))
            .unwrap();
        assert_eq!(got, raw, "invalid record rewritten");
    }
    let f = Fixture::new();
    f.db().execute_batch("PRAGMA user_version=42").unwrap();
    assert!(
        Registry::open(&bg(), &f.dir()).is_err(),
        "unknown database version accepted"
    );
}

#[test]
fn outside_cache_and_private_registry() {
    let f = Fixture::new();
    assert!(
        session(&f.registry)
            .acquire(&bg(), &dests(&[&f.root]), false)
            .is_err(),
        "registry inside cache"
    );
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        let dir = f.path("public");
        std::fs::DirBuilder::new().mode(0o755).create(&dir).unwrap();
        std::fs::set_permissions(&dir, std::os::unix::fs::PermissionsExt::from_mode(0o755))
            .unwrap();
        assert!(
            Registry::open(&bg(), &dir).is_err(),
            "public registry accepted"
        );
    }
}

#[test]
fn concurrent_registries() {
    let f = Fixture::new();
    let other = f.reopen();
    let cache = f.path("cache");
    let handles: Vec<_> = (0..12)
        .map(|i| {
            let reg = if i % 2 == 0 {
                other.clone()
            } else {
                f.registry.clone()
            };
            let cache = cache.clone();
            thread::spawn(move || -> anyhow::Result<()> {
                let s = session(&reg);
                let (c, _cancel) = bg().with_timeout(Duration::from_secs(10));
                s.acquire(&c, &dests(&[&cache]), true)?;
                loop {
                    match s.finish(&c, &verified) {
                        Err(e) if crate::error::is(&e, &LeaseError::RegistryBusy) => {}
                        other => return other,
                    }
                }
            })
        })
        .collect();
    for h in handles {
        h.join().unwrap().unwrap();
    }
}

const HELPER_ENV: &str = "CCOW_LEASE_TEST_HELPER";

/// Subprocess owner for `subprocess_owner_loss`; inert without its environment.
#[test]
fn subprocess_owner_helper() {
    if std::env::var(HELPER_ENV).as_deref() != Ok("1") {
        return;
    }
    let r = Registry::open(&bg(), &std::env::var("CCOW_LEASE_TEST_REGISTRY").unwrap())
        .unwrap_or_else(|_| std::process::exit(2));
    let s = r
        .new_session("subprocess-intent-before-spawn")
        .unwrap_or_else(|_| std::process::exit(3));
    let cache = std::env::var("CCOW_LEASE_TEST_CACHE").unwrap();
    if s.acquire(&bg(), &dests(&[&cache]), false).is_err() {
        std::process::exit(4);
    }
    use std::io::{Read, Write};
    let mut out = std::io::stdout();
    out.write_all(b"ready\n").unwrap();
    out.flush().unwrap();
    let mut input = [0u8; 1];
    let _ignored = std::io::stdin().read(&mut input);
    std::process::exit(5);
}

#[test]
fn subprocess_owner_loss() {
    use std::{
        io::{BufRead, BufReader},
        process::{Command, Stdio},
    };
    let f = Fixture::new();
    let path = f.path("cache");
    let mut child = Command::new(std::env::current_exe().unwrap())
        .args([
            "lease::tests::subprocess_owner_helper",
            "--exact",
            "--nocapture",
            "--test-threads=1",
        ])
        .env(HELPER_ENV, "1")
        .env("CCOW_LEASE_TEST_REGISTRY", f.dir())
        .env("CCOW_LEASE_TEST_CACHE", &path)
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let mut reader = BufReader::new(child.stdout.take().unwrap());
    let mut ready = false;
    let mut line = String::new();
    while reader.read_line(&mut line).unwrap() > 0 {
        if line.ends_with("ready\n") {
            ready = true;
            break;
        }
        line.clear();
    }
    assert!(ready, "helper readiness: {line:?}");
    assert_eq!(
        lease_err(session(&f.registry).acquire(&bg(), &dests(&[&path]), false)),
        LeaseError::Busy
    );
    acquire(
        &session(&f.registry),
        &[&f.path("independent-worktree/cache")],
    );
    child.kill().unwrap();
    assert!(!child.wait().unwrap().success(), "expected killed owner");
    assert_eq!(
        lease_err(session(&f.registry).acquire(&bg(), &dests(&[&path]), false)),
        LeaseError::Busy,
        "dead owner lost protection"
    );
}

#[test]
fn reconcile_rejects_changed_record() {
    let f = Fixture::new();
    let s = session(&f.registry);
    acquire(&s, &[&f.path("x")]);
    let all = f.registry.records(&bg()).unwrap();
    acquire(&s, &[&f.path("y")]);
    assert_eq!(
        lease_err(f.registry.reconcile(&bg(), &all[0], &verified)),
        LeaseError::Unverified
    );
}

/// #4583 scenario 1: a sustained hold is contention, never cache use.
#[test]
fn registry_transaction_contention() {
    let f = Fixture::new();
    let other = f.reopen();
    let _hold = f.hold(None);
    let cache = f.path("cache");
    let start = Instant::now();
    let e = session(&other)
        .acquire(&bg(), &dests(&[&cache]), false)
        .unwrap_err();
    let elapsed = start.elapsed();
    assert!(crate::error::is(&e, &LeaseError::RegistryBusy), "{e:#}");
    assert!(!crate::error::is(&e, &LeaseError::Busy));
    assert!(
        elapsed >= Duration::from_secs(1) && elapsed < Duration::from_millis(1500),
        "{elapsed:?}"
    );
    let (c, _cancel) = bg().with_timeout(Duration::from_millis(60));
    assert_eq!(
        cancel_err(session(&other).acquire(&c, &dests(&[&cache]), true)),
        CancelError::DeadlineExceeded
    );
}

/// #4583 scenario 2: brief contention is retried transparently.
#[test]
fn brief_contention_succeeds() {
    let brief = Some(Duration::from_millis(100));
    // Open on a second handle.
    let f = Fixture::new();
    let hold = f.hold(brief);
    Registry::open(&bg(), &f.dir()).unwrap();
    drop(hold);
    // Records.
    let other = f.reopen();
    let hold = f.hold(brief);
    other.records(&bg()).unwrap();
    drop(hold);
    // Nonblocking acquisition of an idle path.
    let hold = f.hold(brief);
    session(&other)
        .acquire(&bg(), &dests(&[&f.path("idle")]), false)
        .unwrap();
    drop(hold);
    // Nested acquisition records the new path exactly once.
    let nested = session(&other);
    acquire(&nested, &[&f.path("a")]);
    let hold = f.hold(brief);
    let grant = nested
        .acquire(&bg(), &dests(&[&f.path("b")]), false)
        .unwrap();
    drop(hold);
    assert_eq!(grant.new_paths, vec![f.path("b")]);
    let record = other
        .records(&bg())
        .unwrap()
        .into_iter()
        .find(|r| r.id == nested.id())
        .unwrap();
    assert_eq!(
        record.paths.iter().filter(|p| **p == f.path("b")).count(),
        1
    );
    // Prepared-source release.
    let copier = session(&other);
    let grant = copier
        .acquire(
            &bg(),
            &Resources {
                sources: vec![f.path("src")],
                destinations: vec![f.path("dst")],
            },
            false,
        )
        .unwrap();
    let hold = f.hold(brief);
    copier
        .release_prepared_sources(&[f.path("src")], &grant)
        .unwrap();
    drop(hold);
    // Reconcile.
    let record = other
        .records(&bg())
        .unwrap()
        .into_iter()
        .find(|r| r.id == copier.id())
        .unwrap();
    let hold = f.hold(brief);
    other.reconcile(&bg(), &record, &verified).unwrap();
    drop(hold);
}

/// #4583 scenario 3: a real overlap is reported at once, contended or not.
#[test]
fn real_overlap_is_unchanged() {
    let f = Fixture::new();
    let a = session(&f.registry);
    let x = f.path("x");
    acquire(&a, &[&x]);
    for brief in [None, Some(Duration::from_millis(100))] {
        let hold = brief.map(|d| f.hold(Some(d)));
        let start = Instant::now();
        assert_eq!(
            lease_err(session(&f.registry).acquire(&bg(), &dests(&[&x]), false)),
            LeaseError::Busy
        );
        // Under a hold the overlap is visible once the lock releases.
        let bound = if brief.is_some() {
            Duration::from_millis(400)
        } else {
            Duration::from_millis(200)
        };
        assert!(start.elapsed() < bound, "{:?}", start.elapsed());
        drop(hold);
    }
    let nested = session(&f.registry);
    acquire(&nested, &[&f.path("a")]);
    assert_eq!(
        lease_err(nested.acquire(&bg(), &dests(&[&x]), false)),
        LeaseError::NestedConflict
    );
}

/// #4583 scenario 4: contention waits honor the context.
#[test]
fn contention_honors_context() {
    let f = Fixture::new();
    let other = f.reopen();
    let _hold = f.hold(None);
    let (c, _cancel) = bg().with_timeout(Duration::from_millis(50));
    let start = Instant::now();
    assert_eq!(cancel_err(other.records(&c)), CancelError::DeadlineExceeded);
    assert!(start.elapsed() < Duration::from_millis(500));
}

#[test]
fn session_id_does_not_authenticate() {
    let f = Fixture::new();
    let a = session(&f.registry);
    let path = f.path("cache");
    acquire(&a, &[&path]);
    let b = f
        .registry
        .new_session("native-supervisor-test-boundary")
        .unwrap();
    assert_eq!(
        lease_err(b.acquire(&bg(), &dests(&[&path]), false)),
        LeaseError::Busy
    );
}

#[cfg(unix)]
#[test]
fn private_database_and_dangling_alias() {
    use std::os::unix::fs::PermissionsExt;
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().to_str().unwrap().to_string();
    let dir = paths::join2(&root, "registry");
    std::fs::create_dir(&dir).unwrap();
    std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
    let db = paths::join2(&dir, "leases.sqlite");
    std::fs::write(&db, b"").unwrap();
    std::fs::set_permissions(&db, std::fs::Permissions::from_mode(0o644)).unwrap();
    assert!(
        Registry::open(&bg(), &dir).is_err(),
        "public database accepted"
    );
    let alias = paths::join2(&root, "alias");
    std::os::unix::fs::symlink(paths::join2(&root, "missing"), &alias).unwrap();
    assert!(
        normalize(&dests(&[&paths::join2(&alias, "cache")])).is_err(),
        "dangling alias accepted"
    );
}

#[cfg(unix)]
mod private_records {
    use std::os::unix::fs::PermissionsExt;

    use super::*;

    fn record_dir() -> (tempfile::TempDir, String) {
        let temp = tempfile::tempdir().unwrap();
        let dir = paths::join2(temp.path().to_str().unwrap(), "state");
        std::fs::create_dir(&dir).unwrap();
        std::fs::set_permissions(&dir, std::fs::Permissions::from_mode(0o700)).unwrap();
        (temp, dir)
    }

    fn record(path: &str, mode: u32) {
        std::fs::write(path, b"{}").unwrap();
        std::fs::set_permissions(path, std::fs::Permissions::from_mode(mode)).unwrap();
    }

    #[test]
    fn missing_record_is_not_found_and_not_created() {
        let (_temp, dir) = record_dir();
        let path = paths::join2(&dir, "cache-missing.json");
        let err = validate_private_file(&path).unwrap_err();
        assert!(crate::error::is_not_found(&err), "{err:#}");
        let err = read_private_file(&path).unwrap_err();
        assert!(crate::error::is_not_found(&err), "{err:#}");
        assert!(std::fs::symlink_metadata(&path).is_err(), "record created");
    }

    #[test]
    fn record_removed_after_a_successful_check_is_not_created() {
        let (_temp, dir) = record_dir();
        let path = paths::join2(&dir, "cache-gone.json");
        record(&path, 0o600);
        std::fs::symlink_metadata(&path).unwrap();
        std::fs::remove_file(&path).unwrap();
        let err = validate_private_file(&path).unwrap_err();
        assert!(crate::error::is_not_found(&err), "{err:#}");
        assert!(std::fs::symlink_metadata(&path).is_err(), "record created");
    }

    #[test]
    fn private_record_is_read_through_the_validated_descriptor() {
        let (_temp, dir) = record_dir();
        let path = paths::join2(&dir, "cache-ok.json");
        record(&path, 0o600);
        validate_private_file(&path).unwrap();
        assert_eq!(read_private_file(&path).unwrap(), b"{}");
    }

    #[test]
    fn unsafe_records_are_rejected_with_the_privacy_error() {
        let (_temp, dir) = record_dir();
        let reject = |path: &str| {
            for err in [
                validate_private_file(path).unwrap_err(),
                read_private_file(path).unwrap_err(),
            ] {
                assert!(
                    err.to_string().contains("not a private owned"),
                    "{path}: {err:#}"
                );
            }
        };
        let public = paths::join2(&dir, "cache-public.json");
        record(&public, 0o640);
        reject(&public);
        let linked = paths::join2(&dir, "cache-linked.json");
        record(&linked, 0o600);
        std::fs::hard_link(&linked, paths::join2(&dir, "alias.json")).unwrap();
        reject(&linked);
        let target = paths::join2(&dir, "target.json");
        record(&target, 0o600);
        let symlink = paths::join2(&dir, "cache-symlink.json");
        std::os::unix::fs::symlink(&target, &symlink).unwrap();
        reject(&symlink);
        let directory = paths::join2(&dir, "cache-directory.json");
        std::fs::create_dir(&directory).unwrap();
        std::fs::set_permissions(&directory, std::fs::Permissions::from_mode(0o700)).unwrap();
        reject(&directory);
    }

    #[test]
    fn fifo_record_is_rejected_without_blocking() {
        let (_temp, dir) = record_dir();
        let path = paths::join2(&dir, "cache-fifo.json");
        let c_path = std::ffi::CString::new(path.clone()).unwrap();
        // SAFETY: c_path is a valid NUL-terminated path.
        assert_eq!(unsafe { libc::mkfifo(c_path.as_ptr(), 0o600) }, 0);
        let err = validate_private_file(&path).unwrap_err();
        assert!(err.to_string().contains("not a private owned"), "{err:#}");
    }
}

#[test]
fn failed_complete_set_does_not_acquire_partial_resources() {
    let f = Fixture::new();
    let (a, b) = (session(&f.registry), session(&f.registry));
    let (x, y) = (f.path("x"), f.path("y"));
    acquire(&a, &[&x]);
    let mixed = Resources {
        sources: vec![x],
        destinations: vec![y.clone()],
    };
    assert_eq!(lease_err(b.acquire(&bg(), &mixed, false)), LeaseError::Busy);
    acquire(&session(&f.registry), &[&y]);
}

#[test]
fn nested_mixed_resources() {
    let f = Fixture::new();
    let s = session(&f.registry);
    let (x, y) = (f.path("x"), f.path("y"));
    acquire(&s, &[&x]);
    let g = acquire(&s, &[&x, &y]);
    assert!(
        !g.borrowed
            && g.new_paths.len() == 1
            && contains(&g.new_paths[0], &y)
            && contains(&y, &g.new_paths[0]),
        "{g:?}"
    );
    assert!(
        s.acquire(&bg(), &dests(&[&f.root]), true).is_err(),
        "nested ancestor extension accepted"
    );
}

#[test]
fn registry_path_characters() {
    let mut name = String::from("registry space 한글");
    if cfg!(unix) {
        name.push_str("?query#fragment");
    }
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().to_str().unwrap().to_string();
    let r = Registry::open(&bg(), &paths::join2(&root, &name)).unwrap();
    acquire(&session(&r), &[&paths::join2(&root, "cache")]);
    assert!(
        std::fs::metadata(paths::join(&[
            root.as_str(),
            name.as_str(),
            "leases.sqlite"
        ]))
        .is_ok()
    );
}

#[test]
fn parent_traversal_rejected() {
    assert!(
        normalize(&dests(&["alias/../cache"])).is_err(),
        "ambiguous traversal accepted"
    );
}

#[test]
fn open_wait_registry_contention() {
    let f = Fixture::new();
    let hold = f.hold(None);
    let (c, _cancel) = bg().with_timeout(Duration::from_millis(80));
    let result = Registry::open_wait(&c, &f.dir());
    drop(hold);
    assert_eq!(
        cancel_err(result.map(|_| ())),
        CancelError::DeadlineExceeded
    );
    // A hold released before the budget is waited through.
    let _hold = f.hold(Some(Duration::from_millis(100)));
    Registry::open_wait(&bg(), &f.dir()).unwrap();
}

#[test]
fn prepared_source_release_retains_destination() {
    let f = Fixture::new();
    let s = session(&f.registry);
    let (source, dest) = (f.path("source"), f.path("dest"));
    let g = s
        .acquire(
            &bg(),
            &Resources {
                sources: vec![source.clone()],
                destinations: vec![dest.clone()],
            },
            false,
        )
        .unwrap();
    s.release_prepared_sources(std::slice::from_ref(&source), &g)
        .unwrap();
    let other = session(&f.registry);
    acquire(&other, &[&source]);
    assert_eq!(
        lease_err(session(&f.registry).acquire(&bg(), &dests(&[&dest]), false)),
        LeaseError::Busy
    );
    assert_eq!(
        lease_err(s.release_prepared_sources(&[dest], &Grant::default())),
        LeaseError::Unverified
    );
    finish(&other);
    finish(&s);
}

/// #4579: an unstarted nested grant is undone exactly.
#[test]
fn release_unstarted_grant() {
    let f = Fixture::new();
    let s = session(&f.registry);
    let (outer, src, dest) = (f.path("outer"), f.path("src"), f.path("dest"));
    acquire(&s, &[&outer]);
    let grant = s
        .acquire(
            &bg(),
            &Resources {
                sources: vec![src.clone()],
                destinations: vec![dest.clone()],
            },
            false,
        )
        .unwrap();
    assert_eq!(grant.new_paths.len(), 2);
    s.release_unstarted_grant(&grant).unwrap();
    let record = f
        .registry
        .records(&bg())
        .unwrap()
        .into_iter()
        .find(|r| r.id == s.id())
        .unwrap();
    assert_eq!(record.paths, vec![outer.clone()]);
    acquire(&session(&f.registry), &[&src]);
    // Empty grants are a no-op without a write.
    let _hold = f.hold(None);
    s.release_unstarted_grant(&Grant {
        borrowed: true,
        new_paths: Vec::new(),
    })
    .unwrap();
    drop(_hold);
    // Refuses to empty the record.
    let only = session(&f.registry);
    let g = acquire(&only, &[&f.path("only")]);
    assert_eq!(
        lease_err(only.release_unstarted_grant(&g)),
        LeaseError::Unverified
    );
    finish(&only);
    assert_eq!(
        lease_err(only.release_unstarted_grant(&g)),
        LeaseError::Unverified
    );
}

struct Observer {
    recoverable: bool,
    verified: AtomicUsize,
    blocked: Mutex<Vec<(String, String)>>,
}

impl Observer {
    fn new(recoverable: bool) -> Arc<Self> {
        Arc::new(Self {
            recoverable,
            verified: AtomicUsize::new(0),
            blocked: Mutex::new(Vec::new()),
        })
    }
}

impl WaitObserver for Observer {
    fn verify(&self, _: &Context, _: &Record) -> anyhow::Result<()> {
        self.verified.fetch_add(1, Ordering::SeqCst);
        if self.recoverable {
            Ok(())
        } else {
            Err(anyhow!("same boot; descendants may survive"))
        }
    }

    fn blocked(&self, cache_path: &str, record: &Record) {
        self.blocked
            .lock()
            .unwrap()
            .push((cache_path.to_string(), record.ownership.clone()));
    }
}

fn stale_record(f: &Fixture, path: &str) -> Record {
    let stale = f
        .registry
        .new_session("/state/processes/stale-journal")
        .unwrap();
    acquire(&stale, &[path]);
    f.registry
        .records(&bg())
        .unwrap()
        .into_iter()
        .find(|r| r.id == stale.id())
        .unwrap()
}

/// #4582: a recoverable conflicting lease is released while waiting.
#[test]
fn waiting_recovers_completed_owner() {
    let f = Fixture::new();
    let cache = f.path("cache");
    let stale = stale_record(&f, &cache);
    let s = session(&f.registry);
    let observer = Observer::new(true);
    s.set_observer(observer.clone());
    let (c, _cancel) = bg().with_timeout(Duration::from_secs(2));
    s.acquire(&c, &dests(&[&cache]), true).unwrap();
    assert!(
        f.registry
            .records(&bg())
            .unwrap()
            .iter()
            .all(|r| r.id != stale.id),
        "stale record retained"
    );
    assert!(observer.blocked.lock().unwrap().is_empty());
}

/// #4582: an unrecoverable lease keeps protection and notifies exactly once.
#[test]
fn waiting_on_unrecoverable_owner_notifies_once() {
    let f = Fixture::new();
    let cache = f.path("cache");
    let stale = stale_record(&f, &cache);
    let s = session(&f.registry);
    let observer = Observer::new(false);
    s.set_observer(observer.clone());
    let (c, _cancel) = bg().with_timeout(Duration::from_millis(300));
    assert_eq!(
        cancel_err(s.acquire(&c, &dests(&[&cache]), true)),
        CancelError::DeadlineExceeded
    );
    let blocked = observer.blocked.lock().unwrap().clone();
    assert_eq!(blocked, vec![(cache.clone(), stale.ownership.clone())]);
    assert_eq!(
        observer.verified.load(Ordering::SeqCst),
        1,
        "recovery attempted once per acquisition"
    );
    assert!(
        f.registry
            .records(&bg())
            .unwrap()
            .iter()
            .any(|r| r.id == stale.id)
    );
    // Nonblocking clean also attempts recovery once before refusing.
    let clean = session(&f.registry);
    let observer = Observer::new(false);
    clean.set_observer(observer.clone());
    assert_eq!(
        lease_err(clean.acquire(&bg(), &dests(&[&cache]), false)),
        LeaseError::Busy
    );
    assert!(
        observer.blocked.lock().unwrap().is_empty(),
        "clean refuses without a waiting notice"
    );
    // A recoverable record lets clean proceed.
    let clean = session(&f.registry);
    clean.set_observer(Observer::new(true));
    clean.acquire(&bg(), &dests(&[&cache]), false).unwrap();
}

#[test]
fn finish_accepts_record_released_by_verified_recovery() {
    let f = Fixture::new();
    let cache = f.path("cache");
    let owner = session(&f.registry);
    acquire(&owner, &[&cache]);
    // A waiting command verified the completed journal and released the
    // record before the owner's own Finish ran.
    let record = f
        .registry
        .records(&bg())
        .unwrap()
        .into_iter()
        .find(|r| r.id == owner.id())
        .unwrap();
    f.registry.reconcile(&bg(), &record, &verified).unwrap();
    owner.finish(&bg(), &verified).unwrap();
    // Verification failure still retains protection semantics.
    let other = session(&f.registry);
    acquire(&other, &[&cache]);
    let failing = |_: &Context, _: &Record| -> anyhow::Result<()> { Err(anyhow!("alive")) };
    assert_eq!(
        lease_err(other.finish(&bg(), &failing)),
        LeaseError::Unverified
    );
}

/// A state directory that grants another principal access must be refused with
/// the path, the failed check and a remedy. The error must not claim an
/// unrelated process-ownership failure.
#[cfg(windows)]
#[test]
fn windows_shared_state_directory_error_is_actionable() {
    let temp = tempfile::tempdir().unwrap();
    let root = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
    let dir = self_dir(&root);
    let user = crate::winsec::current_user()
        .unwrap()
        .to_string_sid()
        .unwrap();
    // Current user full control plus read access for Everyone (`WD`).
    crate::winsec::create_directory(&dir, &format!("D:P(A;OICI;FA;;;{user})(A;OICI;FR;;;WD)"))
        .unwrap();
    let Err(error) = Registry::open(&Context::background(), &dir) else {
        panic!("accepted a state directory that another principal can read");
    };
    let text = crate::session::error_text(&error);
    assert!(
        text.contains(&dir) && text.contains("not private to the current user"),
        "{text}"
    );
    assert!(text.contains("restrict it to your account"), "{text}");
    assert!(!text.contains("process-tree ownership"), "{text}");
}
