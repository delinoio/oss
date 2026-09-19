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

#[tokio::test]
#[ignore = "native adapter conformance requires pnpm and Go in addition to Rust"]
async fn scenarios_12_13_14_21_23_24_native_workspaces_aliases_and_conditions() {
    let root = tempfile::tempdir().unwrap();
    let write = |name: &str, content: &str| {
        files::atomic_write(&root.path().join(name), content.as_bytes()).unwrap()
    };
    write(
        "taskflow.yml",
        "version: 1\nproject: repo\nworkspace:\n  manifests: [pnpm-workspace.yaml, Cargo.toml, \
         go.work]\n",
    );
    write(
        "package.json",
        r#"{"name":"root","private":true,"packageManager":"pnpm@10.26.2"}"#,
    );
    write(
        "pnpm-workspace.yaml",
        "packages:\n  - 'packages/*'\n  - '!packages/excluded'\n",
    );
    write(
        "packages/a/package.json",
        r#"{"name":"package-a","version":"1.0.0","dependencies":{"alias":"workspace:package-b@*"},"devDependencies":{"package-b":"workspace:*"}}"#,
    );
    write(
        "packages/b/package.json",
        r#"{"name":"package-b","version":"1.0.0"}"#,
    );
    write(
        "packages/excluded/package.json",
        r#"{"name":"excluded","version":"1.0.0"}"#,
    );
    write(
        "packages/a/taskflow.yml",
        "version: 1\nproject: js-a\ntasks:\n  build:\n    command: [node, --version]\n    \
         dependsOn:\n      - task: build\n        from: dependencies\n",
    );
    write(
        "packages/b/taskflow.yml",
        "version: 1\nproject: js-b\ntasks:\n  build:\n    command: [node, --version]\n",
    );
    write(
        "Cargo.toml",
        "[workspace]\nmembers=['rust/a','rust/b']\nresolver='2'\n",
    );
    write(
        "rust/a/Cargo.toml",
        r#"[package]
name='native-a'
version='0.1.0'
edition='2021'
[dependencies]
renamed={package='native-b',path='../b'}
[target.'cfg(windows)'.build-dependencies]
renamed={package='native-b',path='../b'}
"#,
    );
    write(
        "rust/a/src/lib.rs",
        "pub fn a() -> usize { renamed::b() }\n",
    );
    write(
        "rust/b/Cargo.toml",
        "[package]\nname='native-b'\nversion='0.1.0'\nedition='2021'\n",
    );
    write("rust/b/src/lib.rs", "pub fn b() -> usize { 1 }\n");
    write(
        "rust/a/taskflow.yml",
        "version: 1\nproject: rust-a\ntasks:\n  build:\n    command: [cargo, build, -p, \
         native-a]\n",
    );
    write("rust/b/taskflow.yml", "version: 1\nproject: rust-b\n");
    write("go.work", "go 1.25.0\nuse (\n ./go/a\n ./go/b\n)\n");
    write(
        "go/a/go.mod",
        "module example.test/a\ngo 1.25.0\nrequire example.test/b v0.0.0\nreplace example.test/b \
         => ../b\n",
    );
    write("go/b/go.mod", "module example.test/b\ngo 1.25.0\n");
    write("go/a/taskflow.yml", "version: 1\nproject: go-a\n");
    write("go/b/taskflow.yml", "version: 1\nproject: go-b\n");
    taskflow::discover::output_tool(
        root.path(),
        &["pnpm", "install", "--lockfile-only", "--ignore-scripts"],
        &[],
    )
    .await
    .unwrap();
    let lockfile = std::process::Command::new("cargo")
        .args(["generate-lockfile", "--offline"])
        .current_dir(root.path())
        .output()
        .unwrap();
    assert!(
        lockfile.status.success(),
        "{}",
        String::from_utf8_lossy(&lockfile.stderr)
    );
    let g = graph(root.path()).await;
    if !g.workspace.complete() {
        for (directory, args) in [
            (
                root.path().to_path_buf(),
                vec![
                    "cargo",
                    "metadata",
                    "--locked",
                    "--offline",
                    "--format-version",
                    "1",
                ],
            ),
            (
                root.path().join("go/a"),
                vec!["go", "list", "-mod=readonly", "-m", "-json", "all"],
            ),
        ] {
            let output = std::process::Command::new(args[0])
                .args(&args[1..])
                .current_dir(directory)
                .output()
                .unwrap();
            eprintln!(
                "fixture metadata diagnostic: {}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
    }
    assert!(g.workspace.complete(), "{:?}", g.workspace.coverage);
    assert_eq!(g.workspace.projects.len(), 7);
    assert_eq!(g.prerequisites("js-a#build"), vec!["js-b#build"]);
    assert!(
        g.prerequisites("rust-a#build").is_empty(),
        "native compilation must remain Cargo-owned"
    );
    assert!(g
        .workspace
        .edges
        .iter()
        .any(|e| e.from == "go-a" && e.to == "go-b"));
    assert!(g
        .workspace
        .edges
        .iter()
        .any(|e| e.from == "rust-a" && e.to == "rust-b" && e.condition.is_some()));
    assert!(g.workspace.edges.iter().any(|e| e.from == "js-a"
        && e.to == "js-b"
        && e.kind == config::DependencyKind::DevDependencies));
    let plan = Plan::create(
        &g,
        &["build".into()],
        &[PathBuf::from("rust/b/src/lib.rs")],
        true,
    )
    .unwrap();
    assert!(plan.causes.contains_key("rust-a#build"));
    let direct = taskflow::discover::output_tool(
        &root.path().join("rust/a"),
        &["cargo", "build", "-p", "native-a", "--offline"],
        &[],
    )
    .await;
    assert!(direct.is_ok(), "{direct:?}");
    assert!(run(g.clone(), &["rust-a#build"]).await.success);
    write("packages/b/taskflow.yml", "version: 1\nproject: js-b\n");
    let changed = graph(root.path()).await;
    assert!(changed.prerequisites("js-a#build").is_empty());
    assert!(changed.explanations.iter().any(|e| e.contains("task-less")));
    assert_ne!(g.workspace.generation, changed.workspace.generation);
    write("packages/b/taskflow.yml", "version: 1\nproject: js-a\n");
    assert!(Workspace::discover(root.path()).await.is_err());
}

#[tokio::test]
#[ignore = "requires Docker and the pinned Node fixture image"]
async fn scenario_10_local_docker_execution_uses_explicit_platform_and_cleans_container() {
    let image = "node@sha256:6dac556d980b7f0e5498d08f08cee0ca67798b4ad6c23964a9214920e67758d0";
    let dir = fixture(
        json!({"container":{"command":["node","-e","require('fs').mkdirSync('out',{recursive:true});require('fs').writeFileSync('out/value',process.env.MESSAGE)"],"input":[],"output":["out/**"],"env":{"MESSAGE":"docker-ok"},"platform":{"os":"linux","arch":config::host_arch(),"executor":"docker","image":image}}}),
    );
    let result = run(graph(dir.path()).await, &["container"]).await;
    assert!(result.success, "{result:?}");
    assert_eq!(
        std::fs::read_to_string(dir.path().join("out/value")).unwrap(),
        "docker-ok"
    );
    let name = format!("tflow-{}", result.results["app#container"].execution);
    let output = taskflow::discover::output_tool(
        dir.path(),
        &[
            "docker",
            "ps",
            "-a",
            "--filter",
            &format!("name=^/{name}$"),
            "--format",
            "{{.Names}}",
        ],
        &[],
    )
    .await
    .unwrap();
    assert!(output.is_empty());
}

#[tokio::test]
#[ignore = "requires Docker and the pinned MinIO conformance image"]
async fn scenario_22_real_s3_cache_restores_on_clean_runner_and_handles_denied_credentials() {
    struct Container(String);
    impl Drop for Container {
        fn drop(&mut self) {
            let _ = std::process::Command::new("docker")
                .args(["rm", "-f", &self.0])
                .stdout(std::process::Stdio::null())
                .status();
        }
    }
    let name = format!("tflow-s3-{}", uuid::Uuid::now_v7());
    let container = Container(name.clone());
    let root = tempfile::tempdir().unwrap();
    let image = "quay.io/minio/minio@sha256:\
                 14cea493d9a34af32f524e538b8346cf79f3321eff8e708c1e2960462bd8936e";
    taskflow::discover::output_tool(
        root.path(),
        &[
            "docker",
            "run",
            "-d",
            "--rm",
            "--name",
            &name,
            "-p",
            "127.0.0.1::9000",
            "-e",
            "MINIO_ROOT_USER=taskflowfixture",
            "-e",
            "MINIO_ROOT_PASSWORD=taskflow-fixture-password",
            image,
            "server",
            "/data",
        ],
        &[],
    )
    .await
    .unwrap();
    let address = String::from_utf8(
        taskflow::discover::output_tool(root.path(), &["docker", "port", &name, "9000"], &[])
            .await
            .unwrap(),
    )
    .unwrap();
    let endpoint = format!("http://{}", address.trim());
    for _ in 0..100 {
        if reqwest::get(format!("{endpoint}/minio/health/live"))
            .await
            .is_ok_and(|r| r.status().is_success())
        {
            break;
        }
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    taskflow::discover::output_tool(
        root.path(),
        &[
            "docker",
            "exec",
            &name,
            "mc",
            "alias",
            "set",
            "fixture",
            "http://127.0.0.1:9000",
            "taskflowfixture",
            "taskflow-fixture-password",
        ],
        &[],
    )
    .await
    .unwrap();
    taskflow::discover::output_tool(
        root.path(),
        &["docker", "exec", &name, "mc", "mb", "fixture/taskflow"],
        &[],
    )
    .await
    .unwrap();
    let suffix = uuid::Uuid::now_v7().simple().to_string();
    let key_env = format!("TFLOW_FIXTURE_KEY_{suffix}");
    let secret_env = format!("TFLOW_FIXTURE_SECRET_{suffix}");
    std::env::set_var(&key_env, "taskflowfixture");
    std::env::set_var(&secret_env, "taskflow-fixture-password");
    let tasks = json!({"build":{"command":command(&["copy","source","out/result"]),"input":["source"],"output":["out/**"],"cache":true,"tools":{"fixture":command(&["version"])}}});
    let configuration = json!({"version":1,"project":"app","tasks":tasks,"remote":{"endpoint":endpoint,"bucket":"taskflow","namespace":"conformance","region":"us-east-1","accessKeyEnv":key_env,"secretKeyEnv":secret_env,"mode":"read-write"}});
    let source = serde_yaml::to_string(&configuration).unwrap();
    let first = tempfile::tempdir().unwrap();
    let second = tempfile::tempdir().unwrap();
    let denied = tempfile::tempdir().unwrap();
    for directory in [&first, &second, &denied] {
        std::fs::write(directory.path().join("taskflow.yml"), &source).unwrap();
        std::fs::write(directory.path().join("source"), "remote artifact").unwrap();
    }
    assert_eq!(
        run(graph(first.path()).await, &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    let restored = run(graph(second.path()).await, &["build"]).await;
    assert_eq!(
        restored.results["app#build"].outcome,
        Outcome::Restored,
        "{restored:?}"
    );
    assert_eq!(
        std::fs::read_to_string(second.path().join("out/result")).unwrap(),
        "remote artifact"
    );
    std::env::set_var(&secret_env, "incorrect-fixture-password");
    assert_eq!(
        run(graph(denied.path()).await, &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    std::env::remove_var(&key_env);
    std::env::remove_var(&secret_env);
    drop(container);
}

#[tokio::test]
#[ignore = "native suite conformance installs pinned Vitest and Jest fixtures"]
async fn scenario_09_native_go_rust_vitest_and_jest_sharding() {
    let go = fixture(
        json!({"test":{"command":["go","test","./..."],"input":["*.go"],"output":[],"shard":{"adapter":"go","count":2}}}),
    );
    std::fs::write(
        go.path().join("go.mod"),
        "module example.test/shard\ngo 1.25.0\n",
    )
    .unwrap();
    std::fs::write(
        go.path().join("sample_test.go"),
        "package shard\nimport \"testing\"\nfunc TestOne(t *testing.T) {}\nfunc TestTwo(t \
         *testing.T) {}\n",
    )
    .unwrap();
    let result = run(graph(go.path()).await, &["test"]).await;
    assert!(result.success, "{result:?}");
    taskflow::discover::output_tool(go.path(), &["go", "test", "./..."], &[])
        .await
        .unwrap();
    let (inventory, reports) = shard::read_reports(
        &go.path()
            .join(".taskflow/runs")
            .join(&result.results["app#test"].execution),
    )
    .unwrap();
    assert_eq!(inventory.tests.len(), 2);
    assert!(shard::aggregate(&inventory, 2, &reports).unwrap());
    let rust = fixture(
        json!({"test":{"command":["cargo","test"],"input":["src/**"],"output":[],"shard":{"adapter":"libtest","count":2}}}),
    );
    files::atomic_write(
        &rust.path().join("Cargo.toml"),
        b"[package]\nname='shard-fixture'\nversion='0.1.0'\nedition='2021'\n[workspace]\n",
    )
    .unwrap();
    files::atomic_write(&rust.path().join("src/lib.rs"),b"/// ```\n/// assert!(true);\n/// ```\npub fn sample() {}\n#[test] fn one() {}\n#[test] fn two() {}\n").unwrap();
    taskflow::discover::output_tool(
        rust.path(),
        &["cargo", "generate-lockfile", "--offline"],
        &[],
    )
    .await
    .unwrap();
    let result = run(graph(rust.path()).await, &["test"]).await;
    assert!(result.success, "{result:?}");
    taskflow::discover::output_tool(rust.path(), &["cargo", "test", "--offline"], &[])
        .await
        .unwrap();
    let (inventory, reports) = shard::read_reports(
        &rust
            .path()
            .join(".taskflow/runs")
            .join(&result.results["app#test"].execution),
    )
    .unwrap();
    assert_eq!(
        inventory.tests.len(),
        3,
        "two libtests and one doctest unit"
    );
    assert!(shard::aggregate(&inventory, 2, &reports).unwrap());
    for (adapter, version) in [("vitest", "4.1.11"), ("jest", "29.7.0")] {
        let command = if adapter == "vitest" {
            vec!["pnpm", "exec", adapter, "run"]
        } else {
            vec!["pnpm", "exec", adapter, "--runInBand"]
        };
        let js = fixture(
            json!({"test":{"command":command,"input":["*.test.js"],"output":[],"shard":{"adapter":adapter,"count":3}}}),
        );
        std::fs::write(js.path().join("package.json"),serde_json::to_vec(&json!({"name":"taskflow-shard-fixture","private":true,"packageManager":"pnpm@10.26.2","devDependencies":{adapter:version}})).unwrap()).unwrap();
        for name in ["one", "two"] {
            std::fs::write(
                js.path().join(format!("{name}.test.js")),
                if adapter == "vitest" {
                    "import {test,expect} from 'vitest';test('works',()=>expect(1).toBe(1));"
                } else {
                    "test('works',()=>expect(1).toBe(1));"
                },
            )
            .unwrap();
        }
        taskflow::discover::output_tool(
            js.path(),
            &[
                "pnpm",
                "install",
                "--ignore-scripts",
                "--prefer-offline",
                "--fetch-timeout=30000",
                "--fetch-retries=1",
            ],
            &[],
        )
        .await
        .unwrap();
        let result = run(graph(js.path()).await, &["test"]).await;
        assert!(result.success, "{adapter}: {result:?}");
        taskflow::discover::output_tool(js.path(), &command, &[])
            .await
            .unwrap();
        let (inventory, reports) = shard::read_reports(
            &js.path()
                .join(".taskflow/runs")
                .join(&result.results["app#test"].execution),
        )
        .unwrap();
        assert_eq!(
            inventory.tests.len(),
            2,
            "each JS fixture has two test files"
        );
        assert!(shard::aggregate(&inventory, 3, &reports).unwrap());
    }
}

#[tokio::test]
async fn scenarios_08_15_16_19_watch_and_timer_companions_preserve_server() {
    let address = std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
        .to_string();
    let dir = fixture(json!({
        "server":{"command":command(&["server",&address,"server.pid"]),"input":[],"service":true,"readiness":{"type":"tcp","address":address,"timeout":"5s"},"dependsOn":["check"],"with":["check","poll"]},
        "check":{"command":command(&["record","trace","check"]),"input":["source"],"watch":{"initial":true,"debounce":"30ms"}},
        "poll":{"command":command(&["record","trace","poll"]),"input":[],"schedule":{"every":"100ms","initial":false},"overlap":"skip"}
    }));
    let mut cfg: Value =
        serde_yaml::from_slice(&std::fs::read(dir.path().join("taskflow.yml")).unwrap()).unwrap();
    cfg["start"] = json!({"default":["server"]});
    files::atomic_write(
        &dir.path().join("taskflow.yml"),
        serde_yaml::to_string(&cfg).unwrap().as_bytes(),
    )
    .unwrap();
    std::fs::write(dir.path().join("source"), "before").unwrap();
    let cancel = CancellationToken::new();
    let token = cancel.clone();
    let root = dir.path().to_path_buf();
    let session = tokio::spawn(async move {
        taskflow::session::start(
            &root,
            "default",
            RunOptions {
                quiet: true,
                ..RunOptions::default()
            },
            token,
        )
        .await
    });
    for _ in 0..500 {
        if dir.path().join("server.pid").exists() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    assert!(dir.path().join("server.pid").exists());
    let pid = std::fs::read_to_string(dir.path().join("server.pid")).unwrap();
    assert_eq!(
        std::fs::read_to_string(dir.path().join("trace"))
            .unwrap()
            .matches("check")
            .count(),
        1
    );
    std::fs::write(dir.path().join("source"), "after").unwrap();
    for _ in 0..100 {
        let trace = std::fs::read_to_string(dir.path().join("trace")).unwrap();
        if trace.matches("check").count() == 2 && trace.contains("poll") {
            break;
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    let trace = std::fs::read_to_string(dir.path().join("trace")).unwrap();
    assert_eq!(trace.matches("check").count(), 2, "{trace}");
    assert!(trace.contains("poll"));
    assert_eq!(
        std::fs::read_to_string(dir.path().join("server.pid")).unwrap(),
        pid
    );
    cancel.cancel();
    tokio::time::timeout(Duration::from_secs(5), session)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(tokio::net::TcpStream::connect(&address).await.is_err());
}

#[tokio::test]
async fn scenarios_11_22_26_ci_units_transfer_artifacts_and_preserve_causes() {
    let dir = fixture(json!({
        "a":{"command":command(&["copy","source","a-out/file"]),"input":["source"],"output":["a-out/**"]},
        "b":{"command":command(&["copy","a-out/file","b-out/file"]),"input":["a-out/**"],"output":["b-out/**"],"dependsOn":["a"]},
        "c":{"command":command(&["copy","b-out/file","c-out/file"]),"input":["b-out/**"],"output":["c-out/**"],"dependsOn":["b"]}
    }));
    let mut cfg: Value =
        serde_yaml::from_slice(&std::fs::read(dir.path().join("taskflow.yml")).unwrap()).unwrap();
    let platform = config::Platform::default();
    let (os, arch) = platform.resolved();
    for task in cfg["tasks"].as_object_mut().unwrap().values_mut() {
        task["platform"] = json!({"os":os,"arch":arch});
    }
    cfg["ci"] = json!({"revision":"1111111111111111111111111111111111111111","rust":"nightly-2026-01-01","runners":{platform.key():"self-hosted"}});
    files::atomic_write(
        &dir.path().join("taskflow.yml"),
        serde_yaml::to_string(&cfg).unwrap().as_bytes(),
    )
    .unwrap();
    std::fs::write(dir.path().join("source"), "artifact").unwrap();
    let g = graph(dir.path()).await;
    let blueprint = taskflow::ci::Blueprint::new(&g, vec!["c".into()]).unwrap();
    assert_eq!(blueprint.units.len(), 3);
    let plan = taskflow::ci::prepare(dir.path(), &blueprint, None)
        .await
        .unwrap();
    let storage = tempfile::tempdir().unwrap();
    let mut completed = BTreeSet::new();
    while completed.len() < blueprint.units.len() {
        let unit = blueprint
            .units
            .iter()
            .find(|u| !completed.contains(&u.id) && u.needs.iter().all(|n| completed.contains(n)))
            .unwrap();
        let runner = tempfile::tempdir().unwrap();
        std::fs::copy(
            dir.path().join("taskflow.yml"),
            runner.path().join("taskflow.yml"),
        )
        .unwrap();
        std::fs::copy(dir.path().join("source"), runner.path().join("source")).unwrap();
        let output = storage.path().join(&unit.id).join("bundle.json");
        let result = taskflow::ci::execute(
            runner.path(),
            &blueprint,
            plan.clone(),
            &unit.id,
            storage.path(),
            &output,
            CancellationToken::new(),
        )
        .await
        .unwrap();
        assert!(result.success, "{result:?}");
        assert_eq!(
            result.results.len(),
            1,
            "prerequisites must not be executed again on another runner"
        );
        completed.insert(unit.id.clone());
    }
    assert_eq!(
        taskflow::ci::aggregate(&blueprint, &plan, storage.path()).unwrap()["success"],
        true
    );
    taskflow::ci::export(
        &g,
        vec!["c".into()],
        Path::new(".github/workflows/taskflow.yml"),
    )
    .unwrap();
    let workflow: Value = serde_yaml::from_slice(
        &std::fs::read(dir.path().join(".github/workflows/taskflow.yml")).unwrap(),
    )
    .unwrap();
    assert!(
        workflow["jobs"]["result"]["needs"]
            .as_array()
            .unwrap()
            .len()
            >= 4
    );
    if let Ok(actionlint) = std::env::var("TFLOW_ACTIONLINT") {
        let result = std::process::Command::new(actionlint)
            .arg("-shellcheck=")
            .arg(dir.path().join(".github/workflows/taskflow.yml"))
            .output()
            .unwrap();
        assert!(
            result.status.success(),
            "{}{}",
            String::from_utf8_lossy(&result.stdout),
            String::from_utf8_lossy(&result.stderr)
        );
    }
    let bundle_path = storage
        .path()
        .join(&blueprint.units[0].id)
        .join("bundle.json");
    let original = std::fs::read(&bundle_path).unwrap();
    let mut incomplete: Value = serde_json::from_slice(&original).unwrap();
    incomplete["result"]["results"] = json!({});
    std::fs::write(&bundle_path, serde_json::to_vec(&incomplete).unwrap()).unwrap();
    assert!(taskflow::ci::aggregate(&blueprint, &plan, storage.path()).is_err());
    let mut incomplete: Value = serde_json::from_slice(&original).unwrap();
    incomplete["artifacts"] = json!({});
    std::fs::write(&bundle_path, serde_json::to_vec(&incomplete).unwrap()).unwrap();
    assert!(taskflow::ci::aggregate(&blueprint, &plan, storage.path()).is_err());
    std::fs::write(&bundle_path, original).unwrap();
    std::fs::remove_file(
        storage
            .path()
            .join(&blueprint.units[0].id)
            .join("bundle.json"),
    )
    .unwrap();
    assert!(taskflow::ci::aggregate(&blueprint, &plan, storage.path()).is_err());
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
        "version: 1\nproject: app\ntasks:\n  test: {command: echo one}\n  test: {command: echo \
         two}\n",
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
    for _ in 0..500 {
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

#[test]
fn schema_is_fresh_and_cli_queries_mask_designated_values() {
    let expected: Value =
        serde_json::from_slice(include_bytes!("../taskflow.schema.json")).unwrap();
    assert_eq!(
        expected,
        serde_json::to_value(schemars::schema_for!(config::Config)).unwrap()
    );
    let directory = fixture(
        json!({"secret": {"command":command(&["env","EXAMPLE_VALUE"]),"env":{"EXAMPLE_VALUE":"fixture-sensitive-value"},"secrets":["EXAMPLE_VALUE"]}}),
    );
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .args(["--json", "--root"])
        .arg(directory.path())
        .args(["query", "tasks"])
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(!String::from_utf8_lossy(&output.stdout).contains("fixture-sensitive-value"));
    assert!(String::from_utf8_lossy(&output.stdout).contains("[REDACTED]"));
}

#[tokio::test]
async fn cached_shards_retain_complete_accounting_evidence() {
    let directory = fixture(
        json!({"suite":{"command":command(&["version"]),"input":[],"output":[],"cache":true,"tools":{"fixture":command(&["version"])},"shard":{"adapter":"generic","count":4,"list":command(&["inventory"]),"run":command(&["shard"])}}}),
    );
    let g = graph(directory.path()).await;
    assert!(run(g.clone(), &["suite"]).await.success);
    let cached = run(g, &["suite"]).await;
    let receipt = &cached.results["app#suite"];
    assert_eq!(receipt.outcome, Outcome::LocalCache);
    let (inventory, reports) = shard::read_reports(
        &directory
            .path()
            .join(".taskflow/runs")
            .join(&receipt.execution),
    )
    .unwrap();
    assert!(shard::aggregate(&inventory, 4, &reports).unwrap());
}

fn profile(directory: &Path, tasks: &[&str]) {
    let path = directory.join("taskflow.yml");
    let mut value: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    value["start"] = json!({"default": tasks});
    files::atomic_write(&path, serde_yaml::to_string(&value).unwrap().as_bytes()).unwrap();
}
async fn wait_lines(path: &Path, prefix: &str, count: usize) {
    let deadline = tokio::time::Instant::now() + Duration::from_secs(10);
    loop {
        if std::fs::read_to_string(path)
            .unwrap_or_default()
            .lines()
            .filter(|l| l.starts_with(prefix))
            .count()
            >= count
        {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "missing {count} {prefix} records in {}",
            path.display()
        );
        tokio::time::sleep(Duration::from_millis(15)).await;
    }
}

#[tokio::test]
async fn reading_session_files_does_not_cancel_or_requeue_work() {
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap().to_string();
    drop(listener);
    let directory = fixture(json!({"check": {
        "command": command(&["paced", "events", &address, "500"]),
        "input": ["source"], "watch": {"debounce": "20ms"}
    }}));
    std::fs::write(directory.path().join("source"), "unchanged").unwrap();
    profile(directory.path(), &["check"]);
    let root = directory.path().to_path_buf();
    let token = CancellationToken::new();
    let stop = token.clone();
    let session = tokio::spawn(async move {
        taskflow::session::start(&root, "default", RunOptions::default(), stop).await
    });
    let events = directory.path().join("events");
    wait_lines(&events, "start", 1).await;
    for _ in 0..20 {
        for name in ["taskflow.yml", "source"] {
            std::fs::read(directory.path().join(name)).unwrap();
        }
        tokio::time::sleep(Duration::from_millis(20)).await;
    }
    wait_lines(&events, "end", 1).await;
    tokio::time::timeout(Duration::from_secs(10), async {
        while !runner::previous(directory.path(), "app#check").is_some_and(|r| r.success()) {
            tokio::time::sleep(Duration::from_millis(15)).await;
        }
    })
    .await
    .expect("read-only events must allow the task to complete");
    token.cancel();
    assert!(session.await.unwrap().unwrap().success);
    let records = std::fs::read_to_string(events).unwrap();
    assert_eq!(
        records
            .lines()
            .filter(|line| line.starts_with("start"))
            .count(),
        1,
        "{records}"
    );
}

#[tokio::test]
async fn scenario_17_queue_skip_restart_own_real_exclusive_processes() {
    for (overlap, expected_starts) in [("queue", 2), ("skip", 1), ("restart", 2)] {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap().to_string();
        drop(listener);
        let directory = fixture(
            json!({"check":{"command":command(&["paced","events",&address,"700"]),"input":["source"],"watch":{"debounce":"20ms"},"overlap":overlap}}),
        );
        std::fs::write(directory.path().join("source"), "initial").unwrap();
        profile(directory.path(), &["check"]);
        let root = directory.path().to_path_buf();
        let token = CancellationToken::new();
        let stop = token.clone();
        let session = tokio::spawn(async move {
            taskflow::session::start(
                &root,
                "default",
                RunOptions {
                    quiet: true,
                    ..Default::default()
                },
                stop,
            )
            .await
        });
        let events = directory.path().join("events");
        wait_lines(&events, "start", 1).await;
        for change in ["first", "second"] {
            std::fs::write(directory.path().join("source"), change).unwrap();
            tokio::time::sleep(Duration::from_millis(40)).await;
        }
        wait_lines(&events, "start", expected_starts).await;
        wait_lines(&events, "end", if overlap == "queue" { 2 } else { 1 }).await;
        tokio::time::sleep(Duration::from_millis(100)).await;
        token.cancel();
        session.await.unwrap().unwrap();
        let records = std::fs::read_to_string(events).unwrap();
        let starts = records.lines().filter(|l| l.starts_with("start")).count();
        // Restart may replace twice when two separated edits are delivered.
        assert!(
            if overlap == "restart" {
                (2..=3).contains(&starts)
            } else {
                starts == expected_starts
            },
            "{overlap}: {records}"
        );
        assert!(
            std::net::TcpListener::bind(&address).is_ok(),
            "session leaked a process"
        );
    }
}

#[tokio::test]
async fn scenarios_15_19_21_live_invalid_configuration_recovers_atomically() {
    let directory = fixture(
        json!({"check":{"command":command(&["record","events","old"]),"input":["source"],"watch":{}}}),
    );
    std::fs::write(directory.path().join("source"), "initial").unwrap();
    profile(directory.path(), &["check"]);
    let path = directory.path().join("taskflow.yml");
    let original = std::fs::read(&path).unwrap();
    let root = directory.path().to_path_buf();
    let token = CancellationToken::new();
    let stop = token.clone();
    let session = tokio::spawn(async move {
        taskflow::session::start(
            &root,
            "default",
            RunOptions {
                quiet: true,
                ..Default::default()
            },
            stop,
        )
        .await
    });
    let events = directory.path().join("events");
    wait_lines(&events, "old", 1).await;
    files::atomic_write(&path, b"version: 1\nproject: app\ntasks: [invalid").unwrap();
    tokio::time::sleep(Duration::from_millis(300)).await;
    std::fs::write(directory.path().join("source"), "changed").unwrap();
    tokio::time::sleep(Duration::from_millis(300)).await;
    assert_eq!(std::fs::read_to_string(&events).unwrap().lines().count(), 1);
    let mut corrected: Value = serde_yaml::from_slice(&original).unwrap();
    corrected["tasks"]["check"]["command"] = command(&["record", "events", "new"]);
    files::atomic_write(&path, serde_yaml::to_string(&corrected).unwrap().as_bytes()).unwrap();
    wait_lines(&events, "new", 1).await;
    token.cancel();
    session.await.unwrap().unwrap();
    let g = graph(directory.path()).await;
    let plan = Plan::create(&g, &[], &[PathBuf::from("removed/package.json")], true).unwrap();
    assert!(plan.causes.contains_key("app#check"));
}

#[tokio::test]
async fn server_readiness_failure_reaps_concurrent_work_before_returning() {
    let directory = fixture(json!({
        "server":{"command":command(&["sleep","server.pid"]),"input":[],"service":true,"readiness":{"type":"command","command":command(&["fail-after-files","server.pid","check.pid"]),"timeout":"3s"}},
        "check":{"command":command(&["sleep","check.pid"]),"input":[],"watch":{}}
    }));
    profile(directory.path(), &["server", "check"]);
    let result = tokio::time::timeout(
        Duration::from_secs(8),
        taskflow::session::start(
            directory.path(),
            "default",
            RunOptions {
                jobs: 2,
                quiet: true,
                ..Default::default()
            },
            CancellationToken::new(),
        ),
    )
    .await
    .expect("service failure must promptly cancel its parallel checks");
    assert!(result.is_err());
    #[cfg(unix)]
    for name in ["server.pid", "check.pid"] {
        let pid: i32 = std::fs::read_to_string(directory.path().join(name))
            .unwrap()
            .parse()
            .unwrap();
        assert!(
            nix::sys::signal::kill(nix::unistd::Pid::from_raw(pid), None).is_err(),
            "owned process survived session failure"
        );
    }
}

#[tokio::test]
async fn untrusted_ci_never_contacts_the_remote_cache() {
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let directory = fixture(
        json!({"check":{"command":command(&["version"]),"input":[],"output":[],"cache":true,"tools":{"fixture":command(&["version"])}}}),
    );
    let path = directory.path().join("taskflow.yml");
    let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    config["remote"] = json!({"endpoint":format!("http://{}",listener.local_addr().unwrap()),"bucket":"fixture","namespace":"fixture","accessKeyEnv":"FIXTURE_ACCESS","secretKeyEnv":"FIXTURE_PRIVATE","mode":"read-write"});
    std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
    let output = tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .arg("--root")
        .arg(directory.path())
        .args(["run", "check", "--quiet"])
        .env("GITHUB_EVENT_NAME", "pull_request")
        .env("FIXTURE_ACCESS", "fixture-value")
        .env("FIXTURE_PRIVATE", "fixture-value")
        .output()
        .await
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(
        tokio::time::timeout(Duration::from_millis(100), listener.accept())
            .await
            .is_err()
    );
}

#[tokio::test]
async fn scenario_09_ci_shards_reject_partial_success_and_gate_secret_mapping() {
    let directory = fixture(
        json!({"test":{"command":command(&["version"]),"input":[],"output":[],"secrets":["BUILD_AUTH"],"shard":{"adapter":"generic","count":4,"list":command(&["inventory"]),"run":command(&["shard"])}}}),
    );
    let path = directory.path().join("taskflow.yml");
    let mut value: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    let platform = config::Platform::default();
    let (os, arch) = platform.resolved();
    value["tasks"]["test"]["platform"] = json!({"os":os,"arch":arch});
    value["ci"] = json!({"revision":"1111111111111111111111111111111111111111","rust":"nightly-2026-01-01","runners":{platform.key():"self-hosted"}});
    std::fs::write(&path, serde_yaml::to_string(&value).unwrap()).unwrap();
    let g = graph(directory.path()).await;
    let blueprint = taskflow::ci::Blueprint::new(&g, vec!["test".into()]).unwrap();
    assert_eq!(blueprint.units.len(), 4);
    let plan = taskflow::ci::prepare(directory.path(), &blueprint, None)
        .await
        .unwrap();
    let storage = tempfile::tempdir().unwrap();
    for unit in &blueprint.units {
        let runner = tempfile::tempdir().unwrap();
        std::fs::copy(&path, runner.path().join("taskflow.yml")).unwrap();
        let result = taskflow::ci::execute(
            runner.path(),
            &blueprint,
            plan.clone(),
            &unit.id,
            storage.path(),
            &storage.path().join(&unit.id).join("bundle.json"),
            CancellationToken::new(),
        )
        .await
        .unwrap();
        assert!(result.success);
    }
    assert_eq!(
        taskflow::ci::aggregate(&blueprint, &plan, storage.path()).unwrap()["success"],
        true
    );
    let bundle_path = storage
        .path()
        .join(&blueprint.units[0].id)
        .join("bundle.json");
    let mut partial: Value = serde_json::from_slice(&std::fs::read(&bundle_path).unwrap()).unwrap();
    partial["shards"] = json!({});
    std::fs::write(bundle_path, serde_json::to_vec(&partial).unwrap()).unwrap();
    assert!(taskflow::ci::aggregate(&blueprint, &plan, storage.path()).is_err());
    taskflow::ci::export(
        &g,
        vec!["test".into()],
        Path::new(".github/workflows/taskflow.yml"),
    )
    .unwrap();
    let workflow: Value = serde_yaml::from_slice(
        &std::fs::read(directory.path().join(".github/workflows/taskflow.yml")).unwrap(),
    )
    .unwrap();
    for unit in &blueprint.units {
        let step = workflow["jobs"][&unit.id]["steps"]
            .as_array()
            .unwrap()
            .iter()
            .find(|s| s["name"] == "Execute graph unit")
            .unwrap();
        let mapping = step["env"]["BUILD_AUTH"].as_str().unwrap();
        assert!(
            mapping.contains("secrets.BUILD_AUTH")
                && mapping.contains("!= 'pull_request'")
                && mapping.contains("!= 'pull_request_target'")
        );
    }
}

#[test]
fn scenario_16_five_field_cron_uses_conventional_weekdays_and_day_union() {
    let now = std::time::Instant::now();
    let period = Duration::from_secs(5);
    let mut interval = taskflow::schedule::IntervalClock::new(period, now);
    assert!(!interval.tick(now));
    assert!(interval.tick(now + period));
    assert!(interval.tick(now + period * 100));
    assert!(
        !interval.tick(now + period * 100),
        "missed ticks must not burst"
    );
    let date = |s: &str| {
        chrono::DateTime::parse_from_rfc3339(s)
            .unwrap()
            .with_timezone(&chrono::Utc)
    };
    let mut monday = taskflow::schedule::CronClock::new("0 0 1 * 1", "UTC").unwrap();
    assert!(monday.tick(date("2026-09-21T00:00:00Z")));
    assert!(!monday.tick(date("2026-09-22T00:00:00Z")));
    assert!(monday.tick(date("2026-10-01T00:00:00Z")));
    let mut weekend = taskflow::schedule::CronClock::new("0 0 * * 5-7", "UTC").unwrap();
    for day in [
        "2026-09-25T00:00:00Z",
        "2026-09-26T00:00:00Z",
        "2026-09-27T00:00:00Z",
    ] {
        assert!(weekend.tick(date(day)));
    }
    assert!(!weekend.tick(date("2026-09-28T00:00:00Z")));
    assert!(taskflow::schedule::CronClock::new("0 0 * * MON-FRI", "UTC")
        .unwrap()
        .tick(date("2026-09-21T00:00:00Z")));
}
