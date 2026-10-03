// SPDX-License-Identifier: Apache-2.0
#![cfg(any(target_os = "macos", target_os = "linux"))]

use std::{
    fs,
    io::Write,
    path::{Path, PathBuf},
    process::Command,
};

use serde_json::json;

struct Fixture {
    root: tempfile::TempDir,
    binary: PathBuf,
}

fn pnport_binary() -> PathBuf {
    std::env::var_os("PNPORT_TEST_BINARY")
        .map(PathBuf::from)
        .inspect(|path| assert!(path.is_absolute(), "Installed test binary must be absolute"))
        .unwrap_or_else(|| PathBuf::from(env!("CARGO_BIN_EXE_pnport")))
}

impl Fixture {
    fn new(split: bool, static_binary: bool) -> Self {
        let root = tempfile::tempdir().unwrap();
        let package = json!({
            "packageLocation": "./", "packageDependencies": [["dep", "npm:1"]],
            "linkType": "SOFT"
        });
        let mut top = package.clone();
        top["discardFromLookup"] = json!(true);
        let data = json!({
            "enableTopLevelFallback": false, "ignorePatternData": null,
            "dependencyTreeRoots": [{"name": "root", "reference": "workspace:."}],
            "fallbackPool": [], "fallbackExclusionList": [],
            "packageRegistryData": [
                [null, [[null, top]]], ["root", [["workspace:.", package]]],
                ["dep", [["npm:1", {
                    "packageLocation": "./packages.zip/node_modules/dep/",
                    "packageDependencies": [], "linkType": "HARD"
                }]]]
            ]
        });
        if split {
            fs::write(
                root.path().join(".pnp.data.json"),
                serde_json::to_vec(&data).unwrap(),
            )
            .unwrap();
            fs::write(
                root.path().join(".pnp.cjs"),
                "const pnpDataFilepath = path.resolve(__dirname, \".pnp.data.json\");\nthrow new \
                 Error('must not evaluate');\n",
            )
            .unwrap();
        } else {
            let data = serde_json::to_string(&data)
                .unwrap()
                .replace('\\', "\\\\")
                .replace('\'', "\\'");
            fs::write(
                root.path().join(".pnp.cjs"),
                format!(
                    "const RAW_RUNTIME_STATE = '{data}'; throw new Error('must not evaluate');"
                ),
            )
            .unwrap();
        }
        fs::write(root.path().join("source.txt"), "source").unwrap();
        fs::create_dir(root.path().join("output")).unwrap();
        let source = PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("tests/native-conformance.c");
        let binary = root.path().join("native-probe");
        let mut compiler = Command::new("cc");
        compiler.args(["-Wall", "-Wextra", "-Werror"]);
        if static_binary {
            compiler.args(["-static", "-DPNPORT_WATCH_ONLY"]);
        }
        compiler.arg(&source).arg("-o").arg(&binary);
        if cfg!(target_os = "linux") && !static_binary {
            compiler.arg("-ldl");
        }
        assert!(
            compiler.status().unwrap().success(),
            "Compile native fixture"
        );
        let extension = if cfg!(target_os = "macos") {
            "dylib"
        } else {
            "so"
        };
        if !static_binary {
            for (name, nested) in [("libprobe", false), ("libnested", true)] {
                let mut compiler = Command::new("cc");
                compiler.args(["-Wall", "-Wextra", "-Werror", "-DPNPORT_LIBRARY"]);
                if nested {
                    compiler.arg("-DPNPORT_NESTED_LIBRARY");
                }
                if cfg!(target_os = "macos") {
                    compiler.arg("-dynamiclib");
                } else {
                    compiler.args(["-shared", "-fPIC", "-ldl"]);
                }
                assert!(
                    compiler
                        .arg(&source)
                        .arg("-o")
                        .arg(root.path().join(format!("{name}.{extension}")))
                        .status()
                        .unwrap()
                        .success(),
                    "Compile constructor fixture"
                );
            }
        }
        let mut archive =
            zip::ZipWriter::new(fs::File::create(root.path().join("packages.zip")).unwrap());
        for (name, bytes, mode) in [
            ("file.txt".to_owned(), b"package bytes".to_vec(), 0o644),
            (
                "package.json".to_owned(),
                br#"{"name":"dep","version":"1.0.0","bin":{"pnport-native-probe":"bin/probe"}}"#
                    .to_vec(),
                0o644,
            ),
            ("bin/probe".to_owned(), fs::read(&binary).unwrap(), 0o755),
        ]
        .into_iter()
        .chain(if static_binary {
            vec![]
        } else {
            ["libprobe", "libnested"]
                .map(|name| {
                    let name = format!("{name}.{extension}");
                    let bytes = fs::read(root.path().join(&name)).unwrap();
                    (name, bytes, 0o755)
                })
                .to_vec()
        }) {
            archive
                .start_file(
                    format!("node_modules/dep/{name}"),
                    zip::write::SimpleFileOptions::default().unix_permissions(mode),
                )
                .unwrap();
            archive.write_all(&bytes).unwrap();
        }
        archive.finish().unwrap();
        Self { root, binary }
    }

