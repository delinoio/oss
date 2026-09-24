use std::{env::consts::EXE_SUFFIX, process::Command};

#[test]
fn help_and_no_arguments_succeed_on_stdout() {
    for arguments in [vec![], vec!["--help"], vec!["-h"]] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(arguments)
            .output()
            .unwrap();
        assert!(output.status.success());
        let stdout = String::from_utf8(output.stdout).unwrap();
        assert!(stdout.contains("Usage: clibox"));
        assert!(stdout.contains("--help"));
        assert!(stdout.contains("--version"));
        assert!(stdout.contains(&format!("Version: {}", env!("CARGO_PKG_VERSION"))));
        assert!(stdout.contains("Maintained by: Delino"));
        assert!(stdout.contains("Repository: https://github.com/delinoio/oss"));
        assert!(stdout.contains("License: MIT"));
        assert!(stdout.contains("Support: https://github.com/delinoio/oss/issues"));
        for command in [
            "run",
            "port",
            "open",
            "clipboard",
            "system",
            "text",
            "time",
            "base64",
            "hash",
            "wait",
            "dotenv",
            "yaml",
            "fspy",
        ] {
            assert!(
                stdout
                    .lines()
                    .any(|line| line.trim_start().starts_with(&format!("{command} "))),
                "missing {command} in root help"
            );
        }
        assert!(!stdout
            .lines()
            .any(|line| line.trim_start().starts_with("env ")));
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn version_comes_from_the_cargo_package() {
    for argument in ["--version", "-V"] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .arg(argument)
            .output()
            .unwrap();
        assert!(output.status.success());
        assert_eq!(
            String::from_utf8(output.stdout).unwrap(),
            format!("clibox {}\n", env!("CARGO_PKG_VERSION"))
        );
        assert!(output.stderr.is_empty());
    }
}

#[test]
fn missing_subcommands_show_command_help_on_stderr() {
    let groups: [(&str, &[&str]); 12] = [
        (
            "run",
            &[
                "env",
                "with-rate-limit",
                "with-lock",
                "with-service",
                "with-retry",
                "with-timeout",
            ],
        ),
        ("port", &["list", "kill"]),
        ("clipboard", &["copy", "paste"]),
        ("system", &["cpus"]),
        ("wait", &["tcp", "http", "file"]),
        ("dotenv", &["list", "merge"]),
        ("yaml", &["normalize"]),
        ("text", &["replace"]),
        ("time", &["format", "add"]),
        ("base64", &["encode", "decode"]),
        ("hash", &["compute", "verify"]),
        ("fspy", &["record", "compare", "assetcov"]),
    ];
    for (group, subcommands) in groups {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .arg(group)
            .env("CLICOLOR_FORCE", "1")
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2), "{group}");
        assert!(output.stdout.is_empty(), "{group}");
        let stderr = String::from_utf8(output.stderr).unwrap();
        // clap derives usage from argv[0], including the Windows .exe suffix.
        assert!(
            stderr.contains(&format!("Usage: clibox{EXE_SUFFIX} {group}")),
            "{stderr}"
        );
        assert!(stderr.contains("Commands:"));
        assert!(!stderr.contains("Maintained by: Delino"));
        for subcommand in subcommands {
            assert!(stderr.contains(&format!("  {subcommand} ")), "{stderr}");
        }
        assert!(!stderr.contains("error:"));
        assert!(!stderr.contains('\u{1b}'));

        for flag in ["--help", "-h"] {
            let help = Command::new(env!("CARGO_BIN_EXE_clibox"))
                .args([group, flag])
                .env("NO_COLOR", "1")
                .output()
                .unwrap();
            assert!(help.status.success(), "{group} {flag}");
            assert!(help.stderr.is_empty());
            assert_eq!(String::from_utf8(help.stdout).unwrap(), stderr);
        }
    }
}

#[test]
fn unknown_arguments_fail_on_stderr() {
    for arguments in [
        vec!["--PRIVATE-MARKER"],
        vec!["PRIVATE-MARKER"],
        vec!["run", "PRIVATE-MARKER"],
        vec!["port", "PRIVATE-MARKER"],
        vec!["clipboard", "PRIVATE-MARKER"],
        vec!["system", "PRIVATE-MARKER"],
        vec!["wait", "PRIVATE-MARKER"],
        vec!["text", "PRIVATE-MARKER"],
        vec!["time", "PRIVATE-MARKER"],
        vec!["base64", "PRIVATE-MARKER"],
        vec!["hash", "PRIVATE-MARKER"],
        vec!["dotenv", "PRIVATE-MARKER"],
        vec!["yaml", "PRIVATE-MARKER"],
        vec!["fspy", "PRIVATE-MARKER"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(arguments)
            .env("RUST_LOG", "trace")
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2));
        assert!(output.stdout.is_empty());
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert!(stderr.contains("error:"));
        assert!(!stderr.contains("PRIVATE-MARKER"));
    }
}

