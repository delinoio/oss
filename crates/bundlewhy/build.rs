//! Embeds the built web UI document.
//!
//! Uses `BUNDLEWHY_UI_HTML` when set, otherwise
//! `apps/bundlewhy-web/dist/index.html` when it exists. Without a built UI the
//! binary embeds a minimal page that explains how to build it, so `cargo test`
//! works without a frontend build. Release builds must build the UI first.

use std::path::PathBuf;

const FALLBACK: &str = r#"<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>bundlewhy</title>
<!--bundlewhy:report-->
</head>
<body>
<main>
<h1>bundlewhy UI is not built</h1>
<p>This bundlewhy binary was compiled without the web UI. Results were saved; build the UI with <code>pnpm --filter bundlewhy-web build</code>, rebuild bundlewhy, then run <code>bundlewhy open &lt;run-dir&gt;</code>.</p>
</main>
</body>
</html>
"#;

fn main() {
    println!("cargo:rerun-if-env-changed=BUNDLEWHY_UI_HTML");
    let manifest_dir = PathBuf::from(std::env::var("CARGO_MANIFEST_DIR").expect("manifest dir"));
    let default_ui = manifest_dir.join("../../apps/bundlewhy-web/dist/index.html");
    println!("cargo:rerun-if-changed={}", default_ui.display());
    let source = std::env::var_os("BUNDLEWHY_UI_HTML").map_or(default_ui, PathBuf::from);
    let html = std::fs::read_to_string(&source).unwrap_or_else(|_| FALLBACK.to_owned());
    let out = PathBuf::from(std::env::var("OUT_DIR").expect("out dir")).join("bundlewhy-ui.html");
    std::fs::write(out, html).expect("write embedded UI");
}
