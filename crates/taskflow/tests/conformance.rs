use std::{
    collections::{BTreeMap, BTreeSet},
    path::{Path, PathBuf},
    sync::{Arc, OnceLock},
    time::Duration,
};

use serde_json::{json, Value};
use taskflow::{
    cache, config,
    discover::Workspace,
    environment::Redactor,
    files,
    graph::Graph,
    plan::{Cause, Plan},
    runner::{self, Outcome, RunOptions},
    shard,
};
use tokio_util::sync::CancellationToken;

fn helper() -> PathBuf {
    static HELPER: OnceLock<(tempfile::TempDir, PathBuf)> = OnceLock::new();
    HELPER
        .get_or_init(|| {
            let dir = tempfile::tempdir().unwrap();
            let path = dir.path().join(if cfg!(windows) {
                "fixture.exe"
            } else {
                "fixture"
            });
            let source = Path::new(env!("CARGO_MANIFEST_DIR")).join("tests/support/fixture.rs");
            assert!(std::process::Command::new("rustc")
                .args(["--edition=2021"])
                .arg(source)
                .arg("-o")
                .arg(&path)
                .status()
                .unwrap()
                .success());
            (dir, path)
        })
        .1
        .clone()
}
fn command(args: &[&str]) -> Value {
    json!(std::iter::once(helper().to_string_lossy().into_owned())
        .chain(args.iter().map(|v| (*v).to_owned()))
        .collect::<Vec<_>>())
}
fn fixture(tasks: Value) -> tempfile::TempDir {
    let dir = tempfile::tempdir().unwrap();
    files::atomic_write(
        &dir.path().join("taskflow.yml"),
        serde_yaml::to_string(&json!({"version":1,"project":"app","tasks":tasks}))
            .unwrap()
            .as_bytes(),
    )
    .unwrap();
    dir
}
async fn graph(root: &Path) -> Arc<Graph> {
    Arc::new(Graph::build(Workspace::discover(root).await.unwrap()).unwrap())
}
async fn run(graph: Arc<Graph>, targets: &[&str]) -> runner::RunResult {
    let plan = Plan::create(
        &graph,
        &targets.iter().map(|s| s.to_string()).collect::<Vec<_>>(),
        &[],
        false,
    )
    .unwrap();
    runner::run_plan(
        graph,
        plan,
        RunOptions {
            quiet: true,
            ..RunOptions::default()
        },
        CancellationToken::new(),
    )
    .await
    .unwrap()
}

#[test]
fn configuration_rejects_duplicates_unknown_fields_and_invalid_policies() {
    for text in [
        "version: 1\nproject: app\nproject: duplicate\n",
        "version: 1\nproject: app\nunknown: true\n",
        "version: 1\nproject: app\ntasks:\n  test:\n    command: echo hi\n    cache: true\n",
        "version: 1\nproject: app\ntasks:\n  test:\n    command: echo hi\n    output: \
         ['../outside']\n",
    ] {
        let directory = tempfile::tempdir().unwrap();
        let file = directory.path().join("taskflow.yml");
        std::fs::write(&file, text).unwrap();
        assert!(config::load(&file).is_err());
    }
}

#[tokio::test]
async fn scenario_01_installation_prerequisite_precedes_build_and_deduplicates() {
    let dir = fixture(json!({
        "install":{"command":command(&["record","trace","install"]),"input":[],"install":true},
        "a":{"command":command(&["record","trace","a"]),"input":[],"dependsOn":["install"]},
        "b":{"command":command(&["record","trace","b"]),"input":[],"dependsOn":["install"]}
    }));
    let result = run(graph(dir.path()).await, &["a", "b"]).await;
    assert!(result.success, "{result:?}");
    let trace = std::fs::read_to_string(dir.path().join("trace")).unwrap();
    assert!(trace.starts_with("install\n"));
    assert_eq!(trace.matches("install").count(), 1);
}

