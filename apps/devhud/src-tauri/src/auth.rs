//! Closed, dependency-injected authentication state for Deck and RealQA.
//!
//! The webview never receives OAuth credentials or network authority. Platform
//! adapters implement [`SecureVault`] and [`TokenTransport`]; this module owns
//! PKCE, callback consumption, account binding, token validation, and
//! lifecycle.

use std::{
    collections::{BTreeMap, BTreeSet},
    fmt,
    io::{self, BufRead, BufReader, Read, Write},
    net::{IpAddr, Ipv4Addr, SocketAddr, TcpListener, TcpStream},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use subtle::ConstantTimeEq;
use url::Url;
use zeroize::{Zeroize, ZeroizeOnDrop};

const MOBILE_CALLBACK: &str = "https://deli.dev/auth/devhud/callback";
const DELIBASE_AUDIENCE: &str = "https://delibase.deli.dev";
const DECK_AUDIENCE: &str = "https://deck.deli.dev";
const REALQA_AUDIENCE: &str = "https://realqa.deli.dev";
const DESKTOP_CALLBACK_PATH: &str = "/auth/callback";
const CALLBACK_TIMEOUT: Duration = Duration::from_secs(180);
const MAX_CALLBACK_REQUEST_LINE_BYTES: usize = 4096;
const MAX_SUBJECT_BYTES: usize = 512;
const DEVICE_SESSION_VERSION: &str = "v2";
const REALQA_DRAFT_ACCOUNT_BINDING_CONTEXT: &[u8] = b"devhud-realqa-draft-account-v1\0";

type HmacSha256 = Hmac<Sha256>;

#[derive(Clone, Copy, Debug, Deserialize, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum AuthPlatform {
    Desktop,
    Mobile,
}

#[derive(Clone, Copy, Debug, Deserialize, Ord, PartialEq, PartialOrd, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum AuthFeature {
    Deck,
    RealQa,
}

impl AuthFeature {
    const fn audience(self) -> &'static str {
        match self {
            Self::Deck => DECK_AUDIENCE,
            Self::RealQa => REALQA_AUDIENCE,
        }
    }

    const fn scopes(self) -> &'static [&'static str] {
        match self {
            Self::Deck => &["deck:access"],
            Self::RealQa => &["realqa:access"],
        }
    }

    const fn delibase_scopes(self) -> &'static [&'static str] {
        match self {
            Self::Deck => &["delibase:deck:forward"],
            Self::RealQa => &["delibase:realqa:forward"],
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub(crate) enum Connectivity {
    Online,
    Offline,
}

/// Stable, value-free errors safe to cross IPC.
#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum AuthError {
    ConfigurationUnavailable,
    InvalidConfiguration,
    InvalidCallback,
    CallbackStateMismatch,
    CallbackAlreadyConsumed,
    CallbackTimedOut,
    CallbackListenerUnavailable,
    BrowserUnavailable,
    AuthorizationRejected,
    TransportUnavailable,
    TokenExchangeFailed,
    TokenInvalid,
    TokenExpired,
    AudienceMismatch,
    SubjectMismatch,
    ScopeMismatch,
    SecureVaultUnavailable,
    SecureVaultWriteFailed,
    SecureVaultDeleteFailed,
    AccountSwitchRequiresLogout,
    FirstTimeOffline,
    ReauthenticationRequired,
    SignInAlreadyActive,
}

impl fmt::Display for AuthError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str(match self {
            Self::ConfigurationUnavailable => "authentication is not configured",
            Self::InvalidConfiguration => "authentication configuration is invalid",
            Self::InvalidCallback => "the authentication callback is invalid",
            Self::CallbackStateMismatch => "the authentication callback did not match",
            Self::CallbackAlreadyConsumed => "the authentication callback was already used",
            Self::CallbackTimedOut => "authentication timed out",
            Self::CallbackListenerUnavailable => "the callback listener is unavailable",
            Self::BrowserUnavailable => "the system browser is unavailable",
            Self::AuthorizationRejected => "authorization was rejected",
            Self::TransportUnavailable => "the authentication service is unavailable",
            Self::TokenExchangeFailed => "the token exchange failed",
            Self::TokenInvalid => "the token response is invalid",
            Self::TokenExpired => "the token is expired",
            Self::AudienceMismatch => "the token audience did not match",
            Self::SubjectMismatch => "the token subject did not match",
            Self::ScopeMismatch => "the token scope did not match",
            Self::SecureVaultUnavailable => "the secure vault is unavailable",
            Self::SecureVaultWriteFailed => "the secure vault write failed",
            Self::SecureVaultDeleteFailed => "the secure vault delete failed",
            Self::AccountSwitchRequiresLogout => "logout is required before changing accounts",
            Self::FirstTimeOffline => "a first sign-in requires a network connection",
            Self::ReauthenticationRequired => "online reauthentication is required",
            Self::SignInAlreadyActive => "a sign-in is already active",
        })
    }
}

impl std::error::Error for AuthError {}

/// A secret deliberately has no `Serialize`, `Clone`, `Display`, or revealing
/// `Debug` implementation.
#[derive(Zeroize, ZeroizeOnDrop)]
pub(crate) struct Secret(String);

impl Secret {
    pub(crate) fn new(value: impl Into<String>) -> Result<Self, AuthError> {
        let value = value.into();
        if value.is_empty() {
            return Err(AuthError::TokenInvalid);
        }
        Ok(Self(value))
    }

    pub(crate) fn expose(&self) -> &str {
        &self.0
    }
}

impl fmt::Debug for Secret {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter.write_str("[REDACTED]")
    }
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct TokenClaims {
    pub(crate) issuer: String,
    pub(crate) subject: String,
    pub(crate) audiences: BTreeSet<String>,
    pub(crate) scopes: BTreeSet<String>,
    pub(crate) expires_at_unix_seconds: u64,
}

pub(crate) struct TokenSet {
    pub(crate) access_token: Secret,
    pub(crate) id_token: Option<Secret>,
    pub(crate) refresh_token: Option<Secret>,
    pub(crate) claims: TokenClaims,
}

pub(crate) struct TokenRefreshError {
    error: AuthError,
    rotated_refresh_token: Option<Secret>,
}

impl TokenRefreshError {
    pub(crate) fn with_rotated_refresh_token(
        error: AuthError,
        rotated_refresh_token: Option<Secret>,
    ) -> Self {
        Self {
            error,
            rotated_refresh_token,
        }
    }

    fn into_error(self) -> AuthError {
        self.error
    }
}

impl From<AuthError> for TokenRefreshError {
    fn from(error: AuthError) -> Self {
        Self {
            error,
            rotated_refresh_token: None,
        }
    }
}

impl fmt::Debug for TokenRefreshError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("TokenRefreshError")
            .field("error", &self.error)
            .field(
                "rotated_refresh_token",
                &self.rotated_refresh_token.as_ref().map(|_| "[REDACTED]"),
            )
            .finish()
    }
}

impl fmt::Debug for TokenSet {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("TokenSet")
            .field("access_token", &"[REDACTED]")
            .field("id_token", &self.id_token.as_ref().map(|_| "[REDACTED]"))
            .field(
                "refresh_token",
                &self.refresh_token.as_ref().map(|_| "[REDACTED]"),
            )
            .field("claims", &self.claims)
            .finish()
    }
}

pub(crate) struct BearerPair {
    feature: Secret,
    delibase: Secret,
    subject: String,
}

impl BearerPair {
    pub(crate) fn with_exposed<R>(&self, operation: impl FnOnce(&str, &str, &str) -> R) -> R {
        operation(self.feature.expose(), self.delibase.expose(), &self.subject)
    }
}

impl fmt::Debug for BearerPair {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("BearerPair")
            .field("feature", &"[REDACTED]")
            .field("delibase", &"[REDACTED]")
            .field("subject", &self.subject)
            .finish()
    }
}

pub(crate) trait TokenTransport: Send {
    fn exchange_code(
        &mut self,
        endpoint: &Url,
        client_id: &str,
        code: &Secret,
        verifier: &Secret,
        redirect_uri: &Url,
        nonce: &Secret,
    ) -> Result<TokenSet, AuthError>;

    fn refresh(
        &mut self,
        endpoint: &Url,
        client_id: &str,
        refresh_token: &Secret,
        audience: &str,
        scopes: &[&str],
    ) -> Result<TokenSet, TokenRefreshError>;

    fn revoke(
        &mut self,
        endpoint: &Url,
        client_id: &str,
        refresh_token: &Secret,
    ) -> Result<(), AuthError>;
}

pub(crate) struct VaultSession {
    pub(crate) refresh_tokens: BTreeMap<AuthFeature, Secret>,
    pub(crate) device_session_key: Secret,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct RealQaDraftAccessContext {
    pub(crate) account_binding: String,
    pub(crate) online_reauthenticated: bool,
}

pub(crate) trait SecureVault: Send {
    fn load(&mut self) -> Result<Option<VaultSession>, AuthError>;
    fn replace(&mut self, session: &VaultSession) -> Result<(), AuthError>;
    fn clear(&mut self) -> Result<(), AuthError>;
}

#[derive(Debug, Clone)]
pub(crate) struct AuthConfiguration {
    oidc_issuer: Url,
    authorization_endpoint: Url,
    token_endpoint: Url,
    revocation_endpoint: Url,
    client_id: String,
}

impl AuthConfiguration {
    pub(crate) fn new(logto_endpoint: &str, client_id: &str) -> Result<Self, AuthError> {
        let logto_endpoint =
            Url::parse(logto_endpoint).map_err(|_| AuthError::InvalidConfiguration)?;
        if logto_endpoint.scheme() != "https"
            || logto_endpoint.host_str().is_none()
            || !logto_endpoint.username().is_empty()
            || logto_endpoint.password().is_some()
            || logto_endpoint.query().is_some()
            || logto_endpoint.fragment().is_some()
            || !matches!(logto_endpoint.path(), "" | "/")
            || client_id.trim().is_empty()
            || client_id.len() > 256
        {
            return Err(AuthError::InvalidConfiguration);
        }
        let endpoint = |path: &str| {
            let mut url = logto_endpoint.clone();
            url.set_path(path);
            url
        };
        Ok(Self {
            oidc_issuer: endpoint("/oidc"),
            authorization_endpoint: endpoint("/oidc/auth"),
            token_endpoint: endpoint("/oidc/token"),
            revocation_endpoint: endpoint("/oidc/token/revocation"),
            client_id: client_id.to_owned(),
        })
    }

