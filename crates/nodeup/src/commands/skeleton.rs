use tracing::info;

use crate::{
    cli::SelfCommand,
    errors::{NodeupError, Result},
};

pub fn self_command(command: SelfCommand) -> Result<i32> {
    let action = match command {
        SelfCommand::Update => "self update",
        SelfCommand::Uninstall => "self uninstall",
        SelfCommand::UpgradeData => "self upgrade-data",
    };

    info!(
        command_path = "nodeup.self",
        action,
        outcome = "not-implemented",
        "Self-management command is not implemented yet"
    );

    Err(NodeupError::not_implemented(format!(
        "nodeup {action} is planned for the next phase"
    )))
}
