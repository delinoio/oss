use binpm::{
    cli::{Cli, Command},
    error::{set_frozen_lockfile_context, BinpmError, FrozenLockfileCommandContext},
    logging, run_cli,
};
use swc_malloc as _;

fn main() {
    let parse_json = Cli::json_requested(std::env::args_os());
    let cli = match Cli::try_parse_args() {
        Ok(cli) => cli,
        Err(error) => exit_with_parse_error(error, parse_json),
    };
    let json = cli.json;
    if !json {
        logging::init_logging(cli.log_verbosity());
    }
    set_frozen_lockfile_context(frozen_lockfile_command_context(&cli.command));

    match run_cli(cli) {
        Ok(code) => std::process::exit(code),
        Err(error) => exit_with_error(error, json),
    }
}

fn frozen_lockfile_command_context(command: &Command) -> FrozenLockfileCommandContext {
    match command {
        Command::Add(args) => FrozenLockfileCommandContext::Add {
            cmd: args.cmd.clone(),
            source: args.source.clone(),
            bin: args.bin.clone(),
            require_verified: args.require_verified,
            mode: args.lockfile.frozen_lockfile_mode(),
        },
        Command::Install(args) if args.scope.local && args.source.is_some() => {
            FrozenLockfileCommandContext::InstallLocalSource {
                source: args
                    .source
                    .clone()
                    .expect("checked source is present for local source install"),
                require_verified: args.require_verified,
                mode: args.lockfile.frozen_lockfile_mode(),
            }
        }
        Command::Install(args) => FrozenLockfileCommandContext::Other {
            mode: args.lockfile.frozen_lockfile_mode(),
        },
        Command::Exec(args) => FrozenLockfileCommandContext::Exec {
            mode: args.lockfile.frozen_lockfile_mode(),
        },
        Command::Update(args) => FrozenLockfileCommandContext::Other {
            mode: args.lockfile.frozen_lockfile_mode(),
        },
        _ => FrozenLockfileCommandContext::NotFrozen,
    }
}

fn exit_with_parse_error(error: clap::Error, json: bool) -> ! {
    if json {
        let exit_code = error.exit_code();
        let payload = serde_json::json!({
            "error": {
                "message": error.to_string(),
                "exit_code": exit_code,
            }
        });
        eprintln!("{payload}");
        std::process::exit(exit_code);
    }
    error.exit();
}

fn exit_with_error(error: BinpmError, json: bool) -> ! {
    let exit_code = error.exit_code();
    if json {
        let mut payload = serde_json::json!({
            "error": {
                "message": error.to_string(),
                "exit_code": exit_code,
            }
        });
        if let Some(diagnostic) = error.structured_diagnostic() {
            payload["error"]["diagnostic"] = diagnostic;
        }
        eprintln!("{payload}");
    } else {
        eprintln!("binpm error: {error}");
        if error.suggest_verbose_diagnostics() {
            eprintln!("hint: rerun with `--verbose` or `--debug` for structured diagnostics.");
        }
    }
    std::process::exit(exit_code);
}
