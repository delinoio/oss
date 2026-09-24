use std::{fs, io::Write, path::Path};

use pnport::{
    cache::{Cache, Operation, State},
    diagnostic::Code,
    graph::{Graph, Input},
    view::View,
};
use serde_json::{json, Value};

fn data() -> Value {
    json!({
        "enableTopLevelFallback": true,
        "ignorePatternData": null,
        "dependencyTreeRoots": [{"name":"root","reference":"workspace:."}],
        "fallbackPool": [], "fallbackExclusionList": [],
        "packageRegistryData": [
            [null, [[null,{"packageLocation":"./","packageDependencies":[["dep","npm:1"],["alias",["dep","npm:1"]],["@scope/pkg","npm:1"]],"linkType":"SOFT","discardFromLookup":true}]]],
            ["root", [["workspace:.",{"packageLocation":"./","packageDependencies":[["dep","npm:1"],["alias",["dep","npm:1"]],["@scope/pkg","npm:1"]],"linkType":"SOFT"}]]],
            ["dep",[["npm:1",{"packageLocation":"./cache.zip/node_modules/dep/","packageDependencies":[],"linkType":"HARD"}]]],
            ["@scope/pkg",[["npm:1",{"packageLocation":"./cache.zip/node_modules/@scope/pkg/","packageDependencies":[],"linkType":"HARD"}]]]
        ]
    })
}
fn inline(root: &Path, data: &Value) {
    let value = serde_json::to_string_pretty(data)
        .unwrap()
        .replace('\\', "\\\\")
        .replace('\'', "\\'")
        .replace('\n', "\\\n");
    fs::write(
        root.join(".pnp.cjs"),
        format!(
            "// UTF-8 π before the payload\nconst RAW_RUNTIME_STATE =\n'{value}';\nthrow new \
             Error('must never execute');\n"
        ),
    )
    .unwrap();
}
fn archive(path: &Path, files: &[(&str, &[u8])]) {
    let mut zip = zip::ZipWriter::new(fs::File::create(path).unwrap());
    for (name, bytes) in files {
        zip.start_file(
            *name,
            zip::write::SimpleFileOptions::default().unix_permissions(0o644),
        )
        .unwrap();
        zip.write_all(bytes).unwrap();
    }
    zip.finish().unwrap();
}
fn fixture() -> tempfile::TempDir {
    let root = tempfile::tempdir().unwrap();
    inline(root.path(), &data());
    archive(
        &root.path().join("cache.zip"),
        &[
            ("node_modules/dep/file.txt", b"package bytes"),
            ("node_modules/dep/package.json", br#"{"name":"dep"}"#),
            (
                "node_modules/@scope/pkg/package.json",
                br#"{"name":"@scope/pkg"}"#,
            ),
        ],
    );
    root
}

#[test]
fn inline_and_split_load_without_executing_javascript() {
    let root = fixture();
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    let root_path = fs::canonicalize(root.path()).unwrap();
    assert!(!graph.managed(&root_path.join("source.ts")));
    assert!(graph.managed(&root_path.join("cache.zip/node_modules/dep/file.txt")));
    assert!(!graph.managed(&root_path.parent().unwrap().join("unrelated.ts")));
    let restored = Graph::from_snapshot(graph.snapshot.clone()).unwrap();
    assert!(!restored.managed(&root_path));
    assert!(restored.managed(&root_path.join("cache.zip/node_modules/dep/file.txt")));
    assert_eq!(
        graph.resolve("dep", &root_path).unwrap(),
        graph.resolve("alias", &root_path).unwrap()
    );
    fs::write(
        root.path().join(".pnp.cjs"),
        "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");\nthrow Error('do \
         not execute');",
    )
    .unwrap();
    fs::write(
        root.path().join(".pnp.data.json"),
        serde_json::to_vec(&data()).unwrap(),
    )
    .unwrap();
    let split = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    assert_eq!(split.snapshot.inputs.len(), 2);
    assert_eq!(
        graph.resolve("dep", &root_path).unwrap(),
        split.resolve("dep", &root_path).unwrap()
    );
    fs::write(root.path().join(".pnp.data.json"), b"{}").unwrap();
    assert_eq!(
        split.unchanged().unwrap_err().code,
        Code::PnportGraphChanged
    );
}
#[test]
fn malformed_graphs_fail_without_panic_or_input_disclosure() {
    let root = fixture();
    for bad in [
        json!({}),
        {
            let mut d = data();
            d["packageRegistryData"][0][1] = json!([]);
            d
        },
        {
            let mut d = data();
            d["packageRegistryData"][1][1][0][1]["packageDependencies"] =
                json!([["canary-secret", "missing"]]);
            d
        },
    ] {
        inline(root.path(), &bad);
        let error = Graph::load(&root.path().join(".pnp.cjs")).err().unwrap();
        assert_eq!(error.code, Code::PnportManifestInvalid);
        assert!(!error.to_string().contains("canary-secret"));
    }
}
#[test]
fn view_reads_zip_scopes_aliases_and_preserves_project_writes() {
    let root = fixture();
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    let root_path = fs::canonicalize(root.path()).unwrap();
    let cache_root = tempfile::tempdir().unwrap();
    let session = tempfile::tempdir().unwrap();
    let mut view = View::new(
        graph,
        Cache::open(cache_root.path().join("cache")).unwrap(),
        session.path().to_owned(),
    );
    let dep = view
        .translate(&root_path.join("node_modules/dep/file.txt"))
        .unwrap();
    assert_eq!(fs::read(&dep.physical).unwrap(), b"package bytes");
    assert!(dep.readonly);
    let alias = view
        .translate(&root_path.join("node_modules/alias/file.txt"))
        .unwrap();
    assert_eq!(alias.logical, dep.logical);
    let dir = view.translate(&root_path.join("node_modules")).unwrap();
    let mut names: Vec<_> = fs::read_dir(dir.physical)
        .unwrap()
        .map(|e| e.unwrap().file_name())
        .collect();
    names.sort();
    assert_eq!(names, vec!["@scope", "alias", "dep"]);
    assert!(
        view.translate(&root_path.join("node_modules/@scope/pkg/package.json"))
            .unwrap()
            .readonly
    );
    assert!(
        !view
            .translate(&root_path.join("output.txt"))
            .unwrap()
            .readonly
    );
    assert!(!root_path.join("node_modules").exists());
    fs::create_dir(root_path.join("node_modules")).unwrap();
    assert_eq!(
        view.translate(&root_path.join("node_modules/dep/file.txt"))
            .unwrap_err()
            .code,
        Code::PnportFilesystemConflict
    );
}
#[test]
fn leases_survive_cleanup_and_cache_corruption_is_not_reused() {
    let root = fixture();
    let cache_root = tempfile::tempdir().unwrap();
    let cache = Cache::open(cache_root.path().join("cache")).unwrap();
    let lease = cache.materialize(&root.path().join("cache.zip")).unwrap();
    let second = cache.materialize(&root.path().join("cache.zip")).unwrap();
    assert_eq!(lease.content, second.content);
    assert!(matches!(
        cache.entries(Operation::Clean).unwrap()[0].state,
        State::Active
    ));
    let changed = lease.content.join("node_modules/dep/file.txt");
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        fs::set_permissions(&changed, fs::Permissions::from_mode(0o600)).unwrap();
    }
    #[cfg(windows)]
    #[allow(
        clippy::permissions_set_readonly_false,
        reason = "Windows-only code clears FILE_ATTRIBUTE_READONLY; Unix mode changes use \
                  PermissionsExt"
    )]
    {
        let mut permissions = fs::metadata(&changed).unwrap().permissions();
        permissions.set_readonly(false);
        fs::set_permissions(&changed, permissions).unwrap();
    }
    fs::write(&changed, b"corrupt").unwrap();
    assert!(cache.materialize(&root.path().join("cache.zip")).is_err());
    assert!(matches!(
        cache.entries(Operation::Clean).unwrap()[0].state,
        State::Active
    ));
    drop(second);
    drop(lease);
    assert!(matches!(
        cache.entries(Operation::Prune).unwrap()[0].state,
        State::Corrupt
    ));
    assert!(matches!(
        cache.entries(Operation::Clean).unwrap()[0].state,
        State::Removed
    ));
    assert!(cache.entries(Operation::List).unwrap().is_empty());
}
#[test]
fn archive_traversal_and_unknown_ownership_are_preserved_or_rejected() {
    let root = tempfile::tempdir().unwrap();
    let cache_root = tempfile::tempdir().unwrap();
    let cache = Cache::open(cache_root.path().join("cache")).unwrap();
    archive(
        &root.path().join("unsafe.zip"),
        &[("../outside", b"escape")],
    );
    assert!(cache.materialize(&root.path().join("unsafe.zip")).is_err());
    assert!(!cache_root.path().join("outside").exists());
    fs::create_dir(cache_root.path().join("cache/foreign")).unwrap();
    fs::write(cache_root.path().join("cache/foreign/keep"), "retained").unwrap();
    cache.entries(Operation::Clean).unwrap();
    assert!(cache_root.path().join("cache/foreign/keep").exists());
}

