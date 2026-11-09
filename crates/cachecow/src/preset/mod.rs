// SPDX-License-Identifier: Apache-2.0
//! Tool-owned cache discovery without modifying freshness metadata (Go
//! `internal/preset`). A successful discovery is not proof that an existing
//! cache was built with these settings: compare its recorded compatibility.

use anyhow::{Result, anyhow, bail};
use serde::{Deserialize, Deserializer, Serialize};
use serde_json::Value;

use crate::{
    Context, paths,
    repository::{self, FileStamp, Info},
};

mod cargo;
mod frontend;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Hash, Default, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum Id {
    #[default]
    Cargo,
    Rspack,
    Rsbuild,
}

impl Id {
    pub const ALL: [Self; 3] = [Self::Cargo, Self::Rspack, Self::Rsbuild];

    pub const fn as_str(self) -> &'static str {
        match self {
            Self::Cargo => "cargo",
            Self::Rspack => "rspack",
            Self::Rsbuild => "rsbuild",
        }
    }

    pub fn parse(value: &str) -> Option<Self> {
        Self::ALL.into_iter().find(|id| id.as_str() == value)
    }
}

impl std::fmt::Display for Id {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.write_str(self.as_str())
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "lowercase")]
pub enum RootKind {
    Target,
    Build,
    Persistent,
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Root {
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "Kinds")]
    pub kinds: Vec<RootKind>,
}

/// Compatibility evidence. Field names match the Go JSON encoding.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Compatibility {
    #[serde(rename = "Schema")]
    pub schema: i64,
    #[serde(rename = "Preset")]
    pub preset: Id,
    #[serde(rename = "ToolVersion")]
    pub tool_version: String,
    #[serde(rename = "CompilerVersion")]
    pub compiler_version: String,
    #[serde(rename = "NodeVersion")]
    pub node_version: String,
    #[serde(rename = "Platform")]
    pub platform: String,
    #[serde(rename = "Architecture")]
    pub architecture: String,
    #[serde(rename = "SettingsDigest")]
    pub settings_digest: String,
}

impl Compatibility {
    pub fn verified(&self) -> bool {
        if self.schema != 1
            || self.platform.is_empty()
            || self.architecture.is_empty()
            || self.settings_digest.len() != 64
            || hex::decode(&self.settings_digest).is_err()
        {
            return false;
        }
        match self.preset {
            Id::Cargo => {
                cargo::supported_toolchain(&self.tool_version) && self.compiler_version.len() == 64
            }
            Id::Rspack | Id::Rsbuild => {
                supported(&self.tool_version, "2.3.0", "3.0.0")
                    && supported(&self.compiler_version, "2.3.0", "3.0.0")
                    && supported(&self.node_version, "24.0.0", "1000000.0.0")
            }
        }
    }

    pub fn compatible(&self, other: &Self) -> bool {
        self.verified() && other.verified() && self == other
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CachePath {
    #[serde(rename = "ToolPath")]
    pub tool_path: String,
    #[serde(rename = "Canonical")]
    pub canonical: String,
}

/// Verified Cargo wrapper selected during discovery.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, Deserialize)]
#[serde(tag = "kind", rename_all = "lowercase", deny_unknown_fields)]
pub enum CargoWrapper {
    Sccache { executable: String },
}

impl CargoWrapper {
    pub(crate) fn environment(&self, env: &[String]) -> Vec<String> {
        match self {
            Self::Sccache { executable } => set_env(env, "RUSTC_WRAPPER", executable),
        }
    }
}

/// Discovery result. Field names match the Go JSON encoding.
#[derive(Debug, Clone, Default, PartialEq, Eq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct PresetResult {
    #[serde(
        rename = "CargoWrapper",
        default,
        skip_serializing_if = "Option::is_none"
    )]
    pub cargo_wrapper: Option<CargoWrapper>,
    #[serde(rename = "CachePaths")]
    pub cache_paths: Vec<CachePath>,
    #[serde(rename = "Inputs")]
    pub inputs: Vec<FileStamp>,
    #[serde(rename = "Preset")]
    pub preset: Id,
    #[serde(rename = "Repository")]
    pub repository: Info,
    #[serde(rename = "Roots")]
    pub roots: Vec<Root>,
    #[serde(rename = "Compatibility")]
    pub compatibility: Compatibility,
}

