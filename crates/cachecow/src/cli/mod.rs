// SPDX-License-Identifier: Apache-2.0
//! CacheCow's public command boundaries (Go `internal/cli`).

use std::{
    collections::BTreeMap,
    io::{BufRead, IsTerminal, Write},
};

use anyhow::{Result, anyhow};
use serde::Serialize;
use serde_json::json;

use crate::{
    Context, error,
    lease::LeaseError,
    paths, process, repository,
    session::{Plan, RecordHooks, error_text},
    shell, shim, signals, state,
};

mod direct;
mod managed;
#[cfg(test)]
mod tests;

/// Parsed CacheCow options. Never produced for shim invocations.
#[derive(Debug, Clone, Default)]
pub(crate) struct Options {
    pub preset: String,
    pub source: String,
    pub file: String,
    pub allow_copy: bool,
    pub replace: bool,
    pub json: bool,
    pub yes: bool,
    pub verbose: bool,
    pub help: bool,
    pub args: Vec<String>,
    pub command: Vec<String>,
}

/// A failure with an explicit exit code and remediation.
#[derive(Debug, Clone, PartialEq, Eq, Serialize, thiserror::Error)]
#[error("{cause}")]
pub struct Failure {
    pub code: i32,
    pub cause: String,
    pub remediation: String,
}

impl Failure {
    pub fn new(code: i32, cause: impl Into<String>, remediation: impl Into<String>) -> Self {
        Self {
            code,
            cause: cause.into(),
            remediation: remediation.into(),
        }
    }
}

const DEFAULT_REMEDIATION: &str = "Check the cause, run ccow doctor --verbose, and retry after \
                                   correcting configuration, source selection, permissions, or \
                                   active cache use.";
const REGISTRY_BUSY_REMEDIATION: &str = "Another ccow process held the shared lease registry; \
                                         retry the command. If this repeats, run ccow doctor \
                                         --verbose.";

const CONFIG_REMEDIATION: &str = "Check config get/set/unset arguments, saved record version, and \
                                  same-repository source selection.";

/// A configuration error (exit 2) with the config remediation.
fn config_failure(cause: impl Into<String>) -> anyhow::Error {
    anyhow::Error::new(Failure::new(2, cause, CONFIG_REMEDIATION))
}

/// Classifies a failure from reading or writing the saved settings record.
/// An I/O error anywhere in the cause chain (unwritable state root, failed
/// read, rename or sync) and a busy lease registry are environment failures
/// that keep their original error so `fail_with` reports exit 125. Every other
/// failure means the saved record itself is invalid (unsupported
/// `schemaVersion`, undecodable content, trailing data, or non-private
/// permissions), which is a configuration error (exit 2).
fn config_record_error(e: anyhow::Error) -> anyhow::Error {
    if error::find::<std::io::Error>(&e).is_some() || error::is(&e, &LeaseError::RegistryBusy) {
        return e;
    }
    config_failure(error_text(&e))
}

/// A usage error (exit 2).
pub(crate) fn bad(cause: impl Into<String>) -> anyhow::Error {
    anyhow::Error::new(Failure::new(
        2,
        cause,
        "Run ccow --help for supported commands and options.",
    ))
}

pub(crate) fn parse(args: &[String]) -> Result<Options> {
    let mut o = Options::default();
    let mut i = 0;
    while i < args.len() {
        let a = &args[i];
        if a == "--" {
            o.command = args[i + 1..].to_vec();
            break;
        }
        let (key, inline) = match a.split_once('=') {
            Some((k, v)) => (k, Some(v.to_string())),
            None => (a.as_str(), None),
        };
        match key {
            "--preset" | "--source" | "--file" => {
                let value = match inline {
                    Some(v) => v,
                    None => {
                        i += 1;
                        if i == args.len() {
                            return Err(bad(format!("{key} requires a value")));
                        }
                        args[i].clone()
                    }
                };
                if value.is_empty() {
                    return Err(bad(format!("{key} requires a value")));
                }
                match key {
                    "--preset" => o.preset = value,
                    "--source" => o.source = value,
                    _ => o.file = value,
                }
            }
            "--allow-copy" | "--replace" | "--json" | "--yes" | "--verbose" | "--help" | "-h" => {
                if inline.is_some() {
                    return Err(bad("boolean options do not accept values"));
                }
                match key {
                    "--allow-copy" => o.allow_copy = true,
                    "--replace" => o.replace = true,
                    "--json" => o.json = true,
                    "--yes" => o.yes = true,
                    "--verbose" => o.verbose = true,
                    _ => o.help = true,
                }
            }
            _ => {
                if a.starts_with('-') {
                    return Err(bad(format!("unknown option {a}")));
                }
                o.args.push(a.clone());
            }
        }
        i += 1;
    }
    if !o.preset.is_empty() && shim::Tool::parse(&o.preset).is_none() {
        return Err(bad(format!("unknown preset {}", o.preset)));
    }
    Ok(o)
}

