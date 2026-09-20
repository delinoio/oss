use std::process::Command;

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
        for command in [
            "run",
            "port",
            "open",
            "clipboard",
            "text",
            "time",
            "base64",
            "hash",
            "wait",
        ] {
            assert!(
                stdout
                    .lines()
                    .any(|line| line.trim_start().starts_with(&format!("{command} "))),
                "missing {command} in root help"
            );
        }
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
fn unknown_arguments_fail_on_stderr() {
    for argument in ["--unknown", "run"] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .arg(argument)
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2));
        assert!(output.stdout.is_empty());
        assert!(String::from_utf8(output.stderr).unwrap().contains("error:"));
    }
}

#[test]
fn every_utility_has_help_and_examples() {
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
        assert!(!output.stderr.is_empty());
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
        vec!["wait", "http", "https://SECRET-PARSER@localhost"],
        vec!["hash", "encode", "--algorithm", "SECRET-PARSER"],
        vec!["port", "which", "SECRET-PARSER"],
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
