//! Production adapters for the closed authentication core.
//!
//! Only this module owns HTTP and OS-vault implementations. Callers supply no
//! URL, method, header, issuer, audience, scope, or vault key.

use std::{collections::BTreeSet, sync::Mutex, time::Duration};

use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header, jwk::JwkSet};
use reqwest::{blocking::Client, redirect::Policy};
use serde::{Deserialize, Serialize};
#[cfg(any(target_os = "android", target_os = "ios"))]
use tauri::{AppHandle, Manager};
#[cfg(any(target_os = "android", target_os = "ios"))]
use tauri_plugin_devhud_auth::DevHudAuthBridgeExt;
use url::Url;
use zeroize::Zeroizing;

use crate::auth::{
    AuthConfiguration, AuthError, AuthFeature, AuthPlatform, Connectivity, LoopbackCallback,
    Secret, SecureVault, SessionManager, SessionSnapshot, TokenClaims, TokenSet, TokenTransport,
    VaultSession, unix_time_now,
};

const ISSUER_VARIABLE: &str = "DEVHUD_LOGTO_ENDPOINT";
const CLIENT_ID_VARIABLE: &str = "DEVHUD_LOGTO_APP_ID";
const VAULT_SERVICE: &str = "dev.deli.devhud.auth";
const VAULT_ACCOUNT: &str = "active-session";
const HTTP_TIMEOUT: Duration = Duration::from_secs(20);

#[derive(Deserialize)]
#[serde(untagged)]
enum AudienceClaim {
    One(String),
    Many(Vec<String>),
}

#[derive(Deserialize)]
struct VerifiedClaims {
    iss: String,
    sub: String,
    aud: AudienceClaim,
    exp: u64,
    #[serde(default)]
    scope: String,
    nonce: Option<String>,
}

#[derive(Deserialize)]
struct OAuthTokenResponse {
    access_token: String,
    id_token: Option<String>,
    refresh_token: Option<String>,
    token_type: String,
}

struct HttpTokenTransport {
    client: Client,
    issuer: String,
    jwks_endpoint: Url,
}

impl HttpTokenTransport {
    fn new(configuration: &AuthConfiguration) -> Result<Self, AuthError> {
        let client = Client::builder()
            .https_only(true)
            .redirect(Policy::none())
            .timeout(HTTP_TIMEOUT)
            .user_agent("DevHud/0.1")
            .build()
            .map_err(|_| AuthError::ConfigurationUnavailable)?;
        Ok(Self {
            client,
            issuer: configuration
                .issuer()
                .as_str()
                .trim_end_matches('/')
                .to_owned(),
            jwks_endpoint: configuration.jwks_endpoint(),
        })
    }

    fn verify(
        &self,
        encoded: &str,
        expected_audience: &str,
        expected_nonce: Option<&str>,
    ) -> Result<TokenClaims, AuthError> {
        let header = decode_header(encoded).map_err(|_| AuthError::TokenInvalid)?;
        if header.alg != Algorithm::RS256 {
            return Err(AuthError::TokenInvalid);
        }
        let key_id = header.kid.ok_or(AuthError::TokenInvalid)?;
        let response = self
            .client
            .get(self.jwks_endpoint.clone())
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
            .send()
            .map_err(|_| AuthError::TokenExchangeFailed)?;
        if !response.status().is_success() {
            return Err(AuthError::TokenExchangeFailed);
        }
        let set: JwkSet = response.json().map_err(|_| AuthError::TokenInvalid)?;
        let jwk = set.find(&key_id).ok_or(AuthError::TokenInvalid)?;
        let key = DecodingKey::from_jwk(jwk).map_err(|_| AuthError::TokenInvalid)?;
        let mut validation = Validation::new(Algorithm::RS256);
        validation.set_issuer(&[self.issuer.as_str()]);
        validation.set_audience(&[expected_audience]);
        validation.set_required_spec_claims(&["exp", "iss", "sub", "aud"]);
        let claims = decode::<VerifiedClaims>(encoded, &key, &validation)
            .map_err(|_| AuthError::TokenInvalid)?
            .claims;
        if expected_nonce.is_some_and(|expected| claims.nonce.as_deref() != Some(expected)) {
            return Err(AuthError::TokenInvalid);
        }
        let audiences = match claims.aud {
            AudienceClaim::One(value) => [value].into_iter().collect(),
            AudienceClaim::Many(values) => values.into_iter().collect(),
        };
        Ok(TokenClaims {
            issuer: claims.iss,
            subject: claims.sub,
            audiences,
            scopes: claims
                .scope
                .split_ascii_whitespace()
                .map(str::to_owned)
                .collect::<BTreeSet<_>>(),
            expires_at_unix_seconds: claims.exp,
        })
    }

