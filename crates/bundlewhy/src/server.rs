//! Loopback Connect server for `bundlewhy.v1.AnalysisService`.
//!
//! Binds 127.0.0.1 on an OS-assigned port. Every RPC requires the per-run
//! bearer token, a `Host` equal to the bound address and an `Origin` equal to
//! the server origin. No method accepts a filesystem path.

use std::{
    convert::Infallible,
    future::Future,
    io,
    net::SocketAddr,
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant},
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
const ACCEPT_RETRY_DELAY: Duration = Duration::from_millis(250);

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

/// Creates a new report file without following or replacing existing entries.
/// A saved run directory may contain symlinks with predictable report names;
/// exclusive creation refuses them and a unique suffix is tried. Only a file
/// created by this call is removed after a failed write.
fn write_new_report_with(
    run_dir: &Path,
    file_name: &str,
    bytes: &[u8],
    mut write: impl FnMut(&mut std::fs::File, &[u8]) -> std::io::Result<()>,
    mut cleanup: impl FnMut(&Path) -> std::io::Result<()>,
) -> std::io::Result<String> {
    let stem = file_name.trim_end_matches(".html");
    for attempt in 0..100u32 {
        let candidate = if attempt == 0 {
            file_name.to_owned()
        } else {
            format!("{stem}-{attempt}.html")
        };
        let path = run_dir.join(&candidate);
        match std::fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .open(&path)
        {
            Ok(mut file) => {
                if let Err(write_error) = write(&mut file, bytes) {
                    drop(file);
                    if let Err(cleanup_error) = cleanup(&path) {
                        return Err(std::io::Error::new(
                            write_error.kind(),
                            format!(
                                "{write_error}; could not remove incomplete report {}: \
                                 {cleanup_error}",
                                path.display()
                            ),
                        ));
                    }
                    return Err(write_error);
                }
                return Ok(candidate);
            }
            Err(error) if error.kind() == std::io::ErrorKind::AlreadyExists => {}
            Err(error) => return Err(error),
        }
    }
    Err(std::io::Error::new(
        std::io::ErrorKind::AlreadyExists,
        "no unused report file name",
    ))
}

fn write_new_report(run_dir: &Path, file_name: &str, bytes: &[u8]) -> std::io::Result<String> {
    use std::io::Write;
    write_new_report_with(
        run_dir,
        file_name,
        bytes,
        |file, bytes| file.write_all(bytes).and_then(|()| file.sync_all()),
        |path| std::fs::remove_file(path),
    )
}

