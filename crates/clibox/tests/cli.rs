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