    fn post_token(
        &self,
        endpoint: &Url,
        parameters: &[(&str, &str)],
    ) -> Result<OAuthTokenResponse, AuthError> {
        let response = self
            .client
            .post(endpoint.clone())
            .header("Accept", "application/json")
            .header("Cache-Control", "no-store")
            .form(parameters)
            .send()
            .map_err(|_| AuthError::TokenExchangeFailed)?;
        if !response.status().is_success() {
            return Err(AuthError::TokenExchangeFailed);
        }
        let response: OAuthTokenResponse = response.json().map_err(|_| AuthError::TokenInvalid)?;
        if response.token_type != "Bearer" {
            return Err(AuthError::TokenInvalid);
        }
        Ok(response)
    }
}

impl TokenTransport for HttpTokenTransport {
    fn exchange_code(
        &mut self,
        endpoint: &Url,
        client_id: &str,
        code: &Secret,
        verifier: &Secret,
        redirect_uri: &Url,
        nonce: &Secret,
    ) -> Result<TokenSet, AuthError> {
        let response = self.post_token(
            endpoint,
            &[
                ("grant_type", "authorization_code"),
                ("client_id", client_id),
                ("code", code.expose()),
                ("code_verifier", verifier.expose()),
                ("redirect_uri", redirect_uri.as_str()),
            ],
        )?;
        let id_token = response.id_token.ok_or(AuthError::TokenInvalid)?;
        let claims = self.verify(&id_token, client_id, Some(nonce.expose()))?;
        Ok(TokenSet {
            access_token: Secret::new(response.access_token)?,
            id_token: Some(Secret::new(id_token)?),
            refresh_token: response.refresh_token.map(Secret::new).transpose()?,
            claims,
        })
    }

    fn refresh(
        &mut self,
        endpoint: &Url,
        client_id: &str,
        refresh_token: &Secret,
        audience: &str,
        scopes: &[&str],
    ) -> Result<TokenSet, AuthError> {
        let joined_scopes = scopes.join(" ");
        let response = self.post_token(
            endpoint,
            &[
                ("grant_type", "refresh_token"),
                ("client_id", client_id),
                ("refresh_token", refresh_token.expose()),
                ("resource", audience),
                ("scope", &joined_scopes),
            ],
        )?;
        let claims = self.verify(&response.access_token, audience, None)?;
        Ok(TokenSet {
            access_token: Secret::new(response.access_token)?,
            id_token: response.id_token.map(Secret::new).transpose()?,
            refresh_token: response.refresh_token.map(Secret::new).transpose()?,
            claims,
        })
    }

