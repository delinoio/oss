#![cfg(any(target_os = "linux", target_os = "macos"))]
use std::{
    fs,
    path::Path,
    process::{Command, Output},
};

use serde_json::{Value, json};

fn fixture(code: &str) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    fs::write(temp.path().join("tsconfig.json"), r#"{"compilerOptions":{"target":"ES2022","module":"ESNext","moduleResolution":"bundler","strict":true}}"#).unwrap();
    fs::write(temp.path().join("main.ts"), code).unwrap();
    fs::write(temp.path().join("package.json"), r#"{"type":"module"}"#).unwrap();
    temp
}

fn nested_fixture(root_dir: Option<&str>, imports_outside: bool) -> tempfile::TempDir {
    let temp = tempfile::tempdir().unwrap();
    let mut config = json!({
        "compilerOptions": {
            "target": "ES2022",
            "module": "ESNext",
            "moduleResolution": "Bundler",
            "strict": true
        },
        "include": ["src"]
    });
    if let Some(root_dir) = root_dir {
        config["compilerOptions"]["rootDir"] = root_dir.into();
    }
    fs::write(temp.path().join("tsconfig.json"), config.to_string()).unwrap();
    fs::create_dir(temp.path().join("src")).unwrap();
    if imports_outside {
        fs::create_dir(temp.path().join("lib")).unwrap();
        fs::write(
            temp.path().join("src/main.ts"),
            "import { answer } from '../lib/util'; console.log(answer);",
        )
        .unwrap();
        fs::write(temp.path().join("lib/util.ts"), "export const answer = 42;").unwrap();
    } else {
        fs::write(
            temp.path().join("src/main.ts"),
            "export const answer: number = 42; console.log(answer);",
        )
        .unwrap();
    }
    temp
}

fn run_nested(root: &Path, args: &[&str]) -> Output {
    Command::new(env!("CARGO_BIN_EXE_typeopt"))
        .current_dir(root)
        .env("NO_COLOR", "1")
        .args(["--project", "tsconfig.json", "--entry", "src/main.ts"])
        .args(args)
        .output()
        .unwrap()
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
        // These fixtures exercise the property passes through tiny helper
        // functions that the call passes would otherwise inline first.
        .args([
            "--disable-pass",
            "call-inlining",
            "--disable-pass",
            "method-devirtualization",
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
    let manifest: Value =
        serde_json::from_slice(&fs::read(root.join("out/modules.json")).unwrap()).unwrap();
    let digest: String = {
        use sha2::{Digest, Sha256};
        Sha256::digest(code.as_bytes())
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect()
    };
    assert_eq!(manifest["modules"][0]["source_sha256"], digest.as_str());
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
        // Non-literal values keep constant-property-inlining out of this
        // property-allocation fixture.
        "export {}; const first = {sharedProperty: Number(1), firstOther: Number(2)}; const \
         second = {sharedProperty: Number(3), secondOther: Number(4)}; \
         console.log(first.sharedProperty, first.sharedProperty, first.firstOther, \
         second.sharedProperty, second.secondOther, second.secondOther);",
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
        "export {}; const obj = {longProperty: 3}; export function read(v: {longProperty: \
         number}) {return v.longProperty}; console.log(read(obj));",
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
    assert_eq!(report["schema_version"], 1);
    assert!(!temp.path().join("out").exists());
}

#[test]
fn nested_entry_checks_and_publishes_numbered_output() {
    let temp = nested_fixture(None, false);
    let check = run_nested(temp.path(), &["--check", "--verbose"]);
    assert!(
        check.status.success(),
        "{}",
        String::from_utf8_lossy(&check.stderr)
    );
    let report: Value = serde_json::from_slice(&check.stdout).unwrap();
    assert_eq!(report["schema_version"], 1);
    assert!(String::from_utf8_lossy(&check.stderr).contains("root_dir=/"));
    assert!(!temp.path().join("out").exists());

    success(run_nested(temp.path(), &["--out-dir", "out"]));
    for file in [
        "0.js",
        "0.js.map",
        "modules.json",
        "properties.json",
        "report.json",
    ] {
        assert!(temp.path().join("out").join(file).is_file(), "{file}");
    }
    let manifest: Value =
        serde_json::from_slice(&fs::read(temp.path().join("out/modules.json")).unwrap()).unwrap();
    assert!(
        manifest["modules"]
            .as_array()
            .unwrap()
            .iter()
            .any(|module| {
                module["source"]
                    .as_str()
                    .is_some_and(|source| source.ends_with("/src/main.ts"))
            })
    );
}

#[test]
fn narrow_root_dir_does_not_reject_an_import_outside_include() {
    let temp = nested_fixture(Some("src"), true);
    let check = run_nested(temp.path(), &["--check"]);
    assert!(
        check.status.success(),
        "{}",
        String::from_utf8_lossy(&check.stderr)
    );
    assert_eq!(
        serde_json::from_slice::<Value>(&check.stdout).unwrap()["schema_version"],
        1
    );
}

#[test]
fn configured_root_dir_does_not_change_emitted_bytes() {
    let temp = nested_fixture(Some("."), true);
    success(run_nested(temp.path(), &["--out-dir", "configured"]));
    let mut config: Value =
        serde_json::from_slice(&fs::read(temp.path().join("tsconfig.json")).unwrap()).unwrap();
    config["compilerOptions"]
        .as_object_mut()
        .unwrap()
        .remove("rootDir");
    fs::write(temp.path().join("tsconfig.json"), config.to_string()).unwrap();
    success(run_nested(temp.path(), &["--out-dir", "default"]));
    for file in ["0.js", "1.js", "0.js.map", "1.js.map", "properties.json"] {
        assert_eq!(
            fs::read(temp.path().join("configured").join(file)).unwrap(),
            fs::read(temp.path().join("default").join(file)).unwrap(),
            "{file}"
        );
    }
}

#[test]
fn source_maps_refer_to_original_unicode_input() {
    let code = "export {}; const text = '😀'; function identity(v: {longProperty: number}) { \
                return v; } const obj = identity({longProperty: 4}); console.log(text, \
                obj.longProperty);";
    let temp = fixture(code);
    success(run(temp.path(), "out"));
    compare(temp.path());
    let bytes = fs::read(temp.path().join("out/0.js.map")).unwrap();
    let map = sourcemap::SourceMap::from_slice(&bytes).unwrap();
    assert_eq!(map.get_source_contents(0), Some(code));
    let mapping: Value =
        serde_json::from_slice(&fs::read(temp.path().join("out/properties.json")).unwrap())
            .unwrap();
    let renamed = mapping["properties"][0]["renamed"].as_str().unwrap();
    assert_ne!(renamed, "longProperty");
    let emitted = fs::read_to_string(temp.path().join("out/0.js")).unwrap();
    let offset = emitted.rfind(&format!("obj.{renamed}")).unwrap() + "obj.".len();
    let prefix = &emitted[..offset];
    let line = prefix.bytes().filter(|&b| b == b'\n').count() as u32;
    let column = prefix.rsplit('\n').next().unwrap().encode_utf16().count() as u32;
    let token = map.lookup_token(line, column).unwrap();
    let original_column = code[..code.rfind("longProperty").unwrap()]
        .encode_utf16()
        .count();
    assert_eq!(token.get_src_line(), 0);
    assert_eq!(token.get_src_col(), original_column as u32);
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

#[test]
fn unsupported_entries_fail_and_commonjs_javascript_passes_through() {
    for name in ["main.cts", "main.d.ts"] {
        let temp = fixture("export {};");
        fs::rename(temp.path().join("main.ts"), temp.path().join(name)).unwrap();
        let result = Command::new(env!("CARGO_BIN_EXE_typeopt"))
            .current_dir(temp.path())
            .args([
                "--project",
                "tsconfig.json",
                "--entry",
                name,
                "--out-dir",
                "out",
            ])
            .output()
            .unwrap();
        assert!(!result.status.success());
        assert!(!temp.path().join("out").exists());
    }
    let temp = fixture("export {};");
    fs::write(
        temp.path().join("tsconfig.json"),
        r#"{"compilerOptions":{"module":"ESNext","allowJs":true}}"#,
    )
    .unwrap();
    fs::write(temp.path().join("main.cjs"), "module.exports = 3;").unwrap();
    success(
        Command::new(env!("CARGO_BIN_EXE_typeopt"))
            .current_dir(temp.path())
            .args([
                "--project",
                "tsconfig.json",
                "--entry",
                "main.cjs",
                "--out-dir",
                "out",
            ])
            .output()
            .unwrap(),
    );
    let manifest: Value =
        serde_json::from_slice(&fs::read(temp.path().join("out/modules.json")).unwrap()).unwrap();
    assert_eq!(manifest["modules"][0]["kind"], "passthrough");
}

#[test]
fn interprocedural_arguments_returns_recursion_and_captures() {
    let cases = [
        "export {}; function identity(v: {longProperty: number}) { return v; } function \
         forward(v: {longProperty: number}) { return identity(v); } const obj = {longProperty: \
         4}; const result = forward(obj); console.log(result.longProperty, obj.longProperty);",
        "export {}; function make() { return {longProperty: 5}; } const result = make(); \
         console.log(result.longProperty);",
        "export {}; function read(v: {longProperty: number}) { return v.longProperty; } \
         console.log(read({longProperty: 7}));",
        "export {}; function recur(v: {longProperty: number}, depth: number): {longProperty: \
         number} { if (depth > 0) return recur(v, depth - 1); return v; } const result = \
         recur({longProperty: 8}, 3); console.log(result.longProperty);",
        "export {}; const obj = {longProperty: 9}; function capture() { return obj; } function \
         forward() { return capture(); } console.log(forward().longProperty);",
        "export {}; const identity = (v: {longProperty: number}) => v; let first = {longProperty: \
         10}; const second = {longProperty: 11}; first = identity(second); \
         console.log(first.longProperty, second.longProperty);",
        "export {}; function left(v: {longProperty: number}, n: number): {longProperty: number} { \
         if (n > 0) return right(v, n - 1); return v; } function right(v: {longProperty: number}, \
         n: number): {longProperty: number} { return left(v, n); } \
         console.log(left({longProperty: 12}, 4).longProperty);",
    ];
    for code in cases {
        let temp = fixture(code);
        success(run(temp.path(), "out"));
        compare(temp.path());
        assert!(
            report(temp.path())["renamed_properties"].as_u64().unwrap() > 0,
            "{code}"
        );
    }
}

#[test]
fn shared_callee_unifies_shapes_and_preserved_names_without_collisions() {
    let temp = fixture(
        "export {}; function read(v: {sharedProperty: number}) { return v.sharedProperty; } const \
         first = {sharedProperty: 3, a: 10}; const second = {sharedProperty: 4, anotherProperty: \
         20}; console.log(read(first), read(second), first.a, second.anotherProperty);",
    );
    success(run(temp.path(), "out"));
    compare(temp.path());
    let mapping: Value =
        serde_json::from_slice(&fs::read(temp.path().join("out/properties.json")).unwrap())
            .unwrap();
    let shared: Vec<_> = mapping["properties"]
        .as_array()
        .unwrap()
        .iter()
        .filter(|m| m["original"] == "sharedProperty")
        .collect();
    assert_eq!(shared.len(), 1);
    assert_ne!(shared[0]["renamed"], "a");
    success(run(temp.path(), "again"));
    for file in ["0.js", "0.js.map", "modules.json", "properties.json"] {
        assert_eq!(
            fs::read(temp.path().join("out").join(file)).unwrap(),
            fs::read(temp.path().join("again").join(file)).unwrap()
        );
    }
}

#[test]
fn interprocedural_uncertainty_and_transitive_escapes_preserve_groups() {
    let cases = [
        "export {}; function identity(v: {longProperty: number}) { return v; } const obj = \
         {longProperty: 1}; console.log(JSON.stringify(identity(obj)));",
        "export {}; function inner(v: {longProperty: number}) { console.log(Object.keys(v)); } \
         function outer(v: {longProperty: number}) { inner(v); } const obj = {longProperty: 2}; \
         outer(obj);",
        "export {}; function read(v: {longProperty: number}) { return v.longProperty; } const obj \
         = {longProperty: 3}; const unknown = JSON.parse('{\"longProperty\":4}'); \
         console.log(read(obj), read(unknown));",
        "export {}; function read(v: {longProperty: number}) { return v.longProperty; } const obj \
         = {longProperty: 5}; const fn = read; console.log(fn(obj));",
        "export {}; function read(v: {longProperty: number}) { return v.longProperty; } const obj \
         = {longProperty: 6}; console.log([obj].map(read));",
        "export {}; function identity(v: {longProperty: number}) { return v; } const first = \
         {longProperty: 7}; const second = {longProperty: 8}; \
         console.log(identity(first).longProperty, identity(second).longProperty, \
         Object.keys(second));",
        "export {}; function hold(v: {longProperty: number}) { return {v}; } const obj = \
         {longProperty: 9}; console.log(JSON.stringify(hold(obj)));",
        "export {}; interface Shape {longProperty: number} function read(v: Shape) { return \
         v.longProperty; } const obj = {longProperty: 10}; console.log(read(obj));",
        "export {}; function outer(v: {longProperty: number}) { return () => v; } const obj = \
         {longProperty: 11}; console.log(JSON.stringify(outer(obj)()));",
        "export {}; function read(v: {longProperty: number}) { return v.longProperty; } const obj \
         = {longProperty: 12}; export {read}; console.log(read(obj));",
        "export {}; async function read(v: {longProperty: number}) { return v.longProperty; } \
         const obj = {longProperty: 13}; console.log(await read(obj));",
        "export {}; function* read(v: {longProperty: number}) { yield v.longProperty; } const obj \
         = {longProperty: 14}; console.log([...read(obj)]);",
        "export {}; const obj = {longProperty: 15}; function read(v = obj) { return \
         v.longProperty; } console.log(read());",
        "export {}; function read(...values: {longProperty: number}[]) { return \
         values[0].longProperty; } const obj = {longProperty: 16}; console.log(read(obj));",
        "export {}; let obj = {longProperty: 17}; function replace() { obj = \
         JSON.parse('{\"longProperty\":18}'); } replace(); console.log(obj.longProperty);",
    ];
    for code in cases {
        let temp = fixture(code);
        success(run(temp.path(), "out"));
        compare(temp.path());
        assert_eq!(report(temp.path())["renamed_properties"], 0, "{code}");
    }
}

#[test]
fn accessors_reflection_and_prototype_lookup_keep_names() {
    let cases = [
        "export {}; const obj = {inner: 0, set longProperty(v: number) {this.inner = v * 2}}; \
         obj.longProperty = 3; console.log(obj.inner);",
        "export {}; let writes = 0; const obj = {inner: 1, get longProperty() {return \
         this.inner}, set longProperty(v: number) {writes++; this.inner = v}}; obj.longProperty \
         += 4; console.log(obj.longProperty, writes);",
        "export {}; const obj = {longProperty: 3}; console.log(Reflect.ownKeys(obj));",
        "export {}; const obj = {longProperty: 3}; console.log(Reflect.has(obj, 'longProperty'));",
        "export {}; const obj = {longProperty: 3}; console.log(Reflect.get(obj, 'longProperty'));",
        "export {}; const obj = {longProperty: 3}; console.log(Object.hasOwn(obj, \
         'longProperty'));",
        "export {}; const obj = {longProperty: 3}; \
         console.log(obj.hasOwnProperty('longProperty'));",
        "export {}; const obj = {longProperty: 3}; console.log(Object.entries(obj));",
        "export {}; const obj = {longProperty: 3}; console.log(Object.getOwnPropertyNames(obj));",
        "export {}; const obj = {longProperty: 3}; const copy = Object.assign({}, obj); \
         console.log(copy.longProperty, Object.keys(copy));",
        "export {}; const obj = {longProperty: 3}; Object.defineProperty(obj, 'longProperty', \
         {value: 4}); console.log(obj.longProperty);",
        "export {}; const obj = {longProperty: 3}; const keys: string[] = []; for (const key in \
         obj) keys.push(key); console.log(keys);",
        "export {}; const base = {longProperty: 3}; const child: {longProperty: number} = \
         Object.create(base); console.log(child.longProperty, Object.keys(child));",
    ];
    for code in cases {
        let temp = fixture(code);
        success(run(temp.path(), "out"));
        compare(temp.path());
        assert_eq!(report(temp.path())["renamed_properties"], 0, "{code}");
    }
}

#[test]
fn class_inheritance_overrides_and_super_keep_behavior() {
    // Closed classes: every instance and receiver is known, so the field
    // and the method are renamed together across the hierarchy.
    let temp = fixture(
        "export {}; class Base {longProperty = 1; read() {return this.longProperty}} class Child \
         extends Base {longProperty = 2} console.log(new Base().read(), new Child().read());",
    );
    success(run(temp.path(), "out"));
    compare(temp.path());
    assert!(report(temp.path())["renamed_properties"].as_u64().unwrap() >= 2);
    let cases = [
        "export {}; class Base {longProperty = 1; describe() {return `base \
         ${this.longProperty}`}} class Child extends Base {describe() {return `child \
         ${super.describe()}`}} const items: Base[] = [new Base(), new Child()]; \
         console.log(items.map((item) => item.describe()));",
        "export {}; class Base {get longProperty() {return 1}} class Child extends Base {get \
         longProperty() {return super.longProperty + 10}} console.log(new Child().longProperty, \
         Object.getPrototypeOf(new Child()) === Child.prototype);",
        "export {}; class Base {static longProperty = 3; static read() {return \
         this.longProperty}} class Child extends Base {static longProperty = 4} \
         console.log(Base.read(), Child.read());",
        "export {}; class Base {longProperty = 5} const instance = new Base(); \
         console.log(instance.longProperty, Object.keys(instance), 'longProperty' in instance);",
    ];
    for code in cases {
        let temp = fixture(code);
        success(run(temp.path(), "out"));
        compare(temp.path());
        assert_eq!(report(temp.path())["renamed_properties"], 0, "{code}");
    }
}

#[test]
fn filesystem_failures_fail_without_publishing_or_leaking_staging() {
    use std::os::unix::fs::PermissionsExt;
    let no_staging = |dir: &Path| {
        !fs::read_dir(dir).unwrap().any(|p| {
            p.unwrap()
                .file_name()
                .to_string_lossy()
                .starts_with(".typeopt-")
        })
    };
    let code = "export {}; const obj = {longProperty: 1}; console.log(obj.longProperty);";

    let temp = fixture(code);
    assert!(!run(temp.path(), "missing-parent/out").status.success());
    assert!(!temp.path().join("missing-parent").exists());
    assert!(no_staging(temp.path()));

    let temp = fixture(code);
    fs::write(temp.path().join("out"), "keep").unwrap();
    assert!(!run(temp.path(), "out").status.success());
    assert_eq!(fs::read_to_string(temp.path().join("out")).unwrap(), "keep");
    assert!(no_staging(temp.path()));

    let temp = fixture(code);
    fs::remove_file(temp.path().join("tsconfig.json")).unwrap();
    assert!(!run(temp.path(), "out").status.success());
    assert!(!temp.path().join("out").exists());

    // Permission checks do not apply to root, so the remaining cases need a
    // regular user. SAFETY: geteuid has no preconditions and only reads
    // process credentials.
    if unsafe { libc::geteuid() } == 0 {
        return;
    }

    let temp = fixture(code);
    let locked = temp.path().join("locked");
    fs::create_dir(&locked).unwrap();
    fs::set_permissions(&locked, fs::Permissions::from_mode(0o555)).unwrap();
    let result = run(temp.path(), "locked/out");
    fs::set_permissions(&locked, fs::Permissions::from_mode(0o755)).unwrap();
    assert!(!result.status.success());
    assert_eq!(fs::read_dir(&locked).unwrap().count(), 0);
    assert!(no_staging(temp.path()));

    let temp = fixture(code);
    let entry = temp.path().join("main.ts");
    fs::set_permissions(&entry, fs::Permissions::from_mode(0o000)).unwrap();
    let result = run(temp.path(), "out");
    fs::set_permissions(&entry, fs::Permissions::from_mode(0o644)).unwrap();
    assert!(!result.status.success());
    assert!(!temp.path().join("out").exists());
    assert!(no_staging(temp.path()));
    assert_eq!(fs::read_to_string(&entry).unwrap(), code);
}

#[test]
fn verbose_diagnostics_redact_source_derived_arguments() {
    let temp = fixture("export {}; const token: 'expected' = 'secret-literal-value';");
    let output = run(temp.path(), "out");
    assert!(!output.status.success());
    let stderr = String::from_utf8_lossy(&output.stderr);
    assert!(stderr.contains("TS2322"), "{stderr}");
    assert!(stderr.contains("main.ts:1:"), "{stderr}");
    assert!(stderr.contains("<redacted>"), "{stderr}");
    assert!(!stderr.contains("secret-literal-value"), "{stderr}");
    assert!(!temp.path().join("out").exists());
}

#[test]
fn erased_type_spans_do_not_make_renames_profitable() {
    // Only the erased parameter annotation would shrink; the emitted shorthand
    // would grow to `a: longProperty`, so the property must keep its name.
    // `v` stays a runtime use so that signature-reduction keeps the argument.
    let code = "export {}; const longProperty = 1; function size(v: {longProperty: number}) { \
                return v ? 0 : 1; } console.log(size({longProperty}));";
    let temp = fixture(code);
    success(run(temp.path(), "out"));
    compare(temp.path());
    assert_eq!(report(temp.path())["renamed_properties"], 0);
    let emitted = fs::read_to_string(temp.path().join("out/0.js")).unwrap();
    assert!(emitted.contains("size({ longProperty })"), "{emitted}");
}

#[test]
fn preserve_module_rejects_commonjs_syntax() {
    let preserve = r#"{"compilerOptions":{"module":"preserve","target":"ES2022","moduleResolution":"bundler","strict":true}}"#;
    for (code, message) in [
        (
            "import value = require('./value'); console.log(value.longProperty);",
            "import = require()",
        ),
        ("const obj = {longProperty: 1}; export = obj;", "export ="),
    ] {
        let temp = fixture(code);
        fs::write(temp.path().join("tsconfig.json"), preserve).unwrap();
        fs::write(
            temp.path().join("value.ts"),
            "export const longProperty = 1;",
        )
        .unwrap();
        let output = run(temp.path(), "out");
        assert!(!output.status.success(), "{code}");
        assert!(
            String::from_utf8_lossy(&output.stderr).contains(message),
            "{}",
            String::from_utf8_lossy(&output.stderr)
        );
        assert!(!temp.path().join("out").exists());
    }
    let temp = fixture("export {}; const obj = {longProperty: 1}; console.log(obj.longProperty);");
    fs::write(temp.path().join("tsconfig.json"), preserve).unwrap();
    success(run(temp.path(), "out"));
    compare(temp.path());
}