    pub(crate) fn endpoint_allowed(&self, endpoint: &Url) -> bool {
        endpoint == &self.authorization_endpoint
            || endpoint == &self.token_endpoint
            || endpoint == &self.revocation_endpoint
            || endpoint == &self.jwks_endpoint()
    }

    pub(crate) fn oidc_issuer(&self) -> &Url {
        &self.oidc_issuer
    }

    pub(crate) fn client_id(&self) -> &str {
        &self.client_id
    }

    pub(crate) fn jwks_endpoint(&self) -> Url {
        let mut endpoint = self.oidc_issuer.clone();
        endpoint.set_path("/oidc/jwks");
        endpoint
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Serialize)]
#[serde(tag = "status", rename_all = "kebab-case")]
pub(crate) enum SessionSnapshot {
    SignedOut,
    Authenticating,
    SignedIn { subject: String },
    PriorSessionOffline,
    CleanupRequired,
}

enum SessionState {
    SignedOut,
    Authenticating,
    SignedIn {
        subject: String,
        access_token: Secret,
        id_token: Option<Secret>,
    },
    PriorSessionOffline {
        account_binding: String,
    },
    CleanupRequired,
}

struct PendingAuthorization {
    feature: AuthFeature,
    state: Secret,
    verifier: Secret,
    nonce: Secret,
    redirect_uri: Url,
    expires_at_unix_seconds: u64,
    resume_state: SessionState,
}

pub(crate) struct AuthorizationRequest {
    pub(crate) authorization_url: Url,
    pub(crate) redirect_uri: Url,
}

impl fmt::Debug for AuthorizationRequest {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("AuthorizationRequest")
            .field("authorization_url", &"[REDACTED QUERY]")
            .field("redirect_uri", &self.redirect_uri)
            .finish()
    }
}

pub(crate) struct SessionManager<T: TokenTransport, V: SecureVault> {
    configuration: AuthConfiguration,
    transport: T,
    vault: V,
    state: SessionState,
    pending: Option<PendingAuthorization>,
}

impl<T: TokenTransport, V: SecureVault> SessionManager<T, V> {
    pub(crate) fn new(configuration: AuthConfiguration, transport: T, vault: V) -> Self {
        Self {
            configuration,
            transport,
            vault,
            state: SessionState::SignedOut,
            pending: None,
        }
    }

    pub(crate) fn snapshot(&self) -> SessionSnapshot {
        match &self.state {
            SessionState::SignedOut => SessionSnapshot::SignedOut,
            SessionState::Authenticating => SessionSnapshot::Authenticating,
            SessionState::SignedIn { subject, .. } => SessionSnapshot::SignedIn {
                subject: subject.clone(),
            },
            SessionState::PriorSessionOffline { .. } => SessionSnapshot::PriorSessionOffline,
            SessionState::CleanupRequired => SessionSnapshot::CleanupRequired,
        }
    }

    pub(crate) fn restore(
        &mut self,
        connectivity: Connectivity,
    ) -> Result<SessionSnapshot, AuthError> {
        self.restore_at(connectivity, unix_time_now())
    }

    fn restore_at(
        &mut self,
        connectivity: Connectivity,
        now_unix_seconds: u64,
    ) -> Result<SessionSnapshot, AuthError> {
        let Some(mut retained) = self.vault.load()? else {
            self.state = SessionState::SignedOut;
            return if connectivity == Connectivity::Offline {
                Err(AuthError::FirstTimeOffline)
            } else {
                Ok(self.snapshot())
            };
        };
        validate_retained_session_shape(&retained)?;
        if connectivity == Connectivity::Offline {
            self.state = SessionState::PriorSessionOffline {
                account_binding: device_session_account_binding(
                    retained.device_session_key.expose(),
                )?,
            };
            return Ok(self.snapshot());
        }
        let token_endpoint = self.configuration.token_endpoint.clone();
        let retained_features = retained.refresh_tokens.keys().copied().collect::<Vec<_>>();
        let mut grant_error = None;
        let mut transport_unavailable = false;
        for feature in retained_features {
            let refresh_token = retained
                .refresh_tokens
                .get(&feature)
                .ok_or(AuthError::TokenInvalid)?;
            let tokens = match self.transport.refresh(
                &token_endpoint,
                &self.configuration.client_id,
                refresh_token,
                feature.audience(),
                feature.scopes(),
            ) {
                Ok(tokens) => tokens,
                Err(failure) => {
                    let error = self.persist_retryable_rotation(&mut retained, feature, failure)?;
                    if error == AuthError::TransportUnavailable {
                        transport_unavailable = true;
                        continue;
                    }
                    grant_error.get_or_insert(error);
                    continue;
                }
            };
            if let Err(error) = validate_restored_bearer(
                &tokens.claims,
                &self.configuration,
                feature,
                now_unix_seconds,
            ) {
                grant_error.get_or_insert(error);
                continue;
            }
            let subject = tokens.claims.subject.clone();
            if !device_session_matches(retained.device_session_key.expose(), &subject)? {
                grant_error.get_or_insert(AuthError::AccountSwitchRequiresLogout);
                continue;
            }
            if let Some(rotated_refresh) = tokens.refresh_token {
                retained.refresh_tokens.insert(feature, rotated_refresh);
                self.vault.replace(&retained)?;
            }
            let refresh_token = retained
                .refresh_tokens
                .get(&feature)
                .ok_or(AuthError::TokenInvalid)?;
            let delibase_tokens = match self.transport.refresh(
                &token_endpoint,
                &self.configuration.client_id,
                refresh_token,
                DELIBASE_AUDIENCE,
                feature.delibase_scopes(),
            ) {
                Ok(tokens) => tokens,
                Err(failure) => {
                    let error = self.persist_retryable_rotation(&mut retained, feature, failure)?;
                    if error == AuthError::TransportUnavailable {
                        transport_unavailable = true;
                        continue;
                    }
                    grant_error.get_or_insert(error);
                    continue;
                }
            };
            if let Err(error) = validate_bearer(
                &delibase_tokens.claims,
                &self.configuration,
                &subject,
                DELIBASE_AUDIENCE,
                feature.delibase_scopes(),
                now_unix_seconds,
            ) {
                grant_error.get_or_insert(error);
                continue;
            }
            if let Some(rotated_refresh) = delibase_tokens.refresh_token {
                retained.refresh_tokens.insert(feature, rotated_refresh);
                self.vault.replace(&retained)?;
            }
            self.state = SessionState::SignedIn {
                subject,
                access_token: tokens.access_token,
                id_token: tokens.id_token,
            };
            return Ok(self.snapshot());
        }
        if let Some(error) = grant_error {
            return Err(error);
        }
        if transport_unavailable {
            self.state = SessionState::PriorSessionOffline {
                account_binding: device_session_account_binding(
                    retained.device_session_key.expose(),
                )?,
            };
            return Ok(self.snapshot());
        }
        Err(AuthError::TokenInvalid)
    }

    pub(crate) fn begin(
        &mut self,
        feature: AuthFeature,
        platform: AuthPlatform,
        redirect_uri: Url,
    ) -> Result<AuthorizationRequest, AuthError> {
        self.begin_at(feature, platform, redirect_uri, unix_time_now())
    }

