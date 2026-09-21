use std::{path::PathBuf, process::ExitCode};

use clap::{Args, Parser, Subcommand, ValueEnum};
use runlens::{
    analysis, clean, config,
    error::{Error, ErrorCode, Result},
    execute::{self, Request},
    model::{Report, ReportKind, Role, Verdict},
    platform, report,
};
use tokio_util::sync::CancellationToken;

#[derive(Parser)]
#[command(name="runlens",version,about="Explain command filesystem dependencies, differences, and side effects.",long_about="Observe finite, noninteractive commands. Reports contain metadata only and are saved only with --save. Runlens is not a security sandbox or a universal cache-safety checker.",color=clap::ColorChoice::Never)]
struct Cli {
    #[arg(long, global = true)]
    config: Option<PathBuf>,
    #[arg(long, global = true, value_enum, default_value = "auto")]
    color: Color,
    #[arg(long, global = true, value_enum, default_value = "info")]
    log_level: LogLevel,
    #[command(subcommand)]
    command: Commands,
}
#[derive(Clone, Copy, ValueEnum)]
enum Color {
    Auto,
    Always,
    Never,
}
#[derive(Clone, Copy, ValueEnum)]
enum LogLevel {
    Off,
    Error,
    Warn,
    Info,
    Debug,
}
#[derive(Args)]
struct Output {
    #[arg(long)]
    json: bool,
}
#[derive(Args)]
struct Reports {
    #[arg(long = "report", required = true, num_args = 1)]
    reports: Vec<PathBuf>,
    #[command(flatten)]
    output: Output,
}
#[derive(Args)]
struct Run {
    #[arg(long, conflicts_with = "argv")]
    command: Option<String>,
    #[arg(last = true, required_unless_present = "command")]
    argv: Vec<String>,
    #[arg(long)]
    save: Option<PathBuf>,
    #[arg(long)]
    timeout_ms: Option<u64>,
}
#[derive(Subcommand)]
enum Commands {
    /// Observe an explicitly supplied argv or a configured command.
    Run(Run),
    /// Compare explicitly supplied reports; map right-hand paths with FROM=TO.
    Compare {
        left: PathBuf,
        right: PathBuf,
        #[arg(long = "map")]
        mappings: Vec<analysis::PathMap>,
        #[command(flatten)]
        output: Output,
    },
    /// Audit configured input and output declarations.
    Cache {
        #[command(subcommand)]
        command: CacheCommand,
    },
    /// Render access attempts separately from verified filesystem changes.
    Receipt {
        report: PathBuf,
        #[command(flatten)]
        output: Output,
    },
    /// Execute a configured command in fresh temporary environments.
    Verify {
        #[command(subcommand)]
        command: VerifyCommand,
    },
    /// Check configured policies without executing commands.
    Policy {
        #[command(subcommand)]
        command: PolicyCommand,
    },
    /// Find observed file usage in explicitly supplied reports.
    Explain {
        path: String,
        #[command(flatten)]
        reports: Reports,
    },
    /// Identify potential conflicts; never execute the supplied commands.
    Conflicts {
        #[command(flatten)]
        reports: Reports,
    },
    /// Explicitly export JSON or a self-contained offline HTML document.
    Export {
        report: PathBuf,
        #[arg(long, value_enum)]
        format: Format,
        #[arg(long)]
        output: PathBuf,
    },
    /// Diagnose host and tracing prerequisites without running a target.
    Doctor {
        #[command(flatten)]
        output: Output,
    },
}
#[derive(Subcommand)]
enum CacheCommand {
    Check {
        report: PathBuf,
        #[arg(long)]
        command: String,
        #[command(flatten)]
        output: Output,
    },
}
#[derive(Subcommand)]
enum PolicyCommand {
    Check {
        report: PathBuf,
        #[arg(long)]
        baseline: Option<PathBuf>,
        #[command(flatten)]
        output: Output,
    },
}
#[derive(Subcommand)]
enum VerifyCommand {
    Clean {
        name: String,
        #[arg(long)]
        include_working_tree: bool,
        #[arg(long)]
        baseline: Option<PathBuf>,
        #[arg(long)]
        save: Option<PathBuf>,
    },
    Repeat {
        name: String,
        #[arg(long, default_value_t = 3)]
        runs: u32,
        #[arg(long)]
        include_working_tree: bool,
        #[arg(long)]
        save: Option<PathBuf>,
    },
}
#[derive(Clone, Copy, ValueEnum)]
enum Format {
    Json,
    Html,
}
#[tokio::main]
async fn main() -> ExitCode {
    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(error) => {
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) {
                let _ = error.print();
                return ExitCode::SUCCESS;
            }
            // Clap's detailed error can echo arbitrary argv, including secrets.
            eprintln!("Invalid arguments. Run runlens --help for supported usage.");
            return ExitCode::from(2);
        }
    };
    use tracing_subscriber::{Layer, layer::SubscriberExt, util::SubscriberInitExt};
    let level = match cli.log_level {
        LogLevel::Off => tracing::level_filters::LevelFilter::OFF,
        LogLevel::Error => tracing::level_filters::LevelFilter::ERROR,
        LogLevel::Warn => tracing::level_filters::LevelFilter::WARN,
        LogLevel::Info => tracing::level_filters::LevelFilter::INFO,
        LogLevel::Debug => tracing::level_filters::LevelFilter::DEBUG,
    };
    // Only audited Runlens events may reach its diagnostic stream. Ambient
    // RUST_LOG must not enable upstream raw-path or environment diagnostics.
    let filter = tracing_subscriber::filter::Targets::new().with_target("runlens", level);
    use std::io::IsTerminal;
    let ansi = match cli.color {
        Color::Auto => std::io::stderr().is_terminal(),
        Color::Always => true,
        Color::Never => false,
    };
    let _ = tracing_subscriber::registry()
        .with(
            tracing_subscriber::fmt::layer()
                .with_ansi(ansi)
                .with_writer(std::io::stderr)
                .with_filter(filter),
        )
        .try_init();
    let cancel = CancellationToken::new();
    let signal_cancel = cancel.clone();
    // Install handlers before any snapshot or child work can occupy the runtime.
    #[cfg(unix)]
    let signals = (
        tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate()),
        tokio::signal::unix::signal(tokio::signal::unix::SignalKind::interrupt()),
    );
    #[cfg(unix)]
    let (mut terminate, mut interrupt) = match signals {
        (Ok(terminate), Ok(interrupt)) => (terminate, interrupt),
        _ => {
            eprintln!("Runlens could not initialize cancellation handlers.");
            return ExitCode::from(ErrorCode::Internal.exit_code() as u8);
        }
    };
    #[cfg(windows)]
    let (mut interrupt, mut terminate) = match (
        tokio::signal::windows::ctrl_c(),
        tokio::signal::windows::ctrl_break(),
    ) {
        (Ok(interrupt), Ok(terminate)) => (interrupt, terminate),
        _ => {
            eprintln!("Runlens could not initialize cancellation handlers.");
            return ExitCode::from(ErrorCode::Internal.exit_code() as u8);
        }
    };
    let signal = tokio::spawn(async move {
        #[cfg(unix)]
        {
            tokio::select! {_=interrupt.recv()=>{},_=terminate.recv()=>{}}
        }
        #[cfg(windows)]
        {
            tokio::select! {_=interrupt.recv()=>{},_=terminate.recv()=>{}}
        }
        signal_cancel.cancel();
    });
    let mut result = run(cli, cancel).await;
    if runlens::temporary::cleanup_failed() {
        result = Err(Error::new(
            ErrorCode::CleanupFailed,
            "private temporary metadata cleanup failed",
        ));
    }
    signal.abort();
    match result {
        Ok(code) => ExitCode::from(code as u8),
        Err(error) => {
            tracing::error!(code=?error.code,"Runlens operation failed");
            eprintln!("{}", error.message);
            ExitCode::from(error.code.exit_code() as u8)
        }
    }
}
async fn run(cli: Cli, cancel: CancellationToken) -> Result<i32> {
    let cwd = std::env::current_dir()
        .map_err(|_| Error::input("current directory is unavailable"))?
        .canonicalize()
        .map_err(|_| Error::input("current directory is unavailable"))?;
    let root = clean::workspace_root(&cwd);
    match cli.command {
        Commands::Run(args) => {
            if let Some(path) = &args.save {
                report::destination_available(path)?;
            }
            let config = load_config(cli.config.as_deref(), &root)?;
            let mut command = match &args.command {
                Some(name) => config
                    .commands
                    .get(name)
                    .cloned()
                    .ok_or_else(|| Error::input("configured command was not found"))?,
                None => {
                    let mut command = config::Command::direct(args.argv);
                    command.cwd = cwd.strip_prefix(&root).unwrap_or(&cwd).into();
                    command
                }
            };
            if let Some(ms) = args.timeout_ms {
                if ms == 0 {
                    return Err(Error::input("timeout must be positive"));
                }
                command.timeout_ms = Some(ms);
            }
            let execution = execute::observe(Request {
                root: &root,
                command: &command,
                name: args.command.as_deref(),
                config: &config,
                environment: std::env::vars_os().collect(),
                temporary: vec![],
                revision: clean::revision(&root, &cancel).await,
                working_tree_included: true,
                role: Role::Target,
                repetition: 1,
                cancellation: cancel,
            })
            .await?;
            let mut report = Report::new(ReportKind::Run);
            report.executions.push(execution);
            finish_execution(&report, args.save.as_deref())
        }
        Commands::Doctor { output } => {
            let doctor = platform::doctor();
            if output.json {
                report::json(&doctor)?;
            } else {
                println!("Runlens doctor: {} {}", doctor.os, doctor.architecture);
                for check in &doctor.checks {
                    println!(
                        "{}: {}. {}",
                        check.name,
                        if check.passed {
                            "available"
                        } else {
                            "unavailable"
                        },
                        check.guidance
                    );
                }
            }
            Ok(if doctor.supported { 0 } else { 3 })
        }
        Commands::Compare {
            left,
            right,
            mappings,
            output,
        } => print_analysis(
            analysis::compare(&report::read(&left)?, &report::read(&right)?, &mappings)?,
            output,
            false,
        ),
        Commands::Receipt {
            report: path,
            output,
        } => print_analysis(analysis::receipt(&report::read(&path)?)?, output, false),
        Commands::Cache {
            command:
                CacheCommand::Check {
                    report: path,
                    command: name,
                    output,
                },
        } => {
            let config = load_config(cli.config.as_deref(), &root)?;
            let command = config
                .commands
                .get(&name)
                .ok_or_else(|| Error::input("configured command was not found"))?;
            let identities = runlens::privacy::command_identities(&config, &root, &[])?;
            let redactor = runlens::privacy::Redactor::new(&root, &[], &config.redaction)?;
            let expected = &identities[&redactor.text(&name)];
            print_analysis(
                analysis::cache(&report::read(&path)?, command, expected)?,
                output,
                true,
            )
        }
        Commands::Policy {
            command:
                PolicyCommand::Check {
                    report: path,
                    baseline,
                    output,
                },
        } => {
            let config = load_config(cli.config.as_deref(), &root)?;
            let baseline = baseline.map(|path| report::read(&path)).transpose()?;
            print_analysis(
                analysis::policy(
                    &report::read(&path)?,
                    &config.policy,
                    &config.commands,
                    &runlens::privacy::command_identities(&config, &root, &[])?,
                    baseline.as_ref(),
                )?,
                output,
                true,
            )
        }
        Commands::Explain { path, reports } => print_analysis(
            analysis::explain(&path, &read_reports(&reports.reports)?)?,
            reports.output,
            false,
        ),
        Commands::Conflicts { reports } => {
            if reports.reports.len() < 2 {
                return Err(Error::input(
                    "conflict analysis requires at least two reports",
                ));
            }
            print_analysis(
                analysis::conflicts(&read_reports(&reports.reports)?)?,
                reports.output,
                false,
            )
        }
        Commands::Export {
            report: path,
            format,
            output,
        } => {
            report::save(
                &output,
                &report::read(&path)?,
                matches!(format, Format::Html),
            )?;
            eprintln!("Report exported.");
            Ok(0)
        }
        Commands::Verify { command } => {
            let (name, runs, include, baseline, save) = match command {
                VerifyCommand::Clean {
                    name,
                    include_working_tree,
                    baseline,
                    save,
                } => (name, 1, include_working_tree, baseline, save),
                VerifyCommand::Repeat {
                    name,
                    runs,
                    include_working_tree,
                    save,
                } => {
                    if runs < 2 {
                        return Err(Error::input(
                            "repeat verification requires at least two runs",
                        ));
                    }
                    (name, runs, include_working_tree, None, save)
                }
            };
            if let Some(path) = &save {
                report::destination_available(path)?;
            }
            let config = load_config(cli.config.as_deref(), &root)?;
            let baseline = baseline.map(|path| report::read(&path)).transpose()?;
            let report = clean::verify(
                &root,
                &name,
                &config,
                runs,
                include,
                baseline.as_ref(),
                cancel,
            )
            .await?;
            finish_execution(&report, save.as_deref())
        }
    }
}
fn load_config(path: Option<&std::path::Path>, root: &std::path::Path) -> Result<config::Config> {
    let config = config::load(path, root)?;
    // Establish the shared limit before any supplied evidence is deserialized.
    // Lowering it only when a child starts leaves retained baseline maps above
    // the requested threshold and misses entirely offline configured commands.
    runlens::entries::set_memory_limit(config.limits.memory_bytes);
    Ok(config)
}

