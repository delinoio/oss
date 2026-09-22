use clap::Parser;

#[tokio::main]
async fn main() {
    let cli = taskflow::cli::Cli::parse();
    let json = cli.json;
    let color = !cli.no_color && std::env::var_os("NO_COLOR").is_none();
    tracing_subscriber::fmt()
        .with_writer(std::io::stderr)
        .with_ansi(color)
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "taskflow=info".into()),
        )
        .init();
    let cancel = tokio_util::sync::CancellationToken::new();
    let signal = cancel.clone();
    let signal_task = tokio::spawn(async move {
        #[cfg(unix)]
        {
            let mut term =
                tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
                    .expect("install SIGTERM handler");
            tokio::select! { _ = tokio::signal::ctrl_c() => {}, _ = term.recv() => {} }
        }
        #[cfg(not(unix))]
        {
            let _ = tokio::signal::ctrl_c().await;
        }
        signal.cancel();
    });
    let result = taskflow::process::CANCELLATION
        .scope(cancel.clone(), taskflow::cli::run(cli, cancel.clone()))
        .await;
    signal_task.abort();
    let code = match result {
        Ok(code) => code,
        Err(error) => {
            if json {
                eprintln!(
                    "{}",
                    serde_json::json!({"version":1,"error":{"message":error.to_string()}})
                );
            } else {
                eprintln!("tflow: {error}");
            }
            taskflow::process::error_exit_code(&error)
        }
    };
    std::process::exit(code);
}

#[cfg(test)]
mod tests {
    #[test]
    fn cancellation_identity_survives_context_without_reclassifying_other_errors() {
        let cancelled =
            anyhow::Error::new(taskflow::process::Cancelled).context("native metadata unavailable");
        assert!(cancelled.is::<taskflow::process::Cancelled>());
        assert!(!anyhow::anyhow!("real configuration failure").is::<taskflow::process::Cancelled>());
    }
}