    fn begin_at(
        &mut self,
        feature: AuthFeature,
        platform: AuthPlatform,
        redirect_uri: Url,
        now_unix_seconds: u64,
    ) -> Result<AuthorizationRequest, AuthError> {
        if self.pending.is_some() || matches!(self.state, SessionState::Authenticating) {
            return Err(AuthError::SignInAlreadyActive);
        }
        validate_redirect(platform, &redirect_uri)?;
        self.clear_invalid_retained_session_before_sign_in()?;
        let state = random_secret(32)?;
        let verifier = random_secret(64)?;
        let nonce = random_secret(32)?;
        let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.expose().as_bytes()));
        let authorization_scopes = ["openid", "offline_access", "profile"]
            .into_iter()
            .chain(feature.scopes().iter().copied())
            .chain(feature.delibase_scopes().iter().copied())
            .collect::<BTreeSet<_>>()
            .into_iter()
            .collect::<Vec<_>>()
            .join(" ");
        let mut authorization_url = self.configuration.authorization_endpoint.clone();
        {
            let mut query = authorization_url.query_pairs_mut();
            query
                .append_pair("client_id", &self.configuration.client_id)
                .append_pair("redirect_uri", redirect_uri.as_str())
                .append_pair("response_type", "code")
                .append_pair("scope", &authorization_scopes)
                .append_pair("resource", feature.audience())
                .append_pair("resource", DELIBASE_AUDIENCE)
                .append_pair("code_challenge_method", "S256")
                .append_pair("code_challenge", &challenge)
                .append_pair("state", state.expose())
                .append_pair("nonce", nonce.expose())
                .append_pair("prompt", "select_account");
        }
        let resume_state = std::mem::replace(&mut self.state, SessionState::Authenticating);
        self.pending = Some(PendingAuthorization {
            feature,
            state,
            verifier,
            nonce,
            redirect_uri: redirect_uri.clone(),
            expires_at_unix_seconds: now_unix_seconds.saturating_add(CALLBACK_TIMEOUT.as_secs()),
            resume_state,
        });
        Ok(AuthorizationRequest {
            authorization_url,
            redirect_uri,
        })
    }

    pub(crate) fn cancel_pending(&mut self) {
        if let Some(pending) = self.pending.take() {
            self.state = pending.resume_state;
        } else if matches!(self.state, SessionState::Authenticating) {
            self.state = SessionState::SignedOut;
        }
    }

    pub(crate) fn expire_pending(&mut self, now_unix_seconds: u64) -> Result<(), AuthError> {
        if self
            .pending
            .as_ref()
            .is_some_and(|pending| now_unix_seconds >= pending.expires_at_unix_seconds)
        {
            self.cancel_pending();
            return Err(AuthError::CallbackTimedOut);
        }
        Ok(())
    }

    pub(crate) fn complete_callback(
        &mut self,
        callback: &Url,
        now_unix_seconds: u64,
    ) -> Result<SessionSnapshot, AuthError> {
        // Taking first makes callback processing one-shot even when validation
        // or exchange fails. A retry starts a fresh authorization transaction.
        let PendingAuthorization {
            feature,
            state,
            verifier,
            nonce,
            redirect_uri,
            resume_state,
            ..
        } = self
            .pending
            .take()
            .ok_or(AuthError::CallbackAlreadyConsumed)?;
        // Incremental authorization for another feature retains the existing
        // same-account session if validation, exchange, or consent fails.
        self.state = resume_state;
        validate_callback_origin(callback, &redirect_uri)?;
        if callback.as_str().len() > MAX_CALLBACK_REQUEST_LINE_BYTES {
            return Err(AuthError::InvalidCallback);
        }
        let mut callback_state = None;
        let mut code = None;
        let mut authorization_error = false;
        for (key, value) in callback.query_pairs() {
            match key.as_ref() {
                "state" => {
                    if callback_state.replace(value).is_some() {
                        return Err(AuthError::InvalidCallback);
                    }
                }
                "code" => {
                    if code.replace(value).is_some() {
                        return Err(AuthError::InvalidCallback);
                    }
                }
                "error" if authorization_error => return Err(AuthError::InvalidCallback),
                "error" => authorization_error = true,
                _ => {}
            }
        }
        let callback_state = callback_state.ok_or(AuthError::InvalidCallback)?;
        if !constant_time_equal(callback_state.as_bytes(), state.expose().as_bytes()) {
            return Err(AuthError::CallbackStateMismatch);
        }
        if authorization_error {
            return Err(AuthError::AuthorizationRejected);
        }
        let code = Secret::new(
            code.filter(|value| !value.is_empty() && value.len() <= 4096)
                .ok_or(AuthError::InvalidCallback)?
                .as_ref(),
        )?;
        let token_endpoint = self.configuration.token_endpoint.clone();
        debug_assert!(self.configuration.endpoint_allowed(&token_endpoint));
        let tokens = self.transport.exchange_code(
            &token_endpoint,
            &self.configuration.client_id,
            &code,
            &verifier,
            &redirect_uri,
            &nonce,
        )?;
        validate_identity_token(&tokens.claims, &self.configuration, now_unix_seconds)?;
        let subject = tokens.claims.subject.clone();
        self.reject_account_switch(&subject)?;

        let mut refresh_token = tokens.refresh_token.ok_or(AuthError::TokenInvalid)?;
        let mut retained = match self.vault.load()? {
            Some(existing) => {
                validate_retained_session_shape(&existing)?;
                if !device_session_matches(existing.device_session_key.expose(), &subject)? {
                    return Err(AuthError::AccountSwitchRequiresLogout);
                }
                existing
            }
            None => VaultSession {
                refresh_tokens: BTreeMap::new(),
                device_session_key: new_device_session_key(&subject)?,
            },
        };
        let feature_tokens = self
            .transport
            .refresh(
                &token_endpoint,
                &self.configuration.client_id,
                &refresh_token,
                feature.audience(),
                feature.scopes(),
            )
            .map_err(TokenRefreshError::into_error)?;
        validate_bearer(
            &feature_tokens.claims,
            &self.configuration,
            &subject,
            feature.audience(),
            feature.scopes(),
            now_unix_seconds,
        )?;
        if let Some(rotated_refresh) = feature_tokens.refresh_token {
            refresh_token = rotated_refresh;
        }
        let delibase_tokens = self
            .transport
            .refresh(
                &token_endpoint,
                &self.configuration.client_id,
                &refresh_token,
                DELIBASE_AUDIENCE,
                feature.delibase_scopes(),
            )
            .map_err(TokenRefreshError::into_error)?;
        validate_bearer(
            &delibase_tokens.claims,
            &self.configuration,
            &subject,
            DELIBASE_AUDIENCE,
            feature.delibase_scopes(),
            now_unix_seconds,
        )?;
        if let Some(rotated_refresh) = delibase_tokens.refresh_token {
            refresh_token = rotated_refresh;
        }
        retained.refresh_tokens.insert(feature, refresh_token);
        self.vault.replace(&retained)?;
        self.state = SessionState::SignedIn {
            subject,
            access_token: tokens.access_token,
            id_token: tokens.id_token,
        };
        Ok(self.snapshot())
    }

    pub(crate) fn bearer_pair(
        &mut self,
        feature: AuthFeature,
        now_unix_seconds: u64,
    ) -> Result<BearerPair, AuthError> {
        let subject = match &self.state {
            SessionState::SignedIn { subject, .. } => subject.clone(),
            SessionState::PriorSessionOffline { .. } | SessionState::SignedOut => {
                return Err(AuthError::ReauthenticationRequired);
            }
            SessionState::Authenticating => return Err(AuthError::SignInAlreadyActive),
            SessionState::CleanupRequired => return Err(AuthError::SecureVaultDeleteFailed),
        };
        let retained = self
            .vault
            .load()?
            .ok_or(AuthError::ReauthenticationRequired)?;
        validate_retained_session_shape(&retained)?;
        if !device_session_matches(retained.device_session_key.expose(), &subject)? {
            return Err(AuthError::AccountSwitchRequiresLogout);
        }
        let mut retained = retained;
        let token_endpoint = self.configuration.token_endpoint.clone();
        let feature_tokens = match self.transport.refresh(
            &token_endpoint,
            &self.configuration.client_id,
            retained
                .refresh_tokens
                .get(&feature)
                .ok_or(AuthError::ReauthenticationRequired)?,
            feature.audience(),
            feature.scopes(),
        ) {
            Ok(tokens) => tokens,
            Err(failure) => {
                let error = self.persist_retryable_rotation(&mut retained, feature, failure)?;
                return Err(error);
            }
        };
        validate_bearer(
            &feature_tokens.claims,
            &self.configuration,
            &subject,
            feature.audience(),
            feature.scopes(),
            now_unix_seconds,
        )?;
        if let Some(refresh_token) = feature_tokens.refresh_token {
            retained.refresh_tokens.insert(feature, refresh_token);
            self.vault.replace(&retained)?;
        }
        let delibase_tokens = match self.transport.refresh(
            &token_endpoint,
            &self.configuration.client_id,
            retained
                .refresh_tokens
                .get(&feature)
                .ok_or(AuthError::ReauthenticationRequired)?,
            DELIBASE_AUDIENCE,
            feature.delibase_scopes(),
        ) {
            Ok(tokens) => tokens,
            Err(failure) => {
                let error = self.persist_retryable_rotation(&mut retained, feature, failure)?;
                return Err(error);
            }
        };
        validate_bearer(
            &delibase_tokens.claims,
            &self.configuration,
            &subject,
            DELIBASE_AUDIENCE,
            feature.delibase_scopes(),
            now_unix_seconds,
        )?;
        if feature_tokens.claims.subject != delibase_tokens.claims.subject {
            return Err(AuthError::SubjectMismatch);
        }
        if let Some(rotated_refresh) = delibase_tokens.refresh_token {
            retained.refresh_tokens.insert(feature, rotated_refresh);
            self.vault.replace(&retained)?;
        }
        Ok(BearerPair {
            feature: feature_tokens.access_token,
            delibase: delibase_tokens.access_token,
            subject,
        })
    }

    pub(crate) fn realqa_draft_access(&mut self) -> Result<RealQaDraftAccessContext, AuthError> {
        let retained = self.vault.load()?.ok_or(AuthError::FirstTimeOffline)?;
        validate_retained_session_shape(&retained)?;
        if !retained.refresh_tokens.contains_key(&AuthFeature::RealQa) {
            return Err(AuthError::ReauthenticationRequired);
        }
        let retained_binding =
            device_session_account_binding(retained.device_session_key.expose())?;
        match &self.state {
            SessionState::SignedIn { subject, .. } => {
                if !device_session_matches(retained.device_session_key.expose(), subject)? {
                    return Err(AuthError::AccountSwitchRequiresLogout);
                }
                Ok(RealQaDraftAccessContext {
                    account_binding: retained_binding,
                    online_reauthenticated: true,
                })
            }
            SessionState::PriorSessionOffline { account_binding }
                if constant_time_equal(account_binding.as_bytes(), retained_binding.as_bytes()) =>
            {
                Ok(RealQaDraftAccessContext {
                    account_binding: retained_binding,
                    online_reauthenticated: false,
                })
            }
            SessionState::PriorSessionOffline { .. } => Err(AuthError::SubjectMismatch),
            SessionState::SignedOut => Err(AuthError::ReauthenticationRequired),
            SessionState::Authenticating => Err(AuthError::SignInAlreadyActive),
            SessionState::CleanupRequired => Err(AuthError::SecureVaultDeleteFailed),
        }
    }

    pub(crate) fn logout(&mut self) -> Result<SessionSnapshot, AuthError> {
        self.pending = None;
        // Drop access and ID tokens before touching fallible storage or the
        // network. Logout always locks feature use locally.
        self.state = SessionState::SignedOut;
        let retained = self.vault.load().ok().flatten();
        // Local logout is authoritative, so secure-vault deletion must finish
        // before best-effort network revocation can delay or interrupt logout.
        match self.vault.clear() {
            Ok(()) => {
                if let Some(session) = retained {
                    let revocation_endpoint = self.configuration.revocation_endpoint.clone();
                    for refresh_token in session.refresh_tokens.values() {
                        let _ = self.transport.revoke(
                            &revocation_endpoint,
                            &self.configuration.client_id,
                            refresh_token,
                        );
                    }
                }
                Ok(self.snapshot())
            }
            Err(_) => {
                self.state = SessionState::CleanupRequired;
                Err(AuthError::SecureVaultDeleteFailed)
            }
        }
    }

    pub(crate) fn reset(&mut self) -> Result<SessionSnapshot, AuthError> {
        self.pending = None;
        self.state = SessionState::SignedOut;
        match self.vault.clear() {
            Ok(()) => Ok(self.snapshot()),
            Err(_) => {
                self.state = SessionState::CleanupRequired;
                Err(AuthError::SecureVaultDeleteFailed)
            }
        }
    }

    fn reject_account_switch(&mut self, subject: &str) -> Result<(), AuthError> {
        if let SessionState::SignedIn {
            subject: active_subject,
            ..
        } = &self.state
            && active_subject != subject
        {
            return Err(AuthError::AccountSwitchRequiresLogout);
        }
        if let Some(retained) = self.vault.load()? {
            validate_retained_session_shape(&retained)?;
            if !device_session_matches(retained.device_session_key.expose(), subject)? {
                return Err(AuthError::AccountSwitchRequiresLogout);
            }
        }
        Ok(())
    }

    fn clear_invalid_retained_session_before_sign_in(&mut self) -> Result<(), AuthError> {
        if !matches!(self.state, SessionState::SignedOut) {
            return Ok(());
        }
        let invalid = match self.vault.load() {
            Ok(Some(retained)) => match validate_retained_session_shape(&retained) {
                Ok(()) => false,
                Err(AuthError::TokenInvalid) => true,
                Err(error) => return Err(error),
            },
            Ok(None) => false,
            Err(AuthError::TokenInvalid) => true,
            Err(error) => return Err(error),
        };
        if invalid {
            if self.vault.clear().is_err() {
                self.state = SessionState::CleanupRequired;
                return Err(AuthError::SecureVaultDeleteFailed);
            }
            self.state = SessionState::SignedOut;
        }
        Ok(())
    }

    fn persist_retryable_rotation(
        &mut self,
        retained: &mut VaultSession,
        feature: AuthFeature,
        failure: TokenRefreshError,
    ) -> Result<AuthError, AuthError> {
        let TokenRefreshError {
            error,
            rotated_refresh_token,
        } = failure;
        if error == AuthError::TransportUnavailable
            && let Some(rotated_refresh_token) = rotated_refresh_token
        {
            retained
                .refresh_tokens
                .insert(feature, rotated_refresh_token);
            self.vault.replace(retained)?;
        }
        Ok(error)
    }

    #[cfg(test)]
    fn memory_tokens_present(&self) -> bool {
        matches!(
            self.state,
            SessionState::SignedIn {
                access_token: _,
                id_token: _,
                ..
            }
        )
    }

    pub(crate) fn has_pending_authorization(&self) -> bool {
        self.pending.is_some()
    }
}