fn read_reports(paths: &[PathBuf]) -> Result<Vec<Report>> {
    if paths.len() > 64 {
        return Err(Error::input("at most 64 reports may be queried at once"));
    }
    let mut bytes = 0u64;
    for path in paths {
        bytes = bytes.saturating_add(
            std::fs::metadata(path)
                .map_err(|_| Error::input("report cannot be opened"))?
                .len(),
        );
        if bytes > report::MAX_REPORT_BYTES {
            return Err(Error::input("combined report inputs exceed 1 GiB"));
        }
    }
    paths.iter().map(|path| report::read(path)).collect()
}
fn finish_execution(value: &Report, save: Option<&std::path::Path>) -> Result<i32> {
    if let Some(path) = save {
        report::save(path, value, false)?;
    }
    let mut code = 0;
    for execution in value.current_executions() {
        eprintln!(
            "Execution {}: child={:?}, collection={}, accesses={}, changes={}, errors={:?}",
            execution.id,
            execution.outcome.child_exit_code,
            if execution.outcome.collection_complete {
                "complete within coverage"
            } else {
                "incomplete"
            },
            execution.accesses.len(),
            execution.changes.len(),
            execution.outcome.errors
        );
        for error in &execution.outcome.errors {
            code = code.max(error.exit_code());
        }
    }
    if let Some(verdict) = value.verification {
        eprintln!("Verification: {verdict:?}");
        if verdict != Verdict::Passed {
            code = code.max(if verdict == Verdict::Inconclusive {
                4
            } else {
                5
            });
        }
    }
    if save.is_none() {
        eprintln!("No report retained. Use --save <path> to save metadata.");
    }
    Ok(code)
}
fn print_analysis(result: analysis::Analysis, output: Output, check: bool) -> Result<i32> {
    if output.json {
        report::json(&result)?;
    } else {
        println!(
            "{:?}: {} findings, {} state differences, {} observed usages",
            result.kind,
            result.findings.len(),
            result.differences.len(),
            result.usages.len()
        );
        if let Some(verdict) = result.verdict {
            println!("Check outcome: {verdict:?}");
        }
        for item in result.findings.iter() {
            let (_, finding) = item?;
            println!("{:?}: {:?}", finding.classification, finding.code);
            for reference in finding.evidence {
                println!(
                    "  {} {:?} {}",
                    reference.execution_id,
                    reference.source,
                    reference.path.as_deref().unwrap_or("command outcome")
                );
            }
        }
        for item in result.differences.iter() {
            let (_, difference) = item?;
            println!("{:?}: {}", difference.change, difference.path);
        }
        for item in result.environment_differences.iter() {
            let (id, _) = item?;
            println!(
                "Environment metadata differs for execution {id}; use --json for exact non-secret \
                 fields."
            );
        }
        for item in result.usages.iter() {
            let (path, usage) = item?;
            println!(
                "{}: access={:?}, change={:?}, producer candidate={}, consumer candidate={}",
                path,
                usage.access,
                usage.change,
                usage.producer_candidate,
                usage.consumer_candidate
            );
        }
        for limitation in &result.limitations {
            println!("{limitation}");
        }
    }
    Ok(
        if result.verdict == Some(Verdict::Inconclusive)
            || check && result.verdict != Some(Verdict::Passed)
        {
            if result.verdict == Some(Verdict::Inconclusive) {
                4
            } else {
                5
            }
        } else {
            0
        },
    )
}
