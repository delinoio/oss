use std::{fs, io::Write, path::Path};

use pnport::{cache::Cache, diagnostic::Code, graph::Graph, view::View};
use serde_json::{json, Value};

fn package(location: &str, dependencies: Value, kind: &str) -> Value {
    json!({"packageLocation": location, "packageDependencies": dependencies, "linkType": kind})
}

fn fixture(split: bool) -> tempfile::TempDir {
    let root = tempfile::tempdir().unwrap();
    let dependencies = json!([
        ["outer-one", ["outer", "virtual:one"]],
        ["outer-two", ["outer", "virtual:two"]],
        ["@scope/inner-one", ["inner", "virtual:one"]],
        ["@scope/inner-two", ["inner", "virtual:two"]],
        ["unplugged", "npm:1"],
        ["wrapper", "npm:1"]
    ]);
    let top = package("./", dependencies.clone(), "SOFT");
    let mut fallback = top.clone();
    fallback["discardFromLookup"] = json!(true);
    let mut outers = Vec::new();
    let mut inners = Vec::new();
    for context in ["one", "two"] {
        let reference = format!("virtual:{context}");
        outers.push(json!([
            reference,
            package(
                &format!("./.yarn/__virtual__/outer-{context}/1/packages.zip/node_modules/outer/"),
                json!([
                    ["@scope/inner", ["inner", reference]],
                    ["alias", ["inner", reference]],
                    ["unplugged", "npm:1"]
                ]),
                "HARD"
            )
        ]));
        inners.push(json!([
            reference,
            package(
                &format!("./.yarn/__virtual__/inner-{context}/1/packages.zip/node_modules/inner/"),
                json!([["unplugged", "npm:1"]]),
                "HARD"
            )
        ]));
    }
    let data = json!({
        "enableTopLevelFallback": true, "ignorePatternData": null,
        "dependencyTreeRoots": [{"name": "root", "reference": "workspace:."}],
        "fallbackPool": [], "fallbackExclusionList": [],
        "packageRegistryData": [
            [null, [[null, fallback]]], ["root", [["workspace:.", top]]],
            ["outer", outers], ["inner", inners],
            ["wrapper", [["npm:1", package("./.yarn/unplugged/wrapper/node_modules/wrapper/", dependencies, "HARD")]]],
            ["unplugged", [["npm:1", package("./.yarn/unplugged/unplugged/node_modules/unplugged/", json!([]), "HARD")]]]
        ]
    });
    if split {
        fs::write(root.path().join(".pnp.data.json"), data.to_string()).unwrap();
        fs::write(
            root.path().join(".pnp.cjs"),
            "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");\n",
        )
        .unwrap();
    } else {
        let payload = data.to_string().replace('\\', "\\\\").replace('\'', "\\'");
        fs::write(
            root.path().join(".pnp.cjs"),
            format!("const RAW_RUNTIME_STATE = '{payload}';\n"),
        )
        .unwrap();
    }
    let mut zip = zip::ZipWriter::new(fs::File::create(root.path().join("packages.zip")).unwrap());
    let options = zip::write::SimpleFileOptions::default().unix_permissions(0o644);
    for name in ["outer", "inner"] {
        for suffix in ["file.txt", "subdir/file.txt"] {
            zip.start_file(format!("node_modules/{name}/{suffix}"), options)
                .unwrap();
            zip.write_all(b"package bytes").unwrap();
        }
        for (link, target) in [("file-link", "file.txt"), ("broken-link", "missing.txt")] {
            zip.add_symlink(format!("node_modules/{name}/{link}"), target, options)
                .unwrap();
        }
    }
    zip.finish().unwrap();
    for name in ["wrapper", "unplugged"] {
        let location = root
            .path()
            .join(format!(".yarn/unplugged/{name}/node_modules/{name}"));
        fs::create_dir_all(location.join("subdir")).unwrap();
        fs::write(location.join("file.txt"), b"package bytes").unwrap();
    }
    fs::write(root.path().join("source.txt"), b"source").unwrap();
    #[cfg(unix)]
    std::os::unix::fs::symlink("source.txt", root.path().join("source-link")).unwrap();
    root
}