/// Reports whether the caller asked for the JSON envelope as a CacheCow option.
///
/// Argument errors are found before `parse` can return its options, so this
/// scans the raw arguments instead. It stops at `--` because later arguments
/// belong to the child command, and it skips the value of `--preset`,
/// `--source` and `--file` the same way `parse` does. `--json=value` counts as
/// a request because `parse` rejects it as a boolean option with a value, and
/// the caller still expects an envelope.
pub(crate) fn requests_json(args: &[String]) -> bool {
    let mut i = 0;
    while i < args.len() {
        let a = args[i].as_str();
        if a == "--" {
            return false;
        }
        match a.split_once('=') {
            Some(("--json", _)) => return true,
            Some(_) => {}
            None => match a {
                "--json" => return true,
                "--preset" | "--source" | "--file" => i += 1,
                _ => {}
            },
        }
        i += 1;
    }
    false
}

const USAGE: &str = "ccow run [--preset cargo|rspack|rsbuild] [--source WORKTREE] [--allow-copy] \
                     [--replace] -- COMMAND [ARGS...]
ccow prepare|status|clean|doctor [--preset PRESET] [--source WORKTREE] [--json]
ccow prepare [--replace] [--allow-copy] [-- COMMAND [ARGS...]]
ccow config get [source] | config set source WORKTREE | config unset source
ccow shim install|uninstall|list|doctor [--json]
ccow shell init|setup|remove bash|zsh|powershell [--file FILE] [--yes]
ccow --version
Use --verbose for diagnostics; NO_COLOR disables ANSI notices.
prepare never runs a build. Use its optional command after -- to discover exact build settings.
";

/// Writes an English notice to stderr, colored unless `NO_COLOR` is set.
pub(crate) fn notice(message: &str) {
    let color = std::env::var_os("NO_COLOR").is_none();
    let _ = std::io::stderr().write_all(crate::session::notice_line(message, color).as_bytes());
}

fn stdout_write(text: &str) {
    let mut out = std::io::stdout().lock();
    let _ = out.write_all(text.as_bytes());
    let _ = out.flush();
}

/// The current environment as `KEY=value` entries.
pub(crate) fn environ() -> Vec<String> {
    std::env::vars_os()
        .map(|(k, v)| format!("{}={}", k.to_string_lossy(), v.to_string_lossy()))
        .collect()
}

fn env_string(key: &str) -> String {
    std::env::var(key).unwrap_or_default()
}

/// The `PATH` used for tool lookup. Entries that are not valid UTF-8 cannot be
/// represented by the string based resolution code, so they are dropped and the
/// remaining entries stay searchable. A tool installed only below such a
/// directory is therefore not found by CacheCow. The pass-through environment
/// is never rewritten.
fn env_path() -> String {
    std::env::var_os("PATH")
        .map(|value| utf8_path_entries(&value))
        .unwrap_or_default()
}

/// The `PATHEXT` used for tool lookup, without entries that are not valid
/// UTF-8.
fn env_path_ext() -> String {
    let Some(value) = std::env::var_os("PATHEXT") else {
        return String::new();
    };
    match value.to_str() {
        Some(text) => text.to_string(),
        None => value
            .to_string_lossy()
            .split(';')
            .filter(|entry| !entry.contains('\u{fffd}'))
            .collect::<Vec<_>>()
            .join(";"),
    }
}

