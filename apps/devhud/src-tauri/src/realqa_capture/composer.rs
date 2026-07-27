use std::{collections::HashMap, sync::Mutex};

use serde::{Deserialize, Serialize};

use super::{
    CaptureFailure, EncodedImage, ImageMediaType, ImageSessionBudget, decode_image, sanitize_image,
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
    image_sizes: HashMap<ComposerImageId, u64>,
}

#[derive(Debug, Default)]
pub(crate) struct ComposerCore {
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

        let mut sessions = self
            .sessions
            .lock()
            .map_err(|_| CaptureFailure::CaptureFailed)?;
        let session = sessions.entry(request.session_id).or_default();
        let previous_encoded_bytes = session
            .image_sizes
            .get(&request.image_id)
            .copied()
            .unwrap_or(0);
        session
            .budget
            .replace(previous_encoded_bytes, encoded_bytes)
            .map_err(CaptureFailure::from)?;
        session
            .image_sizes
            .insert(request.image_id.clone(), encoded_bytes);
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
        if let Some(encoded_bytes) = session.image_sizes.remove(image_id) {
            session
                .budget
                .remove(encoded_bytes)
                .map_err(CaptureFailure::from)?;
        }
        if session.image_sizes.is_empty() {
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
}
