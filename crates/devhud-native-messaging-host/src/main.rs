use std::{io, path::PathBuf};

use devhud_native_messaging_host::{
    PROTOCOL_VERSION, REQUEST_DEADLINE_MILLIS, SCHEMA_VERSION,
    auth::{handshake_proof, now_unix_millis, random_nonce, sign_request, verify_auth_result},
    configured_extension_id, delete_pairing_secret, endpoint, expected_extension_origin,
    framing::{ByteOrder, read_json, write_json},
    protocol::{
        AuthResponse, AuthResult, Challenge, IpcRequest, IpcResponse, NativeRequest,
        NativeResponse, NativeResponseState, validate_deadline, validate_version,
    },
    read_pairing_secret, registration,
};
use tracing::{error, info, warn};

#[cfg(unix)]
type PlatformStream = std::os::unix::net::UnixStream;
#[cfg(windows)]
type PlatformStream = std::fs::File;

struct Session {
    stream: PlatformStream,
    secret: Vec<u8>,
    session_id: String,
}

fn response(
    request: &NativeRequest,
    state: NativeResponseState,
    payload: serde_json::Value,
) -> NativeResponse {
    NativeResponse {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        request_id: request.request_id.clone(),
        ok: matches!(
            state,
            NativeResponseState::Paired | NativeResponseState::Accepted
        ),
        state,
        payload,
    }
}

fn valid_nonce(value: &str) -> bool {
    use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
    URL_SAFE_NO_PAD
        .decode(value)
        .is_ok_and(|bytes| bytes.len() == 32)
}

fn validate_native_request(request: &NativeRequest, now: i64) -> Result<(), &'static str> {
    validate_version(request.version, request.schema_version)?;
    let request_id = uuid::Uuid::parse_str(&request.request_id).map_err(|_| "invalid-request")?;
    if request_id.get_version_num() != 7 || !valid_nonce(&request.nonce) {
        return Err("invalid-request");
    }
    validate_deadline(now, request.deadline_unix_ms, now)
}

fn authenticate(origin: &str, pairing_nonce: Option<String>) -> Result<Session, String> {
    let mut stream = endpoint::connect().map_err(|_| "disconnected".to_string())?;
    let challenge: Challenge = read_json(&mut stream, ByteOrder::LittleEndian)
        .map_err(|_| "authentication-failed".to_string())?;
    validate_version(challenge.version, challenge.schema_version).map_err(str::to_string)?;
    if challenge.deadline_unix_ms < now_unix_millis() {
        return Err("expired-request".to_string());
    }
    let secret = read_pairing_secret()
        .map_err(|_| "storage-failure".to_string())?
        .ok_or_else(|| "not-paired".to_string())?;
    let client_nonce = random_nonce();
    let response = AuthResponse {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        challenge_id: challenge.challenge_id.clone(),
        extension_id: configured_extension_id().to_string(),
        origin: origin.to_string(),
        proof: handshake_proof(
            &secret,
            &challenge,
            configured_extension_id(),
            origin,
            &client_nonce,
            pairing_nonce.as_deref(),
        ),
        client_nonce,
        pairing_nonce,
    };
    write_json(&mut stream, ByteOrder::LittleEndian, &response)
        .map_err(|_| "authentication-failed".to_string())?;
    let result: AuthResult = read_json(&mut stream, ByteOrder::LittleEndian)
        .map_err(|_| "authentication-failed".to_string())?;
    validate_version(result.version, result.schema_version).map_err(str::to_string)?;
    if !result.accepted {
        return Err(result
            .error
            .unwrap_or_else(|| "authentication-failed".to_string()));
    }
    if !verify_auth_result(&secret, &challenge, &result) {
        return Err("authentication-failed".to_string());
    }
    Ok(Session {
        stream,
        secret,
        session_id: result
            .session_id
            .ok_or_else(|| "authentication-failed".to_string())?,
    })
}

fn forward(session: &mut Session, request: &NativeRequest) -> Result<serde_json::Value, String> {
    let issued_at = now_unix_millis();
    let mut ipc = IpcRequest {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        request_id: request.request_id.clone(),
        message_type: request.message_type,
        issued_at_unix_ms: issued_at,
        deadline_unix_ms: request
            .deadline_unix_ms
            .min(issued_at + REQUEST_DEADLINE_MILLIS),
        nonce: request.nonce.clone(),
        payload: request.payload.clone(),
        proof: String::new(),
    };
    sign_request(&session.secret, &session.session_id, &mut ipc);
    write_json(&mut session.stream, ByteOrder::LittleEndian, &ipc)
        .map_err(|_| "disconnected".to_string())?;
    let result: IpcResponse = read_json(&mut session.stream, ByteOrder::LittleEndian)
        .map_err(|_| "disconnected".to_string())?;
    validate_version(result.version, result.schema_version).map_err(str::to_string)?;
    if result.request_id != request.request_id || !result.accepted {
        return Err(result.error.unwrap_or_else(|| "denied".to_string()));
    }
    Ok(result.payload)
}