#[tokio::test]
async fn scenarios_02_20_cache_hit_input_change_and_deleted_output_restoration() {
    let dir = fixture(
        json!({"build":{"command":command(&["copy","source","out/result"]),"input":["source"],"output":["out/**"],"cache":true,"tools":{"fixture":command(&["version"])}}}),
    );
    std::fs::write(dir.path().join("source"), "first").unwrap();
    let g = graph(dir.path()).await;
    assert_eq!(
        run(g.clone(), &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    assert_eq!(
        run(g.clone(), &["build"]).await.results["app#build"].outcome,
        Outcome::LocalCache
    );
    std::fs::remove_dir_all(dir.path().join("out")).unwrap();
    assert_eq!(
        run(g.clone(), &["build"]).await.results["app#build"].outcome,
        Outcome::Restored
    );
    assert_eq!(
        std::fs::read_to_string(dir.path().join("out/result")).unwrap(),
        "first"
    );
    std::fs::write(dir.path().join("source"), "second").unwrap();
    assert_eq!(
        run(g, &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    assert_eq!(
        std::fs::read_to_string(dir.path().join("out/result")).unwrap(),
        "second"
    );
}

#[tokio::test]
async fn scenarios_03_04_05_18_unchanged_preserves_independent_causes() {
    let dir = fixture(json!({
        "a":{"command":command(&["unchanged","trace","a"]),"input":["a.src"]},
        "d":{"command":command(&["record","trace","d"]),"input":["d.src"]},
        "b":{"command":command(&["record","trace","b"]),"input":["b.src"],"dependsOn":["a"]},
        "c":{"command":command(&["record","trace","c"]),"input":["c.src"],"dependsOn":["b"]},
        "join":{"command":command(&["record","trace","join"]),"input":[],"dependsOn":["a","d"]}
    }));
    let g = graph(dir.path()).await;
    let plan = Plan::create(&g, &[], &[PathBuf::from("a.src")], true).unwrap();
    let result = runner::run_plan(
        g.clone(),
        plan,
        RunOptions::default(),
        CancellationToken::new(),
    )
    .await
    .unwrap();
    assert_eq!(result.results["app#b"].outcome, Outcome::Suppressed);
    assert_eq!(result.results["app#c"].outcome, Outcome::Suppressed);
    assert_eq!(
        run(g.clone(), &["b"]).await.results["app#b"].outcome,
        Outcome::Executed
    );
    let plan = Plan::create(
        &g,
        &[],
        &[
            PathBuf::from("a.src"),
            PathBuf::from("b.src"),
            PathBuf::from("d.src"),
        ],
        true,
    )
    .unwrap();
    let result = runner::run_plan(
        g.clone(),
        plan,
        RunOptions::default(),
        CancellationToken::new(),
    )
    .await
    .unwrap();
    assert_eq!(result.results["app#b"].outcome, Outcome::Executed);
    assert_eq!(result.results["app#join"].outcome, Outcome::Executed);
    let plan = Plan::for_tasks(
        &g,
        BTreeMap::from([("app#b".into(), BTreeSet::from([Cause::Schedule]))]),
    )
    .unwrap();
    assert_eq!(
        runner::run_plan(g, plan, RunOptions::default(), CancellationToken::new())
            .await
            .unwrap()
            .results["app#b"]
            .outcome,
        Outcome::Executed
    );
}

#[test]
fn scenario_06_streaming_secret_masking_handles_all_boundaries() {
    let secret = b"long\nsecret\xe2\x98\x83".to_vec();
    let raw = [b"before ".as_slice(), &secret, b" after"].concat();
    for split in 0..=raw.len() {
        let mut redactor = Redactor::new(vec![secret.clone()]);
        let mut masked = redactor.push(&raw[..split], false);
        masked.extend(redactor.push(&raw[split..], true));
        assert_eq!(masked, b"before [REDACTED] after");
    }
}

#[tokio::test]
async fn scenarios_06_07_dotenv_precedence_disable_and_persisted_masking() {
    let dir = fixture(
        json!({"env":{"command":command(&["env","TFLOW_TEST_SECRET","value"]),"input":[],"secrets":["TFLOW_TEST_SECRET"]}}),
    );
    std::fs::write(
        dir.path().join(".env"),
        "TFLOW_TEST_SECRET=split-secret-value\n",
    )
    .unwrap();
    let g = graph(dir.path()).await;
    let result = run(g.clone(), &["env"]).await;
    assert!(result.success);
    let receipt = &result.results["app#env"];
    let log = std::fs::read_to_string(
        dir.path()
            .join(".taskflow/runs")
            .join(&receipt.execution)
            .join("output.log"),
    )
    .unwrap();
    assert_eq!(log, "[REDACTED]");
    let plan = Plan::create(&g, &["env".into()], &[], false).unwrap();
    runner::run_plan(
        g,
        plan,
        RunOptions {
            no_dotenv: true,
            quiet: true,
            ..RunOptions::default()
        },
        CancellationToken::new(),
    )
    .await
    .unwrap();
    assert_eq!(
        std::fs::read_to_string(dir.path().join("value")).unwrap(),
        ""
    );
}

#[test]
fn scenarios_09_16_shard_accounting_and_cron_dst() {
    let inventory = shard::Inventory {
        version: 1,
        tests: (0..7)
            .map(|i| shard::TestUnit {
                id: format!("test{i}"),
                duration_ms: i,
            })
            .collect(),
    };
    let assignments = shard::assign(&inventory, 3).unwrap();
    assert_eq!(
        assignments.iter().flatten().collect::<BTreeSet<_>>().len(),
        7
    );
    assert!(shard::account(&assignments[0], &[]).is_err());
    let mut clock = taskflow::schedule::CronClock::new("30 1 * * *", "America/New_York").unwrap();
    assert!(clock.tick("2026-11-01T05:30:00Z".parse().unwrap()));
    assert!(!clock.tick("2026-11-01T06:30:00Z".parse().unwrap()));
    assert!(clock.tick("2026-11-02T06:30:00Z".parse().unwrap()));
}

#[tokio::test]
async fn scenario_09_generic_shards_execute_and_account_for_every_test() {
    let dir = fixture(
        json!({"test":{"command":command(&["version"]),"input":[],"output":[],"shard":{"adapter":"generic","count":4,"list":command(&["inventory"]),"run":command(&["shard"])}}}),
    );
    let result = run(graph(dir.path()).await, &["test"]).await;
    assert!(result.success, "{result:?}");
    let folder = dir
        .path()
        .join(".taskflow/runs")
        .join(&result.results["app#test"].execution);
    let inventory: shard::Inventory =
        serde_json::from_slice(&std::fs::read(folder.join("inventory.json")).unwrap()).unwrap();
    let reports: Vec<shard::ShardResults> = (0..4)
        .map(|i| {
            serde_json::from_slice(&std::fs::read(folder.join(format!("shard-{i}.json"))).unwrap())
                .unwrap()
        })
        .collect();
    assert!(shard::aggregate(&inventory, 4, &reports).unwrap());
}

#[tokio::test]
async fn scenario_26_queries_cycles_missing_references_and_output_ownership() {
    let dir = fixture(
        json!({"a":{"command":command(&["version"]),"input":["a.src"]},"b":{"command":command(&["version"]),"input":[],"dependsOn":["a"]}}),
    );
    let g = graph(dir.path()).await;
    assert_eq!(
        g.path("app#b", "app#a", false).unwrap(),
        vec!["app#b", "app#a"]
    );
    assert_eq!(g.inputs(&dir.path().join("a.src")).unwrap(), vec!["app#a"]);
    for tasks in [
        json!({"a":{"command":"echo a","dependsOn":["b"]},"b":{"command":"echo b","dependsOn":["a"]}}),
        json!({"a":{"command":"echo a","dependsOn":["missing"]}}),
        json!({"a":{"command":"echo a","output":["out/**"]},"b":{"command":"echo b","output":["out/sub/**"]}}),
    ] {
        let dir = fixture(tasks);
        assert!(Graph::build(Workspace::discover(dir.path()).await.unwrap()).is_err());
    }
}

#[tokio::test]
async fn cache_rejects_corruption_and_path_traversal_before_changing_outputs() {
    let dir =
        fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":["out/**"]}}));
    std::fs::create_dir(dir.path().join("out")).unwrap();
    std::fs::write(dir.path().join("out/file"), "safe").unwrap();
    let g = graph(dir.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    let mut artifact =
        cache::Artifact::capture("key".into(), "app#build".into(), project, task).unwrap();
    artifact.files.push(cache::FileRecord {
        path: "../escape".into(),
        content: cache::Content::Directory,
    });
    artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
    assert!(artifact.restore("key", "app#build", project, task).is_err());
    assert_eq!(
        std::fs::read_to_string(dir.path().join("out/file")).unwrap(),
        "safe"
    );
}

#[tokio::test]
async fn scenario_17_cancellation_reaps_process_tree_and_never_caches() {
    let dir = fixture(
        json!({"sleep":{"command":command(&["sleep","parent.pid","child.pid"]),"input":[],"output":[],"cache":true,"tools":{"fixture":command(&["version"])}}}),
    );
    let g = graph(dir.path()).await;
    let plan = Plan::create(&g, &["sleep".into()], &[], false).unwrap();
    let cancel = CancellationToken::new();
    let stop = cancel.clone();
    let root = dir.path().to_path_buf();
    let task = tokio::spawn(runner::run_plan(g, plan, RunOptions::default(), cancel));
    for _ in 0..100 {
        if root.join("child.pid").exists() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert!(root.join("child.pid").exists());
    stop.cancel();
    let result = tokio::time::timeout(Duration::from_secs(5), task)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(!result.success);
    assert!(!root.join(".taskflow/cache/entries").exists());
    #[cfg(unix)]
    for name in ["parent.pid", "child.pid"] {
        let pid: i32 = std::fs::read_to_string(root.join(name))
            .unwrap()
            .parse()
            .unwrap();
        for _ in 0..100 {
            if nix::sys::signal::kill(nix::unistd::Pid::from_raw(pid), None).is_err() {
                break;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
        assert!(
            nix::sys::signal::kill(nix::unistd::Pid::from_raw(pid), None).is_err(),
            "process {pid} survived"
        );
    }
}

#[tokio::test]
async fn scenario_25_external_effect_survives_unchanged_prerequisite() {
    let dir = fixture(
        json!({"build":{"command":command(&["unchanged","trace","build"]),"input":["source"]},"deploy":{"command":command(&["record","trace","deploy"]),"input":["deploy.config"],"dependsOn":["build"],"effect":"external"}}),
    );
    let g = graph(dir.path()).await;
    let plan = Plan::create(&g, &[], &[PathBuf::from("source")], true).unwrap();
    let result = runner::run_plan(g, plan, RunOptions::default(), CancellationToken::new())
        .await
        .unwrap();
    assert_eq!(result.results["app#deploy"].outcome, Outcome::Executed);
}
