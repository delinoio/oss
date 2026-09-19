use std::{fs, io::Write, path::Path, time::Duration};
fn write(path: &str, content: &[u8]) {
    if let Some(parent) = Path::new(path).parent() { if !parent.as_os_str().is_empty() { fs::create_dir_all(parent).unwrap(); } }
    let staged = Path::new(path).with_extension(format!("{}.tmp", std::process::id()));
    fs::write(&staged, content).unwrap();
    fs::rename(staged, path).unwrap();
}
fn main() {
    let mut args: Vec<_> = std::env::args().collect();
    if Path::new(&args[0]).file_stem().unwrap() == "docker" {
        // Fault injection owns a CLI process, never a real Docker daemon.
        match args[1].as_str() {
            "context" => {
                let endpoint = if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("remote-fixture") {
                    "tcp://remote.invalid:2375"
                } else { "unix:///taskflow-fixture" };
                println!("\"{endpoint}\"");
            }
            "run" => {
                write("docker-start", std::process::id().to_string().as_bytes());
                std::thread::sleep(Duration::from_secs(30));
            }
            "rm" | "ps" => std::process::exit(7),
            _ => panic!("unexpected Docker fixture command"),
        }
        return;
    }
    if args[1] == "shell" {
        // A portable test interpreter: neither OS default shell understands
        // these inventory/run expressions without the explicit shell setting.
        assert_eq!(args.len(), 3);
        let mut log = fs::OpenOptions::new().create(true).append(true).open("shell-events").unwrap();
        writeln!(log, "{}", args[2]).unwrap();
        args.remove(1);
    }
    match args[1].as_str() {
        "version" => println!("taskflow-fixture-1"),
        "version-streams" => {
            print!("{}", fs::read_to_string(&args[2]).unwrap());
            eprint!("{}", fs::read_to_string(&args[3]).unwrap());
        }
        "copy" => { write(&args[3], &fs::read(&args[2]).unwrap()); }
        "write" => write(&args[2], args[3].as_bytes()),
        "record" | "unchanged" => {
            let mut file = fs::OpenOptions::new().create(true).append(true).open(&args[2]).unwrap();
            writeln!(file, "{}", args[3]).unwrap();
            if args[1] == "unchanged" {
                let path = std::env::var("TFLOW_RESULT_FILE").unwrap();
                let id = std::env::var("TFLOW_EXECUTION_ID").unwrap();
                write(&path, format!("{{\"version\":1,\"execution\":\"{id}\",\"result\":\"unchanged\"}}").as_bytes());
            }
        }
        "env" => {
            let value = std::env::var(&args[2]).unwrap_or_default();
            let mut stdout = std::io::stdout();
            for byte in value.as_bytes() { stdout.write_all(&[*byte]).unwrap(); stdout.flush().unwrap(); std::thread::sleep(Duration::from_millis(1)); }
            if let Some(path) = args.get(3) { write(path, value.as_bytes()); }
        }
        "gated" => {
            write(&args[2], b"started");
            while !Path::new(&args[3]).exists() { std::thread::sleep(Duration::from_millis(10)); }
        }
        "sleep" => {
            write(&args[2], std::process::id().to_string().as_bytes());
            if let Some(child_path) = args.get(3) {
                std::process::Command::new(&args[0]).args(["sleep", child_path]).spawn().unwrap();
            }
            std::thread::sleep(Duration::from_secs(30));
        }
        "server" => {
            let listener = std::net::TcpListener::bind(&args[2]).unwrap();
            write(&args[3], std::process::id().to_string().as_bytes());
            for stream in listener.incoming() { drop(stream.unwrap()); }
        }
        "paced" => {
            let _exclusive = std::net::TcpListener::bind(&args[3]).unwrap();
            let mut file = fs::OpenOptions::new().create(true).append(true).open(&args[2]).unwrap();
            writeln!(file, "start:{}", std::process::id()).unwrap();
            std::thread::sleep(Duration::from_millis(args[4].parse().unwrap()));
            writeln!(file, "end:{}", std::process::id()).unwrap();
        }
        "inventory" => println!("{{\"version\":1,\"tests\":[{{\"id\":\"aa\"}},{{\"id\":\"bb\"}},{{\"id\":\"cc\"}}]}}"),
        "shard" => {
            let input = fs::read_to_string(std::env::var("TFLOW_SHARD_INPUT").unwrap()).unwrap();
            let selected: Vec<_> = ["aa", "bb", "cc"].iter().filter(|id| input.contains(&format!("\"{id}\""))).map(|id| format!("{{\"id\":\"{id}\",\"status\":\"passed\"}}")).collect();
            write(&std::env::var("TFLOW_SHARD_RESULT").unwrap(), format!("{{\"version\":1,\"results\":[{}]}}", selected.join(",")).as_bytes());
        }
        "fail" => std::process::exit(7),
        "fail-after-files" => {
            while args[2..].iter().any(|path| !Path::new(path).exists()) {
                std::thread::sleep(Duration::from_millis(10));
            }
            std::process::exit(7);
        }
        _ => panic!("unknown fixture mode"),
    }
}
