// SPDX-License-Identifier: Apache-2.0
//! Shim command classification.

use super::{Classification, Mode, Reason, Tool};

const fn pass(reason: Reason) -> Classification {
    Classification {
        mode: Mode::Passthrough,
        reason,
    }
}

const fn managed() -> Classification {
    Classification {
        mode: Mode::Managed,
        reason: Reason::CacheOperation,
    }
}

pub fn classify(tool: Tool, args: &[String], in_git: bool, bypass: &str) -> Classification {
    if bypass == "1" {
        return pass(Reason::Bypass);
    }
    if !in_git {
        return pass(Reason::OutsideGit);
    }
    // Help/version flags after -- belong to the executed program, not the tool.
    for arg in args {
        if arg == "--" {
            break;
        }
        if matches!(arg.as_str(), "--help" | "-h" | "--version" | "-V") {
            return pass(Reason::Query);
        }
    }
    let mut i = 0;
    if tool == Tool::Cargo && args.first().is_some_and(|a| a.starts_with('+')) {
        i += 1;
    }
    while i < args.len() && args[i].starts_with('-') {
        let a = args[i].as_str();
        if tool != Tool::Cargo {
            // Frontend CLIs accept common options before the subcommand. Rspack
            // and Rsbuild also use a default command when only options occur.
            match a {
                "--watch" | "-w" | "--hot" | "--no-hot" | "--open" | "--no-open" => i += 1,
                "--config" | "-c" | "--mode" | "--env" | "--config-name" | "--entry"
                | "--output-path" | "--port" | "--host" => {
                    if i + 1 >= args.len() {
                        return pass(Reason::Unknown);
                    }
                    i += 2;
                }
                _ => {
                    const PREFIXES: [&str; 8] = [
                        "--config=",
                        "--mode=",
                        "--env=",
                        "--config-name=",
                        "--entry=",
                        "--output-path=",
                        "--port=",
                        "--host=",
                    ];
                    if !PREFIXES.iter().any(|p| a.starts_with(p)) {
                        return pass(Reason::Unknown);
                    }
                    i += 1;
                }
            }
            continue;
        }
        let verbose = a.starts_with("-v") && a[1..].trim_matches('v').is_empty();
        if a == "--verbose"
            || a == "--quiet"
            || a == "-q"
            || a == "--locked"
            || a == "--offline"
            || a == "--frozen"
            || verbose
        {
            i += 1;
        } else if a == "--color" || a == "--config" || a == "-C" || a == "-Z" {
            if i + 1 >= args.len() {
                return pass(Reason::Unknown);
            }
            i += 2;
        } else if a.starts_with("--color=")
            || a.starts_with("--config=")
            || a.starts_with("-Z")
            || a.starts_with("-C")
        {
            i += 1;
        } else {
            return pass(Reason::Unknown);
        }
    }
    if i == args.len() {
        // Rspack defaults to build and Rsbuild defaults to the dev server.
        if matches!(tool, Tool::Rspack | Tool::Rsbuild) {
            return managed();
        }
        return pass(Reason::Query);
    }
    let command = args[i].as_str();
    if tool == Tool::Cargo {
        match command {
            "clean" => {
                return Classification {
                    mode: Mode::Clean,
                    reason: Reason::CleanOperation,
                };
            }
            // publish packages the crate into <target-dir>/package and, unless
            // --no-verify is set, runs a verification build into the cache roots.
            "build" | "b" | "check" | "c" | "test" | "t" | "bench" | "run" | "r" | "doc" | "d"
            | "rustc" | "rustdoc" | "clippy" | "fix" | "publish" => return managed(),
            "package" => {
                // package writes the .crate file into <target-dir>/package even
                // with --no-verify. Only --list/-l prints the file list.
                for arg in &args[i + 1..] {
                    if arg == "--" {
                        break;
                    }
                    if arg == "--list" || arg == "-l" {
                        return pass(Reason::Query);
                    }
                }
                return managed();
            }
            "help" | "version" | "metadata" | "locate-project" | "read-manifest" | "pkgid"
            | "tree" | "search" | "info" | "verify-project" => return pass(Reason::Query),
            _ => {}
        }
    } else {
        match command {
            "build" | "dev" => return managed(),
            // The Rspack entries follow the aliases registered by @rspack/cli
            // (build: "", build, bundle, b; serve: serve, server, s, dev).
            // Rsbuild does not define these names, so they stay unknown for it.
            "serve" | "watch" | "server" | "s" | "bundle" | "b" if tool == Tool::Rspack => {
                return managed();
            }
            // Rspack preview alias; Rsbuild does not define it.
            "p" if tool == Tool::Rspack => return pass(Reason::Query),
            "help" | "version" | "inspect" | "preview" => return pass(Reason::Query),
            _ => {}
        }
    }
    pass(Reason::Unknown)
}