#[cfg(unix)]
#[test]
fn confined_dangling_archive_symlinks_remain_readable() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("links.zip");
    let mut writer = zip::ZipWriter::new(fs::File::create(&path).unwrap());
    let options = zip::write::SimpleFileOptions::default();
    writer
        .add_symlink("node_modules/dep/missing", "not-installed", options)
        .unwrap();
    writer
        .add_symlink("node_modules/dep/indirect", "missing", options)
        .unwrap();
    writer.finish().unwrap();
    let cache = Cache::open(root.path().join("cache")).unwrap();
    let lease = cache.materialize(&path).unwrap();
    for (name, target) in [("missing", "not-installed"), ("indirect", "missing")] {
        let link = lease.content.join("node_modules/dep").join(name);
        assert!(fs::symlink_metadata(&link)
            .unwrap()
            .file_type()
            .is_symlink());
        assert_eq!(fs::read_link(&link).unwrap(), Path::new(target));
        assert_eq!(
            fs::read(&link).unwrap_err().kind(),
            std::io::ErrorKind::NotFound
        );
    }
    assert!(cache.materialize(&path).is_ok());
}

#[cfg(target_os = "macos")]
#[test]
fn pnp_unaware_native_process_reads_virtual_dependencies() {
    use std::process::Command;
    let root = fixture();
    let cache = tempfile::tempdir().unwrap();
    let executable = root.path().join("fixture");
    assert!(Command::new("cc")
        .arg(concat!(env!("CARGO_MANIFEST_DIR"), "/tests/native.c"))
        .arg("-o")
        .arg(&executable)
        .status()
        .unwrap()
        .success());
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["--log-level", "debug", "run", "--"])
        .arg(&executable)
        .args(["", "literal;$() argument"])
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "stdout={} stderr={}",
        String::from_utf8_lossy(&result.stdout),
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"protocol-output\nprotocol-output\n");
    let no_path = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .env_remove("PATH")
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--", "fixture"])
        .output()
        .unwrap();
    assert_eq!(no_path.status.code(), Some(127));
    assert_eq!(fs::read(root.path().join("output.txt")).unwrap(), b"native");
    assert!(!root.path().join("node_modules").exists());
    // PATH lookup must skip a regular, non-executable file with the same name.
    use std::os::unix::fs::PermissionsExt;
    let shadow = root.path().join("shadow");
    fs::create_dir(&shadow).unwrap();
    fs::write(shadow.join("fixture"), "not executable").unwrap();
    fs::set_permissions(shadow.join("fixture"), fs::Permissions::from_mode(0o644)).unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .env(
            "PATH",
            std::env::join_paths([shadow.as_path(), root.path()]).unwrap(),
        )
        .arg("--cache-dir")
        .arg(cache.path().join("cache"))
        .args(["run", "--", "fixture", "", "literal;$() argument"])
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(0),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"protocol-output\nprotocol-output\n");
}

