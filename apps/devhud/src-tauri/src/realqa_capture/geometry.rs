use serde::{Deserialize, Serialize};

use super::CaptureFailure;

const MIN_SELECTION_EXTENT: f64 = 1.0;

#[derive(Debug, Clone, Copy, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct LogicalRect {
    pub(crate) x: f64,
    pub(crate) y: f64,
    pub(crate) width: f64,
    pub(crate) height: f64,
}

impl LogicalRect {
    pub(crate) fn checked(self) -> Result<Self, CaptureFailure> {
        if !self.x.is_finite()
            || !self.y.is_finite()
            || !self.width.is_finite()
            || !self.height.is_finite()
            || self.width < MIN_SELECTION_EXTENT
            || self.height < MIN_SELECTION_EXTENT
            || !self.right().is_finite()
            || !self.bottom().is_finite()
        {
            return Err(CaptureFailure::InvalidSelection);
        }
        Ok(self)
    }

    pub(crate) fn right(self) -> f64 {
        self.x + self.width
    }

    pub(crate) fn bottom(self) -> f64 {
        self.y + self.height
    }

    pub(crate) fn intersection(self, other: Self) -> Option<Self> {
        let x = self.x.max(other.x);
        let y = self.y.max(other.y);
        let right = self.right().min(other.right());
        let bottom = self.bottom().min(other.bottom());
        (right > x && bottom > y).then_some(Self {
            x,
            y,
            width: right - x,
            height: bottom - y,
        })
    }

