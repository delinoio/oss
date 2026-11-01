// SPDX-License-Identifier: Apache-2.0
//! Admission-rule fixtures. Executable tests in `tests/cli.rs` exercise
//! kernel-authenticated IPC and actual supervisor ownership.

use std::{sync::mpsc, time::Duration};

use super::*;
use crate::lease::Registry;

fn accept(_: &Plan) -> Result<()> {
    Ok(())
}

/// Rejects any source whose final component is `srcA`, like an unusable
/// source provenance record.
fn reject_src_a(plan: &Plan) -> Result<()> {
    if plan
        .resources
        .sources
        .iter()
        .any(|s| paths::base(s) == "srcA")
    {
        return Err(anyhow!(
            "source provenance is unavailable; run the exact requested command through ccow in \
             the source first"
        ));
    }
    Ok(())
}

struct Fixture {
    _temp: tempfile::TempDir,
    root: String,
    registry: Arc<Registry>,
}

impl Fixture {
    fn new() -> Self {
        let temp = tempfile::tempdir().unwrap();
        let base = paths::eval_symlinks(temp.path().to_str().unwrap()).unwrap();
        // Transaction records require private storage. Windows temporary
        // directories inherit grants for other principals.
        let root = paths::join2(&base, "state");
        lease::ensure_private_directory(&root).unwrap();
        let registry =
            Registry::open(&Context::background(), &paths::join2(&root, "leases")).unwrap();
        Self {
            _temp: temp,
            root,
            registry,
        }
    }

    fn path(&self, name: &str) -> String {
        paths::join2(&self.root, name)
    }

    fn server(&self, records: RecordHooks) -> Server {
        let session = self.registry.new_session("fixture-only").unwrap();
        Server::new(
            Scope::default(),
            Some(self.registry.clone()),
            session,
            accept,
            records,
            self.root.clone(),
        )
    }

    fn plan(
        &self,
        action: Action,
        sources: &[&str],
        destinations: &[&str],
        pairs: &[(&str, &str)],
        record: &str,
    ) -> Plan {
        Plan {
            schema_version: 1,
            root: self.root.clone(),
            action,
            resources: Resources {
                sources: sources.iter().map(|s| (*s).to_string()).collect(),
                destinations: destinations.iter().map(|s| (*s).to_string()).collect(),
            },
            pairs: pairs
                .iter()
                .map(|(s, d)| Pair {
                    source: (*s).to_string(),
                    destination: (*d).to_string(),
                })
                .collect(),
            record: self.path(record),
            allow_copy: true,
            ..Plan::default()
        }
    }

    /// Paths of the session's stored lease record.
    fn stored(&self, server: &Server) -> Vec<String> {
        let id = server.session.id();
        let mut paths = self
            .registry
            .records(&Context::background())
            .unwrap()
            .into_iter()
            .find(|r| r.id == id)
            .map(|r| r.paths)
            .unwrap_or_default();
        paths.sort();
        paths
    }
}

fn mkdir_with_file(dir: &str, contents: &str) {
    std::fs::create_dir_all(dir).unwrap();
    std::fs::write(paths::join2(dir, "data"), contents).unwrap();
}

#[test]
fn nested_cannot_copy_actively_writable_source() {
    let f = Fixture::new();
    let server = f.server(RecordHooks::default());
    let (source, dest) = (f.path("source"), f.path("dest"));
    std::fs::create_dir(&source).unwrap();
    let ctx = Context::background();
    let initial = f.plan(
        Action::Run,
        &[],
        &[&source],
        &[(&source, &source)],
        "one.json",
    );
    server.apply(&ctx, initial.clone(), false).unwrap();
    let borrowed = server.apply(&ctx, initial, true).unwrap();
    assert!(borrowed.reused);
    let nested = f.plan(
        Action::Run,
        &[&source],
        &[&dest],
        &[(&source, &dest)],
        "two.json",
    );
    let e = server.apply(&ctx, nested.clone(), true).unwrap_err();
    assert!(error_text(&e).contains("already writable"), "{e:#}");
    assert!(!std::path::Path::new(&dest).exists(), "copied live source");
    // #4579 scenario 1: the failed request released its new destination.
    assert_eq!(f.stored(&server), vec![source.clone()]);
    // Repeating the request runs admission again instead of borrowing.
    let e = server.apply(&ctx, nested, true).unwrap_err();
    assert!(error_text(&e).contains("already writable"), "{e:#}");
    assert_eq!(f.stored(&server), vec![source]);
}

#[test]
fn failed_source_validation_releases_and_retry_with_other_source_succeeds() {
    let f = Fixture::new();
    let server = f.server(RecordHooks {
        validate_source: Some(reject_src_a),
        ..RecordHooks::default()
    });
    let ctx = Context::background();
    let (outer, src_a, src_b, dest) = (
        f.path("outer"),
        f.path("srcA"),
        f.path("srcB"),
        f.path("dest"),
    );
    std::fs::create_dir(&outer).unwrap();
    mkdir_with_file(&src_a, "a");
    mkdir_with_file(&src_b, "b");
    server
        .apply(
            &ctx,
            f.plan(
                Action::Run,
                &[],
                &[&outer],
                &[(&outer, &outer)],
                "outer.json",
            ),
            false,
        )
        .unwrap();
    let failing = f.plan(
        Action::Run,
        &[&src_a],
        &[&dest],
        &[(&src_a, &dest)],
        "dest.json",
    );
    let e = server.apply(&ctx, failing, true).unwrap_err();
    assert!(error::find::<SourceError>(&e).is_some(), "{e:#}");
    assert_eq!(f.stored(&server), vec![outer.clone()]);

    // Scenario 3: the released source is available to another session.
    let other = f.registry.new_session("other").unwrap();
    other
        .acquire(
            &ctx,
            &Resources {
                sources: Vec::new(),
                destinations: vec![src_a.clone()],
            },
            false,
        )
        .unwrap();

    let retry = f.plan(
        Action::Run,
        &[&src_b],
        &[&dest],
        &[(&src_b, &dest)],
        "dest.json",
    );
    let result = server.apply(&ctx, retry, true).unwrap();
    assert!(!result.reused);
    assert_eq!(
        std::fs::read_to_string(paths::join2(&dest, "data")).unwrap(),
        "b"
    );
    let mut want = vec![dest, outer];
    want.sort();
    assert_eq!(f.stored(&server), want);
}

