use image::Rgba;

use super::*;

pub(super) struct Raster<'a> {
    project: &'a Project,
    prepared: &'a Prepared,
    work: u64,
    pub geometry: BTreeMap<String, Value>,
}
impl<'a> Raster<'a> {
    pub fn new(project: &'a Project, prepared: &'a Prepared) -> Self {
        Self {
            project,
            prepared,
            work: 0,
            geometry: BTreeMap::new(),
        }
    }

    fn charge(&mut self, pixels: u64) -> Result<()> {
        self.work = self.work.checked_add(pixels).ok_or_else(limit)?;
        if self.work > MAX_WORK {
            return Err(limit());
        }
        checkpoint()
    }

    fn geometry(&mut self, id: Uuid, x: i64, y: i64, width: u32, height: u32, page: u32) {
        self.geometry.insert(id.to_string(), json!({"frame":{"coordinate_space":"sprite_frame","x":x,"y":y,"width":width,"height":height,"page":page}}));
    }

    pub fn frame(&mut self, frame: &Frame, page: u32) -> Result<RgbaImage> {
        let p = self.project;
        self.charge(u64::from(p.width) * u64::from(p.height) * u64::from(p.scale).pow(2))?;
        let mut canvas = RgbaImage::new(p.width, p.height);
        self.geometry(frame.id, 0, 0, p.width, p.height, page);
        self.nodes(&frame.children, &mut canvas, (0, 0), page, true)?;
        if p.scale == 1 {
            return Ok(canvas);
        }
        let mut scaled = RgbaImage::new(p.width * p.scale, p.height * p.scale);
        for y in 0..scaled.height() {
            checkpoint()?;
            for x in 0..scaled.width() {
                scaled.put_pixel(x, y, *canvas.get_pixel(x / p.scale, y / p.scale));
            }
        }
        Ok(scaled)
    }

    fn nodes(
        &mut self,
        nodes: &[Node],
        canvas: &mut RgbaImage,
        offset: (i64, i64),
        page: u32,
        visible: bool,
    ) -> Result<()> {
        for node in nodes {
            checkpoint()?;
            let (id, nx, ny) = node.identity();
            let x = offset.0 + i64::from(nx);
            let y = offset.1 + i64::from(ny);
            if let Node::Layer {
                visible: layer_visible,
                children,
                ..
            } = node
            {
                // Hidden nodes still traverse validation, including image crops.
                self.nodes(children, canvas, (x, y), page, visible && *layer_visible)?;
                continue;
            }
            let (width, height) = match node {
                Node::Rect { width, height, .. }
                | Node::Ellipse { width, height, .. }
                | Node::Image { width, height, .. } => (*width, *height),
                Node::PixelGrid { rows, .. } => (rows[0].len() as u32, rows.len() as u32),
                Node::Layer { .. } => unreachable!(),
            };
            let fill = match node {
                Node::Rect { fill, .. } | Node::Ellipse { fill, .. } => color(fill)?,
                _ => [0; 4],
            };
            let image = if let Node::Image { asset, source, .. } = node {
                let image = &self.prepared.images[asset];
                let crop = source.unwrap_or(Crop {
                    x: 0,
                    y: 0,
                    width: image.width(),
                    height: image.height(),
                });
                if u64::from(crop.x) + u64::from(crop.width) > u64::from(image.width())
                    || u64::from(crop.y) + u64::from(crop.height) > u64::from(image.height())
                {
                    return Err(invalid("source"));
                }
                Some((image, crop))
            } else {
                None
            };
            self.geometry(id, x, y, width, height, page);
            if !visible {
                continue;
            }
            let left = x.clamp(0, i64::from(canvas.width()));
            let top = y.clamp(0, i64::from(canvas.height()));
            let right = (x + i64::from(width)).clamp(0, i64::from(canvas.width()));
            let bottom = (y + i64::from(height)).clamp(0, i64::from(canvas.height()));
            self.charge(((right - left) * (bottom - top)) as u64)?;
            for cy in top..bottom {
                checkpoint()?;
                for cx in left..right {
                    let sx = (cx - x) as u32;
                    let sy = (cy - y) as u32;
                    let rgba = match node {
                        Node::Rect { .. } => fill,
                        Node::Ellipse { .. } => {
                            // Evaluate the ellipse at pixel centers with integer
                            // arithmetic so all hosts produce identical edges.
                            let dx = i64::from(2 * sx + 1) - i64::from(width);
                            let dy = i64::from(2 * sy + 1) - i64::from(height);
                            let w2 = i64::from(width).pow(2);
                            let h2 = i64::from(height).pow(2);
                            if dx * dx * h2 + dy * dy * w2 > w2 * h2 {
                                continue;
                            }
                            fill
                        }
                        Node::PixelGrid { rows, .. } => {
                            let key = rows[sy as usize].as_bytes()[sx as usize];
                            if key == b'.' {
                                continue;
                            }
                            self.prepared.palette[&key]
                        }
                        Node::Image { flip_x, flip_y, .. } => {
                            let (image, crop) = image.expect("validated image");
                            let ix =
                                (u64::from(sx) * u64::from(crop.width) / u64::from(width)) as u32;
                            let iy =
                                (u64::from(sy) * u64::from(crop.height) / u64::from(height)) as u32;
                            image
                                .get_pixel(
                                    crop.x + if *flip_x { crop.width - 1 - ix } else { ix },
                                    crop.y + if *flip_y { crop.height - 1 - iy } else { iy },
                                )
                                .0
                        }
                        Node::Layer { .. } => unreachable!(),
                    };
                    blend(canvas.get_pixel_mut(cx as u32, cy as u32), rgba);
                }
            }
        }
        Ok(())
    }
}
fn blend(destination: &mut Rgba<u8>, source: [u8; 4]) {
    let sa = u32::from(source[3]);
    if sa == 0 {
        return;
    }
    let da = u32::from(destination[3]);
    let alpha = sa * 255 + da * (255 - sa);
    for c in 0..3 {
        let numerator =
            u32::from(source[c]) * sa * 255 + u32::from(destination[c]) * da * (255 - sa);
        destination[c] = ((numerator + alpha / 2) / alpha) as u8;
    }
    destination[3] = ((alpha + 127) / 255) as u8;
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn work_budget_accepts_boundary_and_rejects_excess() {
        let p = Project {
            id: Uuid::now_v7(),
            width: 1,
            height: 1,
            scale: 1,
            columns: None,
            padding: 0,
            palette: BTreeMap::new(),
            animations: vec![],
        };
        let prepared = Prepared {
            palette: BTreeMap::new(),
            images: BTreeMap::new(),
            columns: 1,
            sheet_width: 1,
            sheet_height: 1,
        };
        let mut raster = Raster::new(&p, &prepared);
        raster.charge(MAX_WORK).unwrap();
        assert_eq!(raster.charge(1).unwrap_err().code, ErrorCode::ResourceLimit);
    }
}
