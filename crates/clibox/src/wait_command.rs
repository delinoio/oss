use std::{
    io::{self, Write},
    sync::Arc,
};

use crate::{
    cli::Wait,
    probe,
    wait::{self, Code, Kind, Report},
};

pub fn execute(command: Wait) -> u8 {
    // The native-root loader currently has no API to ignore CA environment
    // overrides. Clear only those overrides before any worker threads exist;
    // remove this workaround when the loader exposes native-only loading.
    std::env::remove_var("SSL_CERT_FILE");
    std::env::remove_var("SSL_CERT_DIR");
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
        return 1;
    }
    report.exit_code
}