#[test]
fn failed_preparation_releases_and_identical_retry_prepares() {
    let f = Fixture::new();
    let server = f.server(RecordHooks::default());
    let ctx = Context::background();
    let (outer, src, dest) = (f.path("outer"), f.path("src"), f.path("dest"));
    std::fs::create_dir(&outer).unwrap();
    server
        .apply(
            &ctx,
            f.plan(
                Action::Run,
                &[],
                &[&outer],
                &[(&outer, &outer)],
                "outer.json",
            ),
            false,
        )
        .unwrap();
    let request = f.plan(
        Action::Run,
        &[&src],
        &[&dest],
        &[(&src, &dest)],
        "dest.json",
    );
    let e = server.apply(&ctx, request.clone(), true).unwrap_err();
    assert!(error_text(&e).contains("source cache unavailable"), "{e:#}");
    assert_eq!(f.stored(&server), vec![outer.clone()]);
    mkdir_with_file(&src, "source");
    let result = server.apply(&ctx, request, true).unwrap();
    assert!(!result.reused);
    assert_eq!(
        std::fs::read_to_string(paths::join2(&dest, "data")).unwrap(),
        "source"
    );
}

#[test]
fn finish_session_retries_registry_contention() {
    let f = Fixture::new();
    let ctx = Context::background();
    let owner = f.registry.new_session("completed-scope").unwrap();
    owner
        .acquire(
            &ctx,
            &Resources {
                sources: Vec::new(),
                destinations: vec![f.path("cache")],
            },
            false,
        )
        .unwrap();
    let db = rusqlite::Connection::open(paths::join2(&f.path("leases"), "leases.sqlite")).unwrap();
    db.busy_timeout(Duration::from_secs(5)).unwrap();
    db.execute_batch("BEGIN IMMEDIATE").unwrap();
    let (verified_tx, verified) = mpsc::channel::<()>();
    let verify = move |_: &Context, _: &Record| -> Result<()> {
        let _ = verified_tx.send(());
        Ok(())
    };
    std::thread::scope(|s| {
        let done = s.spawn(|| finish_session(&owner, &verify));
        // Two positive verifications prove Finish retried while SQL was held.
        for _ in 0..2 {
            verified
                .recv_timeout(Duration::from_secs(5))
                .expect("no retry");
            assert!(!done.is_finished(), "finished during contention");
        }
        db.execute_batch("COMMIT").unwrap();
        done.join().unwrap().unwrap();
    });
    let records = f.registry.records(&ctx).unwrap();
    assert!(records.is_empty(), "leases retained: {records:?}");
}

#[test]
fn preparation_evidence_uses_leased_result() {
    for existing in [false, true] {
        let f = Fixture::new();
        let server = f.server(RecordHooks::default());
        let (src, dst) = (f.path("source"), f.path("dest"));
        mkdir_with_file(&src, "source");
        let mut plan = f.plan(
            Action::Prepare,
            &[&src],
            &[&dst],
            &[(&src, &dst)],
            "transaction.json",
        );
        plan.prepared_all = true;
        // The plan was built while dst was absent. Another owner creates it
        // before this request acquires protection; it must not acquire
        // new provenance.
        if existing {
            mkdir_with_file(&dst, "retained");
        }
        let record = plan.record.clone();
        server.apply(&Context::background(), plan, false).unwrap();
        let got = server.state.lock().unwrap().pending[&record].prepared_all;
        assert_ne!(got, existing, "PreparedAll={got} for existing={existing}");
        let data = std::fs::read_to_string(paths::join2(&dst, "data")).unwrap();
        if existing {
            assert_eq!(data, "retained", "existing cache was overwritten");
        }
    }
}

#[test]
fn helpers() {
    assert_eq!(
        with_env(&["A=1".to_string(), "CCOW_SESSION=x".to_string()], ENV, "y"),
        vec!["A=1".to_string(), "CCOW_SESSION=y".to_string()]
    );
    assert!(constant_time_eq(b"abc", b"abc"));
    assert!(!constant_time_eq(b"abc", b"abd"));
    assert!(!constant_time_eq(b"abc", b"ab"));
    let e = anyhow!("inner").context("outer");
    assert_eq!(error_text(&e), "outer: inner");
    let e = anyhow::Error::new(AuthError).context(format!("{AuthError}: dial failed"));
    assert_eq!(error_text(&e), format!("{AuthError}: dial failed"));
    let response = Response::failed(&anyhow::Error::new(SourceError {
        message: "gone".into(),
    }));
    assert!(response.source_failure);
    assert!(error::find::<SourceError>(&response.failure()).is_some());
}
