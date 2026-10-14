//! bundlewhy command-line entry point.

use std::{io::Write, path::PathBuf, process::ExitCode};

use bundlewhy::{
    engine::{self, BundlerChoice},
    error::{BwError, ErrorCode},
    model::RunStatus,
    run::{self, AnalyzeOptions},
};
use clap::{Parser, Subcommand};

#[derive(Debug, Parser)]
#[command(
    name = "bundlewhy",
    version,
    about = "Explain why your bundler could not apply an optimization",
    after_help = "Analyzes trusted local projects only: project configuration and plugins run \
                  with your permissions."
)]
struct Cli {
    /// Disable ANSI colors in logs (also honors NO_COLOR).
    #[arg(long, global = true)]
    no_color: bool,
    /// Show debug logs.
    #[arg(long, global = true)]
    verbose: bool,
    #[command(subcommand)]
    command: Command,
}

#[derive(Debug, Subcommand)]
enum Command {
    /// Collect one diagnostic production build and explore the results.
    Analyze {
        /// Directory that receives the new run directory.
        #[arg(long)]
        out_dir: PathBuf,
        /// Project directory (defaults to the current directory).
        #[arg(long)]
        project: Option<PathBuf>,
        /// Bundler configuration file.
        #[arg(long)]
        config: Option<PathBuf>,
        /// Build name when an Rspack configuration exports several builds.
        #[arg(long)]
        config_name: Option<String>,
        /// Bundler to analyze when several are candidates.
        #[arg(long, value_enum)]
        bundler: Option<BundlerChoice>,
        /// Analyze with an installed engine outside the compatibility matrix.
        #[arg(long)]
        allow_unsupported_engine: bool,
        /// Do not open a browser.
        #[arg(long)]
        no_open: bool,
        /// Save results and exit without starting the local server.
        #[arg(long)]
        no_serve: bool,
        /// Store hashes and paths but not source content.
        #[arg(long)]
        exclude_sources: bool,
    },
    /// Open a saved analysis.
    Open {
        /// Run directory created by `bundlewhy analyze`.
        run_dir: PathBuf,
        /// Do not open a browser.
        #[arg(long)]
        no_open: bool,
    },
    /// Write a self-contained interactive HTML report.
    Export {
        /// Run directory created by `bundlewhy analyze`.
        run_dir: PathBuf,
        /// Destination HTML file.
        #[arg(long)]
        output: PathBuf,
        /// Omit source content from the report.
        #[arg(long)]
        exclude_sources: bool,
        /// Replace an existing output file.
        #[arg(long)]
        force: bool,
    },
    /// Print the frozen engine compatibility matrix.
    Engines,
}

fn init_tracing(no_color: bool, verbose: bool) {
    let color = !no_color && std::env::var_os("NO_COLOR").is_none();
    let default_level = if verbose {
        "bundlewhy=debug"
    } else {
        "bundlewhy=info"
    };
    let filter = tracing_subscriber::EnvFilter::try_from_env("BUNDLEWHY_LOG")
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new(default_level));
    tracing_subscriber::fmt()
        .with_env_filter(filter)
        .with_ansi(color)
        .with_writer(std::io::stderr)
        .with_target(false)
        .init();
}

fn report(error: &BwError) -> ExitCode {
    let _ = writeln!(
        std::io::stderr(),
        "error[{}]: {}\n  recovery: {}",
        error.code,
        error.message,
        error.recovery
    );
    ExitCode::from(u8::try_from(error.code.exit_code()).unwrap_or(1))
}

fn main() -> ExitCode {
    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(error) => {
            let _ = error.print();
            return if error.use_stderr() {
                ExitCode::from(2)
            } else {
                ExitCode::SUCCESS
            };
        }
    };
    init_tracing(cli.no_color, cli.verbose);
    let runtime = match tokio::runtime::Builder::new_multi_thread()
        .enable_all()
        .build()
    {
        Ok(runtime) => runtime,
        Err(error) => {
            return report(&BwError::new(
                ErrorCode::Usage,
                format!("Could not start the runtime: {error}"),
                "Retry the command.",
            ));
        }
    };
    let result = runtime.block_on(async {
        match cli.command {
            Command::Analyze {
                out_dir,
                project,
                config,
                config_name,
                bundler,
                allow_unsupported_engine,
                no_open,
                no_serve,
                exclude_sources,
            } => {
                let outcome = run::analyze(AnalyzeOptions {
                    out_dir,
                    project,
                    config,
                    config_name,
                    bundler,
                    allow_unsupported_engine,
                    no_open,
                    no_serve,
                    exclude_sources,
                })
                .await?;
                match (outcome.status, outcome.error) {
                    (RunStatus::Complete | RunStatus::Partial, None) => Ok(ExitCode::SUCCESS),
                    (_, Some(error)) => Ok(report(&error)),
                    (RunStatus::Cancelled, None) => Ok(ExitCode::from(130)),
                    _ => Ok(ExitCode::from(1)),
                }
            }
            Command::Open { run_dir, no_open } => run::open(&run_dir, no_open)
                .await
                .map(|()| ExitCode::SUCCESS),
            Command::Export {
                run_dir,
                output,
                exclude_sources,
                force,
            } => run::export_report(&run_dir, &output, exclude_sources, force)
                .map(|()| ExitCode::SUCCESS),
            Command::Engines => {
                let _ = write!(std::io::stdout(), "{}", engine::matrix_text());
                Ok(ExitCode::SUCCESS)
            }
        }
    });
    match result {
        Ok(code) => code,
        Err(error) => report(&error),
    }
}