/// The variables that decide where a tool is looked up.
pub(crate) struct LookupEnv {
    path: String,
    path_ext: String,
}

impl LookupEnv {
    /// The lookup variables of the current process.
    fn ambient() -> Self {
        Self {
            path: env_path(),
            path_ext: env_path_ext(),
        }
    }

    /// The lookup variables of a `KEY=value` environment list, such as the
    /// environment translated for a source worktree. Entries that are not valid
    /// UTF-8 were already replaced by U+FFFD when the list was built, so they
    /// are dropped like in the ambient lookup.
    fn from_env(env: &[String]) -> Self {
        let path = crate::preset::env_value(env, "PATH");
        let path_ext = crate::preset::env_value(env, "PATHEXT");
        let kept: Vec<_> = std::env::split_paths(path)
            .filter(|entry| !entry.to_string_lossy().contains('\u{fffd}'))
            .collect();
        Self {
            path: std::env::join_paths(kept)
                .ok()
                .and_then(|joined| joined.to_str().map(str::to_string))
                .unwrap_or_default(),
            path_ext: path_ext
                .split(';')
                .filter(|entry| !entry.contains('\u{fffd}'))
                .collect::<Vec<_>>()
                .join(";"),
        }
    }
}

/// Re-joins the entries of a path list that are valid UTF-8, in order.
fn utf8_path_entries(value: &std::ffi::OsStr) -> String {
    if let Some(text) = value.to_str() {
        return text.to_string();
    }
    let kept: Vec<_> = std::env::split_paths(value)
        .filter(|entry| entry.to_str().is_some())
        .collect();
    match std::env::join_paths(kept) {
        Ok(joined) => joined.to_str().map(str::to_string).unwrap_or_default(),
        Err(e) => {
            tracing::debug!(error = %e, "PATH entries could not be re-joined");
            String::new()
        }
    }
}

/// Error for a managed command whose child argument is not valid UTF-8.
/// `index` counts the child argv, where `ARG #0` is the command itself. The
/// value is never printed.
/// Remediation for a failed shim pass-through. Exit code 2 means the arguments
/// were rejected before the real tool started (Windows passes text only, so an
/// argument that is not valid UTF-8 is refused instead of being changed). Any
/// other code means the real tool could not be started.
pub(crate) fn passthrough_remediation(code: i32) -> &'static str {
    if code == 2 {
        "Pass arguments that are valid UTF-8. Pass-through never changes arguments; to give the \
         tool other bytes, run it by its full path or remove the CacheCow shim directory from PATH."
    } else {
        "Check the real executable and its permissions."
    }
}

fn managed_arg_error(index: usize) -> anyhow::Error {
    bad(format!(
        "managed commands require UTF-8 arguments and environment; ARG #{index} is not valid UTF-8"
    ))
}

/// Fails when a child argument is not valid UTF-8.
/// `offset` is the child argv index of `args[0]`.
pub(crate) fn check_managed_args(args: &[std::ffi::OsString], offset: usize) -> Result<()> {
    match args.iter().position(|arg| arg.to_str().is_none()) {
        Some(i) => Err(managed_arg_error(offset + i)),
        None => Ok(()),
    }
}

/// Fails when an environment entry is not valid UTF-8. Managed execution
/// serializes the environment as text, so it must not change bytes silently.
/// Only the variable name is reported, never its value.
pub(crate) fn check_managed_environment() -> Result<()> {
    for (key, value) in std::env::vars_os() {
        if key.to_str().is_none() || value.to_str().is_none() {
            return Err(bad(format!(
                "managed commands require UTF-8 arguments and environment; variable {} is not \
                 valid UTF-8",
                key.to_string_lossy()
            )));
        }
    }
    Ok(())
}

pub(crate) struct App {
    pub store: state::Store,
    pub o: Options,
    pub ctx: Context,
    input: Option<std::io::StdinLock<'static>>,
}

fn init_logging(verbose: bool) {
    let level = if verbose {
        tracing::Level::DEBUG
    } else {
        tracing::Level::WARN
    };
    let _ = tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_max_level(level)
        .with_ansi(std::env::var_os("NO_COLOR").is_none())
        .with_target(false)
        .try_init();
}

