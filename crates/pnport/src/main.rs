use std::{
    ffi::OsString,
    fs,
    io::{self, IsTerminal},
    path::{Path, PathBuf},
};

use clap::{Parser, Subcommand, ValueEnum};
use pnport::{
    cache::{self, Cache, Operation, State},
    diagnostic::{cache_error, Code, Error, Result},
    graph::{self, Graph},
    view::View,
};
use serde::Serialize;

mod input_watch;
#[cfg(target_os = "linux")]
mod linux;
#[cfg(any(target_os = "linux", test))]
mod linux_scratch;
#[cfg(target_os = "macos")]
mod macos_job;
#[cfg(target_os = "macos")]
mod macos_owner;
#[cfg(target_os = "macos")]
mod macos_registry;
#[cfg(target_os = "macos")]
mod macos_replica;
mod supervisor;
#[cfg(windows)]
mod windows;
#[cfg(windows)]
mod windows_launch;

#[derive(Parser)]
#[command(
    version,
    about = "Run subprocesses through an installed Yarn 4 Plug'n'Play filesystem",
    after_help = "Support: https://github.com/delinoio/oss/issues\nDocumentation: https://oss.delino.io/pnport\nDependency views are read-only. Graph changes require restarting the process tree."
)]
struct Cli {
    #[arg(
        long,
        global = true,
        help = "Select a project directory or its .pnp.cjs without changing cwd"
    )]
    project: Option<PathBuf>,
    #[arg(
        long,
        global = true,
        help = "Override the private per-user cache directory"
    )]
    cache_dir: Option<PathBuf>,
    #[arg(long, global = true, value_enum, default_value = "error")]
    log_level: LogLevel,
    #[arg(long, global = true, value_enum, default_value = "auto")]
    color: Color,
    #[command(subcommand)]
    command: Action,
}
#[derive(Clone, Copy, ValueEnum)]
enum LogLevel {
    Error,
    Warn,
    Info,
    Debug,
    Trace,
}
#[derive(Clone, Copy, ValueEnum)]
enum Color {
    Auto,
    Always,
    Never,
}
#[derive(Subcommand)]
enum Action {
    /// Execute with inherited cwd, environment and standard streams.
    Run {
        #[arg(required = true, last = true, num_args = 1.., allow_hyphen_values = true)]
        command: Vec<OsString>,
    },
    /// Diagnose project data, native injection prerequisites and cache access.
    Doctor {
        #[arg(long)]
        json: bool,
    },
    /// Inspect or explicitly clean the local package cache (no project
    /// required).
    Cache {
        #[command(subcommand)]
        command: CacheCommand,
    },
}
#[derive(Subcommand)]
enum CacheCommand {
    Path,
    List,
    Prune,
    Clean,
}

#[derive(Serialize)]
#[serde(rename_all = "kebab-case")]
enum Status {
    Pass,
    Fail,
    Unsupported,
}
#[derive(Serialize)]
struct Check {
    id: &'static str,
    status: Status,
    code: Code,
    message: &'static str,
}
#[derive(Serialize)]
#[serde(rename_all = "camelCase")]
struct Report {
    schema_version: u32,
    ready: bool,
    checks: Vec<Check>,
}
fn check(id: &'static str, result: Result<()>, success: &'static str) -> Check {
    match result {
        Ok(()) => Check {
            id,
            status: Status::Pass,
            code: Code::PnportReady,
            message: success,
        },
        Err(error) => Check {
            id,
            status: if error.code == Code::PnportUnsupportedOperation {
                Status::Unsupported
            } else {
                Status::Fail
            },
            code: error.code,
            message: error.message,
        },
    }
}

fn cache_path(cli: &Cli, cwd: &Path) -> Result<PathBuf> {
    cli.cache_dir
        .as_ref()
        .map(|path| graph::normalize(&cwd.join(path)))
        .map_or_else(cache::default_path, Ok)
}
fn load_graph(cli: &Cli, cwd: &Path) -> Result<Graph> {
    Graph::load(&graph::select(cli.project.as_deref(), cwd)?)
}
#[derive(Debug)]
enum RunStage {
    Starting,
    ProjectLoaded,
    ProjectValidated,
    PlatformValidated,
    CompanionValidated,
    CacheOpened,
    SessionPrepared,
    CommandResolved,
}

fn run_stage(stage: RunStage) {
    tracing::debug!(action = "execution_stage", stage = ?stage, "Preparing owned execution");
}

