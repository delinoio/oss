#![doc = include_str!("../README.md")]

mod types;
use std::{fmt, time::Duration};

use reqwest::{
    StatusCode, Url,
    header::{AUTHORIZATION, HeaderValue, RETRY_AFTER},
};
use serde::Deserialize;
use serde_json::Value;
pub use types::*;

/// Cloudflare's canonical model identifier.
pub const MODEL_ID: &str = "@cf/cloudflare/clairic";

/// Client configuration, request, transport, or upstream failure.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("invalid Clairic request or configuration: {0}")]
    Validation(String),
    #[error("Clairic HTTP transport failed")]
    Transport(#[from] reqwest::Error),
    #[error("invalid Clairic JSON response")]
    Decode(#[from] serde_json::Error),
    #[error("Cloudflare returned HTTP {status}")]
    Http {
        status: StatusCode,
        /// Raw upstream body; may contain sensitive input.
        body: String,
        retry_after: Option<String>,
    },
    #[error("Cloudflare rejected the inference request")]
    Api {
        /// Structured upstream details; may contain sensitive input.
        errors: Vec<Value>,
    },
    #[error("Cloudflare success response is missing a result")]
    MissingResult,
}

impl Error {
    pub(crate) fn validation(message: &str) -> Self {
        Self::Validation(message.into())
    }
}

/// Reusable asynchronous client. Clones share the connection pool.
#[derive(Clone)]
pub struct Client {
    http: reqwest::Client,
    endpoint: Url,
    authorization: HeaderValue,
}

impl fmt::Debug for Client {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Client").finish_non_exhaustive()
    }
}

impl Client {
    /// Create a client with a 120-second timeout and no redirects.
    pub fn new(account_id: &str, api_token: &str) -> Result<Self, Error> {
        Self::with_endpoint(
            account_id,
            api_token,
            "https://api.cloudflare.com/client/v4/",
            Duration::from_secs(120),
        )
    }

    /// Use an explicitly trusted API base URL and timeout. The base URL must
    /// end at the API prefix (normally `/client/v4/`). HTTP is allowed only
    /// on loopback.
    pub fn with_endpoint(
        account_id: &str,
        api_token: &str,
        base_url: &str,
        timeout: Duration,
    ) -> Result<Self, Error> {
        if account_id.is_empty()
            || !account_id
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
        {
            return Err(Error::validation(
                "account ID must be a nonempty identifier",
            ));
        }
        if api_token.trim().is_empty() || timeout.is_zero() {
            return Err(Error::validation("token and nonzero timeout are required"));
        }
        let mut authorization = HeaderValue::from_str(&format!("Bearer {api_token}"))
            .map_err(|_| Error::validation("invalid API token header"))?;
        authorization.set_sensitive(true);
        let mut endpoint =
            Url::parse(base_url).map_err(|_| Error::validation("invalid base URL"))?;
        let loopback = matches!(
            endpoint.host_str(),
            Some("localhost" | "127.0.0.1" | "[::1]")
        );
        if !(endpoint.scheme() == "https" || endpoint.scheme() == "http" && loopback)
            || endpoint.host_str().is_none()
            || !endpoint.username().is_empty()
            || endpoint.password().is_some()
            || endpoint.query().is_some()
            || endpoint.fragment().is_some()
        {
            return Err(Error::validation(
                "base URL requires HTTPS (or loopback HTTP), without credentials, query, or \
                 fragment",
            ));
        }
        endpoint
            .path_segments_mut()
            .map_err(|_| Error::validation("base URL cannot contain path segments"))?
            .pop_if_empty()
            .extend([
                "accounts",
                account_id,
                "ai",
                "run",
                "@cf",
                "cloudflare",
                "clairic",
            ]);
        let http = reqwest::Client::builder()
            .timeout(timeout)
            .redirect(reqwest::redirect::Policy::none())
            .user_agent(concat!("clairic-client/", env!("CARGO_PKG_VERSION")))
            .build()?;
        Ok(Self {
            http,
            endpoint,
            authorization,
        })
    }

    /// Run one inference without automatic retries. HTTP or API failures are
    /// errors, even when the upstream sends a JSON error envelope with HTTP
    /// 200.
    pub async fn run(&self, request: &Request) -> Result<Response, Error> {
        request.validate()?;
        let body = serde_json::to_vec(request)?;
        if body.len() > 16 * 1024 * 1024 {
            return Err(Error::validation("request body exceeds 16 MiB"));
        }
        tracing::debug!(
            fields = request.fields.len(),
            images = request.images.len(),
            "sending Clairic inference"
        );
        let response = self
            .http
            .post(self.endpoint.clone())
            .header(AUTHORIZATION, self.authorization.clone())
            .header(reqwest::header::CONTENT_TYPE, "application/json")
            .body(body)
            .send()
            .await?;
        let status = response.status();
        tracing::debug!(status = status.as_u16(), "received Clairic response");
        let retry_after = response
            .headers()
            .get(RETRY_AFTER)
            .and_then(|v| v.to_str().ok())
            .map(str::to_owned);
        let bytes = response.bytes().await?;
        if !status.is_success() {
            return Err(Error::Http {
                status,
                body: String::from_utf8_lossy(&bytes).into_owned(),
                retry_after,
            });
        }
        let value: Value = serde_json::from_slice(&bytes)?;
        if value.get("success").is_some() {
            let envelope: Envelope = serde_json::from_value(value)?;
            if !envelope.success || !envelope.errors.is_empty() {
                return Err(Error::Api {
                    errors: envelope.errors,
                });
            }
            serde_json::from_value(
                envelope
                    .result
                    .filter(|v| !v.is_null())
                    .ok_or(Error::MissingResult)?,
            )
            .map_err(Error::from)
        } else {
            serde_json::from_value(value).map_err(Error::from)
        }
    }
}

#[derive(Deserialize)]
struct Envelope {
    success: bool,
    result: Option<Value>,
    #[serde(default)]
    errors: Vec<Value>,
}
