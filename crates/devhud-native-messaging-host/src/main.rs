use std::{io, path::PathBuf, time::Duration};

use devhud_native_messaging_host::{
    PROTOCOL_VERSION, REQUEST_DEADLINE_MILLIS, SCHEMA_VERSION,
    auth::{handshake_proof, now_unix_millis, random_nonce, sign_request, verify_auth_result},
    configured_extension_id, delete_pairing_secret, endpoint, expected_extension_origin,
    framing::{ByteOrder, read_json, write_json},
    pairing_is_complete,
    protocol::{
        AuthResponse, AuthResult, Challenge, IpcMessageType, IpcRequest, IpcResponse,
        NativeRequest, NativeResponse, NativeResponseState, validate_deadline, validate_version,
    },
    read_pairing_secret, registration,
};
use tracing::{error, info, warn};

type PlatformStream = endpoint::IpcClientStream;

const IPC_IO_TIMEOUT: Duration = Duration::from_secs(5);

struct Session {
    stream: PlatformStream,
    secret: Vec<u8>,
    session_id: String,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum ForwardFailure {
    Disconnected,
    Denied,
    Malformed,
}

impl ForwardFailure {
    fn response_state(self) -> NativeResponseState {
        match self {
            Self::Disconnected => NativeResponseState::Disconnected,
            Self::Denied => NativeResponseState::Denied,
            Self::Malformed => NativeResponseState::Malformed,
        }
    }

