//! Loopback Connect server for `bundlewhy.v1.AnalysisService`.
//!
//! Binds 127.0.0.1 on an OS-assigned port. Every RPC requires the per-run
//! bearer token, a `Host` equal to the bound address and an `Origin` equal to
//! the server origin. No method accepts a filesystem path.

use std::{
    convert::Infallible,
    net::SocketAddr,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::Instant,
};

use bytes::Bytes;
use http_body_util::{BodyExt, Full, Limited};
use hyper::{
    Method, Request, Response, StatusCode,
    body::Incoming,
    header::{self, HeaderValue},
    server::conn::http1,
    service::service_fn,
};
use hyper_util::rt::TokioIo;
use serde::Serialize;
use tokio::net::TcpListener;

use crate::{
    adapter::CancelSignal,
    bundle::{self, BUNDLE_FILE, ResultBundle},
    error::{BwError, ErrorCode},
    export,
    model::{Analysis, ReportMode, RunProgress, RunStage, RunStatus, RunSummary, SourceFile, rpc},
};

const SERVICE_PREFIX: &str = "/bundlewhy.v1.AnalysisService/";
const MAX_REQUEST_BYTES: usize = 64 * 1024;

pub struct Served {
    pub progress: RunProgress,
    pub analysis: Option<Analysis>,
    pub sources: Vec<SourceFile>,
    pub run_dir: PathBuf,
    pub out_dir: PathBuf,
    pub started: Instant,
}

pub struct ServerState {
    pub served: Mutex<Served>,
    pub token: String,
    pub cancel: Arc<CancelSignal>,
    pub ui_html: String,
    origin: Mutex<String>,
}

impl ServerState {
    pub fn new(served: Served, cancel: Arc<CancelSignal>) -> Arc<Self> {
        Arc::new(Self {
            served: Mutex::new(served),
            token: new_token(),
            cancel,
            ui_html: export::UI_HTML.to_owned(),
            origin: Mutex::new(String::new()),
        })
    }

    pub fn update(&self, apply: impl FnOnce(&mut Served)) {
        if let Ok(mut served) = self.served.lock() {
            apply(&mut served);
        }
    }

    fn origin(&self) -> String {
        self.origin
            .lock()
            .map(|origin| origin.clone())
            .unwrap_or_default()
    }
}

pub fn new_token() -> String {
    let mut bytes = [0u8; 32];
    getrandom::fill(&mut bytes).expect("operating system random source");
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}

/// Connect error codes used by the service.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum ConnectCode {
    InvalidArgument,
    NotFound,
    FailedPrecondition,
    Unauthenticated,
    PermissionDenied,
    Unimplemented,
    Internal,
}

impl ConnectCode {
    const fn as_str(self) -> &'static str {
        match self {
            Self::InvalidArgument => "invalid_argument",
            Self::NotFound => "not_found",
            Self::FailedPrecondition => "failed_precondition",
            Self::Unauthenticated => "unauthenticated",
            Self::PermissionDenied => "permission_denied",
            Self::Unimplemented => "unimplemented",
            Self::Internal => "internal",
        }
    }

    const fn status(self) -> StatusCode {
        match self {
            Self::InvalidArgument | Self::FailedPrecondition => StatusCode::BAD_REQUEST,
            Self::NotFound => StatusCode::NOT_FOUND,
            Self::Unauthenticated => StatusCode::UNAUTHORIZED,
            Self::PermissionDenied => StatusCode::FORBIDDEN,
            Self::Unimplemented => StatusCode::NOT_IMPLEMENTED,
            Self::Internal => StatusCode::INTERNAL_SERVER_ERROR,
        }
    }
}

struct RpcError {
    code: ConnectCode,
    message: String,
}

impl RpcError {
    fn new(code: ConnectCode, error_code: ErrorCode, message: impl Into<String>) -> Self {
        Self {
            code,
            message: format!("{}: {}", error_code.as_str(), message.into()),
        }
    }

    fn from_bw(code: ConnectCode, error: &BwError) -> Self {
        Self {
            code,
            message: format!(
                "{}: {} {}",
                error.code.as_str(),
                error.message,
                error.recovery
            ),
        }
    }
}