    pub(crate) fn bounding(rectangles: impl Iterator<Item = Self>) -> Option<Self> {
        rectangles.reduce(|left, right| {
            let x = left.x.min(right.x);
            let y = left.y.min(right.y);
            let far_x = left.right().max(right.right());
            let far_y = left.bottom().max(right.bottom());
            Self {
                x,
                y,
                width: far_x - x,
                height: far_y - y,
            }
        })
    }
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct PhysicalSize {
    pub(crate) width: u32,
    pub(crate) height: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct PixelRect {
    pub(crate) x: u32,
    pub(crate) y: u32,
    pub(crate) width: u32,
    pub(crate) height: u32,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct ScaleFactor {
    pub(crate) numerator: u32,
    pub(crate) denominator: u32,
}

impl ScaleFactor {
    pub(crate) fn checked(self) -> Result<Self, CaptureFailure> {
        if self.numerator == 0 || self.denominator == 0 {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        Ok(self)
    }

    fn apply_floor(self, logical: f64) -> Result<u32, CaptureFailure> {
        let scaled = logical * f64::from(self.numerator) / f64::from(self.denominator);
        checked_pixel(scaled.floor())
    }

    fn apply_ceil(self, logical: f64) -> Result<u32, CaptureFailure> {
        let scaled = logical * f64::from(self.numerator) / f64::from(self.denominator);
        checked_pixel(scaled.ceil())
    }
}

fn checked_pixel(value: f64) -> Result<u32, CaptureFailure> {
    if !value.is_finite() || value < 0.0 || value > f64::from(u32::MAX) {
        return Err(CaptureFailure::InvalidDisplaySnapshot);
    }
    Ok(value as u32)
}

#[derive(Debug, Clone, PartialEq, Eq, Hash, Deserialize, Serialize)]
#[serde(transparent)]
pub(crate) struct DisplayId(pub(crate) String);

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DisplayDescriptor {
    pub(crate) id: DisplayId,
    pub(crate) logical_bounds: LogicalRect,
    pub(crate) physical_size: PhysicalSize,
    pub(crate) scale: ScaleFactor,
    pub(crate) primary: bool,
}

impl DisplayDescriptor {
    fn checked(self) -> Result<Self, CaptureFailure> {
        self.logical_bounds.checked()?;
        self.scale.checked()?;
        if self.id.0.is_empty()
            || self.id.0.len() > 128
            || self.physical_size.width == 0
            || self.physical_size.height == 0
        {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        Ok(self)
    }

    fn pixel_region(
        &self,
        region: LogicalRect,
    ) -> Result<Option<DisplayPixelRegion>, CaptureFailure> {
        let Some(intersection) = region.intersection(self.logical_bounds) else {
            return Ok(None);
        };
        let local_x = intersection.x - self.logical_bounds.x;
        let local_y = intersection.y - self.logical_bounds.y;
        let local_right = intersection.right() - self.logical_bounds.x;
        let local_bottom = intersection.bottom() - self.logical_bounds.y;
        let x = self.scale.apply_floor(local_x)?;
        let y = self.scale.apply_floor(local_y)?;
        let right = self
            .scale
            .apply_ceil(local_right)?
            .min(self.physical_size.width);
        let bottom = self
            .scale
            .apply_ceil(local_bottom)?
            .min(self.physical_size.height);
        Ok((right > x && bottom > y).then(|| DisplayPixelRegion {
            display_id: self.id.clone(),
            pixels: PixelRect {
                x,
                y,
                width: right - x,
                height: bottom - y,
            },
        }))
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(transparent)]
pub(crate) struct DisplaySnapshotId(pub(crate) String);

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DisplaySnapshot {
    pub(crate) snapshot_id: DisplaySnapshotId,
    pub(crate) displays: Vec<DisplayDescriptor>,
}

impl DisplaySnapshot {
    pub(crate) fn checked(mut displays: Vec<DisplayDescriptor>) -> Result<Self, CaptureFailure> {
        if displays.is_empty() {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }
        displays = displays
            .into_iter()
            .map(DisplayDescriptor::checked)
            .collect::<Result<Vec<_>, _>>()?;
        displays.sort_by(|left, right| left.id.0.cmp(&right.id.0));
        if displays.windows(2).any(|pair| pair[0].id == pair[1].id) {
            return Err(CaptureFailure::InvalidDisplaySnapshot);
        }

        let mut fingerprint = 0xcbf2_9ce4_8422_2325_u64;
        for display in &displays {
            fingerprint_bytes(&mut fingerprint, display.id.0.as_bytes());
            for value in [
                display.logical_bounds.x.to_bits(),
                display.logical_bounds.y.to_bits(),
                display.logical_bounds.width.to_bits(),
                display.logical_bounds.height.to_bits(),
                u64::from(display.physical_size.width),
                u64::from(display.physical_size.height),
                u64::from(display.scale.numerator),
                u64::from(display.scale.denominator),
                u64::from(display.primary),
            ] {
                fingerprint_bytes(&mut fingerprint, &value.to_le_bytes());
            }
        }
        Ok(Self {
            snapshot_id: DisplaySnapshotId(format!("{fingerprint:016x}")),
            displays,
        })
    }

    pub(crate) fn desktop_bounds(&self) -> Result<LogicalRect, CaptureFailure> {
        LogicalRect::bounding(self.displays.iter().map(|display| display.logical_bounds))
            .ok_or(CaptureFailure::InvalidDisplaySnapshot)
    }

    pub(crate) fn display(&self, id: &DisplayId) -> Option<&DisplayDescriptor> {
        self.displays.iter().find(|display| &display.id == id)
    }

    pub(crate) fn pixel_regions(
        &self,
        region: LogicalRect,
    ) -> Result<Vec<DisplayPixelRegion>, CaptureFailure> {
        let region = region.checked()?;
        let mut output = Vec::new();
        for display in &self.displays {
            if let Some(pixel_region) = display.pixel_region(region)? {
                output.push(pixel_region);
            }
        }
        if output.is_empty() {
            return Err(CaptureFailure::InvalidSelection);
        }
        Ok(output)
    }

    pub(crate) fn pixel_region_for_display(
        &self,
        display_id: &DisplayId,
        region: LogicalRect,
    ) -> Result<DisplayPixelRegion, CaptureFailure> {
        let region = region.checked()?;
        self.display(display_id)
            .ok_or(CaptureFailure::InvalidDisplaySnapshot)?
            .pixel_region(region)?
            .ok_or(CaptureFailure::InvalidSelection)
    }
}

fn fingerprint_bytes(fingerprint: &mut u64, bytes: &[u8]) {
    for byte in bytes {
        *fingerprint ^= u64::from(*byte);
        *fingerprint = fingerprint.wrapping_mul(0x0000_0100_0000_01b3);
    }
}

#[derive(Debug, Clone, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct DisplayPixelRegion {
    pub(crate) display_id: DisplayId,
    pub(crate) pixels: PixelRect,
}

#[derive(Debug, Clone, PartialEq, Deserialize, Serialize)]
#[serde(rename_all = "camelCase")]
pub(crate) struct SelectionGeometry {
    pub(crate) snapshot_id: DisplaySnapshotId,
    pub(crate) bounds: LogicalRect,
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, Deserialize, Serialize)]
#[serde(rename_all = "kebab-case")]
pub(crate) enum ResizeHandle {
    North,
    NorthEast,
    East,
    SouthEast,
    South,
    SouthWest,
    West,
    NorthWest,
}

#[derive(Debug, Clone, Copy, PartialEq, Deserialize, Serialize)]
#[serde(
    tag = "kind",
    rename_all = "kebab-case",
    rename_all_fields = "camelCase"
)]
pub(crate) enum SelectionAdjustment {
    Move {
        delta_x: f64,
        delta_y: f64,
    },
    Resize {
        handle: ResizeHandle,
        delta_x: f64,
        delta_y: f64,
    },
}

pub(crate) fn adjust_selection(
    snapshot: &DisplaySnapshot,
    selection: &SelectionGeometry,
    adjustment: SelectionAdjustment,
) -> Result<SelectionGeometry, CaptureFailure> {
    if selection.snapshot_id != snapshot.snapshot_id {
        return Err(CaptureFailure::DisplaySnapshotChanged);
    }
    let desktop = snapshot.desktop_bounds()?;
    let current = selection.bounds.checked()?;
    let adjusted = match adjustment {
        SelectionAdjustment::Move { delta_x, delta_y } => {
            if !delta_x.is_finite() || !delta_y.is_finite() {
                return Err(CaptureFailure::InvalidSelection);
            }
            let max_x = desktop.right() - current.width;
            let max_y = desktop.bottom() - current.height;
            if max_x < desktop.x || max_y < desktop.y {
                return Err(CaptureFailure::InvalidSelection);
            }
            LogicalRect {
                x: (current.x + delta_x).clamp(desktop.x, max_x),
                y: (current.y + delta_y).clamp(desktop.y, max_y),
                ..current
            }
        }
        SelectionAdjustment::Resize {
            handle,
            delta_x,
            delta_y,
        } => resize_selection(current, desktop, handle, delta_x, delta_y)?,
    };
    let adjusted = if snapshot.pixel_regions(adjusted).is_ok() {
        adjusted
    } else {
        snap_to_nearest_display(snapshot, desktop, adjusted)?
    };
    snapshot.pixel_regions(adjusted)?;
    Ok(SelectionGeometry {
        snapshot_id: snapshot.snapshot_id.clone(),
        bounds: adjusted,
    })
}

fn snap_to_nearest_display(
    snapshot: &DisplaySnapshot,
    desktop: LogicalRect,
    selection: LogicalRect,
) -> Result<LogicalRect, CaptureFailure> {
    snapshot
        .displays
        .iter()
        .filter_map(|display| {
            let bounds = display.logical_bounds;
            let (min_x, max_x) = if selection.width <= bounds.width {
                (bounds.x, bounds.right() - selection.width)
            } else {
                (
                    bounds.x - selection.width + MIN_SELECTION_EXTENT,
                    bounds.right() - MIN_SELECTION_EXTENT,
                )
            };
            let (min_y, max_y) = if selection.height <= bounds.height {
                (bounds.y, bounds.bottom() - selection.height)
            } else {
                (
                    bounds.y - selection.height + MIN_SELECTION_EXTENT,
                    bounds.bottom() - MIN_SELECTION_EXTENT,
                )
            };
            let desktop_max_x = desktop.right() - selection.width;
            let desktop_max_y = desktop.bottom() - selection.height;
            let candidate = LogicalRect {
                x: selection
                    .x
                    .clamp(min_x.max(desktop.x), max_x.min(desktop_max_x)),
                y: selection
                    .y
                    .clamp(min_y.max(desktop.y), max_y.min(desktop_max_y)),
                ..selection
            };
            let distance =
                (candidate.x - selection.x).powi(2) + (candidate.y - selection.y).powi(2);
            snapshot
                .pixel_regions(candidate)
                .is_ok()
                .then_some((distance, candidate))
        })
        .min_by(|left, right| left.0.total_cmp(&right.0))
        .map(|(_, selection)| selection)
        .ok_or(CaptureFailure::InvalidSelection)
}

fn resize_selection(
    current: LogicalRect,
    desktop: LogicalRect,
    handle: ResizeHandle,
    delta_x: f64,
    delta_y: f64,
) -> Result<LogicalRect, CaptureFailure> {
    if !delta_x.is_finite() || !delta_y.is_finite() {
        return Err(CaptureFailure::InvalidSelection);
    }
    let mut left = current.x;
    let mut top = current.y;
    let mut right = current.right();
    let mut bottom = current.bottom();
    let moves_left = matches!(
        handle,
        ResizeHandle::NorthWest | ResizeHandle::West | ResizeHandle::SouthWest
    );
    let moves_right = matches!(
        handle,
        ResizeHandle::NorthEast | ResizeHandle::East | ResizeHandle::SouthEast
    );
    let moves_top = matches!(
        handle,
        ResizeHandle::NorthWest | ResizeHandle::North | ResizeHandle::NorthEast
    );
    let moves_bottom = matches!(
        handle,
        ResizeHandle::SouthWest | ResizeHandle::South | ResizeHandle::SouthEast
    );
    if moves_left {
        left = (left + delta_x).clamp(desktop.x, right - MIN_SELECTION_EXTENT);
    }
    if moves_right {
        right = (right + delta_x).clamp(left + MIN_SELECTION_EXTENT, desktop.right());
    }
    if moves_top {
        top = (top + delta_y).clamp(desktop.y, bottom - MIN_SELECTION_EXTENT);
    }
    if moves_bottom {
        bottom = (bottom + delta_y).clamp(top + MIN_SELECTION_EXTENT, desktop.bottom());
    }
    Ok(LogicalRect {
        x: left,
        y: top,
        width: right - left,
        height: bottom - top,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    fn display(
        id: &str,
        bounds: LogicalRect,
        size: PhysicalSize,
        scale: ScaleFactor,
    ) -> DisplayDescriptor {
        DisplayDescriptor {
            id: DisplayId(id.to_owned()),
            logical_bounds: bounds,
            physical_size: size,
            scale,
            primary: id == "primary",
        }
    }

    fn mixed_snapshot() -> DisplaySnapshot {
        DisplaySnapshot::checked(vec![
            display(
                "left",
                LogicalRect {
                    x: -1280.0,
                    y: 0.0,
                    width: 1280.0,
                    height: 1024.0,
                },
                PhysicalSize {
                    width: 1280,
                    height: 1024,
                },
                ScaleFactor {
                    numerator: 1,
                    denominator: 1,
                },
            ),
            display(
                "primary",
                LogicalRect {
                    x: 0.0,
                    y: -100.0,
                    width: 1440.0,
                    height: 900.0,
                },
                PhysicalSize {
                    width: 2880,
                    height: 1800,
                },
                ScaleFactor {
                    numerator: 2,
                    denominator: 1,
                },
            ),
        ])
        .expect("fixture snapshot must be valid")
    }

    #[test]
    fn maps_negative_mixed_dpi_coordinates_to_display_local_pixels() {
        let regions = mixed_snapshot()
            .pixel_regions(LogicalRect {
                x: -100.5,
                y: 0.25,
                width: 201.0,
                height: 99.5,
            })
            .expect("selection must intersect both displays");
        assert_eq!(
            regions,
            vec![
                DisplayPixelRegion {
                    display_id: DisplayId("left".to_owned()),
                    pixels: PixelRect {
                        x: 1179,
                        y: 0,
                        width: 101,
                        height: 100,
                    },
                },
                DisplayPixelRegion {
                    display_id: DisplayId("primary".to_owned()),
                    pixels: PixelRect {
                        x: 0,
                        y: 200,
                        width: 201,
                        height: 200,
                    },
                },
            ]
        );
    }

    #[test]
    fn selection_adjustment_deserializes_camel_case_variant_fields() {
        assert_eq!(
            serde_json::from_value::<SelectionAdjustment>(
                serde_json::json!({"kind": "move", "deltaX": 2.5, "deltaY": -1.0})
            )
            .expect("camel-case move adjustment must deserialize"),
            SelectionAdjustment::Move {
                delta_x: 2.5,
                delta_y: -1.0,
            }
        );
        assert_eq!(
            serde_json::from_value::<SelectionAdjustment>(serde_json::json!({
                "kind": "resize",
                "handle": "south-east",
                "deltaX": 3.0,
                "deltaY": 4.0
            }))
            .expect("camel-case resize adjustment must deserialize"),
            SelectionAdjustment::Resize {
                handle: ResizeHandle::SouthEast,
                delta_x: 3.0,
                delta_y: 4.0,
            }
        );
    }

    #[test]
    fn snapshot_fingerprint_is_order_independent_and_changes_on_hot_plug() {
        let snapshot = mixed_snapshot();
        let reversed = DisplaySnapshot::checked(snapshot.displays.iter().cloned().rev().collect())
            .expect("reordered snapshot must be valid");
        assert_eq!(snapshot.snapshot_id, reversed.snapshot_id);
        let unplugged = DisplaySnapshot::checked(vec![snapshot.displays[0].clone()])
            .expect("one display is valid");
        assert_ne!(snapshot.snapshot_id, unplugged.snapshot_id);
    }

    #[test]
    fn move_and_resize_are_clamped_and_keep_snapshot_identity() {
        let snapshot = mixed_snapshot();
        let selection = SelectionGeometry {
            snapshot_id: snapshot.snapshot_id.clone(),
            bounds: LogicalRect {
                x: -100.0,
                y: 0.0,
                width: 200.0,
                height: 100.0,
            },
        };
        let moved = adjust_selection(
            &snapshot,
            &selection,
            SelectionAdjustment::Move {
                delta_x: -10_000.0,
                delta_y: -10_000.0,
            },
        )
        .expect("move must clamp");
        assert_eq!(moved.bounds.x, -1280.0);
        assert_eq!(moved.bounds.y, 0.0);

        let resized = adjust_selection(
            &snapshot,
            &selection,
            SelectionAdjustment::Resize {
                handle: ResizeHandle::SouthEast,
                delta_x: 10_000.0,
                delta_y: 10_000.0,
            },
        )
        .expect("resize must clamp");
        assert_eq!(resized.bounds.right(), 1440.0);
        assert_eq!(resized.bounds.bottom(), 1024.0);
    }

    #[test]
    fn stale_selection_is_rejected_after_hot_plug() {
        let snapshot = mixed_snapshot();
        let unplugged = DisplaySnapshot::checked(vec![snapshot.displays[0].clone()])
            .expect("one display is valid");
        let selection = SelectionGeometry {
            snapshot_id: snapshot.snapshot_id,
            bounds: snapshot.displays[0].logical_bounds,
        };
        assert_eq!(
            adjust_selection(
                &unplugged,
                &selection,
                SelectionAdjustment::Move {
                    delta_x: 1.0,
                    delta_y: 1.0,
                },
            ),
            Err(CaptureFailure::DisplaySnapshotChanged)
        );
    }
}
