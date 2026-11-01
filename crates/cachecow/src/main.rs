// SPDX-License-Identifier: Apache-2.0
use cachecow::{cli, process, session};

fn main() {
    // The native supervisor re-executes this binary. Dispatch it before any
    // normal argument parsing or worker creation.
    if let Some(code) = process::supervisor_main(session::hooks(cli::validate, cli::records())) {
        std::process::exit(code);
    }
    std::process::exit(cli::main(cachecow::VERSION));
}
