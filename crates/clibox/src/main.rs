mod cli;
mod probe;
mod wait;

use std::{
    io::{self, IsTerminal, Write},
    process::ExitCode,
    sync::Arc,
};

use clap::{CommandFactory, Parser};
use cli::{Cli, Command, Wait};
use tracing_subscriber::{layer::SubscriberExt, util::SubscriberInitExt, Layer};
use wait::{Code, Kind, Report};

fn main() -> ExitCode {
    let cli = match Cli::try_parse() {
        Ok(cli) => cli,
        Err(error)
            if matches!(
                error.kind(),
                clap::error::ErrorKind::DisplayHelp | clap::error::ErrorKind::DisplayVersion
            ) =>
        {
            return if error.print().is_ok() {
                ExitCode::SUCCESS
            } else {
                ExitCode::FAILURE
            };
        }
        Err(error) => {
            eprintln!("error: {}", cli::parser_message(error.kind()));
            return ExitCode::from(2);
        }
    };
    let Some(Command::Wait(command)) = cli.command else {
        let result = Cli::command()
            .print_help()
            .and_then(|_| writeln!(io::stdout()));
        return if result.is_ok() {
            ExitCode::SUCCESS
        } else {
            ExitCode::FAILURE
        };
    };
    // The native-root loader currently has no API to ignore CA environment
    // overrides. Clear only those overrides before any worker threads exist;
    // remove this workaround when the loader exposes native-only loading.
    std::env::remove_var("SSL_CERT_FILE");
    std::env::remove_var("SSL_CERT_DIR");
    let filter = tracing_subscriber::EnvFilter::builder()
        .with_regex(false)
        .with_default_directive(tracing::level_filters::LevelFilter::WARN.into())
        .try_from_env()
        .unwrap_or_else(|_| tracing_subscriber::EnvFilter::new("warn"));
    tracing_subscriber::registry()
        .with(
            tracing_subscriber::fmt::layer()
                .with_writer(io::stderr)
                .with_ansi(io::stderr().is_terminal() && std::env::var_os("NO_COLOR").is_none())
                // Dependency diagnostics can contain URLs, DNS names, or TLS details.
                // Never enable them, even with RUST_LOG=trace or dependency directives.
                .with_filter(tracing_subscriber::filter::filter_fn(|meta| {
                    meta.target().starts_with("clibox")
                }))
                .with_filter(filter),
        )
        .init();

    let (kind, options, attempt_timeout, target) = match command {
        Wait::Tcp {
            target,
            options,
            network,
        } => (
            Kind::Tcp,
            options,
            Some(network.attempt_timeout),
            probe::Target::Tcp(target),
        ),
        Wait::Http {
            target,
            method,
            status,
            options,
            network,
        } => (
            Kind::Http,
            options,
            Some(network.attempt_timeout),
            probe::Target::Http {
                url: target,
                method,
                status,
                client: tokio::sync::OnceCell::new(),
            },
        ),
        Wait::File { target, options } => (Kind::File, options, None, probe::Target::File(target)),
    };
    let runtime = tokio::runtime::Builder::new_current_thread()
        .enable_all()
        .build();
    let report = match runtime {
        Ok(runtime) => {
            let target = Arc::new(target);
            let report = runtime.block_on(async {
                match wait::Signals::install() {
                    Ok(signals) => {
                        wait::run(
                            kind,
                            &options,
                            attempt_timeout,
                            || {
                                let target = target.clone();
                                async move { target.check().await }
                            },
                            signals.cancelled(),
                        )
                        .await
                    }
                    Err(code) => Report::failure(kind, code),
                }
            });
            // Metadata and OS trust calls are read-only blocking OS operations;
            // they cannot hold process exit hostage after a handled deadline.
            runtime.shutdown_background();
            report
        }
        Err(_) => Report::failure(kind, Code::RuntimeInitialization),
    };
    if let Some(error) = &report.error {
        eprintln!("error: {}: {}", error.code, error.message);
        tracing::warn!(%kind, code = %error.code, attempts = report.attempts, elapsed_ms = report.elapsed_ms, "wait_finished");
    }
    let output = if options.json {
        serde_json::to_writer(io::stdout().lock(), &report)
            .map_err(io::Error::other)
            .and_then(|_| writeln!(io::stdout()))
    } else if !options.quiet && report.error.is_none() {
        writeln!(
            io::stdout(),
            "{} ready after {} ms ({} attempts).",
            kind,
            report.elapsed_ms,
            report.attempts
        )
    } else {
        Ok(())
    };
    if output.is_err() {
        eprintln!(
            "error: output_failed: Cannot write the final result; check stdout availability."
        );
        return ExitCode::FAILURE;
    }
    ExitCode::from(report.exit_code)
}
