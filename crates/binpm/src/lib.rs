pub mod cli;
pub mod commands;
pub mod contract;
pub mod error;
pub mod logging;

use cli::Cli;
use error::Result;

pub fn run_cli(cli: Cli) -> Result<i32> {
    commands::run(cli)
}
