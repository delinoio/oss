use std::{
    collections::{HashSet, VecDeque},
    time::{SystemTime, UNIX_EPOCH},
};

use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use rand::RngCore;
use sha2::Sha256;

use crate::{
    PROTOCOL_VERSION, REQUEST_DEADLINE_MILLIS, SCHEMA_VERSION,
    protocol::{AuthPurpose, AuthResponse, AuthResult, Challenge, IpcRequest},
};

type HmacSha256 = Hmac<Sha256>;

pub fn now_unix_millis() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_millis()).unwrap_or(i64::MAX)
        })
}

pub fn random_nonce() -> String {
    let mut bytes = [0_u8; 32];
    rand::rng().fill_bytes(&mut bytes);
    URL_SAFE_NO_PAD.encode(bytes)
}

pub fn new_challenge(now: i64) -> Challenge {
    Challenge {
        version: PROTOCOL_VERSION,
        schema_version: SCHEMA_VERSION,
        challenge_id: uuid::Uuid::now_v7().to_string(),
        challenge: random_nonce(),
        deadline_unix_ms: now + REQUEST_DEADLINE_MILLIS,
    }
}

fn append(mac: &mut HmacSha256, value: &[u8]) {
    mac.update(&(value.len() as u64).to_le_bytes());
    mac.update(value);
}

pub fn handshake_proof(
    secret: &[u8],
    challenge: &Challenge,
    extension_id: &str,
    origin: &str,
    client_nonce: &str,
    pairing_nonce: Option<&str>,
    purpose: AuthPurpose,
) -> String {
    URL_SAFE_NO_PAD.encode(
        handshake_mac(
            secret,
            challenge,
            extension_id,
            origin,
            client_nonce,
            pairing_nonce,
            purpose,
        )
        .finalize()
        .into_bytes(),
    )
}

fn handshake_mac(
    secret: &[u8],
    challenge: &Challenge,
    extension_id: &str,
    origin: &str,
    client_nonce: &str,
    pairing_nonce: Option<&str>,
    purpose: AuthPurpose,
) -> HmacSha256 {
    let mut mac = HmacSha256::new_from_slice(secret).expect("HMAC accepts any key length");
    append(&mut mac, b"devhud-native-messaging-auth-v1");
    append(&mut mac, challenge.challenge_id.as_bytes());
    append(&mut mac, challenge.challenge.as_bytes());
    append(&mut mac, extension_id.as_bytes());
    append(&mut mac, origin.as_bytes());
    append(&mut mac, client_nonce.as_bytes());
    append(&mut mac, pairing_nonce.unwrap_or("").as_bytes());
    append(
        &mut mac,
        match purpose {
            AuthPurpose::BrowserSession => b"browser-session",
            AuthPurpose::PairingRevocation => b"pairing-revocation",
        },
    );
    mac
}

pub fn verify_handshake(secret: &[u8], challenge: &Challenge, response: &AuthResponse) -> bool {
    let Ok(proof) = URL_SAFE_NO_PAD.decode(&response.proof) else {
        return false;
    };
    handshake_mac(
        secret,
        challenge,
        &response.extension_id,
        &response.origin,
        &response.client_nonce,
        response.pairing_nonce.as_deref(),
        response.purpose,
    )
    .verify_slice(&proof)
    .is_ok()
}

fn auth_result_mac(secret: &[u8], challenge: &Challenge, session_id: &str) -> HmacSha256 {
    let mut mac = HmacSha256::new_from_slice(secret).expect("HMAC accepts any key length");
    append(&mut mac, b"devhud-native-messaging-app-auth-v1");
    append(&mut mac, challenge.challenge_id.as_bytes());
    append(&mut mac, challenge.challenge.as_bytes());
    append(&mut mac, session_id.as_bytes());
    mac
}

pub fn auth_result_proof(secret: &[u8], challenge: &Challenge, session_id: &str) -> String {
    URL_SAFE_NO_PAD.encode(
        auth_result_mac(secret, challenge, session_id)
            .finalize()
            .into_bytes(),
    )
}

pub fn verify_auth_result(secret: &[u8], challenge: &Challenge, result: &AuthResult) -> bool {
    let (Some(session_id), Some(proof)) = (&result.session_id, &result.proof) else {
        return false;
    };
    let Ok(proof) = URL_SAFE_NO_PAD.decode(proof) else {
        return false;
    };
    auth_result_mac(secret, challenge, session_id)
        .verify_slice(&proof)
        .is_ok()
}