/// Process entry point after supervisor dispatch. Returns the exit code.
pub fn main(version: &str) -> i32 {
    // Shim pass-through must deliver the original bytes, so the raw values are
    // kept. The lossy copy is only used to detect the shim, classify the call
    // and parse CacheCow's own options.
    let raw: Vec<std::ffi::OsString> = std::env::args_os().collect();
    let args: Vec<String> = raw
        .iter()
        .map(|arg| arg.to_string_lossy().into_owned())
        .collect();
    let first_non_utf8 = raw
        .iter()
        .skip(1)
        .position(|arg| arg.to_str().is_none())
        .map(|i| i + 1);
    let argv0 = args.first().cloned().unwrap_or_default();
    // A shim never parses the real tool's arguments as CacheCow options.
    let shim_tool = shim::from_argv0(&argv0);
    if shim_tool.is_none() && args.len() == 2 && args[1] == "--version" {
        stdout_write(&format!("ccow {version}\n"));
        return 0;
    }
    let mut o = Options::default();
    if shim_tool.is_none() {
        match parse(&args[1..]) {
            Ok(v) => o = v,
            Err(e) => return fail_with(requests_json(&args[1..]), &e),
        }
        if let Some(index) = first_non_utf8 {
            // The lossy copy parsed, so CacheCow's own options are valid. A
            // managed child command must keep its bytes exactly, which the
            // supervisor protocol cannot, so it is rejected explicitly. Other
            // text arguments are plain parse input.
            let managed = matches!(
                o.args.first().map(String::as_str),
                Some("run" | "prepare" | "status" | "clean" | "doctor")
            );
            let child_start = args.len() - o.command.len();
            let error = if managed && index >= child_start {
                managed_arg_error(index - child_start)
            } else {
                bad("arguments must be valid UTF-8")
            };
            return fail_with(o.json, &error);
        }
        // Usage is printed for --help and for a bare `ccow`. Any other input
        // without a subcommand (for example `ccow -- cargo build`) is an
        // argument error so that a forgotten `run` never reports success.
        if o.help || args.len() == 1 {
            stdout_write(USAGE);
            return 0;
        }
        if o.args.is_empty() {
            return fail_with(o.json, &bad("missing command; use ccow run -- COMMAND"));
        }
    }
    init_logging(o.verbose);
    let (ctx, cancel) = Context::background().with_cancel();
    let _signals = match signals::notify(signals::SignalSet::Cli, cancel) {
        Ok(registration) => Some(registration),
        Err(e) => {
            tracing::debug!(error = %error_text(&e), "termination signals are not observed");
            None
        }
    };
    let json = o.json;
    let store = match state::Store::open("") {
        Ok(store) => store,
        Err(e) => return fail_with(json, &e),
    };
    let mut app = App {
        store,
        o,
        ctx,
        input: None,
    };
    if let Some(tool) = shim_tool {
        return app.shim_run(tool, &args[1..], &raw[1..]);
    }
    match app.run() {
        Ok(code) => code,
        Err(e) => app.fail(&e),
    }
}

/// Maps an error to the failure envelope and prints it.
fn fail_with(json: bool, e: &anyhow::Error) -> i32 {
    let mut f = Failure::new(125, error_text(e), DEFAULT_REMEDIATION);
    if error::is(e, &LeaseError::RegistryBusy) {
        f = Failure::new(
            125,
            LeaseError::RegistryBusy.to_string(),
            REGISTRY_BUSY_REMEDIATION,
        );
    }
    if let Some(own) = error::find::<Failure>(e) {
        f = own.clone();
    }
    if let Some(resolve) = error::find::<shim::ResolveError>(e) {
        f.code = resolve.exit_code();
    }
    if json {
        let envelope = json!({"schemaVersion": 1, "success": false, "error": f});
        stdout_write(&format!("{envelope}\n"));
    } else {
        notice(&format!(
            "error: {}\nRemediation: {}",
            f.cause, f.remediation
        ));
    }
    f.code
}

