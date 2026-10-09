// SPDX-License-Identifier: Apache-2.0
//! Auxiliary presentation geometry and original-target fences. No product
//! reads.
use serde::{Deserialize, Serialize};

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct Target {
    pub label: String,
    pub instance: String,
    pub scope: String,
    pub revision: u32,
}
impl Target {
    pub fn matches(&self, label: &str, instance: &str, scope: &str, revision: u32) -> bool {
        self.revision > 0
            && self.label == label
            && self.instance == instance
            && self.scope == scope
            && self.revision == revision
    }
}
#[derive(Clone, Copy, Debug)]
pub struct Area {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
}
#[derive(Clone, Copy, Debug, PartialEq)]
pub struct Placement {
    pub x: i32,
    pub y: i32,
    pub width: u32,
    pub height: u32,
}
/// Physical coordinates throughout; scale affects only the desired CSS size.
/// Prefer below, above, then a fitting side before clamping to the work area.
pub fn place(area: Area, scale: f64, anchor: Option<Area>) -> Placement {
    let scale = if scale.is_finite() && scale > 0.0 {
        scale
    } else {
        1.0
    };
    let width = (380.0 * scale).min(area.width.max(1.0));
    let height = (640.0 * scale).min(area.height.max(1.0));
    let (mut x, mut y) = (
        area.x + (area.width - width) / 2.0,
        area.y + (area.height - height) / 2.0,
    );
    if let Some(icon) = anchor {
        let gap = 8.0 * scale;
        x = icon.x + icon.width / 2.0 - width / 2.0;
        if icon.y + icon.height + gap + height <= area.y + area.height {
            y = icon.y + icon.height + gap;
        } else if icon.y - gap - height >= area.y {
            y = icon.y - gap - height;
        } else if icon.x + icon.width + gap + width <= area.x + area.width {
            x = icon.x + icon.width + gap;
            y = icon.y + icon.height / 2.0 - height / 2.0;
        } else {
            x = icon.x - gap - width;
            y = icon.y + icon.height / 2.0 - height / 2.0;
        }
    }
    Placement {
        x: x.clamp(area.x, area.x + area.width - width).round() as i32,
        y: y.clamp(area.y, area.y + area.height - height).round() as i32,
        width: width.round() as u32,
        height: height.round() as u32,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn negative_monitors_and_mixed_dpi_stay_inside_work_area() {
        for scale in [1.0, 1.25, 2.0] {
            let area = Area {
                x: -1600.0,
                y: -200.0,
                width: 1600.0,
                height: 900.0,
            };
            for icon in [
                Area {
                    x: -1600.0,
                    y: -200.0,
                    width: 24.0,
                    height: 24.0,
                },
                Area {
                    x: -24.0,
                    y: 675.0,
                    width: 24.0,
                    height: 24.0,
                },
            ] {
                let result = place(area, scale, Some(icon));
                assert!(result.x >= -1600 && result.y >= -200);
                assert!(result.x as f64 + result.width as f64 <= 0.0);
                assert!(result.y as f64 + result.height as f64 <= 700.0);
            }
        }
        let small = place(
            Area {
                x: 100.0,
                y: 100.0,
                width: 240.0,
                height: 200.0,
            },
            2.0,
            None,
        );
        assert_eq!(
            small,
            Placement {
                x: 100,
                y: 100,
                width: 240,
                height: 200
            }
        );
    }
    #[test]
    fn old_targets_never_redirect_to_replacement_windows_or_scopes() {
        let target = Target {
            label: "main".into(),
            instance: "original".into(),
            scope: "scope".into(),
            revision: 7,
        };
        assert!(target.matches("main", "original", "scope", 7));
        assert!(!target.matches("main", "replacement", "scope", 7));
        assert!(!target.matches("other", "original", "scope", 7));
        assert!(!target.matches("main", "original", "replacement", 7));
        assert!(!target.matches("main", "original", "scope", 8));
    }
}