#[test]
fn peer_instances_keep_logical_identity_while_sharing_package_bytes() {
    let root = fixture();
    let mut d = data();
    for index in [0, 1] {
        d["packageRegistryData"][index][1][0][1]["packageDependencies"] = json!([
            ["one", ["dep", "virtual:one"]],
            ["two", ["dep", "virtual:two"]]
        ]);
    }
    d["packageRegistryData"][2][1] = json!([
        ["npm:1",{"packageLocation":"./cache.zip/node_modules/dep/","packageDependencies":[],"linkType":"HARD"}],
        ["virtual:one",{"packageLocation":"./.yarn/__virtual__/dep-one/1/cache.zip/node_modules/dep/","packageDependencies":[["peer",["dep","npm:1"]]],"linkType":"HARD"}],
        ["virtual:two",{"packageLocation":"./.yarn/__virtual__/dep-two/1/cache.zip/node_modules/dep/","packageDependencies":[["peer",["@scope/pkg","npm:1"]]],"linkType":"HARD"}]
    ]);
    inline(root.path(), &d);
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    let root_path = fs::canonicalize(root.path()).unwrap();
    let cache = tempfile::tempdir().unwrap();
    let session = tempfile::tempdir().unwrap();
    let mut view = View::new(
        graph,
        Cache::open(cache.path().join("cache")).unwrap(),
        session.path().to_owned(),
    );
    let one = view
        .translate(&root_path.join("node_modules/one/file.txt"))
        .unwrap();
    let two = view
        .translate(&root_path.join("node_modules/two/file.txt"))
        .unwrap();
    assert_eq!(one.physical, two.physical);
    assert_ne!(one.logical, two.logical);
    let peer_one = view
        .translate(&root_path.join("node_modules/one/node_modules/peer/package.json"))
        .unwrap();
    let peer_two = view
        .translate(&root_path.join("node_modules/two/node_modules/peer/package.json"))
        .unwrap();
    assert!(matches!(
        pnp::fs::VPath::from(&peer_one.logical),
        Ok(pnp::fs::VPath::Zip(_))
    ));
    assert!(matches!(
        pnp::fs::VPath::from(&peer_two.logical),
        Ok(pnp::fs::VPath::Zip(_))
    ));
    assert_ne!(peer_one.physical, peer_two.physical);
    assert_eq!(fs::read(&peer_one.physical).unwrap(), br#"{"name":"dep"}"#);
    assert_eq!(
        fs::read(&peer_two.physical).unwrap(),
        br#"{"name":"@scope/pkg"}"#
    );
}
#[test]
fn doctor_json_is_typed_ansi_free_and_does_not_leak_invalid_data() {
    use std::process::Command;
    let root = tempfile::tempdir().unwrap();
    fs::write(
        root.path().join(".pnp.cjs"),
        "const RAW_RUNTIME_STATE = 'canary-secret';",
    )
    .unwrap();
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .args(["--color=always", "--cache-dir"])
        .arg(root.path().join("cache"))
        .args(["doctor", "--json"])
        .output()
        .unwrap();
    assert_eq!(result.status.code(), Some(125));
    assert!(result.stderr.is_empty());
    assert!(!result.stdout.contains(&0x1b));
    let data: Value = serde_json::from_slice(&result.stdout).unwrap();
    assert_eq!(data["schemaVersion"], 1);
    assert_eq!(data["ready"], false);
    assert_eq!(data["checks"][0]["code"], "PNPORT_MANIFEST_INVALID");
    assert!(!String::from_utf8_lossy(&result.stdout).contains("canary-secret"));
}
#[test]
fn concurrent_materializers_publish_one_entry() {
    let root = fixture();
    let cache_root = tempfile::tempdir().unwrap();
    let cache = Cache::open(cache_root.path().join("cache")).unwrap();
    let handles: Vec<_> = (0..4)
        .map(|_| {
            let cache = cache.clone();
            let path = root.path().join("cache.zip");
            std::thread::spawn(move || cache.materialize(&path).unwrap())
        })
        .collect();
    let leases: Vec<_> = handles.into_iter().map(|t| t.join().unwrap()).collect();
    assert!(leases
        .iter()
        .all(|lease| lease.content == leases[0].content));
    let entries = cache.entries(Operation::Clean).unwrap();
    assert_eq!(entries.len(), 1);
    assert!(matches!(entries[0].state, State::Active));
}

#[test]
fn active_markers_retain_distinct_archive_versions() {
    let root = fixture();
    let root_path = fs::canonicalize(root.path()).unwrap();
    let cache_root = tempfile::tempdir().unwrap();
    let session = tempfile::tempdir().unwrap();
    let make_view = || {
        View::new(
            Graph::load(&root.path().join(".pnp.cjs")).unwrap(),
            Cache::open(cache_root.path().join("cache")).unwrap(),
            session.path().to_owned(),
        )
    };
    let mut first = make_view();
    let first_path = first
        .translate(&root_path.join("node_modules/dep/file.txt"))
        .unwrap()
        .physical;
    archive(
        &root.path().join("cache.zip"),
        &[("node_modules/dep/file.txt", b"updated bytes")],
    );
    let mut second = make_view();
    let second_path = second
        .translate(&root_path.join("node_modules/dep/file.txt"))
        .unwrap()
        .physical;
    assert_ne!(first_path, second_path);
    assert_eq!(fs::read(first_path).unwrap(), b"package bytes");
    assert_eq!(fs::read(second_path).unwrap(), b"updated bytes");
    let markers: Vec<Input> = fs::read_dir(session.path().join("active"))
        .unwrap()
        .map(|entry| serde_json::from_slice(&fs::read(entry.unwrap().path()).unwrap()).unwrap())
        .collect();
    assert_eq!(markers.len(), 2);
    assert_eq!(markers[0].path, markers[1].path);
    assert_ne!(markers[0].sha256, markers[1].sha256);
}

#[cfg(target_os = "macos")]
#[test]
fn missing_executables_and_interpreters_return_not_found() {
    use std::{os::unix::fs::PermissionsExt, process::Command};
    let root = fixture();
    let missing = root.path().join("missing-command-canary");
    let script = root.path().join("script");
    fs::write(&script, format!("#!{}\n", missing.display())).unwrap();
    fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
    let invalid = root.path().join("invalid");
    fs::write(&invalid, "not executable").unwrap();
    fs::set_permissions(&invalid, fs::Permissions::from_mode(0o600)).unwrap();
    for (command, status, code) in [
        (
            Path::new("./missing-command-canary"),
            127,
            "PNPORT_COMMAND_NOT_FOUND",
        ),
        (missing.as_path(), 127, "PNPORT_COMMAND_NOT_FOUND"),
        (script.as_path(), 127, "PNPORT_COMMAND_NOT_FOUND"),
        (invalid.as_path(), 126, "PNPORT_COMMAND_NOT_EXECUTABLE"),
    ] {
        let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(root.path().join("private-cache"))
            .args(["run", "--"])
            .arg(command)
            .output()
            .unwrap();
        assert_eq!(
            result.status.code(),
            Some(status),
            "{}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert!(result.stdout.is_empty());
        let stderr = String::from_utf8_lossy(&result.stderr);
        assert!(stderr.contains(code));
        assert!(!stderr.contains("missing-command-canary"));
    }
}

#[cfg(target_os = "macos")]
#[test]
fn descendant_exec_and_spawn_prepare_script_interpreters() {
    use std::{os::unix::fs::PermissionsExt, process::Command};

    let root = fixture();
    let interpreter_source = root.path().join("child-interpreter.c");
    let interpreter = root.path().join("child-interpreter");
    fs::write(
        &interpreter_source,
        r#"#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
  FILE *file = fopen("node_modules/dep/file.txt", "r");
  if (!file) return 70;
  fclose(file);
  if (argc != 4 || strcmp(argv[1], "option") || !strstr(argv[2], "child-script") || strcmp(argv[3], "literal")) return 71;
  puts("descendant-script-ok");
  return 0;
}
"#,
    )
    .unwrap();
    assert!(Command::new("cc")
        .arg(&interpreter_source)
        .arg("-o")
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let script = root.path().join("child-script");
    fs::write(&script, format!("#!{} option\n", interpreter.display())).unwrap();
    fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();

    let launcher_source = root.path().join("child-launcher.c");
    let launcher = root.path().join("child-launcher");
    fs::write(
        &launcher_source,
        r#"#include <errno.h>
#include <spawn.h>
#include <string.h>
#include <sys/wait.h>
#include <unistd.h>
extern char **environ;
int main(int argc, char **argv) {
  if (argc != 3) return 2;
  char *args[] = {"child-script", "literal", 0};
  if (strcmp(argv[1], "execve") == 0) {
    execve(argv[2], args, environ);
    return 30;
  }
  if (strcmp(argv[1], "posix_spawn_chdir") == 0) {
    posix_spawn_file_actions_t actions;
    if (posix_spawn_file_actions_init(&actions) != 0) return 34;
    if (posix_spawn_file_actions_addchdir_np(&actions, ".") != 0) return 35;
    int result = posix_spawn(0, "child-script", &actions, 0, args, environ);
    posix_spawn_file_actions_destroy(&actions);
    return result == ENOTSUP ? 0 : (result ? result : 36);
  }
  pid_t pid;
  if (posix_spawn(&pid, argv[2], 0, 0, args, environ) != 0) return 31;
  int status;
  if (waitpid(pid, &status, 0) != pid) return 32;
  return WIFEXITED(status) ? WEXITSTATUS(status) : 33;
}
"#,
    )
    .unwrap();
    assert!(Command::new("cc")
        .arg(&launcher_source)
        .arg("-o")
        .arg(&launcher)
        .status()
        .unwrap()
        .success());

    for method in ["execve", "posix_spawn"] {
        let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .arg("--cache-dir")
            .arg(root.path().join("private-cache"))
            .args(["run", "--"])
            .arg(&launcher)
            .arg(method)
            .arg(&script)
            .output()
            .unwrap();
        assert!(
            result.status.success(),
            "{method}: {}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(result.stdout, b"descendant-script-ok\n", "{method}");
    }
    let result = Command::new(env!("CARGO_BIN_EXE_pnport"))
        .current_dir(root.path())
        .arg("--cache-dir")
        .arg(root.path().join("private-cache"))
        .args(["run", "--"])
        .arg(&launcher)
        .arg("posix_spawn_chdir")
        .arg(&script)
        .output()
        .unwrap();
    assert_eq!(
        result.status.code(),
        Some(125),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
}

#[cfg(target_os = "macos")]
#[test]
fn script_interpreters_preserve_logical_arguments_and_reject_protection() {
    use std::{os::unix::fs::PermissionsExt, process::Command};
    let root = fixture();
    let source = root.path().join("interpreter.c");
    fs::write(&source, r#"
#include <stdio.h>
#include <string.h>
int main(int argc, char **argv) {
    FILE *file = fopen("node_modules/dep/file.txt", "r");
    if (!file) return 70;
    fclose(file);
    if (argc != 5 || strcmp(argv[1], "two words") || strcmp(argv[3], "") || strcmp(argv[4], "literal;$()")) return 71;
    puts("script-ok");
    return 0;
}
"#).unwrap();
    let interpreter = root.path().join("interpreter");
    assert!(Command::new("cc")
        .arg(&source)
        .arg("-o")
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let script = root.path().join("script with spaces");
    fs::write(
        &script,
        "#!/usr/bin/env -S interpreter 'two words'\nunused\n",
    )
    .unwrap();
    fs::set_permissions(&script, fs::Permissions::from_mode(0o700)).unwrap();
    let run = || {
        Command::new(env!("CARGO_BIN_EXE_pnport"))
            .current_dir(root.path())
            .env("PATH", root.path())
            .args(["--cache-dir"])
            .arg(root.path().join("private-cache"))
            .args(["run", "--"])
            .arg(&script)
            .args(["", "literal;$()"])
            .output()
            .unwrap()
    };
    let result = run();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    assert_eq!(result.stdout, b"script-ok\n");
    assert!(Command::new("codesign")
        .args(["--force", "--sign", "-", "--options", "runtime"])
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let result = run();
    assert_eq!(result.status.code(), Some(125));
    assert!(String::from_utf8_lossy(&result.stderr).contains("PNPORT_UNSUPPORTED_OPERATION"));
    let entitlements = root.path().join("entitlements.plist");
    fs::write(&entitlements, r#"<?xml version="1.0"?><plist version="1.0"><dict><key>com.apple.security.cs.allow-dyld-environment-variables</key><true/><key>com.apple.security.cs.disable-library-validation</key><true/></dict></plist>"#).unwrap();
    assert!(Command::new("codesign")
        .args([
            "--force",
            "--sign",
            "-",
            "--options",
            "runtime",
            "--entitlements"
        ])
        .arg(&entitlements)
        .arg(&interpreter)
        .status()
        .unwrap()
        .success());
    let result = run();
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    fs::write(&script, "#!/bin/sh\nexit 0\n").unwrap();
    assert_eq!(run().status.code(), Some(125));
}

#[test]
fn unplugged_installation_containers_are_not_dependency_conflicts() {
    let root = fixture();
    let location = "./.yarn/unplugged/dep/node_modules/dep/";
    let mut value = data();
    value["packageRegistryData"][2][1][0][1]["packageLocation"] = json!(location);
    fs::create_dir_all(root.path().join(location)).unwrap();
    fs::write(
        root.path().join(location).join("package.json"),
        r#"{"name":"dep"}"#,
    )
    .unwrap();
    inline(root.path(), &value);
    let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
    graph.check_conflicts().unwrap();
    let canonical = fs::canonicalize(root.path()).unwrap();
    let mut view = View::new(
        graph,
        Cache::open(root.path().join("private-cache")).unwrap(),
        root.path().join("session"),
    );
    let ancestor = canonical.join(".yarn/unplugged/dep/node_modules");
    assert_eq!(view.translate(&ancestor).unwrap().physical, ancestor);
    let file = view
        .translate(&canonical.join("node_modules/dep/package.json"))
        .unwrap();
    assert!(file.readonly);
    assert!(file.physical.is_file());
}
