use std::io::{self, Cursor, Seek, SeekFrom, Write};

use image::{DynamicImage, ImageFormat, ImageReader};
use serde::{Deserialize, Serialize};

pub(crate) const MAX_DECODED_PIXELS: u64 = 100_000_000;
pub(crate) const MAX_ENCODED_IMAGE_BYTES: u64 = 25 * 1024 * 1024;
pub(crate) const MAX_ENCODED_SESSION_BYTES: u64 = 250 * 1024 * 1024;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum ImageMediaType {
    Png,
    Webp,
}

impl ImageMediaType {
    const fn image_format(self) -> ImageFormat {
        match self {
            Self::Png => ImageFormat::Png,
            Self::Webp => ImageFormat::WebP,
        }
    }

    pub(crate) const fn content_type(self) -> &'static str {
        match self {
            Self::Png => "image/png",
            Self::Webp => "image/webp",
        }
    }

    fn matches_signature(self, bytes: &[u8]) -> bool {
        match self {
            Self::Png => bytes.starts_with(b"\x89PNG\r\n\x1a\n"),
            Self::Webp => bytes.len() >= 12 && &bytes[..4] == b"RIFF" && &bytes[8..12] == b"WEBP",
        }
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct EncodedImage {
    pub(crate) media_type: ImageMediaType,
    pub(crate) bytes: Vec<u8>,
}

#[derive(Debug, Clone, PartialEq, Eq)]
pub(crate) struct DecodedImage {
    pub(crate) width: u32,
    pub(crate) height: u32,
    pub(crate) rgba: Vec<u8>,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum ImageBoundaryFailure {
    MalformedImage,
    UnsupportedImage,
    DecompressionBomb,
    ImageEncodedLimitExceeded,
    SessionEncodedLimitExceeded,
    EncodingFailed,
}

pub(crate) fn decoded_byte_len(width: u32, height: u32) -> Result<usize, ImageBoundaryFailure> {
    let pixels = u64::from(width)
        .checked_mul(u64::from(height))
        .ok_or(ImageBoundaryFailure::DecompressionBomb)?;
    if pixels == 0 || pixels > MAX_DECODED_PIXELS {
        return Err(ImageBoundaryFailure::DecompressionBomb);
    }
    let bytes = pixels
        .checked_mul(4)
        .and_then(|value| usize::try_from(value).ok())
        .ok_or(ImageBoundaryFailure::DecompressionBomb)?;
    Ok(bytes)
}

pub(crate) fn decode_image(encoded: &EncodedImage) -> Result<DecodedImage, ImageBoundaryFailure> {
    let encoded_len = u64::try_from(encoded.bytes.len())
        .map_err(|_| ImageBoundaryFailure::ImageEncodedLimitExceeded)?;
    if encoded_len == 0 || encoded_len > MAX_ENCODED_IMAGE_BYTES {
        return Err(ImageBoundaryFailure::ImageEncodedLimitExceeded);
    }
    if !encoded.media_type.matches_signature(&encoded.bytes) {
        return Err(ImageBoundaryFailure::UnsupportedImage);
    }

    let format = encoded.media_type.image_format();
    let dimensions = ImageReader::with_format(Cursor::new(&encoded.bytes), format)
        .into_dimensions()
        .map_err(|_| ImageBoundaryFailure::MalformedImage)?;
    let expected_len = decoded_byte_len(dimensions.0, dimensions.1)?;

    let decoded = ImageReader::with_format(Cursor::new(&encoded.bytes), format)
        .decode()
        .map_err(|_| ImageBoundaryFailure::MalformedImage)?;
    let rgba = decoded.to_rgba8().into_raw();
    if rgba.len() != expected_len {
        return Err(ImageBoundaryFailure::MalformedImage);
    }
    Ok(DecodedImage {
        width: dimensions.0,
        height: dimensions.1,
        rgba,
    })
}

pub(crate) fn encode_image(
    decoded: &DecodedImage,
    media_type: ImageMediaType,
) -> Result<EncodedImage, ImageBoundaryFailure> {
    encode_image_with_limit(decoded, media_type, MAX_ENCODED_IMAGE_BYTES)
}

fn encode_image_with_limit(
    decoded: &DecodedImage,
    media_type: ImageMediaType,
    encoded_limit: u64,
) -> Result<EncodedImage, ImageBoundaryFailure> {
    let expected_len = decoded_byte_len(decoded.width, decoded.height)?;
    if decoded.rgba.len() != expected_len {
        return Err(ImageBoundaryFailure::MalformedImage);
    }
    let buffer = image::RgbaImage::from_raw(decoded.width, decoded.height, decoded.rgba.clone())
        .ok_or(ImageBoundaryFailure::MalformedImage)?;
    let mut output = BoundedWriter::new(encoded_limit);
    let encode_result =
        DynamicImage::ImageRgba8(buffer).write_to(&mut output, media_type.image_format());
    if output.limit_exceeded {
        return Err(ImageBoundaryFailure::ImageEncodedLimitExceeded);
    }
    encode_result.map_err(|_| ImageBoundaryFailure::EncodingFailed)?;
    let bytes = output.into_inner();
    Ok(EncodedImage { media_type, bytes })
}

struct BoundedWriter {
    inner: Cursor<Vec<u8>>,
    limit: u64,
    limit_exceeded: bool,
}

impl BoundedWriter {
    fn new(limit: u64) -> Self {
        Self {
            inner: Cursor::new(Vec::new()),
            limit,
            limit_exceeded: false,
        }
    }

    fn into_inner(self) -> Vec<u8> {
        self.inner.into_inner()
    }

    fn limit_error(&mut self) -> io::Error {
        self.limit_exceeded = true;
        io::Error::other("encoded image limit exceeded")
    }
}

impl Write for BoundedWriter {
    fn write(&mut self, buffer: &[u8]) -> io::Result<usize> {
        let buffer_len = u64::try_from(buffer.len()).map_err(|_| self.limit_error())?;
        let end = self
            .inner
            .position()
            .checked_add(buffer_len)
            .ok_or_else(|| self.limit_error())?;
        if end > self.limit {
            return Err(self.limit_error());
        }
        self.inner.write(buffer)
    }

    fn flush(&mut self) -> io::Result<()> {
        self.inner.flush()
    }
}

impl Seek for BoundedWriter {
    fn seek(&mut self, position: SeekFrom) -> io::Result<u64> {
        let next = self.inner.seek(position)?;
        if next > self.limit {
            return Err(self.limit_error());
        }
        Ok(next)
    }
}

pub(crate) fn sanitize_image(
    encoded: &EncodedImage,
    output_media_type: ImageMediaType,
) -> Result<EncodedImage, ImageBoundaryFailure> {
    encode_image(&decode_image(encoded)?, output_media_type)
}

#[derive(Debug, Clone, Copy, Default)]
pub(crate) struct ImageSessionBudget {
    encoded_bytes: u64,
}

impl ImageSessionBudget {
    pub(crate) const fn encoded_bytes(&self) -> u64 {
        self.encoded_bytes
    }

    pub(crate) fn replace(
        &mut self,
        previous_encoded_bytes: u64,
        new_encoded_bytes: u64,
    ) -> Result<(), ImageBoundaryFailure> {
        let without_previous = self
            .encoded_bytes
            .checked_sub(previous_encoded_bytes)
            .ok_or(ImageBoundaryFailure::SessionEncodedLimitExceeded)?;
        let next = without_previous
            .checked_add(new_encoded_bytes)
            .ok_or(ImageBoundaryFailure::SessionEncodedLimitExceeded)?;
        if next > MAX_ENCODED_SESSION_BYTES {
            return Err(ImageBoundaryFailure::SessionEncodedLimitExceeded);
        }
        self.encoded_bytes = next;
        Ok(())
    }

    pub(crate) fn remove(&mut self, encoded_bytes: u64) -> Result<(), ImageBoundaryFailure> {
        self.encoded_bytes = self
            .encoded_bytes
            .checked_sub(encoded_bytes)
            .ok_or(ImageBoundaryFailure::SessionEncodedLimitExceeded)?;
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn one_pixel_png() -> EncodedImage {
        encode_image(
            &DecodedImage {
                width: 1,
                height: 1,
                rgba: vec![1, 2, 3, 255],
            },
            ImageMediaType::Png,
        )
        .expect("fixture image must encode")
    }

    fn crc32(bytes: &[u8]) -> u32 {
        let mut crc = u32::MAX;
        for byte in bytes {
            crc ^= u32::from(*byte);
            for _ in 0..8 {
                crc = (crc >> 1) ^ (0xedb8_8320 & (0_u32.wrapping_sub(crc & 1)));
            }
        }
        !crc
    }

    fn oversized_png_header(width: u32, height: u32) -> EncodedImage {
        let mut bytes = b"\x89PNG\r\n\x1a\n".to_vec();
        append_png_chunk(
            &mut bytes,
            b"IHDR",
            &[
                width.to_be_bytes().as_slice(),
                height.to_be_bytes().as_slice(),
                &[8, 6, 0, 0, 0],
            ]
            .concat(),
        );
        append_png_chunk(&mut bytes, b"IDAT", &[]);
        append_png_chunk(&mut bytes, b"IEND", &[]);
        EncodedImage {
            media_type: ImageMediaType::Png,
            bytes,
        }
    }

    fn append_png_chunk(bytes: &mut Vec<u8>, kind: &[u8; 4], data: &[u8]) {
        bytes.extend_from_slice(
            &u32::try_from(data.len())
                .expect("fixture chunk length must fit")
                .to_be_bytes(),
        );
        bytes.extend_from_slice(kind);
        bytes.extend_from_slice(data);
        let mut checksum_input = kind.to_vec();
        checksum_input.extend_from_slice(data);
        bytes.extend_from_slice(&crc32(&checksum_input).to_be_bytes());
    }

    #[test]
    fn round_trip_reencodes_rgba_without_metadata() {
        let original = one_pixel_png();
        let sanitized =
            sanitize_image(&original, ImageMediaType::Webp).expect("image must sanitize");
        assert_eq!(sanitized.media_type, ImageMediaType::Webp);
        assert_eq!(
            decode_image(&sanitized).expect("sanitized image must decode"),
            DecodedImage {
                width: 1,
                height: 1,
                rgba: vec![1, 2, 3, 255],
            }
        );
    }

    #[test]
    fn sanitizing_png_removes_ancillary_metadata_chunks() {
        let mut original = one_pixel_png();
        let insert_at = original.bytes.len() - 12;
        let mut metadata = Vec::new();
        append_png_chunk(&mut metadata, b"tEXt", b"source\0raw-original");
        original.bytes.splice(insert_at..insert_at, metadata);
        assert!(original.bytes.windows(4).any(|window| window == b"tEXt"));

        let sanitized =
            sanitize_image(&original, ImageMediaType::Png).expect("image must sanitize");
        assert!(!sanitized.bytes.windows(4).any(|window| window == b"tEXt"));
        assert_eq!(
            decode_image(&sanitized).expect("sanitized image must decode"),
            DecodedImage {
                width: 1,
                height: 1,
                rgba: vec![1, 2, 3, 255],
            }
        );
    }

    #[test]
    fn malformed_and_unsupported_images_fail_closed() {
        assert_eq!(
            decode_image(&EncodedImage {
                media_type: ImageMediaType::Png,
                bytes: b"\x89PNG\r\n\x1a\nbroken".to_vec(),
            }),
            Err(ImageBoundaryFailure::MalformedImage)
        );
        assert_eq!(
            decode_image(&EncodedImage {
                media_type: ImageMediaType::Png,
                bytes: b"GIF89a".to_vec(),
            }),
            Err(ImageBoundaryFailure::UnsupportedImage)
        );
    }

    #[test]
    fn encoded_and_decompressed_limits_fail_before_allocation() {
        let mut too_large = vec![0; usize::try_from(MAX_ENCODED_IMAGE_BYTES + 1).unwrap()];
        too_large[..8].copy_from_slice(b"\x89PNG\r\n\x1a\n");
        assert_eq!(
            decode_image(&EncodedImage {
                media_type: ImageMediaType::Png,
                bytes: too_large,
            }),
            Err(ImageBoundaryFailure::ImageEncodedLimitExceeded)
        );
        assert_eq!(
            decode_image(&oversized_png_header(10_001, 10_000)),
            Err(ImageBoundaryFailure::DecompressionBomb)
        );
        assert!(decoded_byte_len(10_000, 10_000).is_ok());
    }

    #[test]
    fn encoding_rejects_output_over_the_per_image_limit() {
        let width = 2_600;
        let height = 2_600;
        let mut state = 0x1234_5678_u32;
        let mut rgba = vec![0; decoded_byte_len(width, height).expect("fixture must fit")];
        for byte in &mut rgba {
            state ^= state << 13;
            state ^= state >> 17;
            state ^= state << 5;
            *byte = state as u8;
        }
        assert_eq!(
            encode_image(
                &DecodedImage {
                    width,
                    height,
                    rgba,
                },
                ImageMediaType::Png,
            ),
            Err(ImageBoundaryFailure::ImageEncodedLimitExceeded)
        );
    }

    #[test]
    fn encoding_stops_writing_as_soon_as_the_per_image_limit_is_crossed() {
        let decoded = DecodedImage {
            width: 1,
            height: 1,
            rgba: vec![1, 2, 3, 255],
        };
        assert_eq!(
            encode_image_with_limit(&decoded, ImageMediaType::Png, 8),
            Err(ImageBoundaryFailure::ImageEncodedLimitExceeded)
        );

        let mut output = BoundedWriter::new(4);
        output
            .write_all(&[1, 2, 3, 4])
            .expect("bytes through the limit must fit");
        assert!(output.write_all(&[5]).is_err());
        assert!(output.limit_exceeded);
        assert_eq!(output.inner.get_ref(), &[1, 2, 3, 4]);
    }

    #[test]
    fn session_budget_has_no_count_limit_but_enforces_total_bytes() {
        let mut budget = ImageSessionBudget::default();
        for _ in 0..1_000_000 {
            budget.replace(0, 1).expect("small image must fit");
        }
        assert_eq!(budget.encoded_bytes(), 1_000_000);
        budget
            .replace(0, MAX_ENCODED_SESSION_BYTES - 1_000_000)
            .expect("exact session limit must fit");
        assert_eq!(budget.encoded_bytes(), MAX_ENCODED_SESSION_BYTES);
        assert_eq!(
            budget.replace(0, 1),
            Err(ImageBoundaryFailure::SessionEncodedLimitExceeded)
        );
    }
}