fn base_response(status: StatusCode, content_type: &str, body: Bytes) -> Response<Full<Bytes>> {
    let mut response = Response::new(Full::new(body));
    *response.status_mut() = status;
    let headers = response.headers_mut();
    headers.insert(
        header::CONTENT_TYPE,
        HeaderValue::from_str(content_type).unwrap_or(HeaderValue::from_static("text/plain")),
    );
    headers.insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    headers.insert(
        "x-content-type-options",
        HeaderValue::from_static("nosniff"),
    );
    headers.insert("referrer-policy", HeaderValue::from_static("no-referrer"));
    headers.insert(
        "cross-origin-resource-policy",
        HeaderValue::from_static("same-origin"),
    );
    response
}

fn error_response(error: &RpcError) -> Response<Full<Bytes>> {
    let body =
        serde_json::json!({ "code": error.code.as_str(), "message": error.message }).to_string();
    base_response(error.code.status(), "application/json", Bytes::from(body))
}

fn json_response(value: &impl Serialize) -> Response<Full<Bytes>> {
    match serde_json::to_vec(value) {
        Ok(body) => base_response(StatusCode::OK, "application/json", Bytes::from(body)),
        Err(error) => error_response(&RpcError::new(
            ConnectCode::Internal,
            ErrorCode::ResultInvalid,
            error.to_string(),
        )),
    }
}

fn header_str(request: &Request<Incoming>, name: header::HeaderName) -> Option<&str> {
    request
        .headers()
        .get(name)
        .and_then(|value| value.to_str().ok())
}

/// Constant-time comparison for the bearer token.
fn token_matches(provided: &str, expected: &str) -> bool {
    provided.len() == expected.len()
        && provided
            .bytes()
            .zip(expected.bytes())
            .fold(0u8, |acc, (left, right)| acc | (left ^ right))
            == 0
}

/// Validates Host, Origin and the bearer token. Returns the denial reason.
pub fn authorize(
    host: Option<&str>,
    origin: Option<&str>,
    authorization: Option<&str>,
    expected_origin: &str,
    token: &str,
) -> Result<(), String> {
    let expected_host = expected_origin.trim_start_matches("http://");
    if host != Some(expected_host) {
        return Err("Host does not match the bundlewhy server address".to_owned());
    }
    if origin != Some(expected_origin) {
        return Err("Origin does not match the bundlewhy server origin".to_owned());
    }
    let provided = authorization
        .and_then(|value| value.strip_prefix("Bearer "))
        .unwrap_or("");
    if !token_matches(provided, token) {
        return Err("Missing or invalid access token".to_owned());
    }
    Ok(())
}

fn progress_snapshot(served: &Served) -> RunProgress {
    let mut progress = served.progress.clone();
    if progress.status == RunStatus::Running {
        progress.elapsed_ms =
            u32::try_from(served.started.elapsed().as_millis()).unwrap_or(u32::MAX);
    }
    progress
}

fn list_runs(out_dir: &Path, active: &str) -> Vec<RunSummary> {
    let Ok(entries) = std::fs::read_dir(out_dir) else {
        return Vec::new();
    };
    let mut runs: Vec<RunSummary> = entries
        .filter_map(Result::ok)
        .filter_map(|entry| {
            let name = entry.file_name().to_string_lossy().into_owned();
            uuid::Uuid::parse_str(&name).ok()?;
            // Saved runs are untrusted: read only bounded regular files.
            let bytes = bundle::read_bundle_bytes(&entry.path().join(BUNDLE_FILE)).ok()?;
            let value: serde_json::Value = serde_json::from_slice(&bytes).ok()?;
            let analysis = value.get("analysis")?;
            let field = |key: &str| {
                analysis
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_owned()
            };
            Some(RunSummary {
                run_id: name.clone(),
                status: serde_json::from_value(analysis.get("status")?.clone()).unwrap_or_default(),
                project_name: field("projectName"),
                bundler: analysis
                    .get("engine")
                    .and_then(|engine| engine.get("bundler"))
                    .and_then(|bundler| serde_json::from_value(bundler.clone()).ok())
                    .unwrap_or_default(),
                started_at: field("startedAt"),
                active: name == active,
            })
        })
        .collect();
    runs.sort_by(|left, right| right.run_id.cmp(&left.run_id));
    runs.truncate(200);
    runs
}