    fn revoke(
        &mut self,
        endpoint: &Url,
        client_id: &str,
        refresh_token: &Secret,
    ) -> Result<(), AuthError> {
        let response = self
            .client
            .post(endpoint.clone())
            .header("Cache-Control", "no-store")
            .form(&[
                ("client_id", client_id),
                ("token", refresh_token.expose()),
                ("token_type_hint", "refresh_token"),
            ])
            .send()
            .map_err(|_| AuthError::TokenExchangeFailed)?;
        if response.status().is_success() {
            Ok(())
        } else {
            Err(AuthError::TokenExchangeFailed)
        }
    }
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
#[derive(Default)]
struct PlatformVault;

#[cfg(not(any(target_os = "android", target_os = "ios")))]
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RetainedVaultSession {
    refresh_token: String,
    device_session_key: String,
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
impl PlatformVault {
    fn entry() -> Result<keyring::Entry, AuthError> {
        keyring::Entry::new(VAULT_SERVICE, VAULT_ACCOUNT)
            .map_err(|_| AuthError::SecureVaultUnavailable)
    }
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
impl SecureVault for PlatformVault {
    fn load(&mut self) -> Result<Option<VaultSession>, AuthError> {
        let entry = Self::entry()?;
        let encoded = match entry.get_password() {
            Ok(value) => Zeroizing::new(value),
            Err(keyring::Error::NoEntry) => return Ok(None),
            Err(_) => return Err(AuthError::SecureVaultUnavailable),
        };
        let retained: RetainedVaultSession =
            serde_json::from_str(&encoded).map_err(|_| AuthError::SecureVaultUnavailable)?;
        Ok(Some(VaultSession {
            refresh_token: Secret::new(retained.refresh_token)?,
            device_session_key: Secret::new(retained.device_session_key)?,
        }))
    }

    fn replace(&mut self, session: &VaultSession) -> Result<(), AuthError> {
        let encoded = Zeroizing::new(
            serde_json::to_string(&RetainedVaultSession {
                refresh_token: session.refresh_token.expose().to_owned(),
                device_session_key: session.device_session_key.expose().to_owned(),
            })
            .map_err(|_| AuthError::SecureVaultWriteFailed)?,
        );
        Self::entry()?
            .set_password(&encoded)
            .map_err(|_| AuthError::SecureVaultWriteFailed)
    }

    fn clear(&mut self) -> Result<(), AuthError> {
        match Self::entry()?.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(AuthError::SecureVaultDeleteFailed),
        }
    }
}

#[cfg(any(target_os = "android", target_os = "ios"))]
struct PlatformVault {
    app: AppHandle<crate::ActiveRuntime>,
}

#[cfg(any(target_os = "android", target_os = "ios"))]
impl SecureVault for PlatformVault {
    fn load(&mut self) -> Result<Option<VaultSession>, AuthError> {
        let Some(encoded) = self
            .app
            .devhud_auth_bridge()
            .read_session()
            .map_err(|_| AuthError::SecureVaultUnavailable)?
        else {
            return Ok(None);
        };
        let encoded = Zeroizing::new(encoded);
        let retained: RetainedMobileVaultSession =
            serde_json::from_str(&encoded).map_err(|_| AuthError::SecureVaultUnavailable)?;
        Ok(Some(VaultSession {
            refresh_token: Secret::new(retained.refresh_token)?,
            device_session_key: Secret::new(retained.device_session_key)?,
        }))
    }

    fn replace(&mut self, session: &VaultSession) -> Result<(), AuthError> {
        let encoded = Zeroizing::new(
            serde_json::to_string(&RetainedMobileVaultSession {
                refresh_token: session.refresh_token.expose().to_owned(),
                device_session_key: session.device_session_key.expose().to_owned(),
            })
            .map_err(|_| AuthError::SecureVaultWriteFailed)?,
        );
        self.app
            .devhud_auth_bridge()
            .write_session(encoded.to_string())
            .map_err(|_| AuthError::SecureVaultWriteFailed)
    }

    fn clear(&mut self) -> Result<(), AuthError> {
        self.app
            .devhud_auth_bridge()
            .clear_session()
            .map_err(|_| AuthError::SecureVaultDeleteFailed)
    }
}

#[cfg(any(target_os = "android", target_os = "ios"))]
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct RetainedMobileVaultSession {
    refresh_token: String,
    device_session_key: String,
}

type NativeManager = SessionManager<HttpTokenTransport, PlatformVault>;

pub(crate) struct NativeAuthState {
    manager: Mutex<Option<NativeManager>>,
    fallback_vault: Mutex<PlatformVault>,
    #[cfg(target_os = "ios")]
    mobile_callback: Mutex<Option<Url>>,
}

impl NativeAuthState {
    pub(crate) fn initialize(
        #[cfg(any(target_os = "android", target_os = "ios"))] app: &AppHandle<crate::ActiveRuntime>,
    ) -> Self {
        let platform_vault = || {
            #[cfg(not(any(target_os = "android", target_os = "ios")))]
            {
                PlatformVault
            }
            #[cfg(any(target_os = "android", target_os = "ios"))]
            {
                PlatformVault { app: app.clone() }
            }
        };
        let manager = (|| {
            let issuer = std::env::var(ISSUER_VARIABLE).ok()?;
            let client_id = std::env::var(CLIENT_ID_VARIABLE).ok()?;
            let configuration = AuthConfiguration::new(&issuer, &client_id).ok()?;
            let transport = HttpTokenTransport::new(&configuration).ok()?;
            Some(SessionManager::new(
                configuration,
                transport,
                platform_vault(),
            ))
        })();
        Self {
            manager: Mutex::new(manager),
            fallback_vault: Mutex::new(platform_vault()),
            #[cfg(target_os = "ios")]
            mobile_callback: Mutex::new(None),
        }
    }

    pub(crate) fn snapshot(&self) -> Result<SessionSnapshot, AuthError> {
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        let Some(manager) = guard.as_mut() else {
            return self
                .fallback_vault
                .lock()
                .map_err(|_| AuthError::SecureVaultUnavailable)?
                .load()
                .map(|session| {
                    if session.is_some() {
                        SessionSnapshot::PriorSessionOffline
                    } else {
                        SessionSnapshot::SignedOut
                    }
                });
        };
        let current = manager.snapshot();
        if current != SessionSnapshot::SignedOut {
            return Ok(current);
        }
        match manager.restore(Connectivity::Offline) {
            Ok(snapshot) => Ok(snapshot),
            Err(AuthError::FirstTimeOffline) => Ok(SessionSnapshot::SignedOut),
            Err(error) => Err(error),
        }
    }

    pub(crate) fn begin_desktop(
        &self,
        feature: AuthFeature,
    ) -> Result<(Url, LoopbackCallback), AuthError> {
        let callback = LoopbackCallback::bind()?;
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        let manager = guard.as_mut().ok_or(AuthError::ConfigurationUnavailable)?;
        let request = manager.begin(
            feature,
            AuthPlatform::Desktop,
            callback.redirect_uri().clone(),
        )?;
        Ok((request.authorization_url, callback))
    }

    #[cfg(any(target_os = "android", target_os = "ios"))]
    pub(crate) fn begin_mobile(&self, feature: AuthFeature) -> Result<Url, AuthError> {
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        let manager = guard.as_mut().ok_or(AuthError::ConfigurationUnavailable)?;
        match manager.expire_pending(unix_time_now()) {
            Ok(()) | Err(AuthError::CallbackTimedOut) => {}
            Err(error) => return Err(error),
        }
        manager
            .begin(
                feature,
                AuthPlatform::Mobile,
                Url::parse("https://deli.dev/auth/devhud/callback")
                    .map_err(|_| AuthError::InvalidConfiguration)?,
            )
            .map(|request| request.authorization_url)
    }

    #[cfg(any(target_os = "android", target_os = "ios"))]
    pub(crate) fn poll_mobile_callback(
        &self,
        app: &AppHandle<crate::ActiveRuntime>,
    ) -> Result<SessionSnapshot, AuthError> {
        #[cfg(target_os = "ios")]
        let _ = app;
        #[cfg(target_os = "ios")]
        let callback = self
            .mobile_callback
            .lock()
            .map_err(|_| AuthError::InvalidCallback)?
            .take()
            .map(|callback| callback.to_string());
        #[cfg(target_os = "android")]
        let callback = app
            .devhud_auth_bridge()
            .take_callback()
            .map_err(|_| AuthError::InvalidCallback)?;

        {
            let mut guard = self
                .manager
                .lock()
                .map_err(|_| AuthError::SecureVaultUnavailable)?;
            if let Some(manager) = guard.as_mut() {
                manager.expire_pending(unix_time_now())?;
            }
        }

        let Some(callback) = callback else {
            return self.snapshot();
        };
        let callback = Url::parse(&callback).map_err(|_| AuthError::InvalidCallback)?;
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        guard
            .as_mut()
            .ok_or(AuthError::ConfigurationUnavailable)?
            .complete_callback(&callback, unix_time_now())
    }

    #[cfg(target_os = "ios")]
    pub(crate) fn accept_mobile_callback(&self, callback: Url) -> Result<(), AuthError> {
        if !crate::auth::is_mobile_callback_boundary(&callback) {
            return Err(AuthError::InvalidCallback);
        }
        let mut pending = self
            .mobile_callback
            .lock()
            .map_err(|_| AuthError::InvalidCallback)?;
        if pending.is_some() {
            return Err(AuthError::CallbackAlreadyConsumed);
        }
        *pending = Some(callback);
        Ok(())
    }

    pub(crate) fn finish_desktop(&self, callback: Url) -> Result<SessionSnapshot, AuthError> {
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        guard
            .as_mut()
            .ok_or(AuthError::ConfigurationUnavailable)?
            .complete_callback(&callback, unix_time_now())
    }

    pub(crate) fn logout(&self) -> Result<SessionSnapshot, AuthError> {
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        match guard.as_mut() {
            Some(manager) => manager.logout(),
            None => {
                self.fallback_vault
                    .lock()
                    .map_err(|_| AuthError::SecureVaultUnavailable)?
                    .clear()?;
                Ok(SessionSnapshot::SignedOut)
            }
        }
    }

    pub(crate) fn cancel_pending(&self) {
        if let Ok(mut guard) = self.manager.lock()
            && let Some(manager) = guard.as_mut()
        {
            manager.cancel_pending();
        }
    }

    pub(crate) fn reset(&self) -> Result<(), AuthError> {
        let mut guard = self
            .manager
            .lock()
            .map_err(|_| AuthError::SecureVaultUnavailable)?;
        match guard.as_mut() {
            Some(manager) => manager.reset().map(|_| ()),
            None => self
                .fallback_vault
                .lock()
                .map_err(|_| AuthError::SecureVaultUnavailable)?
                .clear(),
        }
    }
}

#[cfg(any(target_os = "android", target_os = "ios"))]
pub(crate) fn open_mobile_authorization(
    app: &AppHandle<crate::ActiveRuntime>,
    target: &Url,
) -> Result<(), AuthError> {
    app.devhud_auth_bridge()
        .open_authorization(target.as_str().to_owned())
        .map_err(|_| AuthError::BrowserUnavailable)
}

#[cfg(not(any(target_os = "android", target_os = "ios")))]
pub(crate) fn open_authorization_url(target: &Url) -> Result<(), AuthError> {
    if target.scheme() != "https"
        || target.path() != "/oidc/auth"
        || target.host_str().is_none()
        || target.fragment().is_some()
        || !target.username().is_empty()
        || target.password().is_some()
    {
        return Err(AuthError::InvalidConfiguration);
    }
    #[cfg(target_os = "macos")]
    let status = std::process::Command::new("/usr/bin/open")
        .arg(target.as_str())
        .status();
    #[cfg(target_os = "linux")]
    let status = std::process::Command::new("xdg-open")
        .arg(target.as_str())
        .status();
    #[cfg(target_os = "windows")]
    let status = std::process::Command::new("explorer.exe")
        .arg(target.as_str())
        .status();
    status
        .map_err(|_| AuthError::BrowserUnavailable)
        .and_then(|status| {
            status
                .success()
                .then_some(())
                .ok_or(AuthError::BrowserUnavailable)
        })
}

pub(crate) fn feature_supported(feature: AuthFeature) -> Result<(), AuthError> {
    #[cfg(any(target_os = "android", target_os = "ios"))]
    if feature == AuthFeature::RealQa {
        return Err(AuthError::InvalidConfiguration);
    }
    let _ = feature;
    Ok(())
}
