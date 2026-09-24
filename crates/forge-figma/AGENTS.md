# Figma planner

- Follow `docs/packages-react-forge-figma-contract.md`. This crate owns only bounded model validation, dependency ordering, diff and page/code/result-sized batch plans.
- Keep credentials, networking, sleeps, JavaScript execution and remote recovery in the Node adapter/session layer. Do not add a Figma REST mutation dependency.
- Only explicit selected bindings and managed children authorize changes. Reject unsupported replacement, nested pages and unprovable preservation before publishing a plan.
- Keep error values stable and content-free. Test planning boundaries with synthetic IDs and data; run root `cargo test` for Rust changes.