impl App {
    pub(crate) fn fail(&self, e: &anyhow::Error) -> i32 {
        fail_with(self.o.json, e)
    }

    /// Prints a result: an envelope with `--json`, otherwise indented JSON.
    pub(crate) fn output<T: Serialize + ?Sized>(&self, value: &T) -> Result<()> {
        if self.o.json {
            let envelope = json!({"schemaVersion": 1, "success": true, "result": value});
            stdout_write(&format!("{envelope}\n"));
            return Ok(());
        }
        let text = serde_json::to_string_pretty(value)?;
        stdout_write(&format!("{text}\n"));
        Ok(())
    }

    pub(crate) fn interactive(&self) -> bool {
        std::io::stdin().is_terminal()
    }

    pub(crate) fn answer(&mut self, prompt: &str) -> Result<String> {
        let _ = std::io::stderr().write_all(prompt.as_bytes());
        let input = self.input.get_or_insert_with(|| std::io::stdin().lock());
        let mut line = String::new();
        let n = input.read_line(&mut line)?;
        if n == 0 {
            return Err(anyhow::Error::new(std::io::Error::from(
                std::io::ErrorKind::UnexpectedEof,
            ))
            .context("EOF"));
        }
        Ok(line.trim().to_string())
    }

    pub(crate) fn confirm(&mut self, prompt: &str) -> Result<bool> {
        let s = self.answer(&format!("{prompt} [y/N] "))?;
        Ok(s.eq_ignore_ascii_case("y") || s.eq_ignore_ascii_case("yes"))
    }

    fn shim_dir(&self) -> String {
        paths::join2(&self.store.root, "shims")
    }

    fn resolve_options(&self) -> Result<shim::ResolveOptions> {
        self.resolve_options_in(&LookupEnv::ambient())
    }

    fn resolve_options_in(&self, lookup: &LookupEnv) -> Result<shim::ResolveOptions> {
        let exe = paths::path_string(&std::env::current_exe()?)?;
        Ok(shim::ResolveOptions {
            path: lookup.path.clone(),
            path_ext: lookup.path_ext.clone(),
            shim_dir: self.shim_dir(),
            ccow_executable: exe,
        })
    }

    pub(crate) fn resolve(&self, tool: shim::Tool) -> Result<shim::Resolution> {
        shim::resolve(tool, &self.resolve_options()?).map_err(|f| f.error)
    }

    /// Resolves a tool through the `PATH` and `PATHEXT` of `env`.
    pub(crate) fn resolve_in(&self, tool: shim::Tool, env: &[String]) -> Result<shim::Resolution> {
        let options = self.resolve_options_in(&LookupEnv::from_env(env))?;
        shim::resolve(tool, &options).map_err(|f| f.error)
    }

    /// Resolves the exact executable that discovery and execution share. The
    /// executable is looked up through the `PATH` of `env`, the same
    /// environment the child receives, so a command translated for another
    /// worktree never runs a tool from the original worktree.
    pub(crate) fn command_in(
        &self,
        argv: &[String],
        cwd: &str,
        env: &[String],
    ) -> Result<process::Command> {
        let Some(first) = argv.first() else {
            return Err(bad("run requires -- COMMAND [ARGS...]"));
        };
        let mut path = first.clone();
        if !path.contains(['/', '\\']) {
            if let Some(tool) = shim::from_argv0(&path) {
                path = self.resolve_in(tool, env)?.path;
            } else {
                path = look_path(&path, &LookupEnv::from_env(env)).map_err(|e| {
                    anyhow::Error::new(Failure::new(
                        process::execution_code(&e),
                        error_text(&e),
                        "Install the command and add its executable directory to PATH.",
                    ))
                })?;
            }
        }
        if !paths::is_abs(&path) {
            path = paths::join2(cwd, &path);
        }
        let path = paths::abs(&path)?;
        Ok(process::Command {
            noop: false,
            path,
            args: argv.to_vec(),
            dir: cwd.to_string(),
            env: env.to_vec(),
        })
    }

