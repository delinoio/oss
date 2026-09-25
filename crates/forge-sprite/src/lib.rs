//! Deterministic, bounded pixel sprites. React and filesystem publication stay
//! outside this engine; all returned files belong to one immutable revision.
#![forbid(unsafe_code)]

mod raster;
use std::{
    collections::{BTreeMap, BTreeSet},
    io::Cursor,
};

use forge_tree_doc::{Diagnostic, ErrorCode, Result, cancellation::checkpoint};
use image::{ImageReader, RgbaImage};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use uuid::Uuid;

pub const MAX_PIXELS: u64 = 64_000_000;
pub const MAX_WORK: u64 = 256_000_000;
pub const MAX_FRAMES: usize = 1024;
pub type Assets = BTreeMap<String, Vec<u8>>;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Project {
    pub id: Uuid,
    pub width: u32,
    pub height: u32,
    pub scale: u32,
    pub columns: Option<u32>,
    pub padding: u32,
    pub palette: BTreeMap<String, String>,
    pub animations: Vec<Animation>,
}
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Animation {
    pub id: Uuid,
    pub name: String,
    pub r#loop: bool,
    pub frames: Vec<Frame>,
}
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Frame {
    pub id: Uuid,
    pub duration_ms: u32,
    pub pivot: Option<Pivot>,
    pub children: Vec<Node>,
}
#[derive(Debug, Clone, Copy, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Pivot {
    pub x: f64,
    pub y: f64,
}
// Keep each drawing node flat: a wrapper per layer would exceed JSON's
// recursion ceiling before the shared 48-level React tree limit is reached.
#[derive(Debug, Deserialize)]
#[serde(tag = "type", rename_all = "snake_case", deny_unknown_fields)]
pub enum Node {
    Layer {
        id: Uuid,
        x: i32,
        y: i32,
        visible: bool,
        children: Vec<Node>,
    },
    Rect {
        id: Uuid,
        x: i32,
        y: i32,
        width: u32,
        height: u32,
        fill: String,
    },
    Ellipse {
        id: Uuid,
        x: i32,
        y: i32,
        width: u32,
        height: u32,
        fill: String,
    },
    PixelGrid {
        id: Uuid,
        x: i32,
        y: i32,
        rows: Vec<String>,
    },
    Image {
        id: Uuid,
        x: i32,
        y: i32,
        asset: String,
        width: u32,
        height: u32,
        source: Option<Crop>,
        flip_x: bool,
        flip_y: bool,
    },
}
impl Node {
    fn identity(&self) -> (Uuid, i32, i32) {
        match self {
            Self::Layer { id, x, y, .. }
            | Self::Rect { id, x, y, .. }
            | Self::Ellipse { id, x, y, .. }
            | Self::PixelGrid { id, x, y, .. }
            | Self::Image { id, x, y, .. } => (*id, *x, *y),
        }
    }
}
#[derive(Debug, Clone, Copy, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Crop {
    pub x: u32,
    pub y: u32,
    pub width: u32,
    pub height: u32,
}

fn invalid(location: &str) -> Diagnostic {
    Diagnostic::new(ErrorCode::InvalidField, location, "Invalid sprite model")
}
fn limit() -> Diagnostic {
    Diagnostic::new(
        ErrorCode::ResourceLimit,
        "sprite",
        "Sprite resource limit exceeded",
    )
}
fn dimension(width: u32, height: u32) -> Result<()> {
    if width == 0 || height == 0 {
        return Err(invalid("width"));
    }
    if width > 4096 || height > 4096 {
        return Err(limit());
    }
    Ok(())
}
fn color(value: &str) -> Result<[u8; 4]> {
    let bytes = value.as_bytes();
    if ![7, 9].contains(&bytes.len())
        || bytes[0] != b'#'
        || !bytes[1..].iter().all(u8::is_ascii_hexdigit)
    {
        return Err(invalid("color"));
    }
    let mut rgba = [0, 0, 0, 255];
    for (index, channel) in rgba.iter_mut().enumerate().take((bytes.len() - 1) / 2) {
        *channel = u8::from_str_radix(&value[1 + index * 2..3 + index * 2], 16)
            .map_err(|_| invalid("color"))?;
    }
    Ok(rgba)
}
struct Validation {
    ids: BTreeSet<Uuid>,
    image_ids: BTreeSet<String>,
    palette: BTreeMap<u8, [u8; 4]>,
}
impl Validation {
    fn id(&mut self, id: Uuid) -> Result<()> {
        checkpoint()?;
        if id.get_version_num() != 7 || !self.ids.insert(id) {
            return Err(invalid("id"));
        }
        if self.ids.len() > 20_000 {
            return Err(limit());
        }
        Ok(())
    }

