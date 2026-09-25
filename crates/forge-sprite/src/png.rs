use flate2::{Compress, Compression, FlushCompress, Status};

use super::*;

const CHUNK_BYTES: usize = 64 * 1024;

pub(super) fn encode(image: &RgbaImage) -> Result<Vec<u8>> {
    encode_checked(image, |_| checkpoint())
}

fn encode_checked(image: &RgbaImage, mut check: impl FnMut(u64) -> Result<()>) -> Result<Vec<u8>> {
    check(0)?;
    let mut bytes = Vec::new();
    let mut encoder = ::png::Encoder::new(&mut bytes, image.width(), image.height());
    encoder.set_color(::png::ColorType::Rgba);
    encoder.set_depth(::png::BitDepth::Eight);
    let mut writer = encoder.write_header().map_err(|_| invalid("image"))?;
    let mut compressor = Compress::new(Compression::fast(), true);
    let mut output = [0; CHUNK_BYTES];
    let mut compress = |mut input: &[u8], flush| -> Result<()> {
        loop {
            check(compressor.total_in())?;
            let before_in = compressor.total_in();
            let before_out = compressor.total_out();
            let status = compressor
                .compress(input, &mut output, flush)
                .map_err(|_| invalid("image"))?;
            let consumed = (compressor.total_in() - before_in) as usize;
            let written = (compressor.total_out() - before_out) as usize;
            input = &input[consumed..];
            if written != 0 {
                writer
                    .write_chunk(::png::chunk::IDAT, &output[..written])
                    .map_err(|_| invalid("image"))?;
            }
            if status == Status::StreamEnd || (input.is_empty() && flush != FlushCompress::Finish) {
                return Ok(());
            }
            if consumed == 0 && written == 0 {
                return Err(invalid("image"));
            }
        }
    };
    // png's high-level streaming writer still filters/compresses a whole row
    // per call. Atlas rows can span millions of pixels, so feed unfiltered PNG
    // scanlines through bounded zlib calls and let png frame/checksum the IDATs.
    // Retain this until that writer supports cancellation within a scanline.
    for row in image.as_raw().chunks_exact(image.width() as usize * 4) {
        compress(&[0], FlushCompress::None)?;
        for chunk in row.chunks(CHUNK_BYTES) {
            compress(chunk, FlushCompress::None)?;
        }
    }
    compress(&[], FlushCompress::Finish)?;
    writer.finish().map_err(|_| invalid("image"))?;
    check(compressor.total_in())?;
    Ok(bytes)
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    };

    use forge_tree_doc::cancellation::with_cancellation;

    use super::*;

    #[test]
    fn cancellation_interrupts_compression_inside_a_wide_scanline_and_recovers() {
        let image = RgbaImage::from_pixel(65_536, 2, image::Rgba([10, 20, 30, 255]));
        let flag = Arc::new(AtomicBool::new(false));
        let mut cancelled_at = 0;
        let error = with_cancellation(flag.clone(), || {
            encode_checked(&image, |consumed| {
                if consumed >= CHUNK_BYTES as u64 {
                    cancelled_at = consumed;
                    flag.store(true, Ordering::Release);
                }
                checkpoint()
            })
            .unwrap_err()
        });
        assert_eq!(error.code, ErrorCode::Cancelled);
        assert!(cancelled_at < u64::from(image.width()) * 4);
        let bytes = encode(&image).unwrap();
        assert_eq!(image::load_from_memory(&bytes).unwrap().into_rgba8(), image);
        assert_eq!(encode(&image).unwrap(), bytes);
    }

    #[test]
    fn chunked_png_roundtrips_noisy_rgba_and_alpha() {
        let mut image = RgbaImage::new(257, 193);
        let mut state = 0x12345678u32;
        for byte in image.as_mut() {
            state ^= state << 13;
            state ^= state >> 17;
            state ^= state << 5;
            *byte = state as u8;
        }
        let bytes = encode(&image).unwrap();
        assert!(bytes.len() > CHUNK_BYTES * 2);
        assert_eq!(image::load_from_memory(&bytes).unwrap().into_rgba8(), image);
    }
}
