#![cfg(any(target_os = "linux", target_os = "macos"))]
use std::{
    fs,
    path::Path,
    process::{Command, Output},
};

use serde_json::Value;

fn fixture(code: &str) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    fs::write(temp.path().join("tsconfig.json"), r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true}}"#).unwrap();
    fs::write(temp.path().join("main.ts"), code).unwrap();
    fs::write(temp.path().join("package.json"), r#"{"type":"module"}"#).unwrap();
    temp
}
fn run(root: &Path, destination: &str) -> Output {
    Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(root)
        .args([
            "--project",
            "tsconfig.json",
            "--entry",
            "main.ts",
            "--out-dir",
            destination,
            "--verbose",
        ])
        .output()
        .unwrap()
}
fn success(output: Output) {
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
}
fn report(root: &Path) -> Value {
    serde_json::from_slice(&fs::read(root.join("out/report.json")).unwrap()).unwrap()
}
fn compare(root: &Path) {
    let before = Command::new("node")
        .arg(root.join("main.ts"))
        .output()
        .unwrap();
    assert!(
        before.status.success(),
        "{}",
        String::from_utf8_lossy(&before.stderr)
    );
    let after = Command::new("node")
        .arg(root.join("out/0.js"))
        .output()
        .unwrap();
    assert!(
        after.status.success(),
        "{}",
        String::from_utf8_lossy(&after.stderr)
    );
    assert_eq!(before.stdout, after.stdout);
}

#[test]
fn shortening_aliases_side_effects_and_determinism() {
    let code = "export {}; let unused: number; let n = 0; const thing = {longProperty: ++n, \
                otherProperty: ++n}; let alias = thing; alias.longProperty += 10; \
                console.log(alias.longProperty, thing['otherProperty'], n);";
    let temp = fixture(code);
    let root = temp.path();
    success(run(root, "out"));
    compare(root);
    assert_eq!(report(root)["renamed_properties"], 2);
    assert_eq!(fs::read_to_string(root.join("main.ts")).unwrap(), code);
    success(run(root, "again"));
    for file in ["0.js", "0.js.map", "modules.json", "properties.json"] {
        assert_eq!(
            fs::read(root.join("out").join(file)).unwrap(),
            fs::read(root.join("again").join(file)).unwrap()
        );
    }
    assert!(!run(root, "out").status.success());
    compare(root);
}

#[test]
fn distinct_disambiguation_and_ambiguation_effects() {
    let temp = fixture(
        "export {}; const first = {sharedProperty: 1, firstOther: 2}; const second = \
         {sharedProperty: 3, secondOther: 4}; console.log(first.sharedProperty, \
         first.sharedProperty, first.firstOther, second.sharedProperty, second.secondOther, \
         second.secondOther);",
    );
    success(run(temp.path(), "out"));
    compare(temp.path());
    let mapping: Value =
        serde_json::from_slice(&fs::read(temp.path().join("out/properties.json")).unwrap())
            .unwrap();
    let properties = mapping["properties"].as_array().unwrap();
    let same: Vec<_> = properties
        .iter()
        .filter(|p| p["original"] == "sharedProperty")
        .collect();
    assert_ne!(same[0]["renamed"], same[1]["renamed"]);
    assert_eq!(properties[0]["renamed"], properties[2]["renamed"]);
}

#[test]
fn preservation_boundaries_keep_behavior() {
    let cases = [
        "export {}; const obj = {longProperty: 3}; console.log(JSON.stringify(obj));",
        "export {}; const obj = {longProperty: 3}; const holder = {obj}; \
         console.log(JSON.stringify(holder));",
        "export {}; const obj = {longProperty: 3}; const alias = obj; \
         console.log(Object.keys(alias));",
        "export {}; const obj = {longProperty: 3}; const key: string = 'longProperty'; \
         console.log(obj[key as keyof typeof obj]);",
        "export const obj = {longProperty: 3}; console.log(obj.longProperty);",
        "const obj = {longProperty: 3}; export {obj}; console.log(obj.longProperty);",
        "export {}; interface Shape {longProperty: number}; const obj: Shape = {longProperty: 3}; \
         console.log(obj.longProperty);",
        "export {}; const obj = {longProperty: () => 3}; console.log(obj.longProperty.name);",
        "export {}; const obj = {get longProperty() {return 3}}; console.log(obj.longProperty);",
        "export {}; const obj = {longProperty: 3}; console.log('longProperty' in obj);",
        "export {}; const obj = {longProperty: 3}; function read(v: {longProperty: number}) \
         {return v.longProperty}; console.log(read(obj));",
        "export {}; const obj = {longProperty: 3}; const {...rest} = obj; \
         console.log(rest.longProperty);",
        "export {}; const obj = {longProperty: 3}; console.log({...obj});",
    ];
    for code in cases {
        let temp = fixture(code);
        success(run(temp.path(), "out"));
        compare(temp.path());
        assert_eq!(report(temp.path())["renamed_properties"], 0, "{code}");
    }
}