    fn reason(self) -> &'static str {
        match self {
            Self::Disconnected => "disconnected",
            Self::Denied => "denied",
            Self::Malformed => "malformed",
        }
    }
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

fn authenticate_stream(
    mut stream: PlatformStream,
    origin: &str,
    pairing_nonce: Option<String>,
) -> Result<Session, String> {
    let challenge: Challenge =
        read_ipc_json(&mut stream).map_err(|_| "authentication-failed".to_string())?;
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
    write_ipc_json(&mut stream, &response).map_err(|_| "authentication-failed".to_string())?;
    let result: AuthResult =
        read_ipc_json(&mut stream).map_err(|_| "authentication-failed".to_string())?;
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

fn authenticate(origin: &str, pairing_nonce: Option<String>) -> Result<Session, String> {
    let stream = endpoint::connect().map_err(|_| "disconnected".to_string())?;
    authenticate_stream(stream, origin, pairing_nonce)
}

fn pairing_nonce_for_authentication(
    request: &NativeRequest,
    retry_after_authenticated_session: bool,
) -> Option<String> {
    if retry_after_authenticated_session {
        None
    } else {
        request.pairing_nonce.clone()
    }
}

fn read_ipc_json<T: serde::de::DeserializeOwned>(stream: &mut PlatformStream) -> io::Result<T> {
    stream.set_io_deadline(IPC_IO_TIMEOUT);
    read_json(stream, ByteOrder::LittleEndian)
}

fn write_ipc_json<T: serde::Serialize>(stream: &mut PlatformStream, value: &T) -> io::Result<()> {
    stream.set_io_deadline(IPC_IO_TIMEOUT);
    write_json(stream, ByteOrder::LittleEndian, value)
}

fn classify_ipc_response(
    result: IpcResponse,
    request_id: &str,
) -> Result<serde_json::Value, ForwardFailure> {
    validate_version(result.version, result.schema_version)
        .map_err(|_| ForwardFailure::Malformed)?;
    if result.request_id != request_id {
        return Err(ForwardFailure::Malformed);
    }
    if !result.accepted {
        return Err(match result.error.as_deref() {
            Some("invalid-browser-context" | "invalid-request" | "unsupported-version") => {
                ForwardFailure::Malformed
            }
            _ => ForwardFailure::Denied,
        });
    }
    Ok(result.payload)
}

fn forward(
    session: &mut Session,
    request: &NativeRequest,
) -> Result<serde_json::Value, ForwardFailure> {
    let issued_at = now_unix_millis();
    let mut ipc = IpcRequest {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        request_id: request.request_id.clone(),
        message_type: request.message_type.into(),
        issued_at_unix_ms: issued_at,
        deadline_unix_ms: request
            .deadline_unix_ms
            .min(issued_at + REQUEST_DEADLINE_MILLIS),
        nonce: request.nonce.clone(),
        payload: request.payload.clone(),
        proof: String::new(),
    };
    sign_request(&session.secret, &session.session_id, &mut ipc);
    write_ipc_json(&mut session.stream, &ipc).map_err(|_| ForwardFailure::Disconnected)?;
    let result: IpcResponse =
        read_ipc_json(&mut session.stream).map_err(|_| ForwardFailure::Disconnected)?;
    classify_ipc_response(result, &request.request_id)
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
            match authenticate(origin, pairing_nonce_for_authentication(&request, false)) {
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
        if result == Err(ForwardFailure::Disconnected) {
            session = None;
            if let Ok(mut authenticated) =
                authenticate(origin, pairing_nonce_for_authentication(&request, true))
            {
                result = forward(&mut authenticated, &request);
                if result != Err(ForwardFailure::Disconnected) {
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
            Err(failure) => {
                warn!(event = "ipc_request_rejected", reason = failure.reason());
                if failure == ForwardFailure::Disconnected {
                    session = None;
                }
                (failure.response_state(), serde_json::Value::Null)
            }
        };
        write_json(
            &mut output,
            ByteOrder::NativeEndian,
            &response(&request, state, payload),
        )?;
    }
}

fn connect_to_running_app() -> Result<Option<PlatformStream>, String> {
    match endpoint::connect() {
        Ok(stream) => Ok(Some(stream)),
        Err(error)
            if matches!(
                error.kind(),
                io::ErrorKind::NotFound | io::ErrorKind::ConnectionRefused
            ) =>
        {
            Ok(None)
        }
        Err(_) => Err("unable to confirm DevHUD IPC availability".to_string()),
    }
}

fn revoke_running_app_pairing() -> Result<bool, String> {
    if !pairing_is_complete()? {
        return Ok(false);
    }
    let Some(stream) = connect_to_running_app()? else {
        return Ok(false);
    };
    let mut session = authenticate_stream(stream, &expected_extension_origin(), None)?;
    let issued_at = now_unix_millis();
    let request_id = uuid::Uuid::now_v7().to_string();
    let mut request = IpcRequest {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        request_id: request_id.clone(),
        message_type: IpcMessageType::RevokePairing,
        issued_at_unix_ms: issued_at,
        deadline_unix_ms: issued_at + REQUEST_DEADLINE_MILLIS,
        nonce: random_nonce(),
        payload: serde_json::Value::Null,
        proof: String::new(),
    };
    sign_request(&session.secret, &session.session_id, &mut request);
    write_ipc_json(&mut session.stream, &request)
        .map_err(|_| "unable to revoke live Native Messaging sessions".to_string())?;
    let response: IpcResponse = read_ipc_json(&mut session.stream)
        .map_err(|_| "unable to confirm Native Messaging session revocation".to_string())?;
    validate_version(response.version, response.schema_version).map_err(str::to_string)?;
    if response.request_id != request_id || !response.accepted {
        return Err("DevHUD rejected Native Messaging session revocation".to_string());
    }
    Ok(true)
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
    let pairing_was_revoked_by_app = revoke_running_app_pairing()?;
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
    let pairing_result = if pairing_was_revoked_by_app {
        Ok(())
    } else {
        delete_pairing_secret()
    };
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

    #[test]
    fn retry_after_successful_authentication_omits_the_consumed_pairing_nonce() {
        let request = NativeRequest {
            version: PROTOCOL_VERSION,
            schema_version: SCHEMA_VERSION,
            request_id: uuid::Uuid::now_v7().to_string(),
            message_type: devhud_native_messaging_host::protocol::NativeMessageType::Pair,
            deadline_unix_ms: now_unix_millis() + REQUEST_DEADLINE_MILLIS,
            nonce: random_nonce(),
            pairing_nonce: Some("consumed".to_string()),
            payload: serde_json::Value::Null,
        };

        assert_eq!(
            pairing_nonce_for_authentication(&request, false).as_deref(),
            Some("consumed")
        );
        assert_eq!(pairing_nonce_for_authentication(&request, true), None);
    }

    fn ipc_response(accepted: bool, error: Option<&str>) -> IpcResponse {
        IpcResponse {
            version: PROTOCOL_VERSION,
            schema_version: SCHEMA_VERSION,
            request_id: "request".to_string(),
            accepted,
            error: error.map(str::to_string),
            payload: serde_json::json!({ "accepted": true }),
        }
    }

    #[test]
    fn logical_app_rejections_preserve_typed_native_states() {
        assert_eq!(
            classify_ipc_response(
                ipc_response(false, Some("capture-not-authorized")),
                "request"
            ),
            Err(ForwardFailure::Denied)
        );
        assert_eq!(
            classify_ipc_response(
                ipc_response(false, Some("invalid-browser-context")),
                "request"
            ),
            Err(ForwardFailure::Malformed)
        );
        assert_eq!(
            classify_ipc_response(ipc_response(false, None), "request"),
            Err(ForwardFailure::Denied)
        );
    }

    #[test]
    fn malformed_app_envelopes_are_not_reported_as_disconnects() {
        let mut mismatched = ipc_response(true, None);
        mismatched.request_id = "other".to_string();
        assert_eq!(
            classify_ipc_response(mismatched, "request"),
            Err(ForwardFailure::Malformed)
        );
        let mut unsupported = ipc_response(true, None);
        unsupported.version += 1;
        assert_eq!(
            classify_ipc_response(unsupported, "request"),
            Err(ForwardFailure::Malformed)
        );
    }
}