    fn nodes(&mut self, nodes: &[Node], depth: usize) -> Result<()> {
        if !nodes.is_empty() && depth > 48 {
            return Err(limit());
        }
        for node in nodes {
            self.id(node.identity().0)?;
            match node {
                Node::Layer { children, .. } => self.nodes(children, depth + 1)?,
                Node::Rect {
                    width,
                    height,
                    fill,
                    ..
                }
                | Node::Ellipse {
                    width,
                    height,
                    fill,
                    ..
                } => {
                    dimension(*width, *height)?;
                    color(fill)?;
                }
                Node::PixelGrid { rows, .. } => {
                    if rows.is_empty() || rows.len() > 4096 {
                        return Err(invalid("rows"));
                    }
                    let width = rows[0].len();
                    if width == 0 || width > 4096 {
                        return Err(invalid("rows"));
                    }
                    for row in rows {
                        checkpoint()?;
                        if row.len() != width
                            || !row
                                .bytes()
                                .all(|b| b == b'.' || self.palette.contains_key(&b))
                        {
                            return Err(invalid("rows"));
                        }
                    }
                }
                Node::Image {
                    asset,
                    width,
                    height,
                    source,
                    ..
                } => {
                    dimension(*width, *height)?;
                    if let Some(crop) = source
                        && (crop.width == 0 || crop.height == 0)
                    {
                        return Err(invalid("source"));
                    }
                    self.image_ids.insert(asset.clone());
                }
            }
        }
        Ok(())
    }
}
struct Prepared {
    palette: BTreeMap<u8, [u8; 4]>,
    images: BTreeMap<String, RgbaImage>,
    columns: u32,
    sheet_width: u32,
    sheet_height: u32,
}
fn prepare(project: &Project, assets: &Assets) -> Result<Prepared> {
    checkpoint()?;
    dimension(project.width, project.height)?;
    if !(1..=16).contains(&project.scale) || project.padding > 64 {
        return Err(invalid("scale"));
    }
    let count: usize = project.animations.iter().map(|a| a.frames.len()).sum();
    if count == 0 {
        return Err(invalid("frames"));
    }
    if count > MAX_FRAMES {
        return Err(limit());
    }
    let columns = project
        .columns
        .unwrap_or((count as f64).sqrt().ceil() as u32);
    if columns == 0 || columns > count as u32 {
        return Err(invalid("columns"));
    }
    let width = project.width * project.scale;
    let height = project.height * project.scale;
    let sheet_width = columns * (width + project.padding * 2);
    let sheet_height = (count as u32).div_ceil(columns) * (height + project.padding * 2);
    if u64::from(sheet_width) * u64::from(sheet_height) > MAX_PIXELS
        || u64::from(width) * u64::from(height) * count as u64 > MAX_PIXELS
    {
        return Err(limit());
    }
    let mut v = Validation {
        ids: BTreeSet::new(),
        image_ids: BTreeSet::new(),
        palette: BTreeMap::new(),
    };
    v.id(project.id)?;
    for (key, value) in &project.palette {
        if key.len() != 1 || !(b'!'..=b'~').contains(&key.as_bytes()[0]) || key == "." {
            return Err(invalid("palette"));
        }
        v.palette.insert(key.as_bytes()[0], color(value)?);
    }
    let mut names = BTreeSet::new();
    for animation in &project.animations {
        v.id(animation.id)?;
        if animation.name.is_empty()
            || animation.name.len() > 64
            || !animation
                .name
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
            || !names.insert(&animation.name)
            || animation.frames.is_empty()
        {
            return Err(invalid("animations"));
        }
        for frame in &animation.frames {
            v.id(frame.id)?;
            if !(1..=60_000).contains(&frame.duration_ms) {
                return Err(invalid("duration_ms"));
            }
            if let Some(p) = frame.pivot
                && (!p.x.is_finite()
                    || !p.y.is_finite()
                    || p.x < 0.0
                    || p.y < 0.0
                    || p.x > f64::from(project.width)
                    || p.y > f64::from(project.height))
            {
                return Err(invalid("pivot"));
            }
            v.nodes(&frame.children, 4)?;
        }
    }
    let mut images = BTreeMap::new();
    let mut pixels = 0;
    let mut bytes_total = 0;
    // Validate every referenced asset, even when its layer is hidden. Decode
    // each unique image once and bound aggregate RGBA allocation before decode.
    for id in v.image_ids {
        checkpoint()?;
        let bytes = assets.get(&id).ok_or_else(|| {
            Diagnostic::new(
                ErrorCode::InvalidReference,
                "asset",
                "Sprite image is not registered",
            )
        })?;
        bytes_total += bytes.len();
        if bytes.len() > forge_package::MAX_PART_BYTES
            || bytes_total > forge_package::MAX_PACKAGE_BYTES
        {
            return Err(limit());
        }
        let format = image::guess_format(bytes).map_err(|_| invalid("image"))?;
        if !matches!(format, image::ImageFormat::Png | image::ImageFormat::Jpeg) {
            return Err(invalid("image"));
        }
        let reader = ImageReader::with_format(Cursor::new(bytes), format);
        let (w, h) = reader.into_dimensions().map_err(|_| invalid("image"))?;
        pixels += u64::from(w) * u64::from(h);
        if w == 0 || h == 0 || pixels > MAX_PIXELS {
            return Err(limit());
        }
        let mut reader = ImageReader::with_format(Cursor::new(bytes), format);
        let mut limits = image::Limits::default();
        limits.max_alloc = Some(MAX_PIXELS * 4);
        reader.limits(limits);
        images.insert(
            id,
            reader.decode().map_err(|_| invalid("image"))?.into_rgba8(),
        );
    }
    Ok(Prepared {
        palette: v.palette,
        images,
        columns,
        sheet_width,
        sheet_height,
    })
}