fn execute(cli: &Cli) -> Result<i32> {
    let cwd = std::env::current_dir().map_err(|_| {
        Error::new(
            Code::PnportCommandNotExecutable,
            "Cannot access the current working directory.",
        )
    })?;
    match &cli.command {
        Action::Cache { command } => {
            let path = cache_path(cli, &cwd)?;
            if matches!(command, CacheCommand::Path) {
                println!("{}", path.display());
                return Ok(0);
            }
            let cache = Cache::open(path)?;
            let entries = cache.entries(match command {
                CacheCommand::List => Operation::List,
                CacheCommand::Prune => Operation::Prune,
                CacheCommand::Clean => Operation::Clean,
                CacheCommand::Path => unreachable!(),
            })?;
            let failed = entries
                .iter()
                .any(|entry| matches!(entry.state, State::Failed));
            for entry in entries {
                println!(
                    "{}\t{}",
                    serde_json::to_value(&entry.state)
                        .unwrap()
                        .as_str()
                        .unwrap(),
                    entry.name
                );
            }
            if failed {
                Err(Error::new(
                    Code::PnportCleanupFailed,
                    "Some cache entries could not be deleted; retained entries are listed above.",
                ))
            } else {
                Ok(0)
            }
        }
        Action::Doctor { json } => {
            #[allow(unused_mut, reason = "Linux adds a syscall capability check")]
            let mut checks = vec![
                check(
                    "project",
                    load_graph(cli, &cwd).and_then(|graph| graph.check_conflicts()),
                    "Yarn PnP data is valid and no dependency-view conflicts were found.",
                ),
                check(
                    "platform",
                    supervisor::platform(),
                    "The host matches a supported native target.",
                ),
                check(
                    "injection",
                    supervisor::artifact().map(|_| ()),
                    "The matching native injection artifact is available; command-specific \
                     protection is checked at run time.",
                ),
                check(
                    "cache",
                    cache_path(cli, &cwd).and_then(Cache::open).map(|_| ()),
                    "Private cache access is available.",
                ),
            ];
            #[cfg(target_os = "linux")]
            checks.push(check(
                "linux-syscall",
                linux::probe(),
                "Owned-child seccomp syscall interception is available.",
            ));
            let ready = checks
                .iter()
                .all(|check| matches!(check.status, Status::Pass));
            let report = Report {
                schema_version: 1,
                ready,
                checks,
            };
            if *json {
                println!(
                    "{}",
                    serde_json::to_string(&report).map_err(|_| cache_error())?
                );
            } else {
                for check in &report.checks {
                    println!("{}: {}: {}", check.id, check.code.as_str(), check.message);
                }
            }
            Ok(if ready { 0 } else { 125 })
        }
        Action::Run { command } => {
            run_stage(RunStage::Starting);
            let graph = load_graph(cli, &cwd)?;
            run_stage(RunStage::ProjectLoaded);
            graph.check_conflicts()?;
            run_stage(RunStage::ProjectValidated);
            supervisor::platform()?;
            run_stage(RunStage::PlatformValidated);
            let artifact = supervisor::artifact()?;
            run_stage(RunStage::CompanionValidated);
            let cache = Cache::open(cache_path(cli, &cwd)?)?;
            run_stage(RunStage::CacheOpened);
            let mut session_builder = tempfile::Builder::new();
            let prefix = format!("pnport-{}-", uuid::Uuid::now_v7());
            session_builder.prefix(&prefix);
            #[cfg(not(windows))]
            let session = session_builder.tempdir().map_err(|_| cache_error())?;
            #[cfg(windows)]
            let session = {
                // Inherit the already verified user-only ACL at creation;
                // never expose a snapshot in the ordinary temporary directory.
                let sessions = cache.root.join("sessions");
                cache::private_dir(&sessions)?;
                session_builder
                    .tempdir_in(sessions)
                    .map_err(|_| cache_error())?
            };
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(session.path(), fs::Permissions::from_mode(0o700))
                    .map_err(|_| cache_error())?;
            }
            cache::private_dir(session.path())?;
            let bytes = serde_json::to_vec(&graph.snapshot).map_err(|_| cache_error())?;
            fs::write(session.path().join("graph.json"), bytes).map_err(|_| cache_error())?;
            run_stage(RunStage::SessionPrepared);
            let mut view = View::new(graph, cache, session.path().to_owned());
            let executable = resolve_command(&mut view, &cwd, &command[0])?;
            run_stage(RunStage::CommandResolved);
            supervisor::run(&mut view, &artifact, &executable, &command[1..])
        }
    }
}

