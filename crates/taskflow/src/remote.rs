use anyhow::{ensure, Context, Result};
use hmac::{Hmac, Mac};
use reqwest::{Method, Url};
use sha2::Sha256;

use crate::{
    cache::{Artifact, MAX_CACHE_BYTES},
    config::{RemoteConfig, RemoteMode},
    files,
};

/// An S3-compatible GET/PUT client. Credentials are host-only and never
/// serialized.
pub struct Remote {
    config: RemoteConfig,
    key: String,
    secret: String,
    token: Option<String>,
    client: reqwest::Client,
}
impl Remote {
    #[cfg(test)]
    pub(crate) fn fixture(endpoint: String) -> Self {
        Self {
            config: serde_json::from_value(serde_json::json!({"endpoint":endpoint,"bucket":"fixture","namespace":"fixture","accessKeyEnv":"UNUSED_ACCESS","secretKeyEnv":"UNUSED_SECRET","mode":"read-write"})).unwrap(),
            key: "fixture".into(), secret: "fixture".into(), token: None,
            client: reqwest::Client::builder().timeout(std::time::Duration::from_secs(5)).build().unwrap(),
        }
    }

    pub fn new(config: &RemoteConfig) -> Result<Option<Self>> {
        config.validate()?;
        if config.mode == RemoteMode::Off || untrusted_ci() {
            return Ok(None);
        }
        Ok(Some(Self {
            config: config.clone(),
            key: std::env::var(&config.access_key_env)
                .context("remote access-key environment is missing")?,
            secret: std::env::var(&config.secret_key_env)
                .context("remote secret-key environment is missing")?,
            token: config
                .session_token_env
                .as_ref()
                .map(|n| std::env::var(n).context("remote session token is missing"))
                .transpose()?,
            client: reqwest::Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(std::time::Duration::from_secs(60))
                .build()?,
        }))
    }

    pub async fn get(&self, key: &str) -> Result<Option<Artifact>> {
        let Some(manifest) = self
            .request(Method::GET, &format!("entries/{key}.json"), vec![])
            .await?
        else {
            return Ok(None);
        };
        let value: serde_json::Value = serde_json::from_slice(&manifest)?;
        let object = value["object"]
            .as_str()
            .context("remote object digest missing")?;
        ensure!(
            value["version"] == 1 && crate::cache::valid_hash(object),
            "invalid remote cache manifest"
        );
        let bytes = self
            .request(Method::GET, &format!("objects/{object}.json"), vec![])
            .await?
            .context("remote cache object missing")?;
        ensure!(
            files::digest(&bytes) == object,
            "remote cache digest mismatch"
        );
        let artifact: Artifact = serde_json::from_slice(&bytes)?;
        ensure!(artifact.key == key, "remote cache key mismatch");
        Ok(Some(artifact))
    }

    pub async fn put(&self, artifact: &Artifact) -> Result<()> {
        if let Some(digest) = self.stage(artifact).await? {
            self.commit(&artifact.key, &digest).await?;
        }
        Ok(())
    }

    /// Objects are unreachable until the small entry manifest is committed.
    /// The executor rechecks input generation and cancellation between these
    /// steps.
    pub async fn stage(&self, artifact: &Artifact) -> Result<Option<String>> {
        if self.config.mode != RemoteMode::ReadWrite {
            return Ok(None);
        }
        let bytes = crate::cache::encode(artifact)?;
        let digest = files::digest(&bytes);
        self.request(Method::PUT, &format!("objects/{digest}.json"), bytes)
            .await?;
        Ok(Some(digest))
    }

    pub async fn commit(&self, key: &str, digest: &str) -> Result<()> {
        self.request(
            Method::PUT,
            &format!("entries/{key}.json"),
            serde_json::to_vec(&serde_json::json!({"version":1,"object":digest}))?,
        )
        .await?;
        Ok(())
    }

