// SPDX-License-Identifier: Apache-2.0
//! Embeds the release identity verified by `packages/ccow/scripts/package.mjs`.

fn main() {
    for key in ["CCOW_VERSION", "CCOW_BUILD_REVISION", "CCOW_BUILD_DIRTY"] {
        println!("cargo:rerun-if-env-changed={key}");
    }
    let version = std::env::var("CCOW_VERSION")
        .unwrap_or_else(|_| std::env::var("CARGO_PKG_VERSION").unwrap_or_default());
    let revision = std::env::var("CCOW_BUILD_REVISION").unwrap_or_default();
    let dirty = std::env::var("CCOW_BUILD_DIRTY").map_or(true, |v| v != "false");
    let target = std::env::var("TARGET").unwrap_or_default();
    let quote = |s: &str| {
        let mut out = String::from("\"");
        for c in s.chars() {
            match c {
                '"' => out.push_str("\\\""),
                '\\' => out.push_str("\\\\"),
                c if c.is_control() => out.push_str(&format!("\\u{:04x}", u32::from(c))),
                c => out.push(c),
            }
        }
        out.push('"');
        out
    };
    let info = format!(
        "ccow-build-info:{{\"version\":{},\"revision\":{},\"target\":{},\"dirty\":{dirty}}}",
        quote(&version),
        quote(&revision),
        quote(&target),
    );
    println!("cargo:rustc-env=CCOW_BUILD_INFO={info}");
}