/// Return one deterministic archive and logical-pixel geometry for this model.
pub fn generate(project: &Project, assets: &Assets) -> Result<(Vec<u8>, Value)> {
    let prepared = prepare(project, assets)?;
    let mut raster = raster::Raster::new(project, &prepared);
    let mut sheet = RgbaImage::new(prepared.sheet_width, prepared.sheet_height);
    let mut parts = forge_package::Package::new();
    let mut frames = Vec::new();
    let mut tags = Vec::new();
    let mut animations = Vec::new();
    let mut part_bytes = 0;
    for animation in &project.animations {
        let from = frames.len();
        for frame in &animation.frames {
            checkpoint()?;
            let index = frames.len() as u32;
            let bitmap = raster.frame(frame, index)?;
            let x = (index % prepared.columns) * (bitmap.width() + project.padding * 2)
                + project.padding;
            let y = (index / prepared.columns) * (bitmap.height() + project.padding * 2)
                + project.padding;
            for row in 0..bitmap.height() {
                checkpoint()?;
                for column in 0..bitmap.width() {
                    sheet.put_pixel(x + column, y + row, *bitmap.get_pixel(column, row));
                }
            }
            let path = format!("frames/{index:04}.png");
            add_png(&mut parts, &path, &bitmap, &mut part_bytes)?;
            let pivot = frame.pivot.unwrap_or(Pivot {
                x: f64::from(project.width) / 2.0,
                y: f64::from(project.height),
            });
            frames.push(json!({"filename":path,"frame":{"x":x,"y":y,"w":bitmap.width(),"h":bitmap.height()},"rotated":false,"trimmed":false,"spriteSourceSize":{"x":0,"y":0,"w":bitmap.width(),"h":bitmap.height()},"sourceSize":{"w":bitmap.width(),"h":bitmap.height()},"duration":frame.duration_ms,"pivot":{"x":pivot.x*f64::from(project.scale),"y":pivot.y*f64::from(project.scale)}}));
        }
        let to = frames.len() - 1;
        tags.push(json!({"name":animation.name,"from":from,"to":to,"direction":"forward"}));
        animations.push(json!({"name":animation.name,"from":from,"to":to,"loop":animation.r#loop}));
    }
    add_png(&mut parts, "sheet.png", &sheet, &mut part_bytes)?;
    let metadata = json!({"frames":frames,"meta":{"app":"React Forge","image":"sheet.png","format":"RGBA8888","size":{"w":sheet.width(),"h":sheet.height()},"scale":project.scale.to_string(),"frameTags":tags,"reactForge":{"version":1,"animations":animations}}});
    parts.insert(
        "sprite.json".into(),
        serde_json::to_vec_pretty(&metadata).map_err(|_| invalid("frames"))?,
    );
    checkpoint()?;
    Ok((
        forge_package::write(&parts)?,
        json!({"nodes":raster.geometry}),
    ))
}
fn add_png(
    parts: &mut forge_package::Package,
    path: &str,
    image: &RgbaImage,
    total: &mut usize,
) -> Result<()> {
    use image::ImageEncoder;
    checkpoint()?;
    let mut bytes = Vec::new();
    image::codecs::png::PngEncoder::new(&mut bytes)
        .write_image(
            image.as_raw(),
            image.width(),
            image.height(),
            image::ExtendedColorType::Rgba8,
        )
        .map_err(|_| invalid("image"))?;
    checkpoint()?;
    *total += bytes.len();
    if bytes.len() > forge_package::MAX_PART_BYTES || *total > forge_package::MAX_EXPANDED_BYTES {
        return Err(limit());
    }
    parts.insert(path.into(), bytes);
    Ok(())
}

#[cfg(test)]
mod tests;