    async fn request(&self, method: Method, key: &str, bytes: Vec<u8>) -> Result<Option<Vec<u8>>> {
        let url = Url::parse(&format!(
            "{}/{}/{}/{}",
            self.config.endpoint.trim_end_matches('/'),
            self.config.bucket,
            self.config.namespace,
            key
        ))?;
        let now = chrono::Utc::now();
        let date = now.format("%Y%m%d").to_string();
        let timestamp = now.format("%Y%m%dT%H%M%SZ").to_string();
        let payload = files::digest(&bytes);
        let host = match url.port() {
            Some(port) => format!("{}:{port}", url.host_str().unwrap()),
            None => url.host_str().unwrap().into(),
        };
        let mut headers =
            format!("host:{host}\nx-amz-content-sha256:{payload}\nx-amz-date:{timestamp}\n");
        let mut names = "host;x-amz-content-sha256;x-amz-date".to_owned();
        if let Some(token) = &self.token {
            headers.push_str(&format!("x-amz-security-token:{}\n", token.trim()));
            names.push_str(";x-amz-security-token");
        }
        let canonical = format!(
            "{}\n{}\n\n{}\n{}\n{}",
            method.as_str(),
            url.path(),
            headers,
            names,
            payload
        );
        let scope = format!("{date}/{}/s3/aws4_request", self.config.region);
        let signing = format!(
            "AWS4-HMAC-SHA256\n{timestamp}\n{scope}\n{}",
            files::digest(canonical.as_bytes())
        );
        let k_date = hmac(format!("AWS4{}", self.secret).as_bytes(), date.as_bytes());
        let k_region = hmac(&k_date, self.config.region.as_bytes());
        let k_service = hmac(&k_region, b"s3");
        let k_signing = hmac(&k_service, b"aws4_request");
        let signature: String = hmac(&k_signing, signing.as_bytes())
            .iter()
            .map(|b| format!("{b:02x}"))
            .collect();
        let authorization = format!(
            "AWS4-HMAC-SHA256 Credential={}/{scope}, SignedHeaders={names}, Signature={signature}",
            self.key
        );
        let mut authorization = reqwest::header::HeaderValue::from_str(&authorization)
            .context("invalid remote authorization metadata")?;
        authorization.set_sensitive(true);
        let mut request = self
            .client
            .request(method.clone(), url)
            .header("x-amz-date", timestamp)
            .header("x-amz-content-sha256", payload)
            .header("authorization", authorization)
            .body(bytes);
        if let Some(token) = &self.token {
            let mut token = reqwest::header::HeaderValue::from_str(token)
                .context("invalid remote session metadata")?;
            token.set_sensitive(true);
            request = request.header("x-amz-security-token", token);
        }
        let mut response = request
            .send()
            .await
            .map_err(|_| anyhow::anyhow!("remote cache transport failed"))?;
        if response.status() == reqwest::StatusCode::NOT_FOUND && method == Method::GET {
            return Ok(None);
        }
        ensure!(
            response.status().is_success(),
            "remote cache returned status {}",
            response.status().as_u16()
        );
        ensure!(
            response.content_length().unwrap_or(0) <= MAX_CACHE_BYTES as u64,
            "remote cache object too large"
        );
        let mut result = vec![];
        while let Some(chunk) = response
            .chunk()
            .await
            .map_err(|_| anyhow::anyhow!("remote cache transfer interrupted"))?
        {
            ensure!(
                result.len() + chunk.len() <= MAX_CACHE_BYTES,
                "remote cache object too large"
            );
            result.extend_from_slice(&chunk);
        }
        Ok(Some(result))
    }
}
fn hmac(key: &[u8], bytes: &[u8]) -> Vec<u8> {
    let mut mac = Hmac::<Sha256>::new_from_slice(key).expect("HMAC accepts arbitrary key sizes");
    mac.update(bytes);
    mac.finalize().into_bytes().to_vec()
}
pub fn untrusted_ci() -> bool {
    std::env::var("TFLOW_UNTRUSTED_CI").is_ok_and(|v| v == "1")
        || std::env::var("GITHUB_EVENT_NAME")
            .is_ok_and(|v| matches!(v.as_str(), "pull_request" | "pull_request_target"))
}