#[test]
fn recursive_terminal_links_keep_peer_identity_and_descendant_types() {
    for split in [false, true] {
        let root = fixture(split);
        let root_path = fs::canonicalize(root.path()).unwrap();
        let cache = tempfile::tempdir().unwrap();
        let session = tempfile::tempdir().unwrap();
        let mut view = View::new(
            Graph::load(&root.path().join(".pnp.cjs")).unwrap(),
            Cache::open(cache.path().join("cache")).unwrap(),
            session.path().to_owned(),
        );
        let mut peers = Vec::new();
        for context in ["one", "two"] {
            let direct = view
                .translate(&root_path.join(format!("node_modules/@scope/inner-{context}")))
                .unwrap();
            assert!(direct.virtual_link && direct.readonly);
            assert!(fs::metadata(&direct.physical).unwrap().is_dir());
            for alias in [
                format!("node_modules/outer-{context}/node_modules/@scope/inner"),
                format!("node_modules/outer-{context}/node_modules/alias"),
                format!(
                    "node_modules/wrapper/node_modules/outer-{context}/node_modules/@scope/inner"
                ),
            ] {
                let nested = view.translate(&root_path.join(&alias)).unwrap();
                assert!(nested.virtual_link, "split={split}, alias={alias}");
                assert!(nested.readonly);
                assert_eq!(nested.logical, direct.logical);
                assert_eq!(nested.physical, direct.physical);
                for suffix in [
                    "file.txt",
                    "subdir",
                    "missing.txt",
                    "file-link",
                    "broken-link",
                    "node_modules",
                ] {
                    let descendant = view
                        .translate(&root_path.join(&alias).join(suffix))
                        .unwrap();
                    assert!(
                        !descendant.virtual_link,
                        "split={split}, alias={alias}/{suffix}"
                    );
                    assert!(descendant.readonly);
                    match suffix {
                        "file.txt" => assert!(fs::metadata(descendant.physical).unwrap().is_file()),
                        "subdir" | "node_modules" => {
                            assert!(fs::metadata(descendant.physical).unwrap().is_dir())
                        }
                        "missing.txt" => assert!(!descendant.physical.exists()),
                        #[cfg(unix)]
                        "file-link" | "broken-link" => {
                            assert!(fs::symlink_metadata(&descendant.physical)
                                .unwrap()
                                .file_type()
                                .is_symlink());
                            assert_eq!(
                                fs::read_link(descendant.physical).unwrap(),
                                Path::new(if suffix == "file-link" {
                                    "file.txt"
                                } else {
                                    "missing.txt"
                                })
                            );
                        }
                        _ => {}
                    }
                }
                let native = view
                    .translate(&root_path.join(&alias).join("node_modules/unplugged"))
                    .unwrap();
                let direct_native = view
                    .translate(&root_path.join("node_modules/unplugged"))
                    .unwrap();
                assert!(native.virtual_link && native.readonly);
                assert_eq!(native.logical, direct_native.logical);
                assert_eq!(native.physical, direct_native.physical);
                assert!(
                    !view
                        .translate(
                            &root_path
                                .join(&alias)
                                .join("node_modules/unplugged/file.txt")
                        )
                        .unwrap()
                        .virtual_link
                );
            }
            peers.push(direct);
        }
        assert_ne!(peers[0].logical, peers[1].logical);
        assert_eq!(peers[0].physical, peers[1].physical);
        for native in ["source.txt", "source-link", "output.txt"] {
            let translated = view.translate(&root_path.join(native)).unwrap();
            assert!(!translated.virtual_link && !translated.readonly);
            assert_eq!(translated.physical, root_path.join(native));
        }
        fs::create_dir(root_path.join("node_modules")).unwrap();
        assert_eq!(
            view.translate(&root_path.join("node_modules/outer-one/node_modules/alias"))
                .unwrap_err()
                .code,
            Code::PnportFilesystemConflict
        );
    }
}

#[cfg(any(target_os = "macos", target_os = "linux"))]
#[test]
fn recursive_terminal_links_agree_for_native_paths_and_directory_handles() {
    use std::process::Command;
    for split in [false, true] {
        let root = fixture(split);
        let cache = tempfile::tempdir().unwrap();
        let executable = root.path().join("terminal-links");
        assert!(Command::new("cc")
            .args(["-Wall", "-Wextra", "-Werror"])
            .arg(concat!(
                env!("CARGO_MANIFEST_DIR"),
                "/tests/terminal-links.c"
            ))
            .arg("-o")
            .arg(&executable)
            .status()
            .unwrap()
            .success());
        let negative = Command::new(&executable)
            .current_dir(root.path())
            .output()
            .unwrap();
        assert!(
            !negative.status.success(),
            "native negative control unexpectedly passed"
        );
        let run = || {
            Command::new(env!("CARGO_BIN_EXE_pnport"))
                .current_dir(root.path())
                .arg("--cache-dir")
                .arg(cache.path().join("cache"))
                .args(["run", "--"])
                .arg(&executable)
                .output()
                .unwrap()
        };
        let result = run();
        assert_eq!(
            result.status.code(),
            Some(0),
            "split={split}, stdout={} stderr={}",
            String::from_utf8_lossy(&result.stdout),
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(result.stdout, b"terminal-link conformance passed\n");
        assert_eq!(fs::read(root.path().join("output.txt")).unwrap(), b"output");
        assert_eq!(
            fs::read(root.path().join("source.txt")).unwrap(),
            b"source!"
        );
        assert!(!root.path().join("node_modules").exists());
        fs::create_dir(root.path().join("node_modules")).unwrap();
        let conflict = run();
        assert_eq!(conflict.status.code(), Some(125));
        assert!(String::from_utf8_lossy(&conflict.stderr).contains("PNPORT_FILESYSTEM_CONFLICT"));
        assert!(root.path().join("node_modules").is_dir());
    }
}
