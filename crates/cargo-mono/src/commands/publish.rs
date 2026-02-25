use crate::{
    cli::PublishArgs,
    errors::{CargoMonoError, Result},
    types::OutputFormat,
    CargoMonoApp,
};

pub fn execute(_args: &PublishArgs, _output: OutputFormat, _app: &CargoMonoApp) -> Result<i32> {
    Err(CargoMonoError::internal(
        "publish command implementation is not initialized",
    ))
}
