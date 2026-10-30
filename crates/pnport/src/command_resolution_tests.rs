use serde_json::json;

use super::*;

struct Fixture {
    root: tempfile::TempDir,
    view: View,
}

impl Fixture {
    fn new() -> Self {
        let root = tempfile::tempdir().unwrap();
        fs::write(
            root.path().join(".pnp.cjs"),
            r#"const pnpDataFilepath = path.resolve(__dirname, ".pnp.data.json");"#,
        )
        .unwrap();
        let data = json!({
            "enableTopLevelFallback": false,
            "ignorePatternData": null,
            "dependencyTreeRoots": [{"name":"root","reference":"workspace:."}],
            "fallbackPool": [],
            "fallbackExclusionList": [],
            "packageRegistryData": [
                [null, [[null, {
                    "packageLocation":"./",
                    "packageDependencies":[["dep","npm:1"],["other","npm:1"]],
                    "linkType":"SOFT",
                    "discardFromLookup":true
                }]]],
                ["root", [["workspace:.", {
                    "packageLocation":"./",
                    "packageDependencies":[["dep","npm:1"],["other","npm:1"]],
                    "linkType":"SOFT"
                }]]],
                ["dep", [["npm:1", {
                    "packageLocation":"./dep/",
                    "packageDependencies":[],
                    "linkType":"HARD"
                }]]],
                ["other", [["npm:1", {
                    "packageLocation":"./other/",
                    "packageDependencies":[],
                    "linkType":"HARD"
                }]]]
            ]
        });
        fs::write(
            root.path().join(".pnp.data.json"),
            serde_json::to_vec(&data).unwrap(),
        )
        .unwrap();
        for package in ["dep", "other"] {
            fs::create_dir(root.path().join(package)).unwrap();
            fs::write(root.path().join(package).join("package.json"), b"{}\n").unwrap();
        }
        let graph = Graph::load(&root.path().join(".pnp.cjs")).unwrap();
        let view = View::new(
            graph,
            Cache::open(root.path().join("cache")).unwrap(),
            root.path().join("session"),
        );
        Self { root, view }
    }

    fn canonical_cwd(&self) -> PathBuf {
        self.view
            .graph
            .snapshot
            .manifest_path
            .parent()
            .unwrap()
            .to_owned()
    }

    fn bin(&self, package: &str, name: &str) {
        fs::write(
            self.root.path().join(package).join("package.json"),
            serde_json::to_vec(&json!({"name":package,"bin":{name:"bin/fixture.exe"}})).unwrap(),
        )
        .unwrap();
    }
}

#[test]
fn absolute_explicit_command_skips_unsupported_cwd_lookup() {
    let mut fixture = Fixture::new();
    let cwd = Path::new(r"C:\unsupported-reparse\caller");
    let command = OsString::from(r"C:\tools\fixture.exe");
    let mut lookup_calls = 0;
    let resolved = resolve_command_with_cwd_lookup(&mut fixture.view, cwd, &command, |_, _| {
        lookup_calls += 1;
        Err(Error::new(
            Code::PnportUnsupportedOperation,
            "The Windows pathname uses an unsupported reparse traversal.",
        ))
    })
    .unwrap();
    assert_eq!(resolved, PathBuf::from(&command));
    assert_eq!(lookup_calls, 0);
}

#[test]
fn relative_explicit_command_uses_original_cwd_without_lookup() {
    let mut fixture = Fixture::new();
    let cwd = Path::new(r"C:\unsupported-reparse\caller");
    for command in [r".\tools\fixture.exe", r"..\tools\fixture.exe"] {
        let command = OsString::from(command);
        let mut lookup_calls = 0;
        let resolved = resolve_command_with_cwd_lookup(&mut fixture.view, cwd, &command, |_, _| {
            lookup_calls += 1;
            Err(Error::new(
                Code::PnportUnsupportedOperation,
                "The Windows pathname uses an unsupported reparse traversal.",
            ))
        })
        .unwrap();
        assert_eq!(resolved, cwd.join(&command));
        assert_eq!(lookup_calls, 0);
    }
}

#[test]
fn bare_command_preserves_cwd_lookup_errors_before_bin_selection() {
    let mut fixture = Fixture::new();
    fixture.bin("dep", "fixture");
    let cwd = fixture.root.path();
    for code in [
        Code::PnportUnsupportedOperation,
        Code::PnportFilesystemConflict,
    ] {
        let mut lookup_calls = 0;
        let error = resolve_command_with_cwd_lookup(
            &mut fixture.view,
            cwd,
            &OsString::from("fixture"),
            |observed_cwd, _| {
                assert_eq!(observed_cwd, cwd);
                lookup_calls += 1;
                Err(Error::new(code, "guarded cwd lookup rejected"))
            },
        )
        .unwrap_err();
        assert_eq!(error.code, code);
        assert_eq!(error.message, "guarded cwd lookup rejected");
        assert_eq!(lookup_calls, 1);
    }
}

#[test]
fn bare_command_uses_canonical_cwd_for_dependency_ownership() {
    let mut fixture = Fixture::new();
    fixture.bin("dep", "fixture");
    let canonical_cwd = fixture.canonical_cwd();
    let caller_cwd = Path::new(r"C:\caller-alias");
    let resolved = resolve_command_with_cwd_lookup(
        &mut fixture.view,
        caller_cwd,
        &OsString::from("fixture"),
        |observed_cwd, _| {
            assert_eq!(observed_cwd, caller_cwd);
            Ok(canonical_cwd.clone())
        },
    )
    .unwrap();
    assert_eq!(resolved, canonical_cwd.join(r"dep\bin\fixture.exe"));
}

#[test]
fn bare_command_rejects_ambiguous_dependency_bins() {
    let mut fixture = Fixture::new();
    fixture.bin("dep", "fixture");
    fixture.bin("other", "fixture");
    let cwd = fixture.root.path().to_owned();
    let error = resolve_command(&mut fixture.view, &cwd, &OsString::from("fixture")).unwrap_err();
    assert_eq!(error.code, Code::PnportResolutionFailed);
    assert!(error
        .message
        .starts_with("Several direct dependencies provide this bin"));
}

#[test]
fn bare_command_keeps_native_casing_lookup_and_path_fallback() {
    let mut fixture = Fixture::new();
    fixture.bin("dep", "fixture");
    let cwd = fixture.root.path().to_str().unwrap().to_uppercase();
    let canonical_cwd = fixture.canonical_cwd();
    let resolved = resolve_command(
        &mut fixture.view,
        Path::new(&cwd),
        &OsString::from("fixture"),
    )
    .unwrap();
    assert_eq!(resolved, canonical_cwd.join(r"dep\bin\fixture.exe"));
    let command = OsString::from("cmd.exe");
    let expected = pnport::executable::find_on_path(
        &command,
        std::env::var_os("PATH").as_deref(),
        &canonical_cwd,
    )
    .expect("Windows test environment must provide cmd.exe on PATH");
    assert_eq!(
        resolve_command(&mut fixture.view, Path::new(&cwd), &command).unwrap(),
        expected
    );
}
