//! Private Windows wire decoder, shared with portable protocol fixtures.

#![cfg_attr(not(windows), allow(dead_code))]

use std::io::{self, Read};

use crate::record::{AccessPath, FileIdentity, NativePath, Operation};

pub(crate) const HEADER_BYTES: usize = 50;
const MAX_PATH_BYTES: usize = 4096;

fn invalid(reason: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidData, reason)
}

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum FrameKind {
    Hello,
    Start,
    Completion,
}

#[derive(Debug, Clone)]
pub struct Frame {
    pub kind: FrameKind,
    pub operation: u8,
    pub pid: u32,
    pub parent_pid: u32,
    pub tid: u64,
    pub id: u64,
    pub monotonic_ns: u64,
    pub result: i64,
    pub error: i32,
    pub path: Vec<u8>,
    pub handle_identity: Option<FileIdentity>,
    pub requested_bytes: Option<u64>,
    pub second_path: Option<Vec<u8>>,
    pub sequence: u64,
    pub access_path: Option<AccessPath>,
    pub second_access_path: Option<AccessPath>,
    pub requested_delay_ns: u64,
    pub observed_delay_ns: u64,
}

pub fn read_frame(reader: &mut impl Read) -> io::Result<Option<Frame>> {
    let mut header = [0_u8; HEADER_BYTES];
    match reader.read(&mut header[..1])? {
        0 => return Ok(None),
        1 => reader.read_exact(&mut header[1..])?,
        _ => unreachable!(),
    }
    let kind = match header[0] {
        b'h' => FrameKind::Hello,
        b's' => FrameKind::Start,
        b'e' => FrameKind::Completion,
        value => {
            eprintln!("clibox fspy receiver: stage=frame_kind value={value}");
            return Err(invalid("frame_kind"));
        }
    };
    let operation = header[1];
    let pid = u32::from_le_bytes(header[2..6].try_into().unwrap());
    let parent_pid = u32::from_le_bytes(header[6..10].try_into().unwrap());
    let tid = u64::from_le_bytes(header[10..18].try_into().unwrap());
    let id = u64::from_le_bytes(header[18..26].try_into().unwrap());
    let monotonic_ns = u64::from_le_bytes(header[26..34].try_into().unwrap());
    let result = i64::from_le_bytes(header[34..42].try_into().unwrap());
    let error = i32::from_le_bytes(header[42..46].try_into().unwrap());
    let length = u32::from_le_bytes(header[46..50].try_into().unwrap()) as usize;
    if length > MAX_PATH_BYTES || pid == 0 || tid == 0 {
        return Err(invalid("frame_boundary"));
    }
    match kind {
        FrameKind::Hello
            if operation != 0 || id != 0 || result != 0 || error != 0 || length != 32 =>
        {
            return Err(invalid("hello_frame"))
        }
        FrameKind::Start
            if operation_id(operation).is_none()
                || id == 0
                || (result != 0
                    && !(operation == 1 && result == 1)
                    && !(result == 2 && (2..=9).contains(&operation))
                    && !(result == 3 && matches!(operation, 3 | 5)))
                || error != 0
                || !length.is_multiple_of(2)
                || (result == 2 && length <= 24)
                || (result == 3 && length <= 32) =>
        {
            return Err(invalid("start_frame"))
        }
        FrameKind::Completion
            if operation_id(operation).is_none()
                || id == 0
                || length != 0
                || ((result < 0) != (error != 0)) =>
        {
            return Err(invalid("completion_frame"))
        }
        _ => {}
    }
    let mut path = vec![0_u8; length];
    reader.read_exact(&mut path)?;
    let handle_identity = if kind == FrameKind::Start && matches!(result, 2 | 3) {
        let volume = u64::from_le_bytes(path[..8].try_into().unwrap());
        let file_id = u128::from_le_bytes(path[8..24].try_into().unwrap());
        Some(FileIdentity::Windows { volume, file_id })
    } else {
        None
    };
    let requested_bytes = if kind == FrameKind::Start && result == 3 {
        let requested = u64::from_le_bytes(path[24..32].try_into().unwrap());
        path.drain(..32);
        Some(requested)
    } else {
        if handle_identity.is_some() {
            path.drain(..24);
        }
        None
    };
    // Descriptor mutations carry one handle-resolved path after the identity.
    // Only pathname mutations carry the two length-prefixed paths.
    let second_path =
        if kind == FrameKind::Start && operation == 9 && result == 0 && !path.is_empty() {
            if path.len() < 8 {
                return Err(invalid("mutation_paths"));
            }
            let source_len = u16::from_le_bytes([path[0], path[1]]) as usize;
            let destination_len = u16::from_le_bytes([path[2], path[3]]) as usize;
            if source_len == 0
                || destination_len == 0
                || !source_len.is_multiple_of(2)
                || !destination_len.is_multiple_of(2)
                || source_len + destination_len + 4 != path.len()
            {
                return Err(invalid("mutation_paths"));
            }
            let destination = path.split_off(4 + source_len);
            path.drain(..4);
            Some(destination)
        } else {
            None
        };
    Ok(Some(Frame {
        kind,
        operation,
        pid,
        parent_pid,
        tid,
        id,
        monotonic_ns,
        result,
        error,
        path,
        handle_identity,
        requested_bytes,
        second_path,
        sequence: 0,
        access_path: None,
        second_access_path: None,
        requested_delay_ns: 0,
        observed_delay_ns: 0,
    }))
}

