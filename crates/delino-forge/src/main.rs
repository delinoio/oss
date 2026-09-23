use std::path::PathBuf;

use clap::{Parser, Subcommand};
use delino_forge::{
    mcp, preview,
    store::{Store, limited_read},
};
use forge_tree_doc::*;
use tokio_util::sync::CancellationToken;
use uuid::Uuid;
#[derive(Parser)]
#[command(
    name = "delino-forge",
    version,
    about = "Delino Forge MCP: validated local document creation and editing"
)]
struct Cli {
    #[arg(long, global = true)]
    state_dir: Option<PathBuf>,
    #[command(subcommand)]
    command: Command,
}
#[derive(Subcommand)]
enum Command {
    Schema,
    Capabilities,
    Asset {
        #[command(subcommand)]
        command: AssetCommand,
    },
    Create {
        input: PathBuf,
    },
    Open {
        input: PathBuf,
    },
    Inspect {
        document_id: Uuid,
        #[arg(long)]
        key: Option<String>,
        #[arg(long)]
        node_id: Option<Uuid>,
        #[arg(long, default_value_t = 2)]
        depth: usize,
    },
    Apply {
        input: PathBuf,
    },
    Export {
        document_id: Uuid,
        #[arg(long)]
        output: PathBuf,
        #[arg(long)]
        overwrite: bool,
    },
    Preview {
        document_id: Uuid,
        #[arg(long)]
        output: PathBuf,
    },
    Close {
        document_id: Uuid,
    },
    Mcp,
}
#[derive(Subcommand)]
enum AssetCommand {
    Add { input: PathBuf },
}
#[tokio::main]
async fn main() {
    tracing_subscriber::fmt()
        .json()
        .with_writer(std::io::stderr)
        .with_env_filter(
            tracing_subscriber::EnvFilter::try_from_default_env()
                .unwrap_or_else(|_| "delino_forge=info".into()),
        )
        .with_target(false)
        .init();
    let cli = Cli::parse();
    let cancel = CancellationToken::new();
    let signal = cancel.clone();
    tokio::spawn(async move {
        let _ = tokio::signal::ctrl_c().await;
        signal.cancel();
    });
    #[cfg(unix)]
    {
        let signal = cancel.clone();
        tokio::spawn(async move {
            if let Ok(mut stream) =
                tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
            {
                stream.recv().await;
                signal.cancel();
            }
        });
    }
    let is_mcp = matches!(cli.command, Command::Mcp);
    let result = execute(cli, cancel).await;
    match result {
        Ok(Some(value)) => println!("{}", value),
        Ok(None) => {}
        Err(error) => {
            tracing::error!(code=?error.code,stage="command","Forge command failed");
            if !is_mcp {
                println!("{}", serde_json::json!({"error":error}));
            }
            std::process::exit(if error.code == ErrorCode::Cancelled {
                130
            } else {
                1
            });
        }
    }
}
async fn execute(cli: Cli, cancel: CancellationToken) -> Result<Option<serde_json::Value>> {
    match cli.command {
        Command::Schema => return Ok(Some(schema())),
        Command::Capabilities => return Ok(Some(delino_forge::capabilities())),
        _ => {}
    }
    let store = Store::new(cli.state_dir, cancel)?;
    let started = std::time::Instant::now();
    let operation = match &cli.command {
        Command::Schema => "schema",
        Command::Capabilities => "capabilities",
        Command::Asset { .. } => "asset.add",
        Command::Create { .. } => "create",
        Command::Open { .. } => "open",
        Command::Inspect { .. } => "inspect",
        Command::Apply { .. } => "apply",
        Command::Export { .. } => "export",
        Command::Preview { .. } => "preview",
        Command::Close { .. } => "close",
        Command::Mcp => "mcp",
    };
    tracing::info!(operation, stage = "start", "Forge command started");
    let value = match cli.command {
        Command::Mcp => {
            mcp::serve(store).await?;
            return Ok(None);
        }
        Command::Preview {
            document_id,
            output,
        } => preview::preview(store, document_id, output).await?,
        command => tokio::task::spawn_blocking(move || -> Result<serde_json::Value> {
            match command {
                Command::Asset {
                    command: AssetCommand::Add { input },
                } => store.register_asset(&input),
                Command::Create { input } => store
                    .create(parse(&limited_read(&input, MAX_JSON_BYTES)?)?)
                    .map(|r| serde_json::json!(r)),
                Command::Open { input } => store.open(&input).map(|r| serde_json::json!(r)),
                Command::Inspect {
                    document_id,
                    key,
                    node_id,
                    depth,
                } => store.inspect(
                    document_id,
                    if key.is_some() || node_id.is_some() {
                        Some(Target { key, node_id })
                    } else {
                        None
                    },
                    depth,
                ),
                Command::Apply { input } => store
                    .apply(parse(&limited_read(&input, MAX_JSON_BYTES)?)?)
                    .map(|r| serde_json::json!(r)),
                Command::Export {
                    document_id,
                    output,
                    overwrite,
                } => store.export(document_id, &output, overwrite),
                Command::Close { document_id } => store.close(document_id),
                _ => error(ErrorCode::InvalidField, "", "Unsupported command"),
            }
        })
        .await
        .map_err(|_| Diagnostic::new(ErrorCode::Io, "", "Worker failed"))??,
    };
    tracing::info!(
        operation,
        stage = "command",
        elapsed_ms = started.elapsed().as_millis() as u64,
        "Forge command completed"
    );
    Ok(Some(value))
}
