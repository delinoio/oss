### Instructions

- Use the `@docs/` directory as the source of truth. You should list the files in the docs directory before starting any task, and update the documents as required. The `@docs/` directory should always be up-to-date.
- After completing each task, update the relevant documentation in `@docs/` to reflect any changes made.
- Write all code and comments in English.
- Prefer enum types over strings whenever possible.
- If you modified Rust code, run `cargo test` from the root directory before finishing your task.
- If you modified frontend code, run `pnpm test` from the frontend directory before finishing your task.
- Commit your work as frequent as possible using git. Do NOT use `--no-verify` flag.
- Do not guess; rather search for the web.
- Debug by logging. You should write enough logging code.
- Prioritize Connect RPC-based communication for business flows over Tauri-specific bindings.