pub(crate) fn operation_id(value: u8) -> Option<Operation> {
    Some(match value {
        1 => Operation::Open,
        2 => Operation::Close,
        3 => Operation::Read,
        4 => Operation::Write,
        5 => Operation::PositionalRead,
        6 => Operation::PositionalWrite,
        7 => Operation::Metadata,
        8 => Operation::Directory,
        9 => Operation::Mutation,
        10 => Operation::Exec,
        _ => return None,
    })
}

pub(crate) fn native_path(bytes: &[u8]) -> io::Result<NativePath> {
    if !bytes.len().is_multiple_of(2) {
        return Err(invalid("windows_path_length"));
    }
    let units = bytes
        .as_chunks::<2>()
        .0
        .iter()
        .map(|pair| u16::from_le_bytes([pair[0], pair[1]]))
        .collect::<Vec<_>>();
    if units.contains(&0) {
        return Err(invalid("windows_path_nul"));
    }
    Ok(NativePath::WindowsUtf16(units))
}

#[cfg(test)]
mod tests {
    use super::*;

    const VOLUME: u64 = 0x1234_5678_9abc_def0;
    const FILE_ID: u128 = 0xfedc_ba98_7654_3210_0123_4567_89ab_cdef;

    fn path(value: &str) -> Vec<u8> {
        value.encode_utf16().flat_map(u16::to_le_bytes).collect()
    }

    fn frame(kind: u8, operation: u8, result: i64, error: i32, payload: &[u8]) -> Vec<u8> {
        let mut bytes = vec![0; HEADER_BYTES];
        bytes[0] = kind;
        bytes[1] = operation;
        bytes[2..6].copy_from_slice(&17_u32.to_le_bytes());
        bytes[6..10].copy_from_slice(&11_u32.to_le_bytes());
        bytes[10..18].copy_from_slice(&19_u64.to_le_bytes());
        bytes[18..26].copy_from_slice(&23_u64.to_le_bytes());
        bytes[26..34].copy_from_slice(&29_u64.to_le_bytes());
        bytes[34..42].copy_from_slice(&result.to_le_bytes());
        bytes[42..46].copy_from_slice(&error.to_le_bytes());
        bytes[46..50].copy_from_slice(&(payload.len() as u32).to_le_bytes());
        bytes.extend_from_slice(payload);
        bytes
    }

    fn descriptor_payload(path: &[u8]) -> Vec<u8> {
        let mut payload = VOLUME.to_le_bytes().to_vec();
        payload.extend_from_slice(&FILE_ID.to_le_bytes());
        payload.extend_from_slice(path);
        payload
    }

    fn decode(mut bytes: &[u8]) -> Frame {
        read_frame(&mut bytes).unwrap().unwrap()
    }

    #[test]
    fn descriptor_mutation_vector_retains_single_path_and_live_identity() {
        let path = path(r"\\?\C:\root\a");
        let bytes = frame(b's', 9, 2, 0, &descriptor_payload(&path));
        assert_eq!(bytes.len(), 50 + 24 + path.len());
        let decoded = decode(&bytes);
        assert_eq!(operation_id(decoded.operation), Some(Operation::Mutation));
        assert_eq!(decoded.path, path);
        assert!(decoded.second_path.is_none());
        assert_eq!(
            decoded.handle_identity,
            Some(FileIdentity::Windows {
                volume: VOLUME,
                file_id: FILE_ID
            })
        );
        assert!(decoded.requested_bytes.is_none());

        // The path does not supply identity: a different live handle identity
        // survives unchanged even when its resolved pathname is identical.
        let mut replaced = bytes;
        replaced[HEADER_BYTES + 8..HEADER_BYTES + 24].copy_from_slice(&7_u128.to_le_bytes());
        assert_eq!(
            decode(&replaced).handle_identity,
            Some(FileIdentity::Windows {
                volume: VOLUME,
                file_id: 7
            })
        );
    }