#[derive(Debug)]
enum CommandTarget<'a> {
    Explicit(PathBuf),
    Bare { cwd: PathBuf, name: &'a str },
}

fn command_target_with_lookup<'a>(
    cwd: &Path,
    command: &'a OsString,
    lookup_cwd: impl FnOnce(&Path) -> Result<PathBuf>,
) -> Result<CommandTarget<'a>> {
    let path = Path::new(command);
    if path.components().count() > 1 || path.is_absolute() {
        return Ok(CommandTarget::Explicit(cwd.join(path)));
    }
    let cwd = lookup_cwd(cwd)?;
    let Some(name) = command.to_str() else {
        return Err(Error::new(
            Code::PnportCommandNotFound,
            "The executable was not found.",
        ));
    };
    Ok(CommandTarget::Bare { cwd, name })
}

fn resolve_command(view: &mut View, cwd: &Path, command: &OsString) -> Result<PathBuf> {
    let target = command_target_with_lookup(cwd, command, |cwd| {
        // Windows keeps short names and caller casing in GetCurrentDirectory,
        // while the graph is rooted at the canonical manifest directory. Bare
        // commands need guarded lookup to retain their direct-bin owner.
        #[cfg(windows)]
        {
            pnport_core::windows::lookup_spelling(cwd, &view.graph)
        }
        #[cfg(not(windows))]
        {
            Ok(cwd.to_owned())
        }
    })?;
    let (cwd, name) = match target {
        CommandTarget::Explicit(path) => return Ok(path),
        CommandTarget::Bare { cwd, name } => (cwd, name),
    };
    let cwd = cwd.as_path();
    tracing::debug!(action = "command_lookup", cwd = %cwd.display(), command = name, owner = ?view.graph.package(cwd).map(|package| &package.package_location), "Resolving direct dependency executable");
    let dependencies: Vec<_> = view
        .graph
        .package(cwd)
        .map(|pkg| pkg.package_dependencies.keys().cloned().collect())
        .unwrap_or_default();
    let mut bins = std::collections::BTreeSet::new();
    for dependency in dependencies {
        let package = match view.graph.resolve(&dependency, cwd) {
            Ok(package) => package,
            Err(error) => {
                tracing::debug!(action = "command_dependency_unresolved", dependency, error = %error);
                continue;
            }
        };
        tracing::debug!(action = "command_dependency_resolved", dependency, package = %package.display());
        let translation = view.translate(&package.join("package.json"))?;
        let bytes = fs::read(&translation.physical).map_err(|_| {
            Error::new(
                Code::PnportResolutionFailed,
                "A direct dependency is missing package.json; restore the Yarn installation.",
            )
        })?;
        let manifest: serde_json::Value = serde_json::from_slice(&bytes).map_err(|_| {
            Error::new(
                Code::PnportResolutionFailed,
                "A direct dependency has invalid package.json data.",
            )
        })?;
        let bin = match manifest.get("bin") {
            Some(serde_json::Value::String(bin))
                if manifest
                    .get("name")
                    .and_then(|n| n.as_str())
                    .and_then(|n| n.rsplit('/').next())
                    == Some(name) =>
            {
                Some(bin.as_str())
            }
            Some(serde_json::Value::Object(bins)) => bins.get(name).and_then(|bin| bin.as_str()),
            _ => None,
        };
        tracing::debug!(action = "command_bin_selected", dependency, bin);
        if let Some(bin) = bin {
            let target = graph::normalize(&package.join(bin));
            if !target.starts_with(graph::normalize(&package)) {
                return Err(Error::new(
                    Code::PnportResolutionFailed,
                    "A dependency bin escapes its package directory.",
                ));
            }
            bins.insert(target);
        }
    }
    if bins.len() > 1 {
        return Err(Error::new(
            Code::PnportResolutionFailed,
            "Several direct dependencies provide this bin; use an explicit package executable \
             path.",
        ));
    }
    if let Some(bin) = bins.into_iter().next() {
        return Ok(bin);
    }
    if let Some(candidate) =
        pnport::executable::find_on_path(command, std::env::var_os("PATH").as_deref(), cwd)
    {
        return Ok(candidate);
    }
    Err(Error::new(
        Code::PnportCommandNotFound,
        "Command not found in direct dependency bins or PATH; install it or use an explicit \
         executable path.",
    ))
}

