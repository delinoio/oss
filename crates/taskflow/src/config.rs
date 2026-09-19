use std::{collections::BTreeMap, path::Path};

use anyhow::{bail, ensure, Context, Result};
use schemars::JsonSchema;
use serde::{Deserialize, Serialize};

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct Config {
    #[schemars(range(min = 1, max = 1))]
    pub version: u32,
    pub project: String,
    #[serde(default)]
    pub workspace: WorkspaceConfig,
    #[serde(default)]
    pub tasks: BTreeMap<String, Task>,
    #[serde(default)]
    pub start: BTreeMap<String, Vec<String>>,
    #[serde(default = "yes")]
    pub dotenv: bool,
    #[serde(default)]
    pub remote: Option<RemoteConfig>,
    #[serde(default)]
    pub ci: Option<CiConfig>,
}

#[derive(Debug, Clone, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct WorkspaceConfig {
    #[serde(default)]
    pub manifests: Vec<String>,
    #[serde(default)]
    pub cargo_features: Vec<String>,
    #[serde(default)]
    pub cargo_no_default_features: bool,
    #[serde(default)]
    pub cargo_target: Option<String>,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct Task {
    pub command: Command,
    #[serde(default)]
    pub shell: Option<Vec<String>>,
    #[serde(default)]
    pub depends_on: Vec<Dependency>,
    /// None distinguishes an undeclared input/output contract from an empty
    /// one.
    #[serde(default)]
    pub input: Option<Vec<Input>>,
    #[serde(default)]
    pub output: Option<Vec<String>>,
    #[serde(default)]
    pub cache: bool,
    #[serde(default)]
    pub env: BTreeMap<String, String>,
    #[serde(default)]
    pub env_inputs: Vec<String>,
    #[serde(default)]
    pub secrets: Vec<String>,
    #[serde(default)]
    pub tools: BTreeMap<String, Command>,
    #[serde(default)]
    pub dotenv: Option<bool>,
    #[serde(default)]
    pub service: bool,
    #[serde(default)]
    pub with: Vec<String>,
    #[serde(default)]
    pub watch: Option<Watch>,
    #[serde(default)]
    pub schedule: Option<Schedule>,
    #[serde(default)]
    pub overlap: Option<Overlap>,
    #[serde(default)]
    pub readiness: Option<Readiness>,
    #[serde(default)]
    pub platform: Platform,
    #[serde(default)]
    pub resources: Vec<String>,
    #[serde(default)]
    pub effect: Effect,
    #[serde(default)]
    pub shard: Option<ShardConfig>,
    #[serde(default)]
    pub timeout: Option<String>,
    #[serde(default)]
    pub install: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(untagged)]
pub enum Command {
    Argv(Vec<String>),
    Shell(String),
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(untagged)]
pub enum Input {
    Pattern(String),
    Auto(AutoInput),
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct AutoInput {
    pub auto: bool,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(untagged)]
pub enum Dependency {
    Reference(String),
    Selector(Selector),
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct Selector {
    pub task: String,
    #[serde(default)]
    pub from: Option<Kinds>,
    #[serde(default)]
    pub wait_for: WaitFor,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(untagged)]
pub enum Kinds {
    One(DependencyKind),
    Many(Vec<DependencyKind>),
}
impl Kinds {
    pub fn contains(&self, kind: DependencyKind) -> bool {
        match self {
            Self::One(value) => *value == kind,
            Self::Many(values) => values.contains(&kind),
        }
    }
}

#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "camelCase")]
pub enum DependencyKind {
    Dependencies,
    DevDependencies,
    BuildDependencies,
    PeerDependencies,
    OptionalDependencies,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum WaitFor {
    #[default]
    Success,
    Ready,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum Overlap {
    Queue,
    Skip,
    Restart,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum Effect {
    #[default]
    Local,
    External,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Watch {
    #[serde(default = "yes")]
    pub initial: bool,
    #[serde(default = "debounce")]
    pub debounce: String,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Schedule {
    pub every: Option<String>,
    pub cron: Option<String>,
    #[serde(default = "utc")]
    pub timezone: String,
    #[serde(default)]
    pub initial: bool,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(tag = "type", rename_all = "kebab-case", deny_unknown_fields)]
pub enum Readiness {
    Tcp { address: String, timeout: String },
    Http { url: String, timeout: String },
    Command { command: Command, timeout: String },
}
#[derive(Debug, Clone, Default, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields)]
pub struct Platform {
    pub os: Option<Os>,
    pub arch: Option<Arch>,
    #[serde(default)]
    pub executor: Executor,
    pub image: Option<String>,
    #[serde(default)]
    pub ports: Vec<String>,
}
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "lowercase")]
#[derive(clap::ValueEnum)]
pub enum Os {
    Macos,
    Linux,
    Windows,
}
#[derive(
    Debug, Clone, Copy, PartialEq, Eq, PartialOrd, Ord, Serialize, Deserialize, JsonSchema,
)]
#[serde(rename_all = "lowercase")]
#[derive(clap::ValueEnum)]
pub enum Arch {
    X64,
    Arm64,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "lowercase")]
pub enum Executor {
    #[default]
    Host,
    Docker,
}
impl Platform {
    pub fn resolved(&self) -> (Os, Arch) {
        (
            self.os.unwrap_or_else(host_os),
            self.arch.unwrap_or_else(host_arch),
        )
    }

