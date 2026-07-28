use std::collections::VecDeque;

use serde::{Deserialize, Serialize};

use super::{CaptureFailure, DecodedImage, decoded_byte_len};

const MAX_OPERATIONS: usize = 1_000;
const MAX_FREEHAND_POINTS: usize = 20_000;
const MAX_TEXT_BYTES: usize = 4_096;
const MAX_LINE_WIDTH: u32 = 128;
const MAX_EFFECT_SIZE: u32 = 128;

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct EditorPoint {
    pub(crate) x: u32,
    pub(crate) y: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct EditorRect {
    pub(crate) x: u32,
    pub(crate) y: u32,
    pub(crate) width: u32,
    pub(crate) height: u32,
}

impl EditorRect {
    fn checked(self, width: u32, height: u32) -> Result<Self, CaptureFailure> {
        let right = self
            .x
            .checked_add(self.width)
            .ok_or(CaptureFailure::InvalidEditorOperation)?;
        let bottom = self
            .y
            .checked_add(self.height)
            .ok_or(CaptureFailure::InvalidEditorOperation)?;
        if self.width == 0 || self.height == 0 || right > width || bottom > height {
            return Err(CaptureFailure::InvalidEditorOperation);
        }
        Ok(self)
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(
    tag = "kind",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase"
)]
pub(crate) enum EditorOperation {
    Crop {
        rect: EditorRect,
    },
    Arrow {
        start: EditorPoint,
        end: EditorPoint,
        color: String,
        line_width: u32,
    },
    Rectangle {
        rect: EditorRect,
        color: String,
        line_width: u32,
    },
    Freehand {
        points: Vec<EditorPoint>,
        color: String,
        line_width: u32,
    },
    Text {
        origin: EditorPoint,
        text: String,
        color: String,
        font_size: u32,
    },
    Marker {
        center: EditorPoint,
        number: u32,
        color: String,
        size: u32,
    },
    Blur {
        rect: EditorRect,
        radius: u32,
    },
    Pixelate {
        rect: EditorRect,
        block_size: u32,
    },
}

pub(crate) fn flatten(
    source: DecodedImage,
    operations: &[EditorOperation],
) -> Result<DecodedImage, CaptureFailure> {
    validate_operations(source.width, source.height, operations)?;
    let crop = operations.iter().find_map(|operation| match operation {
        EditorOperation::Crop { rect } => Some(*rect),
        _ => None,
    });
    let mut output = source;
    for operation in operations {
        match operation {
            EditorOperation::Crop { .. } => {}
            EditorOperation::Arrow {
                start,
                end,
                color,
                line_width,
            } => {
                let color = parse_color(color)?;
                draw_line(&mut output, *start, *end, color, *line_width);
                draw_arrow_head(&mut output, *start, *end, color, *line_width);
            }
            EditorOperation::Rectangle {
                rect,
                color,
                line_width,
            } => draw_rectangle(&mut output, *rect, parse_color(color)?, *line_width),
            EditorOperation::Freehand {
                points,
                color,
                line_width,
            } => {
                let color = parse_color(color)?;
                for pair in points.windows(2) {
                    draw_line(&mut output, pair[0], pair[1], color, *line_width);
                }
            }
            EditorOperation::Text {
                origin,
                text,
                color,
                font_size,
            } => draw_text(&mut output, *origin, text, parse_color(color)?, *font_size),
            EditorOperation::Marker {
                center,
                number,
                color,
                size,
            } => draw_marker(&mut output, *center, *number, parse_color(color)?, *size),
            EditorOperation::Blur { rect, radius } => blur_region(&mut output, *rect, *radius),
            EditorOperation::Pixelate { rect, block_size } => {
                pixelate_region(&mut output, *rect, *block_size)
            }
        }
    }
    match crop {
        Some(rect) => crop_image(&output, rect),
        None => Ok(output),
    }
}

fn validate_operations(
    width: u32,
    height: u32,
    operations: &[EditorOperation],
) -> Result<(), CaptureFailure> {
    if operations.len() > MAX_OPERATIONS {
        return Err(CaptureFailure::InvalidEditSequence);
    }
    let mut crop_count = 0_usize;
    for operation in operations {
        match operation {
            EditorOperation::Crop { rect } => {
                crop_count += 1;
                rect.checked(width, height)?;
            }
            EditorOperation::Arrow {
                start,
                end,
                color,
                line_width,
            } => {
                checked_point(*start, width, height)?;
                checked_point(*end, width, height)?;
                if start == end {
                    return Err(CaptureFailure::InvalidEditorOperation);
                }
                checked_line(*line_width)?;
                parse_color(color)?;
            }
            EditorOperation::Rectangle {
                rect,
                color,
                line_width,
            } => {
                rect.checked(width, height)?;
                checked_line(*line_width)?;
                parse_color(color)?;
            }
            EditorOperation::Freehand {
                points,
                color,
                line_width,
            } => {
                if points.len() < 2 || points.len() > MAX_FREEHAND_POINTS {
                    return Err(CaptureFailure::InvalidEditorOperation);
                }
                for point in points {
                    checked_point(*point, width, height)?;
                }
                checked_line(*line_width)?;
                parse_color(color)?;
            }
            EditorOperation::Text {
                origin,
                text,
                color,
                font_size,
            } => {
                checked_point(*origin, width, height)?;
                if text.is_empty()
                    || text.len() > MAX_TEXT_BYTES
                    || text
                        .chars()
                        .any(|character| !is_supported_text_character(character))
                    || !(8..=MAX_EFFECT_SIZE).contains(font_size)
                {
                    return Err(CaptureFailure::InvalidEditorOperation);
                }
                parse_color(color)?;
            }
            EditorOperation::Marker {
                center,
                number,
                color,
                size,
            } => {
                checked_point(*center, width, height)?;
                if !(1..=999).contains(number) || !(12..=MAX_EFFECT_SIZE).contains(size) {
                    return Err(CaptureFailure::InvalidEditorOperation);
                }
                parse_color(color)?;
            }
            EditorOperation::Blur { rect, radius } => {
                rect.checked(width, height)?;
                if !(1..=MAX_EFFECT_SIZE).contains(radius) {
                    return Err(CaptureFailure::InvalidEditorOperation);
                }
            }
            EditorOperation::Pixelate { rect, block_size } => {
                rect.checked(width, height)?;
                if !(2..=MAX_EFFECT_SIZE).contains(block_size) {
                    return Err(CaptureFailure::InvalidEditorOperation);
                }
            }
        }
    }
    if crop_count > 1 {
        return Err(CaptureFailure::InvalidEditSequence);
    }
    Ok(())
}

fn checked_point(point: EditorPoint, width: u32, height: u32) -> Result<(), CaptureFailure> {
    if point.x >= width || point.y >= height {
        return Err(CaptureFailure::InvalidEditorOperation);
    }
    Ok(())
}

fn checked_line(line_width: u32) -> Result<(), CaptureFailure> {
    if !(1..=MAX_LINE_WIDTH).contains(&line_width) {
        return Err(CaptureFailure::InvalidEditorOperation);
    }
    Ok(())
}

fn parse_color(value: &str) -> Result<[u8; 4], CaptureFailure> {
    let bytes = value.as_bytes();
    if bytes.len() != 7 || bytes[0] != b'#' || !bytes[1..].iter().all(u8::is_ascii_hexdigit) {
        return Err(CaptureFailure::InvalidEditorOperation);
    }
    let component = |high: u8, low: u8| {
        let nibble = |byte: u8| match byte {
            b'0'..=b'9' => byte - b'0',
            b'a'..=b'f' => byte - b'a' + 10,
            b'A'..=b'F' => byte - b'A' + 10,
            _ => unreachable!("hex digits were validated above"),
        };
        nibble(high) * 16 + nibble(low)
    };
    Ok([
        component(bytes[1], bytes[2]),
        component(bytes[3], bytes[4]),
        component(bytes[5], bytes[6]),
        255,
    ])
}

fn pixel_offset(image: &DecodedImage, x: u32, y: u32) -> usize {
    ((y as usize * image.width as usize) + x as usize) * 4
}

fn blend_pixel(image: &mut DecodedImage, x: i64, y: i64, color: [u8; 4]) {
    let Ok(x) = u32::try_from(x) else {
        return;
    };
    let Ok(y) = u32::try_from(y) else {
        return;
    };
    if x >= image.width || y >= image.height {
        return;
    }
    let offset = pixel_offset(image, x, y);
    let alpha = u32::from(color[3]);
    let inverse = 255 - alpha;
    for (channel, source) in color[..3].iter().enumerate() {
        image.rgba[offset + channel] =
            ((u32::from(*source) * alpha + u32::from(image.rgba[offset + channel]) * inverse + 127)
                / 255) as u8;
    }
    image.rgba[offset + 3] = 255;
}

fn stamp(image: &mut DecodedImage, point: EditorPoint, color: [u8; 4], width: u32) {
    let lower = -i64::from(width.saturating_sub(1) / 2);
    let upper = i64::from(width / 2);
    let center_offset = i64::from(width.is_multiple_of(2));
    let doubled_radius = i64::from(if width.is_multiple_of(2) {
        width
    } else {
        width.saturating_sub(1)
    });
    let doubled_radius_squared = doubled_radius * doubled_radius;
    for y in lower..=upper {
        for x in lower..=upper {
            let x_from_center = x * 2 - center_offset;
            let y_from_center = y * 2 - center_offset;
            if x_from_center * x_from_center + y_from_center * y_from_center
                <= doubled_radius_squared
            {
                blend_pixel(image, i64::from(point.x) + x, i64::from(point.y) + y, color);
            }
        }
    }
}

fn draw_line(
    image: &mut DecodedImage,
    start: EditorPoint,
    end: EditorPoint,
    color: [u8; 4],
    width: u32,
) {
    let mut x0 = i64::from(start.x);
    let mut y0 = i64::from(start.y);
    let x1 = i64::from(end.x);
    let y1 = i64::from(end.y);
    let dx = (x1 - x0).abs();
    let sx = if x0 < x1 { 1 } else { -1 };
    let dy = -(y1 - y0).abs();
    let sy = if y0 < y1 { 1 } else { -1 };
    let mut error = dx + dy;
    loop {
        stamp(
            image,
            EditorPoint {
                x: x0 as u32,
                y: y0 as u32,
            },
            color,
            width,
        );
        if x0 == x1 && y0 == y1 {
            break;
        }
        let twice_error = error * 2;
        if twice_error >= dy {
            error += dy;
            x0 += sx;
        }
        if twice_error <= dx {
            error += dx;
            y0 += sy;
        }
    }
}

fn draw_arrow_head(
    image: &mut DecodedImage,
    start: EditorPoint,
    end: EditorPoint,
    color: [u8; 4],
    width: u32,
) {
    let dx = f64::from(end.x) - f64::from(start.x);
    let dy = f64::from(end.y) - f64::from(start.y);
    let length = dx.hypot(dy);
    let head = f64::from((width * 4).clamp(8, 48)).min(length * 0.6);
    let angle = dy.atan2(dx);
    for offset in [2.55_f64, -2.55_f64] {
        let wing = EditorPoint {
            x: (f64::from(end.x) + head * (angle + offset).cos())
                .round()
                .clamp(0.0, f64::from(image.width - 1)) as u32,
            y: (f64::from(end.y) + head * (angle + offset).sin())
                .round()
                .clamp(0.0, f64::from(image.height - 1)) as u32,
        };
        draw_line(image, end, wing, color, width);
    }
}

fn draw_rectangle(image: &mut DecodedImage, rect: EditorRect, color: [u8; 4], width: u32) {
    let right = rect.x + rect.width - 1;
    let bottom = rect.y + rect.height - 1;
    let top_left = EditorPoint {
        x: rect.x,
        y: rect.y,
    };
    let top_right = EditorPoint {
        x: right,
        y: rect.y,
    };
    let bottom_left = EditorPoint {
        x: rect.x,
        y: bottom,
    };
    let bottom_right = EditorPoint {
        x: right,
        y: bottom,
    };
    draw_line(image, top_left, top_right, color, width);
    draw_line(image, top_right, bottom_right, color, width);
    draw_line(image, bottom_right, bottom_left, color, width);
    draw_line(image, bottom_left, top_left, color, width);
}

fn draw_text(
    image: &mut DecodedImage,
    origin: EditorPoint,
    text: &str,
    color: [u8; 4],
    font_size: u32,
) {
    let scale = (font_size / 7).max(1);
    let mut cursor_x = origin.x;
    let mut cursor_y = origin.y;
    for character in text.chars() {
        if character == '\n' {
            cursor_x = origin.x;
            cursor_y = cursor_y.saturating_add(8 * scale);
            continue;
        }
        let glyph = glyph(character);
        for (row, bits) in glyph.into_iter().enumerate() {
            for column in 0..5 {
                if bits & (1 << (4 - column)) == 0 {
                    continue;
                }
                for y in 0..scale {
                    for x in 0..scale {
                        blend_pixel(
                            image,
                            i64::from(cursor_x + column * scale + x),
                            i64::from(cursor_y + row as u32 * scale + y),
                            color,
                        );
                    }
                }
            }
        }
        cursor_x = cursor_x.saturating_add(6 * scale);
    }
}

fn is_supported_text_character(character: char) -> bool {
    matches!(
        character,
        '0'..='9'
            | 'A'..='Z'
            | 'a'..='z'
            | ' '
            | '-'
            | '.'
            | ':'
            | '!'
            | '?'
    )
}

fn glyph(character: char) -> [u8; 7] {
    match character {
        '0' => [14, 17, 19, 21, 25, 17, 14],
        '1' => [4, 12, 4, 4, 4, 4, 14],
        '2' => [14, 17, 1, 2, 4, 8, 31],
        '3' => [30, 1, 1, 14, 1, 1, 30],
        '4' => [2, 6, 10, 18, 31, 2, 2],
        '5' => [31, 16, 16, 30, 1, 1, 30],
        '6' => [14, 16, 16, 30, 17, 17, 14],
        '7' => [31, 1, 2, 4, 8, 8, 8],
        '8' => [14, 17, 17, 14, 17, 17, 14],
        '9' => [14, 17, 17, 15, 1, 1, 14],
        'A' => [14, 17, 17, 31, 17, 17, 17],
        'B' => [30, 17, 17, 30, 17, 17, 30],
        'C' => [14, 17, 16, 16, 16, 17, 14],
        'D' => [30, 17, 17, 17, 17, 17, 30],
        'E' => [31, 16, 16, 30, 16, 16, 31],
        'F' => [31, 16, 16, 30, 16, 16, 16],
        'G' => [14, 17, 16, 23, 17, 17, 15],
        'H' => [17, 17, 17, 31, 17, 17, 17],
        'I' => [14, 4, 4, 4, 4, 4, 14],
        'J' => [7, 2, 2, 2, 18, 18, 12],
        'K' => [17, 18, 20, 24, 20, 18, 17],
        'L' => [16, 16, 16, 16, 16, 16, 31],
        'M' => [17, 27, 21, 21, 17, 17, 17],
        'N' => [17, 25, 21, 19, 17, 17, 17],
        'O' => [14, 17, 17, 17, 17, 17, 14],
        'P' => [30, 17, 17, 30, 16, 16, 16],
        'Q' => [14, 17, 17, 17, 21, 18, 13],
        'R' => [30, 17, 17, 30, 20, 18, 17],
        'S' => [15, 16, 16, 14, 1, 1, 30],
        'T' => [31, 4, 4, 4, 4, 4, 4],
        'U' => [17, 17, 17, 17, 17, 17, 14],
        'V' => [17, 17, 17, 17, 17, 10, 4],
        'W' => [17, 17, 17, 21, 21, 21, 10],
        'X' => [17, 17, 10, 4, 10, 17, 17],
        'Y' => [17, 17, 10, 4, 4, 4, 4],
        'Z' => [31, 1, 2, 4, 8, 16, 31],
        'a' => [0, 0, 14, 1, 15, 17, 15],
        'b' => [16, 16, 30, 17, 17, 17, 30],
        'c' => [0, 0, 14, 16, 16, 17, 14],
        'd' => [1, 1, 15, 17, 17, 17, 15],
        'e' => [0, 0, 14, 17, 31, 16, 14],
        'f' => [6, 8, 30, 8, 8, 8, 8],
        'g' => [0, 0, 15, 17, 15, 1, 14],
        'h' => [16, 16, 30, 17, 17, 17, 17],
        'i' => [4, 0, 12, 4, 4, 4, 14],
        'j' => [2, 0, 6, 2, 2, 18, 12],
        'k' => [16, 16, 18, 20, 24, 20, 18],
        'l' => [12, 4, 4, 4, 4, 4, 14],
        'm' => [0, 0, 26, 21, 21, 21, 21],
        'n' => [0, 0, 30, 17, 17, 17, 17],
        'o' => [0, 0, 14, 17, 17, 17, 14],
        'p' => [0, 0, 30, 17, 30, 16, 16],
        'q' => [0, 0, 15, 17, 15, 1, 1],
        'r' => [0, 0, 22, 25, 16, 16, 16],
        's' => [0, 0, 15, 16, 14, 1, 30],
        't' => [8, 8, 30, 8, 8, 9, 6],
        'u' => [0, 0, 17, 17, 17, 19, 13],
        'v' => [0, 0, 17, 17, 17, 10, 4],
        'w' => [0, 0, 17, 17, 21, 21, 10],
        'x' => [0, 0, 17, 10, 4, 10, 17],
        'y' => [0, 0, 17, 17, 15, 1, 14],
        'z' => [0, 0, 31, 2, 4, 8, 31],
        ' ' => [0; 7],
        '-' => [0, 0, 0, 31, 0, 0, 0],
        '.' => [0, 0, 0, 0, 0, 12, 12],
        ':' => [0, 12, 12, 0, 12, 12, 0],
        '!' => [4, 4, 4, 4, 4, 0, 4],
        '?' => [14, 17, 1, 2, 4, 0, 4],
        _ => [31, 17, 1, 2, 4, 0, 4],
    }
}

fn draw_marker(
    image: &mut DecodedImage,
    center: EditorPoint,
    number: u32,
    color: [u8; 4],
    size: u32,
) {
    let radius = i64::from(size / 2);
    for y in -radius..=radius {
        for x in -radius..=radius {
            if x * x + y * y <= radius * radius {
                blend_pixel(
                    image,
                    i64::from(center.x) + x,
                    i64::from(center.y) + y,
                    color,
                );
            }
        }
    }
    let label = number.to_string();
    let font_size = (size / 2).max(8);
    let text_width = label.len() as u32 * 6 * (font_size / 7).max(1);
    let origin = EditorPoint {
        x: center.x.saturating_sub(text_width / 2),
        y: center.y.saturating_sub(font_size / 2),
    };
    draw_text(image, origin, &label, [255, 255, 255, 255], font_size);
}

fn blur_region(image: &mut DecodedImage, rect: EditorRect, radius: u32) {
    let mut window = VecDeque::<[u8; 4]>::with_capacity((radius as usize * 2) + 1);

    for local_y in 0..rect.height {
        let mut totals = [0_u64; 4];
        let initial_right = radius.min(rect.width - 1);
        for local_x in 0..=initial_right {
            let source_offset = pixel_offset(image, rect.x + local_x, rect.y + local_y);
            let pixel = image.rgba[source_offset..source_offset + 4]
                .try_into()
                .expect("RGBA pixels always have four channels");
            window.push_back(pixel);
            for (channel, total) in totals.iter_mut().enumerate() {
                *total += u64::from(pixel[channel]);
            }
        }
        for local_x in 0..rect.width {
            let target_offset = pixel_offset(image, rect.x + local_x, rect.y + local_y);
            for (channel, total) in totals.iter().enumerate() {
                image.rgba[target_offset + channel] = (*total / window.len() as u64) as u8;
            }
            if local_x >= radius {
                let removed = window
                    .pop_front()
                    .expect("the blur window contains the current pixel");
                for (channel, total) in totals.iter_mut().enumerate() {
                    *total -= u64::from(removed[channel]);
                }
            }
            if let Some(add_x) = local_x.checked_add(radius + 1)
                && add_x < rect.width
            {
                let add_offset = pixel_offset(image, rect.x + add_x, rect.y + local_y);
                let pixel = image.rgba[add_offset..add_offset + 4]
                    .try_into()
                    .expect("RGBA pixels always have four channels");
                window.push_back(pixel);
                for (channel, total) in totals.iter_mut().enumerate() {
                    *total += u64::from(pixel[channel]);
                }
            }
        }
        window.clear();
    }

    for local_x in 0..rect.width {
        let mut totals = [0_u64; 4];
        let initial_bottom = radius.min(rect.height - 1);
        for local_y in 0..=initial_bottom {
            let source_offset = pixel_offset(image, rect.x + local_x, rect.y + local_y);
            let pixel = image.rgba[source_offset..source_offset + 4]
                .try_into()
                .expect("RGBA pixels always have four channels");
            window.push_back(pixel);
            for (channel, total) in totals.iter_mut().enumerate() {
                *total += u64::from(pixel[channel]);
            }
        }
        for local_y in 0..rect.height {
            let target_offset = pixel_offset(image, rect.x + local_x, rect.y + local_y);
            for (channel, total) in totals.iter().enumerate() {
                image.rgba[target_offset + channel] = (*total / window.len() as u64) as u8;
            }
            if local_y >= radius {
                let removed = window
                    .pop_front()
                    .expect("the blur window contains the current pixel");
                for (channel, total) in totals.iter_mut().enumerate() {
                    *total -= u64::from(removed[channel]);
                }
            }
            if let Some(add_y) = local_y.checked_add(radius + 1)
                && add_y < rect.height
            {
                let add_offset = pixel_offset(image, rect.x + local_x, rect.y + add_y);
                let pixel = image.rgba[add_offset..add_offset + 4]
                    .try_into()
                    .expect("RGBA pixels always have four channels");
                window.push_back(pixel);
                for (channel, total) in totals.iter_mut().enumerate() {
                    *total += u64::from(pixel[channel]);
                }
            }
        }
        window.clear();
    }
}

fn pixelate_region(image: &mut DecodedImage, rect: EditorRect, block_size: u32) {
    let right = rect.x + rect.width;
    let bottom = rect.y + rect.height;
    for block_y in (rect.y..bottom).step_by(block_size as usize) {
        for block_x in (rect.x..right).step_by(block_size as usize) {
            let block_right = block_x.saturating_add(block_size).min(right);
            let block_bottom = block_y.saturating_add(block_size).min(bottom);
            let mut totals = [0_u64; 4];
            let count = u64::from((block_right - block_x) * (block_bottom - block_y));
            for y in block_y..block_bottom {
                for x in block_x..block_right {
                    let offset = pixel_offset(image, x, y);
                    for (channel, total) in totals.iter_mut().enumerate() {
                        *total += u64::from(image.rgba[offset + channel]);
                    }
                }
            }
            let average = [
                (totals[0] / count) as u8,
                (totals[1] / count) as u8,
                (totals[2] / count) as u8,
                (totals[3] / count) as u8,
            ];
            for y in block_y..block_bottom {
                for x in block_x..block_right {
                    let offset = pixel_offset(image, x, y);
                    image.rgba[offset..offset + 4].copy_from_slice(&average);
                }
            }
        }
    }
}

fn crop_image(image: &DecodedImage, rect: EditorRect) -> Result<DecodedImage, CaptureFailure> {
    let capacity = decoded_byte_len(rect.width, rect.height).map_err(CaptureFailure::from)?;
    let mut rgba = Vec::with_capacity(capacity);
    for y in rect.y..rect.y + rect.height {
        let start = pixel_offset(image, rect.x, y);
        let end = start + rect.width as usize * 4;
        rgba.extend_from_slice(&image.rgba[start..end]);
    }
    Ok(DecodedImage {
        width: rect.width,
        height: rect.height,
        rgba,
    })
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::realqa_capture::{ImageMediaType, encode_image};

    fn fixture() -> DecodedImage {
        let mut rgba = Vec::new();
        for y in 0..8_u8 {
            for x in 0..8_u8 {
                rgba.extend_from_slice(&[x * 20, y * 20, x * 10 + y, 255]);
            }
        }
        DecodedImage {
            width: 8,
            height: 8,
            rgba,
        }
    }

    #[test]
    fn validates_every_operation_and_flattens_deterministically() {
        let operations = vec![
            EditorOperation::Rectangle {
                rect: EditorRect {
                    x: 1,
                    y: 1,
                    width: 6,
                    height: 6,
                },
                color: "#ff0000".to_owned(),
                line_width: 1,
            },
            EditorOperation::Arrow {
                start: EditorPoint { x: 0, y: 7 },
                end: EditorPoint { x: 7, y: 0 },
                color: "#00ff00".to_owned(),
                line_width: 1,
            },
            EditorOperation::Freehand {
                points: vec![EditorPoint { x: 0, y: 0 }, EditorPoint { x: 7, y: 7 }],
                color: "#0000ff".to_owned(),
                line_width: 1,
            },
            EditorOperation::Text {
                origin: EditorPoint { x: 0, y: 0 },
                text: "A".to_owned(),
                color: "#ffffff".to_owned(),
                font_size: 8,
            },
            EditorOperation::Marker {
                center: EditorPoint { x: 4, y: 4 },
                number: 1,
                color: "#ff0000".to_owned(),
                size: 12,
            },
            EditorOperation::Blur {
                rect: EditorRect {
                    x: 0,
                    y: 0,
                    width: 4,
                    height: 4,
                },
                radius: 1,
            },
            EditorOperation::Pixelate {
                rect: EditorRect {
                    x: 4,
                    y: 4,
                    width: 4,
                    height: 4,
                },
                block_size: 2,
            },
            EditorOperation::Crop {
                rect: EditorRect {
                    x: 1,
                    y: 1,
                    width: 6,
                    height: 6,
                },
            },
        ];
        let first = flatten(fixture(), &operations).expect("operations must flatten");
        let second = flatten(fixture(), &operations).expect("operations must flatten again");
        assert_eq!(first, second);
        assert_eq!((first.width, first.height), (6, 6));
        for media_type in [ImageMediaType::Png, ImageMediaType::Webp] {
            assert_eq!(
                encode_image(&first, media_type).expect("first output must encode"),
                encode_image(&second, media_type).expect("second output must encode")
            );
        }
    }

    #[test]
    fn preserves_supported_text_case_and_rejects_unsupported_glyphs() {
        let text_operation = |text: &str| EditorOperation::Text {
            origin: EditorPoint { x: 0, y: 0 },
            text: text.to_owned(),
            color: "#ffffff".to_owned(),
            font_size: 8,
        };
        let lowercase =
            flatten(fixture(), &[text_operation("a")]).expect("lowercase text must flatten");
        let uppercase =
            flatten(fixture(), &[text_operation("A")]).expect("uppercase text must flatten");
        assert_ne!(lowercase, uppercase);
        assert_eq!(
            flatten(fixture(), &[text_operation("é")]),
            Err(CaptureFailure::InvalidEditorOperation)
        );
    }

    #[test]
    fn rejects_non_ascii_colors_without_panicking() {
        let operation = EditorOperation::Rectangle {
            rect: EditorRect {
                x: 0,
                y: 0,
                width: 4,
                height: 4,
            },
            color: "#aé123".to_owned(),
            line_width: 1,
        };

        assert_eq!(
            flatten(fixture(), &[operation]),
            Err(CaptureFailure::InvalidEditorOperation)
        );
    }

    #[test]
    fn rasterizes_even_stroke_widths_at_the_requested_diameter() {
        let mut image = DecodedImage {
            width: 8,
            height: 8,
            rgba: vec![0; 8 * 8 * 4],
        };

        stamp(
            &mut image,
            EditorPoint { x: 3, y: 3 },
            [255, 255, 255, 255],
            4,
        );

        let touched = (0..image.height)
            .flat_map(|y| (0..image.width).map(move |x| (x, y)))
            .filter(|(x, y)| image.rgba[pixel_offset(&image, *x, *y) + 3] != 0)
            .collect::<Vec<_>>();
        let min_x = touched.iter().map(|(x, _)| *x).min();
        let max_x = touched.iter().map(|(x, _)| *x).max();
        let min_y = touched.iter().map(|(_, y)| *y).min();
        let max_y = touched.iter().map(|(_, y)| *y).max();

        assert_eq!((min_x, max_x), (Some(2), Some(5)));
        assert_eq!((min_y, max_y), (Some(2), Some(5)));
    }

    #[test]
    fn rejects_invalid_bounds_effects_and_sequences() {
        let invalid_crop = vec![EditorOperation::Crop {
            rect: EditorRect {
                x: 7,
                y: 7,
                width: 2,
                height: 2,
            },
        }];
        assert_eq!(
            flatten(fixture(), &invalid_crop),
            Err(CaptureFailure::InvalidEditorOperation)
        );

        let duplicate_crop = vec![
            EditorOperation::Crop {
                rect: EditorRect {
                    x: 0,
                    y: 0,
                    width: 4,
                    height: 4,
                },
            },
            EditorOperation::Crop {
                rect: EditorRect {
                    x: 1,
                    y: 1,
                    width: 4,
                    height: 4,
                },
            },
        ];
        assert_eq!(
            flatten(fixture(), &duplicate_crop),
            Err(CaptureFailure::InvalidEditSequence)
        );

        let invalid_blur = vec![EditorOperation::Blur {
            rect: EditorRect {
                x: 0,
                y: 0,
                width: 8,
                height: 8,
            },
            radius: 0,
        }];
        assert_eq!(
            flatten(fixture(), &invalid_blur),
            Err(CaptureFailure::InvalidEditorOperation)
        );
    }

    #[test]
    fn blur_and_pixelate_do_not_modify_pixels_outside_their_bounds() {
        let source = fixture();
        for operation in [
            EditorOperation::Blur {
                rect: EditorRect {
                    x: 1,
                    y: 1,
                    width: 3,
                    height: 3,
                },
                radius: 2,
            },
            EditorOperation::Pixelate {
                rect: EditorRect {
                    x: 1,
                    y: 1,
                    width: 3,
                    height: 3,
                },
                block_size: 2,
            },
        ] {
            let output = flatten(source.clone(), &[operation]).expect("effect must flatten");
            assert_eq!(&output.rgba[..32], &source.rgba[..32]);
            assert_eq!(&output.rgba[4 * 8 * 4..], &source.rgba[4 * 8 * 4..]);
        }
    }

    #[test]
    fn blur_matches_the_separable_box_average() {
        let output = flatten(
            fixture(),
            &[EditorOperation::Blur {
                rect: EditorRect {
                    x: 0,
                    y: 0,
                    width: 8,
                    height: 8,
                },
                radius: 1,
            }],
        )
        .expect("blur must flatten");

        assert_eq!(
            &output.rgba[pixel_offset(&output, 4, 4)..pixel_offset(&output, 4, 4) + 4],
            &[80, 80, 44, 255]
        );
    }
}