    fn run(&self, executable: &std::ffi::OsStr, mode: &str) {
        self.run_with_cache(executable, mode, &self.root.path().join("native-cache"));
    }

    fn run_with_cache(&self, executable: &std::ffi::OsStr, mode: &str, cache: &Path) {
        use std::os::unix::process::CommandExt;
        let result = Command::new(pnport_binary())
            .current_dir(self.root.path())
            .arg("--cache-dir")
            .arg(cache)
            .args(["--color", "never", "run", "--"])
            .arg(executable)
            .arg(mode)
            // Bin resolution must use declared dependency metadata before PATH.
            .env("PATH", "/absent-pnport-native-conformance-path")
            .process_group(0)
            .output()
            .unwrap();
        assert_eq!(
            result.status.code(),
            Some(0),
            "{mode}: {}",
            String::from_utf8_lossy(&result.stderr)
        );
        assert_eq!(result.stdout, b"pnport native conformance\n");
        assert!(!self.root.path().join("node_modules").exists());
    }
}

#[test]
fn zip_libraries_preserve_constructor_nested_loading_and_fork_interception() {
    for split in [false, true] {
        let fixture = Fixture::new(split, false);
        let negative = Command::new(&fixture.binary)
            .current_dir(fixture.root.path())
            .arg("library")
            .output()
            .unwrap();
        assert_eq!(
            negative.status.code(),
            Some(60),
            "Unvirtualized ZIP library must be unavailable"
        );
        fixture.run(fixture.binary.as_os_str(), "library");
    }
}

#[test]
fn zip_package_bins_preserve_mmap_readonly_data_and_source_watch_events() {
    for split in [false, true] {
        let fixture = Fixture::new(split, false);
        let negative = Command::new(&fixture.binary)
            .current_dir(fixture.root.path())
            .arg("watch")
            .output()
            .unwrap();
        assert_eq!(
            negative.status.code(),
            Some(10),
            "Unvirtualized dependency must be unavailable"
        );
        fixture.run(std::ffi::OsStr::new("pnport-native-probe"), "watch");
        assert_eq!(
            fs::read(fixture.root.path().join("source.txt")).unwrap(),
            b"edited"
        );
        assert_eq!(
            fs::read(fixture.root.path().join("output/new.txt")).unwrap(),
            b"output"
        );
    }
}

#[cfg(target_os = "linux")]
#[test]
fn static_children_preserve_file_and_directory_watch_events() {
    for split in [false, true] {
        let fixture = Fixture::new(split, true);
        fixture.run(fixture.binary.as_os_str(), "watch");
    }
}

#[test]
fn dependency_descriptor_mutations_fail_and_output_descriptor_reuse_remains_native() {
    for split in [false, true] {
        let fixture = Fixture::new(split, false);
        fixture.run(fixture.binary.as_os_str(), "mutations");
        assert_eq!(
            fs::metadata(fixture.root.path().join("output/mutations.txt"))
                .unwrap()
                .len(),
            7
        );
    }
}

#[test]
fn dependency_descriptor_mutations_remain_readonly_with_a_symlinked_cache_ancestor() {
    for split in [false, true] {
        let fixture = Fixture::new(split, false);
        let real = fixture.root.path().join("cache-storage");
        let alias = fixture.root.path().join("cache-storage-alias");
        fs::create_dir(&real).unwrap();
        std::os::unix::fs::symlink(&real, &alias).unwrap();
        // Reproduce Darwin's /var versus /private/var spelling without depending
        // on TMPDIR. The cache leaf is private storage, not a symlink itself.
        fixture.run_with_cache(
            fixture.binary.as_os_str(),
            "mutations",
            &alias.join("native-cache"),
        );
        assert_eq!(
            fs::metadata(fixture.root.path().join("output/mutations.txt"))
                .unwrap()
                .len(),
            7
        );
    }
}