fn request_mac(secret: &[u8], session_id: &str, request: &IpcRequest) -> HmacSha256 {
    let mut mac = HmacSha256::new_from_slice(secret).expect("HMAC accepts any key length");
    append(&mut mac, b"devhud-native-messaging-request-v1");
    append(&mut mac, session_id.as_bytes());
    append(&mut mac, request.request_id.as_bytes());
    append(&mut mac, format!("{:?}", request.message_type).as_bytes());
    append(&mut mac, &request.issued_at_unix_ms.to_le_bytes());
    append(&mut mac, &request.deadline_unix_ms.to_le_bytes());
    append(&mut mac, request.nonce.as_bytes());
    append(
        &mut mac,
        &serde_json::to_vec(&request.payload).unwrap_or_default(),
    );
    mac
}

pub fn sign_request(secret: &[u8], session_id: &str, request: &mut IpcRequest) {
    let proof = URL_SAFE_NO_PAD.encode(
        request_mac(secret, session_id, request)
            .finalize()
            .into_bytes(),
    );
    request.proof = proof;
}

pub fn verify_request(secret: &[u8], session_id: &str, request: &IpcRequest) -> bool {
    let Ok(proof) = URL_SAFE_NO_PAD.decode(&request.proof) else {
        return false;
    };
    request_mac(secret, session_id, request)
        .verify_slice(&proof)
        .is_ok()
}

pub struct ReplayGuard {
    limit: usize,
    order: VecDeque<String>,
    values: HashSet<String>,
}

impl ReplayGuard {
    pub fn new(limit: usize) -> Self {
        Self {
            limit,
            order: VecDeque::new(),
            values: HashSet::new(),
        }
    }

    pub fn accept(&mut self, value: String) -> bool {
        if self.values.contains(&value) {
            return false;
        }
        self.values.insert(value.clone());
        self.order.push_back(value);
        while self.order.len() > self.limit {
            if let Some(expired) = self.order.pop_front() {
                self.values.remove(&expired);
            }
        }
        true
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn challenge_proof_rejects_changes() {
        let secret = [7_u8; 32];
        let challenge = new_challenge(10);
        let proof = handshake_proof(
            &secret,
            &challenge,
            "abcdefghijklmnopabcdefghijklmnop",
            "chrome-extension://abcdefghijklmnopabcdefghijklmnop/",
            "client",
            Some("pair"),
            AuthPurpose::BrowserSession,
        );
        let mut response = AuthResponse {
            version: 1,
            schema_version: 1,
            challenge_id: challenge.challenge_id.clone(),
            extension_id: "abcdefghijklmnopabcdefghijklmnop".into(),
            origin: "chrome-extension://abcdefghijklmnopabcdefghijklmnop/".into(),
            client_nonce: "client".into(),
            pairing_nonce: Some("pair".into()),
            purpose: AuthPurpose::BrowserSession,
            proof,
        };
        assert!(verify_handshake(&secret, &challenge, &response));
        response.purpose = AuthPurpose::PairingRevocation;
        assert!(!verify_handshake(&secret, &challenge, &response));
        response.purpose = AuthPurpose::BrowserSession;
        response.client_nonce.push('x');
        assert!(!verify_handshake(&secret, &challenge, &response));
    }

    #[test]
    fn replay_guard_rejects_a_nonce_once_seen() {
        let mut guard = ReplayGuard::new(2);
        assert!(guard.accept("one".into()));
        assert!(!guard.accept("one".into()));
        assert!(guard.accept("two".into()));
        assert!(guard.accept("three".into()));
        assert!(guard.accept("one".into()));
    }

    #[test]
    fn app_proof_binds_the_challenge_and_session() {
        let secret = [7_u8; 32];
        let challenge = new_challenge(10);
        let session_id = "session";
        let mut result = AuthResult {
            version: 1,
            schema_version: 1,
            accepted: true,
            session_id: Some(session_id.into()),
            proof: Some(auth_result_proof(&secret, &challenge, session_id)),
            error: None,
        };
        assert!(verify_auth_result(&secret, &challenge, &result));
        let mut other_challenge = challenge.clone();
        other_challenge.challenge.push('x');
        assert!(!verify_auth_result(&secret, &other_challenge, &result));
        result.session_id = Some("other-session".into());
        assert!(!verify_auth_result(&secret, &challenge, &result));
    }
}
