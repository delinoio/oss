//! Early macOS injected-operation receiver fixtures.
//!
//! The complete collector and complete operation boundary are not available
//! yet. This module tests the private wire before any CLI handler uses it.

#[cfg(test)]
mod tests {
    use std::{
        fs,
        io::{self, Read, Write},
        os::unix::net::{UnixListener, UnixStream},
        path::PathBuf,
        process::Stdio,
        sync::{Arc, Mutex},
        thread,
        time::Duration,
    };

    use tokio_util::sync::CancellationToken;

    #[derive(Debug)]
    struct Frame {
        kind: u8,
        operation: u8,
        result: i64,
        path: Vec<u8>,
    }

    fn receive_frames(mut stream: UnixStream, frames: Arc<Mutex<Vec<Frame>>>) -> io::Result<()> {
        stream.set_nonblocking(false)?;
        stream.set_read_timeout(Some(Duration::from_secs(10)))?;
        loop {
            let mut header = [0_u8; 50];
            match stream.read_exact(&mut header) {
                Ok(()) => {}
                Err(error)
                    if matches!(
                        error.kind(),
                        io::ErrorKind::UnexpectedEof
                            | io::ErrorKind::TimedOut
                            | io::ErrorKind::WouldBlock
                    ) =>
                {
                    return Ok(());
                }
                Err(error) => return Err(error),
            }
            let length = u32::from_le_bytes(header[46..50].try_into().unwrap()) as usize;
            if length > 4096 {
                return Err(io::Error::new(io::ErrorKind::InvalidData, "path_limit"));
            }
            let mut path = vec![0; length];
            stream.read_exact(&mut path)?;
            frames.lock().unwrap().push(Frame {
                kind: header[0],
                operation: header[1],
                result: i64::from_le_bytes(header[34..42].try_into().unwrap()),
                path,
            });
            if header[0] == b's' {
                stream.write_all(b"g")?;
            }
        }
    }

    #[test]
    fn injected_child_reports_actual_read_results() {
        let directory = tempfile::Builder::new()
            .prefix("clibox-fspy-mac-")
            .tempdir_in("/tmp")
            .unwrap();
        let socket = directory.path().join("trace.sock");
        let listener = UnixListener::bind(&socket).unwrap();
        listener.set_nonblocking(true).unwrap();
        let input = directory.path().join("input.txt");
        fs::write(&input, b"fixture").unwrap();
        let frames = Arc::new(Mutex::new(Vec::<Frame>::new()));
        let observed = Arc::clone(&frames);
        let accept =
            thread::spawn(move || {
                let mut connections = Vec::new();
                let deadline = std::time::Instant::now() + Duration::from_secs(10);
                while std::time::Instant::now() < deadline {
                    match listener.accept() {
                        Ok((stream, _)) => {
                            let frames = Arc::clone(&observed);
                            connections.push(thread::spawn(move || receive_frames(stream, frames)));
                        }
                        Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                            thread::sleep(Duration::from_millis(10));
                        }
                        Err(error) => panic!("accept failed: {error}"),
                    }
                    if observed.lock().unwrap().iter().any(|frame| {
                        frame.kind == b'e' && frame.operation == 3 && frame.result == 7
                    }) {
                        break;
                    }
                }
                for connection in connections {
                    connection.join().unwrap().unwrap();
                }
            });
        let mut command = fspy::Command::new(std::env::current_exe().unwrap());
        command
            .args(["--exact", "macos::tests::read_fixture_child"])
            .envs(std::env::vars_os())
            .env("CLIBOX_FSPY_SOCKET", socket.as_os_str())
            .env("CLIBOX_FSPY_TEST_INPUT", input.as_os_str())
            .stdout(Stdio::null())
            .stderr(Stdio::inherit());
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .unwrap();
        let child = runtime
            .block_on(command.spawn(CancellationToken::new()))
            .unwrap();
        let status = runtime.block_on(child.wait_handle).unwrap();
        assert!(status.status.success(), "{:?}", status.status);
        accept.join().unwrap();
        let frames = frames.lock().unwrap();
        assert!(status.path_accesses.is_ok(), "frames: {frames:?}");
        assert!(frames.iter().any(|frame| {
            frame.kind == b's' && frame.operation == 3 && frame.path.ends_with(b"input.txt")
        }));
        assert!(frames
            .iter()
            .any(|frame| { frame.kind == b'e' && frame.operation == 3 && frame.result == 7 }));
        assert!(frames
            .iter()
            .any(|frame| { frame.kind == b'e' && frame.operation == 7 && frame.result >= 0 }));
        assert!(frames
            .iter()
            .any(|frame| { frame.kind == b'e' && frame.operation == 8 && frame.result >= 0 }));
        assert!(frames
            .iter()
            .any(|frame| { frame.kind == b'e' && frame.operation == 9 && frame.result == 0 }));
    }

    #[test]
    fn read_fixture_child() {
        let Some(path) = std::env::var_os("CLIBOX_FSPY_TEST_INPUT") else {
            return;
        };
        let path = PathBuf::from(path);
        assert_eq!(fs::read(&path).unwrap(), b"fixture");
        assert_eq!(fs::metadata(&path).unwrap().len(), 7);
        assert!(fs::read_dir(path.parent().unwrap()).unwrap().count() > 0);
        fs::rename(&path, path.with_extension("moved")).unwrap();
    }
}