#[cfg(test)]
mod command_resolution_tests {
    use std::{
        cell::Cell,
        ffi::OsString,
        path::{Path, PathBuf},
    };

    use super::{command_target_with_lookup, Code, CommandTarget, Error};

    #[test]
    fn explicit_paths_skip_cwd_lookup_and_bare_commands_use_it() {
        let cwd = std::env::current_dir().unwrap().join("caller");
        let absolute = std::env::current_dir().unwrap().join("tool.exe");
        let relative = PathBuf::from("bin").join("tool.exe");
        let calls = Cell::new(0);
        let unsupported = |_: &Path| {
            calls.set(calls.get() + 1);
            Err(Error::new(
                Code::PnportUnsupportedOperation,
                "The Windows pathname uses an unsupported reparse traversal.",
            ))
        };

        for (command, expected) in [
            (absolute.clone().into_os_string(), absolute),
            (relative.clone().into_os_string(), cwd.join(relative)),
        ] {
            let target = command_target_with_lookup(&cwd, &command, unsupported).unwrap();
            let CommandTarget::Explicit(path) = target else {
                panic!("explicit command was treated as a bare command");
            };
            assert_eq!(path, expected);
            assert_eq!(calls.get(), 0);
        }

        let bare = OsString::from("tool");
        let error = command_target_with_lookup(&cwd, &bare, unsupported)
            .err()
            .expect("bare command must perform guarded lookup");
        assert_eq!(error.code, Code::PnportUnsupportedOperation);
        assert_eq!(calls.get(), 1);

        let canonical = std::env::current_dir().unwrap().join("canonical-caller");
        let target = command_target_with_lookup(&cwd, &bare, |seen| {
            assert_eq!(seen, cwd);
            calls.set(calls.get() + 1);
            Ok(canonical.clone())
        })
        .unwrap();
        let CommandTarget::Bare { cwd: owner, name } = target else {
            panic!("bare command was treated as an explicit command");
        };
        assert_eq!(owner, canonical);
        assert_eq!(name, "tool");
        assert_eq!(calls.get(), 2);
    }

    #[cfg(windows)]
    #[test]
    fn windows_explicit_spellings_skip_guarded_lookup() {
        let cwd = PathBuf::from(r"C:\caller\alias");
        let calls = Cell::new(0);
        for (command, expected) in [
            (r"C:\tools\tool.exe", PathBuf::from(r"C:\tools\tool.exe")),
            (r".\bin\tool.exe", cwd.join(r".\bin\tool.exe")),
            (r"bin\tool.exe", cwd.join(r"bin\tool.exe")),
        ] {
            let command = OsString::from(command);
            let target = command_target_with_lookup(&cwd, &command, |_| {
                calls.set(calls.get() + 1);
                Err(Error::new(
                    Code::PnportUnsupportedOperation,
                    "The Windows pathname uses an unsupported reparse traversal.",
                ))
            })
            .unwrap();
            let CommandTarget::Explicit(path) = target else {
                panic!("explicit Windows command was treated as a bare command");
            };
            assert_eq!(path, expected);
        }
        assert_eq!(calls.get(), 0);
    }
}

fn main() {
    #[cfg(target_os = "linux")]
    linux::dispatch_helper();
    #[cfg(target_os = "macos")]
    macos_owner::dispatch_helper();
    let cli = Cli::parse();
    let json = matches!(cli.command, Action::Doctor { json: true });
    let ansi = !json
        && match cli.color {
            Color::Always => true,
            Color::Never => false,
            Color::Auto => std::env::var_os("NO_COLOR").is_none() && io::stderr().is_terminal(),
        };
    let level = match cli.log_level {
        LogLevel::Error => tracing::Level::ERROR,
        LogLevel::Warn => tracing::Level::WARN,
        LogLevel::Info => tracing::Level::INFO,
        LogLevel::Debug => tracing::Level::DEBUG,
        LogLevel::Trace => tracing::Level::TRACE,
    };
    tracing_subscriber::fmt()
        .with_writer(io::stderr)
        .with_ansi(ansi)
        .with_max_level(level)
        .with_target(false)
        .without_time()
        .init();
    let status = match execute(&cli) {
        Ok(status) => status,
        Err(error) => {
            tracing::error!(code = error.code.as_str(), "{}", error.message);
            error.exit_code()
        }
    };
    std::process::exit(status);
}
