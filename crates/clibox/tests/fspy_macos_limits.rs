#![cfg(target_os = "macos")]

use std::{fs, process::Command};

#[test]
fn native_reader_keeps_receiver_limits_distinct_from_cleanup_failure() {
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().join("project");
    fs::create_dir(&root).unwrap();
    fs::write(root.join("input"), b"x").unwrap();
    let source = directory.path().join("reader.c");
    let reader = directory.path().join("reader");
    fs::write(
        &source,
        b"#include <fcntl.h>\n#include <unistd.h>\nint main(void) {\n\
          int fd = open(\"input\", O_RDONLY); if (fd < 0) return 2;\n\
          char byte; ssize_t n = read(fd, &byte, 1); close(fd);\n\
          return n == 1 ? 0 : 3;\n}\n",
    )
    .unwrap();
    assert!(Command::new("cc")
        .arg(&source)
        .arg("-o")
        .arg(&reader)
        .status()
        .unwrap()
        .success());
    for (limits, expected) in [
        (vec![], None),
        (vec!["--max-events", "2"], Some("event_limit")),
        (vec!["--max-bytes", "4096"], Some("byte_limit")),
    ] {
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .current_dir(&root)
            .args(["fspy", "record", "--root"])
            .arg(&root)
            .args(["--timeout", "5s"])
            .args(limits)
            .arg("--")
            .arg(&reader)
            .output()
            .unwrap();
        let summary: serde_json::Value = serde_json::from_slice(
            output
                .stdout
                .split(|byte| *byte == b'\n')
                .rfind(|line| !line.is_empty())
                .unwrap(),
        )
        .unwrap();
        assert_eq!(summary["type"], "summary");
        let summary = &summary["data"];
        if let Some(expected) = expected {
            assert_eq!(output.status.code(), Some(1));
            assert_eq!(summary["complete"], false);
            if summary["failure"] == "cleanup" {
                // Cleanup races are a separate native failure. They remain
                // primary, but must retain the exhausted budget in diagnostics.
                let stderr = String::from_utf8(output.stderr).unwrap();
                assert!(stderr.contains(&format!("cause={expected}")), "{stderr}");
            } else {
                assert_eq!(summary["failure"], expected);
                assert!(String::from_utf8(output.stderr).unwrap().contains(expected));
            }
        } else {
            assert!(output.status.success());
            assert_eq!(summary["complete"], true);
            assert!(summary["failure"].is_null());
        }
    }
    let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
        .current_dir(&root)
        .args(["fspy", "record", "--root"])
        .arg(&root)
        .args(["--max-bytes", "1", "--"])
        .arg(reader)
        .output()
        .unwrap();
    assert_eq!(output.status.code(), Some(1));
    assert!(output.stdout.is_empty());
    assert!(String::from_utf8(output.stderr)
        .unwrap()
        .contains("byte_limit"));
}