#[test]
fn destructuring_preserves_bindings_defaults_and_side_effects() {
    let temp = fixture(
        "export {}; let calls = 0; const longProperty = 4; const obj = {longProperty, \
         secondProperty: undefined as number | undefined}; const {longProperty: local, \
         secondProperty = ++calls} = obj; const {longProperty: again} = obj; \
         console.log(longProperty, local, secondProperty, again, obj.longProperty, calls);",
    );
    success(run(temp.path(), "out"));
    compare(temp.path());
    assert_eq!(report(temp.path())["renamed_properties"], 2);
    let emitted = fs::read_to_string(temp.path().join("out/0.js")).unwrap();
    assert!(emitted.contains("secondProperty = ++calls"));
}

#[test]
fn malformed_type_errors_eval_and_unsupported_settings_fail_without_output() {
    for code in [
        "export {}; const x: number = 'bad';",
        "export {}; const x = ;",
        "export {}; eval('1')",
        "export {}; const run = eval; run('1')",
        "with ({}) {}",
    ] {
        let temp = fixture(code);
        assert!(!run(temp.path(), "out").status.success(), "{code}");
        assert!(!temp.path().join("out").exists());
        assert!(!fs::read_dir(temp.path()).unwrap().any(|p| {
            p.unwrap()
                .file_name()
                .to_string_lossy()
                .starts_with(".typeopt-")
        }));
    }
    let temp = fixture("export {};");
    fs::write(
        temp.path().join("tsconfig.json"),
        r#"{"compilerOptions":{"module":"commonjs"}}"#,
    )
    .unwrap();
    assert!(!run(temp.path(), "out").status.success());
}

#[test]
fn check_analyzes_without_publishing_or_executing_input() {
    let temp = fixture(
        "export {}; throw new Error('input executed'); const obj = {longProperty: 1}; \
         console.log(obj.longProperty);",
    );
    let output = Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(temp.path())
        .args([
            "--project",
            "tsconfig.json",
            "--entry",
            "main.ts",
            "--check",
        ])
        .output()
        .unwrap();
    assert!(
        output.status.success(),
        "{}",
        String::from_utf8_lossy(&output.stderr)
    );
    let report: Value = serde_json::from_slice(&output.stdout).unwrap();
    assert_eq!(report["schema_version"], 0);
    assert!(!temp.path().join("out").exists());
}

#[test]
fn source_maps_refer_to_original_unicode_input() {
    let code = "export {}; const text = '😀'; const obj = {longProperty: 4}; console.log(text, \
                obj.longProperty);";
    let temp = fixture(code);
    success(run(temp.path(), "out"));
    compare(temp.path());
    let bytes = fs::read(temp.path().join("out/0.js.map")).unwrap();
    let map = sourcemap::SourceMap::from_slice(&bytes).unwrap();
    assert_eq!(map.get_source_contents(0), Some(code));
    assert!(
        map.tokens()
            .any(|token| token.has_source() && token.get_src_col() > 40)
    );
}

#[test]
fn cancellation_removes_only_owned_artifacts() {
    use std::{process::Stdio, thread, time::Duration};
    let temp =
        fixture("export {}; const value = {longProperty: 1}; console.log(value.longProperty);");
    fs::write(temp.path().join("keep.txt"), "keep").unwrap();
    let mut child = Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(temp.path())
        .args([
            "--project",
            "tsconfig.json",
            "--entry",
            "main.ts",
            "--out-dir",
            "out",
        ])
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .unwrap();
    thread::sleep(Duration::from_millis(100));
    // SAFETY: the child is owned by this test; SIGINT requests normal cleanup.
    unsafe {
        libc::kill(child.id() as libc::pid_t, libc::SIGINT);
    }
    assert!(!child.wait().unwrap().success());
    assert!(!temp.path().join("out").exists());
    assert_eq!(
        fs::read_to_string(temp.path().join("keep.txt")).unwrap(),
        "keep"
    );
    assert!(!fs::read_dir(temp.path()).unwrap().any(|p| {
        p.unwrap()
            .file_name()
            .to_string_lossy()
            .starts_with(".typeopt-")
    }));
}

#[test]
fn existing_symlink_and_missing_entry_are_rejected() {
    use std::os::unix::fs::symlink;
    let temp = fixture("export {};");
    symlink("missing-target", temp.path().join("out")).unwrap();
    assert!(!run(temp.path(), "out").status.success());
    assert!(
        fs::symlink_metadata(temp.path().join("out"))
            .unwrap()
            .file_type()
            .is_symlink()
    );
    fs::remove_file(temp.path().join("main.ts")).unwrap();
    assert!(!run(temp.path(), "new").status.success());
}

#[test]
fn inherited_union_and_external_js_shapes_remain_observable() {
    let temp = fixture(
        "import {read} from './external.js'; class Base {longProperty = 4} class Child extends \
         Base {} const obj: Base | Child = new Child(); console.log(read(obj));",
    );
    fs::write(
        temp.path().join("external.js"),
        "export function read(value) {return value.longProperty}",
    )
    .unwrap();
    fs::write(temp.path().join("tsconfig.json"), r#"{"compilerOptions":{"module":"esnext","target":"es2022","allowJs":true,"moduleResolution":"bundler"}}"#).unwrap();
    success(run(temp.path(), "out"));
    assert_eq!(report(temp.path())["renamed_properties"], 0);
    let manifest: Value =
        serde_json::from_slice(&fs::read(temp.path().join("out/modules.json")).unwrap()).unwrap();
    assert!(
        manifest["modules"]
            .as_array()
            .unwrap()
            .iter()
            .any(|m| m["kind"] == "passthrough")
    );
}