    fn run(&mut self) -> Result<i32> {
        let cmd = self.o.args[0].clone();
        if (self.o.replace || self.o.allow_copy) && cmd != "run" && cmd != "prepare" {
            return Err(bad(
                "--replace and --allow-copy apply only to run and prepare"
            ));
        }
        if (self.o.yes || !self.o.file.is_empty()) && cmd != "shell" {
            return Err(bad("--yes and --file apply only to shell setup/remove"));
        }
        if matches!(cmd.as_str(), "config" | "shim" | "shell")
            && (!self.o.source.is_empty()
                || !self.o.preset.is_empty()
                || !self.o.command.is_empty())
        {
            return Err(bad(
                "cache options and child argv do not apply to this command"
            ));
        }
        if cmd == "clean" && !self.o.source.is_empty() {
            return Err(bad("clean selects the current worktree cache, not a source"));
        }
        match cmd.as_str() {
            "shim" => self.shims().map(|()| 0),
            "shell" => self.shell().map(|()| 0),
            "config" => self.config().map(|()| 0),
            "run" | "prepare" | "status" | "clean" | "doctor" => {
                if self.o.args.len() != 1 {
                    return Err(bad(
                        "unexpected positional arguments; put the child command after --"
                    ));
                }
                if cmd == "run" && self.o.json {
                    return Err(bad(
                        "--json is for management commands; run preserves child streams"
                    ));
                }
                let argv = self.o.command.clone();
                self.managed(&cmd, &argv)
            }
            other => Err(bad(format!("unknown command {other}"))),
        }
    }

    fn config(&mut self) -> Result<()> {
        if self.o.args.len() < 2 {
            return Err(bad("config requires get, set, or unset"));
        }
        // Validate every argument before touching Git or the state root so
        // usage mistakes never surface as environment failures.
        let args = self.o.args[1..].to_vec();
        match args[0].as_str() {
            "get" if args.len() <= 2 && args.get(1).is_none_or(|a| a == "source") => {}
            "get" => return Err(bad("config get [source]")),
            "set" if args.len() == 3 && args[1] == "source" => {}
            "set" => return Err(bad("config set source WORKTREE")),
            "unset" if args.len() == 2 && args[1] == "source" => {}
            "unset" => return Err(bad("config unset source")),
            _ => return Err(bad("unknown config operation")),
        }
        let cwd = paths::current_dir()?;
        // Git discovery of the current directory is an environment concern
        // (exit 125), so it is deliberately not classified as a config error.
        let r = repository::discover(&self.ctx, &cwd)?;
        match args[0].as_str() {
            "get" => {
                let v = self.store.settings(&r.id).map_err(config_record_error)?;
                return self.output(&v);
            }
            "set" => {
                // The source path is a user argument: every failure to
                // resolve it, including Git discovery on that path, is an
                // argument error.
                let s = repository::select_source(&self.ctx, &r, &args[2], "")
                    .map_err(|e| config_failure(error_text(&e)))?;
                self.store
                    .set_source(&r.id, &s.repository.root)
                    .map_err(config_record_error)?;
            }
            _ => {
                self.store
                    .set_source(&r.id, "")
                    .map_err(config_record_error)?;
            }
        }
        self.output(&BTreeMap::from([("updated", true)]))
    }

    fn shims(&mut self) -> Result<()> {
        if self.o.args.len() != 2 {
            return Err(bad("shim requires install, uninstall, list, or doctor"));
        }
        let dir = self.shim_dir();
        match self.o.args[1].as_str() {
            "install" => {
                let exe = paths::path_string(&std::env::current_exe()?)?;
                shim::install(&dir, &exe)?;
            }
            "uninstall" => {
                shim::uninstall(&dir)?;
                notice(
                    "Shell registration blocks remain. Use ccow shell remove SHELL to remove them.",
                );
            }
            "list" => {}
            "doctor" => {
                let entries = shim::list(&dir)?;
                let options = self.resolve_options()?;
                let mut selected = BTreeMap::new();
                for tool in shim::Tool::ALL {
                    let value = match shim::resolve(tool, &options) {
                        Ok(r) => serde_json::to_value(r)?,
                        Err(f) => {
                            json!({"resolution": f.resolution, "cause": error_text(&f.error)})
                        }
                    };
                    selected.insert(tool.as_str().to_string(), value);
                }
                return self.output(&json!({
                    "installation": entries,
                    "tools": selected,
                    "shimDirectory": dir,
                }));
            }
            _ => return Err(bad("unknown shim operation")),
        }
        let entries = shim::list(&dir)?;
        self.output(&entries)
    }

