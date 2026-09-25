//! Authored box measurements, distinct from Office application pagination.
use std::collections::BTreeMap;

use serde::Serialize;
use uuid::Uuid;

#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum CoordinateSpace {
    WordFlow,
    Worksheet,
    MountedRegion,
}
#[derive(Debug, Clone, Serialize)]
pub struct Frame {
    pub x: f64,
    pub y: f64,
    pub width: f64,
    pub height: f64,
    pub coordinate_space: CoordinateSpace,
}
#[derive(Debug, Default, Serialize)]
pub struct Geometry {
    pub nodes: BTreeMap<Uuid, Placed>,
}
#[derive(Debug, Serialize)]
pub struct Placed {
    pub frame: Frame,
}
impl Geometry {
    pub fn insert(&mut self, id: Uuid, frame: Frame) {
        self.nodes.insert(id, Placed { frame });
    }
}
