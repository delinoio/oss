use tracing::info;

use crate::errors::{NodeupError, Result};

pub fn completions(shell: &str, command: Option<&str>) -> Result<i32> {
    let scope = command.unwrap_or("<all-commands>");
    info!(
        command_path = "nodeup.completions",
        action = "generate",
        shell,
        scope_present = command.is_some(),
        outcome = "not-implemented",
        "Completions generation command is not implemented yet"
    );
    Err(NodeupError::not_implemented(format!(
        "nodeup completions for shell '{shell}' and scope '{scope}' is planned for the next phase"
    )))
}
