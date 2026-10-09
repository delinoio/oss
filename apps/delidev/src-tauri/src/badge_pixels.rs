// SPDX-License-Identifier: Apache-2.0
//! Bounded runtime badge pixels. Installed branding is never modified.
pub fn fresh(age: std::time::Duration) -> bool {
    age < std::time::Duration::from_secs(30)
}
pub fn label(count: u64) -> String {
    if count == 0 {
        String::new()
    } else if count > 99 {
        "99+".into()
    } else {
        count.to_string()
    }
}
pub fn render(base: &[u8], width: u32, height: u32, count: u64) -> Option<Vec<u8>> {
    if width < 16
        || height < 16
        || width > 1024
        || height > 1024
        || base.len() != (width * height * 4) as usize
    {
        return None;
    }
    let mut rgba = base.to_vec();
    if count == 0 {
        return Some(rgba);
    }
    let text = label(count);
    let scale = (width.min(height) / 32).max(1);
    let badge_width = (text.len() as u32 * 4 + 2) * scale;
    let badge_height = 9 * scale;
    let left = width.saturating_sub(badge_width);
    for y in 0..badge_height.min(height) {
        for x in left..width {
            if (y < scale || y >= badge_height - scale) && (x < left + scale || x >= width - scale)
            {
                continue;
            }
            let offset = ((y * width + x) * 4) as usize;
            rgba[offset..offset + 4].copy_from_slice(&[214, 36, 49, 255]);
        }
    }
    for (index, ch) in text.bytes().enumerate() {
        let rows: [u8; 5] = match ch {
            b'0' => [7, 5, 5, 5, 7],
            b'1' => [2, 6, 2, 2, 7],
            b'2' => [7, 1, 7, 4, 7],
            b'3' => [7, 1, 7, 1, 7],
            b'4' => [5, 5, 7, 1, 1],
            b'5' => [7, 4, 7, 1, 7],
            b'6' => [7, 4, 7, 5, 7],
            b'7' => [7, 1, 1, 1, 1],
            b'8' => [7, 5, 7, 5, 7],
            b'9' => [7, 5, 7, 1, 7],
            _ => [0, 2, 7, 2, 0],
        };
        for (row, bits) in rows.iter().enumerate() {
            for col in 0..3 {
                if bits & (1 << (2 - col)) == 0 {
                    continue;
                }
                for sy in 0..scale {
                    for sx in 0..scale {
                        let x = left + (index as u32 * 4 + 1 + col) * scale + sx;
                        let y = (row as u32 + 2) * scale + sy;
                        if x < width && y < height {
                            let offset = ((y * width + x) * 4) as usize;
                            rgba[offset..offset + 4].copy_from_slice(&[255; 4]);
                        }
                    }
                }
            }
        }
    }
    Some(rgba)
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn freshness() {
        assert!(fresh(std::time::Duration::from_secs(29)));
        assert!(!fresh(std::time::Duration::from_secs(30)));
        assert!(!fresh(std::time::Duration::from_secs(90)));
    }
    #[test]
    fn labels() {
        assert_eq!(label(0), "");
        assert_eq!(label(1), "1");
        assert_eq!(label(99), "99");
        assert_eq!(label(100), "99+");
        assert_eq!(label(u64::MAX), "99+");
    }
    #[test]
    fn bounded_brand() {
        let base = vec![55; 32 * 32 * 4];
        assert_eq!(render(&base, 32, 32, 0).unwrap(), base);
        let badge = render(&base, 32, 32, 100).unwrap();
        assert_eq!(&badge[16 * 32 * 4..], &base[16 * 32 * 4..]);
        assert!(badge.chunks(4).any(|pixel| pixel == [255; 4]));
        assert!(render(&[], 32, 32, 1).is_none());
    }
}