async fn read_body<T: serde::de::DeserializeOwned + Default>(
    request: Request<Incoming>,
) -> Result<T, RpcError> {
    let body = Limited::new(request.into_body(), MAX_REQUEST_BYTES)
        .collect()
        .await
        .map_err(|_| {
            RpcError::new(
                ConnectCode::InvalidArgument,
                ErrorCode::Usage,
                "Request body is too large or unreadable",
            )
        })?
        .to_bytes();
    if body.is_empty() {
        return Ok(T::default());
    }
    serde_json::from_slice(&body).map_err(|error| {
        RpcError::new(
            ConnectCode::InvalidArgument,
            ErrorCode::Usage,
            format!("Invalid request: {error}"),
        )
    })
}

async fn handle_rpc(
    state: &ServerState,
    method: &str,
    request: Request<Incoming>,
) -> Result<Response<Full<Bytes>>, RpcError> {
    match method {
        "GetRunProgress" => {
            let _: rpc::Empty = read_body(request).await?;
            let served = state.served.lock().map_err(|_| poisoned())?;
            Ok(json_response(&rpc::ProgressResponse {
                progress: progress_snapshot(&served),
            }))
        }
        "GetAnalysis" => {
            let _: rpc::Empty = read_body(request).await?;
            let served = state.served.lock().map_err(|_| poisoned())?;
            let analysis = served.analysis.clone().ok_or_else(|| {
                RpcError::new(
                    ConnectCode::FailedPrecondition,
                    ErrorCode::ResultInvalid,
                    "The analysis is not available yet",
                )
            })?;
            Ok(json_response(&rpc::GetAnalysisResponse { analysis }))
        }
        "GetSourceFile" => {
            let body: rpc::GetSourceFileRequest = read_body(request).await?;
            let served = state.served.lock().map_err(|_| poisoned())?;
            let source = served
                .sources
                .iter()
                .find(|source| source.id == body.source_id)
                .cloned()
                .ok_or_else(|| {
                    RpcError::new(
                        ConnectCode::NotFound,
                        ErrorCode::SourceNotFound,
                        "The source is not part of this analysis",
                    )
                })?;
            Ok(json_response(&rpc::GetSourceFileResponse { source }))
        }
        "CancelRun" => {
            let _: rpc::Empty = read_body(request).await?;
            let progress = {
                let mut served = state.served.lock().map_err(|_| poisoned())?;
                if served.progress.mode != ReportMode::Live
                    || served.progress.status != RunStatus::Running
                {
                    return Err(RpcError::new(
                        ConnectCode::FailedPrecondition,
                        ErrorCode::Usage,
                        "No run is in progress",
                    ));
                }
                served.progress.message = "Cancelling…".to_owned();
                served.progress.cancellable = false;
                progress_snapshot(&served)
            };
            tracing::warn!("cancellation requested from the UI");
            state.cancel.cancel();
            Ok(json_response(&rpc::ProgressResponse { progress }))
        }
        "ListRuns" => {
            let _: rpc::Empty = read_body(request).await?;
            let (out_dir, active) = {
                let served = state.served.lock().map_err(|_| poisoned())?;
                (served.out_dir.clone(), served.progress.run_id.clone())
            };
            Ok(json_response(&rpc::ListRunsResponse {
                runs: list_runs(&out_dir, &active),
            }))
        }
        "SelectRun" => {
            let body: rpc::SelectRunRequest = read_body(request).await?;
            let run_id = uuid::Uuid::parse_str(&body.run_id)
                .map_err(|_| {
                    RpcError::new(
                        ConnectCode::InvalidArgument,
                        ErrorCode::Usage,
                        "runId must be a UUID",
                    )
                })?
                .to_string();
            let out_dir = {
                let served = state.served.lock().map_err(|_| poisoned())?;
                if served.progress.status == RunStatus::Running {
                    return Err(RpcError::new(
                        ConnectCode::FailedPrecondition,
                        ErrorCode::Usage,
                        "Wait for the active run to finish",
                    ));
                }
                served.out_dir.clone()
            };
            // A UUID-named symlink must not redirect the served run (and later
            // exports) outside the output directory.
            let escape = || {
                RpcError::new(
                    ConnectCode::InvalidArgument,
                    ErrorCode::Usage,
                    "The selected run is not inside the output directory",
                )
            };
            let canonical_out = std::fs::canonicalize(&out_dir).map_err(|_| escape())?;
            let run_dir = std::fs::canonicalize(out_dir.join(&run_id)).map_err(|_| escape())?;
            if run_dir.parent() != Some(canonical_out.as_path()) {
                return Err(escape());
            }
            let loaded = bundle::load(&run_dir)
                .map_err(|error| RpcError::from_bw(ConnectCode::FailedPrecondition, &error))?;
            let progress = serve_bundle(state, loaded, run_dir);
            tracing::info!(run_id = %run_id, "selected saved run");
            Ok(json_response(&rpc::ProgressResponse { progress }))
        }
        "ExportReport" => {
            let body: rpc::ExportReportRequest = read_body(request).await?;
            let (analysis, sources, run_dir) = {
                let served = state.served.lock().map_err(|_| poisoned())?;
                if served.progress.status == RunStatus::Running {
                    return Err(RpcError::new(
                        ConnectCode::FailedPrecondition,
                        ErrorCode::ExportFailed,
                        "Wait for the run to finish before exporting",
                    ));
                }
                let analysis = served.analysis.clone().ok_or_else(|| {
                    RpcError::new(
                        ConnectCode::FailedPrecondition,
                        ErrorCode::ExportFailed,
                        "No analysis to export",
                    )
                })?;
                (analysis, served.sources.clone(), served.run_dir.clone())
            };
            let html = export::render(&state.ui_html, &analysis, &sources, body.include_sources);
            // A run analyzed with --exclude-sources has no content to include,
            // whatever the request asked for.
            let sources_included = body.include_sources
                && sources
                    .iter()
                    .any(|source| source.content_included && !source.content.is_empty());
            let file_name = format!(
                "report-{}{}.html",
                chrono::Utc::now().format("%Y%m%dT%H%M%SZ"),
                if body.include_sources {
                    ""
                } else {
                    "-no-sources"
                }
            );
            std::fs::write(run_dir.join(&file_name), &html).map_err(|error| {
                RpcError::new(
                    ConnectCode::Internal,
                    ErrorCode::ExportFailed,
                    format!("Could not write the report: {error}"),
                )
            })?;
            tracing::info!(file = %file_name, sources = sources_included, bytes = html.len(), "exported report");
            Ok(json_response(&rpc::ExportReportResponse {
                file_name,
                sources_included,
                size_bytes: u32::try_from(html.len()).unwrap_or(u32::MAX),
            }))
        }
        _ => Err(RpcError::new(
            ConnectCode::Unimplemented,
            ErrorCode::Usage,
            format!("Unknown method {method}"),
        )),
    }
}