fn run_native(origin: &str) -> io::Result<()> {
    let stdin = io::stdin();
    let stdout = io::stdout();
    let mut input = stdin.lock();
    let mut output = stdout.lock();
    let mut session: Option<Session> = None;
    loop {
        let request: NativeRequest = match read_json(&mut input, ByteOrder::NativeEndian) {
            Ok(request) => request,
            Err(error) if error.kind() == io::ErrorKind::UnexpectedEof => return Ok(()),
            Err(error) => {
                error!(event = "native_message_rejected", reason = %error);
                return Err(error);
            }
        };
        let now = now_unix_millis();
        if let Err(reason) = validate_native_request(&request, now) {
            warn!(event = "native_request_rejected", reason);
            write_json(
                &mut output,
                ByteOrder::NativeEndian,
                &response(
                    &request,
                    NativeResponseState::Malformed,
                    serde_json::Value::Null,
                ),
            )?;
            continue;
        }
        if session.is_none() {
            match authenticate(origin, request.pairing_nonce.clone()) {
                Ok(authenticated) => session = Some(authenticated),
                Err(reason) => {
                    warn!(event = "app_connection_unavailable", reason);
                    write_json(
                        &mut output,
                        ByteOrder::NativeEndian,
                        &response(
                            &request,
                            NativeResponseState::Disconnected,
                            serde_json::Value::Null,
                        ),
                    )?;
                    continue;
                }
            }
        }
        let mut result = forward(session.as_mut().expect("session was established"), &request);
        if result.is_err() {
            session = None;
            if let Ok(mut authenticated) = authenticate(origin, None) {
                result = forward(&mut authenticated, &request);
                if result.is_ok() {
                    session = Some(authenticated);
                }
            }
        }
        let (state, payload) = match result {
            Ok(payload)
                if request.message_type
                    == devhud_native_messaging_host::protocol::NativeMessageType::Pair =>
            {
                (NativeResponseState::Paired, payload)
            }
            Ok(payload) => (NativeResponseState::Accepted, payload),
            Err(reason) => {
                warn!(event = "ipc_request_rejected", reason);
                session = None;
                (NativeResponseState::Disconnected, serde_json::Value::Null)
            }
        };
        write_json(
            &mut output,
            ByteOrder::NativeEndian,
            &response(&request, state, payload),
        )?;
    }
}

fn register(args: &[String]) -> Result<(), String> {
    let binary = args
        .first()
        .map(PathBuf::from)
        .ok_or("register requires an absolute binary path")?;
    let destination = args
        .get(1)
        .map(PathBuf::from)
        .map_or_else(registration::user_manifest_path, Ok)
        .map_err(|error| error.to_string())?;
    registration::write_manifest(&destination, &binary).map_err(|error| error.to_string())?;
    #[cfg(windows)]
    {
        let key = format!(
            r"HKCU\Software\Google\Chrome\NativeMessagingHosts\{}",
            devhud_native_messaging_host::HOST_NAME
        );
        let status = std::process::Command::new("reg.exe")
            .args([
                "ADD",
                &key,
                "/ve",
                "/t",
                "REG_SZ",
                "/d",
                &destination.to_string_lossy(),
                "/f",
            ])
            .status()
            .map_err(|error| error.to_string())?;
        if !status.success() {
            return Err("unable to register Native Messaging host".into());
        }
    }
    info!(event = "native_host_registered");
    Ok(())
}

fn unregister(args: &[String]) -> Result<(), String> {
    let manifest_result = args
        .first()
        .map(PathBuf::from)
        .map_or_else(registration::user_manifest_path, Ok)
        .map_err(|error| error.to_string())
        .and_then(|destination| {
            registration::remove_manifest(&destination).map_err(|error| error.to_string())
        });
    #[cfg(windows)]
    {
        let key = format!(
            r"HKCU\Software\Google\Chrome\NativeMessagingHosts\{}",
            devhud_native_messaging_host::HOST_NAME
        );
        let _ = std::process::Command::new("reg.exe")
            .args(["DELETE", &key, "/f"])
            .status();
    }
    let pairing_result = delete_pairing_secret();
    manifest_result?;
    pairing_result?;
    info!(event = "native_host_unregistered");
    Ok(())
}

fn main() {
    tracing_subscriber::fmt()
        .with_writer(io::stderr)
        .with_target(false)
        .init();
    let args: Vec<String> = std::env::args().skip(1).collect();
    let result = match args.first().map(String::as_str) {
        Some("register") => register(&args[1..]),
        Some("unregister") => unregister(&args[1..]),
        Some(origin) if origin == expected_extension_origin() => {
            run_native(origin).map_err(|error| error.to_string())
        }
        Some(_) => {
            Err("Native Messaging origin did not match the configured extension".to_string())
        }
        None => Err("Native Messaging origin is required".to_string()),
    };
    if let Err(reason) = result {
        error!(event = "native_host_failed", reason);
        std::process::exit(1);
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn native_request_requires_v7_nonce_and_five_second_deadline() {
        let now = now_unix_millis();
        let request = NativeRequest {
            version: 1,
            schema_version: 1,
            request_id: uuid::Uuid::now_v7().to_string(),
            message_type: devhud_native_messaging_host::protocol::NativeMessageType::Ping,
            deadline_unix_ms: now + 5_000,
            nonce: random_nonce(),
            pairing_nonce: None,
            payload: serde_json::Value::Null,
        };
        assert_eq!(validate_native_request(&request, now), Ok(()));
    }
}
