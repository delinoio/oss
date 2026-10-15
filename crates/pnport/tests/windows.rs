// SPDX-License-Identifier: Apache-2.0
#![cfg(windows)]

use std::{
    fs,
    io::{Read, Write},
    path::{Path, PathBuf},
    process::{Command, Stdio},
    time::{Duration, Instant},
};

use serde_json::json;

const MODE: &str = "PNPORT_NATIVE_WINDOWS_FIXTURE";
const PROJECT: &str = "PNPORT_NATIVE_WINDOWS_PROJECT";

fn binary() -> PathBuf {
    std::env::var_os("PNPORT_TEST_BINARY")
        .map(PathBuf::from)
        .unwrap_or_else(|| PathBuf::from(env!("CARGO_BIN_EXE_pnport")))
}
fn fixture(split: bool) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    let root = temp.path().join("project 한글 space");
    fs::create_dir(&root).unwrap();
    fs::write(root.join("source.txt"), b"source").unwrap();
    let archive = fs::File::create(root.join("dependency.zip")).unwrap();
    let mut archive = zip::ZipWriter::new(archive);
    for (name, bytes) in [
        (
            "node_modules/example/package.json",
            br#"{"name":"example"}"#.as_slice(),
        ),
        ("node_modules/example/data.txt", b"dependency".as_slice()),
    ] {
        archive
            .start_file(name, zip::write::SimpleFileOptions::default())
            .unwrap();
        archive.write_all(bytes).unwrap();
    }
    archive.finish().unwrap();
    let data = json!({"__info": [], "dependencyTreeRoots": [{"name":"root","reference":"workspace:."}], "enableTopLevelFallback":false, "ignorePatternData":null,"fallbackExclusionList":[],"fallbackPool":[],"packageRegistryData":[[null,[[null,{"packageLocation":"./","packageDependencies":[["example","npm:1.0.0"]],"linkType":"SOFT"}]]],["root",[["workspace:.",{"packageLocation":"./","packageDependencies":[["example","npm:1.0.0"]],"linkType":"SOFT"}]]],["example",[["npm:1.0.0",{"packageLocation":"./dependency.zip/node_modules/example/","packageDependencies":[],"linkType":"HARD"}]]]]});
    if split {
        fs::write(
            root.join(".pnp.cjs"),
            "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");\n",
        )
        .unwrap();
        fs::write(
            root.join(".pnp.data.json"),
            serde_json::to_vec(&data).unwrap(),
        )
        .unwrap();
    } else {
        let escaped = serde_json::to_string(&data)
            .unwrap()
            .replace('\\', "\\\\")
            .replace('\'', "\\'");
        fs::write(
            root.join(".pnp.cjs"),
            format!("const RAW_RUNTIME_STATE = '{escaped}';\n"),
        )
        .unwrap();
    }
    temp
}
fn run(root: &Path, cache: &Path, mode: &str) -> Command {
    let mut command = Command::new(binary());
    command
        .arg("--project")
        .arg(root)
        .arg("--cache-dir")
        .arg(cache)
        .args(["--color=never", "--log-level=debug", "run", "--"])
        .arg(std::env::current_exe().unwrap())
        .args(["--exact", "native_child", "--nocapture"])
        .env(MODE, mode)
        .env(PROJECT, root)
        .current_dir(root);
    command
}

#[test]
fn native_child() {
    let Some(mode) = std::env::var_os(MODE) else {
        return;
    };
    let root = PathBuf::from(std::env::var_os(PROJECT).unwrap());
    if mode == "read" {
        let dependency = root.join("node_modules/example/data.txt");
        assert_eq!(fs::read(&dependency).unwrap(), b"dependency");
        assert_eq!(fs::metadata(&dependency).unwrap().len(), 10);
        assert!(fs::write(&dependency, b"forbidden").is_err());
        assert!(fs::remove_file(&dependency).is_err());
        assert!(fs::rename(root.join("source.txt"), &dependency).is_err());
        fs::write(root.join("output.txt"), b"output").unwrap();
        let root_entries: Vec<_> = fs::read_dir(&root)
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert_eq!(
            root_entries
                .iter()
                .filter(|name| *name == "node_modules")
                .count(),
            1
        );
        let deps: Vec<_> = fs::read_dir(root.join("node_modules"))
            .unwrap()
            .map(|entry| entry.unwrap().file_name())
            .collect();
        assert!(deps.iter().any(|name| name == "example"));
        let canonical = fs::canonicalize(&dependency).unwrap();
        assert!(!canonical.to_string_lossy().contains("/v1/"));
        assert!(canonical.to_string_lossy().contains("dependency.zip"));
    } else if mode == "spawn" {
        // Windows' own PE loader requires SystemRoot even without pnport.
        // Retain that OS prerequisite while replacing every pnport/Node option.
        let output = Command::new(std::env::current_exe().unwrap())
            .args(["--exact", "native_child", "--nocapture"])
            .env_clear()
            .env("SystemRoot", std::env::var_os("SystemRoot").unwrap())
            .env("PATH", std::env::var_os("PATH").unwrap())
            .env(MODE, "read")
            .env(PROJECT, &root)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "descendant failed {:?}: {} {}",
            output.status.code(),
            String::from_utf8_lossy(&output.stderr),
            String::from_utf8_lossy(&output.stdout)
        );
    } else if mode == "wait" {
        fs::write(root.join("ready.txt"), std::process::id().to_string()).unwrap();
        loop {
            std::thread::sleep(Duration::from_millis(50));
        }
    }
}