fn poisoned() -> RpcError {
    RpcError::new(
        ConnectCode::Internal,
        ErrorCode::ResultInvalid,
        "Server state is unavailable",
    )
}

/// Replaces the served analysis with a saved bundle.
pub fn serve_bundle(state: &ServerState, loaded: ResultBundle, run_dir: PathBuf) -> RunProgress {
    let mut analysis = loaded.analysis;
    analysis.mode = ReportMode::Saved;
    let progress = RunProgress {
        run_id: analysis.run_id.clone(),
        status: analysis.status,
        stage: RunStage::Finished,
        message: String::new(),
        elapsed_ms: analysis.duration_ms,
        mode: ReportMode::Saved,
        cancellable: false,
    };
    state.update(|served| {
        served.progress = progress.clone();
        served.analysis = Some(analysis);
        served.sources = loaded.sources;
        served.run_dir = run_dir;
    });
    progress
}

async fn handle(
    state: Arc<ServerState>,
    request: Request<Incoming>,
) -> Result<Response<Full<Bytes>>, Infallible> {
    let started = Instant::now();
    let path = request.uri().path().to_owned();
    let method = request.method().clone();
    let expected_origin = state.origin();
    let expected_host = expected_origin.trim_start_matches("http://").to_owned();
    let host_ok = header_str(&request, header::HOST) == Some(expected_host.as_str());

    let response = if method == Method::GET && (path == "/" || path == "/index.html") {
        if host_ok {
            let mut response = base_response(
                StatusCode::OK,
                "text/html; charset=utf-8",
                Bytes::from(state.ui_html.clone()),
            );
            let csp = export::content_security_policy(&state.ui_html, true);
            if let Ok(value) = HeaderValue::from_str(&csp) {
                response
                    .headers_mut()
                    .insert(header::CONTENT_SECURITY_POLICY, value);
            }
            response
                .headers_mut()
                .insert("x-frame-options", HeaderValue::from_static("DENY"));
            response
        } else {
            base_response(
                StatusCode::FORBIDDEN,
                "text/plain",
                Bytes::from_static(b"BW_ACCESS_DENIED: unexpected Host"),
            )
        }
    } else if let Some(rpc_method) = path.strip_prefix(SERVICE_PREFIX) {
        if method == Method::POST {
            let rpc_method = rpc_method.to_owned();
            let denial = authorize(
                header_str(&request, header::HOST),
                header_str(&request, header::ORIGIN),
                header_str(&request, header::AUTHORIZATION),
                &expected_origin,
                &state.token,
            );
            let content_type_ok = header_str(&request, header::CONTENT_TYPE)
                .is_some_and(|value| value.starts_with("application/json"));
            match denial {
                Err(reason) => {
                    tracing::warn!(method = %rpc_method, %reason, "rejected request");
                    error_response(&RpcError::new(
                        ConnectCode::Unauthenticated,
                        ErrorCode::AccessDenied,
                        reason,
                    ))
                }
                Ok(()) if !content_type_ok => error_response(&RpcError::new(
                    ConnectCode::InvalidArgument,
                    ErrorCode::Usage,
                    "Only application/json Connect requests are supported",
                )),
                Ok(()) => match handle_rpc(&state, &rpc_method, request).await {
                    Ok(response) => response,
                    Err(error) => error_response(&error),
                },
            }
        } else {
            base_response(
                StatusCode::METHOD_NOT_ALLOWED,
                "text/plain",
                Bytes::from_static(b"method not allowed"),
            )
        }
    } else {
        base_response(
            StatusCode::NOT_FOUND,
            "text/plain",
            Bytes::from_static(b"not found"),
        )
    };
    tracing::debug!(%method, %path, status = response.status().as_u16(), duration_ms = started.elapsed().as_millis() as u64, "request");
    Ok(response)
}