fn validate_redirect(platform: AuthPlatform, redirect: &Url) -> Result<(), AuthError> {
    match platform {
        AuthPlatform::Desktop => {
            if redirect.scheme() != "http"
                || redirect.host_str() != Some("127.0.0.1")
                || redirect.port().is_none()
                || redirect.path() != DESKTOP_CALLBACK_PATH
                || redirect.query().is_some()
                || redirect.fragment().is_some()
                || !redirect.username().is_empty()
                || redirect.password().is_some()
            {
                return Err(AuthError::InvalidCallback);
            }
        }
        AuthPlatform::Mobile if redirect.as_str() != MOBILE_CALLBACK => {
            return Err(AuthError::InvalidCallback);
        }
        AuthPlatform::Mobile => {}
    }
    Ok(())
}

fn validate_callback_origin(callback: &Url, expected: &Url) -> Result<(), AuthError> {
    if callback.scheme() != expected.scheme()
        || callback.host_str() != expected.host_str()
        || callback.port_or_known_default() != expected.port_or_known_default()
        || callback.path() != expected.path()
        || callback.fragment().is_some()
        || !callback.username().is_empty()
        || callback.password().is_some()
    {
        return Err(AuthError::InvalidCallback);
    }
    Ok(())
}

pub(crate) fn is_mobile_callback_boundary(callback: &Url) -> bool {
    Url::parse(MOBILE_CALLBACK)
        .is_ok_and(|expected| validate_callback_origin(callback, &expected).is_ok())
}

fn validate_identity_token(
    claims: &TokenClaims,
    configuration: &AuthConfiguration,
    now: u64,
) -> Result<(), AuthError> {
    validate_common_claims(claims, configuration, now)?;
    if claims.subject.is_empty() || claims.subject.len() > MAX_SUBJECT_BYTES {
        return Err(AuthError::TokenInvalid);
    }
    if !claims.audiences.contains(&configuration.client_id) {
        return Err(AuthError::AudienceMismatch);
    }
    Ok(())
}

fn validate_restored_bearer(
    claims: &TokenClaims,
    configuration: &AuthConfiguration,
    feature: AuthFeature,
    now: u64,
) -> Result<(), AuthError> {
    if claims.subject.is_empty() || claims.subject.len() > MAX_SUBJECT_BYTES {
        return Err(AuthError::TokenInvalid);
    }
    validate_bearer(
        claims,
        configuration,
        &claims.subject,
        feature.audience(),
        feature.scopes(),
        now,
    )
}

fn validate_bearer(
    claims: &TokenClaims,
    configuration: &AuthConfiguration,
    expected_subject: &str,
    expected_audience: &str,
    expected_scopes: &[&str],
    now: u64,
) -> Result<(), AuthError> {
    validate_common_claims(claims, configuration, now)?;
    if claims.subject != expected_subject {
        return Err(AuthError::SubjectMismatch);
    }
    if !claims.audiences.contains(expected_audience) {
        return Err(AuthError::AudienceMismatch);
    }
    if expected_scopes
        .iter()
        .any(|scope| !claims.scopes.contains(*scope))
    {
        return Err(AuthError::ScopeMismatch);
    }
    Ok(())
}

fn validate_common_claims(
    claims: &TokenClaims,
    configuration: &AuthConfiguration,
    now: u64,
) -> Result<(), AuthError> {
    if claims.issuer.trim_end_matches('/')
        != configuration.oidc_issuer.as_str().trim_end_matches('/')
    {
        return Err(AuthError::TokenInvalid);
    }
    if claims.expires_at_unix_seconds <= now {
        return Err(AuthError::TokenExpired);
    }
    Ok(())
}

fn random_secret(bytes: usize) -> Result<Secret, AuthError> {
    let mut random = vec![0_u8; bytes];
    getrandom::fill(&mut random).map_err(|_| AuthError::ConfigurationUnavailable)?;
    let encoded = URL_SAFE_NO_PAD.encode(&random);
    random.zeroize();
    Secret::new(encoded)
}

fn new_device_session_key(subject: &str) -> Result<Secret, AuthError> {
    let mut key = [0_u8; 32];
    getrandom::fill(&mut key).map_err(|_| AuthError::SecureVaultUnavailable)?;
    let mut mac =
        HmacSha256::new_from_slice(&key).map_err(|_| AuthError::SecureVaultUnavailable)?;
    mac.update(subject.as_bytes());
    let tag = mac.finalize().into_bytes();
    let account_binding = realqa_draft_account_binding(subject);
    let encoded = format!(
        "{DEVICE_SESSION_VERSION}.{}.{}.{}",
        URL_SAFE_NO_PAD.encode(key),
        URL_SAFE_NO_PAD.encode(tag),
        account_binding,
    );
    key.zeroize();
    Secret::new(encoded)
}

fn validate_device_session_shape(value: &str) -> Result<(), AuthError> {
    let mut parts = value.split('.');
    let version = parts.next();
    let key = parts.next();
    let tag = parts.next();
    let account_binding = parts.next();
    if version != Some(DEVICE_SESSION_VERSION)
        || parts.next().is_some()
        || key
            .and_then(|value| URL_SAFE_NO_PAD.decode(value).ok())
            .map(|v| v.len())
            != Some(32)
        || tag
            .and_then(|value| URL_SAFE_NO_PAD.decode(value).ok())
            .map(|v| v.len())
            != Some(32)
        || account_binding
            .and_then(|value| URL_SAFE_NO_PAD.decode(value).ok())
            .map(|value| value.len())
            != Some(32)
    {
        return Err(AuthError::TokenInvalid);
    }
    Ok(())
}

fn validate_retained_session_shape(session: &VaultSession) -> Result<(), AuthError> {
    if session.refresh_tokens.is_empty() {
        return Err(AuthError::TokenInvalid);
    }
    validate_device_session_shape(session.device_session_key.expose())
}

fn device_session_matches(value: &str, subject: &str) -> Result<bool, AuthError> {
    validate_device_session_shape(value)?;
    let mut parts = value.split('.');
    let _ = parts.next();
    let key = URL_SAFE_NO_PAD
        .decode(parts.next().ok_or(AuthError::TokenInvalid)?)
        .map_err(|_| AuthError::TokenInvalid)?;
    let actual = URL_SAFE_NO_PAD
        .decode(parts.next().ok_or(AuthError::TokenInvalid)?)
        .map_err(|_| AuthError::TokenInvalid)?;
    let account_binding = parts.next().ok_or(AuthError::TokenInvalid)?;
    let mut mac = HmacSha256::new_from_slice(&key).map_err(|_| AuthError::TokenInvalid)?;
    mac.update(subject.as_bytes());
    let expected = mac.finalize().into_bytes();
    Ok(constant_time_equal(&actual, &expected)
        && constant_time_equal(
            account_binding.as_bytes(),
            realqa_draft_account_binding(subject).as_bytes(),
        ))
}

fn realqa_draft_account_binding(subject: &str) -> String {
    let mut digest = Sha256::new();
    digest.update(REALQA_DRAFT_ACCOUNT_BINDING_CONTEXT);
    digest.update(subject.as_bytes());
    URL_SAFE_NO_PAD.encode(digest.finalize())
}

fn device_session_account_binding(value: &str) -> Result<String, AuthError> {
    validate_device_session_shape(value)?;
    value
        .split('.')
        .nth(3)
        .map(ToOwned::to_owned)
        .ok_or(AuthError::TokenInvalid)
}

fn constant_time_equal(left: &[u8], right: &[u8]) -> bool {
    left.len() == right.len() && bool::from(left.ct_eq(right))
}

pub(crate) struct LoopbackCallback {
    listener: TcpListener,
    redirect_uri: Url,
}

impl LoopbackCallback {
    pub(crate) fn bind() -> Result<Self, AuthError> {
        let listener = TcpListener::bind(SocketAddr::new(IpAddr::V4(Ipv4Addr::LOCALHOST), 0))
            .map_err(|_| AuthError::CallbackListenerUnavailable)?;
        let port = listener
            .local_addr()
            .map_err(|_| AuthError::CallbackListenerUnavailable)?
            .port();
        let redirect_uri = Url::parse(&format!("http://127.0.0.1:{port}{DESKTOP_CALLBACK_PATH}",))
            .map_err(|_| AuthError::CallbackListenerUnavailable)?;
        Ok(Self {
            listener,
            redirect_uri,
        })
    }

    pub(crate) fn redirect_uri(&self) -> &Url {
        &self.redirect_uri
    }

    pub(crate) fn receive(self) -> Result<Url, AuthError> {
        self.receive_with_timeout(CALLBACK_TIMEOUT)
    }

