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
async fn cargo_target_selectors_follow_the_selected_platform() {
    let directory = fixture(json!({}));
    files::atomic_write(
        &directory.path().join("Cargo.toml"),
        b"[workspace]\nmembers=['a','b']\nresolver='2'\n",
    )
    .unwrap();
    for name in ["a", "b"] {
        let mut manifest = format!("[package]\nname='{name}'\nversion='0.1.0'\nedition='2021'\n");
        if name == "a" {
            manifest.push_str(
                "[target.'cfg(all(windows, target_arch = \
                 \"x86_64\"))'.build-dependencies]\nrenamed={package='b',path='../b'}\n",
            );
        }
        files::atomic_write(
            &directory.path().join(name).join("Cargo.toml"),
            manifest.as_bytes(),
        )
        .unwrap();
        files::atomic_write(
            &directory.path().join(name).join("src/lib.rs"),
            b"pub fn value() {}\n",
        )
        .unwrap();
        let dependencies = if name == "a" {
            json!([{"task":"build","from":"buildDependencies"}])
        } else {
            json!([])
        };
        files::atomic_write(&directory.path().join(name).join("taskflow.yml"),
            serde_yaml::to_string(&json!({"version":1,"project":name,"tasks":{"build":{"command":command(&["version"]),"dependsOn":dependencies}}})).unwrap().as_bytes()).unwrap();
    }
    taskflow::discover::output_tool(
        directory.path(),
        &["cargo", "generate-lockfile", "--offline"],
        &[],
    )
    .await
    .unwrap();
    let workspace = Workspace::discover(directory.path()).await.unwrap();
    assert!(workspace.complete(), "{:?}", workspace.coverage);
    assert!(workspace
        .edges
        .iter()
        .any(|edge| edge.condition.is_some() && edge.name == "renamed"));
    for os in [config::Os::Linux, config::Os::Macos, config::Os::Windows] {
        for arch in [config::Arch::X64, config::Arch::Arm64] {
            let g = Graph::build(workspace.clone().select_platform(Some(os), Some(arch))).unwrap();
            assert_eq!(
                g.prerequisites("a#build"),
                if os == config::Os::Windows && arch == config::Arch::X64 {
                    vec!["b#build"]
                } else {
                    vec![]
                },
                "{os:?} {arch:?}"
            );
        }
    }
    // An explicit Cargo compilation target is independent of the host running
    // Cargo and takes precedence over the execution-platform default.
    let path = directory.path().join("taskflow.yml");
    let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    config["workspace"] = json!({"cargoTarget":"x86_64-pc-windows-msvc"});
    std::fs::write(&path, serde_yaml::to_string(&config).unwrap()).unwrap();
    let g = Graph::build(
        Workspace::discover(directory.path())
            .await
            .unwrap()
            .select_platform(Some(config::Os::Linux), Some(config::Arch::Arm64)),
    )
    .unwrap();
    assert_eq!(g.prerequisites("a#build"), ["b#build"]);
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
        json!({"container":{"command":["node","-e","require('fs').mkdirSync('out',{recursive:true});require('fs').writeFileSync('out/value',process.env.MESSAGE);require('child_process').execFileSync(process.env.TFLOW_BIN,['result','unchanged'])"],"input":[],"output":["out/**"],"env":{"MESSAGE":"docker-ok"},"platform":{"os":"linux","arch":config::host_arch(),"executor":"docker","image":image}}}),
    );
    let g = graph(dir.path()).await;
    for expected_changed in [true, false] {
        let result = run(g.clone(), &["container"]).await;
        assert!(result.success, "{result:?}");
        assert_eq!(
            std::fs::read_to_string(dir.path().join("out/value")).unwrap(),
            "docker-ok"
        );
        assert_eq!(result.results["app#container"].changed, expected_changed);
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
            json!({"test":{"command":command,"input":["**/*.test.js"],"output":[],"shard":{"adapter":adapter,"count":3}}}),
        );
        std::fs::write(js.path().join("package.json"),serde_json::to_vec(&json!({"name":"taskflow-shard-fixture","private":true,"packageManager":"pnpm@10.26.2","devDependencies":{adapter:version}})).unwrap()).unwrap();
        std::fs::create_dir(js.path().join("test cases")).unwrap();
        for name in ["one", "two"] {
            std::fs::write(
                js.path().join(format!("test cases/{name}.test.js")),
                if adapter == "vitest" {
                    format!(
                        "import {{test,expect}} from 'vitest';import {{appendFileSync}} from \
                         'node:fs';test('works',()=>{{appendFileSync('executed.log','{name}\\n');\
                         expect(1).toBe(1);}});"
                    )
                } else {
                    format!(
                        "const {{appendFileSync}}=require('node:fs');test('works',\
                         ()=>{{appendFileSync('executed.log','{name}\\n');expect(1).toBe(1);}});"
                    )
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
        let output = js
            .path()
            .join(".taskflow/runs")
            .join(&result.results["app#test"].execution)
            .join("output.log");
        assert!(
            result.success,
            "{adapter}: {result:?}\n{}",
            std::fs::read_to_string(output).unwrap_or_default()
        );
        let executed = || {
            let mut names: Vec<_> = std::fs::read_to_string(js.path().join("executed.log"))
                .unwrap()
                .lines()
                .map(str::to_owned)
                .collect();
            names.sort();
            names
        };
        let sharded = executed();
        assert_eq!(
            sharded,
            ["one", "two"],
            "every selected file executes exactly once"
        );
        std::fs::remove_file(js.path().join("executed.log")).unwrap();
        taskflow::discover::output_tool(js.path(), &command, &[])
            .await
            .unwrap();
        assert_eq!(
            executed(),
            sharded,
            "sharded and unsharded inventories agree"
        );
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
    wait_lines(&dir.path().join("server.pid"), "", 1).await;
    let pid = std::fs::read_to_string(dir.path().join("server.pid")).unwrap();
    assert_eq!(
        std::fs::read_to_string(dir.path().join("trace"))
            .unwrap()
            .matches("check")
            .count(),
        1
    );
    std::fs::write(dir.path().join("source"), "after").unwrap();
    wait_lines(&dir.path().join("trace"), "check", 2).await;
    wait_lines(&dir.path().join("trace"), "poll", 1).await;
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
    // Terminal outputs have no downstream restore to validate their contents.
    let terminal = blueprint
        .units
        .iter()
        .find(|unit| unit.tasks.contains(&"app#c".into()))
        .unwrap();
    let terminal_path = storage.path().join(&terminal.id).join("bundle.json");
    let terminal_bytes = std::fs::read(&terminal_path).unwrap();
    for corruption in ["digest", "path", "output"] {
        let mut bundle: Value = serde_json::from_slice(&terminal_bytes).unwrap();
        let artifact = &mut bundle["artifacts"]["app#c"];
        match corruption {
            "digest" => {
                let file = artifact["files"]
                    .as_array_mut()
                    .unwrap()
                    .iter_mut()
                    .find(|entry| entry["content"]["type"] == "file")
                    .unwrap();
                file["content"]["digest"] = json!(files::digest(b"corrupt"));
            }
            "path" => artifact["files"][0]["path"] = json!("../outside"),
            "output" => {
                artifact["output_digest"] = json!(files::digest(b"corrupt"));
                bundle["result"]["results"]["app#c"]["output"] = json!(files::digest(b"corrupt"));
            }
            _ => unreachable!(),
        }
        std::fs::write(&terminal_path, serde_json::to_vec(&bundle).unwrap()).unwrap();
        assert!(
            taskflow::ci::aggregate(&blueprint, &plan, storage.path()).is_err(),
            "{corruption}"
        );
    }
    std::fs::write(&terminal_path, terminal_bytes).unwrap();
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
    static LOGGING: OnceLock<()> = OnceLock::new();
    LOGGING.get_or_init(|| {
        let _ = tracing_subscriber::fmt()
            .with_test_writer()
            .with_max_level(tracing::Level::INFO)
            .try_init();
    });
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
async fn cache_rejects_noncanonical_record_paths_before_restoration() {
    let directory =
        fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":["out/**"]}}));
    files::atomic_write(&directory.path().join("out/file"), b"preserved").unwrap();
    let graph = graph(directory.path()).await;
    let project = &graph.workspace.projects["app"];
    let task = &graph.tasks["app#build"].task;
    let original =
        cache::Artifact::capture(files::digest(b"key"), "app#build".into(), project, task).unwrap();
    original.validate_integrity(&original.key).unwrap();
    for path in [
        "out/./file",
        "out//file",
        "out/file/",
        "./out/file",
        "out\\file",
        "out/file\0",
        "C:out/file",
    ] {
        for duplicate in [false, true] {
            let mut artifact = original.clone();
            let mut record = artifact
                .files
                .iter()
                .find(|entry| entry.path == "out/file")
                .unwrap()
                .clone();
            record.path = path.into();
            if !duplicate {
                artifact.files.retain(|entry| entry.path != "out/file");
            }
            artifact.files.push(record);
            artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
            assert!(
                artifact.validate_integrity(&artifact.key).is_err(),
                "{path:?}"
            );
            assert!(
                artifact
                    .restore(&artifact.key, "app#build", project, task)
                    .is_err(),
                "{path:?}"
            );
            assert_eq!(
                std::fs::read(directory.path().join("out/file")).unwrap(),
                b"preserved"
            );
        }
    }
    let mut duplicate = original.clone();
    duplicate.files.push(original.files.last().unwrap().clone());
    duplicate.output_digest = cache::output_digest(&duplicate.files).unwrap();
    assert!(duplicate.validate_integrity(&duplicate.key).is_err());
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
    let result = runner::run_plan(
        g.clone(),
        plan,
        RunOptions::default(),
        CancellationToken::new(),
    )
    .await
    .unwrap();
    assert_eq!(result.results["app#deploy"].outcome, Outcome::Executed);
    // Session propagation seeds a dependent with no independent input cause.
    for reason in [
        Cause::Input {
            path: "source".into(),
        },
        Cause::Schedule,
    ] {
        let plan = Plan::for_tasks(
            &g,
            BTreeMap::from([
                ("app#build".into(), BTreeSet::from([reason])),
                ("app#deploy".into(), BTreeSet::new()),
            ]),
        )
        .unwrap();
        assert!(plan.causes["app#deploy"].contains(&Cause::ExternalEffect));
        let result = runner::run_plan(
            g.clone(),
            plan,
            RunOptions::default(),
            CancellationToken::new(),
        )
        .await
        .unwrap();
        assert_eq!(result.results["app#deploy"].outcome, Outcome::Executed);
    }
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
async fn unchanged_prerequisites_cannot_suppress_corrupt_outputs() {
    for cached in [false, true] {
        let directory = fixture(json!({
            "a": {"command":command(&["unchanged","events","a"]),"input":[]},
            "b": {"command":command(&["copy","source","middle"]),"dependsOn":["a"],
                "input":["source"],"output":["middle"],"cache":cached,"tools":{"fixture":command(&["version"])}},
            "c": {"command":command(&["copy","middle","consumed"]),"dependsOn":["b"],"input":["middle"],"output":["consumed"]}
        }));
        std::fs::write(directory.path().join("source"), "correct").unwrap();
        let g = graph(directory.path()).await;
        assert!(run(g.clone(), &["c"]).await.success);
        std::fs::write(directory.path().join("middle"), "corrupt").unwrap();
        let result = run(g, &["c"]).await;
        assert!(result.success, "{result:?}");
        assert_eq!(
            result.results["app#b"].outcome,
            if cached {
                Outcome::Restored
            } else {
                Outcome::Executed
            }
        );
        assert_eq!(
            std::fs::read_to_string(directory.path().join("consumed")).unwrap(),
            "correct"
        );
    }
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

#[test]
fn shard_preflight_preserves_explicit_metadata_bootstrap() {
    let directory = fixture(json!({
        "install":{"command":["cargo","generate-lockfile","--offline"],"install":true},
        "test":{"command":command(&["version"]),"dependsOn":["install",{"task":"test","from":"dependencies"}],
            "shard":{"adapter":"generic","count":4,"list":command(&["inventory"]),"run":command(&["shard"])}}
    }));
    files::atomic_write(
        &directory.path().join("Cargo.toml"),
        b"[package]\nname='bootstrap-fixture'\nversion='0.1.0'\nedition='2021'\n",
    )
    .unwrap();
    files::atomic_write(&directory.path().join("src/lib.rs"), b"pub fn value() {}\n").unwrap();
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .arg("--root")
        .arg(directory.path())
        .args(["run", "test", "--shard", "0/4"])
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
    assert!(directory.path().join("Cargo.lock").exists());
}

#[test]
fn invalid_shard_selection_cannot_execute_prerequisites() {
    for shard in [
        Value::Null,
        json!({"adapter":"generic","count":2,
        "list":command(&["inventory"]),"run":command(&["shard"])}),
    ] {
        let directory = fixture(json!({
            "prepare":{"command":command(&["write","started","unexpected"])},
            "test":{"command":command(&["version"]),"dependsOn":["prepare"],"shard":shard}
        }));
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .arg("--root")
            .arg(directory.path())
            .args(["run", "test", "--shard", "0/4"])
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(String::from_utf8_lossy(&output.stderr).contains("--shard"));
        assert!(!directory.path().join("started").exists());
    }
}

#[test]
fn standalone_head_never_becomes_a_direct_run() {
    let directory = fixture(json!({"deploy": {
        "command": command(&["write", "deployed", "unexpected"]), "effect":"external"
    }}));
    for flags in [
        vec!["--head", "HEAD"],
        vec!["--affected", "--head", "HEAD", "--changed", "source"],
    ] {
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .arg("--root")
            .arg(directory.path())
            .args(["run", "deploy"])
            .args(flags)
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(String::from_utf8_lossy(&output.stderr).contains("--head"));
        assert!(!directory.path().join("deployed").exists());
    }
}

#[test]
fn check_rejects_invalid_readiness_before_starting_processes() {
    for readiness in [
        json!({"type":"command", "command":[], "timeout":"5s"}),
        json!({"type":"command", "command":"", "timeout":"5s"}),
        json!({"type":"command", "command":["echo"], "timeout":"invalid"}),
        json!({"type":"tcp", "address":"127.0.0.1:1234", "timeout":"invalid"}),
        json!({"type":"http", "url":"http://127.0.0.1:1234", "timeout":"invalid"}),
        json!({"type":"tcp", "address":"", "timeout":"5s"}),
        json!({"type":"tcp", "address":"localhost", "timeout":"5s"}),
        json!({"type":"tcp", "address":"localhost:port", "timeout":"5s"}),
        json!({"type":"tcp", "address":"localhost:0", "timeout":"5s"}),
        json!({"type":"tcp", "address":"localhost:65536", "timeout":"5s"}),
        json!({"type":"tcp", "address":"localhost/path:80", "timeout":"5s"}),
        json!({"type":"tcp", "address":"localhost:80?x:80", "timeout":"5s"}),
        json!({"type":"tcp", "address":"[::1:80", "timeout":"5s"}),
        json!({"type":"tcp", "address":" localhost:80", "timeout":"5s"}),
        json!({"type":"http", "url":"", "timeout":"5s"}),
        json!({"type":"http", "url":"not a URL", "timeout":"5s"}),
        json!({"type":"http", "url":"http://", "timeout":"5s"}),
        json!({"type":"http", "url":"http://localhost:invalid", "timeout":"5s"}),
        json!({"type":"http", "url":"file:///health", "timeout":"5s"}),
        json!({"type":"http", "url":"ftp://localhost/health", "timeout":"5s"}),
    ] {
        let directory = fixture(json!({"server": {
            "command": command(&["write", "started", "unexpected"]),
            "service": true, "readiness": readiness
        }}));
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .arg("--root")
            .arg(directory.path())
            .arg("check")
            .output()
            .unwrap();
        assert!(!output.status.success(), "{readiness}");
        assert!(!directory.path().join("started").exists());
        assert!(!String::from_utf8_lossy(&output.stderr).contains("panicked"));
    }
}
#[test]
fn readiness_endpoint_validation_does_not_require_a_running_service() {
    for readiness in [
        json!({"type":"tcp","address":"localhost:80","timeout":"5s"}),
        json!({"type":"tcp","address":"127.0.0.1:65535","timeout":"5s"}),
        json!({"type":"tcp","address":"[::1]:443","timeout":"5s"}),
        json!({"type":"tcp","address":"offline.invalid:1234","timeout":"5s"}),
        json!({"type":"http","url":"http://localhost/health","timeout":"5s"}),
        json!({"type":"http","url":"https://[::1]:443/health?ready=true","timeout":"5s"}),
    ] {
        let task: config::Task = serde_json::from_value(
            json!({"command":["unused"],"service":true,"readiness":readiness}),
        )
        .unwrap();
        task.validate().unwrap();
    }
}
async fn wait_lines(path: &Path, prefix: &str, count: usize) {
    // CI runs native compilers and test runners alongside these sessions. Wait
    // for observable progress instead of assuming workstation startup timings.
    // Process cancellation/reaping assertions retain their separate short bound.
    let deadline = tokio::time::Instant::now() + Duration::from_secs(60);
    loop {
        let records = std::fs::read_to_string(path).unwrap_or_default();
        if records.lines().filter(|l| l.starts_with(prefix)).count() >= count {
            return;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "missing {count} {prefix} records in {}; observed {records:?}",
            path.display()
        );
        tokio::time::sleep(Duration::from_millis(15)).await;
    }
}

#[tokio::test]
async fn session_normalizes_watch_paths_for_existing_and_deleted_inputs() {
    let directory = fixture(json!({"check": {
        "command": command(&["record", "events", "run"]),
        "input": ["source"], "watch": {"debounce": "20ms"}
    }}));
    std::fs::write(directory.path().join("source"), "initial").unwrap();
    std::fs::create_dir(directory.path().join("nested")).unwrap();
    profile(directory.path(), &["check"]);
    // Keep a lexical alias at the API boundary. On Windows TempDir's ordinary
    // drive path also differs from discovery's verbatim canonical root.
    let root = directory.path().join("nested").join("..");
    let token = CancellationToken::new();
    let stop = token.clone();
    let session = tokio::spawn(async move {
        taskflow::session::start(&root, "default", RunOptions::default(), stop).await
    });
    let events = directory.path().join("events");
    wait_lines(&events, "run", 1).await;
    std::fs::remove_file(directory.path().join("source")).unwrap();
    wait_lines(&events, "run", 2).await;
    std::fs::write(directory.path().join("source"), "recreated").unwrap();
    wait_lines(&events, "run", 3).await;
    token.cancel();
    session.await.unwrap().unwrap();
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
    for (overlap, expected_starts) in [("queue", 2), ("skip", 1), ("restart", 3)] {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap().to_string();
        drop(listener);
        let directory = fixture(json!({
            "check":{"command":command(&["gated-paced","events",&address,"release"]),"input":["source"],"watch":{"debounce":"20ms"},"overlap":overlap},
            "observe":{"command":command(&["copy","source","observed"]),"input":["source"],"output":["observed"],"watch":{"initial":false,"debounce":"20ms"}}
        }));
        std::fs::write(directory.path().join("source"), "initial").unwrap();
        profile(directory.path(), &["check", "observe"]);
        let root = directory.path().to_path_buf();
        let token = CancellationToken::new();
        let stop = token.clone();
        let session = tokio::spawn(async move {
            taskflow::session::start(
                &root,
                "default",
                RunOptions {
                    quiet: true,
                    jobs: 2,
                    ..Default::default()
                },
                stop,
            )
            .await
        });
        let events = directory.path().join("events");
        wait_lines(&events, "start", 1).await;
        // Keep the process alive until an independent subscriber confirms the
        // session has processed each edit. FSEvents may deliver mutations after
        // a fixed-duration process exits, which would no longer test overlap.
        for (index, change) in ["first", "second"].into_iter().enumerate() {
            std::fs::write(directory.path().join("source"), change).unwrap();
            tokio::time::timeout(Duration::from_secs(15), async {
                while std::fs::read_to_string(directory.path().join("observed"))
                    .ok()
                    .as_deref()
                    != Some(change)
                {
                    assert!(
                        !session.is_finished(),
                        "session failed before observing mutation"
                    );
                    tokio::time::sleep(Duration::from_millis(10)).await;
                }
            })
            .await
            .unwrap();
            if overlap == "restart" {
                wait_lines(&events, "start", index + 2).await;
            }
        }
        std::fs::write(directory.path().join("release"), "release").unwrap();
        wait_lines(&events, "start", expected_starts).await;
        wait_lines(&events, "end", if overlap == "queue" { 2 } else { 1 }).await;
        tokio::time::sleep(Duration::from_millis(100)).await;
        token.cancel();
        session.await.unwrap().unwrap();
        let records = std::fs::read_to_string(events).unwrap();
        let starts = records.lines().filter(|l| l.starts_with("start")).count();
        assert_eq!(starts, expected_starts, "{overlap}: {records}");
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
        "server":{"command":command(&["sleep","server.pid"]),"input":[],"service":true,"readiness":{"type":"command","command":command(&["fail-after-files","server.pid","check.pid"]),"timeout":"15s"}},
        "check":{"command":command(&["sleep","check.pid"]),"input":[],"watch":{}}
    }));
    profile(directory.path(), &["server", "check"]);
    let result = tokio::time::timeout(
        Duration::from_secs(20),
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
    assert!(result.is_err(), "readiness must fail: {result:?}");
    #[cfg(unix)]
    for name in ["server.pid", "check.pid"] {
        let pid: i32 = std::fs::read_to_string(directory.path().join(name))
            .unwrap_or_else(|error| panic!("{name}: {error}; session returned {result:?}"))
            .parse()
            .unwrap();
        assert!(
            nix::sys::signal::kill(nix::unistd::Pid::from_raw(pid), None).is_err(),
            "owned process survived session failure"
        );
    }
}

#[tokio::test]
#[cfg(unix)]
async fn cache_rejects_links_through_external_ancestors_before_mutation() {
    let directory = fixture(json!({"build":{"command":command(&["version"]),"output":["out/**"]}}));
    let outside = tempfile::tempdir().unwrap();
    std::fs::write(outside.path().join("file"), "external").unwrap();
    std::fs::create_dir(directory.path().join("out")).unwrap();
    std::fs::write(directory.path().join("out/keep"), "preserved").unwrap();
    std::os::unix::fs::symlink(outside.path(), directory.path().join("shared")).unwrap();
    let g = graph(directory.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    let mut artifact =
        cache::Artifact::capture(files::digest(b"links"), "app#build".into(), project, task)
            .unwrap();
    for target in ["../shared/file", "../shared/../file", "alias/file", "cycle"] {
        artifact.files = vec![
            cache::FileRecord {
                path: "out".into(),
                content: cache::Content::Directory,
            },
            cache::FileRecord {
                path: "out/result".into(),
                content: cache::Content::Link {
                    directory: false,
                    target: target.into(),
                },
            },
        ];
        if target == "alias/file" {
            artifact.files.push(cache::FileRecord {
                path: "out/alias".into(),
                content: cache::Content::Link {
                    directory: false,
                    target: "../shared".into(),
                },
            });
        } else if target == "cycle" {
            artifact.files.push(cache::FileRecord {
                path: "out/cycle".into(),
                content: cache::Content::Link {
                    directory: false,
                    target: "cycle".into(),
                },
            });
        }
        artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
        assert!(
            artifact
                .restore(&artifact.key, "app#build", project, task)
                .is_err(),
            "{target}"
        );
        assert_eq!(
            std::fs::read_to_string(directory.path().join("out/keep")).unwrap(),
            "preserved"
        );
    }
    std::fs::remove_file(directory.path().join("shared")).unwrap();
    std::fs::create_dir(directory.path().join("inside")).unwrap();
    std::fs::write(directory.path().join("inside/file"), "internal").unwrap();
    std::os::unix::fs::symlink("inside", directory.path().join("shared")).unwrap();
    artifact.files = vec![
        cache::FileRecord {
            path: "out".into(),
            content: cache::Content::Directory,
        },
        cache::FileRecord {
            path: "out/alias".into(),
            content: cache::Content::Link {
                directory: false,
                target: "../shared".into(),
            },
        },
        cache::FileRecord {
            path: "out/result".into(),
            content: cache::Content::Link {
                directory: false,
                target: "alias/file".into(),
            },
        },
    ];
    artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
    artifact
        .restore(&artifact.key, "app#build", project, task)
        .unwrap();
    assert_eq!(
        std::fs::read_to_string(directory.path().join("out/result")).unwrap(),
        "internal"
    );
}

#[tokio::test]
async fn cache_clean_serializes_with_readers_and_writers() {
    let directory = fixture(json!({"build":{"command":command(&["version"]),"output":["output"]}}));
    std::fs::write(directory.path().join("output"), "content").unwrap();
    let g = graph(directory.path()).await;
    let key = files::digest(b"cache-lock-fixture");
    let artifact = cache::Artifact::capture(
        key.clone(),
        "app#build".into(),
        &g.workspace.projects["app"],
        &g.tasks["app#build"].task,
    )
    .unwrap();
    cache::store(directory.path(), &artifact).unwrap();
    let guard = cache::read_lock(directory.path()).unwrap();
    let mut child = tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .kill_on_drop(true)
        .arg("--root")
        .arg(directory.path())
        .args(["cache", "clean"])
        .stdout(std::process::Stdio::null())
        .spawn()
        .unwrap();
    assert!(
        tokio::time::timeout(Duration::from_millis(200), child.wait())
            .await
            .is_err()
    );
    assert!(cache::entry_path(directory.path(), &key).exists());
    drop(guard);
    assert!(tokio::time::timeout(Duration::from_secs(5), child.wait())
        .await
        .unwrap()
        .unwrap()
        .success());
    let barrier = std::sync::Barrier::new(4);
    std::thread::scope(|scope| {
        for worker in 0..4 {
            let (directory, artifact, key, barrier) = (directory.path(), &artifact, &key, &barrier);
            scope.spawn(move || {
                barrier.wait();
                for _ in 0..50 {
                    match worker {
                        0 => cache::clean(directory).unwrap(),
                        1 => {
                            cache::load(directory, key).unwrap();
                        }
                        _ => {
                            cache::store(directory, artifact).unwrap();
                        }
                    }
                }
            });
        }
    });
    cache::store(directory.path(), &artifact).unwrap();
    assert!(cache::load(directory.path(), &key).unwrap().is_some());
}

#[tokio::test]
async fn remote_lookup_input_change_preserves_current_outputs() {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let directory = fixture(json!({"build": {
        "command": command(&["copy", "source", "output"]), "input":["source"], "output":["output"],
        "cache":true, "tools":{"fixture":command(&["version"])}
    }}));
    let path = directory.path().join("taskflow.yml");
    let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    config["remote"] = json!({"endpoint":format!("http://{}",listener.local_addr().unwrap()),"bucket":"fixture","namespace":"fixture","accessKeyEnv":"TFLOW_TEST_ACCESS","secretKeyEnv":"TFLOW_TEST_PRIVATE","mode":"read-only"});
    std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
    std::fs::write(directory.path().join("source"), "original").unwrap();
    let g = graph(directory.path()).await;
    let plan = Plan::create(&g, &["build".into()], &[], false).unwrap();
    let seeded = runner::run_plan(
        g,
        plan,
        RunOptions {
            force: true,
            ..Default::default()
        },
        CancellationToken::new(),
    )
    .await
    .unwrap();
    assert!(seeded.success);
    let artifact = cache::load(directory.path(), &seeded.results["app#build"].key)
        .unwrap()
        .unwrap();
    let bytes = cache::encode(&artifact).unwrap();
    let manifest =
        serde_json::to_vec(&json!({"version":1,"object":files::digest(&bytes)})).unwrap();
    cache::remove_path(&directory.path().join(".taskflow/cache")).unwrap();
    std::fs::write(directory.path().join("output"), "preserve-current-output").unwrap();
    let root = directory.path().to_path_buf();
    let server = tokio::spawn(async move {
        for (index, body) in [manifest, bytes].into_iter().enumerate() {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = Vec::new();
            while !request.windows(4).any(|part| part == b"\r\n\r\n") {
                assert!(stream.read_buf(&mut request).await.unwrap() > 0);
            }
            if index == 1 {
                // The snapshot and key are already fixed, but the cached object
                // has not arrived. Change the input at this deterministic barrier.
                std::fs::write(root.join("source"), "changed-during-lookup").unwrap();
            }
            let headers = format!(
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            stream.write_all(headers.as_bytes()).await.unwrap();
            stream.write_all(&body).await.unwrap();
        }
    });
    let output = tokio::time::timeout(
        Duration::from_secs(30),
        tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .kill_on_drop(true)
            .arg("--root")
            .arg(directory.path())
            .args(["run", "build", "--quiet"])
            .env_remove("GITHUB_EVENT_NAME")
            .env_remove("TFLOW_UNTRUSTED_CI")
            .env("TFLOW_TEST_ACCESS", "fixture")
            .env("TFLOW_TEST_PRIVATE", "fixture")
            .output(),
    )
    .await
    .unwrap()
    .unwrap();
    server.await.unwrap();
    assert!(
        !output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stdout)
    );
    assert_eq!(
        std::fs::read_to_string(directory.path().join("output")).unwrap(),
        "preserve-current-output"
    );
    assert!(!cache::entry_path(directory.path(), &artifact.key).exists());
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
    let mut stepped = taskflow::schedule::CronClock::new("0 0 */2 * MON", "UTC").unwrap();
    assert!(!stepped.tick(date("2026-09-22T00:00:00Z")));
    assert!(stepped.tick(date("2026-09-23T00:00:00Z")));
    assert!(stepped.tick(date("2026-09-28T00:00:00Z")));
    let mut stepped_weekday = taskflow::schedule::CronClock::new("0 0 1 * */2", "UTC").unwrap();
    assert!(stepped_weekday.tick(date("2026-09-22T00:00:00Z")));
    assert!(!stepped_weekday.tick(date("2026-09-23T00:00:00Z")));
    assert!(stepped_weekday.tick(date("2026-10-01T00:00:00Z")));
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

#[tokio::test]
async fn grouped_tasks_only_receive_their_declared_secrets() {
    let directory = fixture(json!({
        "owner":{"input":[],"command":command(&["env","TFLOW_GROUP_SECRET","owner-value"]),"secrets":["TFLOW_GROUP_SECRET"]},
        "sibling":{"input":[],"command":command(&["env","TFLOW_GROUP_SECRET","sibling-value"]),"dependsOn":["owner"]}
    }));
    let result = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .current_dir(directory.path())
        .args(["--json", "run", "sibling"])
        .env(
            if cfg!(windows) {
                "Tflow_Group_Secret"
            } else {
                "TFLOW_GROUP_SECRET"
            },
            "grouped-credential",
        )
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(
        std::fs::read_to_string(directory.path().join("owner-value")).unwrap(),
        "grouped-credential"
    );
    assert_eq!(
        std::fs::read_to_string(directory.path().join("sibling-value")).unwrap(),
        ""
    );
    let result: runner::RunResult = serde_json::from_slice(&result.stdout).unwrap();
    for receipt in result.results.values() {
        let log = std::fs::read_to_string(
            directory
                .path()
                .join(".taskflow/runs")
                .join(&receipt.execution)
                .join("output.log"),
        )
        .unwrap();
        assert!(!log.contains("grouped-credential"));
        if receipt.task == "app#owner" {
            assert_eq!(log, "[REDACTED]");
        }
    }
}

#[tokio::test]
async fn libtest_ids_distinguish_workspace_packages() {
    let directory = fixture(
        json!({"test":{"command":["cargo","test","--workspace","--tests","--offline"],"input":[],"output":[],"shard":{"adapter":"libtest","count":2}}}),
    );
    files::atomic_write(
        &directory.path().join("Cargo.toml"),
        b"[workspace]\nmembers=['alpha','beta']\nresolver='2'\n",
    )
    .unwrap();
    for name in ["alpha", "beta"] {
        files::atomic_write(
            &directory.path().join(name).join("Cargo.toml"),
            format!("[package]\nname='{name}'\nversion='0.1.0'\nedition='2021'\n").as_bytes(),
        )
        .unwrap();
        files::atomic_write(
            &directory.path().join(name).join("tests/shared.rs"),
            b"#[test] fn same_name() {}\n",
        )
        .unwrap();
    }
    taskflow::discover::output_tool(
        directory.path(),
        &["cargo", "generate-lockfile", "--offline"],
        &[],
    )
    .await
    .unwrap();
    let result = run(graph(directory.path()).await, &["test"]).await;
    assert!(result.success, "{result:?}");
    let (inventory, reports) = shard::read_reports(
        &directory
            .path()
            .join(".taskflow/runs")
            .join(&result.results["app#test"].execution),
    )
    .unwrap();
    assert_eq!(inventory.tests.len(), 2);
    assert!(inventory
        .tests
        .iter()
        .any(|test| test.id == "alpha@0.1.0:test:shared::same_name"));
    assert!(inventory
        .tests
        .iter()
        .any(|test| test.id == "beta@0.1.0:test:shared::same_name"));
    assert!(shard::aggregate(&inventory, 2, &reports).unwrap());
}

#[tokio::test]
async fn cache_restores_directory_links_outside_output_roots() {
    let directory =
        fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":["out/**"]}}));
    std::fs::create_dir_all(directory.path().join("shared")).unwrap();
    std::fs::write(directory.path().join("shared/value"), "retained").unwrap();
    std::fs::create_dir_all(directory.path().join("out")).unwrap();
    #[cfg(unix)]
    std::os::unix::fs::symlink("../shared", directory.path().join("out/link")).unwrap();
    #[cfg(windows)]
    std::os::windows::fs::symlink_dir(
        Path::new("..").join("shared"),
        directory.path().join("out/link"),
    )
    .unwrap();
    let g = graph(directory.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    let artifact =
        cache::Artifact::capture("key".into(), "app#build".into(), project, task).unwrap();
    assert!(artifact.files.iter().any(|entry| matches!(
        &entry.content,
        cache::Content::Link {
            target,
            directory: true,
        } if target == "../shared"
    )));
    std::fs::remove_dir_all(directory.path().join("out")).unwrap();
    artifact.restore("key", "app#build", project, task).unwrap();
    assert_eq!(
        std::fs::read_link(directory.path().join("out/link")).unwrap(),
        Path::new("..").join("shared")
    );
    assert_eq!(
        std::fs::read_to_string(directory.path().join("out/link/value")).unwrap(),
        "retained"
    );
    assert_eq!(
        cache::output_state(project, task).unwrap(),
        artifact.output_digest
    );
}

#[tokio::test]
async fn unrelated_native_metadata_does_not_block_resolved_selectors() {
    let directory = fixture(json!({}));
    let path = directory.path().join("taskflow.yml");
    let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    config["workspace"] = json!({"manifests":["complete/Cargo.toml","incomplete/Cargo.toml"]});
    std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
    files::atomic_write(
        &directory.path().join("complete/Cargo.toml"),
        b"[workspace]\nmembers=['a','b']\nresolver='2'\n",
    )
    .unwrap();
    for (path, name) in [
        ("complete/a", "a"),
        ("complete/b", "b"),
        ("incomplete", "c"),
    ] {
        let manifest = format!(
            "[package]\nname='{name}'\nversion='0.1.0'\nedition='2021'\n{}",
            if name == "a" {
                "[dependencies]\nb={path='../b'}\n"
            } else {
                ""
            }
        );
        files::atomic_write(
            &directory.path().join(path).join("Cargo.toml"),
            manifest.as_bytes(),
        )
        .unwrap();
        files::atomic_write(
            &directory.path().join(path).join("src/lib.rs"),
            b"pub fn sample() {}\n",
        )
        .unwrap();
        files::atomic_write(&directory.path().join(path).join("taskflow.yml"), serde_yaml::to_string(&json!({"version":1,"project":name,"tasks":{"build":{"input":[],"command":command(&["version"]),"dependsOn":[{"task":"build","from":"dependencies"}]}}})).unwrap().as_bytes()).unwrap();
    }
    taskflow::discover::output_tool(
        &directory.path().join("complete"),
        &["cargo", "generate-lockfile", "--offline"],
        &[],
    )
    .await
    .unwrap();
    let g = graph(directory.path()).await;
    assert!(!g.workspace.complete());
    assert_eq!(g.unresolved, BTreeSet::from(["c#build".into()]));
    assert_eq!(g.prerequisites("a#build"), ["b#build"]);
    assert!(run(g, &["a#build"]).await.success);
}

#[tokio::test]
async fn cargo_ci_blueprints_are_independent_of_checkout_paths() {
    let mut blueprints = vec![];
    for _ in 0..2 {
        let directory =
            fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":[]}}));
        let path = directory.path().join("taskflow.yml");
        let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        let (os, arch) = config::Platform::default().resolved();
        config["tasks"]["build"]["platform"] = json!({"os":os,"arch":arch});
        config["ci"] = json!({"revision":"1111111111111111111111111111111111111111","rust":"1.93.0","runners":{config::Platform::default().key():"self-hosted"}});
        std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
        files::atomic_write(
            &directory.path().join("Cargo.toml"),
            b"[workspace]\nmembers=['a','b']\nresolver='2'\n",
        )
        .unwrap();
        for name in ["a", "b"] {
            let manifest = format!(
                "[package]\nname='{name}'\nversion='0.1.0'\nedition='2021'\n{}",
                if name == "a" {
                    "[dependencies]\nb={path='../b'}\n"
                } else {
                    ""
                }
            );
            files::atomic_write(
                &directory.path().join(name).join("Cargo.toml"),
                manifest.as_bytes(),
            )
            .unwrap();
            files::atomic_write(
                &directory.path().join(name).join("src/lib.rs"),
                b"pub fn sample() {}\n",
            )
            .unwrap();
        }
        taskflow::discover::output_tool(
            directory.path(),
            &["cargo", "generate-lockfile", "--offline"],
            &[],
        )
        .await
        .unwrap();
        let g = graph(directory.path()).await;
        let blueprint = taskflow::ci::Blueprint::new(&g, vec!["build".into()]).unwrap();
        assert_eq!(
            blueprint.edges.iter().next().unwrap().resolved,
            "project:path:b"
        );
        let json = serde_json::to_string(&blueprint).unwrap();
        assert!(!json.contains("path+file:"));
        assert!(!json.contains(directory.path().to_str().unwrap()));
        blueprints.push(json);
    }
    assert_eq!(blueprints[0], blueprints[1]);
}

#[tokio::test]
async fn service_timeout_shuts_down_and_reaps_the_session() {
    for ready in [true, false] {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let unavailable = listener.local_addr().unwrap().to_string();
        drop(listener);
        let readiness = if ready {
            json!({"type":"command","command":command(&["version"]),"timeout":"15s"})
        } else {
            json!({"type":"tcp","address":unavailable,"timeout":"15s"})
        };
        let directory = fixture(json!({
            "server":{"command":command(&["sleep","server-pid"]),"input":[],"service":true,"timeout":"2s","readiness":readiness},
            "check":{"command":command(&["sleep","check-pid"]),"input":[]}
        }));
        profile(directory.path(), &["server", "check"]);
        let result = tokio::time::timeout(
            Duration::from_secs(10),
            taskflow::session::start(
                directory.path(),
                "default",
                RunOptions {
                    jobs: 2,
                    ..RunOptions::default()
                },
                CancellationToken::new(),
            ),
        )
        .await
        .expect("service timeout must stop the session");
        assert!(result.is_err());
        for name in ["server-pid", "check-pid"] {
            let pid: u32 = std::fs::read_to_string(directory.path().join(name))
                .unwrap()
                .parse()
                .unwrap();
            assert!(!pid_alive(pid), "{name} survived service timeout");
        }
    }
}

fn pid_alive(pid: u32) -> bool {
    #[cfg(unix)]
    {
        nix::sys::signal::kill(nix::unistd::Pid::from_raw(pid as i32), None).is_ok()
    }
    #[cfg(windows)]
    unsafe {
        use windows_sys::Win32::{
            Foundation::CloseHandle,
            System::Threading::{
                GetExitCodeProcess, OpenProcess, PROCESS_QUERY_LIMITED_INFORMATION,
            },
        };
        let handle = OpenProcess(PROCESS_QUERY_LIMITED_INFORMATION, 0, pid);
        if handle.is_null() {
            return false;
        }
        let mut code = 0;
        let live = GetExitCodeProcess(handle, &mut code) != 0 && code == 259;
        CloseHandle(handle);
        live
    }
}

#[cfg(unix)]
#[tokio::test]
async fn input_permission_changes_invalidate_cached_success() {
    use std::os::unix::fs::PermissionsExt;
    for linked in [false, true] {
        let input = if linked { "launcher" } else { "script" };
        let directory = fixture(
            json!({"build":{"command":[format!("./{input}")],"input":[input],"output":["out"],"cache":true,"tools":{"fixture":command(&["version"])}}}),
        );
        let script = directory.path().join("script");
        std::fs::write(&script, "#!/bin/sh\nprintf built > out\n").unwrap();
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o755)).unwrap();
        if linked {
            std::os::unix::fs::symlink("script", directory.path().join("launcher")).unwrap();
        }
        let g = graph(directory.path()).await;
        assert!(run(g.clone(), &["build"]).await.success);
        assert_eq!(
            run(g.clone(), &["build"]).await.results["app#build"].outcome,
            Outcome::LocalCache
        );
        std::fs::set_permissions(&script, std::fs::Permissions::from_mode(0o644)).unwrap();
        let result = run(g, &["build"]).await;
        assert!(!result.success, "{result:?}");
        assert_eq!(result.results["app#build"].outcome, Outcome::Failed);
    }
}

#[tokio::test]
async fn unchanged_metadata_notifications_do_not_cancel_live_work() {
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let address = listener.local_addr().unwrap().to_string();
    drop(listener);
    let directory = fixture(
        json!({"check":{"command":command(&["paced","events",&address,"700"]),"input":[],"watch":{},"overlap":"queue"}}),
    );
    profile(directory.path(), &["check"]);
    let config = directory.path().join("taskflow.yml");
    let original = std::fs::read(&config).unwrap();
    let root = directory.path().to_path_buf();
    let token = CancellationToken::new();
    let stop = token.clone();
    let session = tokio::spawn(async move {
        taskflow::session::start(&root, "default", RunOptions::default(), stop).await
    });
    let events = directory.path().join("events");
    wait_lines(&events, "start", 1).await;
    for _ in 0..4 {
        files::atomic_write(&config, &original).unwrap();
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    wait_lines(&events, "end", 1).await;
    tokio::time::timeout(Duration::from_secs(10), async {
        while !runner::previous(directory.path(), "app#check")
            .is_some_and(|receipt| receipt.success())
        {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
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

#[test]
fn go_shard_flags_fail_closed_for_unknown_and_selection_options() {
    for flag in [
        "-unknown",
        "-unknown=value",
        "-run=TestA",
        "-test.run=TestA",
        "-coverprofile=out",
        "-c",
        "--",
    ] {
        let task: config::Task = serde_json::from_value(
            json!({"command":["go","test",flag],"shard":{"adapter":"go","count":2}}),
        )
        .unwrap();
        assert!(shard::validate_task(&task).is_err(), "{flag}");
    }
    let task: config::Task = serde_json::from_value(
        json!({"command":["go","test","-coverpkg"],"shard":{"adapter":"go","count":2}}),
    )
    .unwrap();
    assert!(shard::validate_task(&task).is_err());
}

#[tokio::test]
#[ignore = "requires Go"]
async fn go_sharding_preserves_separated_flag_values_and_package_failures() {
    let directory = fixture(
        json!({"test":{"command":["go","test","-coverpkg","./...","-covermode","atomic","-shuffle","1","-vet","off","./..."],"input":[],"output":[],"shard":{"adapter":"go","count":2}}}),
    );
    files::atomic_write(
        &directory.path().join("go.mod"),
        b"module example.test/flags\n\ngo 1.25.0\n",
    )
    .unwrap();
    files::atomic_write(
        &directory.path().join("root_test.go"),
        b"package flags\nimport \"testing\"\nfunc TestRoot(t *testing.T) {}\n",
    )
    .unwrap();
    files::atomic_write(&directory.path().join("leaf/leaf_test.go"), b"package leaf\nimport \"testing\"\nfunc TestFailure(t *testing.T) { t.Fatal(\"expected failure\") }\n").unwrap();
    let result = run(graph(directory.path()).await, &["test"]).await;
    assert!(
        !result.success,
        "a failing inventoried package must not become a false pass"
    );
    let (inventory, reports) = shard::read_reports(
        &directory
            .path()
            .join(".taskflow/runs")
            .join(&result.results["app#test"].execution),
    )
    .unwrap();
    assert_eq!(inventory.tests.len(), 2);
    assert!(reports
        .iter()
        .flat_map(|r| &r.results)
        .any(|r| r.id == "example.test/flags/leaf::TestFailure"
            && r.status == shard::UnitStatus::Failed));
    assert!(!shard::aggregate(&inventory, 2, &reports).unwrap());
    for flag in ["-failfast", "-failfast=false"] {
        let root = fixture(
            json!({"test":{"command":["go","test",flag],"input":[],"output":[],"shard":{"adapter":"go","count":2}}}),
        );
        files::atomic_write(
            &root.path().join("go.mod"),
            b"module example.test/failfast\n\ngo 1.25.0\n",
        )
        .unwrap();
        files::atomic_write(&root.path().join("suite_test.go"), b"package suite\nimport (\"testing\"; \"os\")\nfunc TestA(t *testing.T) { t.Fatal(\"expected\") }\nfunc TestB(t *testing.T) { os.WriteFile(\"second-ran\", []byte(\"ran\"), 0600) }\n").unwrap();
        let result = run(graph(root.path()).await, &["test"]).await;
        assert!(!result.success);
        let receipt = &result.results["app#test"];
        assert_eq!(receipt.exit_code, 1);
        let (inventory, reports) =
            shard::read_reports(&root.path().join(".taskflow/runs").join(&receipt.execution))
                .unwrap();
        assert!(!shard::aggregate(&inventory, 2, &reports).unwrap());
        assert_eq!(
            root.path().join("second-ran").exists(),
            flag.ends_with("false")
        );
        assert!(reports
            .iter()
            .flat_map(|report| &report.results)
            .any(|result| result.id.ends_with("TestB")
                && result.status
                    == if flag.ends_with("false") {
                        shard::UnitStatus::Passed
                    } else {
                        shard::UnitStatus::Skipped
                    }));
    }
}

#[tokio::test]
async fn partial_output_snapshots_require_matches_and_ignore_neighbors() {
    let directory = fixture(
        json!({"build":{"command":command(&["unchanged","generated/config.json","input edit"]),"input":[],"output":["generated/*.js"]}}),
    );
    files::atomic_write(&directory.path().join("generated/config.json"), b"input").unwrap();
    let g = graph(directory.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    assert!(cache::output_state(project, task).is_err());
    assert!(!run(g.clone(), &["build"]).await.success);
    files::atomic_write(&directory.path().join("generated/bundle.js"), b"output").unwrap();
    #[cfg(unix)]
    std::os::unix::fs::symlink(
        "/unowned-missing-input",
        directory.path().join("generated/unrelated.json"),
    )
    .unwrap();
    let snapshot = cache::snapshot(project, task).unwrap();
    assert_eq!(
        snapshot
            .iter()
            .map(|entry| entry.path.as_str())
            .collect::<Vec<_>>(),
        ["generated/bundle.js"]
    );
    let first = run(g.clone(), &["build"]).await;
    assert!(first.success && first.results["app#build"].changed);
    let second = run(g.clone(), &["build"]).await;
    assert!(second.success && !second.results["app#build"].changed);
    assert_eq!(
        first.results["app#build"].output,
        second.results["app#build"].output
    );
    std::fs::write(
        directory.path().join("generated/bundle.js"),
        "changed output",
    )
    .unwrap();
    assert_ne!(
        cache::output_state(project, task).unwrap(),
        second.results["app#build"].output
    );
    let mut both = task.clone();
    both.output.as_mut().unwrap().push("generated/*.css".into());
    assert!(cache::output_state(project, &both).is_err());
    files::atomic_write(&directory.path().join("generated/style.css"), b"style").unwrap();
    assert!(cache::output_state(project, &both).is_ok());
    let mut empty_tree = task.clone();
    empty_tree.output = Some(vec!["empty/**".into()]);
    std::fs::create_dir(directory.path().join("empty")).unwrap();
    assert!(cache::output_state(project, &empty_tree).is_ok());
    if directory.path().join("EMPTY").is_dir() {
        for alias in ["EMPTY", "EMPTY/**"] {
            empty_tree.output = Some(vec![alias.into()]);
            assert!(cache::output_state(project, &empty_tree).is_ok(), "{alias}");
        }
    }
}

#[tokio::test]
async fn partial_output_globs_preserve_neighboring_inputs_and_watch_changes() {
    let directory = fixture(
        json!({"check":{"command":command(&["record","events","run"]),"input":["generated/**"],"output":["generated/*.js"],"watch":{"debounce":"20ms"}}}),
    );
    files::atomic_write(&directory.path().join("generated/config.json"), b"initial").unwrap();
    files::atomic_write(&directory.path().join("generated/output.js"), b"output").unwrap();
    profile(directory.path(), &["check"]);
    let g = graph(directory.path()).await;
    let task = &g.tasks["app#check"].task;
    let project = &g.workspace.projects["app"];
    let state = files::input_state(&g.workspace, project, task).unwrap();
    assert!(state.contains_key("generated/config.json"));
    assert!(!state.contains_key("generated/output.js"));
    let plan = Plan::create(&g, &[], &["generated/config.json".into()], true).unwrap();
    assert!(plan.causes.contains_key("app#check"));
    let mut exact = task.clone();
    exact.output = Some(vec!["generated".into()]);
    assert!(!files::input_matches(
        project,
        &exact,
        &project.directory.join("generated/config.json")
    )
    .unwrap());
    let root = directory.path().to_path_buf();
    let token = CancellationToken::new();
    let stop = token.clone();
    let session = tokio::spawn(async move {
        taskflow::session::start(&root, "default", RunOptions::default(), stop).await
    });
    let events = directory.path().join("events");
    wait_lines(&events, "run", 1).await;
    std::fs::write(directory.path().join("generated/config.json"), "changed").unwrap();
    wait_lines(&events, "run", 2).await;
    std::fs::write(
        directory.path().join("generated/output.js"),
        "changed output",
    )
    .unwrap();
    tokio::time::sleep(Duration::from_millis(300)).await;
    token.cancel();
    session.await.unwrap().unwrap();
    assert_eq!(std::fs::read_to_string(events).unwrap().lines().count(), 2);
}

#[tokio::test]
async fn generic_shard_inventory_and_execution_use_the_configured_shell() {
    let directory = fixture(
        json!({"test":{"command":command(&["version"]),"input":[],"output":[],"shell":[helper(),"shell"],"shard":{"adapter":"generic","count":2,"list":"inventory","run":"shard"}}}),
    );
    let result = run(graph(directory.path()).await, &["test"]).await;
    assert!(result.success, "{result:?}");
    let log = std::fs::read_to_string(directory.path().join("shell-events")).unwrap();
    assert_eq!(log.lines().filter(|line| *line == "inventory").count(), 1);
    assert_eq!(log.lines().filter(|line| *line == "shard").count(), 2);
    let (inventory, reports) = shard::read_reports(
        &directory
            .path()
            .join(".taskflow/runs")
            .join(&result.results["app#test"].execution),
    )
    .unwrap();
    assert_eq!(inventory.tests.len(), 3);
    assert!(shard::aggregate(&inventory, 2, &reports).unwrap());
}

#[tokio::test]
async fn cargo_member_commands_discover_the_implicit_workspace_root() {
    let directory = fixture(
        json!({"root":{"command":command(&["version"]),"input":[],"dependsOn":["a#build"]}}),
    );
    profile(directory.path(), &["root"]);
    std::fs::create_dir(directory.path().join(".git")).unwrap();
    files::atomic_write(
        &directory.path().join("Cargo.toml"),
        b"[workspace]\nmembers=['a','b']\nexclude=['standalone']\nresolver='2'\n",
    )
    .unwrap();
    for name in ["a", "b", "standalone"] {
        let manifest = format!(
            "[package]\nname='{name}'\nversion='0.1.0'\nedition='2021'\n{}",
            if name == "a" {
                "[dependencies]\nb={path='../b'}\n"
            } else {
                ""
            }
        );
        files::atomic_write(
            &directory.path().join(name).join("Cargo.toml"),
            manifest.as_bytes(),
        )
        .unwrap();
        files::atomic_write(
            &directory.path().join(name).join("src/lib.rs"),
            b"pub fn sample() {}\n",
        )
        .unwrap();
        files::atomic_write(&directory.path().join(name).join("taskflow.yml"), serde_yaml::to_string(&json!({"version":1,"project":name,"tasks":{"build":{"command":command(&["version"]),"input":[],"dependsOn":[{"task":"build","from":"dependencies"}]}}})).unwrap().as_bytes()).unwrap();
    }
    taskflow::discover::output_tool(
        directory.path(),
        &["cargo", "generate-lockfile", "--offline"],
        &[],
    )
    .await
    .unwrap();
    let member = directory.path().join("a");
    assert_eq!(
        taskflow::discover::locate_root(&member.join("src"))
            .await
            .unwrap(),
        directory.path().canonicalize().unwrap()
    );
    let result = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .current_dir(&member)
        .args(["--json", "start"])
        .output()
        .unwrap();
    assert!(
        result.status.success(),
        "{}{}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
    let result: runner::RunResult = serde_json::from_slice(&result.stdout).unwrap();
    assert!(result.results.contains_key("app#root"));
    assert!(result.results.contains_key("b#build"));
    let standalone = directory.path().join("standalone");
    assert_eq!(
        taskflow::discover::locate_root(&standalone.join("src"))
            .await
            .unwrap(),
        standalone.canonicalize().unwrap()
    );
}

#[tokio::test]
async fn completed_tasks_keep_edits_while_an_independent_wave_task_runs() {
    for overlap in ["queue", "skip", "restart"] {
        let directory = fixture(json!({
            "fast":{"command":command(&["record","events","run"]),"input":["source"],"watch":{"debounce":"20ms"},"overlap":overlap},
            "slow":{"command":command(&["gated","slow-start","release"]),"input":[]}
        }));
        profile(directory.path(), &["fast", "slow"]);
        std::fs::write(directory.path().join("source"), "initial").unwrap();
        let options = RunOptions {
            jobs: 2,
            ..RunOptions::default()
        };
        let running = options.task_cancellations.clone();
        let root = directory.path().to_path_buf();
        let token = CancellationToken::new();
        let stop = token.clone();
        let mut session =
            tokio::spawn(
                async move { taskflow::session::start(&root, "default", options, stop).await },
            );
        tokio::time::timeout(Duration::from_secs(60), async {
            loop {
                if session.is_finished() {
                    panic!(
                        "session exited before the watch barrier: {:?}",
                        (&mut session).await
                    );
                }
                if runner::previous(directory.path(), "app#fast").is_some_and(|r| r.success())
                    && directory.path().join("slow-start").exists()
                {
                    let states = running.lock().unwrap();
                    if !states.contains_key("app#fast") && states.contains_key("app#slow") {
                        break;
                    }
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        std::fs::write(directory.path().join("source"), "changed").unwrap();
        wait_lines(&directory.path().join("events"), "run", 2).await;
        assert!(running.lock().unwrap().contains_key("app#slow"));
        std::fs::write(directory.path().join("release"), "release").unwrap();
        token.cancel();
        session.await.unwrap().unwrap();
    }
}

#[test]
#[ignore = "requires Go"]
fn go_metadata_queries_do_not_contact_module_proxies() {
    use std::{
        io::{Read, Write},
        sync::atomic::{AtomicBool, AtomicUsize, Ordering},
    };
    let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
    let endpoint = format!("http://{}", listener.local_addr().unwrap());
    listener.set_nonblocking(true).unwrap();
    let stop = Arc::new(AtomicBool::new(false));
    let requests = Arc::new(AtomicUsize::new(0));
    let stopped = stop.clone();
    let received = requests.clone();
    let server = std::thread::spawn(move || {
        while !stopped.load(Ordering::Relaxed) {
            if let Ok((mut stream, _)) = listener.accept() {
                received.fetch_add(1, Ordering::Relaxed);
                stream
                    .set_read_timeout(Some(Duration::from_secs(1)))
                    .unwrap();
                let _ = stream.read(&mut [0; 4096]);
                let _ = stream.write_all(
                    b"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\nConnection: close\r\n\r\n",
                );
            } else {
                std::thread::sleep(Duration::from_millis(5));
            }
        }
    });
    let directory = fixture(json!({}));
    let modules = tempfile::tempdir().unwrap();
    files::atomic_write(
        &directory.path().join("go.mod"),
        b"module example.test/local\n\ngo 1.25.0\n\nrequire example.test/missing v1.2.3\n",
    )
    .unwrap();
    let mut outputs = vec![];
    for bypass in ["none", "*"] {
        outputs.push(
            std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(directory.path())
                .args(["--json", "check"])
                .env("GOPROXY", &endpoint)
                .env("GOMODCACHE", modules.path())
                .env("GOPRIVATE", "example.test")
                .env("GONOPROXY", bypass)
                .output()
                .unwrap(),
        );
    }
    stop.store(true, Ordering::Relaxed);
    server.join().unwrap();
    assert_eq!(
        requests.load(Ordering::Relaxed),
        0,
        "discovery must not download module metadata"
    );
    for output in outputs {
        assert!(
            output.status.success(),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        let report: Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(report["coverage"][0]["complete"], false);
    }
    assert!(!walkdir::WalkDir::new(modules.path())
        .into_iter()
        .filter_map(Result::ok)
        .any(|entry| matches!(
            entry.path().extension().and_then(|s| s.to_str()),
            Some("mod" | "zip" | "info")
        )));
}

#[cfg(unix)]
#[tokio::test]
async fn docker_service_cleanup_failure_survives_session_cancellation() {
    for setup in [false, true] {
        let directory = fixture(
            json!({"server":{"command":["unused"],"input":[],"service":true,"platform":{"executor":"docker","os":"linux","arch":config::host_arch(),"image":"fixture@sha256:0000000000000000000000000000000000000000000000000000000000000000"}}}),
        );
        profile(directory.path(), &["server"]);
        if setup {
            let path = directory.path().join("taskflow.yml");
            let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
            config["tasks"]["server"]["tools"] = json!({"probe":["unused"]});
            std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
        }
        let tools = tempfile::tempdir().unwrap();
        std::fs::hard_link(helper(), tools.path().join("docker")).unwrap();
        let paths = std::iter::once(tools.path().to_path_buf())
            .chain(std::env::split_paths(&std::env::var_os("PATH").unwrap()))
            .collect::<Vec<_>>();
        let child = tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(directory.path())
            .args(["--json", "start"])
            .env("PATH", std::env::join_paths(paths).unwrap())
            .env_remove("DOCKER_HOST")
            .env_remove("DOCKER_CONTEXT")
            .stdout(std::process::Stdio::piped())
            .stderr(std::process::Stdio::piped())
            .kill_on_drop(true)
            .spawn()
            .unwrap();
        tokio::time::timeout(Duration::from_secs(60), async {
            while !directory.path().join("docker-start").exists() {
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        nix::sys::signal::kill(
            nix::unistd::Pid::from_raw(child.id().unwrap() as i32),
            nix::sys::signal::Signal::SIGINT,
        )
        .unwrap();
        let output = tokio::time::timeout(Duration::from_secs(10), child.wait_with_output())
            .await
            .unwrap()
            .unwrap();
        assert!(!output.status.success());
        let diagnostic = String::from_utf8_lossy(&output.stderr);
        assert!(
            diagnostic.contains("could not confirm cleanup")
                || diagnostic.contains("cleanup could not be confirmed"),
            "{diagnostic}"
        );
        let pid: u32 = std::fs::read_to_string(directory.path().join("docker-start"))
            .unwrap()
            .parse()
            .unwrap();
        assert!(!pid_alive(pid));
    }
}

#[tokio::test]
async fn service_cleanup_failures_still_await_all_owners() {
    let (events, _) = tokio::sync::mpsc::unbounded_channel();
    let stop = CancellationToken::new();
    let owner_stop = stop.clone();
    let complete = Arc::new(std::sync::atomic::AtomicBool::new(false));
    let completed = complete.clone();
    let services = runner::Services {
        controls: std::sync::Mutex::new(BTreeMap::from([("second".into(), stop)])),
        events,
        joins: std::sync::Mutex::new(vec![
            tokio::spawn(async { anyhow::bail!("injected cleanup failure") }),
            tokio::spawn(async move {
                owner_stop.cancelled().await;
                completed.store(true, std::sync::atomic::Ordering::SeqCst);
                Ok(())
            }),
        ]),
    };
    assert!(services.shutdown().await.is_err());
    assert!(complete.load(std::sync::atomic::Ordering::SeqCst));
    assert!(services.controls.lock().unwrap().is_empty());
}

#[tokio::test]
async fn partial_outputs_cannot_be_exported_or_restored_as_artifacts() {
    let directory = fixture(
        json!({"build":{"command":command(&["write","generated/bundle.js","built"]),"input":["generated/config.json"],"output":["generated/*.js"]}}),
    );
    let path = directory.path().join("taskflow.yml");
    let mut cfg: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    let platform = config::Platform::default();
    cfg["ci"] = json!({"revision":"1111111111111111111111111111111111111111","rust":"nightly-2026-01-01","runners":{platform.key():"self-hosted"}});
    std::fs::write(path, serde_yaml::to_string(&cfg).unwrap()).unwrap();
    files::atomic_write(
        &directory.path().join("generated/config.json"),
        b"private input",
    )
    .unwrap();
    let g = graph(directory.path()).await;
    assert!(run(g.clone(), &["build"]).await.success);
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    assert!(cache::Artifact::capture("key".into(), "app#build".into(), project, task).is_err());
    let error = taskflow::ci::export(&g, vec!["build".into()], Path::new("ci.yml")).unwrap_err();
    assert!(
        error.to_string().contains("complete ownership"),
        "{error:#}"
    );
    assert!(!directory.path().join("ci.yml").exists());
    assert!(!directory.path().join("ci.taskflow.json").exists());
    // An old or forged complete-root artifact must not authorize partial ownership.
    let mut whole = task.clone();
    whole.output = Some(vec!["generated/**".into()]);
    let artifact =
        cache::Artifact::capture("key".into(), "app#build".into(), project, &whole).unwrap();
    std::fs::write(
        directory.path().join("generated/config.json"),
        "new private input",
    )
    .unwrap();
    assert!(artifact.restore("key", "app#build", project, task).is_err());
    assert_eq!(
        std::fs::read_to_string(directory.path().join("generated/config.json")).unwrap(),
        "new private input"
    );
}

#[tokio::test]
async fn readiness_commands_use_the_configured_shell() {
    let directory = fixture(json!({
        "server":{"command":command(&["sleep","server.pid"]),"input":[],"service":true,"shell":[helper(),"shell"],"readiness":{"type":"command","command":"version","timeout":"15s"}},
        "check":{"command":command(&["record","events","ready"]),"input":[],"dependsOn":[{"task":"server","waitFor":"ready"}]}
    }));
    profile(directory.path(), &["server", "check"]);
    let root = directory.path().to_path_buf();
    let stop = CancellationToken::new();
    let cancel = stop.clone();
    let mut session = tokio::spawn(async move {
        taskflow::session::start(&root, "default", RunOptions::default(), cancel).await
    });
    let events = directory.path().join("events");
    tokio::select! {
        result = &mut session => panic!("readiness session exited early: {result:?}"),
        _ = wait_lines(&events, "ready", 1) => {}
    }
    stop.cancel();
    session.await.unwrap().unwrap();
    assert_eq!(
        std::fs::read_to_string(directory.path().join("shell-events")).unwrap(),
        "version\n"
    );
    let pid = std::fs::read_to_string(directory.path().join("server.pid"))
        .unwrap()
        .parse()
        .unwrap();
    assert!(!pid_alive(pid));
}

#[test]
fn check_rejects_malformed_positive_and_negative_input_globs() {
    for input in [json!(["["]), json!(["!["]), json!(["{unfinished"])] {
        let directory = fixture(json!({"check":{"command":command(&["version"]),"input":input}}));
        let error = config::load(&directory.path().join("taskflow.yml")).unwrap_err();
        assert!(format!("{error:#}").contains("invalid input glob"));
        let result = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(directory.path())
            .args(["--json", "check"])
            .output()
            .unwrap();
        assert!(!result.status.success());
        let error = String::from_utf8_lossy(&result.stderr);
        assert!(error.contains("app#check"), "{error}");
    }
    let directory = fixture(
        json!({"check":{"command":command(&["version"]),"input":[{"auto":true},"**/*.rs","!generated/**"]}}),
    );
    assert!(config::load(&directory.path().join("taskflow.yml")).is_ok());
}

#[tokio::test]
async fn environment_names_follow_host_precedence_and_security_rules() {
    use taskflow::environment::Environment;
    let directory = fixture(json!({
        "owner":{"command":command(&["version"]),"env":{"TFLOW_ä_SECRET":"unicode-credential"},"secrets":["TFLOW_CASE_SECRET","TFLOW_Ä_SECRET"]},
        "sibling":{"command":command(&["version"]),"env":{"TFLOW_ä_SECRET":"unicode-credential"}},
        "cached":{"command":command(&["version"]),"input":[],"output":[],"cache":true,"envInputs":["TFLOW_CASE_MODE"],"tools":{"fixture":command(&["version"])},"env":{"tflow_CASE_mode":"task"}}
    }));
    std::fs::write(
        directory.path().join(".env"),
        "TfLow_Case_Secret=credential\nTfLow_Case_Mode=dotenv\nTfLow_Remote_Secret=transport\n",
    )
    .unwrap();
    let mut ws = Workspace::discover(directory.path()).await.unwrap();
    ws.config.remote = Some(serde_json::from_value(json!({"endpoint":"http://127.0.0.1:9000","bucket":"fixture","namespace":"fixture","accessKeyEnv":"TFLOW_REMOTE_ACCESS","secretKeyEnv":"TFLOW_REMOTE_SECRET","mode":"off"})).unwrap());
    let project = &ws.projects["app"];
    let tasks = &project.config.as_ref().unwrap().tasks;
    let build = |id: &str, overrides: &BTreeMap<String, String>| {
        Environment::build(&ws, project, &tasks[id], overrides, false).unwrap()
    };
    let owner = build("owner", &BTreeMap::new());
    let sibling = build("sibling", &BTreeMap::new());
    if cfg!(windows) {
        assert!(owner.secrets.contains(&b"credential".to_vec()));
        assert!(owner.secrets.contains(&b"unicode-credential".to_vec()));
        assert!(!sibling.values.contains_key("TfLow_Case_Secret"));
        assert!(!sibling.values.contains_key("TFLOW_ä_SECRET"));
        assert!(!owner.values.contains_key("TfLow_Remote_Secret"));
    } else {
        assert!(owner.secrets.is_empty());
        assert_eq!(sibling.values["TfLow_Case_Secret"], "credential");
        assert_eq!(owner.values["TfLow_Remote_Secret"], "transport");
    }
    let first = build("cached", &BTreeMap::new());
    assert_eq!(
        first.fingerprint["TFLOW_CASE_MODE"],
        if cfg!(windows) {
            files::digest(b"task")
        } else {
            "<missing>".into()
        }
    );
    let overrides = BTreeMap::from([("TFLOW_CASE_MODE".into(), "cli".into())]);
    let second = build("cached", &overrides);
    assert_eq!(second.fingerprint["TFLOW_CASE_MODE"], files::digest(b"cli"));
    if cfg!(windows) {
        assert_eq!(
            second
                .values
                .keys()
                .filter(|key| key.eq_ignore_ascii_case("TFLOW_CASE_MODE"))
                .count(),
            1
        );
        for name in ["PATH", "SYSTEMROOT"] {
            if let Ok(expected) = std::env::var(name) {
                assert!(second
                    .values
                    .iter()
                    .any(|(key, value)| key.eq_ignore_ascii_case(name) && value == &expected));
            }
        }
    }
    let overrides = BTreeMap::from([("tflow_remote_secret".into(), "forbidden".into())]);
    assert_eq!(
        Environment::build(&ws, project, &tasks["sibling"], &overrides, false).is_err(),
        cfg!(windows)
    );
}

#[tokio::test]
async fn setup_cancellation_preserves_receipts_and_service_events() {
    for service in [false, true] {
        for probe in [false, true] {
            let mut task = json!({"command":command(&["write","executed","unexpected"]),"input":[],"service":service,"resources":["held"]});
            if probe {
                task["tools"] = json!({"fixture":command(&["sleep","probe.pid"])});
            }
            let directory = fixture(json!({"task":task}));
            let g = graph(directory.path()).await;
            let plan = Plan::create(&g, &["task".into()], &[], false).unwrap();
            let lock_path = directory
                .path()
                .join(".taskflow/locks")
                .join(files::digest(b"resource:held"));
            files::atomic_write(&lock_path, b"").unwrap();
            let lock = std::fs::OpenOptions::new()
                .read(true)
                .write(true)
                .open(lock_path)
                .unwrap();
            if !probe {
                lock.lock().unwrap();
            }
            let (events, mut receiver) = tokio::sync::mpsc::unbounded_channel();
            let services = Arc::new(runner::Services {
                controls: std::sync::Mutex::new(BTreeMap::new()),
                events,
                joins: std::sync::Mutex::new(vec![]),
            });
            let options = RunOptions {
                services: Some(services.clone()),
                quiet: true,
                ..Default::default()
            };
            let running = options.task_cancellations.clone();
            let cancel = CancellationToken::new();
            let stop = cancel.clone();
            let execution =
                tokio::spawn(async move { runner::run_plan(g, plan, options, stop).await });
            if probe {
                wait_lines(&directory.path().join("probe.pid"), "", 1).await;
            } else {
                tokio::time::timeout(Duration::from_secs(10), async {
                    while !running.lock().unwrap().contains_key("app#task") {
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    }
                })
                .await
                .unwrap();
            }
            cancel.cancel();
            let result = tokio::time::timeout(Duration::from_secs(10), execution)
                .await
                .unwrap()
                .unwrap()
                .unwrap();
            let receipt = &result.results["app#task"];
            assert_eq!(receipt.outcome, Outcome::Cancelled, "{receipt:?}");
            assert_eq!(receipt.exit_code, 130);
            assert!(!directory.path().join("executed").exists());
            if service {
                let (_, event) = receiver.try_recv().unwrap();
                assert!(event.cancelled());
                assert_eq!(event.code, 130);
            }
            services.shutdown().await.unwrap();
            if probe {
                let pid = std::fs::read_to_string(directory.path().join("probe.pid"))
                    .unwrap()
                    .parse()
                    .unwrap();
                assert!(!pid_alive(pid));
            }
        }
    }
}

#[tokio::test]
async fn cache_verify_rejects_misdirected_and_inconsistent_artifacts() {
    let directory =
        fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":["output"]}}));
    std::fs::write(directory.path().join("output"), "retained").unwrap();
    let g = graph(directory.path()).await;
    let original = cache::Artifact::capture(
        files::digest(b"valid"),
        "app#build".into(),
        &g.workspace.projects["app"],
        &g.tasks["app#build"].task,
    )
    .unwrap();
    cache::store(directory.path(), &original).unwrap();
    let verify = || {
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(directory.path())
            .args(["--json", "cache", "verify"])
            .output()
            .unwrap();
        let report: Value = serde_json::from_slice(&output.stdout).unwrap();
        (output.status.success(), report)
    };
    assert!(verify().0);
    let wrong_key = files::digest(b"misdirected");
    std::fs::copy(
        cache::entry_path(directory.path(), &original.key),
        cache::entry_path(directory.path(), &wrong_key),
    )
    .unwrap();
    for corruption in [
        "version",
        "output-digest",
        "file-digest",
        "base64",
        "duplicate-path",
        "file-descendant",
        "unsafe-path",
    ] {
        let mut artifact = original.clone();
        artifact.key = files::digest(corruption.as_bytes());
        match corruption {
            "version" => artifact.version = 99,
            "output-digest" => artifact.output_digest = files::digest(b"incorrect"),
            "file-digest" => {
                let cache::Content::File { digest, .. } = &mut artifact.files[0].content else {
                    panic!("expected file");
                };
                *digest = files::digest(b"incorrect");
            }
            "base64" => {
                let cache::Content::File { data, .. } = &mut artifact.files[0].content else {
                    panic!("expected file");
                };
                *data = "!invalid-base64!".into();
            }
            "duplicate-path" => artifact.files.push(artifact.files[0].clone()),
            "file-descendant" => {
                let mut child = artifact.files[0].clone();
                child.path.push_str("/child");
                artifact.files.push(child);
            }
            "unsafe-path" => artifact.files[0].path = "../outside".into(),
            _ => unreachable!(),
        }
        if corruption != "output-digest" {
            artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
        }
        // The object envelope is valid: verification must inspect its payload.
        cache::store(directory.path(), &artifact).unwrap();
    }
    let (success, report) = verify();
    assert!(!success);
    let entries = report["entries"].as_array().unwrap();
    assert_eq!(entries.len(), 9);
    for entry in entries {
        assert_eq!(entry["valid"], entry["key"] == original.key);
    }
    assert_eq!(
        std::fs::read_to_string(directory.path().join("output")).unwrap(),
        "retained"
    );
}

#[test]
fn notification_paths_survive_concurrent_file_removal() {
    let directory = tempfile::tempdir().unwrap();
    let path = directory.path().join("transient");
    let expected = directory.path().canonicalize().unwrap().join("transient");
    let barrier = std::sync::Barrier::new(2);
    std::thread::scope(|scope| {
        scope.spawn(|| {
            barrier.wait();
            for _ in 0..10000 {
                std::fs::write(&path, "temporary").unwrap();
                std::fs::remove_file(&path).unwrap();
            }
        });
        barrier.wait();
        for _ in 0..10000 {
            assert_eq!(files::canonical_path(&path).unwrap(), expected);
        }
    });
}

#[tokio::test]
async fn docker_context_cannot_override_a_validated_local_host() {
    let directory = fixture(json!({}));
    let tools = tempfile::tempdir().unwrap();
    // Link the already-closed executable: parallel subprocess creation can
    // transiently retain a writer to a freshly copied binary on Linux (ETXTBSY).
    // Both temporary directories use the same filesystem.
    std::fs::hard_link(
        helper(),
        tools.path().join(if cfg!(windows) {
            "docker.exe"
        } else {
            "docker"
        }),
    )
    .unwrap();
    let paths: Vec<_> = std::iter::once(tools.path().to_path_buf())
        .chain(std::env::split_paths(&std::env::var_os("PATH").unwrap()))
        .collect();
    let mut environment: BTreeMap<String, String> = std::env::vars().collect();
    // Remove inherited spellings as well: Windows environment names ignore case.
    environment.retain(|key, _| {
        !["PATH", "DOCKER_HOST", "DOCKER_CONTEXT"]
            .iter()
            .any(|name| key.eq_ignore_ascii_case(name))
    });
    environment.insert(
        "PATH".into(),
        std::env::join_paths(paths)
            .unwrap()
            .to_string_lossy()
            .into(),
    );
    environment.insert("DOCKER_HOST".into(), "unix:///local.sock".into());
    environment.insert("DOCKER_CONTEXT".into(), "remote-fixture".into());
    let task: config::Task = serde_json::from_value(json!({"command":["unused"]})).unwrap();
    let error = taskflow::docker::prepare(
        directory.path(),
        directory.path(),
        &task,
        &environment,
        &BTreeMap::new(),
        &uuid::Uuid::now_v7().to_string(),
        &CancellationToken::new(),
    )
    .await
    .err()
    .unwrap();
    assert!(
        error.to_string().contains("local daemon socket"),
        "{error:#}"
    );
    assert!(!directory.path().join("docker-start").exists());
}

#[tokio::test]
async fn check_rejects_remote_credentials_in_every_project_task() {
    for name in [
        "TFLOW_REMOTE_ACCESS",
        "TFLOW_REMOTE_SECRET",
        "TFLOW_REMOTE_SESSION",
        "tflow_remote_secret",
    ] {
        for field in ["env", "envInputs", "secrets"] {
            let directory = fixture(json!({"safe":{"command":command(&["version"])}}));
            let path = directory.path().join("taskflow.yml");
            let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
            config["remote"] = json!({"endpoint":"http://127.0.0.1:9000","bucket":"fixture","namespace":"fixture","accessKeyEnv":"TFLOW_REMOTE_ACCESS","secretKeyEnv":"TFLOW_REMOTE_SECRET","sessionTokenEnv":"TFLOW_REMOTE_SESSION","mode":"off"});
            std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
            std::fs::create_dir(directory.path().join("child")).unwrap();
            std::fs::write(
                directory.path().join("Cargo.toml"),
                "[workspace]\nmembers=['child']\nresolver='2'\n",
            )
            .unwrap();
            std::fs::write(
                directory.path().join("child/Cargo.toml"),
                "[package]\nname='child'\nversion='0.1.0'\n[lib]\npath='lib.rs'\n",
            )
            .unwrap();
            std::fs::write(directory.path().join("child/lib.rs"), "").unwrap();
            let mut task = json!({"command":command(&["write","executed","unexpected"])});
            task[field] = if field == "env" {
                json!({name:"value"})
            } else {
                json!([name])
            };
            std::fs::write(
                directory.path().join("child/taskflow.yml"),
                serde_yaml::to_string(
                    &json!({"version":1,"project":"child","tasks":{"unused":task}}),
                )
                .unwrap(),
            )
            .unwrap();
            let result = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(directory.path())
                .args(["--json", "check"])
                .output()
                .unwrap();
            let collision = cfg!(windows) || name != "tflow_remote_secret";
            assert_eq!(
                result.status.success(),
                !collision,
                "{}",
                String::from_utf8_lossy(&result.stderr)
            );
            if collision {
                let error = String::from_utf8_lossy(&result.stderr);
                assert!(
                    error.contains("child#unused") && error.contains("cache transport credentials"),
                    "{error}"
                );
            }
            assert!(!directory.path().join("child/executed").exists());
        }
    }
}

#[tokio::test]
async fn ci_export_rejects_symlink_destinations_before_writing_either_file() {
    for linked in ["directory", "workflow", "blueprint"] {
        let directory = fixture(
            json!({"check":{"command":command(&["version"]),"input":[],"platform":{"os":config::host_os(),"arch":config::host_arch()}}}),
        );
        let external = tempfile::tempdir().unwrap();
        let path = directory.path().join("taskflow.yml");
        let mut cfg: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
        cfg["ci"] = json!({"revision":"1111111111111111111111111111111111111111","rust":"nightly-2026-01-01","runners":{config::Platform::default().key():"self-hosted"}});
        std::fs::write(path, serde_yaml::to_string(&cfg).unwrap()).unwrap();
        let parent = directory.path().join(".github/workflows");
        std::fs::create_dir_all(parent.parent().unwrap()).unwrap();
        if linked == "directory" {
            #[cfg(unix)]
            std::os::unix::fs::symlink(external.path(), &parent).unwrap();
            #[cfg(windows)]
            std::os::windows::fs::symlink_dir(external.path(), &parent).unwrap();
        } else {
            std::fs::create_dir(&parent).unwrap();
            let name = if linked == "workflow" {
                "ci.yml"
            } else {
                "ci.taskflow.json"
            };
            std::fs::write(external.path().join(name), "sentinel").unwrap();
            #[cfg(unix)]
            std::os::unix::fs::symlink(external.path().join(name), parent.join(name)).unwrap();
            #[cfg(windows)]
            std::os::windows::fs::symlink_file(external.path().join(name), parent.join(name))
                .unwrap();
        }
        let g = graph(directory.path()).await;
        let error = taskflow::ci::export(
            &g,
            vec!["check".into()],
            Path::new(".github/workflows/ci.yml"),
        )
        .unwrap_err();
        assert!(error.to_string().contains("escapes workspace"), "{error:#}");
        for name in ["ci.yml", "ci.taskflow.json"] {
            if external.path().join(name).exists() {
                assert_eq!(
                    std::fs::read_to_string(external.path().join(name)).unwrap(),
                    "sentinel"
                );
            }
            assert!(!parent.join(name).exists() || parent.join(name).is_symlink());
        }
    }
    let directory = fixture(
        json!({"check":{"command":command(&["version"]),"input":[],"platform":{"os":config::host_os(),"arch":config::host_arch()}}}),
    );
    let mut ws = Workspace::discover(directory.path()).await.unwrap();
    ws.config.ci = Some(serde_json::from_value(json!({"revision":"1111111111111111111111111111111111111111","rust":"nightly-2026-01-01","runners":{config::Platform::default().key():"self-hosted"}})).unwrap());
    taskflow::ci::export(
        &Graph::build(ws).unwrap(),
        vec!["check".into()],
        Path::new("new/nested/ci.yml"),
    )
    .unwrap();
    assert!(directory.path().join("new/nested/ci.yml").is_file());
    assert!(directory
        .path()
        .join("new/nested/ci.taskflow.json")
        .is_file());
}

#[tokio::test]
async fn tool_identity_includes_stderr_without_contaminating_metadata() {
    let directory = fixture(
        json!({"build":{"command":command(&["write","out","built"]),"input":[],"output":["out"],"cache":true,"tools":{"fixture":command(&["version-streams","version.stdout","version.stderr"])}}}),
    );
    std::fs::write(directory.path().join("version.stdout"), "").unwrap();
    std::fs::write(directory.path().join("version.stderr"), "tool-v1").unwrap();
    let g = graph(directory.path()).await;
    assert_eq!(
        run(g.clone(), &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    assert_eq!(
        run(g.clone(), &["build"]).await.results["app#build"].outcome,
        Outcome::LocalCache
    );
    std::fs::write(directory.path().join("version.stderr"), "tool-v2").unwrap();
    assert_eq!(
        run(g.clone(), &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    std::fs::write(directory.path().join("version.stderr"), "").unwrap();
    std::fs::write(directory.path().join("version.stdout"), "tool-v2").unwrap();
    assert_eq!(
        run(g, &["build"]).await.results["app#build"].outcome,
        Outcome::Executed
    );
    std::fs::write(
        directory.path().join("version.stdout"),
        r#"{"metadata":true}"#,
    )
    .unwrap();
    std::fs::write(directory.path().join("version.stderr"), "diagnostic").unwrap();
    let command: config::Command = serde_json::from_value(command(&[
        "version-streams",
        "version.stdout",
        "version.stderr",
    ]))
    .unwrap();
    let bytes = taskflow::process::capture(directory.path(), &command, &[])
        .await
        .unwrap();
    assert_eq!(
        serde_json::from_slice::<Value>(&bytes).unwrap(),
        json!({"metadata":true})
    );
}

#[tokio::test]
async fn cache_restore_rejects_filesystem_aliases_before_replacing_outputs() {
    for (first, second) in [("A", "a"), ("é", "e\u{301}")] {
        for directory_alias in [false, true] {
            let root =
                fixture(json!({"build":{"command":command(&["version"]),"output":["out/**"]}}));
            let probe = tempfile::tempdir_in(root.path()).unwrap();
            std::fs::create_dir(probe.path().join(first)).unwrap();
            let aliases = match std::fs::create_dir(probe.path().join(second)) {
                Ok(()) => false,
                Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => true,
                Err(error) => panic!("filesystem probe failed: {error}"),
            };
            files::atomic_write(&root.path().join("out/keep"), b"preserved").unwrap();
            let g = graph(root.path()).await;
            let project = &g.workspace.projects["app"];
            let task = &g.tasks["app#build"].task;
            let mut artifact =
                cache::Artifact::capture("key".into(), "app#build".into(), project, task).unwrap();
            let content = artifact
                .files
                .iter()
                .find(|entry| entry.path == "out/keep")
                .unwrap()
                .content
                .clone();
            artifact.files.retain(|entry| entry.path == "out");
            for (index, name) in [first, second].iter().enumerate() {
                let path = if directory_alias {
                    artifact.files.push(cache::FileRecord {
                        path: format!("out/{name}"),
                        content: cache::Content::Directory,
                    });
                    format!("out/{name}/file{index}")
                } else {
                    format!("out/{name}")
                };
                artifact.files.push(cache::FileRecord {
                    path,
                    content: content.clone(),
                });
            }
            artifact
                .files
                .sort_by(|left, right| left.path.cmp(&right.path));
            artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
            // Portable integrity is independent of the producer's filename rules.
            artifact.validate_integrity("key").unwrap();
            let result = artifact.restore("key", "app#build", project, task);
            if aliases {
                assert!(result
                    .unwrap_err()
                    .to_string()
                    .contains("aliases another path"));
                assert_eq!(
                    std::fs::read(root.path().join("out/keep")).unwrap(),
                    b"preserved"
                );
                assert_eq!(
                    std::fs::read_dir(root.path().join("out")).unwrap().count(),
                    1
                );
            } else {
                result.unwrap();
                assert_eq!(
                    cache::output_state(project, task).unwrap(),
                    artifact.output_digest
                );
            }
            assert!(!std::fs::read_dir(root.path()).unwrap().any(|entry| entry
                .unwrap()
                .file_name()
                .to_string_lossy()
                .starts_with(".taskflow-restore-")));
        }
    }
}

#[tokio::test]
async fn finite_timeouts_fail_while_operator_cancellation_remains_distinct() {
    for cancelled in [false, true] {
        let root = fixture(json!({
            "slow":{"command":command(&["sleep","parent.pid","child.pid"]),"input":[],"output":[],"cache":true,"tools":{"fixture":command(&["version"])},"timeout":"2s"},
            "dependent":{"command":command(&["write","dependent","unexpected"]),"input":[],"dependsOn":["slow"]}
        }));
        let g = graph(root.path()).await;
        let plan = Plan::create(&g, &["dependent".into()], &[], false).unwrap();
        let token = CancellationToken::new();
        let execution = tokio::spawn(runner::run_plan(
            g,
            plan,
            RunOptions {
                quiet: true,
                ..RunOptions::default()
            },
            token.clone(),
        ));
        if cancelled {
            tokio::time::timeout(Duration::from_secs(10), async {
                while !root.path().join("child.pid").exists() {
                    assert!(
                        !execution.is_finished(),
                        "task exited before cancellation barrier"
                    );
                    tokio::time::sleep(Duration::from_millis(10)).await;
                }
            })
            .await
            .unwrap();
            token.cancel();
        }
        let result = tokio::time::timeout(Duration::from_secs(10), execution)
            .await
            .unwrap()
            .unwrap()
            .unwrap();
        let receipt = &result.results["app#slow"];
        assert!(!result.success);
        assert_eq!(
            receipt.outcome,
            if cancelled {
                Outcome::Cancelled
            } else {
                Outcome::Failed
            }
        );
        assert_eq!(receipt.exit_code, if cancelled { 130 } else { 124 });
        assert_eq!(
            receipt.diagnostic.as_deref(),
            if cancelled {
                None
            } else {
                Some("task timeout elapsed")
            }
        );
        assert!(!root.path().join("dependent").exists());
        assert!(!cache::entry_path(root.path(), &receipt.key).exists());
        for name in ["parent.pid", "child.pid"] {
            let pid = std::fs::read_to_string(root.path().join(name))
                .unwrap()
                .parse()
                .unwrap();
            // Pipe EOF and the direct-child wait are complete, but an orphaned
            // grandchild's PID can remain a zombie until the OS reaper runs.
            // Require observable PID removal within a bound instead of racing
            // that external reaper with the receipt notification.
            tokio::time::timeout(Duration::from_secs(5), async {
                while pid_alive(pid) {
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
            })
            .await
            .unwrap_or_else(|_| panic!("{name} ({pid}) survived task completion"));
        }
    }
}

#[tokio::test]
#[cfg(unix)]
async fn input_snapshots_only_walk_possible_project_and_pattern_roots() {
    let directory =
        fixture(json!({"check":{"command":command(&["version"]),"input":[],"output":[]}}));
    files::atomic_write(&directory.path().join("member/src/lib.rs"), b"source").unwrap();
    files::atomic_write(&directory.path().join("shared/data.txt"), b"shared").unwrap();
    let graph = graph(directory.path()).await;
    let mut project = graph.workspace.projects["app"].clone();
    project.directory = directory.path().canonicalize().unwrap().join("member");
    let mut task = graph.tasks["app#check"].task.clone();
    // An invalid identity in an unrelated subtree deterministically detects an
    // accidental workspace scan without depending on timing or ACL privileges.
    std::fs::create_dir(directory.path().join("unrelated")).unwrap();
    std::fs::write(
        directory.path().join("unrelated").join(r"invalid\identity"),
        b"unrelated",
    )
    .unwrap();
    let metadata: BTreeSet<_> = graph
        .workspace
        .metadata_files
        .iter()
        .map(|path| files::slash(path.strip_prefix(&graph.workspace.root).unwrap()).unwrap())
        .collect();
    for inputs in [json!([]), json!([{"auto":false}]), json!(["!**"])] {
        task.input = Some(serde_json::from_value(inputs).unwrap());
        assert_eq!(
            files::input_state(&graph.workspace, &project, &task)
                .unwrap()
                .keys()
                .cloned()
                .collect::<BTreeSet<_>>(),
            metadata
        );
    }
    for inputs in [
        None,
        Some(json!([{"auto":true}])),
        Some(json!(["src/**/*.rs"])),
        Some(json!(["src/lib.rs", "src/**"])),
    ] {
        task.input = inputs.map(|value| serde_json::from_value(value).unwrap());
        let state = files::input_state(&graph.workspace, &project, &task).unwrap();
        assert!(state.contains_key("member/src/lib.rs"));
        assert_eq!(state.len(), metadata.len() + 1);
    }
    task.input = Some(serde_json::from_value(json!(["../shared/*.txt", "missing/*.rs"])).unwrap());
    assert!(files::input_state(&graph.workspace, &project, &task)
        .unwrap()
        .contains_key("shared/data.txt"));
    std::os::unix::fs::symlink("../shared", project.directory.join("alias")).unwrap();
    task.input = Some(serde_json::from_value(json!(["alias/*.txt"])).unwrap());
    assert_eq!(
        files::input_state(&graph.workspace, &project, &task)
            .unwrap()
            .len(),
        metadata.len()
    );
    task.input = Some(serde_json::from_value(json!(["../unrelated/**"])).unwrap());
    assert!(files::input_state(&graph.workspace, &project, &task).is_err());
}

#[tokio::test]
async fn explicit_wildcard_inputs_include_ignored_directories_in_cache_and_watch() {
    for pattern in [
        "*/manifest.json",
        "**/manifest.json",
        "[dt]ist/manifest.json",
        "{dist,target}/manifest.json",
    ] {
        let root = fixture(json!({
            "build":{"command":command(&["copy","dist/manifest.json","out"]),"input":[pattern],"output":["out"],"cache":true,"tools":{"fixture":command(&["version"])},"watch":{}},
            "barrier":{"command":command(&["record","barrier","ready"]),"input":[]}
        }));
        profile(root.path(), &["build", "barrier"]);
        files::atomic_write(&root.path().join("dist/manifest.json"), b"first").unwrap();
        let internal = root.path().join(".taskflow-restore-fixture/manifest.json");
        files::atomic_write(&internal, b"internal").unwrap();
        let g = graph(root.path()).await;
        let snapshot = files::input_state(
            &g.workspace,
            &g.workspace.projects["app"],
            &g.tasks["app#build"].task,
        )
        .unwrap();
        assert!(snapshot.contains_key("dist/manifest.json"), "{pattern}");
        assert!(!snapshot.contains_key(".taskflow-restore-fixture/manifest.json"));
        assert!(!files::input_matches(
            &g.workspace.projects["app"],
            &g.tasks["app#build"].task,
            &internal
        )
        .unwrap());
        assert_eq!(
            run(g.clone(), &["build"]).await.results["app#build"].outcome,
            Outcome::Executed
        );
        assert_eq!(
            run(g.clone(), &["build"]).await.results["app#build"].outcome,
            Outcome::LocalCache
        );
        files::atomic_write(&root.path().join("dist/manifest.json"), b"second").unwrap();
        assert_eq!(
            run(g, &["build"]).await.results["app#build"].outcome,
            Outcome::Executed
        );
        let token = CancellationToken::new();
        let stop = token.clone();
        let directory = root.path().to_path_buf();
        let session = tokio::spawn(async move {
            taskflow::session::start(
                &directory,
                "default",
                RunOptions {
                    quiet: true,
                    ..RunOptions::default()
                },
                stop,
            )
            .await
        });
        wait_lines(&root.path().join("barrier"), "ready", 1).await;
        files::atomic_write(&root.path().join("dist/manifest.json"), b"watched").unwrap();
        let changed = tokio::time::timeout(Duration::from_secs(15), async {
            while std::fs::read(root.path().join("out")).unwrap_or_default() != b"watched" {
                assert!(!session.is_finished(), "session failed before watch update");
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await;
        token.cancel();
        session.await.unwrap().unwrap();
        changed.unwrap();
    }
}

#[tokio::test]
async fn libtest_inventory_honors_explicit_manifest_selection() {
    for nested in [false, true] {
        let prefix = if nested { "native/" } else { "" };
        let manifest = format!("{prefix}standalone/Cargo.toml");
        let selection = if nested {
            vec![format!("--manifest-path={manifest}")]
        } else {
            vec!["--manifest-path".into(), manifest.clone()]
        };
        let args: Vec<String> = ["cargo", "test", "--tests", "--offline"]
            .into_iter()
            .map(str::to_owned)
            .chain(selection)
            .collect();
        let root = fixture(
            json!({"suite":{"command":args,"input":[],"output":[],"shard":{"adapter":"libtest","count":2}}}),
        );
        if nested {
            let path = root.path().join("taskflow.yml");
            let mut cfg: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
            cfg["workspace"] = json!({"manifests":["native/Cargo.toml"]});
            std::fs::write(path, serde_yaml::to_string(&cfg).unwrap()).unwrap();
        }
        files::atomic_write(
            &root.path().join(format!("{prefix}Cargo.toml")),
            b"[workspace]\nmembers=['member']\nexclude=['standalone']\nresolver='2'\n",
        )
        .unwrap();
        for name in ["member", "standalone"] {
            files::atomic_write(
                &root.path().join(format!("{prefix}{name}/Cargo.toml")),
                format!("[package]\nname='{name}'\nversion='0.1.0'\nedition='2021'\n").as_bytes(),
            )
            .unwrap();
            files::atomic_write(
                &root.path().join(format!("{prefix}{name}/tests/shared.rs")),
                b"#[test] fn selected() {}\n",
            )
            .unwrap();
        }
        taskflow::discover::output_tool(
            root.path(),
            &[
                "cargo",
                "generate-lockfile",
                "--offline",
                "--manifest-path",
                &format!("{prefix}Cargo.toml"),
            ],
            &[],
        )
        .await
        .unwrap();
        let result = run(graph(root.path()).await, &["suite"]).await;
        assert!(result.success, "{result:?}");
        let (inventory, reports) = shard::read_reports(
            &root
                .path()
                .join(".taskflow/runs")
                .join(&result.results["app#suite"].execution),
        )
        .unwrap();
        assert_eq!(inventory.tests.len(), 1);
        assert_eq!(
            inventory.tests[0].id,
            "standalone@0.1.0:test:shared::selected"
        );
        assert!(shard::aggregate(&inventory, 2, &reports).unwrap());
    }
}

#[tokio::test]
async fn session_revalidates_provided_prerequisite_outputs() {
    for cached in [false, true] {
        let directory = fixture(json!({
            "producer":{"command":command(&["copy","source","middle"]),"input":["source"],"output":["middle"],"cache":cached,"tools":{"fixture":command(&["version"])}},
            "consumer":{"command":command(&["copy","middle","consumed"]),"dependsOn":["producer"],"input":["middle"],"output":["consumed"],"watch":{}}
        }));
        std::fs::write(directory.path().join("source"), "correct").unwrap();
        profile(directory.path(), &["consumer"]);
        let root = directory.path().to_path_buf();
        let token = CancellationToken::new();
        let stop = token.clone();
        let session = tokio::spawn(async move {
            taskflow::session::start(&root, "default", RunOptions::default(), stop).await
        });
        for mutation in ["initial", "corrupt", "delete"] {
            let previous = runner::previous(directory.path(), "app#consumer").map(|r| r.execution);
            match mutation {
                "corrupt" => std::fs::write(directory.path().join("middle"), "corrupt").unwrap(),
                "delete" => std::fs::remove_file(directory.path().join("middle")).unwrap(),
                _ => {}
            }
            tokio::time::timeout(Duration::from_secs(60), async {
                loop {
                    assert!(!session.is_finished(), "session exited before {mutation}");
                    if runner::previous(directory.path(), "app#consumer")
                        .is_some_and(|r| Some(r.execution) != previous)
                    {
                        break;
                    }
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
            })
            .await
            .unwrap();
            assert!(runner::previous(directory.path(), "app#consumer")
                .unwrap()
                .success());
            assert_eq!(
                std::fs::read_to_string(directory.path().join("consumed")).unwrap(),
                "correct"
            );
            assert_eq!(
                std::fs::read_to_string(directory.path().join("middle")).unwrap(),
                "correct"
            );
        }
        token.cancel();
        session.await.unwrap().unwrap();
    }
}

#[tokio::test]
async fn docker_cleanup_reuses_the_validated_launch_environment() {
    let directory = fixture(json!({}));
    let tools = tempfile::tempdir().unwrap();
    // Link the already-closed executable: parallel subprocess creation can
    // transiently retain a writer to a freshly copied binary on Linux (ETXTBSY).
    // Both temporary directories use the same filesystem.
    std::fs::hard_link(
        helper(),
        tools.path().join(if cfg!(windows) {
            "docker.exe"
        } else {
            "docker"
        }),
    )
    .unwrap();
    let mut environment: BTreeMap<String, String> = std::env::vars().collect();
    for (key, value) in [
        ("PATH", tools.path().to_string_lossy().into_owned()),
        ("DOCKER_CONTEXT", "cleanup-fixture".into()),
        ("DOCKER_HOST", "unix:///selected.sock".into()),
        ("DOCKER_CONFIG", ".".into()),
    ] {
        environment.retain(|name, _| !name.eq_ignore_ascii_case(key));
        environment.insert(key.into(), value);
    }
    let task: config::Task = serde_json::from_value(json!({"command":["unused"],"platform":{"executor":"docker","os":"linux","image":"fixture@sha256:0000000000000000000000000000000000000000000000000000000000000000"}})).unwrap();
    for explicit in [true, false] {
        let (_, mut owner) = taskflow::docker::prepare(
            directory.path(),
            directory.path(),
            &task,
            &environment,
            &BTreeMap::new(),
            &uuid::Uuid::now_v7().to_string(),
            &CancellationToken::new(),
        )
        .await
        .unwrap();
        if explicit {
            owner.cleanup().await.unwrap();
        }
        drop(owner);
    }
    // Both the async absence check and destructor use the selected CLI,
    // context, daemon, relative configuration directory, and working directory.
    assert_eq!(
        std::fs::read_to_string(directory.path().join("cleanup-events")).unwrap(),
        "rm\nps\nrm\n"
    );
}

#[tokio::test]
async fn shard_timeouts_and_cancellation_preserve_receipt_reasons() {
    for adapter in ["generic", "jest"] {
        for cancelled in [false, true] {
            let shard = if adapter == "generic" {
                json!({"adapter":adapter,"count":4,"list":command(&["inventory"]),"run":command(&["sleep","unit.pid"])})
            } else {
                json!({"adapter":adapter,"count":4})
            };
            let root = fixture(
                json!({"suite":{"command":command(&["jest"]),"input":[],"output":[],"timeout":if cancelled {"30s"} else {"2s"},"shard":shard}}),
            );
            for file in ["one.test.js", "two.test.js"] {
                std::fs::write(root.path().join(file), "test").unwrap();
            }
            let g = graph(root.path()).await;
            let plan = Plan::create(&g, &["suite".into()], &[], false).unwrap();
            let token = CancellationToken::new();
            let execution = tokio::spawn(runner::run_plan(
                g,
                plan,
                RunOptions::default(),
                token.clone(),
            ));
            if cancelled {
                tokio::time::timeout(Duration::from_secs(60), async {
                    while !root.path().join("unit.pid").exists() {
                        assert!(!execution.is_finished());
                        tokio::time::sleep(Duration::from_millis(10)).await;
                    }
                })
                .await
                .unwrap();
                token.cancel();
            }
            let result = tokio::time::timeout(Duration::from_secs(15), execution)
                .await
                .unwrap()
                .unwrap()
                .unwrap();
            let receipt = &result.results["app#suite"];
            assert!(!result.success);
            assert_eq!(
                receipt.outcome,
                if cancelled {
                    Outcome::Cancelled
                } else {
                    Outcome::Failed
                }
            );
            assert_eq!(receipt.exit_code, if cancelled { 130 } else { 124 });
            assert_eq!(
                receipt.diagnostic.as_deref(),
                if cancelled {
                    None
                } else {
                    Some("task timeout elapsed")
                }
            );
            let (inventory, reports) =
                shard::read_reports(&root.path().join(".taskflow/runs").join(&receipt.execution))
                    .unwrap();
            assert_eq!(reports.len(), 4);
            assert!(!shard::aggregate(&inventory, 4, &reports).unwrap());
            assert!(!cache::entry_path(root.path(), &receipt.key).exists());
        }
    }
}

#[tokio::test]
async fn affected_selection_rejects_unknown_task_filters() {
    let directory = fixture(
        json!({"check":{"command":command(&["write","started","unexpected"]),"input":["source"]}}),
    );
    let g = graph(directory.path()).await;
    for changes in [vec![], vec![PathBuf::from("source")]] {
        for requests in [
            vec!["chek"],
            vec!["other#check"],
            vec!["check", "app#missing"],
        ] {
            let requests: Vec<_> = requests.into_iter().map(String::from).collect();
            assert!(Plan::create(&g, &requests, &changes, true)
                .unwrap_err()
                .to_string()
                .contains("unknown requested task"));
        }
    }
    for request in ["check", "app#check"] {
        assert!(Plan::create(&g, &[request.into()], &[], true)
            .unwrap()
            .order
            .is_empty());
        assert_eq!(
            Plan::create(&g, &[request.into()], &[PathBuf::from("source")], true)
                .unwrap()
                .order,
            ["app#check"]
        );
    }
    for verb in ["plan", "run"] {
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .arg("--root")
            .arg(directory.path())
            .args([verb, "chek", "--changed", "source"])
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(String::from_utf8_lossy(&output.stderr).contains("unknown requested task"));
    }
    assert!(!directory.path().join("started").exists());
}

#[test]
fn docker_platform_validation_applies_cli_defaults_first() {
    for declared in [None, Some("linux"), Some("windows")] {
        let mut platform = json!({"executor":"docker","image":"fixture@sha256:0000000000000000000000000000000000000000000000000000000000000000"});
        if let Some(os) = declared {
            platform["os"] = json!(os);
        }
        let directory = fixture(
            json!({"build":{"command":command(&["write","started","unexpected"]),"platform":platform}}),
        );
        for selected in ["linux", "windows", "macos"] {
            let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .arg("--root")
                .arg(directory.path())
                .args(["--os", selected, "check"])
                .output()
                .unwrap();
            assert_eq!(
                output.status.success(),
                declared.unwrap_or(selected) == "linux",
                "declared={declared:?}, selected={selected}: {}",
                String::from_utf8_lossy(&output.stderr)
            );
        }
        assert!(!directory.path().join("started").exists());
    }
}

#[test]
fn check_rejects_absolute_input_patterns_on_every_host() {
    let directory = fixture(json!({}));
    let absolute = directory
        .path()
        .join("source")
        .to_string_lossy()
        .into_owned();
    for path in [
        absolute.as_str(),
        "/workspace/source",
        "C:/workspace/**",
        r"C:\workspace\**",
        r"\\server\share\**",
        r"\rooted\**",
        "C:relative",
    ] {
        for negative in [false, true] {
            let pattern = format!("{}{path}", if negative { "!" } else { "" });
            let config = json!({"version":1,"project":"app","tasks":{"check":{"command":command(&["version"]),"input":[pattern]}}});
            std::fs::write(
                directory.path().join("taskflow.yml"),
                serde_yaml::to_string(&config).unwrap(),
            )
            .unwrap();
            let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .arg("--root")
                .arg(directory.path())
                .arg("check")
                .output()
                .unwrap();
            assert!(!output.status.success(), "{pattern}");
            let error = config::load(&directory.path().join("taskflow.yml")).unwrap_err();
            assert!(format!("{error:#}").contains("project-relative"));
            assert!(
                String::from_utf8_lossy(&output.stderr).contains("app#check"),
                "{pattern}"
            );
        }
    }
    let task: config::Task = serde_json::from_value(json!({"command":["unused"],"input":["../sibling/**","!../sibling/generated/**","./source"]})).unwrap();
    task.validate().unwrap();
}

#[tokio::test]
async fn libtest_rejects_custom_harnesses_only_for_selected_targets() {
    let tasks: serde_json::Map<String, Value> = [
        ("library", vec!["--lib"]),
        ("normal", vec!["--test", "normal"]),
        ("custom", vec!["--test", "custom"]),
    ]
    .into_iter()
    .map(|(name, selector)| {
        let args: Vec<_> = ["cargo", "test", "--offline", "-p", "selected-app"]
            .into_iter()
            .chain(selector)
            .collect();
        (
            name.into(),
            json!({"command":args,"input":[],"output":[],"shard":{"adapter":"libtest","count":2}}),
        )
    })
    .collect();
    let root = fixture(json!(tasks));
    files::atomic_write(
        &root.path().join("Cargo.toml"),
        b"[workspace]\nmembers=['app','other']\nresolver='2'\n",
    )
    .unwrap();
    files::atomic_write(&root.path().join("app/Cargo.toml"), b"[package]\nname='selected-app'\nversion='0.1.0'\nedition='2021'\n[[test]]\nname='custom'\n'harness' = false # only this target uses a custom runner\n").unwrap();
    files::atomic_write(
        &root.path().join("other/Cargo.toml"),
        b"[package]\nname='other'\nversion='0.1.0'\nedition='2021'\n[lib]\nharness=false\n",
    )
    .unwrap();
    files::atomic_write(
        &root.path().join("app/src/lib.rs"),
        b"#[test] fn library() {}\n",
    )
    .unwrap();
    files::atomic_write(&root.path().join("other/src/lib.rs"), b"fn main() {}\n").unwrap();
    files::atomic_write(
        &root.path().join("app/tests/normal.rs"),
        b"#[test] fn normal() {}\n",
    )
    .unwrap();
    files::atomic_write(
        &root.path().join("app/tests/custom.rs"),
        b"fn main() { std::fs::write(\"custom-started\", b\"unexpected\").unwrap(); }\n",
    )
    .unwrap();
    taskflow::discover::output_tool(
        root.path(),
        &["cargo", "generate-lockfile", "--offline"],
        &[],
    )
    .await
    .unwrap();
    let g = graph(root.path()).await;
    for task in ["library", "normal"] {
        let result = run(g.clone(), &[task]).await;
        assert!(result.success, "{result:?}");
        let receipt = &result.results[&format!("app#{task}")];
        let (inventory, reports) =
            shard::read_reports(&root.path().join(".taskflow/runs").join(&receipt.execution))
                .unwrap();
        assert_eq!(inventory.tests.len(), 1);
        assert!(shard::aggregate(&inventory, 2, &reports).unwrap());
    }
    let result = run(g, &["custom"]).await;
    assert!(!result.success);
    assert!(result.results["app#custom"]
        .diagnostic
        .as_ref()
        .unwrap()
        .contains("custom Rust harness"));
    assert!(!root.path().join("custom-started").exists());
    assert!(!root.path().join("app/custom-started").exists());
}

#[tokio::test]
async fn pending_cancellation_uses_operator_exit_code() {
    for before_start in [true, false] {
        let directory = fixture(json!({
            "a": {"command":command(&["sleep", "active.pid"]), "input":[]},
            "b": {"command":command(&["write", "unexpected", "ran"]), "input":[]},
            "c": {"command":command(&["version"]), "dependsOn":["a"], "input":[]}
        }));
        let g = graph(directory.path()).await;
        let plan = Plan::create(&g, &["a".into(), "b".into(), "c".into()], &[], false).unwrap();
        let cancel = CancellationToken::new();
        if before_start {
            cancel.cancel();
        }
        let stop = cancel.clone();
        let execution = tokio::spawn(runner::run_plan(
            g,
            plan,
            RunOptions {
                jobs: 1,
                quiet: true,
                ..Default::default()
            },
            stop,
        ));
        if !before_start {
            wait_lines(&directory.path().join("active.pid"), "", 1).await;
            cancel.cancel();
        }
        let result = execution.await.unwrap().unwrap();
        assert!(!result.success);
        assert_eq!(result.results.len(), 3);
        for receipt in result.results.values() {
            assert_eq!(receipt.outcome, Outcome::Cancelled, "{receipt:?}");
            assert_eq!(receipt.exit_code, 130);
        }
        assert!(!directory.path().join("unexpected").exists());
    }
}

#[test]
#[cfg(unix)]
fn non_unicode_inherited_environment_is_rejected_without_panicking() {
    use std::os::unix::ffi::OsStringExt;

    let directory =
        fixture(json!({"build":{"command":command(&["record","unexpected","ran"]),"output":[]}}));
    files::atomic_write(
        &directory.path().join("Cargo.toml"),
        b"[package]\nname='env-fixture'\nversion='0.1.0'\nedition='2021'\n",
    )
    .unwrap();
    files::atomic_write(&directory.path().join("src/lib.rs"), b"").unwrap();
    for invalid_name in [true, false] {
        for args in [
            vec!["check"],
            vec!["query", "projects"],
            vec!["plan", "build"],
            vec!["run", "build"],
        ] {
            let invalid = std::ffi::OsString::from_vec(b"private-invalid-\xff".to_vec());
            let mut child = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"));
            if invalid_name {
                child.env(&invalid, "private-value");
            } else {
                child.env("TFLOW_INVALID_UTF8", &invalid);
            }
            let output = child
                .current_dir(directory.path())
                .args(["--root", "."])
                .args(&args)
                .output()
                .unwrap();
            assert_eq!(output.status.code(), Some(1), "{args:?}: {output:?}");
            let diagnostic = String::from_utf8(output.stderr).unwrap();
            assert!(
                diagnostic.contains(if invalid_name {
                    "non-Unicode name"
                } else {
                    "non-Unicode value"
                }),
                "{diagnostic}"
            );
            assert!(!diagnostic.contains("private-"), "{diagnostic}");
            assert!(!diagnostic.contains("panicked"), "{diagnostic}");
            assert!(!directory.path().join("unexpected").exists());
        }
    }
}

#[test]
fn reserved_output_aliases_are_rejected_on_every_host() {
    for path in [
        ".TASKFLOW/cache/**",
        ".GiT/**",
        "out/.git/config",
        ".TaskFlow-Restore-old/**",
    ] {
        let directory = fixture(
            json!({"build":{"command":command(&["record","unexpected","ran"]),"output":[path]}}),
        );
        for action in ["check", "run"] {
            let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(directory.path())
                .args(["--root", ".", action])
                .args(if action == "run" {
                    vec!["build"]
                } else {
                    vec![]
                })
                .output()
                .unwrap();
            assert!(!output.status.success(), "{path}: {output:?}");
            assert!(!directory.path().join("unexpected").exists());
        }
        let files = vec![cache::FileRecord {
            path: path.trim_end_matches("/**").into(),
            content: cache::Content::Directory,
        }];
        let artifact = cache::Artifact {
            version: 1,
            key: files::digest(b"reserved"),
            task: "app#build".into(),
            output_digest: cache::output_digest(&files).unwrap(),
            files,
            result_identity: None,
            shards: None,
        };
        assert!(
            artifact.validate_integrity(&artifact.key).is_err(),
            "{path}"
        );
    }
    let task: config::Task = serde_json::from_value(
        json!({"command":["unused"],"output":[".git-output/**",".taskflow-report/**"]}),
    )
    .unwrap();
    task.validate().unwrap();
}

#[test]
fn check_rejects_windows_rooted_outputs_on_every_host() {
    for path in [
        "C:/build/**",
        r"C:\build\**",
        r"\\server\share\build",
        r"\build",
        "C:build",
    ] {
        let directory = fixture(json!({"build":{"command":command(&["version"]),"output":[path]}}));
        for args in [
            vec!["check"],
            vec!["ci", "export", "build", "--output", "workflow.yml"],
        ] {
            let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(directory.path())
                .args(args)
                .output()
                .unwrap();
            assert!(!output.status.success(), "{path}");
            assert!(
                String::from_utf8_lossy(&output.stderr).contains("app#build"),
                "{output:?}"
            );
            let error = config::load(&directory.path().join("taskflow.yml")).unwrap_err();
            assert!(format!("{error:#}").contains("output must stay inside the project"));
        }
    }
}

#[test]
fn check_rejects_nul_in_every_shell_argument() {
    for shell in [json!(["sh\0", "-c"]), json!(["sh", "-c\0"])] {
        let directory = fixture(json!({"task":{"command":"echo unexpected", "shell":shell}}));
        let error = config::load(&directory.path().join("taskflow.yml")).unwrap_err();
        assert!(format!("{error:#}").contains("shell arguments must not contain NUL"));
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(directory.path())
            .arg("check")
            .output()
            .unwrap();
        assert!(!output.status.success());
    }
}

#[test]
fn docker_host_environment_preserves_resolved_precedence() {
    const CHILD: &str = "TFLOW_TEST_DOCKER_PRECEDENCE";
    if std::env::var_os(CHILD).is_some() {
        let directory = fixture(
            json!({"task":{"command":["unused"], "env":{"DOCKER_CONTEXT":"task-context"}}}),
        );
        let runtime = tokio::runtime::Runtime::new().unwrap();
        let g = runtime.block_on(graph(directory.path()));
        let env = taskflow::environment::Environment::build(
            &g.workspace,
            &g.workspace.projects["app"],
            &g.tasks["app#task"].task,
            &BTreeMap::from([("DOCKER_HOST".into(), "unix:///cli.sock".into())]),
            false,
        )
        .unwrap();
        let values = taskflow::docker::host_environment(&env.values);
        assert_eq!(values["DOCKER_HOST"], "unix:///cli.sock");
        assert_eq!(values["DOCKER_CONTEXT"], "task-context");
        assert_eq!(values["DOCKER_CONFIG"], "inherited-config");
        let missing = taskflow::docker::host_environment(&BTreeMap::new());
        assert_eq!(missing["DOCKER_HOST"], "unix:///inherited.sock");
        if cfg!(windows) {
            let values = taskflow::docker::host_environment(&BTreeMap::from([(
                "docker_host".into(),
                "unix:///lower.sock".into(),
            )]));
            assert_eq!(values["docker_host"], "unix:///lower.sock");
            assert!(!values.contains_key("DOCKER_HOST"));
        }
        return;
    }
    let output = std::process::Command::new(std::env::current_exe().unwrap())
        .args([
            "--exact",
            "docker_host_environment_preserves_resolved_precedence",
            "--nocapture",
        ])
        .env(CHILD, "1")
        .env("DOCKER_HOST", "unix:///inherited.sock")
        .env("DOCKER_CONTEXT", "inherited-context")
        .env("DOCKER_CONFIG", "inherited-config")
        .output()
        .unwrap();
    assert!(output.status.success(), "{output:?}");
}

#[tokio::test]
async fn uncached_output_digests_are_not_limited_by_artifact_size() {
    let directory =
        fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":["large"]}}));
    let large = std::fs::File::create(directory.path().join("large")).unwrap();
    large.set_len(cache::MAX_CACHE_BYTES as u64 + 1).unwrap();
    let g = graph(directory.path()).await;
    let result = run(g.clone(), &["build"]).await;
    assert!(result.success, "{result:?}");
    assert_eq!(result.results["app#build"].outcome, Outcome::Executed);
    assert!(!result.results["app#build"].output.is_empty());
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    assert!(
        cache::Artifact::capture(files::digest(b"large"), "app#build".into(), project, task)
            .is_err()
    );
    std::fs::write(directory.path().join("large"), "small").unwrap();
    let artifact =
        cache::Artifact::capture(files::digest(b"small"), "app#build".into(), project, task)
            .unwrap();
    artifact.validate_integrity(&artifact.key).unwrap();
    assert_eq!(
        cache::output_state(project, task).unwrap(),
        artifact.output_digest
    );
    assert_ne!(result.results["app#build"].output, artifact.output_digest);
}

#[tokio::test]
async fn output_ownership_uses_destination_filesystem_aliases() {
    for (first, second) in [("Build", "build"), ("é", "e\u{301}")] {
        for cross_project in [false, true] {
            let root = fixture(
                json!({"a":{"command":command(&["version"]),"output":[format!("nested/{first}")]}}),
            );
            std::fs::create_dir(root.path().join("nested")).unwrap();
            let probe = tempfile::tempdir_in(root.path().join("nested")).unwrap();
            std::fs::create_dir(probe.path().join(first)).unwrap();
            let aliases = probe.path().join(second).exists();
            let mut ws = Workspace::discover(root.path()).await.unwrap();
            let task: config::Task = serde_json::from_value(
                json!({"command":command(&["version"]),"output":[format!("{second}/child")]}),
            )
            .unwrap();
            if cross_project {
                let mut project = ws.projects["app"].clone();
                project.id = "nested".into();
                project.directory = project.directory.join("nested");
                let config = project.config.as_mut().unwrap();
                config.project = "nested".into();
                config.tasks = BTreeMap::from([("b".into(), task)]);
                ws.projects.insert(project.id.clone(), project);
            } else {
                let mut task = task;
                task.output = Some(vec![format!("nested/{second}/child")]);
                ws.projects
                    .get_mut("app")
                    .unwrap()
                    .config
                    .as_mut()
                    .unwrap()
                    .tasks
                    .insert("b".into(), task);
            }
            let result = Graph::build(ws);
            if aliases {
                assert!(
                    format!("{:#}", result.unwrap_err()).contains("overlapping output ownership")
                );
            } else {
                result.unwrap();
            }
            assert!(!root.path().join("nested").join(first).exists());
            assert!(!root.path().join("nested").join(second).exists());
        }
    }
}

#[tokio::test]
async fn docker_forwards_cli_overrides_to_tasks_tools_and_shards() {
    let platform = json!({"executor":"docker","os":"linux","image":"fixture@sha256:0000000000000000000000000000000000000000000000000000000000000000"});
    let root = fixture(json!({
        "finite":{"command":["fixture","finite"],"input":[],"output":["received"],"cache":true,"tools":{"fixture":["fixture","probe"]},"platform":platform},
        "test":{"command":["fixture","shard"],"input":[],"tools":{"fixture":["fixture","probe"]},"shard":{"adapter":"generic","count":1,"list":["fixture","inventory"],"run":["fixture","shard"]},"platform":platform},
        "secret":{"command":command(&["version"]),"secrets":["TFLOW_SIBLING_SECRET"]}
    }));
    let tools = tempfile::tempdir().unwrap();
    std::fs::hard_link(
        helper(),
        tools.path().join(if cfg!(windows) {
            "docker.exe"
        } else {
            "docker"
        }),
    )
    .unwrap();
    let path = std::env::join_paths(
        std::iter::once(tools.path().to_path_buf())
            .chain(std::env::split_paths(&std::env::var_os("PATH").unwrap())),
    )
    .unwrap();
    for value in ["first", "second"] {
        let output = tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(root.path())
            .args([
                "--json",
                "run",
                "finite",
                "test",
                "--env",
                &format!("TFLOW_CLI_VALUE={value}"),
                "--env",
                "TFLOW_SIBLING_SECRET=excluded",
                "--quiet",
            ])
            .env("PATH", &path)
            .env("DOCKER_CONTEXT", "forwarding-fixture")
            .env("TFLOW_INHERITED_VALUE", "host-only")
            .output()
            .await
            .unwrap();
        assert!(output.status.success(), "{output:?}");
        let result: runner::RunResult = serde_json::from_slice(&output.stdout).unwrap();
        assert!(result.success);
        assert_eq!(
            std::fs::read_to_string(root.path().join("received")).unwrap(),
            value
        );
        let phases = std::fs::read_to_string(root.path().join(".taskflow/docker-phases")).unwrap();
        for phase in ["probe", "finite", "inventory", "shard"] {
            assert!(phases.contains(&format!("{phase}:{value}")), "{phases}");
        }
    }
}

#[tokio::test]
async fn directory_notifications_rescan_descendant_inputs() {
    let root = fixture(
        json!({"check":{"command":command(&["record","events","check"]),"input":["src/*.rs","!src/ignored.rs"],"watch":{}}}),
    );
    profile(root.path(), &["check"]);
    files::atomic_write(&root.path().join("src/main.rs"), b"first").unwrap();
    let g = graph(root.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#check"].task;
    assert!(!files::input_matches(project, task, &project.directory.join("src")).unwrap());
    assert!(files::input_event_may_match(
        project,
        task,
        &project.directory.join("src")
    ));
    assert!(!files::input_event_may_match(
        project,
        task,
        &project.directory.join("unrelated")
    ));
    assert!(!files::input_event_may_match(
        project,
        task,
        &project.directory.join(".taskflow")
    ));
    let cancel = CancellationToken::new();
    let stop = cancel.clone();
    let directory = root.path().to_path_buf();
    let session = tokio::spawn(async move {
        taskflow::session::start(&directory, "default", RunOptions::default(), stop).await
    });
    wait_lines(&root.path().join("events"), "check", 1).await;
    std::fs::rename(root.path().join("src"), root.path().join("moved")).unwrap();
    wait_lines(&root.path().join("events"), "check", 2).await;
    std::fs::rename(root.path().join("moved"), root.path().join("src")).unwrap();
    wait_lines(&root.path().join("events"), "check", 3).await;
    std::fs::remove_dir_all(root.path().join("src")).unwrap();
    wait_lines(&root.path().join("events"), "check", 4).await;
    cancel.cancel();
    session.await.unwrap().unwrap();
    assert!(!files::input_matches(project, task, &project.directory.join("src")).unwrap());
    assert!(files::input_event_may_match(
        project,
        task,
        &project.directory.join("src")
    ));
}

#[tokio::test]
async fn cancellation_during_synchronous_cache_restore_returns_cancelled() {
    use notify::Watcher;
    let root = fixture(
        json!({"build":{"command":command(&["version"]),"input":[],"output":["out"],"cache":true,"tools":{"fixture":command(&["version"])}}}),
    );
    for index in 0..1000 {
        files::atomic_write(&root.path().join(format!("out/{index}")), b"cached").unwrap();
    }
    let g = graph(root.path()).await;
    let seeded = run(g.clone(), &["build"]).await;
    assert!(seeded.success);
    std::fs::remove_dir_all(root.path().join("out")).unwrap();
    let cancel = CancellationToken::new();
    let stop = cancel.clone();
    let mut watcher = notify::recommended_watcher(move |event: notify::Result<notify::Event>| {
        if event.is_ok_and(|event| {
            event.paths.iter().any(|path| {
                path.components().any(|part| {
                    part.as_os_str()
                        .to_string_lossy()
                        .starts_with(".taskflow-restore-")
                })
            })
        }) {
            stop.cancel();
        }
    })
    .unwrap();
    watcher
        .watch(root.path(), notify::RecursiveMode::Recursive)
        .unwrap();
    let plan = Plan::create(&g, &["build".into()], &[], false).unwrap();
    let result = runner::run_plan(g, plan, RunOptions::default(), cancel.clone())
        .await
        .unwrap();
    assert!(
        cancel.is_cancelled(),
        "restoration notification must reach the cancellation owner"
    );
    assert_eq!(result.results["app#build"].outcome, Outcome::Cancelled);
    assert_eq!(result.results["app#build"].exit_code, 130);
    assert_eq!(
        runner::previous(root.path(), "app#build")
            .unwrap()
            .execution,
        seeded.results["app#build"].execution
    );
}

#[tokio::test]
async fn invalid_local_artifacts_fall_back_to_valid_remote_entries() {
    use tokio::io::{AsyncReadExt, AsyncWriteExt};
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let directory = fixture(json!({"build": {
        "command": command(&["copy", "source", "output"]), "input":["source"], "output":["output"],
        "cache":true, "tools":{"fixture":command(&["version"])}
    }}));
    let path = directory.path().join("taskflow.yml");
    let mut config: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    config["remote"] = json!({"endpoint":format!("http://{}",listener.local_addr().unwrap()),"bucket":"fixture","namespace":"fixture","accessKeyEnv":"TFLOW_TEST_ACCESS","secretKeyEnv":"TFLOW_TEST_PRIVATE","mode":"read-only"});
    std::fs::write(path, serde_yaml::to_string(&config).unwrap()).unwrap();
    std::fs::write(directory.path().join("source"), "original").unwrap();
    let g = graph(directory.path()).await;
    let plan = Plan::create(&g, &["build".into()], &[], false).unwrap();
    let seeded = runner::run_plan(
        g,
        plan,
        RunOptions {
            force: true,
            ..Default::default()
        },
        CancellationToken::new(),
    )
    .await
    .unwrap();
    assert!(seeded.success);
    let artifact = cache::load(directory.path(), &seeded.results["app#build"].key)
        .unwrap()
        .unwrap();
    let bytes = cache::encode(&artifact).unwrap();
    let manifest =
        serde_json::to_vec(&json!({"version":1,"object":files::digest(&bytes)})).unwrap();
    let mut corrupt = artifact.clone();
    let cache::Content::File { digest, .. } = &mut corrupt.files[0].content else {
        panic!("expected file");
    };
    *digest = files::digest(b"invalid content digest");
    corrupt.output_digest = cache::output_digest(&corrupt.files).unwrap();
    cache::store(directory.path(), &corrupt).unwrap();
    std::fs::write(directory.path().join("output"), "preserve-current-output").unwrap();
    let server = tokio::spawn(async move {
        for body in [manifest, bytes] {
            let (mut stream, _) = listener.accept().await.unwrap();
            let mut request = Vec::new();
            while !request.windows(4).any(|part| part == b"\r\n\r\n") {
                assert!(stream.read_buf(&mut request).await.unwrap() > 0);
            }
            let headers = format!(
                "HTTP/1.1 200 OK\r\nContent-Length: {}\r\nConnection: close\r\n\r\n",
                body.len()
            );
            stream.write_all(headers.as_bytes()).await.unwrap();
            stream.write_all(&body).await.unwrap();
        }
    });
    let output = tokio::time::timeout(
        Duration::from_secs(30),
        tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .kill_on_drop(true)
            .arg("--root")
            .arg(directory.path())
            .args(["--json", "run", "build", "--quiet"])
            .env_remove("GITHUB_EVENT_NAME")
            .env_remove("TFLOW_UNTRUSTED_CI")
            .env("TFLOW_TEST_ACCESS", "fixture")
            .env("TFLOW_TEST_PRIVATE", "fixture")
            .output(),
    )
    .await
    .unwrap()
    .unwrap();
    assert!(output.status.success(), "{output:?}");
    let result: runner::RunResult = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(result.results["app#build"].outcome, Outcome::Restored);
    tokio::time::timeout(Duration::from_secs(10), server)
        .await
        .unwrap()
        .unwrap();
    assert_eq!(
        std::fs::read_to_string(directory.path().join("output")).unwrap(),
        "original"
    );
    cache::load(directory.path(), &artifact.key)
        .unwrap()
        .unwrap()
        .validate_integrity(&artifact.key)
        .unwrap();
}

#[tokio::test]
async fn readiness_cancellation_and_deadlines_await_probe_owners() {
    for mode in ["cancel", "readiness-timeout", "service-timeout"] {
        let mut service = json!({"command":command(&["sleep","service.pid"]),"input":[],"service":true,"readiness":{"type":"command","command":command(&["stubborn","probe.pid","probe-child.pid"]),"timeout":if mode == "readiness-timeout" { "3s" } else { "30s" }}});
        if mode == "service-timeout" {
            service["timeout"] = json!("3s");
        }
        let root = fixture(json!({"service":service}));
        profile(root.path(), &["service"]);
        let token = CancellationToken::new();
        let stop = token.clone();
        let directory = root.path().to_path_buf();
        let session = tokio::spawn(async move {
            taskflow::session::start(&directory, "default", RunOptions::default(), stop).await
        });
        wait_lines(&root.path().join("probe-child.pid"), "", 1).await;
        let started = std::time::Instant::now();
        if mode == "cancel" {
            token.cancel();
        }
        let result = tokio::time::timeout(Duration::from_secs(15), session)
            .await
            .unwrap()
            .unwrap();
        if mode != "cancel" {
            assert!(result.is_err());
        }
        assert!(
            cfg!(windows) || started.elapsed() >= Duration::from_secs(2),
            "probe cleanup was dropped: {mode}"
        );
        for name in ["service.pid", "probe.pid", "probe-child.pid"] {
            let pid = std::fs::read_to_string(root.path().join(name))
                .unwrap()
                .parse()
                .unwrap();
            if name != "probe-child.pid" {
                assert!(!pid_alive(pid), "direct child {name} survived {mode}");
            }
            tokio::time::timeout(Duration::from_secs(5), async {
                while pid_alive(pid) {
                    tokio::time::sleep(Duration::from_millis(20)).await;
                }
            })
            .await
            .unwrap_or_else(|_| panic!("{name} survived {mode}"));
        }
    }
}

#[cfg(unix)]
#[tokio::test]
async fn cli_metadata_cancellation_returns_130_without_fallback() {
    let root = fixture(json!({}));
    std::fs::write(root.path().join("Cargo.toml"), "[workspace]\nmembers=[]\n").unwrap();
    let tools = tempfile::tempdir().unwrap();
    std::fs::hard_link(helper(), tools.path().join("cargo")).unwrap();
    let path = std::env::join_paths(
        std::iter::once(tools.path().to_path_buf())
            .chain(std::env::split_paths(&std::env::var_os("PATH").unwrap())),
    )
    .unwrap();
    let child = tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .arg("--root")
        .arg(root.path())
        .args(["--json", "check"])
        .env("PATH", path)
        .stdout(std::process::Stdio::piped())
        .stderr(std::process::Stdio::piped())
        .kill_on_drop(true)
        .spawn()
        .unwrap();
    wait_lines(&root.path().join("metadata.pid"), "", 1).await;
    nix::sys::signal::kill(
        nix::unistd::Pid::from_raw(child.id().unwrap() as i32),
        nix::sys::signal::Signal::SIGINT,
    )
    .unwrap();
    let output = tokio::time::timeout(Duration::from_secs(10), child.wait_with_output())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(output.status.code(), Some(130), "{output:?}");
    assert!(output.stdout.is_empty());
    assert!(String::from_utf8_lossy(&output.stderr).contains("command cancelled"));
    assert_eq!(
        std::fs::read_to_string(root.path().join("metadata-calls")).unwrap(),
        "metadata\n"
    );
    let pid = std::fs::read_to_string(root.path().join("metadata.pid"))
        .unwrap()
        .parse()
        .unwrap();
    assert!(!pid_alive(pid));
}

#[cfg(unix)]
#[tokio::test]
async fn non_utf8_paths_never_collapse_into_cache_identities() {
    use std::{ffi::OsString, os::unix::ffi::OsStringExt};
    let root = fixture(
        json!({"build":{"command":command(&["version"]),"input":["src/**"],"output":["out"]}}),
    );
    let g = graph(root.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    for byte in [0x80, 0x81] {
        let name = OsString::from_vec(vec![b'f', byte]);
        let path = root.path().join("src").join(&name);
        // APFS rejects malformed UTF-8 names itself; Linux additionally proves
        // rejection when distinct byte names are present on the filesystem.
        if cfg!(target_os = "linux") {
            files::atomic_write(&path, b"input").unwrap();
        }
        assert!(files::input_matches(project, task, &path).is_err());
        assert!(files::slash(&path)
            .unwrap_err()
            .to_string()
            .contains("UTF-8"));
    }
    if cfg!(target_os = "linux") {
        let error = files::input_state(&g.workspace, project, task).unwrap_err();
        assert!(error.to_string().contains("UTF-8"));
        let output = root.path().join("out").join(OsString::from_vec(vec![0x82]));
        files::atomic_write(&output, b"output").unwrap();
        assert!(cache::snapshot(project, task)
            .unwrap_err()
            .to_string()
            .contains("UTF-8"));
        assert!(cache::output_state(project, task).is_err());
        std::fs::remove_dir_all(root.path().join("src")).unwrap();
        std::fs::remove_dir_all(root.path().join("out")).unwrap();
    }
    let bad_root = root.path().join(OsString::from_vec(vec![0x83]));

    assert!(Workspace::discover(&bad_root)
        .await
        .unwrap_err()
        .to_string()
        .contains("UTF-8"));
    files::atomic_write(&root.path().join("src/日本語.rs"), b"valid").unwrap();
    assert!(files::input_state(&g.workspace, project, task)
        .unwrap()
        .contains_key("src/日本語.rs"));
}

#[test]
fn check_rejects_invalid_task_environment_before_execution() {
    for fields in [
        json!({"env":{"":"value"}}),
        json!({"env":{"A=B":"value"}}),
        json!({"env":{"A\0B":"value"}}),
        json!({"env":{"VALID":"secret\0value"}}),
        json!({"envInputs":["A=B"]}),
        json!({"secrets":["A\0B"]}),
    ] {
        let mut task = fields;
        task["command"] = command(&["write", "unexpected", "executed"]);
        let directory = fixture(json!({"task":task}));
        let error = config::load(&directory.path().join("taskflow.yml")).unwrap_err();
        assert!(format!("{error:#}").contains("environment"));
        let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(directory.path())
            .arg("check")
            .output()
            .unwrap();
        assert!(!output.status.success());
        assert!(!String::from_utf8_lossy(&output.stderr).contains("secret"));
        assert!(!directory.path().join("unexpected").exists());
    }
    let valid: config::Task = serde_json::from_value(
        json!({"command":["unused"],"env":{"EXAMPLE":"equals=and\nUnicode: 日本語"}}),
    )
    .unwrap();
    valid.validate().unwrap();
}

#[tokio::test]
async fn outputless_cached_prerequisites_preserve_semantic_result_identity() {
    for kind in ["check", "unchanged", "shard"] {
        let mut check = json!({"command":command(&[if kind == "unchanged" { "unchanged" } else { "record" },"events","check"]),"input":["source"],"output":[],"cache":true,"tools":{"fixture":command(&["version"])}});
        if kind == "shard" {
            check["shard"] = json!({"adapter":"generic","count":2,"list":command(&["inventory"]),"run":command(&["shard"])});
        }
        let root = fixture(
            json!({"check":check,"build":{"command":command(&["copy","source","result"]),"input":[],"output":["result"],"dependsOn":["check"],"cache":true,"tools":{"fixture":command(&["version"])}}}),
        );
        std::fs::write(root.path().join("source"), "first").unwrap();
        let g = graph(root.path()).await;
        let first = run(g.clone(), &["build"]).await;
        assert!(first.success, "{first:?}");
        assert_eq!(
            first.results["app#check"].output,
            first.results["app#check"].key
        );
        let hit = run(g.clone(), &["build"]).await;
        assert_eq!(hit.results["app#check"].outcome, Outcome::LocalCache);
        assert_eq!(hit.results["app#build"].outcome, Outcome::LocalCache);
        for value in ["second", "first"] {
            std::fs::write(root.path().join("source"), value).unwrap();
            let plan = Plan::create(&g, &[], &[PathBuf::from("source")], true).unwrap();
            let result = runner::run_plan(
                g.clone(),
                plan,
                RunOptions::default(),
                CancellationToken::new(),
            )
            .await
            .unwrap();
            assert!(result.success, "{kind}: {result:?}");
            if kind == "unchanged" {
                assert_eq!(
                    result.results["app#check"].output,
                    first.results["app#check"].output
                );
                assert_eq!(result.results["app#build"].outcome, Outcome::Suppressed);
            } else {
                assert!(result.results["app#check"].changed, "{kind}: {result:?}");
                assert_eq!(
                    result.results["app#check"].output,
                    result.results["app#check"].key
                );
                assert_eq!(
                    result.results["app#build"].outcome,
                    if value == "second" {
                        Outcome::Executed
                    } else {
                        Outcome::Restored
                    }
                );
                assert_eq!(
                    std::fs::read_to_string(root.path().join("result")).unwrap(),
                    value
                );
            }
        }
        if kind == "shard" {
            let mut keys = BTreeSet::new();
            for index in 0..2 {
                let plan = Plan::create(&g, &["check".into()], &[], false).unwrap();
                let result = runner::run_plan(
                    g.clone(),
                    plan,
                    RunOptions {
                        shard: Some((index, 2)),
                        ..Default::default()
                    },
                    CancellationToken::new(),
                )
                .await
                .unwrap();
                assert!(result.success, "{result:?}");
                assert_eq!(
                    result.results["app#check"].output,
                    first.results["app#check"].output
                );
                keys.insert(result.results["app#check"].key.clone());
            }
            assert_eq!(
                keys.len(),
                2,
                "cache entries still partition shard selections"
            );
        }
        let key = &first.results["app#check"].key;
        let mut legacy = cache::load(root.path(), key).unwrap().unwrap();
        legacy.result_identity = None;
        assert!(
            legacy.validate_integrity(key).is_err(),
            "old empty snapshot identities must miss"
        );
    }
}

#[cfg(unix)]
#[tokio::test]
async fn unix_backslash_paths_cannot_alias_directory_paths() {
    let root = fixture(json!({"check":{"command":command(&["version"]),"output":["out"]}}));
    std::fs::create_dir_all(root.path().join("a")).unwrap();
    std::fs::write(root.path().join("a/b"), "directory file").unwrap();
    std::fs::write(root.path().join(r"a\b"), "literal file").unwrap();
    let g = graph(root.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#check"].task;
    assert!(files::input_state(&g.workspace, project, task)
        .unwrap_err()
        .to_string()
        .contains("literal backslashes"));
    std::fs::create_dir_all(root.path().join("out/a")).unwrap();
    std::fs::write(root.path().join("out/a/b"), "directory output").unwrap();
    std::fs::write(root.path().join(r"out/a\b"), "literal output").unwrap();
    assert!(cache::snapshot(project, task)
        .unwrap_err()
        .to_string()
        .contains("literal backslashes"));
}

#[cfg(unix)]
#[tokio::test]
async fn captured_output_link_chains_never_leave_the_project() {
    let root =
        fixture(json!({"check":{"command":command(&["version"]),"input":[],"output":["out"]}}));
    let outside = tempfile::tempdir().unwrap();
    std::fs::create_dir(root.path().join("out")).unwrap();
    std::fs::write(root.path().join("source"), "inside").unwrap();
    std::os::unix::fs::symlink(outside.path(), root.path().join("bridge")).unwrap();
    std::os::unix::fs::symlink(root.path(), outside.path().join("back")).unwrap();
    // The final target is internal, but reaching it crosses an external prefix.
    std::os::unix::fs::symlink("../bridge/back/source", root.path().join("out/link")).unwrap();
    let g = graph(root.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#check"].task;
    assert!(cache::snapshot(project, task).is_err());
    assert!(cache::output_state(project, task).is_err());
    let result = run(g.clone(), &["check"]).await;
    assert!(!result.success);
    std::fs::remove_file(root.path().join("out/link")).unwrap();
    std::os::unix::fs::symlink("../source", root.path().join("out/link")).unwrap();
    assert!(cache::snapshot(project, task).is_ok());
    assert!(cache::output_state(project, task).is_ok());
}

#[tokio::test]
async fn shard_deadline_includes_inventory_and_all_units() {
    for slow_inventory in [true, false] {
        let root = fixture(
            json!({"suite":{"command":command(&["version"]),"input":[],"output":[],"timeout":"3s","shard":{"adapter":"generic","count":3,"list":command(&["delay",if slow_inventory {"10000"} else {"400"},"inventory"]),"run":command(&["delay","1100","shard"])}}}),
        );
        let g = graph(root.path()).await;
        let result = tokio::time::timeout(Duration::from_secs(8), run(g, &["suite"]))
            .await
            .unwrap();
        let receipt = &result.results["app#suite"];
        assert_eq!(receipt.outcome, Outcome::Failed, "{result:?}");
        assert_eq!(receipt.exit_code, 124, "{result:?}");
        assert!(receipt.diagnostic.as_ref().unwrap().contains("timeout"));
        if !slow_inventory {
            let (inventory, reports) =
                shard::read_reports(&root.path().join(".taskflow/runs").join(&receipt.execution))
                    .unwrap();
            assert_eq!(reports.len(), 3);
            assert!(!shard::aggregate(&inventory, 3, &reports).unwrap());
        }
    }
}

#[cfg(unix)]
#[tokio::test]
async fn dangling_input_links_track_target_deletion_and_recreation() {
    for auto in [true, false] {
        let mut task = json!({"command":command(&["version"]),"output":[]});
        if !auto {
            task["input"] = json!(["link"]);
        }
        let root = fixture(json!({"check":task}));
        std::fs::write(root.path().join("target-file"), "first").unwrap();
        std::os::unix::fs::symlink("target-file", root.path().join("link")).unwrap();
        let g = graph(root.path()).await;
        let snapshot = || {
            files::input_state(
                &g.workspace,
                &g.workspace.projects["app"],
                &g.tasks["app#check"].task,
            )
        };
        let first = snapshot().unwrap();
        std::fs::remove_file(root.path().join("target-file")).unwrap();
        let missing = snapshot().unwrap();
        assert_eq!(missing["link"], "link:target-file:missing");
        assert_ne!(first["link"], missing["link"]);
        assert!(run(g.clone(), &["check"]).await.success);
        std::fs::write(root.path().join("target-file"), "second").unwrap();
        assert_ne!(snapshot().unwrap()["link"], first["link"]);
        std::fs::remove_file(root.path().join("link")).unwrap();
        std::os::unix::fs::symlink(root.path().join("target-file"), root.path().join("link"))
            .unwrap();
        assert!(
            snapshot().is_ok(),
            "internal absolute links may use a filesystem alias for the root"
        );
        std::fs::remove_file(root.path().join("target-file")).unwrap();
        assert!(snapshot().unwrap()["link"].ends_with(":missing"));
        std::fs::remove_file(root.path().join("link")).unwrap();
        std::os::unix::fs::symlink("../missing-outside", root.path().join("link")).unwrap();
        assert!(
            snapshot().is_err(),
            "missing targets cannot bypass containment"
        );
        std::fs::remove_file(root.path().join("link")).unwrap();
        std::os::unix::fs::symlink("link", root.path().join("link")).unwrap();
        assert!(snapshot().is_err(), "cycles are not missing targets");
    }
}

#[tokio::test]
async fn ready_service_identity_invalidates_cached_consumers() {
    let root = fixture(json!({
        "server":{"command":command(&["sleep","server.pid"]),"service":true,"input":["service-source"],"readiness":{"type":"command","command":command(&["version"]),"timeout":"10s"}},
        "check":{"command":command(&["record","events","check"]),"dependsOn":[{"task":"server","waitFor":"ready"}],"input":[],"output":[],"cache":true,"tools":{"fixture":command(&["version"])}}
    }));
    let mut keys = Vec::new();
    for (input, expected) in [
        ("first", Outcome::Executed),
        ("first", Outcome::LocalCache),
        ("second", Outcome::Executed),
        ("second", Outcome::LocalCache),
    ] {
        std::fs::write(root.path().join("service-source"), input).unwrap();
        let g = graph(root.path()).await;
        let plan = Plan::create(&g, &["check".into()], &[], false).unwrap();
        let (events, _receiver) = tokio::sync::mpsc::unbounded_channel();
        let services = Arc::new(runner::Services {
            controls: Default::default(),
            events,
            joins: Default::default(),
        });
        let result = runner::run_plan(
            g,
            plan,
            RunOptions {
                services: Some(services.clone()),
                quiet: true,
                ..Default::default()
            },
            CancellationToken::new(),
        )
        .await;
        services.shutdown().await.unwrap();
        let result = result.unwrap();
        assert!(result.success, "{result:?}");
        assert_eq!(result.results["app#check"].outcome, expected);
        assert_eq!(
            result.results["app#server"].output,
            result.results["app#server"].key
        );
        keys.push(result.results["app#check"].key.clone());
    }
    assert_eq!(keys[0], keys[1]);
    assert_ne!(keys[1], keys[2]);
    assert_eq!(keys[2], keys[3]);
}

#[tokio::test]
async fn shard_partitions_reuse_unsharded_prerequisite_cache_keys() {
    let root = fixture(json!({
        "prepare":{"command":command(&["record","events","prepare"]),"input":[],"output":[],"cache":true,"tools":{"fixture":command(&["version"])}},
        "suite":{"command":command(&["version"]),"dependsOn":["prepare"],"input":[],"output":[],"shard":{"adapter":"generic","count":2,"list":command(&["inventory"]),"run":command(&["shard"])}}
    }));
    let g = graph(root.path()).await;
    let mut prerequisites = BTreeSet::new();
    let mut suites = BTreeSet::new();
    for index in 0..2 {
        let plan = Plan::create(&g, &["suite".into()], &[], false).unwrap();
        let result = runner::run_plan(
            g.clone(),
            plan,
            RunOptions {
                shard: Some((index, 2)),
                ..Default::default()
            },
            CancellationToken::new(),
        )
        .await
        .unwrap();
        assert!(result.success, "{result:?}");
        assert_eq!(
            result.results["app#prepare"].outcome,
            if index == 0 {
                Outcome::Executed
            } else {
                Outcome::LocalCache
            }
        );
        prerequisites.insert(result.results["app#prepare"].key.clone());
        suites.insert(result.results["app#suite"].key.clone());
    }
    assert_eq!(prerequisites.len(), 1);
    assert_eq!(suites.len(), 2);
    assert_eq!(
        std::fs::read_to_string(root.path().join("events")).unwrap(),
        "prepare\n"
    );
}

#[tokio::test]
async fn shard_deadlines_leave_docker_cleanup_available() {
    for list in ["inventory", "slow-inventory"] {
        let root = fixture(
            json!({"suite":{"command":["fixture","unit"],"input":[],"output":[],"timeout":"2s","shard":{"adapter":"generic","count":1,"list":["fixture",list],"run":["fixture","unit"]},"platform":{"executor":"docker","os":"linux","image":"fixture@sha256:0000000000000000000000000000000000000000000000000000000000000000"}}}),
        );
        let tools = tempfile::tempdir().unwrap();
        std::fs::hard_link(
            helper(),
            tools.path().join(if cfg!(windows) {
                "docker.exe"
            } else {
                "docker"
            }),
        )
        .unwrap();
        let path = std::env::join_paths(
            std::iter::once(tools.path().to_path_buf())
                .chain(std::env::split_paths(&std::env::var_os("PATH").unwrap())),
        )
        .unwrap();
        let output = tokio::time::timeout(
            Duration::from_secs(10),
            tokio::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(root.path())
                .args(["--json", "run", "suite", "--quiet"])
                .env("PATH", path)
                .env("DOCKER_CONTEXT", "deadline-fixture")
                .output(),
        )
        .await
        .unwrap()
        .unwrap();
        assert!(!output.status.success());
        let result: runner::RunResult = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(result.results["app#suite"].exit_code, 124, "{output:?}");
        let cleanup = std::fs::read_to_string(root.path().join("cleanup-events")).unwrap();
        assert!(cleanup.lines().count() >= if list == "inventory" { 2 } else { 1 });
        let pid = std::fs::read_to_string(root.path().join("unit.pid"))
            .unwrap()
            .parse()
            .unwrap();
        assert!(!pid_alive(pid));
    }
}

#[tokio::test]
async fn changed_outputs_override_unchanged_reports() {
    for cached in [false, true] {
        let root = fixture(json!({
            "produce":{"command":command(&["copy-unchanged","source","produced",env!("CARGO_BIN_EXE_tflow")]),"input":["source"],"output":["produced"],"cache":cached,"tools":{"fixture":command(&["version"])}},
            "consume":{"command":command(&["copy","produced","consumed"]),"input":[],"output":["consumed"],"dependsOn":["produce"]}
        }));
        let g = graph(root.path()).await;
        // Only the producer has an independent cause. The consumer must rely on
        // output propagation, including when the producer has no prior receipt.
        for (source, expected_changed) in [("first", true), ("second", true), ("second", false)] {
            std::fs::write(root.path().join("source"), source).unwrap();
            let plan = Plan::create(&g, &[], &[PathBuf::from("source")], true).unwrap();
            let result = runner::run_plan(
                g.clone(),
                plan,
                RunOptions {
                    force: true,
                    ..Default::default()
                },
                CancellationToken::new(),
            )
            .await
            .unwrap();
            assert!(result.success, "{result:?}");
            assert_eq!(result.results["app#produce"].changed, expected_changed);
            assert_eq!(
                result.results["app#consume"].outcome,
                if expected_changed {
                    Outcome::Executed
                } else {
                    Outcome::Suppressed
                }
            );
            assert_eq!(
                std::fs::read_to_string(root.path().join("consumed")).unwrap(),
                source
            );
        }
    }
}

#[test]
fn check_validates_remote_syntax_without_credentials() {
    let root = fixture(json!({}));
    let path = root.path().join("taskflow.yml");
    let valid = json!({"endpoint":"https://cache.example.test","bucket":"fixture","namespace":"team/project","accessKeyEnv":"TFLOW_ABSENT_REMOTE_ACCESS","secretKeyEnv":"TFLOW_ABSENT_REMOTE_SECRET","sessionTokenEnv":"TFLOW_ABSENT_REMOTE_TOKEN"});
    let check = |remote: Value| {
        files::atomic_write(
            &path,
            serde_yaml::to_string(&json!({"version":1,"project":"app","remote":remote}))
                .unwrap()
                .as_bytes(),
        )
        .unwrap();
        std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(root.path())
            .arg("check")
            .env_remove("TFLOW_ABSENT_REMOTE_ACCESS")
            .env_remove("TFLOW_ABSENT_REMOTE_SECRET")
            .env_remove("TFLOW_ABSENT_REMOTE_TOKEN")
            .output()
            .unwrap()
    };
    for endpoint in [
        "https://cache.example.test",
        "http://127.0.0.1:1",
        "http://[::1]:1",
        "http://localhost:1",
    ] {
        let mut remote = valid.clone();
        remote["endpoint"] = json!(endpoint);
        let output = check(remote);
        assert!(output.status.success(), "{output:?}");
    }
    for (field, values) in [
        (
            "endpoint",
            vec![
                "invalid",
                "http://cache.example.test",
                "https://user:fixture-value@cache.example.test",
                "https://cache.example.test/path",
                "https://cache.example.test?secret=fixture-value",
                "https://cache.example.test#fragment",
            ],
        ),
        ("bucket", vec!["", "../bucket", "a/b"]),
        ("namespace", vec!["", "a//b", "a/../b"]),
        ("region", vec!["", "a/b", "a\nb"]),
        ("accessKeyEnv", vec!["", "A=B", "A\0B"]),
        ("secretKeyEnv", vec!["", "A=B", "A\0B"]),
        ("sessionTokenEnv", vec!["", "A=B", "A\0B"]),
    ] {
        for value in values {
            for mode in ["read-only", "read-write", "off"] {
                let mut remote = valid.clone();
                remote[field] = json!(value);
                remote["mode"] = json!(mode);
                let output = check(remote);
                assert!(
                    !output.status.success(),
                    "accepted {field}={value:?} ({mode})"
                );
                let diagnostic = String::from_utf8_lossy(&output.stderr);
                assert!(diagnostic.contains("remote"), "{diagnostic}");
                assert!(!diagnostic.contains("fixture-value"), "{diagnostic}");
            }
        }
    }
}

#[tokio::test]
async fn either_subscription_can_request_one_initial_execution() {
    let mut tasks = serde_json::Map::new();
    let mut expected = BTreeSet::new();
    for (w, watch) in [None, Some(false), Some(true)].into_iter().enumerate() {
        for (s, schedule) in [None, Some(false), Some(true)].into_iter().enumerate() {
            let id = format!("task{w}{s}");
            let mut task = json!({"command":command(&["record",&id,"ran"]),"input":[]});
            if let Some(initial) = watch {
                task["watch"] = json!({"initial":initial});
            }
            if let Some(initial) = schedule {
                task["schedule"] = json!({"initial":initial,"every":"1d"});
            }
            if (watch.is_none() && schedule.is_none())
                || watch == Some(true)
                || schedule == Some(true)
            {
                expected.insert(id.clone());
            }
            tasks.insert(id, task);
        }
    }
    let root = fixture(Value::Object(tasks.clone()));
    profile(
        root.path(),
        &tasks.keys().map(String::as_str).collect::<Vec<_>>(),
    );
    let cancel = CancellationToken::new();
    let directory = root.path().to_owned();
    let token = cancel.clone();
    let session = tokio::spawn(async move {
        taskflow::session::start(&directory, "default", RunOptions::default(), token).await
    });
    for id in &expected {
        wait_lines(&root.path().join(id), "ran", 1).await;
    }
    cancel.cancel();
    tokio::time::timeout(Duration::from_secs(10), session)
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    for id in tasks.keys() {
        let contents = std::fs::read_to_string(root.path().join(id)).unwrap_or_default();
        assert_eq!(
            contents,
            if expected.contains(id) { "ran\n" } else { "" },
            "{id}"
        );
    }
}

#[tokio::test]
async fn cache_rejects_nonportable_link_targets_on_every_host() {
    let root = fixture(json!({"build":{"command":command(&["version"]),"output":["out/**"]}}));
    files::atomic_write(&root.path().join("out/keep"), b"preserved").unwrap();
    let g = graph(root.path()).await;
    let project = &g.workspace.projects["app"];
    let task = &g.tasks["app#build"].task;
    let mut artifact = cache::Artifact::capture(
        files::digest(b"portable-links"),
        "app#build".into(),
        project,
        task,
    )
    .unwrap();
    for target in [
        "C:/temp",
        "z:temp",
        "/rooted",
        "//server/share",
        "\\rooted",
        "\\\\server\\share",
        "\\\\?\\C:\\temp",
        "sub\\file",
        "",
        "bad\0target",
    ] {
        artifact.files = vec![
            cache::FileRecord {
                path: "out".into(),
                content: cache::Content::Directory,
            },
            cache::FileRecord {
                path: "out/link".into(),
                content: cache::Content::Link {
                    target: target.into(),
                    directory: false,
                },
            },
        ];
        artifact.output_digest = cache::output_digest(&artifact.files).unwrap();
        assert!(
            artifact.validate_integrity(&artifact.key).is_err(),
            "{target:?}"
        );
        assert!(
            artifact
                .restore(&artifact.key, "app#build", project, task)
                .is_err(),
            "{target:?}"
        );
        assert_eq!(
            std::fs::read(root.path().join("out/keep")).unwrap(),
            b"preserved"
        );
    }
    #[cfg(unix)]
    for target in ["C:/temp", "z:temp"] {
        // These are real relative paths on Unix. Reject them at the producer
        // before another platform can reinterpret their drive prefixes.
        files::atomic_write(&root.path().join("out").join(target), b"internal").unwrap();
        std::os::unix::fs::symlink(target, root.path().join("out/link")).unwrap();
        for result in [
            cache::snapshot(project, task).map(|_| ()),
            cache::output_state(project, task).map(|_| ()),
        ] {
            assert!(result
                .unwrap_err()
                .to_string()
                .contains("portable relative"));
        }
        std::fs::remove_file(root.path().join("out/link")).unwrap();
    }
}

#[tokio::test]
async fn git_revision_operands_cannot_be_diff_options() {
    let root = fixture(json!({"check":{"command":command(&["version"]),"input":["src/**"]}}));
    let git = |args: &[&str]| {
        let output = std::process::Command::new("git")
            .current_dir(root.path())
            .args([
                "-c",
                "user.name=Fixture",
                "-c",
                "user.email=fixture@example.test",
                "-c",
                "commit.gpgsign=false",
                "-c",
                "core.hooksPath=.git/no-hooks",
            ])
            .args(args)
            .output()
            .unwrap();
        assert!(output.status.success(), "{output:?}");
    };
    git(&["init", "-q"]);
    files::atomic_write(&root.path().join("src/old"), b"before").unwrap();
    git(&["add", "."]);
    git(&["commit", "-qm", "baseline"]);
    git(&["mv", "src/old", "src/new"]);
    git(&["commit", "-qm", "rename"]);
    assert_eq!(
        taskflow::plan::git_changes(root.path(), "HEAD~1", Some("HEAD"))
            .await
            .unwrap(),
        [PathBuf::from("src/new"), PathBuf::from("src/old")]
    );
    files::atomic_write(&root.path().join("src/untracked"), b"untracked").unwrap();
    assert_eq!(
        taskflow::plan::git_changes(root.path(), "HEAD", None)
            .await
            .unwrap(),
        [PathBuf::from("src/untracked")]
    );
    let destination = root.path().join("option-output");
    let output_option = format!("--output={}", destination.display());
    for operand in [output_option.as_str(), "--stat", "--no-index", ""] {
        for (base, head) in [
            (operand, None),
            (operand, Some("HEAD")),
            ("HEAD", Some(operand)),
        ] {
            let error = taskflow::plan::git_changes(root.path(), base, head)
                .await
                .unwrap_err();
            assert!(error.to_string().contains("Git revision"), "{error:#}");
            let mut cli = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"));
            cli.current_dir(root.path())
                .args(["plan", &format!("--base={base}")]);
            if let Some(head) = head {
                cli.arg(format!("--head={head}"));
            }
            assert!(!cli.output().unwrap().status.success());
            assert!(!destination.exists());
        }
    }
}

#[tokio::test]
async fn queued_discovery_mutations_run_watchers_without_initial_execution() {
    let root = fixture(
        json!({"check":{"command":command(&["copy","source","observed"]),"input":["source"],"output":["observed"],"watch":{"initial":false,"debounce":"20ms"}}}),
    );
    profile(root.path(), &["check"]);
    std::fs::write(root.path().join("Cargo.toml"), "[workspace]\nmembers=[]\n").unwrap();
    std::fs::write(root.path().join("source"), "before").unwrap();
    let tools = tempfile::tempdir().unwrap();
    std::fs::hard_link(
        helper(),
        tools
            .path()
            .join(if cfg!(windows) { "cargo.exe" } else { "cargo" }),
    )
    .unwrap();
    let path = std::env::join_paths(
        std::iter::once(tools.path().to_path_buf())
            .chain(std::env::split_paths(&std::env::var_os("PATH").unwrap())),
    )
    .unwrap();
    let mut environment: BTreeMap<String, String> = std::env::vars().collect();
    environment.retain(|name, _| {
        if cfg!(windows) {
            !name.eq_ignore_ascii_case("PATH")
        } else {
            name != "PATH"
        }
    });
    environment.insert("PATH".into(), path.into_string().unwrap());
    environment.insert("TFLOW_METADATA_GATE".into(), "1".into());
    let stop = CancellationToken::new();
    let token = stop.clone();
    let directory = root.path().to_owned();
    let mut process = tokio::spawn(async move {
        taskflow::process::capture_with_env(
            &directory,
            &config::Command::Argv(vec![
                env!("CARGO_BIN_EXE_tflow").into(),
                "--root".into(),
                ".".into(),
                "start".into(),
            ]),
            &environment,
            &token,
        )
        .await
    });
    tokio::time::timeout(Duration::from_secs(10), async {
        while !root.path().join("metadata.pid").exists() {
            if process.is_finished() {
                panic!(
                    "session exited before metadata barrier: {:?}",
                    (&mut process).await
                );
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    std::fs::write(root.path().join("source"), "during-discovery").unwrap();
    // Keep discovery behind a barrier while native notifications are delivered.
    tokio::time::sleep(Duration::from_millis(500)).await;
    files::atomic_write(&root.path().join(".taskflow/metadata.release"), b"release").unwrap();
    let observed = tokio::time::timeout(Duration::from_secs(10), async {
        loop {
            if let Ok(value) = std::fs::read_to_string(root.path().join("observed")) {
                break value;
            }
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await;
    stop.cancel();
    assert!(process.await.unwrap().is_err());
    assert_eq!(observed.unwrap(), "during-discovery");
}

#[test]
fn libtest_cross_targets_require_generic_before_execution() {
    for selection in [
        vec!["--target", "aarch64-unknown-linux-gnu"],
        vec!["--target=aarch64-unknown-linux-gnu"],
    ] {
        let args: Vec<_> = ["cargo", "test"].into_iter().chain(selection).collect();
        let task =
            json!({"command":args,"dependsOn":["install"],"shard":{"adapter":"libtest","count":2}});
        let parsed: config::Task = serde_json::from_value(task.clone()).unwrap();
        assert!(parsed
            .validate()
            .unwrap_err()
            .to_string()
            .contains("target runners"));
        let root = fixture(json!({
            "install":{"command":command(&["write","unexpected","executed"])},
            "test":task
        }));
        for args in [vec!["check"], vec!["run", "test"]] {
            let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(root.path())
                .args(["--root", "."])
                .args(args)
                .output()
                .unwrap();
            assert!(!output.status.success());
            assert!(
                String::from_utf8_lossy(&output.stderr).contains("app#test"),
                "{output:?}"
            );
            assert!(!root.path().join("unexpected").exists());
        }
        let generic: config::Task = serde_json::from_value(json!({"command":args,"shard":{"adapter":"generic","count":2,"list":command(&["inventory"]),"run":command(&["shard"])}})).unwrap();
        generic.validate().unwrap();
    }
}

#[test]
fn docker_digest_references_validate_the_complete_suffix() {
    let digest = "ab01".repeat(16);
    let images = [
        (format!("repo@sha256:{digest}"), true),
        (
            format!("registry.example:5000/team/repo:tag@sha256:{digest}"),
            true,
        ),
        (format!("repo@sha256:not-a-digest:{digest}"), false),
        (format!("repo@sha256:{digest}@sha256:{digest}"), false),
        (format!("@sha256:{digest}"), false),
        (format!("bad name@sha256:{digest}"), false),
        (format!("repo@sha256:{digest}:extra"), false),
        (format!("repo@sha256:{digest}\n"), false),
        (format!("repo@sha256:{}", digest.to_uppercase()), false),
        (format!("repo@sha256:{}", &digest[..63]), false),
        (format!("repo@sha256:{digest}0"), false),
        (format!("repo@sha512:{digest}"), false),
    ];
    for (image, accepted) in images {
        let task = json!({"command":["unused"],"platform":{"executor":"docker","os":"linux","image":image}});
        let parsed: config::Task = serde_json::from_value(task.clone()).unwrap();
        assert_eq!(parsed.validate().is_ok(), accepted, "{image}");
        let root = fixture(json!({"container":task}));
        let result = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
            .current_dir(root.path())
            .args(["--root", ".", "check"])
            .output()
            .unwrap();
        assert_eq!(result.status.success(), accepted, "{image}: {result:?}");
    }
}

#[tokio::test]
async fn overlap_replacements_finish_before_independent_waves_and_keep_latest_receipts() {
    for overlap in ["queue", "restart"] {
        let listener = std::net::TcpListener::bind("127.0.0.1:0").unwrap();
        let address = listener.local_addr().unwrap().to_string();
        drop(listener);
        let root = fixture(json!({
            "fast":{"command":command(&["paced","events",&address,"900"]),"input":["source"],"watch":{"debounce":"20ms"},"overlap":overlap},
            "slow":{"command":command(&["gated","slow-start","release"]),"input":[]}
        }));
        profile(root.path(), &["fast", "slow"]);
        std::fs::write(root.path().join("source"), "first").unwrap();
        let options = RunOptions {
            jobs: 2,
            ..Default::default()
        };
        let running = options.task_cancellations.clone();
        let stop = CancellationToken::new();
        let token = stop.clone();
        let directory = root.path().to_owned();
        let session = tokio::spawn(async move {
            taskflow::session::start(&directory, "default", options, token).await
        });
        wait_lines(&root.path().join("events"), "start:", 1).await;
        wait_lines(&root.path().join("slow-start"), "", 1).await;
        std::fs::write(root.path().join("source"), "second").unwrap();
        wait_lines(&root.path().join("events"), "start:", 2).await;
        assert!(running.lock().unwrap().contains_key("app#slow"));
        let latest = tokio::time::timeout(Duration::from_secs(10), async {
            loop {
                if let Some(receipt) = runner::previous(root.path(), "app#fast") {
                    if receipt.success()
                        && receipt
                            .causes
                            .iter()
                            .any(|cause| matches!(cause, Cause::Input { .. }))
                    {
                        break receipt;
                    }
                }
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        // Complete the old wave last, then confirm its stale receipt cannot
        // replace the latest run or release another wave's ownership.
        std::fs::write(root.path().join("release"), "release").unwrap();
        tokio::time::timeout(Duration::from_secs(10), async {
            while running.lock().unwrap().contains_key("app#slow") {
                tokio::time::sleep(Duration::from_millis(20)).await;
            }
        })
        .await
        .unwrap();
        stop.cancel();
        let result = session.await.unwrap().unwrap();
        assert_eq!(result.results["app#fast"].execution, latest.execution);
        assert!(result.results["app#fast"].success());
        let events = std::fs::read_to_string(root.path().join("events")).unwrap();
        assert_eq!(
            events
                .lines()
                .filter(|line| line.starts_with("start:"))
                .count(),
            2,
            "{events}"
        );
        assert_eq!(
            events
                .lines()
                .filter(|line| line.starts_with("end:"))
                .count(),
            if overlap == "queue" { 2 } else { 1 },
            "{events}"
        );
    }
}

#[tokio::test]
async fn new_waves_retain_prerequisite_causes_behind_active_consumers() {
    let root = fixture(json!({
        "produce":{"command":command(&["copy","source","artifact"]),"input":["source"],"output":["artifact"],"watch":{"debounce":"20ms"}},
        "consume":{"command":command(&["gated-copy","consumer-start","release","artifact","result","events"]),"dependsOn":["produce"],"input":[],"output":["result"],"watch":{"initial":false}}
    }));
    profile(root.path(), &["produce", "consume"]);
    std::fs::write(root.path().join("source"), "first").unwrap();
    let stop = CancellationToken::new();
    let token = stop.clone();
    let directory = root.path().to_owned();
    let session = tokio::spawn(async move {
        taskflow::session::start(&directory, "default", RunOptions::default(), token).await
    });
    wait_lines(&root.path().join("consumer-start"), "", 1).await;
    std::fs::write(root.path().join("source"), "second").unwrap();
    let updated = tokio::time::timeout(Duration::from_secs(10), async {
        while std::fs::read_to_string(root.path().join("artifact")).unwrap_or_default() != "second"
        {
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await;
    std::fs::write(root.path().join("release"), "release").unwrap();
    updated.unwrap();
    wait_lines(&root.path().join("events"), "consumed", 2).await;
    stop.cancel();
    session.await.unwrap().unwrap();
    assert_eq!(
        std::fs::read_to_string(root.path().join("result")).unwrap(),
        "second"
    );
}

#[tokio::test]
async fn ci_export_rejects_blank_runner_mappings_before_writing() {
    let directory =
        fixture(json!({"build":{"command":command(&["version"]),"input":[],"output":[]}}));
    let original = graph(directory.path()).await;
    for label in ["", " ", "\t\n", "runner\0label"] {
        for platform in [config::Platform::default().key(), "linux-x64".into()] {
            let mut g = (*original).clone();
            g.workspace.config.ci = Some(
                serde_json::from_value(json!({
                    "revision":"1111111111111111111111111111111111111111",
                    "rust":"nightly-2026-01-01",
                    "runners":{config::Platform::default().key():"self-hosted"}
                }))
                .unwrap(),
            );
            g.workspace
                .config
                .ci
                .as_mut()
                .unwrap()
                .runners
                .insert(platform, label.into());
            assert!(g
                .workspace
                .config
                .validate()
                .unwrap_err()
                .to_string()
                .contains("runner mapping"));
            let error =
                taskflow::ci::export(&g, vec!["build".into()], Path::new("ci.yml")).unwrap_err();
            assert!(error.to_string().contains("runner mapping"), "{error:#}");
            assert!(!directory.path().join("ci.yml").exists());
            assert!(!directory.path().join("ci.taskflow.json").exists());
        }
    }
    let path = directory.path().join("taskflow.yml");
    let mut cfg: Value = serde_yaml::from_slice(&std::fs::read(&path).unwrap()).unwrap();
    cfg["ci"] = json!({"revision":"1111111111111111111111111111111111111111","rust":"nightly-2026-01-01","runners":{config::Platform::default().key():" "}});
    std::fs::write(&path, serde_yaml::to_string(&cfg).unwrap()).unwrap();
    let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
        .current_dir(directory.path())
        .arg("check")
        .output()
        .unwrap();
    assert!(!output.status.success());
    assert!(String::from_utf8_lossy(&output.stderr).contains("runner mapping"));
}

#[test]
fn check_rejects_invalid_docker_ports_before_prerequisites() {
    let image = format!("fixture@sha256:{}", "a".repeat(64));
    for port in ["8080:80\0", "-p8080:80", "", " "] {
        let directory = fixture(json!({
            "prepare":{"command":command(&["write","unexpected","executed"]),"input":[]},
            "container":{"command":["true"],"input":[],"dependsOn":["prepare"],"platform":{"executor":"docker","os":"linux","image":image,"ports":[port]}}
        }));
        let error = config::load(&directory.path().join("taskflow.yml")).unwrap_err();
        assert!(format!("{error:#}").contains("Docker port mapping"));
        for args in [vec!["check"], vec!["run", "container"]] {
            let output = std::process::Command::new(env!("CARGO_BIN_EXE_tflow"))
                .current_dir(directory.path())
                .args(args)
                .output()
                .unwrap();
            assert!(!output.status.success());
            assert!(
                String::from_utf8_lossy(&output.stderr).contains("app#container"),
                "{output:?}"
            );
            assert!(!directory.path().join("unexpected").exists());
        }
    }
    for port in [
        "80",
        "8080:80",
        "127.0.0.1:3000:3000/tcp",
        "[::1]:8080:80",
        "8000-8010:8000-8010/udp",
    ] {
        let task: config::Task = serde_json::from_value(json!({"command":["true"],"platform":{"executor":"docker","os":"linux","image":image,"ports":[port]}})).unwrap();
        task.validate().unwrap();
    }
}
