use std::{fs, io::Write, path::Path, time::Duration};
fn write(path: &str, content: &[u8]) {
    if let Some(parent) = Path::new(path).parent() { if !parent.as_os_str().is_empty() { fs::create_dir_all(parent).unwrap(); } }
    let staged = Path::new(path).with_extension(format!("{}.tmp", std::process::id()));
    fs::write(&staged, content).unwrap();
    fs::rename(staged, path).unwrap();
}
fn main() {
    let mut args: Vec<_> = std::env::args().collect();
    if Path::new(&args[0]).file_stem().unwrap() == "cargo" {
        assert_eq!(args[1], "metadata");
        let mut log = fs::OpenOptions::new().create(true).append(true).open("metadata-calls").unwrap();
        writeln!(log, "metadata").unwrap();
        // Publish readiness only after the call is recorded; the test may
        // terminate this process immediately after observing the PID file.
        write("metadata.pid", std::process::id().to_string().as_bytes());
        if std::env::var_os("TFLOW_METADATA_GATE").is_some() {
            while !Path::new(".taskflow/metadata.release").exists() { std::thread::sleep(Duration::from_millis(10)); }
            println!("{{\"packages\":[],\"resolve\":{{\"nodes\":[]}}}}");
        } else {
            std::thread::sleep(Duration::from_secs(30));
        }
        return;
    }
    if Path::new(&args[0]).file_stem().unwrap() == "docker" {
        // Fault injection owns a CLI process, never a real Docker daemon.
        match args[1].as_str() {
            "context" => {
                let endpoint = if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("remote-fixture") {
                    "tcp://remote.invalid:2375"
                } else if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("remote-npipe-fixture") {
                    "npipe:////remote-host/pipe/docker_engine"
                } else { "unix:///taskflow-fixture" };
                println!("\"{endpoint}\"");
            }
            "run" => {
                if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("deadline-fixture") {
                    if args.last().unwrap() == "inventory" {
                        println!("{{\"version\":1,\"tests\":[{{\"id\":\"aa\"}}]}}");
                    } else {
                        write("unit.pid", std::process::id().to_string().as_bytes());
                        std::thread::sleep(Duration::from_secs(30));
                    }
                    return;
                }
                if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("forwarding-fixture") {
                    let flags: Vec<_> = args.windows(2).filter(|pair| pair[0] == "--env").map(|pair| pair[1].as_str()).collect();
                    let values: std::collections::BTreeMap<_, _> = flags.iter().map(|flag| flag.split_once('=').unwrap()).collect();
                    assert_eq!(values.len(), flags.len(), "duplicate forwarding");
                    assert!(values.contains_key("TFLOW_CLI_VALUE"), "CLI override missing");
                    assert!(!values.contains_key("TFLOW_INHERITED_VALUE"));
                    assert!(!values.contains_key("TFLOW_SIBLING_SECRET"));
                    assert_ne!(std::env::var("PATH").unwrap(), "/container/bin");
                    for key in ["LD_PRELOAD", "DYLD_INSERT_LIBRARIES", "TFLOW_CLI_VALUE", "TFLOW_MULTILINE"] {
                        assert!(std::env::var_os(key).is_none(), "container value reached the host client: {key}");
                    }
                    assert_eq!(values["PATH"], "/container/bin");
                    assert_eq!(values["LD_PRELOAD"], "/container/only.so");
                    assert_eq!(values["DYLD_INSERT_LIBRARIES"], "/container/only.dylib");
                    assert_eq!(values["TFLOW_MULTILINE"], "first\nsecond ' \" =");
                    let aliases: Vec<_> = values.keys().copied().filter(|name| name.eq_ignore_ascii_case("tflow_case")).collect();
                    if cfg!(windows) {
                        assert_eq!(aliases, ["tflow_case"]);
                    } else {
                        assert_eq!(aliases, ["TFLOW_CASE", "Tflow_Case", "tflow_case"]);
                        assert_eq!(values["TFLOW_CASE"], "inherited");
                        assert_eq!(values["Tflow_Case"], "declared");
                    }
                    assert_eq!(values["tflow_case"], "override");
                    let value = values["TFLOW_CLI_VALUE"];
                    let phase = args.last().unwrap();
                    let mut log = fs::OpenOptions::new().create(true).append(true).open(".taskflow/docker-phases").unwrap();
                    writeln!(log, "{phase}:{value}").unwrap();
                    match phase.as_str() {
                        "probe" => println!("fixture-{value}"),
                        "finite" => write("received", value.as_bytes()),
                        "inventory" => println!("{{\"version\":1,\"tests\":[{{\"id\":\"aa\"}}]}}"),
                        "shard" => {
                            let result = flags.iter().find_map(|value| value.strip_prefix("TFLOW_SHARD_RESULT=/workspace/")).unwrap();
                            write(result, b"{\"version\":1,\"results\":[{\"id\":\"aa\",\"status\":\"passed\"}]}");
                        }
                        _ => panic!("unexpected forwarded phase"),
                    }
                    return;
                }
                write("docker-start", std::process::id().to_string().as_bytes());
                std::thread::sleep(Duration::from_secs(30));
            }
            "rm" | "ps" => {
                if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("deadline-fixture") {
                    let mut log = fs::OpenOptions::new().create(true).append(true).open("cleanup-events").unwrap();
                    writeln!(log, "{}", args[1]).unwrap();
                    return;
                }
                if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("forwarding-fixture") { return; }
                if std::env::var("DOCKER_CONTEXT").as_deref() == Ok("cleanup-fixture") {
                    assert_eq!(std::env::var("DOCKER_HOST").unwrap(), "unix:///selected.sock");
                    let mut log = fs::OpenOptions::new().create(true).append(true)
                        .open(Path::new(&std::env::var("DOCKER_CONFIG").unwrap()).join("cleanup-events")).unwrap();
                    writeln!(log, "{}", args[1]).unwrap();
                    if args[1] == "ps" { return; }
                }
                std::process::exit(7);
            },
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
    if args[1] == "delay" {
        std::thread::sleep(Duration::from_millis(args[2].parse().unwrap()));
        args.drain(1..3);
    }
    match args[1].as_str() {
        #[cfg(unix)]
        "detached-root" => {
            let pid_file = std::env::current_dir().unwrap().join(&args[2]);
            let status = std::process::Command::new(std::env::current_exe().unwrap())
                .args(["detached-middle", pid_file.to_str().unwrap(), &args[3], &args[4]])
                .status().unwrap();
            assert!(status.success());
            while !pid_file.exists() { std::thread::sleep(Duration::from_millis(5)); }
            if args[5] == "hold" { std::thread::sleep(Duration::from_secs(60)); }
        }
        #[cfg(unix)]
        "detached-middle" => {
            use std::os::unix::process::CommandExt;
            unsafe extern "C" { fn setsid() -> i32; }
            assert!(unsafe { setsid() } > 0);
            let mut leaf = std::process::Command::new(std::env::current_exe().unwrap());
            leaf.args(["detached-leaf", &args[2], &args[3]])
                .env_clear().current_dir("/")
                .stdin(std::process::Stdio::null()).stdout(std::process::Stdio::null()).stderr(std::process::Stdio::null());
            if args[4] == "setsid" {
                unsafe { leaf.pre_exec(|| { if setsid() < 0 { return Err(std::io::Error::last_os_error()); } Ok(()) }); }
            } else { leaf.process_group(0); }
            leaf.spawn().unwrap();
            // Deliberately do not wait: root + middle both exit before cleanup.
        }
        #[cfg(unix)]
        "detached-leaf" => {
            let _socket = std::net::TcpListener::bind(&args[3]).unwrap();
            write(&args[2], std::process::id().to_string().as_bytes());
            std::thread::sleep(Duration::from_secs(60));
        }
        "version" => println!("taskflow-fixture-1"),
        "version-streams" => {
            print!("{}", fs::read_to_string(&args[2]).unwrap());
            eprint!("{}", fs::read_to_string(&args[3]).unwrap());
        }
        "copy" | "copy-unchanged" => {
            write(&args[3], &fs::read(&args[2]).unwrap());
            if args[1] == "copy-unchanged" {
                assert!(std::process::Command::new(&args[4])
                    .args(["result", "unchanged"]).status().unwrap().success());
            }
        }
        "rewrite-config-once" => {
            if !Path::new(&args[3]).exists() {
                fs::copy(&args[2], "taskflow.yml").unwrap();
                write(&args[3], b"done");
            }
        }
        "bootstrap-install" => {
            if !Path::new("installed").exists() {
                assert!(std::process::Command::new("cargo").args(["generate-lockfile", "--offline"]).status().unwrap().success());
                match args[2].as_str() {
                    "delete" => fs::remove_file("middle").unwrap(),
                    "replace" => write("middle", b"obsolete"),
                    "config" => { fs::copy("next.yml", "taskflow.yml").unwrap(); }
                    _ => panic!("unknown bootstrap fixture mutation"),
                }
                write("installed", b"done");
            }
        }
        "write" => write(&args[2], args[3].as_bytes()),
        "task-report" => {
            let path = std::env::var("TFLOW_RESULT_FILE").unwrap();
            let id = std::env::var("TFLOW_EXECUTION_ID").unwrap();
            let report = format!("{{\"version\":1,\"execution\":\"{id}\",\"result\":\"unchanged\"}}");
            match args[2].as_str() {
                "absent" => {},
                "valid" => write(&path, report.as_bytes()),
                "boundary" => { let mut bytes = report.into_bytes(); bytes.resize(1024, b' '); write(&path, &bytes); },
                "oversize" => fs::File::create(&path).unwrap().set_len(1024 * 1024 * 1024).unwrap(),
                "directory" => fs::create_dir(&path).unwrap(),
                "foreign" => write(&path, b"{\"version\":1,\"execution\":\"other\",\"result\":\"unchanged\"}"),
                "invalid" => write(&path, b"invalid"),
                #[cfg(unix)]
                "link" | "dangling" => {
                    let target = Path::new(&path).with_extension("target");
                    if args[2] == "link" { write(target.to_str().unwrap(), report.as_bytes()); }
                    std::os::unix::fs::symlink(target, &path).unwrap();
                },
                #[cfg(unix)]
                "fifo" => {
                    unsafe extern "C" { fn mkfifo(path: *const std::ffi::c_char, mode: u32) -> i32; }
                    let path = std::ffi::CString::new(path).unwrap();
                    assert_eq!(unsafe { mkfifo(path.as_ptr(), 0o600) }, 0);
                },
                _ => panic!("unknown report fixture"),
            }
        }
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
        "gated" | "gated-copy" => {
            write(&args[2], b"started");
            while !Path::new(&args[3]).exists() { std::thread::sleep(Duration::from_millis(10)); }
            if args[1] == "gated-copy" {
                write(&args[5], &fs::read(&args[4]).unwrap());
                let mut log = fs::OpenOptions::new().create(true).append(true).open(&args[6]).unwrap();
                writeln!(log, "consumed").unwrap();
            }
        }
        "jest" if args.iter().any(|arg| arg == "--listTests") => {
            println!("[\"one.test.js\",\"two.test.js\"]");
        }
        "jest" => {
            write("unit.pid", std::process::id().to_string().as_bytes());
            std::thread::sleep(Duration::from_secs(30));
        }
        "sleep" | "stubborn" => {
            #[cfg(unix)]
            if args[1] == "stubborn" {
                unsafe extern "C" { fn signal(sig: i32, handler: usize) -> usize; }
                // Force the engine to await its graceful deadline and hard kill.
                unsafe { signal(15, 1); }
            }
            write(&args[2], std::process::id().to_string().as_bytes());
            if let Some(child_path) = args.get(3) {
                std::process::Command::new(&args[0]).args([args[1].as_str(), child_path]).spawn().unwrap();
            }
            std::thread::sleep(Duration::from_secs(30));
        }
        "record-service-pid" => {
            let pid = fs::read_to_string(&args[2]).unwrap();
            let mut log = fs::OpenOptions::new().create(true).append(true).open(&args[3]).unwrap();
            writeln!(log, "{pid}").unwrap();
        }
        "server" => {
            let listener = std::net::TcpListener::bind(&args[2]).unwrap();
            write(&args[3], std::process::id().to_string().as_bytes());
            for stream in listener.incoming() { drop(stream.unwrap()); }
        }
        "paced" | "gated-paced" => {
            let _exclusive = std::net::TcpListener::bind(&args[3]).unwrap();
            let mut file = fs::OpenOptions::new().create(true).append(true).open(&args[2]).unwrap();
            writeln!(file, "start:{}", std::process::id()).unwrap();
            if args[1] == "gated-paced" {
                while !Path::new(&args[4]).exists() { std::thread::sleep(Duration::from_millis(10)); }
            } else {
                std::thread::sleep(Duration::from_millis(args[4].parse().unwrap()));
            }
            writeln!(file, "end:{}", std::process::id()).unwrap();
        }
        "inventory" => println!("{{\"version\":1,\"tests\":[{{\"id\":\"aa\"}},{{\"id\":\"bb\"}},{{\"id\":\"cc\"}}]}}"),
        "oversized-shard" => {
            let path = std::env::var("TFLOW_SHARD_RESULT").unwrap();
            let file = fs::File::create(path).unwrap();
            file.set_len(64 * 1024 * 1024 + 1).unwrap();
        }
        "shard" | "shard-log" => {
            let input = fs::read_to_string(std::env::var("TFLOW_SHARD_INPUT").unwrap()).unwrap();
            if args[1] == "shard-log" {
                let secret = std::env::var("TFLOW_LOG_SECRET").unwrap();
                if input.contains("\"aa\"") { std::io::stdout().write_all(&secret.as_bytes()[..4]).unwrap(); }
                if input.contains("\"bb\"") { std::io::stderr().write_all(&secret.as_bytes()[4..]).unwrap(); }
            }
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