    fn receive_with_timeout(self, timeout: Duration) -> Result<Url, AuthError> {
        self.listener
            .set_nonblocking(true)
            .map_err(|_| AuthError::CallbackListenerUnavailable)?;
        let started = std::time::Instant::now();
        loop {
            match self.listener.accept() {
                Ok((stream, address)) => {
                    if address.ip() != IpAddr::V4(Ipv4Addr::LOCALHOST) {
                        return Err(AuthError::InvalidCallback);
                    }
                    return read_loopback_request(stream, &self.redirect_uri);
                }
                Err(error) if error.kind() == io::ErrorKind::WouldBlock => {
                    if started.elapsed() >= timeout {
                        return Err(AuthError::CallbackTimedOut);
                    }
                    std::thread::sleep(Duration::from_millis(5));
                }
                Err(_) => return Err(AuthError::CallbackListenerUnavailable),
            }
        }
    }
}

fn read_loopback_request(mut stream: TcpStream, redirect_uri: &Url) -> Result<Url, AuthError> {
    stream
        .set_read_timeout(Some(Duration::from_secs(2)))
        .map_err(|_| AuthError::InvalidCallback)?;
    let mut request_line = Vec::new();
    BufReader::new(stream.try_clone().map_err(|_| AuthError::InvalidCallback)?)
        .take(MAX_CALLBACK_REQUEST_LINE_BYTES as u64)
        .read_until(b'\n', &mut request_line)
        .map_err(|_| AuthError::InvalidCallback)?;
    if request_line.len() >= MAX_CALLBACK_REQUEST_LINE_BYTES {
        return Err(AuthError::InvalidCallback);
    }
    let request_line =
        std::str::from_utf8(&request_line).map_err(|_| AuthError::InvalidCallback)?;
    let mut fields = request_line.trim_end().split(' ');
    let method = fields.next();
    let target = fields.next();
    let protocol = fields.next();
    if method != Some("GET")
        || !matches!(protocol, Some("HTTP/1.1" | "HTTP/1.0"))
        || fields.next().is_some()
    {
        return Err(AuthError::InvalidCallback);
    }
    let target = target.ok_or(AuthError::InvalidCallback)?;
    if !target.starts_with('/') || target.starts_with("//") {
        return Err(AuthError::InvalidCallback);
    }
    let callback = redirect_uri
        .join(target)
        .map_err(|_| AuthError::InvalidCallback)?;
    validate_callback_origin(&callback, redirect_uri)?;
    let body = b"Authentication received. You can return to DevHud.";
    let response = format!(
        "HTTP/1.1 200 OK\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Length: \
         {}\r\nCache-Control: no-store\r\nConnection: close\r\nReferrer-Policy: \
         no-referrer\r\nX-Content-Type-Options: nosniff\r\n\r\n",
        body.len()
    );
    // The browser may close immediately after sending the request. Failure to
    // write this credential-free acknowledgement must not turn a valid,
    // already-consumed callback into a retryable OAuth transaction.
    let _ = stream
        .write_all(response.as_bytes())
        .and_then(|()| stream.write_all(body));
    Ok(callback)
}

pub(crate) fn unix_time_now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_default()
        .as_secs()
}

/// Removes credential-shaped material before a native adapter adds context to
/// a diagnostic. Production diagnostics should normally log only `AuthError`.
pub(crate) fn redact_auth_text(value: &str) -> String {
    let without_query = value.split_once('?').map_or(value, |(prefix, _)| prefix);
    if Url::parse(without_query).is_ok() {
        return without_query.to_owned();
    }
    let mut redacted = Vec::new();
    for word in without_query.split_whitespace() {
        if word.starts_with("Bearer ") || word.matches('.').count() == 2 || word.len() > 80 {
            redacted.push("[REDACTED]");
        } else {
            redacted.push(word);
        }
    }
    redacted.join(" ")
}

#[cfg(test)]
mod tests {
    use std::{
        collections::VecDeque,
        net::TcpStream,
        sync::{Arc, Mutex},
        thread,
    };

    use super::*;

    const LOGTO_ENDPOINT: &str = "https://tenant.logto.app";
    const OIDC_ISSUER: &str = "https://tenant.logto.app/oidc";
    const NOW: u64 = 1_800_000_000;

    type FakeRetainedSession = (BTreeMap<AuthFeature, String>, String);

    #[derive(Default)]
    struct FakeVault {
        retained: Option<FakeRetainedSession>,
        fail_load: bool,
        fail_write: bool,
        fail_clear: bool,
        writes: Arc<Mutex<Vec<FakeRetainedSession>>>,
    }

    impl SecureVault for FakeVault {
        fn load(&mut self) -> Result<Option<VaultSession>, AuthError> {
            if self.fail_load {
                return Err(AuthError::SecureVaultUnavailable);
            }
            self.retained
                .as_ref()
                .map(|(refresh_tokens, device)| {
                    Ok(VaultSession {
                        refresh_tokens: refresh_tokens
                            .iter()
                            .map(|(feature, token)| Ok((*feature, Secret::new(token.clone())?)))
                            .collect::<Result<_, AuthError>>()?,
                        device_session_key: Secret::new(device.clone())?,
                    })
                })
                .transpose()
        }

        fn replace(&mut self, session: &VaultSession) -> Result<(), AuthError> {
            if self.fail_write {
                return Err(AuthError::SecureVaultWriteFailed);
            }
            let next = (
                session
                    .refresh_tokens
                    .iter()
                    .map(|(feature, token)| (*feature, token.expose().to_owned()))
                    .collect(),
                session.device_session_key.expose().to_owned(),
            );
            self.writes.lock().unwrap().push(next.clone());
            self.retained = Some(next);
            Ok(())
        }

        fn clear(&mut self) -> Result<(), AuthError> {
            if self.fail_clear {
                return Err(AuthError::SecureVaultDeleteFailed);
            }
            self.retained = None;
            Ok(())
        }
    }

    #[derive(Default)]
    struct FakeTransport {
        exchange: VecDeque<Result<TokenSet, AuthError>>,
        refresh: VecDeque<Result<TokenSet, TokenRefreshError>>,
        refresh_inputs: Vec<String>,
        refresh_requests: Vec<(String, Vec<String>)>,
        revoked: usize,
        fail_revoke: bool,
    }

    impl TokenTransport for FakeTransport {
        fn exchange_code(
            &mut self,
            endpoint: &Url,
            _client_id: &str,
            _code: &Secret,
            _verifier: &Secret,
            _redirect_uri: &Url,
            _nonce: &Secret,
        ) -> Result<TokenSet, AuthError> {
            assert_eq!(endpoint.as_str(), "https://tenant.logto.app/oidc/token");
            self.exchange.pop_front().unwrap()
        }

        fn refresh(
            &mut self,
            endpoint: &Url,
            _client_id: &str,
            refresh_token: &Secret,
            audience: &str,
            scopes: &[&str],
        ) -> Result<TokenSet, TokenRefreshError> {
            assert_eq!(endpoint.as_str(), "https://tenant.logto.app/oidc/token");
            self.refresh_inputs.push(refresh_token.expose().to_owned());
            self.refresh_requests.push((
                audience.to_owned(),
                scopes.iter().map(|scope| (*scope).to_owned()).collect(),
            ));
            self.refresh.pop_front().unwrap()
        }

        fn revoke(
            &mut self,
            endpoint: &Url,
            _client_id: &str,
            _refresh_token: &Secret,
        ) -> Result<(), AuthError> {
            assert_eq!(
                endpoint.as_str(),
                "https://tenant.logto.app/oidc/token/revocation"
            );
            self.revoked += 1;
            if self.fail_revoke {
                Err(AuthError::TokenExchangeFailed)
            } else {
                Ok(())
            }
        }
    }

    fn claims(subject: &str, audience: &str, scopes: &[&str]) -> TokenClaims {
        TokenClaims {
            issuer: OIDC_ISSUER.to_owned(),
            subject: subject.to_owned(),
            audiences: [audience.to_owned()].into_iter().collect(),
            scopes: scopes.iter().map(|value| (*value).to_owned()).collect(),
            expires_at_unix_seconds: NOW + 3600,
        }
    }

    fn tokens(subject: &str, audience: &str, scopes: &[&str], refresh: Option<&str>) -> TokenSet {
        TokenSet {
            access_token: Secret::new("memory-access-token").unwrap(),
            id_token: Some(Secret::new("memory-id-token").unwrap()),
            refresh_token: refresh.map(|value| Secret::new(value).unwrap()),
            claims: claims(subject, audience, scopes),
        }
    }

    fn queue_valid_grant_pair(
        transport: &mut FakeTransport,
        subject: &str,
        feature: AuthFeature,
        feature_refresh: Option<&str>,
        delibase_refresh: Option<&str>,
    ) {
        transport.refresh.push_back(Ok(tokens(
            subject,
            feature.audience(),
            feature.scopes(),
            feature_refresh,
        )));
        transport.refresh.push_back(Ok(tokens(
            subject,
            DELIBASE_AUDIENCE,
            feature.delibase_scopes(),
            delibase_refresh,
        )));
    }

    fn manager(
        transport: FakeTransport,
        vault: FakeVault,
    ) -> SessionManager<FakeTransport, FakeVault> {
        SessionManager::new(
            AuthConfiguration::new(LOGTO_ENDPOINT, "devhud-client").unwrap(),
            transport,
            vault,
        )
    }

    fn retained_grant(
        feature: AuthFeature,
        refresh_token: &str,
        device_session_key: &str,
    ) -> FakeRetainedSession {
        (
            [(feature, refresh_token.to_owned())].into_iter().collect(),
            device_session_key.to_owned(),
        )
    }

    fn callback_for(
        request: &AuthorizationRequest,
        code: &str,
        state_override: Option<&str>,
    ) -> Url {
        let state = request
            .authorization_url
            .query_pairs()
            .find(|(key, _)| key == "state")
            .unwrap()
            .1
            .into_owned();
        let mut callback = request.redirect_uri.clone();
        callback
            .query_pairs_mut()
            .append_pair("code", code)
            .append_pair("state", state_override.unwrap_or(&state));
        callback
    }

