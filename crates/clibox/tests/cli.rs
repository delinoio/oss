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
    let groups: [(&str, &[&str]); 6] = [
        ("run", &["env"]),
        ("port", &["which", "kill"]),
        ("clipboard", &["copy", "paste"]),
        ("wait", &["tcp", "http", "file"]),
        ("dotenv", &["list", "merge"]),
        ("yaml", &["normalize"]),
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
        vec!["wait", "PRIVATE-MARKER"],
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
        vec!["port", "which", "--help"],
        vec!["port", "kill", "--help"],
        vec!["open", "--help"],
        vec!["clipboard", "copy", "--help"],
        vec!["clipboard", "paste", "--help"],
        vec!["dotenv", "list", "--help"],
        vec!["dotenv", "merge", "--help"],
        vec!["yaml", "normalize", "--help"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(args)
            .env("NO_COLOR", "1")
            .output()
            .unwrap();
        assert!(output.status.success());
        assert!(output.stderr.is_empty());
        let stdout = String::from_utf8(output.stdout).unwrap();
        assert!(stdout.contains("Example"));
        assert!(!stdout.contains('\u{1b}'));
    }
}

#[test]
fn invalid_shapes_fail_before_any_os_effect() {
    for args in [
        vec!["run", "env"],
        vec!["run", "env", "FOO=bar"],
        vec!["port", "which"],
        vec!["port", "which", "0"],
        vec!["port", "kill", "65536"],
        vec!["port", "which", "80", "--quiet", "--json"],
        vec!["port", "which", "80", "--protocol", "sctp"],
        vec!["open"],
        vec!["open", "https://example.com", "--wait"],
        vec!["open", "a", "b"],
        vec!["clipboard", "copy", "a", "b"],
        vec!["clipboard", "paste", "a"],
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .args(&args)
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2), "{args:?}");
        assert!(output.stdout.is_empty());
        assert!(String::from_utf8(output.stderr).unwrap().contains("error:"));
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
