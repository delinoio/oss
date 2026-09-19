//! Native finite commands for tracing/installation validation. Never
//! distributed.
use std::{fs, process::Command, time::Duration};
fn main() {
    let args = std::env::args().skip(1).collect::<Vec<_>>();
    match args.first().map(String::as_str).unwrap_or("read-write") {
        "read-write" => {
            let _ = fs::read("input.txt");
            let _ = fs::read("missing.txt");
            let _ = fs::read_dir(".");
            fs::create_dir_all("out").unwrap();
            fs::write(
                "out/result.txt",
                args.get(1).map(String::as_str).unwrap_or("stable"),
            )
            .unwrap();
            println!("child stdout preserved");
            eprintln!("child stderr preserved");
        }
        "read" => {
            let _ = fs::read(args.get(1).map(String::as_str).unwrap_or("input.txt"));
        }
        "list" => {
            let _ = fs::read_dir("out");
        }
        #[cfg(target_os = "macos")]
        "protected-child" => {
            let status = Command::new("/bin/sh")
                .args(["-c", "printf original > protected-child-result"])
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "child" => {
            let status = Command::new(std::env::current_exe().unwrap())
                .arg("read-write")
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "native-child" => {
            let status = Command::new(&args[1]).arg("read-write").status().unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "linger" => {
            #[allow(
                clippy::zombie_processes,
                reason = "the fixture deliberately leaves a child for Runlens process-group \
                          reaping"
            )]
            let _child = Command::new(std::env::current_exe().unwrap())
                .arg("sleep")
                .spawn()
                .unwrap();
        }
        "sleep" => std::thread::sleep(Duration::from_secs(60)),
        "ready-sleep" => {
            fs::write("child-ready", "ready").unwrap();
            std::thread::sleep(Duration::from_secs(60));
        }
        "fail" => std::process::exit(23),
        "vary-content" | "vary-set" | "vary-permissions" => {
            fs::create_dir_all("out").unwrap();
            let cwd = std::env::current_dir().unwrap();
            let round = cwd.parent().unwrap().file_name().unwrap().to_str().unwrap();
            let path = if args[0] == "vary-set" {
                format!("out/{round}")
            } else {
                "out/result.txt".into()
            };
            fs::write(
                &path,
                if args[0] == "vary-content" {
                    round
                } else {
                    "stable"
                },
            )
            .unwrap();
            #[cfg(unix)]
            if args[0] == "vary-permissions" {
                use std::os::unix::fs::PermissionsExt;
                fs::set_permissions(
                    path,
                    fs::Permissions::from_mode(if round == "round-1" { 0o755 } else { 0o644 }),
                )
                .unwrap();
            }
        }
        "benchmark" => {
            let mut bytes = 0usize;
            for entry in fs::read_dir("inputs").unwrap() {
                bytes += fs::read(entry.unwrap().path()).unwrap().len();
            }
            std::hint::black_box(bytes);
        }
        "overflow" => {
            for index in 0..10000 {
                let _ = fs::metadata(format!("missing-{index}"));
            }
        }
        "stdin" => {
            let mut input = String::new();
            use std::io::Read;
            std::io::stdin().read_to_string(&mut input).unwrap();
            print!("{input}");
        }
        "env" => {
            fs::create_dir_all("out").unwrap();
            let path =
                std::path::Path::new(&std::env::var_os("HOME").unwrap()).join("previous-run");
            assert!(!path.exists());
            fs::write(&path, "local cache").unwrap();
            assert!(std::env::var_os("RUNLENS_AMBIENT_SECRET").is_none());
            fs::write("out/result.txt", "stable").unwrap();
        }
        _ => std::process::exit(2),
    }
}
