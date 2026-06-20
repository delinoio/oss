pub mod assets;
pub mod cli;
pub mod commands;
pub mod contract;
pub mod error;
pub mod logging;
pub mod release;

use cli::Cli;
use error::Result;

pub fn run_cli(cli: Cli) -> Result<i32> {
    commands::run(cli)
}
