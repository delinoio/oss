// SPDX-License-Identifier: Apache-2.0
//! Native process-ownership tests. This harness-less test binary first
//! dispatches the supervisor, like Go's TestMain, because `process::run`
//! re-executes the current executable as the supervisor.

fn main() {
    if let Some(code) =
        cachecow::process::supervisor_main(cachecow::process::SupervisorHooks::default())
    {
        std::process::exit(code);
    }
}
