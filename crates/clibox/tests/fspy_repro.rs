#![cfg(any(target_os = "linux", target_os = "macos"))]

use std::{fs, io::Read, os::unix::fs::symlink, path::Path, process::Command};

use clibox_fspy::record::NativePath;
use sha2::{Digest, Sha256};

#[test]
fn reproduction_preserves_selected_file_and_directory_symlinks() {
    if let Some(input) = std::env::var_os("CLIBOX_FSPY_REPRO_INPUT") {
        let mut byte = [0_u8];
        fs::File::open(input)
            .unwrap()
            .read_exact(&mut byte)
            .unwrap();
        assert_eq!(byte, [b'x']);
        eprintln!("EXPECTED");
        std::process::exit(42);
    }
    let directory = tempfile::tempdir().unwrap();
    let root = directory.path().join("project");
    fs::create_dir_all(root.join("real")).unwrap();
    let root = fs::canonicalize(root).unwrap();
    fs::write(root.join("input"), b"x").unwrap();
    fs::write(root.join("real/input"), b"x").unwrap();
    symlink("input", root.join("link")).unwrap();
    symlink("real", root.join("alias-dir")).unwrap();
    for (input, target, link) in [
        ("input", "input", None),
        ("link", "input", Some(("link", "input"))),
        ("alias-dir/input", "real/input", Some(("alias-dir", "real"))),
    ] {
        let bundle = directory.path().join(input.replace('/', "-"));
        let output = Command::new(env!("CARGO_BIN_EXE_clibox"))
            .current_dir(&root)
            .args(["fspy", "min-repro", "--root"])
            .arg(&root)
            .args(["--include", input, "--bundle-dir"])
            .arg(&bundle)
            .args([
                "--expect-exit",
                "42",
                "--expect-stderr",
                "EXPECTED",
                "--timeout",
                "15s",
                "--json",
                "--",
            ])
            .arg(std::env::current_exe().unwrap())
            .args([
                "--exact",
                "reproduction_preserves_selected_file_and_directory_symlinks",
                "--nocapture",
            ])
            .env("CLIBOX_FSPY_REPRO_INPUT", input)
            .output()
            .unwrap();
        assert!(
            output.status.success(),
            "{input}: {}",
            String::from_utf8_lossy(&output.stderr)
        );
        let report: serde_json::Value = serde_json::from_slice(&output.stdout).unwrap();
        assert_eq!(report["verified"], true);
        assert_eq!(report["collected_files"], 1);
        assert_eq!(
            String::from_utf8_lossy(&output.stderr)
                .matches("EXPECTED")
                .count(),
            2
        );
        assert_eq!(fs::read(bundle.join(input)).unwrap(), b"x");
        let manifest: serde_json::Value = serde_json::from_slice(
            &fs::read(bundle.join(".clibox-fspy-repro/manifest.json")).unwrap(),
        )
        .unwrap();
        let native = |path: &str| NativePath::UnixBytes(path.as_bytes().to_vec());
        assert_eq!(manifest["files"].as_array().unwrap().len(), 1);
        assert_eq!(
            serde_json::from_value::<NativePath>(manifest["files"][0]["path"].clone()).unwrap(),
            native(target)
        );
        assert_eq!(
            manifest["files"][0]["sha256"],
            format!("{:x}", Sha256::digest(b"x"))
        );
        assert_eq!(manifest["files"][0]["size"], 1);
        let links = manifest["internal_links"].as_array().unwrap();
        if let Some((alias, target)) = link {
            assert_eq!(
                fs::read_link(bundle.join(alias)).unwrap(),
                Path::new(target)
            );
            assert_eq!(links.len(), 1);
            assert_eq!(
                serde_json::from_value::<NativePath>(links[0]["path"].clone()).unwrap(),
                native(alias)
            );
            assert_eq!(
                serde_json::from_value::<NativePath>(links[0]["target"].clone()).unwrap(),
                native(target)
            );
        } else {
            assert!(links.is_empty());
        }
    }
}