fn list_runs(out_dir: &Path, active: &str) -> Vec<RunSummary> {
    let Ok(entries) = std::fs::read_dir(out_dir) else {
        return Vec::new();
    };
    // UUID v7 names sort by creation time: select the newest 200 run
    // directories before reading anything inside them.
    let mut names: Vec<String> = entries
        .filter_map(Result::ok)
        // A UUID-named symlink could point outside the output directory;
        // list only real directories.
        .filter(|entry| entry.file_type().is_ok_and(|kind| kind.is_dir()))
        .map(|entry| entry.file_name().to_string_lossy().into_owned())
        .filter(|name| uuid::Uuid::parse_str(name).is_ok())
        .collect();
    names.sort_by(|left, right| right.cmp(left));
    names.truncate(200);
    names
        .into_iter()
        .filter_map(|name| {
            let run_dir = out_dir.join(&name);
            // Prefer the small summary; fall back to the bounded full bundle
            // for runs saved without one.
            let summary = bundle::read_summary_bytes(&run_dir.join(bundle::SUMMARY_FILE))
                .ok()
                .and_then(|bytes| serde_json::from_slice::<serde_json::Value>(&bytes).ok());
            let value = match summary {
                Some(summary) => summary,
                None => {
                    let bytes = bundle::read_bundle_bytes(&run_dir.join(BUNDLE_FILE)).ok()?;
                    let full: serde_json::Value = serde_json::from_slice(&bytes).ok()?;
                    let analysis = full.get("analysis")?;
                    serde_json::json!({
                        "status": analysis.get("status"),
                        "projectName": analysis.get("projectName"),
                        "bundler": analysis.get("engine").and_then(|engine| engine.get("bundler")),
                        "startedAt": analysis.get("startedAt"),
                    })
                }
            };
            let field = |key: &str| {
                value
                    .get(key)
                    .and_then(serde_json::Value::as_str)
                    .unwrap_or_default()
                    .to_owned()
            };
            Some(RunSummary {
                run_id: name.clone(),
                // Same interrupted/incomplete normalization as opening the run.
                status: bundle::normalized_status(
                    value
                        .get("status")
                        .and_then(|status| serde_json::from_value(status.clone()).ok())
                        .unwrap_or_default(),
                    &run_dir,
                ),
                project_name: field("projectName"),
                bundler: value
                    .get("bundler")
                    .and_then(|bundler| serde_json::from_value(bundler.clone()).ok())
                    .unwrap_or_default(),
                started_at: field("startedAt"),
                active: name == active,
            })
        })
        .collect()
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
            let file_name =
                write_new_report(&run_dir, &file_name, html.as_bytes()).map_err(|error| {
                    tracing::warn!(%error, "report export failed");
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

async fn accept_with_backoff<T, Accept, AcceptFuture, Delay, DelayFuture>(
    addr: SocketAddr,
    mut accept: Accept,
    mut delay: Delay,
) -> T
where
    Accept: FnMut() -> AcceptFuture,
    AcceptFuture: Future<Output = io::Result<T>>,
    Delay: FnMut() -> DelayFuture,
    DelayFuture: Future<Output = ()>,
{
    loop {
        match accept().await {
            Ok(connection) => return connection,
            Err(error) => {
                tracing::warn!(
                    %addr,
                    %error,
                    retry_delay_ms = ACCEPT_RETRY_DELAY.as_millis() as u64,
                    "listener accept failed"
                );
                delay().await;
            }
        }
    }
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
            let (stream, peer) = accept_with_backoff(
                addr,
                || listener.accept(),
                || tokio::time::sleep(ACCEPT_RETRY_DELAY),
            )
            .await;
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
    use std::{
        cell::Cell,
        future::{poll_fn, ready},
        io::Write,
        pin::pin,
        task::{Context, Poll, Waker},
    };

    use tokio::io::{AsyncReadExt, AsyncWriteExt};

    use super::*;

    #[derive(Clone)]
    struct LogWriter(Arc<Mutex<Vec<u8>>>);

    impl Write for LogWriter {
        fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
            self.0.lock().unwrap().extend_from_slice(bytes);
            Ok(bytes.len())
        }

        fn flush(&mut self) -> io::Result<()> {
            Ok(())
        }
    }

    fn capture_logs(logs: Arc<Mutex<Vec<u8>>>) -> tracing::subscriber::DefaultGuard {
        let subscriber = tracing_subscriber::fmt()
            .with_ansi(false)
            .with_max_level(tracing::Level::DEBUG)
            .with_writer(move || LogWriter(logs.clone()))
            .finish();
        tracing::subscriber::set_default(subscriber)
    }

    #[test]
    fn accept_error_retries_are_delayed_and_logged() {
        let logs = Arc::new(Mutex::new(Vec::new()));
        let _subscriber = capture_logs(logs.clone());
        let addr: SocketAddr = "127.0.0.1:4000".parse().unwrap();
        let attempts = Cell::new(0);
        let delays = Cell::new(0);
        let allow_retry = Cell::new(false);
        let future = accept_with_backoff(
            addr,
            || {
                attempts.set(attempts.get() + 1);
                ready(if attempts.get() <= 3 {
                    Err(io::Error::other("injected accept failure"))
                } else {
                    Ok(7)
                })
            },
            || {
                delays.set(delays.get() + 1);
                poll_fn(|_| {
                    if allow_retry.get() {
                        Poll::Ready(())
                    } else {
                        Poll::Pending
                    }
                })
            },
        );
        let mut future = pin!(future);
        let mut context = Context::from_waker(Waker::noop());
        for _ in 0..10 {
            assert!(matches!(future.as_mut().poll(&mut context), Poll::Pending));
        }
        assert_eq!(attempts.get(), 1);
        assert_eq!(delays.get(), 1);
        allow_retry.set(true);
        assert!(matches!(future.as_mut().poll(&mut context), Poll::Ready(7)));
        assert_eq!(attempts.get(), 4);
        assert_eq!(delays.get(), 3);
        let output = String::from_utf8(logs.lock().unwrap().clone()).unwrap();
        assert_eq!(output.matches("listener accept failed").count(), 3);
        assert!(output.contains("injected accept failure"));
        assert!(output.contains("127.0.0.1:4000"));
        assert!(output.contains("retry_delay_ms=250"));
    }

    #[tokio::test]
    async fn accept_error_recovers_after_transient_failure() {
        let addr: SocketAddr = "127.0.0.1:4000".parse().unwrap();
        let attempts = Cell::new(0);
        let delays = Cell::new(0);
        let accepted = accept_with_backoff(
            addr,
            || {
                attempts.set(attempts.get() + 1);
                ready(if attempts.get() == 1 {
                    Err(io::Error::other("transient accept failure"))
                } else {
                    Ok(7)
                })
            },
            || {
                delays.set(delays.get() + 1);
                ready(())
            },
        )
        .await;
        assert_eq!(accepted, 7);
        assert_eq!(attempts.get(), 2);
        assert_eq!(delays.get(), 1);
    }

    #[tokio::test]
    async fn accept_error_preserves_loopback_rpc_without_logging_token() {
        let logs = Arc::new(Mutex::new(Vec::new()));
        let _subscriber = capture_logs(logs.clone());
        let state = ServerState::new(
            Served {
                progress: RunProgress::default(),
                analysis: None,
                sources: Vec::new(),
                run_dir: PathBuf::new(),
                out_dir: PathBuf::new(),
                started: Instant::now(),
            },
            Arc::new(CancelSignal::default()),
        );
        let token = state.token.clone();
        let running = start(state).await.unwrap();
        let origin = format!("http://{}", running.addr);
        let request = format!(
            "POST /bundlewhy.v1.AnalysisService/GetRunProgress HTTP/1.1\r\nHost: {}\r\nOrigin: \
             {origin}\r\nAuthorization: Bearer {token}\r\nContent-Type: \
             application/json\r\nContent-Length: 2\r\nConnection: close\r\n\r\n{{}}",
            running.addr
        );
        let mut stream = tokio::net::TcpStream::connect(running.addr).await.unwrap();
        stream.write_all(request.as_bytes()).await.unwrap();
        let mut response = Vec::new();
        tokio::time::timeout(Duration::from_secs(2), stream.read_to_end(&mut response))
            .await
            .unwrap()
            .unwrap();
        let response = String::from_utf8(response).unwrap();
        assert!(response.starts_with("HTTP/1.1 200"), "{response}");
        assert!(response.contains("\"progress\""));
        let output = String::from_utf8(logs.lock().unwrap().clone()).unwrap();
        assert!(!output.contains(&token));
    }

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

    #[cfg(unix)]
    #[test]
    fn run_listing_skips_symlinked_run_directories() {
        let out = tempfile::tempdir().unwrap();
        let outside = tempfile::tempdir().unwrap();
        let id = uuid::Uuid::now_v7().to_string();
        std::fs::write(
            outside.path().join(bundle::SUMMARY_FILE),
            r#"{"status":"RUN_STATUS_COMPLETE","projectName":"outside"}"#,
        )
        .unwrap();
        std::os::unix::fs::symlink(outside.path(), out.path().join(&id)).unwrap();
        assert!(list_runs(out.path(), "").is_empty());
        let real = uuid::Uuid::now_v7().to_string();
        std::fs::create_dir(out.path().join(&real)).unwrap();
        std::fs::write(
            out.path().join(&real).join(bundle::SUMMARY_FILE),
            r#"{"status":"RUN_STATUS_COMPLETE","projectName":"inside"}"#,
        )
        .unwrap();
        let runs = list_runs(out.path(), "");
        assert_eq!(runs.len(), 1);
        assert_eq!(runs[0].project_name, "inside");
    }

    #[cfg(unix)]
    #[test]
    fn server_report_export_never_follows_existing_symlinks() {
        let dir = tempfile::tempdir().unwrap();
        let outside = dir.path().join("outside.txt");
        std::fs::write(&outside, "keep").unwrap();
        let run = dir.path().join("run");
        std::fs::create_dir(&run).unwrap();
        std::os::unix::fs::symlink(&outside, run.join("report-x.html")).unwrap();
        let name = write_new_report(&run, "report-x.html", b"<html>").unwrap();
        assert_eq!(name, "report-x-1.html");
        assert_eq!(std::fs::read_to_string(&outside).unwrap(), "keep");
        assert_eq!(std::fs::read(run.join(&name)).unwrap(), b"<html>");
    }

    #[test]
    fn server_report_export_preserves_existing_regular_file() {
        let run = tempfile::tempdir().unwrap();
        let original = run.path().join("report-x.html");
        std::fs::write(&original, b"original").unwrap();

        let name = write_new_report(run.path(), "report-x.html", b"<html>").unwrap();
        assert_eq!(name, "report-x-1.html");
        assert_eq!(std::fs::read(original).unwrap(), b"original");
        assert_eq!(std::fs::read(run.path().join(name)).unwrap(), b"<html>");
    }

    #[test]
    fn server_report_export_removes_partial_write_and_reuses_name() {
        use std::io::{Error, ErrorKind, Write};

        let run = tempfile::tempdir().unwrap();
        let report = run.path().join("report-x.html");
        let error = write_new_report_with(
            run.path(),
            "report-x.html",
            b"<html>complete</html>",
            |file, _| {
                file.write_all(b"<html>")?;
                Err(Error::new(ErrorKind::WriteZero, "injected write failure"))
            },
            |path| std::fs::remove_file(path),
        )
        .unwrap_err();
        assert_eq!(error.kind(), ErrorKind::WriteZero);
        assert!(error.to_string().contains("injected write failure"));
        assert!(!report.exists());

        let name = write_new_report(run.path(), "report-x.html", b"<html>complete</html>").unwrap();
        assert_eq!(name, "report-x.html");
        assert_eq!(std::fs::read(report).unwrap(), b"<html>complete</html>");
    }

    #[test]
    fn server_report_export_removes_file_after_finalization_failure() {
        use std::io::{Error, ErrorKind, Write};

        let run = tempfile::tempdir().unwrap();
        let report = run.path().join("report-x.html");
        let error = write_new_report_with(
            run.path(),
            "report-x.html",
            b"<html>",
            |file, bytes| {
                file.write_all(bytes)?;
                Err(Error::other("injected finalization failure"))
            },
            |path| std::fs::remove_file(path),
        )
        .unwrap_err();
        assert_eq!(error.kind(), ErrorKind::Other);
        assert!(!report.exists());
    }

    #[test]
    fn server_report_export_keeps_write_error_when_cleanup_fails() {
        use std::io::{Error, ErrorKind, Write};

        let run = tempfile::tempdir().unwrap();
        let report = run.path().join("report-x.html");
        let error = write_new_report_with(
            run.path(),
            "report-x.html",
            b"<html>complete</html>",
            |file, _| {
                file.write_all(b"<html>")?;
                Err(Error::new(ErrorKind::WriteZero, "injected write failure"))
            },
            |_| {
                Err(Error::new(
                    ErrorKind::PermissionDenied,
                    "injected cleanup failure",
                ))
            },
        )
        .unwrap_err();
        assert_eq!(error.kind(), ErrorKind::WriteZero);
        assert!(error.to_string().contains("injected write failure"));
        assert!(error.to_string().contains("injected cleanup failure"));
        assert!(error.to_string().contains(&report.display().to_string()));
        assert_eq!(std::fs::read(report).unwrap(), b"<html>");
    }

    #[test]
    fn tokens_are_256_bit_and_unique() {
        let first = new_token();
        assert_eq!(first.len(), 64);
        assert_ne!(first, new_token());
    }
}
