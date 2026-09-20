use clap::{CommandFactory, Parser};

#[derive(Debug, Parser)]
#[command(
    name = "clibox",
    version,
    about = "A native CLI distributed through Cargo and npm"
)]
struct Cli {}

fn main() -> std::io::Result<()> {
    Cli::parse();
    Cli::command().print_help()?;
    println!();
    Ok(())
}
