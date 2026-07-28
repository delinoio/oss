use std::{
    collections::HashMap,
    sync::{
        Mutex,
        atomic::{AtomicU64, Ordering},
    },
};

use serde::{Deserialize, Serialize};

use super::{
    CaptureFailure, EncodedImage, ImageMediaType, ImageSessionBudget, decode_image,
    editor::{EditorOperation, deserialize_bounded_string, deserialize_operations, flatten},
    encode_image, sanitize_image,
};

const MAX_IDENTIFIER_BYTES: usize = 128;

#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize)]
#[serde(transparent)]
pub(crate) struct ComposerSessionId(pub(crate) String);

impl<'de> Deserialize<'de> for ComposerSessionId {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        deserialize_bounded_string(
            deserializer,
            MAX_IDENTIFIER_BYTES,
            "at most 128 session identifier bytes",
        )
        .map(Self)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Hash, Serialize)]
#[serde(transparent)]
pub(crate) struct ComposerImageId(pub(crate) String);

impl<'de> Deserialize<'de> for ComposerImageId {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: serde::Deserializer<'de>,
    {
        deserialize_bounded_string(
            deserializer,
            MAX_IDENTIFIER_BYTES,
            "at most 128 image identifier bytes",
        )
        .map(Self)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ComposerImageRequest {
    pub(crate) session_id: ComposerSessionId,
    pub(crate) image_id: ComposerImageId,
    pub(crate) image: EncodedImage,
    pub(crate) output_media_type: ImageMediaType,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ComposerFlattenRequest {
    pub(crate) session_id: ComposerSessionId,
    pub(crate) image_id: ComposerImageId,
    pub(crate) source_revision: u64,
    #[serde(deserialize_with = "deserialize_operations")]
    pub(crate) operations: Vec<EditorOperation>,
    pub(crate) output_media_type: ImageMediaType,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ComposerImage {
    pub(crate) image_id: ComposerImageId,
    pub(crate) source_revision: u64,
    pub(crate) content_type: &'static str,
    pub(crate) width: u32,
    pub(crate) height: u32,
    pub(crate) encoded_bytes: u64,
    pub(crate) session_encoded_bytes: u64,
    pub(crate) image: EncodedImage,
}

#[derive(Debug, Default)]
struct ComposerSession {
    budget: ImageSessionBudget,
    images: HashMap<ComposerImageId, ComposerSource>,
}

#[derive(Debug)]
struct ComposerSource {
    original: EncodedImage,
    original_encoded_bytes: u64,
    accounted_encoded_bytes: u64,
    revision: u64,
}

#[derive(Debug, Default)]
struct ComposerState {
    retained_source_budget: ImageSessionBudget,
    sessions: HashMap<ComposerSessionId, ComposerSession>,
}

#[derive(Debug, Default)]
pub(crate) struct ComposerCore {
    next_source_revision: AtomicU64,
    state: Mutex<ComposerState>,
}

impl ComposerCore {
    pub(crate) fn accept_image(
        &self,
        request: ComposerImageRequest,
    ) -> Result<ComposerImage, CaptureFailure> {
        validate_identifier(&request.session_id.0)?;
        validate_identifier(&request.image_id.0)?;

        let sanitized = sanitize_image(&request.image, request.output_media_type)
            .map_err(CaptureFailure::from)?;
        let decoded = decode_image(&sanitized).map_err(CaptureFailure::from)?;
        let encoded_bytes = u64::try_from(sanitized.bytes.len())
            .map_err(|_| CaptureFailure::ImageEncodedLimitExceeded)?;
        let revision = self
            .next_source_revision
            .fetch_update(Ordering::Relaxed, Ordering::Relaxed, |current| {
                current.checked_add(1)
            })
            .map_err(|_| CaptureFailure::CaptureFailed)?
            + 1;

        let mut state = self
            .state
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let (previous_accounted_bytes, previous_source_bytes, mut next_session_budget) = state
            .sessions
            .get(&request.session_id)
            .map(|session| {
                let source = session.images.get(&request.image_id);
                (
                    source
                        .map(|source| source.accounted_encoded_bytes)
                        .unwrap_or(0),
                    source
                        .map(|source| source.original_encoded_bytes)
                        .unwrap_or(0),
                    session.budget,
                )
            })
            .unwrap_or_default();
        next_session_budget
            .replace(previous_accounted_bytes, encoded_bytes)
            .map_err(CaptureFailure::from)?;
        let mut next_retained_source_budget = state.retained_source_budget;
        next_retained_source_budget
            .replace(previous_source_bytes, encoded_bytes)
            .map_err(CaptureFailure::from)?;

        state.retained_source_budget = next_retained_source_budget;
        let session = state.sessions.entry(request.session_id).or_default();
        session.budget = next_session_budget;
        session.images.insert(
            request.image_id.clone(),
            ComposerSource {
                original: sanitized.clone(),
                original_encoded_bytes: encoded_bytes,
                accounted_encoded_bytes: encoded_bytes,
                revision,
            },
        );
        Ok(ComposerImage {
            image_id: request.image_id,
            source_revision: revision,
            content_type: request.output_media_type.content_type(),
            width: decoded.width,
            height: decoded.height,
            encoded_bytes,
            session_encoded_bytes: session.budget.encoded_bytes(),
            image: sanitized,
        })
    }

    pub(crate) fn flatten_image(
        &self,
        request: ComposerFlattenRequest,
    ) -> Result<ComposerImage, CaptureFailure> {
        validate_identifier(&request.session_id.0)?;
        validate_identifier(&request.image_id.0)?;
        let (retained_source, original_encoded_bytes, source_revision) = {
            let state = self
                .state
                .lock()
                .map_err(|_| CaptureFailure::CaptureFailed)?;
            let source = state
                .sessions
                .get(&request.session_id)
                .and_then(|session| session.images.get(&request.image_id))
                .ok_or(CaptureFailure::InvalidEditSequence)?;
            if source.revision != request.source_revision {
                return Err(CaptureFailure::InvalidEditSequence);
            }
            (
                source.original.clone(),
                source.original_encoded_bytes,
                source.revision,
            )
        };

        let original = decode_image(&retained_source).map_err(CaptureFailure::from)?;
        let flattened = flatten(original, &request.operations)?;
        let encoded =
            encode_image(&flattened, request.output_media_type).map_err(CaptureFailure::from)?;
        let encoded_bytes = u64::try_from(encoded.bytes.len())
            .map_err(|_| CaptureFailure::ImageEncodedLimitExceeded)?;
        // The retained source and current approved result represent one logical
        // screenshot. Accounting the larger form prevents a crop from laundering
        // retained source bytes out of the session limit while also bounding
        // output growth.
        let accounted_encoded_bytes = original_encoded_bytes.max(encoded_bytes);
        let mut state = self
            .state
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let session = state
            .sessions
            .get_mut(&request.session_id)
            .ok_or(CaptureFailure::InvalidEditSequence)?;
        let previous_encoded_bytes = session
            .images
            .get(&request.image_id)
            .filter(|source| source.revision == source_revision)
            .map(|source| source.accounted_encoded_bytes)
            .ok_or(CaptureFailure::InvalidEditSequence)?;
        session
            .budget
            .replace(previous_encoded_bytes, accounted_encoded_bytes)
            .map_err(CaptureFailure::from)?;
        session
            .images
            .get_mut(&request.image_id)
            .ok_or(CaptureFailure::InvalidEditSequence)?
            .accounted_encoded_bytes = accounted_encoded_bytes;
        Ok(ComposerImage {
            image_id: request.image_id,
            source_revision,
            content_type: request.output_media_type.content_type(),
            width: flattened.width,
            height: flattened.height,
            encoded_bytes,
            session_encoded_bytes: session.budget.encoded_bytes(),
            image: encoded,
        })
    }

    pub(crate) fn remove_image(
        &self,
        session_id: &ComposerSessionId,
        image_id: &ComposerImageId,
    ) -> Result<(), CaptureFailure> {
        validate_identifier(&session_id.0)?;
        validate_identifier(&image_id.0)?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let Some((accounted_encoded_bytes, original_encoded_bytes)) = state
            .sessions
            .get(session_id)
            .and_then(|session| session.images.get(image_id))
            .map(|source| {
                (
                    source.accounted_encoded_bytes,
                    source.original_encoded_bytes,
                )
            })
        else {
            return Ok(());
        };
        let mut next_retained_source_budget = state.retained_source_budget;
        next_retained_source_budget
            .remove(original_encoded_bytes)
            .map_err(CaptureFailure::from)?;
        let session = state
            .sessions
            .get_mut(session_id)
            .ok_or(CaptureFailure::CaptureFailed)?;
        session
            .budget
            .remove(accounted_encoded_bytes)
            .map_err(CaptureFailure::from)?;
        session.images.remove(image_id);
        let session_is_empty = session.images.is_empty();
        state.retained_source_budget = next_retained_source_budget;
        if session_is_empty {
            state.sessions.remove(session_id);
        }
        Ok(())
    }

    pub(crate) fn reset_session(
        &self,
        session_id: &ComposerSessionId,
    ) -> Result<(), CaptureFailure> {
        validate_identifier(&session_id.0)?;
        let mut state = self
            .state
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let Some(session) = state.sessions.get(session_id) else {
            return Ok(());
        };
        let retained_source_bytes = session.images.values().try_fold(0_u64, |total, source| {
            total
                .checked_add(source.original_encoded_bytes)
                .ok_or(CaptureFailure::SessionEncodedLimitExceeded)
        })?;
        let mut next_retained_source_budget = state.retained_source_budget;
        next_retained_source_budget
            .remove(retained_source_bytes)
            .map_err(CaptureFailure::from)?;
        state.sessions.remove(session_id);
        state.retained_source_budget = next_retained_source_budget;
        Ok(())
    }
}

fn validate_identifier(identifier: &str) -> Result<(), CaptureFailure> {
    if identifier.is_empty()
        || identifier.len() > MAX_IDENTIFIER_BYTES
        || !identifier
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'-' | b'_'))
    {
        return Err(CaptureFailure::InvalidSelection);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::realqa_capture::{
        DecodedImage, encode_image, image_boundary::MAX_ENCODED_SESSION_BYTES,
    };

    fn request(session: &str, image_id: &str) -> ComposerImageRequest {
        ComposerImageRequest {
            session_id: ComposerSessionId(session.to_owned()),
            image_id: ComposerImageId(image_id.to_owned()),
            image: encode_image(
                &DecodedImage {
                    width: 2,
                    height: 1,
                    rgba: vec![1, 2, 3, 255, 4, 5, 6, 255],
                },
                ImageMediaType::Png,
            )
            .expect("fixture must encode"),
            output_media_type: ImageMediaType::Webp,
        }
    }

    #[test]
    fn accepts_replaces_removes_and_resets_images_without_count_policy() {
        let composer = ComposerCore::default();
        let first = composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted");
        assert_eq!(first.width, 2);
        assert_eq!(first.height, 1);
        assert_eq!(first.content_type, "image/webp");
        assert_eq!(first.encoded_bytes, first.session_encoded_bytes);
        assert_eq!(
            composer
                .state
                .lock()
                .expect("composer state must be available")
                .retained_source_budget
                .encoded_bytes(),
            first.encoded_bytes
        );

        let replacement = composer
            .accept_image(request("session-1", "image-1"))
            .expect("replacement must be accepted");
        assert!(replacement.source_revision > first.source_revision);
        assert_eq!(replacement.session_encoded_bytes, replacement.encoded_bytes);
        let second_session = composer
            .accept_image(request("session-2", "image-1"))
            .expect("another session must share the retained-source budget");
        assert_eq!(
            composer
                .state
                .lock()
                .expect("composer state must be available")
                .retained_source_budget
                .encoded_bytes(),
            replacement.encoded_bytes + second_session.encoded_bytes
        );
        composer
            .remove_image(
                &ComposerSessionId("session-1".to_owned()),
                &ComposerImageId("image-1".to_owned()),
            )
            .expect("remove must be idempotent");
        assert_eq!(
            composer
                .state
                .lock()
                .expect("composer state must be available")
                .retained_source_budget
                .encoded_bytes(),
            second_session.encoded_bytes
        );
        composer
            .reset_session(&ComposerSessionId("session-2".to_owned()))
            .expect("reset must be idempotent");
        assert_eq!(
            composer
                .state
                .lock()
                .expect("composer state must be available")
                .retained_source_budget
                .encoded_bytes(),
            0
        );
    }

    #[test]
    fn malformed_identifier_is_rejected_before_session_state_changes() {
        let composer = ComposerCore::default();
        assert_eq!(
            composer.accept_image(request("../session", "image-1")),
            Err(CaptureFailure::InvalidSelection)
        );
    }

    #[test]
    fn rejects_a_source_when_the_process_wide_retained_budget_is_full() {
        let composer = ComposerCore::default();
        composer
            .state
            .lock()
            .expect("composer state must be available")
            .retained_source_budget
            .replace(0, MAX_ENCODED_SESSION_BYTES)
            .expect("the exact global limit must be valid");

        assert_eq!(
            composer.accept_image(request("session-1", "image-1")),
            Err(CaptureFailure::SessionEncodedLimitExceeded)
        );
        assert!(
            composer
                .state
                .lock()
                .expect("composer state must be available")
                .sessions
                .is_empty()
        );
    }

    #[test]
    fn source_revisions_survive_session_reset_to_reject_stale_flatten_results() {
        let composer = ComposerCore::default();
        let first = composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted");

        composer
            .reset_session(&ComposerSessionId("session-1".to_owned()))
            .expect("session must reset");
        let second = composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted again");

        assert!(second.source_revision > first.source_revision);
    }

    #[test]
    fn flatten_request_deserializes_the_frontend_tauri_fixture_shape() {
        let request: ComposerFlattenRequest = serde_json::from_value(serde_json::json!({
            "sessionId": "session-1",
            "imageId": "image-1",
            "sourceRevision": 7,
            "operations": [{
                "kind": "arrow",
                "start": { "x": 0, "y": 0 },
                "end": { "x": 1, "y": 0 },
                "color": "#ff0000",
                "lineWidth": 3
            }],
            "outputMediaType": "webp"
        }))
        .expect("frontend fixture must deserialize");
        assert_eq!(
            request,
            ComposerFlattenRequest {
                session_id: ComposerSessionId("session-1".to_owned()),
                image_id: ComposerImageId("image-1".to_owned()),
                source_revision: 7,
                operations: vec![EditorOperation::Arrow {
                    start: super::super::editor::EditorPoint { x: 0, y: 0 },
                    end: super::super::editor::EditorPoint { x: 1, y: 0 },
                    color: "#ff0000".to_owned(),
                    line_width: 3,
                }],
                output_media_type: ImageMediaType::Webp,
            }
        );
    }

    #[test]
    fn flatten_request_rejects_oversized_values_during_deserialization() {
        let request = |operations: serde_json::Value| {
            serde_json::from_value::<ComposerFlattenRequest>(serde_json::json!({
                "sessionId": "session-1",
                "imageId": "image-1",
                "sourceRevision": 7,
                "operations": operations,
                "outputMediaType": "webp"
            }))
        };
        let crop = serde_json::json!({
            "kind": "crop",
            "rect": { "x": 0, "y": 0, "width": 1, "height": 1 }
        });
        assert!(
            request(serde_json::Value::Array(vec![
                crop;
                super::super::editor::MAX_OPERATIONS
                    + 1
            ]))
            .is_err()
        );
        let freehand_points = vec![serde_json::json!({"x": 0, "y": 0}); 20_001];
        assert!(
            request(serde_json::json!([{
                "kind": "freehand",
                "points": freehand_points,
                "color": "#ffffff",
                "lineWidth": 1
            }]))
            .is_err()
        );
        let aggregate_points = vec![serde_json::json!({"x": 0, "y": 0}); 16_667];
        let aggregate_freehand = serde_json::json!({
            "kind": "freehand",
            "points": aggregate_points,
            "color": "#ffffff",
            "lineWidth": 1
        });
        assert!(request(serde_json::Value::Array(vec![aggregate_freehand; 6])).is_err());
        assert!(
            request(serde_json::json!([{
                "kind": "text",
                "origin": {"x": 0, "y": 0},
                "text": "A".repeat(4_097),
                "color": "#ffffff",
                "fontSize": 8
            }]))
            .is_err()
        );
    }

    #[test]
    fn flattens_from_the_retained_original_without_mutating_source_pixels() {
        let composer = ComposerCore::default();
        let accepted = composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted");
        let operation = EditorOperation::Crop {
            rect: super::super::editor::EditorRect {
                x: 1,
                y: 0,
                width: 1,
                height: 1,
            },
        };
        let first = composer
            .flatten_image(ComposerFlattenRequest {
                session_id: ComposerSessionId("session-1".to_owned()),
                image_id: ComposerImageId("image-1".to_owned()),
                source_revision: accepted.source_revision,
                operations: vec![operation.clone()],
                output_media_type: ImageMediaType::Png,
            })
            .expect("flatten must succeed");
        let second = composer
            .flatten_image(ComposerFlattenRequest {
                session_id: ComposerSessionId("session-1".to_owned()),
                image_id: ComposerImageId("image-1".to_owned()),
                source_revision: accepted.source_revision,
                operations: vec![operation],
                output_media_type: ImageMediaType::Png,
            })
            .expect("repeat flatten must succeed");
        assert_eq!(first.image, second.image);
        assert_eq!((first.width, first.height), (1, 1));
        assert_eq!(
            first.session_encoded_bytes,
            accepted.encoded_bytes.max(first.encoded_bytes)
        );
        assert_eq!(
            decode_image(&first.image).expect("output must decode").rgba,
            vec![4, 5, 6, 255]
        );
    }

    #[test]
    fn rejects_a_stale_source_revision_before_flattening() {
        let composer = ComposerCore::default();
        let first = composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted");
        composer
            .accept_image(request("session-1", "image-1"))
            .expect("replacement must be accepted");

        assert_eq!(
            composer.flatten_image(ComposerFlattenRequest {
                session_id: ComposerSessionId("session-1".to_owned()),
                image_id: ComposerImageId("image-1".to_owned()),
                source_revision: first.source_revision,
                operations: Vec::new(),
                output_media_type: ImageMediaType::Png,
            }),
            Err(CaptureFailure::InvalidEditSequence)
        );
    }
}