#[test]
fn installed_inline_split_filesystem_and_replacement_environment() {
    for split in [false, true] {
        let fixture = fixture(split);
        let root = fixture.path().join("project 한글 space");
        let cache = fixture.path().join("private-cache");
        for mode in ["read", "spawn"] {
            let output = run(&root, &cache, mode).output().unwrap();
            assert!(
                output.status.success(),
                "mode {mode}, status {:?}: {} {}",
                output.status.code(),
                String::from_utf8_lossy(&output.stderr),
                String::from_utf8_lossy(&output.stdout)
            );
        }
        assert!(!root.join("node_modules").exists());
        assert_eq!(fs::read(root.join("source.txt")).unwrap(), b"source");
        assert_eq!(fs::read(root.join("output.txt")).unwrap(), b"output");
    }
}

#[test]
fn native_cache_materialization_keeps_active_content() {
    let fixture = fixture(false);
    let cache = pnport::cache::Cache::open(fixture.path().join("cache")).unwrap();
    let lease = cache
        .materialize(&fixture.path().join("project 한글 space/dependency.zip"))
        .unwrap();
    assert_eq!(
        fs::read(lease.content.join("node_modules/example/data.txt")).unwrap(),
        b"dependency"
    );
    assert!(cache
        .entries(pnport::cache::Operation::Clean)
        .unwrap()
        .iter()
        .any(|entry| matches!(entry.state, pnport::cache::State::Active)));
    drop(lease);
    assert!(cache
        .entries(pnport::cache::Operation::Clean)
        .unwrap()
        .iter()
        .any(|entry| matches!(entry.state, pnport::cache::State::Removed)));
}

#[test]
fn native_lookup_preserves_the_drive_root() {
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let graph = pnport::graph::Graph::load(&root.join(".pnp.cjs")).unwrap();
    let path = root.join("node_modules/example/data.txt");
    let resolved = pnport_core::windows::lookup_spelling(&path, &graph).unwrap();
    assert!(resolved.is_absolute(), "{resolved:?}");
}

#[test]
fn forced_supervisor_exit_kills_the_owned_job() {
    use std::os::windows::io::AsRawHandle;

    use windows_sys::Win32::{
        Foundation::CloseHandle,
        System::Threading::{OpenProcess, WaitForSingleObject, PROCESS_SYNCHRONIZE},
    };
    let fixture = fixture(false);
    let root = fixture.path().join("project 한글 space");
    let mut supervisor = run(&root, &fixture.path().join("cache"), "wait")
        .stdout(Stdio::null())
        .stderr(Stdio::piped())
        .spawn()
        .unwrap();
    let deadline = Instant::now() + Duration::from_secs(20);
    while !root.join("ready.txt").exists() {
        if let Some(status) = supervisor.try_wait().unwrap() {
            let mut stderr = String::new();
            supervisor
                .stderr
                .take()
                .unwrap()
                .read_to_string(&mut stderr)
                .unwrap();
            panic!("early exit {status}: {stderr}");
        }
        assert!(
            Instant::now() < deadline,
            "native child initialization deadline"
        );
        std::thread::sleep(Duration::from_millis(20));
    }
    let pid = fs::read_to_string(root.join("ready.txt"))
        .unwrap()
        .parse()
        .unwrap();
    let child = unsafe { OpenProcess(PROCESS_SYNCHRONIZE, 0, pid) };
    assert!(!child.is_null());
    supervisor.kill().unwrap();
    supervisor.wait().unwrap();
    let status = unsafe { WaitForSingleObject(child, 5000) };
    unsafe {
        CloseHandle(child);
    }
    assert_eq!(
        status, 0,
        "owned child survived abrupt supervisor termination"
    );
    let _ = supervisor.as_raw_handle();
}