/// Process adapter so fixtures can replace tool execution.
pub trait Runner: Sync {
    fn output(&self, ctx: &Context, cwd: &str, env: &[String], argv: &[String]) -> Result<Vec<u8>>;
}

/// Runs discovery commands, resolving PATH from the supplied environment.
pub struct ExecRunner;

impl Runner for ExecRunner {
    fn output(&self, ctx: &Context, cwd: &str, env: &[String], argv: &[String]) -> Result<Vec<u8>> {
        let Some(argv0) = argv.first() else {
            bail!("discovery command is empty");
        };
        let executable = resolve_executable(cwd, env, argv0)?;
        let mut cmd = std::process::Command::new(&executable);
        cmd.args(&argv[1..]).current_dir(cwd).env_clear();
        for entry in env {
            if let Some((k, v)) = entry.split_once('=')
                && !k.is_empty()
            {
                cmd.env(k, v);
            }
        }
        let failure = || {
            anyhow!(
                "tool discovery failed; verify the selected tool and project configuration \
                 directly"
            )
        };
        let output = repository::run_output(ctx, cmd).map_err(|_| failure())?;
        if !output.status.success() {
            tracing::debug!(executable = %executable, status = ?output.status, "discovery command failed");
            return Err(failure());
        }
        Ok(output.stdout)
    }
}

/// Resolve once and retain the spelling used to invoke a proxy or symlink.
fn resolve_executable(cwd: &str, env: &[String], name: &str) -> Result<String> {
    // Resolve PATH from the supplied child environment, not this process.
    let executable = if name.contains(['/', '\\']) {
        if paths::is_abs(name) {
            name.to_string()
        } else {
            paths::join2(cwd, name)
        }
    } else {
        let mut found = None;
        for dir in paths::split_list(env_value(env, "PATH")) {
            if dir.is_empty() {
                continue;
            }
            let dir = if paths::is_abs(&dir) {
                dir
            } else {
                paths::join2(cwd, &dir)
            };
            let mut p = paths::join2(&dir, name);
            if cfg!(windows) && !p.to_lowercase().ends_with(".exe") {
                p.push_str(".exe");
            }
            if std::fs::metadata(&p).is_ok_and(|m| !m.is_dir() && is_executable(&m)) {
                found = Some(p);
                break;
            }
        }
        found.ok_or_else(|| {
            anyhow!(
                "discovery executable was not found in the child PATH; install the selected tool"
            )
        })?
    };
    Ok(executable)
}

#[cfg(unix)]
fn is_executable(m: &std::fs::Metadata) -> bool {
    use std::os::unix::fs::PermissionsExt;
    m.permissions().mode() & 0o111 != 0
}

#[cfg(not(unix))]
fn is_executable(_m: &std::fs::Metadata) -> bool {
    true
}

pub struct Request<'a> {
    pub cwd: String,
    /// Exact command including the executable; never persisted.
    pub argv: Vec<String>,
    /// Complete child environment. `None` inherits the current environment.
    pub env: Option<Vec<String>>,
    /// `None` selects from the executable basename.
    pub preset: Option<Id>,
    /// Settings storage, source caches and other forbidden paths.
    pub protected: Vec<String>,
    /// CacheCow shim directory, or empty when none applies. Its `PATH` entry
    /// is left out of the settings digest, because tool lookup already
    /// excludes CacheCow shims; activating the shims must not make a cache
    /// that an explicit `ccow run` built look incompatible.
    pub shim_dir: String,
    pub runner: Option<&'a dyn Runner>,
}

/// A request with defaults applied.
pub(crate) struct Resolved<'a> {
    pub cwd: String,
    pub argv: Vec<String>,
    pub env: Vec<String>,
    pub protected: Vec<String>,
    pub runner: &'a dyn Runner,
}

/// Windows stores and compares environment variable names case-insensitively
/// (`Path` and `PATH` are the same variable); Unix keeps them case-sensitive.
const ENV_NAMES_IGNORE_CASE: bool = cfg!(windows);

fn env_name_matches(name: &str, key: &str, ignore_case: bool) -> bool {
    if ignore_case {
        name.eq_ignore_ascii_case(key)
    } else {
        name == key
    }
}

/// Last value of `key` in `KEY=value` entries, like the child would see.
/// Names match case-insensitively on Windows and exactly elsewhere.
pub(crate) fn env_value<'a>(env: &'a [String], key: &str) -> &'a str {
    env_value_with(env, key, ENV_NAMES_IGNORE_CASE)
}