pub struct RunningServer {
    pub addr: SocketAddr,
    pub url: String,
}

/// Binds 127.0.0.1 on an available port and serves until the process exits.
pub async fn start(state: Arc<ServerState>) -> Result<RunningServer, BwError> {
    let listener = TcpListener::bind(("127.0.0.1", 0)).await.map_err(|error| {
        BwError::new(
            ErrorCode::ServerBindFailed,
            format!("Could not bind 127.0.0.1: {error}"),
            "Check local firewall or sandbox settings, or use --no-serve.",
        )
    })?;
    let addr = listener.local_addr().map_err(|error| {
        BwError::new(
            ErrorCode::ServerBindFailed,
            error.to_string(),
            "Retry the command.",
        )
    })?;
    let origin = format!("http://{addr}");
    if let Ok(mut stored) = state.origin.lock() {
        stored.clone_from(&origin);
    }
    let url = format!("{origin}/#token={}", state.token);
    tokio::spawn(async move {
        loop {
            let Ok((stream, peer)) = listener.accept().await else {
                continue;
            };
            if !peer.ip().is_loopback() {
                continue;
            }
            let state = state.clone();
            tokio::spawn(async move {
                let service = service_fn(move |request| handle(state.clone(), request));
                if let Err(error) = http1::Builder::new()
                    .serve_connection(TokioIo::new(stream), service)
                    .await
                {
                    tracing::debug!(%error, "connection closed with error");
                }
            });
        }
    });
    tracing::info!(%addr, "local server listening");
    Ok(RunningServer { addr, url })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn authorization_requires_host_origin_and_token() {
        let origin = "http://127.0.0.1:4000";
        let token = "a".repeat(64);
        let bearer = format!("Bearer {token}");
        assert!(
            authorize(
                Some("127.0.0.1:4000"),
                Some(origin),
                Some(&bearer),
                origin,
                &token
            )
            .is_ok()
        );
        assert!(
            authorize(
                Some("evil.test:4000"),
                Some(origin),
                Some(&bearer),
                origin,
                &token
            )
            .is_err()
        );
        assert!(
            authorize(
                Some("127.0.0.1:4000"),
                Some("http://evil.test"),
                Some(&bearer),
                origin,
                &token
            )
            .is_err()
        );
        assert!(authorize(Some("127.0.0.1:4000"), None, Some(&bearer), origin, &token).is_err());
        assert!(
            authorize(
                Some("127.0.0.1:4000"),
                Some(origin),
                Some("Bearer wrong"),
                origin,
                &token
            )
            .is_err()
        );
        assert!(authorize(Some("127.0.0.1:4000"), Some(origin), None, origin, &token).is_err());
    }

    #[test]
    fn tokens_are_256_bit_and_unique() {
        let first = new_token();
        assert_eq!(first.len(), 64);
        assert_ne!(first, new_token());
    }
}