    #[test]
    fn configuration_and_redirects_are_exactly_bounded() {
        assert!(AuthConfiguration::new("http://tenant.logto.app", "id").is_err());
        assert!(AuthConfiguration::new("https://user@tenant.logto.app", "id").is_err());
        assert!(AuthConfiguration::new("https://tenant.logto.app/path", "id").is_err());
        let config = AuthConfiguration::new(LOGTO_ENDPOINT, "id").unwrap();
        assert_eq!(config.oidc_issuer().as_str(), OIDC_ISSUER);
        assert!(
            config.endpoint_allowed(&Url::parse("https://tenant.logto.app/oidc/auth").unwrap())
        );
        assert!(!config.endpoint_allowed(&Url::parse("https://tenant.logto.app/admin").unwrap()));
        assert_eq!(
            validate_redirect(
                AuthPlatform::Mobile,
                &Url::parse("https://deli.dev/auth/devhud/callback/extra").unwrap()
            ),
            Err(AuthError::InvalidCallback)
        );
        assert_eq!(
            validate_redirect(
                AuthPlatform::Desktop,
                &Url::parse("http://localhost:3000/auth/random-enough-path").unwrap()
            ),
            Err(AuthError::InvalidCallback)
        );
        assert!(is_mobile_callback_boundary(
            &Url::parse("https://deli.dev/auth/devhud/callback?code=a&state=b").unwrap()
        ));
        for callback in [
            "http://deli.dev/auth/devhud/callback",
            "https://deli.dev:444/auth/devhud/callback",
            "https://deli.dev/auth/devhud/callback/extra",
            "https://deli.dev/auth/devhud/callback#fragment",
            "https://deli.dev.evil.example/auth/devhud/callback",
        ] {
            assert!(!is_mobile_callback_boundary(&Url::parse(callback).unwrap()));
        }
    }