fn env_value_with<'a>(env: &'a [String], key: &str, ignore_case: bool) -> &'a str {
    for entry in env.iter().rev() {
        if let Some((k, v)) = entry.split_once('=')
            && env_name_matches(k, key, ignore_case)
        {
            return v;
        }
    }
    ""
}

/// Replaces every entry for `key` with a single `key=value` entry. Existing
/// entries whose names differ only by case are replaced on Windows.
pub(crate) fn set_env(env: &[String], key: &str, value: &str) -> Vec<String> {
    set_env_with(env, key, value, ENV_NAMES_IGNORE_CASE)
}

fn set_env_with(env: &[String], key: &str, value: &str, ignore_case: bool) -> Vec<String> {
    let mut out: Vec<String> = env
        .iter()
        .filter(|entry| {
            entry
                .split_once('=')
                .is_none_or(|(name, _)| !env_name_matches(name, key, ignore_case))
        })
        .cloned()
        .collect();
    out.push(format!("{key}={value}"));
    out
}

/// Go `golang.org/x/mod/semver` validity for `"v" + v`, returning the three
/// numeric components (shorthand `1` and `1.2` pad with zeros). Prerelease
/// and build suffixes are rejected because [`supported`] forbids them.
fn parse_version(v: &str) -> Option<[&str; 3]> {
    let num = |s: &str| {
        !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit()) && (s == "0" || !s.starts_with('0'))
    };
    let parts: Vec<&str> = v.split('.').collect();
    if parts.is_empty() || parts.len() > 3 || !parts.iter().all(|p| num(p)) {
        return None;
    }
    Some([
        parts[0],
        parts.get(1).copied().unwrap_or("0"),
        parts.get(2).copied().unwrap_or("0"),
    ])
}

fn compare_number(a: &str, b: &str) -> std::cmp::Ordering {
    a.len().cmp(&b.len()).then_with(|| a.cmp(b))
}

fn compare_version(a: [&str; 3], b: [&str; 3]) -> std::cmp::Ordering {
    for i in 0..3 {
        let c = compare_number(a[i], b[i]);
        if c.is_ne() {
            return c;
        }
    }
    std::cmp::Ordering::Equal
}

/// Stable release `v` within `[min, max)`.
pub(crate) fn supported(v: &str, min: &str, max: &str) -> bool {
    if v.contains(['-', '+']) {
        return false;
    }
    let (Some(v), Some(lo), Some(hi)) = (parse_version(v), parse_version(min), parse_version(max))
    else {
        return false;
    };
    compare_version(v, lo).is_ge() && compare_version(v, hi).is_lt()
}

/// SHA-256 of the JSON encoding of `value` (Go `digest`).
pub(crate) fn digest<T: Serialize + ?Sized>(value: &T) -> String {
    crate::jsonx::digest(value)
}

/// Rebuilds `value` with object keys inserted in sorted order, so digests do
/// not depend on serde_json's map implementation (Go marshals maps sorted).
pub(crate) fn sorted_value(value: Value) -> Value {
    match value {
        Value::Object(map) => {
            let mut entries: Vec<(String, Value)> = map.into_iter().collect();
            entries.sort_by(|a, b| a.0.cmp(&b.0));
            Value::Object(
                entries
                    .into_iter()
                    .map(|(k, v)| (k, sorted_value(v)))
                    .collect(),
            )
        }
        Value::Array(items) => Value::Array(items.into_iter().map(sorted_value).collect()),
        other => other,
    }
}

/// Deserializes JSON `null` (or an absent field) as the default value, like
/// Go's `encoding/json`.
pub(crate) fn null_default<'de, D, T>(d: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Ok(Option::<T>::deserialize(d)?.unwrap_or_default())
}