    pub fn key(&self) -> String {
        let (os, arch) = self.resolved();
        format!("{}-{}", enum_name(&os), enum_name(&arch))
    }
}
pub fn enum_name(value: &impl Serialize) -> String {
    serde_json::to_value(value)
        .expect("enum serialization")
        .as_str()
        .expect("string enum")
        .to_owned()
}
pub fn host_os() -> Os {
    if cfg!(target_os = "windows") {
        Os::Windows
    } else if cfg!(target_os = "macos") {
        Os::Macos
    } else {
        Os::Linux
    }
}
pub fn host_arch() -> Arch {
    if cfg!(target_arch = "aarch64") {
        Arch::Arm64
    } else {
        Arch::X64
    }
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct RemoteConfig {
    pub endpoint: String,
    pub bucket: String,
    pub namespace: String,
    #[serde(default = "region")]
    pub region: String,
    pub access_key_env: String,
    pub secret_key_env: String,
    pub session_token_env: Option<String>,
    #[serde(default)]
    pub mode: RemoteMode,
}
#[derive(Debug, Clone, Copy, Default, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum RemoteMode {
    #[default]
    ReadOnly,
    ReadWrite,
    Off,
}
#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct ShardConfig {
    pub adapter: ShardAdapter,
    #[schemars(range(min = 1, max = 256))]
    pub count: usize,
    pub list: Option<Command>,
    pub run: Option<Command>,
}
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize, Deserialize, JsonSchema)]
#[serde(rename_all = "kebab-case")]
pub enum ShardAdapter {
    Go,
    Libtest,
    Vitest,
    Jest,
    Generic,
}

#[derive(Debug, Clone, Serialize, Deserialize, JsonSchema)]
#[serde(deny_unknown_fields, rename_all = "camelCase")]
pub struct CiConfig {
    /// Immutable source SHA from which to build the TaskFlow CLI.
    pub revision: String,
    pub rust: String,
    pub node: Option<String>,
    pub pnpm: Option<String>,
    pub go: Option<String>,
    pub runners: BTreeMap<String, String>,
}

fn yes() -> bool {
    true
}
fn debounce() -> String {
    "200ms".into()
}
fn utc() -> String {
    "UTC".into()
}
fn region() -> String {
    "auto".into()
}

pub fn duration(value: &str) -> Result<std::time::Duration> {
    let split = value
        .find(|c: char| !c.is_ascii_digit())
        .context("duration requires a unit")?;
    let n: u64 = value[..split].parse().context("invalid duration")?;
    let factor = match &value[split..] {
        "ms" => 1,
        "s" => 1000,
        "m" => 60_000,
        "h" => 3_600_000,
        "d" => 86_400_000,
        _ => bail!("duration unit must be ms, s, m, h, or d"),
    };
    let ms = n.checked_mul(factor).context("duration overflow")?;
    ensure!(ms > 0, "duration must be positive");
    Ok(std::time::Duration::from_millis(ms))
}

