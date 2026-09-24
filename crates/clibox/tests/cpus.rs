use std::process::{Command, Stdio};

const CLI: &str = env!("CARGO_BIN_EXE_clibox");

#[test]
fn available_has_exact_default_and_json_output() {
    let output = Command::new(CLI).args(["system", "cpus"]).output().unwrap();
    assert_eq!(output.status.code(), Some(0));
    let count: usize = String::from_utf8(output.stdout.clone())
        .unwrap()
        .trim_end_matches('\n')
        .parse()
        .unwrap();
    assert!(count > 0);
    assert_eq!(output.stdout, format!("{count}\n").as_bytes());
    assert!(output.stderr.is_empty());

    let json = Command::new(CLI)
        .args(["system", "cpus", "--kind", "available", "--json"])
        .output()
        .unwrap();
    assert_eq!(json.status.code(), Some(0));
    let result: serde_json::Value = serde_json::from_slice(&json.stdout).unwrap();
    let json_count = result["count"].as_u64().unwrap();
    assert!(json_count > 0);
    assert_eq!(
        json.stdout,
        format!("{{\"kind\":\"available\",\"count\":{json_count}}}\n").as_bytes()
    );
    assert!(json.stderr.is_empty());
}

#[test]
fn logical_and_quiet_are_consistent_and_ignore_openmp_overrides() {
    let plain = Command::new(CLI)
        .args(["system", "cpus", "--kind", "logical"])
        .env("OMP_NUM_THREADS", "1")
        .env("OMP_DYNAMIC", "TRUE")
        .env("PATH", "")
        .output()
        .unwrap();
    assert_eq!(plain.status.code(), Some(0), "{:?}", plain.stderr);
    assert!(plain.stderr.is_empty());
    let count: usize = String::from_utf8(plain.stdout.clone())
        .unwrap()
        .trim_end_matches('\n')
        .parse()
        .unwrap();
    assert!(count > 0);
    assert_eq!(plain.stdout, format!("{count}\n").as_bytes());

    let json = Command::new(CLI)
        .args(["system", "cpus", "--kind", "logical", "--json"])
        .env("PATH", "")
        .output()
        .unwrap();
    assert_eq!(json.status.code(), Some(0), "{:?}", json.stderr);
    let result: serde_json::Value = serde_json::from_slice(&json.stdout).unwrap();
    let json_count = result["count"].as_u64().unwrap();
    assert!(json_count > 0);
    assert_eq!(
        json.stdout,
        format!("{{\"kind\":\"logical\",\"count\":{json_count}}}\n").as_bytes()
    );

    let quiet = Command::new(CLI)
        .args(["system", "cpus", "--kind", "logical", "--quiet"])
        .output()
        .unwrap();
    assert_eq!(quiet.status.code(), Some(0));
    assert!(quiet.stdout.is_empty());
    assert!(quiet.stderr.is_empty());
}

#[test]
fn cpu_query_does_not_wait_for_stdin() {
    let mut child = Command::new(CLI)
        .args(["system", "cpus"])
        .stdin(Stdio::piped())
        .stdout(Stdio::piped())
        .spawn()
        .unwrap();
    let stdin = child.stdin.take().unwrap();
    let deadline = std::time::Instant::now() + std::time::Duration::from_secs(5);
    let status = loop {
        if let Some(status) = child.try_wait().unwrap() {
            break status;
        }
        if std::time::Instant::now() >= deadline {
            let _ = child.kill();
            panic!("CPU query waited for stdin");
        }
        std::thread::sleep(std::time::Duration::from_millis(10));
    };
    assert!(status.success());
    drop(stdin);
    let mut stdout = child.stdout.take().unwrap();
    let mut bytes = Vec::new();
    std::io::Read::read_to_end(&mut stdout, &mut bytes).unwrap();
    assert_eq!(bytes.last(), Some(&b'\n'));
}

#[test]
fn parser_diagnostics_are_redacted_even_with_logging_disabled() {
    for level in ["off", "debug", "trace"] {
        let output = Command::new(CLI)
            .args(["system", "cpus", "--kind", "PRIVATE_CPU_VALUE"])
            .env("RUST_LOG", level)
            .output()
            .unwrap();
        assert_eq!(output.status.code(), Some(2));
        assert!(output.stdout.is_empty());
        let stderr = String::from_utf8(output.stderr).unwrap();
        assert!(stderr.contains("clibox system cpus --help"));
        assert!(!stderr.contains("PRIVATE_CPU_VALUE"));
    }
}

#[cfg(unix)]
#[test]
fn closed_stdout_returns_an_actionable_runtime_failure() {
    use std::{fs::File, os::fd::FromRawFd};
    let mut descriptors = [0; 2];
    assert_eq!(unsafe { libc::pipe(descriptors.as_mut_ptr()) }, 0);
    let output = unsafe {
        libc::close(descriptors[0]);
        let writer = File::from_raw_fd(descriptors[1]);
        Command::new(CLI)
            .args(["system", "cpus", "--json"])
            .env("RUST_LOG", "off")
            .stdout(Stdio::from(writer))
            .output()
            .unwrap()
    };
    assert_eq!(output.status.code(), Some(1));
    let stderr = String::from_utf8(output.stderr).unwrap();
    assert!(stderr.contains("Could not write CPU count to stdout"));
    assert!(stderr.contains("IoFailed"));
}

#[cfg(target_os = "linux")]
#[test]
fn logical_is_unchanged_by_child_only_affinity() {
    use std::os::unix::process::CommandExt;
    let original = std::fs::read_to_string("/sys/devices/system/cpu/online").unwrap();
    let mut allowed = std::mem::MaybeUninit::<libc::cpu_set_t>::zeroed();
    assert_eq!(
        unsafe {
            libc::sched_getaffinity(
                0,
                std::mem::size_of::<libc::cpu_set_t>(),
                allowed.as_mut_ptr(),
            )
        },
        0
    );
    let mut allowed = unsafe { allowed.assume_init() };
    let selected = (0..libc::CPU_SETSIZE as usize)
        .find(|&cpu| unsafe { libc::CPU_ISSET(cpu, &allowed) })
        .expect("at least one allowed CPU");
    unsafe {
        libc::CPU_ZERO(&mut allowed);
        libc::CPU_SET(selected, &mut allowed);
    }
    let output = unsafe {
        Command::new(CLI)
            .args(["system", "cpus", "--kind", "logical"])
            .pre_exec(move || {
                if libc::sched_setaffinity(0, std::mem::size_of::<libc::cpu_set_t>(), &allowed) != 0
                {
                    return Err(std::io::Error::last_os_error());
                }
                Ok(())
            })
            .output()
            .unwrap()
    };
    assert_eq!(output.status.code(), Some(0), "{:?}", output.stderr);
    let expected = original
        .trim_end()
        .split(',')
        .map(|part| {
            part.split_once('-').map_or(1, |(a, b)| {
                b.parse::<usize>().unwrap() - a.parse::<usize>().unwrap() + 1
            })
        })
        .sum::<usize>();
    assert_eq!(output.stdout, format!("{expected}\n").as_bytes());
}