    #[test]
    fn descriptor_operations_preserve_success_and_permission_failure_frames() {
        // Read, write, metadata and directory controls retain their existing
        // encoding. Mutation adds truncation without changing native outcomes.
        for operation in 2..=9 {
            for (result, error) in [(0, 0), (-1, -1_073_741_790)] {
                let path = path(r"\\?\C:\root\protected");
                let mut stream = frame(b's', operation, 2, 0, &descriptor_payload(&path));
                stream.extend(frame(b'e', operation, result, error, &[]));
                let mut reader = stream.as_slice();
                let start = read_frame(&mut reader).unwrap().unwrap();
                let end = read_frame(&mut reader).unwrap().unwrap();
                assert_eq!(start.kind, FrameKind::Start);
                assert_eq!(end.kind, FrameKind::Completion);
                assert_eq!(
                    (
                        start.pid,
                        start.parent_pid,
                        start.tid,
                        start.id,
                        start.operation
                    ),
                    (end.pid, end.parent_pid, end.tid, end.id, end.operation)
                );
                assert_eq!((end.result, end.error), (result, error));
                assert_eq!(start.path, path);
                assert!(start.second_path.is_none());
                assert!(end.path.is_empty());
                assert!(read_frame(&mut reader).unwrap().is_none());
            }
        }
    }

    #[test]
    fn requested_read_lengths_and_two_path_mutations_remain_distinct() {
        let source = path(r"\\?\C:\root\old");
        let destination = path(r"\\?\C:\root\new");
        for operation in [3, 5] {
            let mut payload = descriptor_payload(&[]);
            payload.extend_from_slice(&31_u64.to_le_bytes());
            payload.extend_from_slice(&source);
            let decoded = decode(&frame(b's', operation, 3, 0, &payload));
            assert_eq!(decoded.path, source);
            assert_eq!(decoded.requested_bytes, Some(31));
            assert!(decoded.second_path.is_none());
        }
        let mut payload = (source.len() as u16).to_le_bytes().to_vec();
        payload.extend_from_slice(&(destination.len() as u16).to_le_bytes());
        payload.extend_from_slice(&source);
        payload.extend_from_slice(&destination);
        let decoded = decode(&frame(b's', 9, 0, 0, &payload));
        assert_eq!(decoded.path, source);
        assert_eq!(decoded.second_path, Some(destination));
        assert!(decoded.handle_identity.is_none());
        // An unresolved pathname attempt remains available for its paired
        // failure; it cannot masquerade as a descriptor with no path.
        assert!(decode(&frame(b's', 9, 0, 0, &[])).path.is_empty());
    }

    #[test]
    fn malformed_descriptor_frames_and_markers_fail_closed() {
        let payload = descriptor_payload(&path(r"\\?\C:\root\a"));
        let mut cases = vec![
            frame(b's', 9, 2, 0, &payload[..22]),
            frame(b's', 9, 2, 0, &payload[..24]),
            frame(b's', 9, 2, 0, &payload[..25]),
            frame(b's', 1, 2, 0, &payload),
            frame(b's', 10, 2, 0, &payload),
            frame(b's', 9, 3, 0, &payload),
            frame(b's', 9, 1, 0, &payload),
            frame(b's', 9, 2, 1, &payload),
            frame(b'e', 9, -1, 0, &[]),
            frame(b'e', 9, 0, -1, &[]),
            frame(b'e', 9, 0, 0, &payload),
        ];
        let valid = frame(b's', 9, 2, 0, &payload);
        for (start, end) in [(2, 6), (10, 18), (18, 26)] {
            let mut zero = valid.clone();
            zero[start..end].fill(0);
            cases.push(zero);
        }
        let mut short = valid.clone();
        short.pop();
        cases.push(short);
        cases.push(valid[..49].to_vec());
        cases.push(frame(b's', 9, 2, 0, &vec![0; MAX_PATH_BYTES + 2]));
        for bytes in cases {
            assert!(read_frame(&mut bytes.as_slice()).is_err());
        }
    }

    #[test]
    fn malformed_two_path_payloads_and_native_path_units_fail_closed() {
        for payload in [
            vec![2, 0, 2, 0, b'a', 0],                   // missing destination
            vec![0, 0, 2, 0, b'b', 0],                   // empty source
            vec![2, 0, 0, 0, b'a', 0],                   // empty destination
            vec![1, 0, 3, 0, b'a', 0, b'b', 0],          // odd lengths
            vec![2, 0, 2, 0, b'a', 0, b'b', 0, b'c', 0], // extra path
            vec![255, 255, 255, 255, b'a', 0, b'b', 0],  // overflowing lengths
        ] {
            assert!(read_frame(&mut frame(b's', 9, 0, 0, &payload).as_slice()).is_err());
        }
        assert!(native_path(b"a").is_err());
        assert!(native_path(&[0, 0]).is_err());
        let bytes = path(r"\\?\C:\root\a");
        assert_eq!(
            native_path(&bytes).unwrap(),
            NativePath::WindowsUtf16(r"\\?\C:\root\a".encode_utf16().collect())
        );
    }
}