#[test]
fn every_command_has_help_and_examples() {
    for args in [
        vec!["wait", "tcp", "--help"],
        vec!["wait", "http", "--help"],
        vec!["wait", "file", "--help"],
        vec!["run", "env", "--help"],
        vec!["run", "with-rate-limit", "--help"],
        vec!["run", "with-lock", "--help"],
        vec!["run", "with-service", "--help"],
        vec!["run", "with-retry", "--help"],
        vec!["run", "with-timeout", "--help"],
        vec!["port", "list", "--help"],
        vec!["port", "kill", "--help"],
        vec!["open", "--help"],
        vec!["clipboard", "copy", "--help"],
        vec!["clipboard", "paste", "--help"],
        vec!["system", "cpus", "--help"],
        vec!["dotenv", "list", "--help"],
        vec!["dotenv", "merge", "--help"],
        vec!["yaml", "normalize", "--help"],
        vec!["text", "replace", "--help"],
        vec!["time", "format", "--help"],
        vec!["time", "add", "--help"],
        vec!["base64", "encode", "--help"],
        vec!["base64", "decode", "--help"],
        vec!["hash", "compute", "--help"],
        vec!["hash", "verify", "--help"],
        vec!["fspy", "record", "--help"],
        vec!["fspy", "compare", "--help"],
        vec!["fspy", "assetcov", "--help"],
    ] {
        for flag in ["-h", "--help"] {
            let mut args = args.clone();
            *args.last_mut().unwrap() = flag;
            let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
                .args(&args)
                .env("NO_COLOR", "1")
                .output()
                .unwrap();
            assert!(output.status.success());
            assert!(output.stderr.is_empty());
            let stdout = String::from_utf8(output.stdout).unwrap();
            assert!(stdout.contains("Example"));
            assert!(!stdout.contains("Maintained by: Delino"));
            assert!(stdout.contains("exit") || stdout.contains("Exit codes"));
            if matches!(args[0], "text" | "base64" | "hash") {
                assert!(stdout.contains("Input defaults to stdin"));
                assert!(stdout.contains("--output -"));
                assert!(stdout.contains("--force requires"));
                if matches!(args[0], "base64" | "hash") {
                    assert!(!stdout.contains("--in-place"));
                }
            }
            assert!(!stdout.contains('\u{1b}'));
        }
    }
}

#[test]
fn invalid_shapes_fail_before_any_os_effect() {
    for args in [
        vec!["run", "env"],
        vec!["run", "env", "--"],
        vec!["run", "env", "FOO=bar"],
        vec!["port", "list"],
        vec!["port", "list", "0"],
        vec!["port", "kill", "65536"],
        vec!["port", "list", "80", "--quiet", "--json"],
        vec!["port", "list", "80", "--protocol", "sctp"],
        vec!["open"],
        vec!["open", "https://example.com", "--wait"],
        vec!["open", "a", "b"],
        vec!["clipboard", "copy", "a", "b"],
        vec!["clipboard", "paste", "a"],
        vec!["system", "cpus", "--kind", "physical"],
        vec!["system", "cpus", "--kind"],
        vec!["system", "cpus", "--json", "--quiet"],
        vec!["system", "cpus", "extra"],
        vec!["system", "cpus", "--unknown"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(&args)
            .env("RUST_LOG", "off")
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2), "{args:?}");
        assert!(output.stdout.is_empty());
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert!(stderr.contains("error:"));
        if args[0] == "run" {
            assert!(stderr.contains("clibox run env --help."));
        }
    }
}

#[test]
fn help_never_forces_color_on_a_pipe() {
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .arg("--help")
        .env("CLICOLOR_FORCE", "1")
        .output()
        .unwrap();
    assert!(output.status.success());
    assert!(!output.stdout.contains(&0x1b));
}

#[test]
fn parser_failures_remain_redacted_and_visible_with_logging_disabled() {
    for args in [
        vec!["run", "SECRET-PARSER"],
        vec!["wait", "http", "https://SECRET-PARSER@localhost"],
        vec!["hash", "compute", "--algorithm", "SECRET-PARSER"],
        vec!["port", "list", "SECRET-PARSER"],
        vec!["dotenv", "SECRET-PARSER"],
        vec!["yaml", "SECRET-PARSER"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(args)
            .env("RUST_LOG", "off")
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2));
        assert!(output.stdout.is_empty());
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert!(stderr.contains("error: arguments:"));
        assert!(stderr.contains("--help"));
        assert!(!stderr.contains("SECRET-PARSER"));
    }
}