/// Returns `env` without the CacheCow shim directory in its `PATH` entries.
///
/// The settings digest hashes the whole environment, and the shim directory
/// only appears in `PATH` after the user activates the shims. Tool lookup
/// excludes CacheCow shims, so the directory never selects a different tool.
/// Leaving it in the digest would make a cache built by an explicit
/// `ccow run` incompatible with the same command run through an activated
/// shim, and the reverse. Remaining `PATH` entries keep their order and any
/// empty entries; an environment without the directory is returned unchanged.
fn without_shim_dir(env: &[String], shim_dir: &str, ignore_case: bool) -> Vec<String> {
    if shim_dir.is_empty() {
        return env.to_vec();
    }
    let shim_real = paths::eval_symlinks(shim_dir).ok();
    let is_shim = |dir: &str| -> bool {
        if dir.is_empty() {
            return false;
        }
        let same = if ignore_case {
            dir.eq_ignore_ascii_case(shim_dir)
        } else {
            dir == shim_dir
        };
        same || (paths::is_abs(dir)
            && shim_real.is_some()
            && paths::eval_symlinks(dir).ok() == shim_real)
    };
    env.iter()
        .map(|entry| {
            let Some((name, value)) = entry.split_once('=') else {
                return entry.clone();
            };
            if !env_name_matches(name, "PATH", ignore_case) {
                return entry.clone();
            }
            let dirs = paths::split_list_raw(value);
            if !dirs.iter().any(|(_, dir)| is_shim(dir)) {
                return entry.clone();
            }
            tracing::debug!("ignoring the CacheCow shim directory in PATH for the settings digest");
            // Keep each retained entry's original spelling, including any
            // Windows quotes, so removing the shim restores the original value.
            let kept: Vec<String> = dirs
                .into_iter()
                .filter(|(_, dir)| !is_shim(dir))
                .map(|(raw, _)| raw)
                .collect();
            format!("{name}={}", kept.join(&paths::LIST_SEPARATOR.to_string()))
        })
        .collect()
}

fn current_environment() -> Vec<String> {
    std::env::vars_os()
        .map(|(k, v)| format!("{}={}", k.to_string_lossy(), v.to_string_lossy()))
        .collect()
}

#[derive(Serialize)]
struct SettingsInput<'a> {
    #[serde(rename = "Args")]
    args: Vec<String>,
    #[serde(rename = "Env")]
    env: Vec<String>,
    #[serde(rename = "Roots")]
    roots: Vec<String>,
    #[serde(rename = "CWD")]
    cwd: String,
    #[serde(rename = "Settings")]
    settings: &'a Value,
}

pub fn discover(ctx: &Context, request: Request<'_>) -> Result<PresetResult> {
    let mut out = PresetResult::default();
    if request.argv.is_empty() {
        bail!("supply the exact build command for preset discovery");
    }
    let runner: &dyn Runner = request.runner.unwrap_or(&ExecRunner);
    let cwd = repository::canonical(&request.cwd)?;
    let req = Resolved {
        cwd,
        argv: request.argv,
        env: request.env.unwrap_or_else(current_environment),
        protected: request.protected,
        runner,
    };
    out.repository = repository::discover(ctx, &req.cwd)?;
    out.preset = match request.preset {
        Some(id) => id,
        None => {
            let base = paths::base(&req.argv[0]).to_lowercase();
            let name = base.strip_suffix(".exe").unwrap_or(&base);
            Id::parse(name).ok_or_else(|| {
                anyhow!("cannot identify the build tool; select --preset cargo, rspack, or rsbuild")
            })?
        }
    };
    out.compatibility = Compatibility {
        schema: 1,
        preset: out.preset,
        platform: platform().to_string(),
        architecture: architecture().to_string(),
        ..Compatibility::default()
    };
    let settings = match out.preset {
        Id::Cargo => cargo::discover(ctx, &req, &mut out)?,
        Id::Rspack | Id::Rsbuild => frontend::discover(ctx, &req, &mut out)?,
    };
    repository::verify_file_stamps(&out.inputs)?;
    if out.roots.is_empty() {
        bail!("no verified cache roots; configure a supported persistent cache");
    }
    let mut protected = req.protected.clone();
    if let Some(CargoWrapper::Sccache { executable }) = &out.cargo_wrapper {
        // Preparation must not replace the wrapper after its identity was
        // verified, including an executable reached through a symlink.
        protected.push(executable.clone());
    }
    for i in 0..out.roots.len() {
        let p = out
            .repository
            .validate_cache(ctx, &out.roots[i].path, &protected)?;
        out.cache_paths.push(CachePath {
            tool_path: out.roots[i].path.clone(),
            canonical: p.clone(),
        });
        out.roots[i].path = p;
    }
    // Merge equal roots but reject partial overlap: lifecycle operations must
    // never move a parent out from under another managed cache.
    let mut roots: Vec<Root> = Vec::new();
    for r in std::mem::take(&mut out.roots) {
        let mut merged = false;
        for existing in &mut roots {
            if r.path == existing.path {
                for kind in &r.kinds {
                    if !existing.kinds.contains(kind) {
                        existing.kinds.push(*kind);
                    }
                }
                merged = true;
                break;
            }
            if repository::overlaps(&r.path, &existing.path) {
                bail!("cache roots overlap; configure equal or disjoint cache directories");
            }
        }
        if !merged {
            roots.push(r);
        }
    }
    out.roots = roots;
    // Only digests leave discovery. Conservative environment hashing can reject
    // reusable caches, but never assumes an unknown setting is irrelevant.
    let root = out.repository.root.clone();
    let normalize = |s: &str| s.replace(root.as_str(), "${WORKTREE}");
    let wrapper_env = out
        .cargo_wrapper
        .as_ref()
        .map(|wrapper| wrapper.environment(&req.env));
    let digest_env = without_shim_dir(
        wrapper_env.as_deref().unwrap_or(&req.env),
        &request.shim_dir,
        ENV_NAMES_IGNORE_CASE,
    );
    let mut env: Vec<String> = digest_env
        .iter()
        .filter(|entry| {
            let key = entry.split_once('=').map_or(entry.as_str(), |(k, _)| k);
            !matches!(key, "CCOW_SESSION" | "PWD" | "OLDPWD" | "SHLVL" | "_")
                // The supervisor replaces this transport address with a private
                // per-session socket. It does not select compiler settings.
                && !(out.cargo_wrapper.is_some() && key == "SCCACHE_SERVER_UDS")
        })
        .map(|entry| normalize(entry))
        .collect();
    env.sort();
    let input = SettingsInput {
        args: req.argv[1..].iter().map(|a| normalize(a)).collect(),
        env,
        roots: out.roots.iter().map(|r| normalize(&r.path)).collect(),
        cwd: normalize(&req.cwd),
        settings: &settings,
    };
    out.compatibility.settings_digest = digest(&input);
    tracing::debug!(
        preset = %out.preset,
        cache_roots = out.roots.len(),
        tool_version = %out.compatibility.tool_version,
        "cache discovery verified"
    );
    verify_cache_paths(&out)?;
    Ok(out)
}