    fn shell(&mut self) -> Result<()> {
        if self.o.args.len() != 3 {
            return Err(bad(
                "shell requires init, setup, or remove and bash, zsh, or powershell"
            ));
        }
        let dir = self.shim_dir();
        let name = self.o.args[2].clone();
        if self.o.args[1] == "init" {
            if !self.o.file.is_empty() || self.o.yes {
                return Err(bad(
                    "shell init emits code; --file and --yes apply to setup/remove"
                ));
            }
            if self.o.json {
                return Err(bad(
                    "shell init emits shell code and does not support --json"
                ));
            }
            let kind = shell::Kind::parse(&name).map_err(|e| bad(error_text(&e)))?;
            let code = shell::init(kind, &dir)?;
            stdout_write(&code);
            return Ok(());
        }
        let Some(action) = shell::Action::parse(&self.o.args[1]) else {
            return Err(bad("unknown shell operation"));
        };
        // Parse the shell name before the home lookup so an unsupported name is
        // an argument error (exit 2) even when the home directory is unknown.
        let kind = shell::Kind::parse(&name).map_err(|e| bad(error_text(&e)))?;
        let home = paths::user_home_dir()?;
        let path = shell::select_file(
            kind,
            &shell::Selection {
                file: self.o.file.clone(),
                home,
                zdotdir: env_string("ZDOTDIR"),
                powershell_profile: String::new(),
            },
        )?;
        let plan = shell::prepare(kind, action, &path, &dir)?;
        tracing::debug!(
            shell = kind.as_str(),
            action = action.as_str(),
            path = %plan.path,
            changed = plan.changed(),
            "shell configuration plan prepared"
        );
        notice(&shell_change_notice(&plan));
        let interactive = self.interactive();
        let yes = self.o.yes;
        {
            let mut confirm =
                |_: &shell::Plan| self.confirm("Apply this shell configuration change?");
            plan.apply(shell::Approval {
                yes,
                interactive,
                confirm: Some(&mut confirm),
            })?;
        }
        if plan.changed() {
            notice(&plan.current_session_instruction());
        }
        self.output(&json!({"path": plan.path, "changed": plan.changed()}))
    }

    /// Runs a shim invocation. `args` is the lossy text copy used for
    /// classification; `raw` holds the original values for pass-through.
    fn shim_run(&mut self, tool: shim::Tool, args: &[String], raw: &[std::ffi::OsString]) -> i32 {
        let resolution = match self.resolve(tool) {
            Ok(r) => r,
            Err(e) => return self.fail(&e),
        };
        let cwd = match paths::current_dir() {
            Ok(c) => c,
            Err(e) => return self.fail(&e),
        };
        let bypass = env_string("CCOW_BYPASS");
        let mut classification = shim::classify(tool, args, true, &bypass);
        if classification.mode != shim::Mode::Passthrough
            && let Err(git_error) = repository::discover(&self.ctx, &cwd)
        {
            let mut dir = cwd.clone();
            loop {
                if std::fs::symlink_metadata(paths::join2(&dir, ".git")).is_ok() {
                    return self.fail(&git_error);
                }
                let parent = paths::dir(&dir);
                if parent == dir {
                    break;
                }
                dir = parent;
            }
            classification = shim::classify(tool, args, false, &bypass);
        }
        if classification.mode == shim::Mode::Passthrough {
            if classification.reason == shim::Reason::Unknown {
                notice(
                    "Unrecognized command passes through. Use ccow run -- for cache management.",
                );
            }
            // The real tool receives the original arguments and inherits the
            // process environment unchanged.
            return match direct::direct(&resolution.path, tool.as_str(), raw, &cwd) {
                Ok(code) => code,
                Err((code, e)) => self.fail(&anyhow::Error::new(Failure::new(
                    code,
                    error_text(&e),
                    passthrough_remediation(code),
                ))),
            };
        }
        // Managed execution serializes the child argv as text.
        if let Err(e) = check_managed_args(raw, 1) {
            return self.fail(&e);
        }
        let mut argv = vec![tool.as_str().to_string()];
        argv.extend_from_slice(args);
        self.o.preset = tool.as_str().to_string();
        let action = if classification.mode == shim::Mode::Clean {
            "tool-clean"
        } else {
            "run"
        };
        match self.managed(action, &argv) {
            Ok(code) => code,
            Err(e) => self.fail(&e),
        }
    }
}