pub fn load(path: &Path) -> Result<Config> {
    let bytes =
        std::fs::read(path).with_context(|| format!("read configuration {}", path.display()))?;
    // Value's YAML mapping visitor rejects duplicate map keys, including maps
    // such as tasks/env that a normal BTreeMap visitor would overwrite silently.
    let _: serde_yaml::Value = serde_yaml::from_slice(&bytes)
        .with_context(|| format!("invalid YAML mapping {}", path.display()))?;
    let config: Config = serde_yaml::from_slice(&bytes)
        .with_context(|| format!("invalid configuration {}", path.display()))?;
    config.validate()?;
    Ok(config)
}
pub fn identifier(value: &str) -> bool {
    !value.is_empty()
        && value
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || "-_.".contains(c))
}
impl Config {
    pub fn validate(&self) -> Result<()> {
        ensure!(
            self.version == 1,
            "unsupported taskflow version {}",
            self.version
        );
        ensure!(identifier(&self.project), "invalid project ID");
        for (name, task) in &self.tasks {
            ensure!(identifier(name), "invalid task ID {name}");
            task.validate()
                .with_context(|| format!("{}#{name}", self.project))?;
        }
        Ok(())
    }
}
impl Task {
    pub fn validate(&self) -> Result<()> {
        validate_command(&self.command)?;
        if let Some(shell) = &self.shell {
            ensure!(
                !shell.is_empty() && !shell[0].is_empty(),
                "shell must contain an executable"
            );
        }
        if self.cache {
            ensure!(
                !self.service
                    && self.schedule.is_none()
                    && self.effect == Effect::Local
                    && self.secrets.is_empty(),
                "services, schedules, effects, and secrets cannot be cached"
            );
            ensure!(
                self.input.is_some() && self.output.is_some() && !self.tools.is_empty(),
                "cache requires explicit input, output, and tool identity commands"
            );
        }
        ensure!(
            !(self.service
                && (self.watch.is_some() || self.schedule.is_some() || self.shard.is_some())),
            "services cannot have watch, schedule, or shard"
        );
        ensure!(
            self.service || self.readiness.is_none(),
            "readiness requires service: true"
        );
        if let Some(readiness) = &self.readiness {
            let timeout = match readiness {
                Readiness::Command { command, timeout } => {
                    validate_command(command)?;
                    timeout
                }
                Readiness::Tcp { timeout, .. } | Readiness::Http { timeout, .. } => timeout,
            };
            duration(timeout).context("invalid readiness timeout")?;
        }
        if let Some(watch) = &self.watch {
            duration(&watch.debounce)?;
        }
        if let Some(timeout) = &self.timeout {
            duration(timeout)?;
        }
        if let Some(schedule) = &self.schedule {
            ensure!(
                schedule.every.is_some() ^ schedule.cron.is_some(),
                "schedule requires exactly one of every or cron"
            );
            if let Some(every) = &schedule.every {
                duration(every)?;
            }
            if let Some(cron) = &schedule.cron {
                crate::schedule::parse_cron(cron)?;
            }
            schedule
                .timezone
                .parse::<chrono_tz::Tz>()
                .context("invalid IANA timezone")?;
        }
        if self.platform.executor == Executor::Docker {
            ensure!(
                self.platform.os.is_none_or(|os| os == Os::Linux),
                "Docker requires platform.os: linux"
            );
            let image = self
                .platform
                .image
                .as_deref()
                .context("Docker requires image")?;
            ensure!(
                image.contains("@sha256:")
                    && image
                        .rsplit(':')
                        .next()
                        .is_some_and(|v| v.len() == 64 && v.bytes().all(|b| b.is_ascii_hexdigit())),
                "Docker image must use an immutable SHA-256 digest"
            );
        }
        if let Some(shard) = &self.shard {
            ensure!(
                (1..=256).contains(&shard.count),
                "shard count must be 1..256"
            );
            ensure!(
                self.output.as_ref().is_none_or(|o| o.is_empty()),
                "sharded tests cannot own shared outputs"
            );
            if shard.adapter == ShardAdapter::Generic {
                ensure!(
                    shard.list.is_some() && shard.run.is_some(),
                    "generic sharding requires list and run commands"
                );
            }
        }
        for input in self.input.iter().flatten() {
            if let Input::Pattern(pattern) = input {
                let pattern = pattern.strip_prefix('!').unwrap_or(pattern);
                globset::Glob::new(pattern).context("invalid input glob")?;
            }
        }
        for output in self.output.iter().flatten() {
            ensure!(
                !output.is_empty() && !output.starts_with('!'),
                "invalid output pattern"
            );
            ensure!(
                !Path::new(output).is_absolute()
                    && !output
                        .split(['/', '\\'])
                        .any(|v| v == ".." || v == ".taskflow" || v == ".git"),
                "output must stay inside the project"
            );
            globset::Glob::new(output).context("invalid output glob")?;
        }
        for command in self.tools.values() {
            validate_command(command)?;
        }
        crate::shard::validate_task(self)?;
        Ok(())
    }

    pub fn overlap(&self) -> Overlap {
        self.overlap.unwrap_or(if self.schedule.is_some() {
            Overlap::Skip
        } else {
            Overlap::Queue
        })
    }
}
pub fn validate_command(command: &Command) -> Result<()> {
    match command {
        Command::Argv(args) => ensure!(
            !args.is_empty() && !args[0].is_empty() && args.iter().all(|a| !a.contains('\0')),
            "command argv must contain a nonempty executable and no NUL"
        ),
        Command::Shell(command) => ensure!(
            !command.trim().is_empty() && !command.contains('\0'),
            "command must be nonempty and NUL-free"
        ),
    }
    Ok(())
}