/// Binds tool-visible aliases to the leased resource names. Repository
/// snapshots cannot observe aliases outside a worktree.
pub fn verify_cache_paths(result: &PresetResult) -> Result<()> {
    let mut covered = std::collections::HashSet::new();
    for claim in &result.cache_paths {
        let current = repository::canonical(&claim.tool_path)?;
        if current != claim.canonical {
            bail!("cache path alias changed; repeat discovery");
        }
        covered.insert(current);
    }
    for root in &result.roots {
        if !covered.contains(&root.path) {
            bail!("cache path alias evidence is missing; repeat discovery");
        }
    }
    Ok(())
}

/// Translates the current worktree root into the source worktree root in
/// environment entries (`KEY=value`) and arguments before source discovery
/// (#4578), so equivalent worktrees produce equal settings digests.
///
/// The rule is purely lexical and case-sensitive, and it is the exact inverse
/// of the digest normalization: every occurrence of `from_root` becomes
/// `to_root`, just as discovery replaces every occurrence of a worktree's own
/// root with `${WORKTREE}`. A value that differs only by the worktree root, at
/// any position (for example `NODE_OPTIONS=--require <root>/.pnp.cjs`),
/// therefore digests identically; every other difference stays visible. Empty
/// or identical roots return the values unchanged.
pub fn translate_worktree_root(values: &[String], from_root: &str, to_root: &str) -> Vec<String> {
    if from_root.is_empty() || from_root == to_root {
        return values.to_vec();
    }
    values
        .iter()
        .map(|value| value.replace(from_root, to_root))
        .collect()
}

/// Platform identifier recorded in compatibility (Go `GOOS` values).
pub const fn platform() -> &'static str {
    if cfg!(target_os = "macos") {
        "darwin"
    } else if cfg!(target_os = "windows") {
        "windows"
    } else if cfg!(target_os = "linux") {
        "linux"
    } else {
        std::env::consts::OS
    }
}

/// Architecture identifier recorded in compatibility (Go `GOARCH` values).
pub const fn architecture() -> &'static str {
    if cfg!(target_arch = "aarch64") {
        "arm64"
    } else if cfg!(target_arch = "x86_64") {
        "amd64"
    } else {
        std::env::consts::ARCH
    }
}

#[cfg(test)]
mod env_tests;
#[cfg(test)]
mod tests;