    #[test]
    fn pkce_state_is_high_entropy_and_callback_is_consumed_once() {
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh-a"),
        )));
        let mut session = manager(transport, FakeVault::default());
        let request = session
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        let query: std::collections::BTreeMap<_, _> = request
            .authorization_url
            .query_pairs()
            .map(|(key, value)| (key.into_owned(), value.into_owned()))
            .collect();
        assert_eq!(query.get("code_challenge_method"), Some(&"S256".to_owned()));
        assert_eq!(
            query.get("scope"),
            Some(&"deck:access delibase:deck:forward offline_access openid profile".to_owned())
        );
        assert_eq!(
            request
                .authorization_url
                .query_pairs()
                .filter(|(key, _)| key == "resource")
                .map(|(_, value)| value.into_owned())
                .collect::<Vec<_>>(),
            [DECK_AUDIENCE, DELIBASE_AUDIENCE]
        );
        let realqa_request = manager(FakeTransport::default(), FakeVault::default())
            .begin(
                AuthFeature::RealQa,
                AuthPlatform::Desktop,
                Url::parse("http://127.0.0.1:3000/auth/callback").unwrap(),
            )
            .unwrap();
        let realqa_query: std::collections::BTreeMap<_, _> = realqa_request
            .authorization_url
            .query_pairs()
            .map(|(key, value)| (key.into_owned(), value.into_owned()))
            .collect();
        assert_eq!(
            realqa_query.get("scope"),
            Some(&"delibase:realqa:forward offline_access openid profile realqa:access".to_owned())
        );
        assert_eq!(
            realqa_request
                .authorization_url
                .query_pairs()
                .filter(|(key, _)| key == "resource")
                .map(|(_, value)| value.into_owned())
                .collect::<Vec<_>>(),
            [REALQA_AUDIENCE, DELIBASE_AUDIENCE]
        );
        assert!(query["state"].len() >= 40);
        assert!(query["code_challenge"].len() >= 40);
        let wrong = callback_for(&request, "code", Some("wrong-state"));
        assert_eq!(
            session.complete_callback(&wrong, NOW),
            Err(AuthError::CallbackStateMismatch)
        );
        assert_eq!(
            session.complete_callback(&wrong, NOW),
            Err(AuthError::CallbackAlreadyConsumed)
        );

        let mut duplicate_transport = FakeTransport::default();
        duplicate_transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh-a"),
        )));
        let mut duplicate_manager = manager(duplicate_transport, FakeVault::default());
        let duplicate_request = duplicate_manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        let mut duplicate_callback = callback_for(&duplicate_request, "code", None);
        let state = duplicate_callback
            .query_pairs()
            .find(|(key, _)| key == "state")
            .unwrap()
            .1
            .into_owned();
        duplicate_callback
            .query_pairs_mut()
            .append_pair("state", &state);
        assert_eq!(
            duplicate_manager.complete_callback(&duplicate_callback, NOW),
            Err(AuthError::InvalidCallback)
        );
    }

    #[test]
    fn abandoned_authorization_expires_and_allows_retry() {
        let mut session = manager(FakeTransport::default(), FakeVault::default());
        session
            .begin_at(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
                NOW,
            )
            .unwrap();
        assert_eq!(
            session.expire_pending(NOW + CALLBACK_TIMEOUT.as_secs()),
            Err(AuthError::CallbackTimedOut)
        );
        assert_eq!(session.snapshot(), SessionSnapshot::SignedOut);
        assert!(
            session
                .begin_at(
                    AuthFeature::Deck,
                    AuthPlatform::Mobile,
                    Url::parse(MOBILE_CALLBACK).unwrap(),
                    NOW + CALLBACK_TIMEOUT.as_secs(),
                )
                .is_ok()
        );
    }

    #[test]
    fn successful_login_vaults_only_refresh_and_bound_device_key() {
        let writes = Arc::new(Mutex::new(Vec::new()));
        let vault = FakeVault {
            writes: Arc::clone(&writes),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh-a"),
        )));
        queue_valid_grant_pair(
            &mut transport,
            "account-a",
            AuthFeature::Deck,
            Some("refresh-feature-rotated"),
            Some("refresh-delibase-rotated"),
        );
        let mut manager = manager(transport, vault);
        let request = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        let callback = callback_for(&request, "code", None);
        assert_eq!(
            manager.complete_callback(&callback, NOW).unwrap(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert!(manager.memory_tokens_present());
        let writes = writes.lock().unwrap();
        assert_eq!(writes.len(), 1);
        assert_eq!(
            writes[0].0.get(&AuthFeature::Deck).map(String::as_str),
            Some("refresh-delibase-rotated")
        );
        assert!(writes[0].1.starts_with("v2."));
        assert!(!writes[0].1.contains("account-a"));
        assert!(!format!("{manager:?}").contains("memory-access-token"));
    }

    #[test]
    fn signed_in_account_can_authorize_another_feature_incrementally() {
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh-deck"),
        )));
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh-realqa"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::RealQa, None, None);
        let mut manager = manager(transport, FakeVault::default());
        let deck_request = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Desktop,
                Url::parse("http://127.0.0.1:3000/auth/callback").unwrap(),
            )
            .unwrap();
        manager
            .complete_callback(&callback_for(&deck_request, "deck-code", None), NOW)
            .unwrap();

        let realqa_request = manager
            .begin(
                AuthFeature::RealQa,
                AuthPlatform::Desktop,
                Url::parse("http://127.0.0.1:3001/auth/callback").unwrap(),
            )
            .unwrap();
        assert_eq!(manager.snapshot(), SessionSnapshot::Authenticating);
        assert_eq!(
            realqa_request
                .authorization_url
                .query_pairs()
                .filter(|(key, _)| key == "resource")
                .map(|(_, value)| value.into_owned())
                .collect::<Vec<_>>(),
            [REALQA_AUDIENCE, DELIBASE_AUDIENCE]
        );
        assert_eq!(
            manager
                .complete_callback(&callback_for(&realqa_request, "realqa-code", None), NOW)
                .unwrap(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert_eq!(
            manager.vault.retained.as_ref().map(|retained| &retained.0),
            Some(
                &[
                    (AuthFeature::Deck, "refresh-deck".to_owned()),
                    (AuthFeature::RealQa, "refresh-realqa".to_owned())
                ]
                .into_iter()
                .collect()
            )
        );
    }

    #[test]
    fn incremental_authorization_failure_restores_the_active_session() {
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh-a"),
        )));
        transport.exchange.push_back(Ok(tokens(
            "account-b",
            "devhud-client",
            &["openid"],
            Some("refresh-b"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        let mut manager = manager(transport, FakeVault::default());
        let initial = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        manager
            .complete_callback(&callback_for(&initial, "deck-code", None), NOW)
            .unwrap();

        let cancelled = manager
            .begin(
                AuthFeature::RealQa,
                AuthPlatform::Desktop,
                Url::parse("http://127.0.0.1:3000/auth/callback").unwrap(),
            )
            .unwrap();
        manager.cancel_pending();
        assert_eq!(
            manager.snapshot(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );

        let rejected_switch = manager
            .begin(
                AuthFeature::RealQa,
                AuthPlatform::Desktop,
                cancelled.redirect_uri,
            )
            .unwrap();
        assert_eq!(
            manager.complete_callback(&callback_for(&rejected_switch, "realqa-code", None), NOW),
            Err(AuthError::AccountSwitchRequiresLogout)
        );
        assert_eq!(
            manager.snapshot(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert!(manager.memory_tokens_present());
    }

    impl<T: TokenTransport, V: SecureVault> fmt::Debug for SessionManager<T, V> {
        fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
            formatter
                .debug_struct("SessionManager")
                .field("configuration", &self.configuration)
                .field("state", &self.snapshot())
                .field("pending", &self.pending.as_ref().map(|_| "[REDACTED]"))
                .finish()
        }
    }

    #[test]
    fn existing_device_binding_rejects_account_switch_until_logout() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::Deck,
                "old-refresh",
                key.expose(),
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-b",
            "devhud-client",
            &["openid"],
            Some("new-refresh"),
        )));
        let mut manager = manager(transport, vault);
        let request = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        let callback = callback_for(&request, "code", None);
        assert_eq!(
            manager.complete_callback(&callback, NOW),
            Err(AuthError::AccountSwitchRequiresLogout)
        );
    }

    #[test]
    fn sign_in_replaces_a_malformed_retained_session_before_callback() {
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::Deck,
                "stale-refresh",
                "future-schema",
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("new-refresh"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        let mut manager = manager(transport, vault);

        let request = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        assert!(manager.vault.retained.is_none());
        assert_eq!(
            manager
                .complete_callback(&callback_for(&request, "code", None), NOW)
                .unwrap(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert_eq!(
            manager
                .vault
                .retained
                .as_ref()
                .and_then(|retained| retained.0.get(&AuthFeature::Deck))
                .map(String::as_str),
            Some("new-refresh")
        );
    }

    #[test]
    fn bearer_pair_requires_matching_subject_audience_and_scope() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            AuthFeature::Deck.scopes(),
            None,
        )));
        transport.refresh.push_back(Ok(tokens(
            "account-b",
            DELIBASE_AUDIENCE,
            AuthFeature::Deck.delibase_scopes(),
            None,
        )));
        let mut manager = manager(transport, vault);
        let request = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        manager
            .complete_callback(&callback_for(&request, "code", None), NOW)
            .unwrap();
        manager.transport.refresh_requests.clear();
        assert!(matches!(
            manager.bearer_pair(AuthFeature::Deck, NOW),
            Err(AuthError::SubjectMismatch)
        ));
        assert_eq!(
            manager.transport.refresh_requests,
            [
                (DECK_AUDIENCE.to_owned(), vec!["deck:access".to_owned()]),
                (
                    DELIBASE_AUDIENCE.to_owned(),
                    vec!["delibase:deck:forward".to_owned()]
                ),
            ]
        );
    }

    #[test]
    fn callback_requires_valid_feature_and_delibase_grants_before_vaulting() {
        let mut invalid_feature_transport = FakeTransport::default();
        invalid_feature_transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh"),
        )));
        invalid_feature_transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            &["openid"],
            None,
        )));
        let mut invalid_feature = manager(invalid_feature_transport, FakeVault::default());
        let request = invalid_feature
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        assert_eq!(
            invalid_feature.complete_callback(&callback_for(&request, "code", None), NOW),
            Err(AuthError::ScopeMismatch)
        );
        assert!(invalid_feature.vault.retained.is_none());
        assert_eq!(invalid_feature.snapshot(), SessionSnapshot::SignedOut);

        let mut invalid_delibase_transport = FakeTransport::default();
        invalid_delibase_transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh"),
        )));
        invalid_delibase_transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            AuthFeature::Deck.scopes(),
            Some("refresh-rotated"),
        )));
        invalid_delibase_transport.refresh.push_back(Ok(tokens(
            "account-a",
            DELIBASE_AUDIENCE,
            &["openid"],
            None,
        )));
        let mut invalid_delibase = manager(invalid_delibase_transport, FakeVault::default());
        let request = invalid_delibase
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        assert_eq!(
            invalid_delibase.complete_callback(&callback_for(&request, "code", None), NOW),
            Err(AuthError::ScopeMismatch)
        );
        assert_eq!(
            invalid_delibase.transport.refresh_inputs,
            ["refresh", "refresh-rotated"]
        );
        assert!(invalid_delibase.vault.retained.is_none());
        assert_eq!(invalid_delibase.snapshot(), SessionSnapshot::SignedOut);
    }

    #[test]
    fn feature_refresh_rotation_is_vaulted_before_delibase_refresh() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::Deck,
                "refresh-original",
                key.expose(),
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            AuthFeature::Deck.scopes(),
            Some("refresh-rotated"),
        )));
        transport
            .refresh
            .push_back(Err(AuthError::TokenExchangeFailed.into()));
        let mut manager = manager(transport, vault);
        manager.state = SessionState::SignedIn {
            subject: "account-a".to_owned(),
            access_token: Secret::new("memory-access-token").unwrap(),
            id_token: Some(Secret::new("memory-id-token").unwrap()),
        };

        assert!(matches!(
            manager.bearer_pair(AuthFeature::Deck, NOW),
            Err(AuthError::TokenExchangeFailed)
        ));
        assert_eq!(
            manager.transport.refresh_inputs,
            ["refresh-original", "refresh-rotated"]
        );
        assert_eq!(
            manager
                .vault
                .retained
                .as_ref()
                .and_then(|retained| retained.0.get(&AuthFeature::Deck))
                .map(String::as_str),
            Some("refresh-rotated")
        );
    }

    #[test]
    fn bearer_pair_vaults_rotation_from_retryable_verification_failure() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::Deck,
                "refresh-original",
                key.expose(),
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport
            .refresh
            .push_back(Err(TokenRefreshError::with_rotated_refresh_token(
                AuthError::TransportUnavailable,
                Some(Secret::new("refresh-rotated").unwrap()),
            )));
        let mut manager = manager(transport, vault);
        manager.state = SessionState::SignedIn {
            subject: "account-a".to_owned(),
            access_token: Secret::new("memory-access-token").unwrap(),
            id_token: Some(Secret::new("memory-id-token").unwrap()),
        };

        assert!(matches!(
            manager.bearer_pair(AuthFeature::Deck, NOW),
            Err(AuthError::TransportUnavailable)
        ));
        assert_eq!(
            manager
                .vault
                .retained
                .as_ref()
                .and_then(|retained| retained.0.get(&AuthFeature::Deck))
                .map(String::as_str),
            Some("refresh-rotated")
        );
    }

    #[test]
    fn bearer_validation_rejects_issuer_audience_and_scope_substitution() {
        let configuration = AuthConfiguration::new(LOGTO_ENDPOINT, "devhud-client").unwrap();
        let mut bare_endpoint_issuer =
            claims("account-a", DECK_AUDIENCE, AuthFeature::Deck.scopes());
        bare_endpoint_issuer.issuer = LOGTO_ENDPOINT.to_owned();
        assert_eq!(
            validate_bearer(
                &bare_endpoint_issuer,
                &configuration,
                "account-a",
                DECK_AUDIENCE,
                AuthFeature::Deck.scopes(),
                NOW,
            ),
            Err(AuthError::TokenInvalid)
        );
        let wrong_audience = claims("account-a", REALQA_AUDIENCE, AuthFeature::Deck.scopes());
        assert_eq!(
            validate_bearer(
                &wrong_audience,
                &configuration,
                "account-a",
                DECK_AUDIENCE,
                AuthFeature::Deck.scopes(),
                NOW,
            ),
            Err(AuthError::AudienceMismatch)
        );
        let missing_scope = claims("account-a", DECK_AUDIENCE, &["openid"]);
        assert_eq!(
            validate_bearer(
                &missing_scope,
                &configuration,
                "account-a",
                DECK_AUDIENCE,
                AuthFeature::Deck.scopes(),
                NOW,
            ),
            Err(AuthError::ScopeMismatch)
        );
    }

    #[test]
    fn first_time_offline_is_rejected_but_prior_binding_is_gated() {
        let mut first = manager(FakeTransport::default(), FakeVault::default());
        assert_eq!(
            first.restore(Connectivity::Offline),
            Err(AuthError::FirstTimeOffline)
        );
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            ..FakeVault::default()
        };
        let mut prior = manager(FakeTransport::default(), vault);
        assert_eq!(
            prior.restore(Connectivity::Offline).unwrap(),
            SessionSnapshot::PriorSessionOffline
        );
        assert!(matches!(
            prior.bearer_pair(AuthFeature::RealQa, NOW),
            Err(AuthError::ReauthenticationRequired)
        ));
        assert_eq!(
            prior.realqa_draft_access(),
            Err(AuthError::ReauthenticationRequired)
        );

        let realqa_key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::RealQa,
                "refresh",
                realqa_key.expose(),
            )),
            ..FakeVault::default()
        };
        let mut realqa_prior = manager(FakeTransport::default(), vault);
        realqa_prior.restore(Connectivity::Offline).unwrap();
        let access = realqa_prior.realqa_draft_access().unwrap();
        assert!(!access.online_reauthenticated);
        assert_eq!(
            access.account_binding,
            realqa_draft_account_binding("account-a")
        );
    }

    #[test]
    fn draft_binding_survives_same_account_relogin_and_separates_accounts() {
        let first = new_device_session_key("account-a").unwrap();
        let relogin = new_device_session_key("account-a").unwrap();
        let other = new_device_session_key("account-b").unwrap();

        assert_ne!(first.expose(), relogin.expose());
        assert_eq!(
            device_session_account_binding(first.expose()).unwrap(),
            device_session_account_binding(relogin.expose()).unwrap()
        );
        assert_ne!(
            device_session_account_binding(first.expose()).unwrap(),
            device_session_account_binding(other.expose()).unwrap()
        );
        assert!(device_session_matches(first.expose(), "account-a").unwrap());
        assert!(!device_session_matches(first.expose(), "account-b").unwrap());
    }

    #[test]
    fn retained_session_rehydrates_online_and_persists_rotation() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::Deck,
                "refresh-original",
                key.expose(),
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            AuthFeature::Deck.scopes(),
            Some("refresh-rotated"),
        )));
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DELIBASE_AUDIENCE,
            AuthFeature::Deck.delibase_scopes(),
            Some("refresh-delibase-rotated"),
        )));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW).unwrap(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert!(prior.memory_tokens_present());
        assert_eq!(
            prior.transport.refresh_requests,
            [
                (DECK_AUDIENCE.to_owned(), vec!["deck:access".to_owned()]),
                (
                    DELIBASE_AUDIENCE.to_owned(),
                    vec!["delibase:deck:forward".to_owned()]
                ),
            ]
        );
        assert_eq!(
            prior.transport.refresh_inputs,
            ["refresh-original", "refresh-rotated"]
        );
        assert_eq!(
            prior
                .vault
                .retained
                .as_ref()
                .and_then(|retained| retained.0.get(&AuthFeature::Deck))
                .map(String::as_str),
            Some("refresh-delibase-rotated")
        );
    }

    #[test]
    fn retained_session_tries_another_grant_after_transport_failure() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some((
                [
                    (AuthFeature::Deck, "refresh-deck".to_owned()),
                    (AuthFeature::RealQa, "refresh-realqa".to_owned()),
                ]
                .into_iter()
                .collect(),
                key.expose().to_owned(),
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport
            .refresh
            .push_back(Err(AuthError::TransportUnavailable.into()));
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            REALQA_AUDIENCE,
            AuthFeature::RealQa.scopes(),
            None,
        )));
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DELIBASE_AUDIENCE,
            AuthFeature::RealQa.delibase_scopes(),
            None,
        )));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW).unwrap(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert_eq!(
            prior.transport.refresh_inputs,
            ["refresh-deck", "refresh-realqa", "refresh-realqa"]
        );
    }

    #[test]
    fn retained_session_tries_another_grant_after_invalid_bearer() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some((
                [
                    (AuthFeature::Deck, "refresh-deck".to_owned()),
                    (AuthFeature::RealQa, "refresh-realqa".to_owned()),
                ]
                .into_iter()
                .collect(),
                key.expose().to_owned(),
            )),
            ..FakeVault::default()
        };
        let mut expired = tokens("account-a", DECK_AUDIENCE, AuthFeature::Deck.scopes(), None);
        expired.claims.expires_at_unix_seconds = NOW;
        let mut transport = FakeTransport::default();
        transport.refresh.push_back(Ok(expired));
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            REALQA_AUDIENCE,
            AuthFeature::RealQa.scopes(),
            None,
        )));
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DELIBASE_AUDIENCE,
            AuthFeature::RealQa.delibase_scopes(),
            None,
        )));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW).unwrap(),
            SessionSnapshot::SignedIn {
                subject: "account-a".to_owned()
            }
        );
        assert_eq!(
            prior.transport.refresh_inputs,
            ["refresh-deck", "refresh-realqa", "refresh-realqa"]
        );
    }

    #[test]
    fn retained_session_requires_the_paired_delibase_grant() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            AuthFeature::Deck.scopes(),
            None,
        )));
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DELIBASE_AUDIENCE,
            &["openid"],
            None,
        )));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW),
            Err(AuthError::ScopeMismatch)
        );
        assert!(!prior.memory_tokens_present());
    }

    #[test]
    fn retained_session_falls_back_offline_when_delibase_refresh_is_unavailable() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport.refresh.push_back(Ok(tokens(
            "account-a",
            DECK_AUDIENCE,
            AuthFeature::Deck.scopes(),
            None,
        )));
        transport
            .refresh
            .push_back(Err(AuthError::TransportUnavailable.into()));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW).unwrap(),
            SessionSnapshot::PriorSessionOffline
        );
        assert!(!prior.memory_tokens_present());
    }

    #[test]
    fn retained_session_vaults_rotation_before_falling_back_offline() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport
            .refresh
            .push_back(Err(TokenRefreshError::with_rotated_refresh_token(
                AuthError::TransportUnavailable,
                Some(Secret::new("refresh-rotated").unwrap()),
            )));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW).unwrap(),
            SessionSnapshot::PriorSessionOffline
        );
        assert!(!prior.memory_tokens_present());
        assert_eq!(
            prior
                .vault
                .retained
                .as_ref()
                .and_then(|retained| retained.0.get(&AuthFeature::Deck))
                .map(String::as_str),
            Some("refresh-rotated")
        );
    }

    #[test]
    fn retained_session_does_not_mask_invalid_grant_with_transport_failure() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some((
                [
                    (AuthFeature::Deck, "refresh-deck".to_owned()),
                    (AuthFeature::RealQa, "refresh-realqa".to_owned()),
                ]
                .into_iter()
                .collect(),
                key.expose().to_owned(),
            )),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport
            .refresh
            .push_back(Err(AuthError::ReauthenticationRequired.into()));
        transport
            .refresh
            .push_back(Err(AuthError::TransportUnavailable.into()));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW),
            Err(AuthError::ReauthenticationRequired)
        );
        assert!(!prior.memory_tokens_present());
    }

    #[test]
    fn retained_session_requires_reauthentication_after_invalid_grant() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            ..FakeVault::default()
        };
        let mut transport = FakeTransport::default();
        transport
            .refresh
            .push_back(Err(AuthError::ReauthenticationRequired.into()));
        let mut prior = manager(transport, vault);

        assert_eq!(
            prior.restore_at(Connectivity::Online, NOW),
            Err(AuthError::ReauthenticationRequired)
        );
        assert!(!prior.memory_tokens_present());
    }

    #[test]
    fn reset_clears_a_vault_that_cannot_be_deserialized() {
        let vault = FakeVault {
            retained: Some(retained_grant(
                AuthFeature::Deck,
                "corrupt",
                "future-schema",
            )),
            fail_load: true,
            ..FakeVault::default()
        };
        let mut manager = manager(FakeTransport::default(), vault);
        manager.state = SessionState::SignedIn {
            subject: "account-a".to_owned(),
            access_token: Secret::new("memory-access-token").unwrap(),
            id_token: Some(Secret::new("memory-id-token").unwrap()),
        };

        assert_eq!(manager.reset().unwrap(), SessionSnapshot::SignedOut);
        assert!(manager.vault.retained.is_none());
        assert!(!manager.memory_tokens_present());
        assert_eq!(manager.snapshot(), SessionSnapshot::SignedOut);
    }

    #[test]
    fn vault_failures_do_not_install_tokens_and_failed_clear_locks_session() {
        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        let vault = FakeVault {
            fail_write: true,
            ..FakeVault::default()
        };
        let mut write_failure_manager = manager(transport, vault);
        let request = write_failure_manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        assert_eq!(
            write_failure_manager.complete_callback(&callback_for(&request, "code", None), NOW),
            Err(AuthError::SecureVaultWriteFailed)
        );
        assert!(!write_failure_manager.memory_tokens_present());

        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            fail_clear: true,
            ..FakeVault::default()
        };
        let mut clear_failure_manager = manager(FakeTransport::default(), vault);
        assert_eq!(
            clear_failure_manager.reset(),
            Err(AuthError::SecureVaultDeleteFailed)
        );
        assert_eq!(
            clear_failure_manager.snapshot(),
            SessionSnapshot::CleanupRequired
        );

        let mut transport = FakeTransport::default();
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        let mut load_failure_manager = manager(transport, FakeVault::default());
        let request = load_failure_manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        load_failure_manager
            .complete_callback(&callback_for(&request, "code", None), NOW)
            .unwrap();
        load_failure_manager.vault.fail_load = true;
        assert_eq!(
            load_failure_manager.logout().unwrap(),
            SessionSnapshot::SignedOut
        );
        assert!(!load_failure_manager.memory_tokens_present());
        assert_eq!(load_failure_manager.snapshot(), SessionSnapshot::SignedOut);
        load_failure_manager.vault.retained = Some(retained_grant(
            AuthFeature::Deck,
            "refresh",
            new_device_session_key("account-a").unwrap().expose(),
        ));
        load_failure_manager.vault.fail_clear = true;
        assert_eq!(
            load_failure_manager.logout(),
            Err(AuthError::SecureVaultDeleteFailed)
        );
        assert_eq!(
            load_failure_manager.snapshot(),
            SessionSnapshot::CleanupRequired
        );
    }

    #[test]
    fn logout_clears_memory_and_vault_even_when_remote_revocation_fails() {
        let mut transport = FakeTransport {
            fail_revoke: true,
            ..FakeTransport::default()
        };
        transport.exchange.push_back(Ok(tokens(
            "account-a",
            "devhud-client",
            &["openid"],
            Some("refresh"),
        )));
        queue_valid_grant_pair(&mut transport, "account-a", AuthFeature::Deck, None, None);
        let mut manager = manager(transport, FakeVault::default());
        let request = manager
            .begin(
                AuthFeature::Deck,
                AuthPlatform::Mobile,
                Url::parse(MOBILE_CALLBACK).unwrap(),
            )
            .unwrap();
        manager
            .complete_callback(&callback_for(&request, "code", None), NOW)
            .unwrap();
        assert!(manager.memory_tokens_present());
        assert_eq!(manager.logout().unwrap(), SessionSnapshot::SignedOut);
        assert!(!manager.memory_tokens_present());
        assert!(manager.vault.load().unwrap().is_none());
        assert_eq!(manager.transport.revoked, 1);
    }

    #[test]
    fn logout_clears_the_vault_before_remote_revocation() {
        let key = new_device_session_key("account-a").unwrap();
        let vault = FakeVault {
            retained: Some(retained_grant(AuthFeature::Deck, "refresh", key.expose())),
            fail_clear: true,
            ..FakeVault::default()
        };
        let mut manager = manager(FakeTransport::default(), vault);

        assert_eq!(manager.logout(), Err(AuthError::SecureVaultDeleteFailed));
        assert_eq!(manager.transport.revoked, 0);
    }

    #[test]
    fn loopback_uses_random_port_stable_path_and_shuts_down_after_callback() {
        let callback = LoopbackCallback::bind().unwrap();
        let redirect = callback.redirect_uri().clone();
        assert_eq!(redirect.host_str(), Some("127.0.0.1"));
        assert!(redirect.port().unwrap() > 0);
        assert_eq!(redirect.path(), DESKTOP_CALLBACK_PATH);
        let address = format!("127.0.0.1:{}", redirect.port().unwrap());
        let target = format!("{}?code=test&state=test", redirect.path());
        let writer = thread::spawn(move || {
            let mut stream = loop {
                if let Ok(stream) = TcpStream::connect(&address) {
                    break stream;
                }
                thread::yield_now();
            };
            write!(
                stream,
                "GET {target} HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n"
            )
            .unwrap();
        });
        let received = callback
            .receive_with_timeout(Duration::from_secs(2))
            .unwrap();
        writer.join().unwrap();
        assert_eq!(received.path(), redirect.path());
        assert!(TcpStream::connect(format!("127.0.0.1:{}", redirect.port().unwrap())).is_err());
    }

    #[test]
    fn callback_rejects_wrong_path_and_listener_still_closes() {
        let callback = LoopbackCallback::bind().unwrap();
        let port = callback.redirect_uri().port().unwrap();
        let writer = thread::spawn(move || {
            let mut stream = TcpStream::connect(("127.0.0.1", port)).unwrap();
            write!(
                stream,
                "GET /auth/wrong-random-path?code=x&state=y HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n"
            )
            .unwrap();
        });
        assert_eq!(
            callback.receive_with_timeout(Duration::from_secs(2)),
            Err(AuthError::InvalidCallback)
        );
        writer.join().unwrap();
        assert!(TcpStream::connect(("127.0.0.1", port)).is_err());
    }

    #[test]
    fn errors_and_redaction_never_expose_tokens_or_query_credentials() {
        let value = "https://tenant.logto.app/oidc/auth?code=secret Bearer abc \
                     eyJhbGciOiJub25lIn0.eyJzdWIiOiJhIn0.signature";
        let redacted = redact_auth_text(value);
        assert_eq!(redacted, "https://tenant.logto.app/oidc/auth");
        assert!(!format!("{:?}", AuthError::TokenExchangeFailed).contains("secret"));
        assert!(!format!("{}", AuthError::TokenExchangeFailed).contains("secret"));
        assert!(!format!("{:?}", AuthError::TransportUnavailable).contains("secret"));
        assert!(!format!("{}", AuthError::TransportUnavailable).contains("secret"));
    }
}
