// SPDX-License-Identifier: Apache-2.0
//! Rspack and Rsbuild cache discovery (Go `frontend.go`).
//!
//! Frontend integration protocol (version 1): the project at the exact
//! execution cwd supplies `.ccow/discover.cjs`, exporting an async function
//! `({preset, argv, cwd, env})`. It must use the SAME configuration resolver as
//! the requested build and return `{schema:1, preset, caches:[{type:
//! "persistent", path:"/absolute"}], outputs:["/absolute/final-output"],
//! inputs:["/absolute/config"], settings:{...resolved build settings...}}`.
//! Inputs include all configuration dependencies and absent candidates whose
//! creation could change resolution. Include every compiler's persistent
//! storage and output directory. Return null or throw if an option or dynamic
//! configuration cannot be resolved faithfully. The hook must not build, write
//! artifacts, disable tool validation, or emit to stdout. CacheCow does not
//! infer caches from package names or default paths. The hook is trusted
//! project code, like builds. <https://rspack.rs/config/cache>

use anyhow::{Result, bail};
use serde::{Deserialize, Serialize};
use serde_json::Value;

use super::{
    CachePath, Id, PresetResult, Resolved, Root, RootKind, env_value, null_default, sorted_value,
    supported,
};
use crate::{Context, paths, repository};

const FRONTEND_PROBE: &str = r"
const {createRequire} = require('node:module');
const path = require('node:path');
const fs = require('node:fs');
(async () => {
 const input = JSON.parse(process.argv[1]);
 const local = createRequire(path.join(process.cwd(), 'package.json'));
 // Resolve the package owning the selected bin, rather than a different
 // installation visible from cwd. Unknown wrappers cannot prove this identity.
 const executable = input.argv[0];
 let selected = executable;
 if (!path.isAbsolute(selected) && !selected.includes('/') && !selected.includes('\\')) {
  selected = (process.env.PATH || '').split(path.delimiter).map(dir => path.resolve(dir,executable)).find(p => {
   try { fs.accessSync(p,fs.constants.X_OK); return fs.statSync(p).isFile(); } catch { return false; }
  });
 }
 if (!selected) throw new Error('selected executable unavailable');
 const bin = fs.realpathSync(path.resolve(selected));
 const ownerName = input.preset === 'rspack' ? '@rspack/cli' : '@rsbuild/core';
 let ownerPath;
 for (let dir=path.dirname(bin); ; dir=path.dirname(dir)) {
  const candidate=path.join(dir,'package.json');
  if (fs.existsSync(candidate)) {
   const meta=JSON.parse(fs.readFileSync(candidate,'utf8'));
   const entry=typeof meta.bin === 'string' ? meta.bin : meta.bin?.[input.preset];
   if (meta.name === ownerName && entry && fs.realpathSync(path.resolve(dir,entry)) === bin) { ownerPath=candidate; break; }
  }
  if (path.dirname(dir) === dir) break;
 }
 if (!ownerPath) throw new Error('selected executable is not a verified package bin');
 const selectedRequire=createRequire(ownerPath);
 const pkg = input.preset === 'rspack' ? '@rspack/core' : '@rsbuild/core';
 const toolPath = input.preset === 'rsbuild' ? ownerPath : selectedRequire.resolve(pkg + '/package.json');
 const toolVersion = JSON.parse(fs.readFileSync(toolPath,'utf8')).version;
 const toolRequire = createRequire(toolPath);
 const rspackPath=toolRequire.resolve('@rspack/core/package.json');
 const rspackVersion = JSON.parse(fs.readFileSync(rspackPath,'utf8')).version;
 const discover = local(path.join(process.cwd(), '.ccow/discover.cjs'));
 if (typeof discover !== 'function') throw new Error('invalid integration');
 const result = await discover({...input, cwd:process.cwd(), env:process.env});
 process.stdout.write(JSON.stringify({nodeVersion:process.versions.node,toolVersion,rspackVersion,toolInputs:[bin,ownerPath,toolPath,rspackPath],result}));
})().catch(() => { process.exitCode=1; });
";

