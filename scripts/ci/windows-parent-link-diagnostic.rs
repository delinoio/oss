#[cfg(windows)]
use std::os::windows::{
    ffi::OsStrExt,
    fs::{symlink_dir, symlink_file},
};
use std::{
    fs, io,
    path::{Path, PathBuf},
    time::{SystemTime, UNIX_EPOCH},
};

struct Fixture(PathBuf);
impl Drop for Fixture {
    fn drop(&mut self) {
        if let Err(error) = fs::remove_dir_all(&self.0) {
            eprintln!(
                "fixture_cleanup_failed kind={:?} code={:?}",
                error.kind(),
                error.raw_os_error()
            );
        }
    }
}
fn read_probe(label: &str, path: &Path) {
    println!("probe={label} path={path:?}");
    match fs::read(path) {
        Ok(bytes) => println!("read={:?}", String::from_utf8_lossy(&bytes)),
        Err(error) => println!(
            "read_error kind={:?} code={:?}",
            error.kind(),
            error.raw_os_error()
        ),
    }
    match fs::canonicalize(path) {
        Ok(path) => println!("canonical={path:?}"),
        Err(error) => println!(
            "canonical_error kind={:?} code={:?}",
            error.kind(),
            error.raw_os_error()
        ),
    }
}
#[cfg(windows)]
fn link_probe(label: &str, path: &Path) {
    match fs::read_link(path) {
        Ok(target) => println!(
            "link={label} target={target:?} utf16={:?}",
            target.as_os_str().encode_wide().collect::<Vec<_>>()
        ),
        Err(error) => println!(
            "link={label} error kind={:?} code={:?}",
            error.kind(),
            error.raw_os_error()
        ),
    }
}
#[cfg(windows)]
fn main() -> io::Result<()> {
    let nonce = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap()
        .as_nanos();
    let fixture = Fixture(std::env::temp_dir().join(format!(
        "clibox-parent-diagnostic-{}-{nonce}",
        std::process::id()
    )));
    fs::create_dir(&fixture.0)?;
    for absolute_directory in [false, true] {
        for root_decoy in [false, true] {
            let case = fixture.0.join(format!(
                "absolute-{absolute_directory}-root-decoy-{root_decoy}"
            ));
            fs::create_dir_all(case.join("deep").join("nested"))?;
            fs::write(case.join("deep").join("target.txt"), b"deep-fixture")?;
            if root_decoy {
                fs::write(case.join("target.txt"), b"root-decoy")?;
            }
            let root = fs::canonicalize(&case)?;
            println!(
                "CASE absolute_directory={absolute_directory} root_decoy={root_decoy} \
                 ordinary_root={case:?} canonical_root={root:?}"
            );
            let directory_target = if absolute_directory {
                root.join("deep").join("nested")
            } else {
                PathBuf::from("deep").join("nested")
            };
            symlink_dir(&directory_target, root.join("shortcut"))?;
            let file_target = PathBuf::from("shortcut").join("..").join("target.txt");
            symlink_file(&file_target, root.join("input.txt"))?;
            symlink_file(
                PathBuf::from("deep")
                    .join("nested")
                    .join("..")
                    .join("target.txt"),
                root.join("direct.txt"),
            )?;
            link_probe("shortcut", &root.join("shortcut"));
            link_probe("input", &root.join("input.txt"));
            link_probe("direct", &root.join("direct.txt"));
            read_probe("input-canonical-root", &root.join("input.txt"));
            read_probe("input-ordinary-root", &case.join("input.txt"));
            read_probe("direct-parent-in-relative-link", &root.join("direct.txt"));
            read_probe(
                "ordinary-spelled-shortcut-parent",
                &case.join("shortcut").join("..").join("target.txt"),
            );
            // PathBuf::push reduces parents when its base is verbatim, so
            // append raw UTF-16-compatible spelling without PathBuf::join.
            let mut verbatim_parent = root.as_os_str().to_os_string();
            verbatim_parent.push(r"\shortcut\..\target.txt");
            read_probe(
                "verbatim-raw-shortcut-parent",
                &PathBuf::from(verbatim_parent),
            );
            println!("END_CASE");
        }
    }
    Ok(())
}
#[cfg(not(windows))]
fn main() {
    eprintln!("This diagnostic must run on Windows.");
}