/// Go `exec.LookPath` against the `PATH` and `PATHEXT` of `lookup`.
fn look_path(file: &str, lookup: &LookupEnv) -> Result<String> {
    let path = &lookup.path;
    #[cfg(windows)]
    let names: Vec<String> = {
        let ext = if lookup.path_ext.is_empty() {
            ".COM;.EXE;.BAT;.CMD"
        } else {
            lookup.path_ext.as_str()
        };
        if !paths::ext(file).is_empty() {
            vec![file.to_string()]
        } else {
            ext.split(';')
                .filter(|e| !e.is_empty())
                .map(|e| format!("{file}{}", e.to_lowercase()))
                .collect()
        }
    };
    #[cfg(not(windows))]
    let names = vec![file.to_string()];
    for dir in paths::split_list(path) {
        let dir = if dir.is_empty() { ".".to_string() } else { dir };
        for name in &names {
            let candidate = paths::join2(&dir, name);
            if executable(&candidate) {
                if !paths::is_abs(&candidate) {
                    return Err(anyhow!(
                        "exec: {file:?}: cannot run executable found relative to current directory"
                    ));
                }
                return Ok(candidate);
            }
        }
    }
    Err(anyhow::Error::new(std::io::Error::new(
        std::io::ErrorKind::NotFound,
        format!("exec: {file:?}: executable file not found in $PATH"),
    )))
}

fn executable(path: &str) -> bool {
    let Ok(m) = std::fs::metadata(path) else {
        return false;
    };
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        !m.is_dir() && m.permissions().mode() & 0o111 != 0
    }
    #[cfg(not(unix))]
    {
        !m.is_dir()
    }
}

/// Describes the proposed shell configuration change. Shows only the
/// CacheCow-owned block and never any other profile content.
pub(crate) fn shell_change_notice(plan: &shell::Plan) -> String {
    let mut b = format!("Shell configuration target: {}", plan.path);
    if !plan.changed() {
        if plan.action == shell::Action::Remove {
            b.push_str("\nNo change: no CacheCow block to remove.");
        } else {
            b.push_str("\nNo change: CacheCow block already up to date.");
        }
        return b;
    }
    let (removed, added, separator) = plan.owned_change();
    if !removed.is_empty() && !added.is_empty() {
        b.push_str("\nAction: replace CacheCow block");
    } else if !added.is_empty() {
        b.push_str("\nAction: add CacheCow block");
    } else {
        b.push_str("\nAction: remove CacheCow block");
    }
    write_block_lines(&mut b, "- ", &removed);
    write_block_lines(&mut b, "+ ", &added);
    if separator {
        if added.is_empty() {
            b.push_str("\nThe separator line break before the block is removed.");
        } else {
            b.push_str("\nA separator line break is added before the block.");
        }
    }
    b
}

fn write_block_lines(b: &mut String, prefix: &str, block: &[u8]) {
    if block.is_empty() {
        return;
    }
    let text = String::from_utf8_lossy(block);
    for line in text.strip_suffix('\n').unwrap_or(&text).split('\n') {
        b.push('\n');
        b.push_str(prefix);
        b.push_str(line.strip_suffix('\r').unwrap_or(line));
    }
}

/// Supervisor-side claim validation (filesystem reads only).
pub fn validate(plan: &Plan) -> Result<()> {
    managed::validate(plan)
}

/// Provenance hooks run by the supervisor under its leases.
pub fn records() -> RecordHooks {
    managed::records()
}