#[derive(Serialize)]
struct Payload<'a> {
    preset: Id,
    argv: &'a [String],
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct Probe {
    #[serde(rename = "toolInputs", deserialize_with = "null_default")]
    tool_inputs: Vec<String>,
    #[serde(rename = "nodeVersion", deserialize_with = "null_default")]
    node: String,
    #[serde(rename = "toolVersion", deserialize_with = "null_default")]
    tool: String,
    #[serde(rename = "rspackVersion", deserialize_with = "null_default")]
    rspack: String,
    #[serde(deserialize_with = "null_default")]
    result: ProbeResult,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct ProbeResult {
    #[serde(deserialize_with = "null_default")]
    schema: i64,
    #[serde(deserialize_with = "null_default")]
    preset: String,
    #[serde(deserialize_with = "null_default")]
    caches: Vec<ProbeCache>,
    #[serde(deserialize_with = "null_default")]
    inputs: Vec<String>,
    #[serde(deserialize_with = "null_default")]
    outputs: Vec<String>,
    #[serde(deserialize_with = "null_default")]
    settings: serde_json::Map<String, Value>,
}

#[derive(Deserialize, Default)]
#[serde(default)]
struct ProbeCache {
    #[serde(rename = "type", deserialize_with = "null_default")]
    kind: String,
    #[serde(deserialize_with = "null_default")]
    path: String,
}

/// Go `json.Compact`: validates and removes insignificant whitespace while
/// preserving member order and number spelling.
fn compact(b: &[u8]) -> Option<Vec<u8>> {
    serde_json::from_slice::<serde::de::IgnoredAny>(b).ok()?;
    let mut out = Vec::with_capacity(b.len());
    let (mut in_string, mut escaped) = (false, false);
    for &c in b {
        if in_string {
            out.push(c);
            if escaped {
                escaped = false;
            } else if c == b'\\' {
                escaped = true;
            } else if c == b'"' {
                in_string = false;
            }
        } else if !matches!(c, b' ' | b'\t' | b'\n' | b'\r') {
            if c == b'"' {
                in_string = true;
            }
            out.push(c);
        }
    }
    Some(out)
}

pub(super) fn discover(ctx: &Context, req: &Resolved<'_>, out: &mut PresetResult) -> Result<Value> {
    let integration = paths::join(&[req.cwd.as_str(), ".ccow", "discover.cjs"]);
    if std::fs::metadata(&integration).is_err() {
        bail!(
            "frontend cache discovery requires .ccow/discover.cjs in the execution directory; \
             integrate the actual resolved build configuration"
        );
    }
    repository::check_owner(&integration)?;
    let payload = serde_json::to_string(&Payload {
        preset: out.preset,
        argv: &req.argv,
    })?;
    let probe_argv = vec![
        "node".to_string(),
        "-e".to_string(),
        FRONTEND_PROBE.to_string(),
        payload,
    ];
    let Ok(b) = req.runner.output(ctx, &req.cwd, &req.env, &probe_argv) else {
        bail!(
            "frontend discovery failed; use a direct installed package bin or symlink and verify \
             its configuration"
        );
    };
    let Ok(probe) = serde_json::from_slice::<Probe>(&b) else {
        bail!("frontend integration returned malformed JSON; fix .ccow/discover.cjs");
    };
    if !supported(&probe.tool, "2.3.0", "3.0.0") || !supported(&probe.rspack, "2.3.0", "3.0.0") {
        bail!(
            "frontend discovery requires Rspack and the selected tool at stable 2.3.0 or later \
             within 2.x"
        );
    }
    if !supported(&probe.node, "24.0.0", "1000000.0.0") {
        bail!("frontend discovery requires Node.js 24 or later");
    }
    let r = &probe.result;
    if r.inputs.is_empty() {
        bail!(
            "frontend integration must declare every cache-selection input, including absent \
             configuration candidates"
        );
    }
    let mut input_paths = vec![integration.clone()];
    input_paths.extend(probe.tool_inputs.iter().cloned());
    for path in &r.inputs {
        if !paths::is_abs(path) {
            bail!("frontend configuration input paths must be absolute");
        }
        input_paths.push(path.clone());
    }
    out.inputs = repository::capture_file_stamps(&input_paths)?;
    // The first pass obtains the trusted resolver's dependency inventory. Run
    // it again between stamps so changes during discovery also fail closed.
    let checked = req.runner.output(ctx, &req.cwd, &req.env, &probe_argv)?;
    match (compact(&b), compact(&checked)) {
        (Some(before), Some(after)) if before == after => {}
        _ => bail!("frontend configuration changed during discovery; repeat discovery"),
    }
    repository::verify_file_stamps(&out.inputs)?;
    if r.schema != 1
        || r.preset != out.preset.as_str()
        || r.caches.is_empty()
        || r.outputs.is_empty()
        || r.settings.is_empty()
    {
        bail!(
            "frontend integration must verify all persistent caches, final outputs, and resolved \
             build settings"
        );
    }
    let mut protected = req.protected.clone();
    // Dependency trees and package-manager download stores are never caches.
    let mut p = req.cwd.clone();
    loop {
        for name in ["node_modules", ".pnpm-store", ".yarn"] {
            protected.push(paths::join2(&p, name));
        }
        let parent = paths::dir(&p);
        if parent == p {
            break;
        }
        p = parent;
    }
    for key in ["npm_config_cache", "PNPM_HOME", "YARN_CACHE_FOLDER"] {
        let v = env_value(&req.env, key);
        if !v.is_empty() {
            protected.push(if paths::is_abs(v) {
                v.to_string()
            } else {
                paths::join2(&req.cwd, v)
            });
        }
    }
    let home = env_value(&req.env, "HOME");
    if !home.is_empty() {
        protected.push(paths::join2(home, ".npm"));
        protected.push(paths::join(&[home, ".cache", "yarn"]));
    }
    for p in &r.outputs {
        if !paths::is_abs(p) {
            bail!("frontend output paths must be absolute");
        }
        protected.push(p.clone());
    }
    for cache in &r.caches {
        if cache.kind != "persistent" || !paths::is_abs(&cache.path) {
            bail!("frontend integration must report absolute persistent-cache paths only");
        }
        let p = out
            .repository
            .validate_cache(ctx, &cache.path, &protected)?;
        out.cache_paths.push(CachePath {
            tool_path: cache.path.clone(),
            canonical: p.clone(),
        });
        out.roots.push(Root {
            path: p,
            kinds: vec![RootKind::Persistent],
        });
    }
    out.compatibility.tool_version.clone_from(&probe.tool);
    out.compatibility.node_version.clone_from(&probe.node);
    out.compatibility.compiler_version.clone_from(&probe.rspack);
    Ok(sorted_value(Value::Object(r.settings.clone())))
}
