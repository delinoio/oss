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
    editor::{EditorOperation, flatten},
    encode_image, sanitize_image,
};

#[derive(Debug, Clone, PartialEq, Eq, Hash, Deserialize, Serialize)]
#[serde(transparent)]
pub(crate) struct ComposerSessionId(pub(crate) String);

#[derive(Debug, Clone, PartialEq, Eq, Hash, Deserialize, Serialize)]
#[serde(transparent)]
pub(crate) struct ComposerImageId(pub(crate) String);

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
    pub(crate) operations: Vec<EditorOperation>,
    pub(crate) output_media_type: ImageMediaType,
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ComposerImage {
    pub(crate) image_id: ComposerImageId,
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
pub(crate) struct ComposerCore {
    next_source_revision: AtomicU64,
    sessions: Mutex<HashMap<ComposerSessionId, ComposerSession>>,
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

        let mut sessions = self
            .sessions
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let session = sessions.entry(request.session_id).or_default();
        let previous_encoded_bytes = session
            .images
            .get(&request.image_id)
            .map(|source| source.accounted_encoded_bytes)
            .unwrap_or(0);
        session
            .budget
            .replace(previous_encoded_bytes, encoded_bytes)
            .map_err(CaptureFailure::from)?;
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
            let sessions = self
                .sessions
                .lock()
                .map_err(|_| CaptureFailure::CaptureFailed)?;
            let source = sessions
                .get(&request.session_id)
                .and_then(|session| session.images.get(&request.image_id))
                .ok_or(CaptureFailure::InvalidEditSequence)?;
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
        let mut sessions = self
            .sessions
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let session = sessions
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
        let mut sessions = self
            .sessions
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let Some(session) = sessions.get_mut(session_id) else {
            return Ok(());
        };
        if let Some(source) = session.images.remove(image_id) {
            session
                .budget
                .remove(source.accounted_encoded_bytes)
                .map_err(CaptureFailure::from)?;
        }
        if session.images.is_empty() {
            sessions.remove(session_id);
        }
        Ok(())
    }

    pub(crate) fn reset_session(
        &self,
        session_id: &ComposerSessionId,
    ) -> Result<(), CaptureFailure> {
        validate_identifier(&session_id.0)?;
        self.sessions
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?
            .remove(session_id);
        Ok(())
    }
}

fn validate_identifier(identifier: &str) -> Result<(), CaptureFailure> {
    if identifier.is_empty()
        || identifier.len() > 128
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
    use crate::realqa_capture::{DecodedImage, encode_image};

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

        let replacement = composer
            .accept_image(request("session-1", "image-1"))
            .expect("replacement must be accepted");
        assert_eq!(replacement.session_encoded_bytes, replacement.encoded_bytes);
        composer
            .remove_image(
                &ComposerSessionId("session-1".to_owned()),
                &ComposerImageId("image-1".to_owned()),
            )
            .expect("remove must be idempotent");
        composer
            .reset_session(&ComposerSessionId("session-1".to_owned()))
            .expect("reset must be idempotent");
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
    fn source_revisions_survive_session_reset_to_reject_stale_flatten_results() {
        let composer = ComposerCore::default();
        composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted");
        let first_revision = composer
            .sessions
            .lock()
            .expect("sessions lock must be available")
            .get(&ComposerSessionId("session-1".to_owned()))
            .and_then(|session| session.images.get(&ComposerImageId("image-1".to_owned())))
            .expect("source must exist")
            .revision;

        composer
            .reset_session(&ComposerSessionId("session-1".to_owned()))
            .expect("session must reset");
        composer
            .accept_image(request("session-1", "image-1"))
            .expect("image must be accepted again");
        let second_revision = composer
            .sessions
            .lock()
            .expect("sessions lock must be available")
            .get(&ComposerSessionId("session-1".to_owned()))
            .and_then(|session| session.images.get(&ComposerImageId("image-1".to_owned())))
            .expect("replacement source must exist")
            .revision;

        assert!(second_revision > first_revision);
    }

    #[test]
    fn flatten_request_deserializes_the_frontend_tauri_fixture_shape() {
        let request: ComposerFlattenRequest = serde_json::from_value(serde_json::json!({
            "sessionId": "session-1",
            "imageId": "image-1",
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
                operations: vec![operation.clone()],
                output_media_type: ImageMediaType::Png,
            })
            .expect("flatten must succeed");
        let second = composer
            .flatten_image(ComposerFlattenRequest {
                session_id: ComposerSessionId("session-1".to_owned()),
                image_id: ComposerImageId("image-1".to_owned()),
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
}
