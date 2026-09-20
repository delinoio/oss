use std::{
    collections::BTreeMap,
    path::{Path, PathBuf},
};

use serde::{Deserialize, Serialize};

use crate::{
    entries::{DEFAULT_MEMORY_BYTES, MAX_RECORDS},
    error::{Error, Result},
};

#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Config {
    pub schema_version: u32,
    #[serde(default)]
    pub commands: BTreeMap<String, Command>,
    #[serde(default)]
    pub exclusions: Vec<String>,
    #[serde(default)]
    pub policy: Policy,
    #[serde(default)]
    pub redaction: Redaction,
    #[serde(default)]
    pub limits: Limits,
}
impl Default for Config {
    fn default() -> Self {
        Self {
            schema_version: 1,
            commands: BTreeMap::new(),
            exclusions: vec![],
            policy: Policy::default(),
            redaction: Redaction::default(),
            limits: Limits::default(),
        }
    }
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Command {
    pub argv: Vec<String>,
    #[serde(default = "default_cwd")]
    pub cwd: PathBuf,
    #[serde(default)]
    pub inputs: Vec<String>,
    #[serde(default)]
    pub outputs: Vec<String>,
    #[serde(default)]
    pub prepare: Vec<Vec<String>>,
    #[serde(default)]
    pub env: Vec<String>,
    #[serde(default)]
    pub exclusions: Vec<String>,
    pub timeout_ms: Option<u64>,
}
fn default_cwd() -> PathBuf {
    PathBuf::from(".")
}
impl Command {
    pub fn direct(argv: Vec<String>) -> Self {
        Self {
            argv,
            cwd: default_cwd(),
            inputs: vec![],
            outputs: vec![],
            prepare: vec![],
            env: vec![],
            exclusions: vec![],
            timeout_ms: None,
        }
    }
}
#[derive(Debug, Clone, Default, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct Policy {
    pub allow_reads: Option<Vec<String>>,
    pub allow_writes: Option<Vec<String>>,
    pub deny_reads: Vec<String>,
    pub deny_writes: Vec<String>,
    pub require_inputs: bool,
    pub require_outputs: bool,
    pub fail_new_accesses: bool,
}
#[derive(Debug, Clone, Default, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct Redaction {
    pub patterns: Vec<String>,
    pub environment_names: Vec<String>,
    pub argument_indices: Vec<usize>,
}
#[derive(Debug, Clone, Serialize, Deserialize, schemars::JsonSchema)]
#[serde(default, deny_unknown_fields)]
pub struct Limits {
    pub memory_bytes: usize,
    pub total_bytes: u64,
    pub max_paths: usize,
}
impl Default for Limits {
    fn default() -> Self {
        Self {
            memory_bytes: DEFAULT_MEMORY_BYTES,
            total_bytes: 1024 * 1024 * 1024,
            max_paths: MAX_RECORDS,
        }
    }
}
impl Limits {
    pub fn validate(&self) -> Result<()> {
        if self.memory_bytes < 8192
            || self.memory_bytes > DEFAULT_MEMORY_BYTES * 4
            || self.total_bytes < 4096
            || self.total_bytes > 4 * 1024 * 1024 * 1024
            || self.max_paths == 0
            || self.max_paths > MAX_RECORDS
        {
            return Err(Error::input(
                "collection limits are outside supported bounds",
            ));
        }
        Ok(())
    }
}
pub fn load(path: Option<&Path>, root: &Path) -> Result<Config> {
    let explicit = path.is_some();
    let default = root.join("runlens.toml");
    let path = path.unwrap_or(&default);
    if !explicit && !path.exists() && path == default {
        return Ok(Config::default());
    }
    let metadata =
        std::fs::metadata(path).map_err(|_| Error::input("configuration cannot be read"))?;
    if !metadata.is_file() || metadata.len() > 1024 * 1024 {
        return Err(Error::input(
            "configuration must be a file no larger than 1 MiB",
        ));
    }
    let text = std::fs::read_to_string(path)
        .map_err(|_| Error::input("configuration must be readable UTF-8"))?;
    let config: Config = toml::from_str(&text).map_err(|_| {
        Error::input("invalid runlens.toml; check the schema v1 configuration guide")
    })?;
    if config.schema_version != 1 {
        return Err(Error::input("unsupported configuration schema version"));
    }
    config.limits.validate()?;
    for (name, command) in &config.commands {
        if name.is_empty()
            || name.len() > 256
            || command.argv.is_empty()
            || command.argv.len() > 1024
            || command.prepare.len() > 32
        {
            return Err(Error::input("invalid named command"));
        }
        for argv in std::iter::once(&command.argv).chain(command.prepare.iter()) {
            if argv.is_empty()
                || argv
                    .iter()
                    .any(|arg| arg.contains('\0') || arg.len() > 32768)
            {
                return Err(Error::input("invalid command argv"));
            }
        }
        if command.cwd.is_absolute()
            || command
                .cwd
                .components()
                .any(|c| matches!(c, std::path::Component::ParentDir))
        {
            return Err(Error::input("command cwd must stay inside the workspace"));
        }
        if command.timeout_ms == Some(0) {
            return Err(Error::input("timeout_ms must be positive"));
        }
        for name in command
            .env
            .iter()
            .chain(config.redaction.environment_names.iter())
        {
            if name.is_empty() || !name.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'_') {
                return Err(Error::input("environment selections contain names only"));
            }
        }
        patterns(&command.inputs)?;
        patterns(&command.outputs)?;
        patterns(&command.exclusions)?;
    }
    patterns(&config.exclusions)?;
    patterns(config.policy.allow_reads.as_deref().unwrap_or_default())?;
    patterns(config.policy.allow_writes.as_deref().unwrap_or_default())?;
    patterns(&config.policy.deny_reads)?;
    patterns(&config.policy.deny_writes)?;
    for pattern in &config.redaction.patterns {
        regex::Regex::new(pattern)
            .map_err(|_| Error::input("invalid redaction regular expression"))?;
    }
    Ok(config)
}
pub fn patterns(values: &[String]) -> Result<globset::GlobSet> {
    patterns_for_os(values, std::env::consts::OS)
}
pub fn patterns_for_os(values: &[String], os: &str) -> Result<globset::GlobSet> {
    let mut set = globset::GlobSetBuilder::new();
    for glob in declaration_globs(values, os)? {
        set.add(glob);
    }
    set.build()
        .map_err(|_| Error::input("invalid path patterns"))
}
pub(crate) fn declaration_globs(values: &[String], os: &str) -> Result<Vec<globset::Glob>> {
    let mut globs = Vec::new();
    if values.len() > 4096 {
        return Err(Error::input("too many path patterns"));
    }
    for value in values {
        if value.contains('\0')
            || value.contains('\\')
            || value.len() > 4096
            || value.split('/').any(|s| s == "..")
        {
            return Err(Error::input(
                "patterns use forward slashes and cannot contain parent traversal",
            ));
        }
        // A directory glob also covers the directory itself, with exactly the
        // same case semantics as its descendants.
        for pattern in std::iter::once(value.as_str()).chain(value.strip_suffix("/**")) {
            globs.push(
                globset::GlobBuilder::new(pattern)
                    .literal_separator(true)
                    .backslash_escape(false)
                    .case_insensitive(os == "windows")
                    .build()
                    .map_err(|_| Error::input("invalid path pattern"))?,
            );
        }
    }
    Ok(globs)
}
pub fn relative_pattern_path(path: &str) -> &str {
    if path == "${workspace}" {
        "."
    } else {
        path.strip_prefix("${workspace}/").unwrap_or(path)
    }
}
