use crate::{
    cli::BumpArgs,
    errors::{CargoMonoError, Result},
    types::OutputFormat,
    CargoMonoApp,
};

pub fn execute(_args: &BumpArgs, _output: OutputFormat, _app: &CargoMonoApp) -> Result<i32> {
    Err(CargoMonoError::internal(
        "bump command implementation is not initialized",
    ))
}
