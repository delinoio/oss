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
        "child" => {
            let status = Command::new(std::env::current_exe().unwrap())
                .arg("read-write")
                .status()
                .unwrap();
            std::process::exit(status.code().unwrap_or(1));
        }
        "linger" => {
            let _child = Command::new(std::env::current_exe().unwrap())
                .arg("sleep")
                .spawn()
                .unwrap();
        }
        "sleep" => std::thread::sleep(Duration::from_secs(60)),
        "fail" => std::process::exit(23),
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
