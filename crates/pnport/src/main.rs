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
mod supervisor;

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
            let graph = load_graph(cli, &cwd)?;
            graph.check_conflicts()?;
            supervisor::platform()?;
            let artifact = supervisor::artifact()?;
            let cache = Cache::open(cache_path(cli, &cwd)?)?;
            let session = tempfile::Builder::new()
                .prefix(&format!("pnport-{}-", uuid::Uuid::now_v7()))
                .tempdir()
                .map_err(|_| cache_error())?;
            #[cfg(unix)]
            {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(session.path(), fs::Permissions::from_mode(0o700))
                    .map_err(|_| cache_error())?;
            }
            cache::private_dir(session.path())?;
            let bytes = serde_json::to_vec(&graph.snapshot).map_err(|_| cache_error())?;
            fs::write(session.path().join("graph.json"), bytes).map_err(|_| cache_error())?;
            let mut view = View::new(graph, cache, session.path().to_owned());
            let executable = resolve_command(&mut view, &cwd, &command[0])?;
            supervisor::run(&mut view, &artifact, &executable, &command[1..])
        }
    }
}

fn resolve_command(view: &mut View, cwd: &Path, command: &OsString) -> Result<PathBuf> {
    let path = Path::new(command);
    if path.components().count() > 1 || path.is_absolute() {
        return Ok(cwd.join(path));
    }
    let Some(name) = command.to_str() else {
        return Err(Error::new(
            Code::PnportCommandNotFound,
            "The executable was not found.",
        ));
    };
    let dependencies: Vec<_> = view
        .graph
        .package(cwd)
        .map(|pkg| pkg.package_dependencies.keys().cloned().collect())
        .unwrap_or_default();
    let mut bins = std::collections::BTreeSet::new();
    for dependency in dependencies {
        let Ok(package) = view.graph.resolve(&dependency, cwd) else {
            continue;
        };
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

fn main() {
    #[cfg(target_os = "linux")]
    linux::dispatch_helper();
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
