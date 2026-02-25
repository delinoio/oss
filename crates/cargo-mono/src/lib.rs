pub mod cli;
pub mod commands;
pub mod errors;
pub mod git;
pub mod logging;
pub mod types;
pub mod versioning;
pub mod workspace;

use errors::Result;
use workspace::Workspace;

#[derive(Debug, Clone)]
pub struct CargoMonoApp {
    pub workspace: Workspace,
}

impl CargoMonoApp {
    pub fn new() -> Result<Self> {
        let workspace = Workspace::load()?;
        Ok(Self { workspace })
    }
}
